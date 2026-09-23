// Package aec provides acoustic echo cancellation for the mic pipeline
// using the speexdsp echo canceller (MDF/AUMDF), vendored from
// https://github.com/xiph/speexdsp tag SpeexDSP-1.2.1 (libspeexdsp/, BSD).
//
// The canceller consumes two streams: the near-end mic signal (16kHz mono
// S16, 512-sample periods — the beamformer's output, pre-NS/AGC) and a
// far-end reference of what the speaker is playing. The reference is tapped
// at the ALSA write in the speaker silence loop (48kHz stereo S16, every
// period *including silence*, so the reference clock advances in lockstep
// with playback), downmixed and 3:1 box-decimated to 16kHz mono here, and
// buffered in a ring the mic goroutine drains one period at a time.
//
// Alignment: both PCM devices sit on the same codec clock, so the streams
// cannot drift — but mic capture overruns (the mic ALSA ring is only 160ms
// deep; any longer stall of the reader loses whole batches) leave the ring
// with excess reference, which the occupancy governor in Process trims
// back to the nominal delay. The delay models write-to-ear latency (ALSA output
// buffering ~340ms at 4×2048 frames / 48kHz, minus input-side buffering);
// the echo filter tail only has to absorb the residual mismatch plus room
// reverb, not the whole pipeline latency.
package aec

/*
#cgo CFLAGS: -I${SRCDIR}/include -I${SRCDIR}/src -DFLOATING_POINT -DUSE_KISS_FFT -DEXPORT= -O2
#cgo LDFLAGS: -lm

#include <stdlib.h>
#include "speex/speex_echo.h"
#include "src/fftwrap.c"
#include "src/kiss_fft.c"
#include "src/kiss_fftr.c"
#include "src/mdf.c"
*/
import "C"

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"math"
	"sync"
	"time"
	"unsafe"
)

const (
	sampleRate = 16000
	// FrameSize matches the mic pipeline period (512 samples = 32ms).
	FrameSize = 512

	// Reference ring capacity: max bulk delay (1s) plus 2s of slack.
	// 48k samples of int16 = 96KB.
	ringCap = 3 * sampleRate

	// Parameter clamps.
	maxDelayMs = 1000
	minTailMs  = 50
	maxTailMs  = 500

	// hwTailMs is the filter length on the hardware reference, where the
	// tail covers only the acoustic path: the reference arrives in the same
	// frame as the mics, so there is no delay error left to absorb. Measured
	// on a bench Dot (2026-09-22): converged, a 16ms tail cancelled as
	// deeply as 300ms (20.3dB vs 20.4dB), while 300ms took ~10s of playback
	// to converge against ~4s for 64ms. 64 leaves 4x headroom for a livelier
	// room. aecTailMs still sets the software tap's length, which must also
	// cover the delay error between the speaker write and the mic batches.
	hwTailMs = 64

	// One learned acoustic path per real microphone. Only the selected state
	// is processed, so CPU remains one canceller; the extra cost is filter
	// memory. Switching from centre to a perimeter mic no longer asks a filter
	// trained on a different physical path to cancel the first word.
	aecPaths      = 7
	defaultPathID = 6 // centre microphone
)

// Canceller owns the retained per-microphone AEC state bank shared by the
// speaker goroutine (WriteFar) and the mic goroutine (Process). Only the
// selected state runs. One mutex guards everything — both call sites run at
// tens of hertz on multi-millisecond periods, so contention is irrelevant
// next to correctness.
type Canceller struct {
	mu      sync.Mutex
	enabled bool
	delayMs int
	tailMs  int // configured (aecTailMs): the software tap's filter length

	// st aliases states[activePath] for the processing hot path and for the
	// existing state import/export contract.
	st         *C.SpeexEchoState
	states     [aecPaths]*C.SpeexEchoState
	activePath int
	stTailMs   int // the length st was built with: hwTailMs or tailMs

	statePath   string              // saved echo path (persist.go); empty = off
	lastSaveTry [aecPaths]time.Time // monotonic, rate-limits maybeSaveLocked

	// Far-end reference ring (16kHz mono), plus the 3:1 decimator carry.
	ring  [ringCap]int16
	head  int // next write index
	tail  int // next read index
	count int // samples buffered
	dsum  int32
	dcnt  int

	// C-side scratch buffers, allocated once per state init.
	micBuf *C.spx_int16_t
	refBuf *C.spx_int16_t
	outBuf *C.spx_int16_t

	underruns uint64 // ref ring empty while enabled (diagnostic)
	resyncs   uint64 // stale-reference trims (see governor in Process)

	// Attenuation telemetry (2026-07-08): live cancellation has measured
	// ≈0dB across every delay setting while the synthetic test shows 42dB —
	// log the actual numbers instead of inferring them controller-side.
	// Accumulated per Process call, reported ~1/s while the reference is
	// active (i.e. during playback), then reset.
	statFrames               int
	statInSum                float64    // Σ mic-frame rms (pre-AEC)
	statOutSum               float64    // Σ output-frame rms (post-AEC)
	statRefSum               float64    // Σ reference-frame rms
	statInBand               [3]float64 // Σ band energy: <500Hz, 500–3000Hz, >3000Hz
	statOutBand              [3]float64
	doubleTalkFrames         uint64
	residualSuppressedFrames uint64
	residualGain             [aecPaths]float64

	// Far-end telemetry: what WriteFar actually receives and pushes,
	// counted in pushed (16kHz) samples. Logged ~1/s while the far end is
	// loud — pairing this with the Process-side line tells whether a dead
	// reference is a tap problem (no loud far lines during playback) or a
	// ring/consumer problem (loud far lines, quiet ref in Process).
	farSamples int
	farSumSq   float64

	// DC mean and absolute peak over the same window.
	//
	// rms alone cannot tell audio from a constant offset — both read high —
	// and that ambiguity cost an evening on #117/#141, where the device was
	// writing rms≈4000 to the codec while an external speaker on the jack
	// stayed silent. A DC offset has rms with no audible content AND trips
	// the protection circuit in a powered speaker, which would explain the
	// silence and why it also swallows voice. mean≈±rms with a small
	// peak-to-peak says offset; mean≈0 with peak well above rms says real
	// audio, and the fault is downstream of us.
	farSum  float64
	farPeak int32

	sizeWarned bool // one-shot guard for the unsupported-buffer-size log

	// Hardware far-end reference (#385). When set, the reference comes from
	// ch8 of the mic capture — the device's own playback, looped back in the
	// SAME TDM frame as the near-end samples — and the ring, the decimator,
	// aecDelayMs and the occupancy governor are all bypassed, because every
	// one of them exists to answer "where in time is the far end", which
	// arriving in the same frame answers by construction.
	//
	// Owned here rather than decided at the call site so WriteFar and
	// ProcessWithRef cannot disagree about which source is live: a period
	// pushed into the ring while the hardware path is running would be
	// consumed by nothing and sit there aging.
	hwRef bool
	// Periods cancelled against a hardware reference, and periods where one
	// was expected and did not arrive (nil, or a length mismatch with the
	// near end). A rising hwMissing with cancellation on is an extraction
	// fault, not a filter one, and the attenuation line alone cannot tell
	// them apart — those frames pass through uncancelled and simply read as
	// att≈0dB.
	//
	// Deliberately NOT a silence counter. A reference that is present and
	// all-zero is the correct state whenever nothing is playing, so counting
	// it would be counting idle; the case worth catching — zero WHILE the
	// speaker plays — is already visible as ref=0 against a loud mic on the
	// once-a-second attenuation line.
	hwFrames  uint64
	hwMissing uint64

	// Playback gain the hardware reference has NOT been through, as a linear
	// scalar (1.0 = the codec's unity gain, index 127). Measured on hardware:
	// the loopback is tapped upstream of the DAC volume control, so it holds
	// full scale whatever the user's volume — 0.431574 at index 127 against
	// 0.431560 at index 60, across a commanded 33.5dB cut.
	//
	// Left uncorrected, every volume change is a step in the echo path gain
	// that the adaptive filter can only discover by re-converging, and the
	// field log shows exactly that: cancellation collapsed to -1.7dB
	// immediately after a change and took 3-4 seconds to climb back, over
	// and over (2026-08-29). We are not obliged to guess it — the device
	// SETS this volume, so it knows the scalar precisely.
	refScale    float64
	scaleWarned bool // one-shot guard for the unscaled-reference log
}

// SetPlaybackLevel tells the canceller the DAC volume index the reference has
// not been through, so the hardware reference can be scaled to match what the
// speaker is actually emitting.
//
// The control is 0.5dB per step with unity at 127 (see Volume in
// device/CLAUDE.md), so the scalar is 10^((level-127)/40).
//
// SOFTWARE-TAP FRAMES ARE DELIBERATELY LEFT ALONE. That tap is pre-volume
// too, but its ring holds audio written BEFORE the change, so scaling it by
// the current level would apply the correction to the wrong samples — and
// keeping it untouched preserves the baseline the hardware path is being
// compared against.
func (c *Canceller) SetPlaybackLevel(level int) {
	if level < 0 {
		level = 0
	}
	scale := math.Pow(10, float64(level-127)/40.0)
	c.mu.Lock()
	defer c.mu.Unlock()
	if scale == c.refScale {
		return
	}
	c.refScale = scale
	log.Printf("[aec] playback level %d → reference scale %.4f (%.1fdB)",
		level, scale, 20*math.Log10(math.Max(scale, 1e-9)))
}

// SetHardwareRef selects the far-end source. True takes it from ch8 of the
// mic capture; false uses the software tap at the speaker ALSA write.
//
// Switching drops any ring contents: on the way in they would never be
// consumed, and on the way out they are stale by however long the hardware
// path ran. The filter is kept only when both paths want the same length;
// normally they do not (hwTailMs), and it is rebuilt.
func (c *Canceller) SetHardwareRef(on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hwRef == on {
		return
	}
	c.hwRef = on
	c.head, c.tail, c.count = 0, 0, 0
	c.dsum, c.dcnt = 0, 0
	log.Printf("[aec] far-end reference: %s",
		map[bool]string{true: "hardware (ch8, frame-aligned)",
			false: "software tap (ring + aecDelayMs)"}[on])
	// The two paths want different filter lengths (hwTailMs). A rebuild
	// discards what was learnt, which on the way in is almost nothing: the
	// hardware reference is confirmed by the first playback after boot.
	if c.st != nil && c.effectiveTailLocked() != c.stTailMs {
		c.buildLocked()
		if !on {
			c.seedRingLocked() // nothing drains the ring on the hardware path
		}
		c.loadStateLocked()
	}
}

// effectiveTailLocked is the filter length for the reference in use.
func (c *Canceller) effectiveTailLocked() int {
	if c.hwRef {
		return hwTailMs
	}
	return c.tailMs
}

// Enabled reports whether cancellation is armed. Callers use it to skip
// preparing a far-end reference that would be discarded.
//
// Note this is NOT the rare case: aecEnabled defaults TRUE controller-side
// (em_db.DEFAULT_CONFIG), so a stock device runs the extraction on every
// period. It is one allocation and a 512-sample copy per 32ms on the mic
// goroutine, which is affordable — but it is the common path, not the
// exception, so treat it as part of the steady-state budget.
func (c *Canceller) Enabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enabled
}

// HardwareRef reports the current far-end source.
func (c *Canceller) HardwareRef() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hwRef
}

// RefSource names the far-end reference in use, for the stats report:
// "hw" once ch8 has proved itself the playback loopback, "sw" for the tap at
// the ALSA write, "off" while cancellation is disarmed.
//
// Always one of those three, never empty: the controller reads an ABSENT
// aecRef as "firmware too old to say", and an empty string arriving as a
// fourth value would collapse that distinction.
func (c *Canceller) RefSource() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case !c.enabled:
		return "off"
	case c.hwRef:
		return "hw"
	default:
		return "sw"
	}
}

// New returns a disabled Canceller. Call SetParams (config push) to arm it.
func New() *Canceller {
	c := &Canceller{activePath: defaultPathID}
	for i := range c.residualGain {
		c.residualGain[i] = 1
	}
	return c
}

// SelectPath chooses the learned echo path for the physical microphone that
// produced the current mono buffer. States are retained across turns; only one
// is processed at a time. Out-of-range ids fall back to the centre mic.
func (c *Canceller) SelectPath(id int) {
	if id < 0 || id >= aecPaths {
		id = defaultPathID
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if id == c.activePath {
		return
	}
	old := c.activePath
	c.activePath = id
	if c.states[id] == nil {
		return
	}
	// The coefficients belong to this microphone and remain useful; the
	// signal history belongs to the last time this state ran and may contain
	// old reply audio. Clear only that history before the state becomes live.
	C.em_echo_state_clear_history(c.states[id])
	c.st = c.states[id]
	c.resetStatsLocked()
	log.Printf("[aec] acoustic path ch%d → ch%d", old, id)
}

// SetParams applies config. On the software tap, any change to delay or tail
// rebuilds the echo state and re-seeds the ring — adaptive filter state is
// worthless across a timing change anyway. Called from the control goroutine
// on config push.
func (c *Canceller) SetParams(enabled bool, delayMs, tailMs int) {
	if delayMs < 0 {
		delayMs = 0
	}
	if delayMs > maxDelayMs {
		delayMs = maxDelayMs
	}
	if tailMs < minTailMs {
		tailMs = minTailMs
	}
	if tailMs > maxTailMs {
		tailMs = maxTailMs
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if enabled == c.enabled && delayMs == c.delayMs && tailMs == c.tailMs {
		return
	}
	// Delay and tail changes are no-ops on the hardware reference. delayMs
	// only seeds the ring, and aecTailMs sets the software tap's length
	// (hwTailMs is used here), so a rebuild would discard a converged filter
	// to apply numbers nothing reads. That is not hypothetical: both ride
	// every config push, so one fleet-wide edit would reset cancellation on
	// every device using the hardware reference.
	//
	// Stored anyway, so a later fall back to the software tap uses the
	// operator's current settings rather than stale ones.
	if c.hwRef && c.st != nil && enabled == c.enabled {
		c.delayMs, c.tailMs = delayMs, tailMs
		return
	}
	c.enabled = enabled
	c.delayMs = delayMs
	c.tailMs = tailMs
	if !enabled {
		c.freeLocked()
		log.Printf("[aec] disabled")
		return
	}
	c.buildLocked()
	c.seedRingLocked()
	c.loadStateLocked()
}

// buildLocked (re)creates the echo state at the length for the reference in
// use. What was learnt is discarded: it is worthless across a length change.
func (c *Canceller) buildLocked() {
	c.freeLocked()
	c.stTailMs = c.effectiveTailLocked()
	tailSamples := C.int(c.stTailMs * sampleRate / 1000)
	rate := C.spx_int32_t(sampleRate)
	for path := range c.states {
		c.states[path] = C.speex_echo_state_init(C.int(FrameSize), tailSamples)
		C.speex_echo_ctl(c.states[path], C.SPEEX_ECHO_SET_SAMPLING_RATE, unsafe.Pointer(&rate))
		c.residualGain[path] = 1
	}
	c.st = c.states[c.activePath]
	c.resetStatsLocked()

	c.micBuf = (*C.spx_int16_t)(C.malloc(FrameSize * 2))
	c.refBuf = (*C.spx_int16_t)(C.malloc(FrameSize * 2))
	c.outBuf = (*C.spx_int16_t)(C.malloc(FrameSize * 2))
	log.Printf("[aec] enabled: frame=%d tail=%dms delay=%dms paths=%d", FrameSize, c.stTailMs, c.delayMs, aecPaths)
}

// seedRingLocked seeds the ring with the bulk delay as silence: the mic
// goroutine then reads reference samples delayMs behind their ALSA write,
// aligning them with when the sound actually reaches the mics.
func (c *Canceller) seedRingLocked() {
	c.head, c.tail, c.count = 0, 0, 0
	c.dsum, c.dcnt = 0, 0
	delaySamples := c.delayMs * sampleRate / 1000
	for i := 0; i < delaySamples; i++ {
		c.pushLocked(0)
	}
}

func (c *Canceller) freeLocked() {
	if c.st != nil {
		for path, st := range c.states {
			if st != nil {
				C.speex_echo_state_destroy(st)
				c.states[path] = nil
			}
		}
		c.st = nil
		C.free(unsafe.Pointer(c.micBuf))
		C.free(unsafe.Pointer(c.refBuf))
		C.free(unsafe.Pointer(c.outBuf))
		c.micBuf, c.refBuf, c.outBuf = nil, nil, nil
	}
}

func (c *Canceller) resetStatsLocked() {
	c.statFrames, c.statInSum, c.statOutSum, c.statRefSum = 0, 0, 0, 0
	c.statInBand = [3]float64{}
	c.statOutBand = [3]float64{}
}

func (c *Canceller) pushLocked(s int16) {
	c.ring[c.head] = s
	c.head = (c.head + 1) % ringCap
	if c.count < ringCap {
		c.count++
	} else {
		c.tail = (c.tail + 1) % ringCap // overwrite oldest
	}
}

// WriteFar feeds one speaker period (48kHz stereo S16LE — audio or silence)
// into the reference ring. Called from the speaker ALSA goroutine for every
// period pumped. Downmix: (L+R)/2; decimate: mean of 3 (box low-pass —
// crude, but the echo content is voice-band and the canceller adapts to
// the filter's response like any other part of the echo path).
func (c *Canceller) WriteFar(period []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.enabled {
		return
	}
	if c.hwRef {
		// Nothing drains the ring on the hardware path, so filling it would
		// peg it at ringCap and leave the far-end telemetry describing a
		// buffer no cancellation ever reads.
		return
	}
	n := len(period) / 4 // frames (2ch × 2 bytes)
	for i := 0; i < n; i++ {
		l := int16(binary.LittleEndian.Uint16(period[i*4:]))
		r := int16(binary.LittleEndian.Uint16(period[i*4+2:]))
		c.dsum += (int32(l) + int32(r)) / 2
		c.dcnt++
		if c.dcnt == 3 {
			s := int16(c.dsum / 3)
			c.pushLocked(s)
			c.farSumSq += float64(s) * float64(s)
			c.farSum += float64(s)
			if a := int32(s); a < 0 {
				if -a > c.farPeak {
					c.farPeak = -a
				}
			} else if a > c.farPeak {
				c.farPeak = a
			}
			c.farSamples++
			c.dsum, c.dcnt = 0, 0
		}
	}
	// ~1/s while the far end carries real audio (playback): what the tap
	// is actually delivering, and where the ring sits.
	if c.farSamples >= sampleRate {
		rms := math.Sqrt(c.farSumSq / float64(c.farSamples))
		mean := c.farSum / float64(c.farSamples)
		if rms > 100 {
			log.Printf("[aec] far: rms=%.0f mean=%.0f peak=%d pushed=%d ring=%d",
				rms, mean, c.farPeak, c.farSamples, c.count)
		}
		c.farSamples, c.farSumSq, c.farSum, c.farPeak = 0, 0, 0, 0
	}
}

// Process runs echo cancellation on one mic buffer: 16kHz mono S16LE, any
// multiple of FrameSize samples. The mic ALSA reader does NOT deliver single
// 512-sample periods — GoTinyAlsa's GetAudioStream reads pcm_get_buffer_size
// per chunk (PeriodSize × PeriodCount = 2560 frames = 160ms), so the buffer
// arriving here is 5 speex frames long. The pre-2026-07-08 version of this
// guard required exactly one frame and silently passed everything through —
// AEC had therefore never processed a single sample on hardware (ring pegged
// at ringCap, 0dB cancellation at every delay setting, zero underruns to give
// it away) while the unit tests, which feed single frames, showed 42dB.
// Hence: any size this function cannot handle is LOGGED, never silently
// bypassed. Called from the mic goroutine.
func (c *Canceller) Process(mono []byte) []byte {
	return c.process(mono, nil, false)
}

// ProcessWithRef cancels using a far-end reference supplied by the CALLER,
// taken from ch8 of the same raw mic period (#385). Both streams therefore
// come off one ADC clock in one TDM frame, so alignment is structural rather
// than inferred: the residual is the +33-sample (2.06ms) converter and
// acoustic delay measured on hardware, which sits far inside the filter tail
// and is absorbed like any other room delay. The measured polarity inversion
// is likewise learned by the filter.
//
// hwRef must be set (SetHardwareRef) or this falls back to the ring, so that
// a caller and the canceller cannot disagree about which reference is live.
// A nil or short ref is treated as "no reference this period" and the frame
// passes through uncancelled rather than being cancelled against silence —
// which would be indistinguishable from a working AEC with nothing playing.
func (c *Canceller) ProcessWithRef(mono, ref []byte) []byte {
	return c.process(mono, ref, false)
}

// ProcessWithRefInPlace is the live-pipeline form. Beamformer output is a
// fresh buffer owned by the current batch, so reusing it for AEC output saves
// one 5KB allocation every 160ms. ProcessWithRef keeps copy semantics for
// callers and tests that retain the uncancelled input.
func (c *Canceller) ProcessWithRefInPlace(mono, ref []byte) []byte {
	return c.process(mono, ref, true)
}

// ProcessInPlace is the software-reference counterpart.
func (c *Canceller) ProcessInPlace(mono []byte) []byte {
	return c.process(mono, nil, true)
}

func (c *Canceller) process(mono, hwref []byte, inPlace bool) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.enabled || c.st == nil {
		return mono
	}
	// The hardware path is only taken when both sides agree it is live and
	// the caller actually supplied a matching period.
	useHW := c.hwRef && hwref != nil && len(hwref) == len(mono)
	if c.hwRef && !useHW {
		// Counted, not logged per period: at 31 periods/s a log line here
		// would bury the very telemetry used to diagnose it.
		c.hwMissing++
		if c.hwMissing == 1 || c.hwMissing%256 == 0 {
			log.Printf("[aec] hardware reference missing or mismatched "+
				"(mic %db, ref %db, occurrences=%d) — frames passed through",
				len(mono), len(hwref), c.hwMissing)
		}
		return mono
	}
	if len(mono) == 0 || len(mono)%(FrameSize*2) != 0 {
		if !c.sizeWarned {
			c.sizeWarned = true
			log.Printf(
				"[aec] mic buffer %db is not a multiple of the %db speex frame — AEC BYPASSED",
				len(mono), FrameSize*2,
			)
		}
		return mono
	}

	out := mono
	if !inPlace {
		out = make([]byte, len(mono))
	}
	mic := unsafe.Slice((*int16)(unsafe.Pointer(c.micBuf)), FrameSize)
	ref := unsafe.Slice((*int16)(unsafe.Pointer(c.refBuf)), FrameSize)
	res := unsafe.Slice((*int16)(unsafe.Pointer(c.outBuf)), FrameSize)

	for off := 0; off < len(mono); off += FrameSize * 2 {
		sub := mono[off : off+FrameSize*2]
		for i := 0; i < FrameSize; i++ {
			mic[i] = int16(binary.LittleEndian.Uint16(sub[i*2:]))
		}
		short := 0
		if useHW {
			// Same frame, same clock — a straight copy, no ring, no delay
			// bookkeeping. This is the whole point of #385.
			hsub := hwref[off : off+FrameSize*2]
			// Unity if nobody has told us the volume yet. That should not
			// happen — cmd/server.go seeds the level from the device's own
			// tinymix reading as it wires the callback, before the control
			// client dials — but unity is the least-wrong guess, since a
			// zero reference cancels nothing and looks identical to a
			// working AEC with nothing playing.
			//
			// It is warned about because it is not free: the device boots
			// at whatever level the previous run left in tinymix, and if
			// that is (say) index 60, an unscaled reference is 33dB hot and
			// cancellation collapses exactly as it did in round one.
			scale := c.refScale
			if scale <= 0 {
				scale = 1.0
				if !c.scaleWarned {
					c.scaleWarned = true
					log.Printf("[aec] no playback level yet — hardware " +
						"reference running unscaled; cancellation will be " +
						"poor at any volume below unity")
				}
			}
			for i := 0; i < FrameSize; i++ {
				v := float64(int16(binary.LittleEndian.Uint16(hsub[i*2:]))) * scale
				// The scalar only ever attenuates (level <= 127 by
				// DEVICE_VOLUME_MAX), so this cannot clip in practice —
				// clamped anyway because a future ceiling change must not
				// silently wrap the reference to full-scale opposite sign.
				if v > 32767 {
					v = 32767
				} else if v < -32768 {
					v = -32768
				}
				ref[i] = int16(v)
			}
			c.hwFrames++
		} else {
			for i := 0; i < FrameSize; i++ {
				if c.count > 0 {
					ref[i] = c.ring[c.tail]
					c.tail = (c.tail + 1) % ringCap
					c.count--
				} else {
					ref[i] = 0
					short++
				}
			}
		}
		if short > 0 {
			c.underruns++
			if c.underruns == 1 || c.underruns%256 == 0 {
				log.Printf("[aec] reference underrun (%d samples short, total underruns=%d)", short, c.underruns)
			}
		}

		C.speex_echo_cancellation(c.st, c.micBuf, c.refBuf, c.outBuf)
		corr := c.suppressResidualLocked(res, ref)
		for i := 0; i < FrameSize; i++ {
			binary.LittleEndian.PutUint16(out[off+i*2:], uint16(res[i]))
		}

		// Attenuation telemetry: fires while the speaker is playing
		// (reference above the silence floor) — and also when the mic is
		// loud with a quiet reference, the broken state this telemetry was
		// built to catch. ~1 line/s of active audio.
		refRMS := frameRMS(ref)
		micRMS := frameRMS(mic)
		if refRMS > 100 || micRMS > 500 { // int16 units; idle floor is well below both
			c.statFrames++
			c.statInSum += micRMS
			c.statOutSum += frameRMS(res)
			c.statRefSum += refRMS
			inBands, outBands := threeBandEnergy(mic), threeBandEnergy(res)
			for band := range inBands {
				c.statInBand[band] += inBands[band]
				c.statOutBand[band] += outBands[band]
			}
			if refRMS > 100 && frameRMS(res) > 500 && corr < 0.35 {
				c.doubleTalkFrames++
			}
			if c.statFrames == 32 { // 32 × 32ms ≈ 1s
				inAvg, outAvg, refAvg := c.statInSum/32, c.statOutSum/32, c.statRefSum/32
				att := 0.0
				if outAvg > 0 {
					att = 20 * math.Log10(inAvg/outAvg)
				}
				var bandAtt [3]float64
				for band := range bandAtt {
					bandAtt[band] = 10 * math.Log10(math.Max(c.statInBand[band], 1e-9)/math.Max(c.statOutBand[band], 1e-9))
				}
				if useHW {
					log.Printf("[aec] att=%.1fdB mic=%.0f out=%.0f ref=%.0f "+
						"bands=%.1f/%.1f/%.1f path=ch%d src=hw(ch8) frames=%d residual=%d double=%d",
						att, inAvg, outAvg, refAvg, bandAtt[0], bandAtt[1], bandAtt[2],
						c.activePath, c.hwFrames, c.residualSuppressedFrames, c.doubleTalkFrames)
					c.maybeSaveLocked(att, refAvg > 100)
				} else {
					log.Printf("[aec] att=%.1fdB mic=%.0f out=%.0f ref=%.0f bands=%.1f/%.1f/%.1f "+
						"path=ch%d ring=%d (delay=%dms) residual=%d double=%d",
						att, inAvg, outAvg, refAvg, bandAtt[0], bandAtt[1], bandAtt[2],
						c.activePath, c.count, c.delayMs, c.residualSuppressedFrames, c.doubleTalkFrames)
				}
				c.resetStatsLocked()
			}
		}
	}

	// Occupancy governor: the ring must sit at ~delaySamples. WriteFar fills
	// it continuously (every speaker period, silence included — that's what
	// keeps the reference clock advancing), but this consumer stops whenever
	// the mic stream does — and the mic stream is stopped/restarted around
	// every voice turn. Each ~1s gap leaves ~16k unconsumed samples behind;
	// production and consumption rates are identical, so the backlog never
	// drains on its own — it compounds per turn until the ring pegs at
	// ringCap and the reference runs a full 3s behind the echo. Trimming
	// back to the nominal delay makes every gap self-heal within one call.
	// Runs AFTER the consume loop (the low-water point): the mic delivers
	// bursty 160ms batches, so occupancy measured before consuming swings
	// by a whole batch and would need slack so wide it re-opens the stale
	// window. Slack of 4 speex frames (128ms) clears producer/consumer
	// phase jitter (~±1 speaker period ≈ 43ms). The filter state is KEPT
	// across the trim: trimming restores the nominal delaySamples alignment
	// — the same alignment the filter converged against — and the physical
	// echo path hasn't changed, so the learned filter is still valid.
	// (2026-07-10: the reset that used to live here was the barge-in
	// killer — mic capture overruns trip this governor every ~20s in
	// steady state, incl. mid-playback, and each reset threw away a
	// converged filter for a ≤43ms alignment shift speexdsp tracks fine.)
	// Not on the hardware path: there is no ring to trim, delayMs means
	// nothing there, and leaving this running would log resyncs about a
	// buffer nothing is filling.
	if useHW {
		return out
	}
	delaySamples := c.delayMs * sampleRate / 1000
	if c.count > delaySamples+4*FrameSize {
		drop := c.count - delaySamples
		c.tail = (c.tail + drop) % ringCap
		c.count = delaySamples
		c.resyncs++
		log.Printf("[aec] reference resync: dropped %d stale samples, filter kept (resyncs=%d)", drop, c.resyncs)
	}

	return out
}

func frameRMS(s []int16) float64 {
	var sum float64
	for _, v := range s {
		f := float64(v)
		sum += f * f
	}
	return math.Sqrt(sum / float64(len(s)))
}

// suppressResidualLocked applies at most 6dB of post-AEC attenuation when the
// remaining output is still strongly correlated with the far-end reference.
// Uncorrelated near-end speech (double-talk) stays at unity. This is purposely
// conservative: the linear Speex filter remains the canceller; the postfilter
// only cleans up a residual that still identifies itself as playback.
func (c *Canceller) suppressResidualLocked(out, ref []int16) float64 {
	if frameRMS(ref) <= 100 {
		c.residualGain[c.activePath] += 0.05 * (1 - c.residualGain[c.activePath])
		return 0
	}
	corr := maxAbsCorrelation(out, ref, 64)
	target := 1.0
	if corr > 0.65 {
		target = 1 - 0.5*math.Min((corr-0.65)/0.30, 1)
	}
	gain := c.residualGain[c.activePath]
	coefficient := 0.05
	if target < gain {
		coefficient = 0.25
	}
	gain += coefficient * (target - gain)
	if gain < 0.5 {
		gain = 0.5
	} else if gain > 1 {
		gain = 1
	}
	c.residualGain[c.activePath] = gain
	if gain < 0.999 {
		c.residualSuppressedFrames++
		for i, sample := range out {
			out[i] = int16(float64(sample) * gain)
		}
	}
	return corr
}

func maxAbsCorrelation(a, b []int16, maxLag int) float64 {
	best := 0.0
	for lag := -maxLag; lag <= maxLag; lag++ {
		startA, startB := 0, 0
		if lag >= 0 {
			startB = lag
		} else {
			startA = -lag
		}
		n := len(a) - startA
		if m := len(b) - startB; m < n {
			n = m
		}
		var ab, aa, bb float64
		for i := 0; i < n; i++ {
			x, y := float64(a[startA+i]), float64(b[startB+i])
			ab += x * y
			aa += x * x
			bb += y * y
		}
		denom := math.Sqrt(aa * bb)
		if denom > 1e-9 {
			corr := math.Abs(ab / denom)
			if corr > best {
				best = corr
			}
		}
	}
	return best
}

// threeBandEnergy is lightweight telemetry, not an audio effect. Two one-pole
// low-passes split each 32ms frame into <500Hz, 500–3000Hz and >3000Hz energy,
// enough to distinguish bass/room-tail failures from speech-band residuals.
func threeBandEnergy(samples []int16) (energy [3]float64) {
	a500 := 1 - math.Exp(-2*math.Pi*500/sampleRate)
	a3000 := 1 - math.Exp(-2*math.Pi*3000/sampleRate)
	var lp500, lp3000 float64
	for _, sample := range samples {
		x := float64(sample)
		lp500 += a500 * (x - lp500)
		lp3000 += a3000 * (x - lp3000)
		bands := [3]float64{lp500, lp3000 - lp500, x - lp3000}
		for band, value := range bands {
			energy[band] += value * value
		}
	}
	return energy
}

// A saved echo path: what the canceller learned, so a restart need not learn
// it again from nothing while the first reply plays. The header ties it to
// the filter shape it came from; speex's own blob is only meaningful to a
// state of the same frame size, filter length and rate.
const stateMagic = "EMAEC1"

const stateHeader = len(stateMagic) + 2 + 2 + 4 + 4 // magic, frame, tailMs, rate, payload

// ExportState returns the learned echo path, or an error when cancellation
// is off.
func (c *Canceller) ExportState() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.exportLocked()
}

func (c *Canceller) exportLocked() ([]byte, error) {
	if c.st == nil {
		return nil, errors.New("aec: not enabled")
	}
	n := int(C.em_echo_state_size(c.st))
	out := make([]byte, stateHeader+n)
	copy(out, stateMagic)
	h := out[len(stateMagic):]
	binary.LittleEndian.PutUint16(h[0:], uint16(FrameSize))
	binary.LittleEndian.PutUint16(h[2:], uint16(c.stTailMs))
	binary.LittleEndian.PutUint32(h[4:], uint32(sampleRate))
	binary.LittleEndian.PutUint32(h[8:], uint32(n))
	if C.em_echo_state_export(c.st, unsafe.Pointer(&out[stateHeader]), C.int(n)) != 0 {
		return nil, errors.New("aec: export size mismatch")
	}
	return out, nil
}

// ImportState loads a saved echo path into the running canceller. Anything
// that does not match the current filter exactly, or holds a non-finite
// value, is refused and the canceller is left as it was: a wrong filter is
// worse than an empty one, which merely has to learn.
func (c *Canceller) ImportState(b []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.importLocked(b)
}

func (c *Canceller) importLocked(b []byte) error {
	if c.st == nil {
		return errors.New("aec: not enabled")
	}
	if len(b) < stateHeader || string(b[:len(stateMagic)]) != stateMagic {
		return errors.New("aec: not a saved echo path")
	}
	h := b[len(stateMagic):]
	frame, tail := int(binary.LittleEndian.Uint16(h[0:])), int(binary.LittleEndian.Uint16(h[2:]))
	rate, n := int(binary.LittleEndian.Uint32(h[4:])), int(binary.LittleEndian.Uint32(h[8:]))
	if frame != FrameSize || tail != c.stTailMs || rate != sampleRate {
		return fmt.Errorf("aec: saved for frame %d tail %dms rate %d, running %d/%dms/%d",
			frame, tail, rate, FrameSize, c.stTailMs, sampleRate)
	}
	if n != int(C.em_echo_state_size(c.st)) || len(b) != stateHeader+n {
		return errors.New("aec: saved echo path is the wrong size")
	}
	// Every field is 4 bytes and, in this FLOATING_POINT build, a float —
	// bar one int flag, which reads as a tiny finite float either way.
	for i := stateHeader; i+4 <= len(b); i += 4 {
		v := math.Float32frombits(binary.LittleEndian.Uint32(b[i:]))
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return errors.New("aec: saved echo path holds a non-finite value")
		}
	}
	if C.em_echo_state_import(c.st, unsafe.Pointer(&b[stateHeader]), C.int(n)) != 0 {
		return errors.New("aec: import size mismatch")
	}
	return nil
}
