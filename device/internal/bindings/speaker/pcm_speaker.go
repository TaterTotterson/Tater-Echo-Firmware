//go:build server

package speaker

import (
	"context"
	"errors"
	"log"
	"math"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/codec"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/mixer"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/checkersalsa"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/outchain"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/platform"

	"github.com/Binozo/GoTinyAlsa/pkg/pcm"
	"github.com/Binozo/GoTinyAlsa/pkg/tinyalsa"
)

// cardNr/deviceNr live in pcmstatus.go so the host test can pin them against
// the status path — this file is ARM-only (build tag `server`).
const periodSize = 2048

// alsaPeriodSize is the legacy tinyalsa period and direct-playback fallback.
// The Linux Dot path uses a 2048-frame hardware period: its 4 x 1024 ring left
// only 85ms of scheduler headroom. pump applies the measured playback-drift
// correction separately.
const alsaPeriodSize = 1024
const alsaPeriodCount = 4
const alsaBufferFrames = alsaPeriodSize * alsaPeriodCount
const periodBytes = periodSize * 2 * 2 // 2 channels * 2 bytes = 8192

// The wire carries MONO 48kHz — PumpPeriod duplicates L=R before queueing.
// The stereo ALSA config is an I2S/codec-path constraint, not a wire
// requirement, and shipping two identical channels to a mono speaker
// doubled TTS bandwidth (~1.5Mbps → ~770kbps saved) for nothing — which
// matters on marginal 2.4GHz links (Lounge stutter).
const monoPeriodBytes = periodSize * 2 // 4096

// audioCh depth — the WS sender delivers at ~2× realtime, so its lead over
// playback grows ~1s per second played until it hits this cap. At 32
// (~1.3s) any WiFi stall longer than the accumulated lead drained the
// channel mid-stream (audible stutter on the far-AP device). 128 periods
// ≈ 5.5s (~1MB queued as stereo): most responses land on-device entirely
// within the first half of playback.
const audioChanDepth = 128

// primePeriods — playback holds on silence until this many periods are
// queued (or the stream's EOS has arrived, for clips shorter than the
// prime). Protects the opening seconds of playback, when the sender's
// lead is still ~zero and a single WiFi stall used to stutter. 24 periods
// ≈ 1s of audio ≈ ~0.5s added start latency at 2× realtime delivery
// (accepted trade, 2026-07-14). The controller's post-playback drain
// sleep allows for the delayed start (SPEAKER_PRIME_SECONDS).
const primePeriods = 24

var silencePeriod = make([]byte, periodBytes)

type PcmSpeaker struct {
	session       playbackSession
	playbackDrift playbackDriftCorrection
	// dotDriftCorrection is true only when Biscuit/Radar successfully opened
	// ALSA with the measured 2048-frame hardware period. pump expands each
	// 2048-frame mixer block by the measured 16-frame correction. Keeping this
	// explicit prevents a fallback period from being corrected with the wrong
	// geometry.
	dotDriftCorrection bool
	// alsaWriteFrames is the MediaTek hardware period selected at open and the
	// chunk size for uncorrected writes. Corrected targets submit a slightly
	// expanded block through playbackDrift instead.
	alsaWriteFrames int
	athenaUpsampler stereo2x
	// Checkers' MediaTek DL1 playback ring must live in DRAM. Hold a second
	// AFE node open before playback, as TECHO5 does, to reserve the SRAM path.
	dramHold *os.File
	target   string
	stopCh   chan struct{}
	// jackInserted is the plug position last applied by SetJackRouting, and
	// jackKnown says whether one has been applied at all. The reconcile loop
	// needs a DESIRED state to compare against, and before jack.Watch has run
	// there is none — acting on a default would fight whatever Init set up.
	jackMu       sync.Mutex
	jackInserted bool
	jackKnown    bool
	// deadCh is closed by silenceLoop on any exit so a pump call can return
	// an error rather than block indefinitely waiting for a dead consumer.
	deadCh chan struct{}

	// Two independent playback planes, mixed at the ALSA write.
	//
	// voice carries TTS and announcements (0x02/0x03); music carries the
	// media player (0x04/0x05). They are separate so a voice turn can duck
	// the music instead of pausing it: the music feed runs LEAD_S=4s ahead
	// of realtime, so by the time a wake word fires the next four seconds
	// are already in this buffer, and audio that has left the controller
	// cannot be ducked by the controller. Holding both here means the music
	// keeps its full ~5.5s of link-stall protection AND the duck is instant.
	//
	// All the per-stream machinery — prime gate, discard-until-EOS,
	// underrun accounting, delivery instrumentation — lives in audioStream,
	// so both planes get the behaviour that was worked out on the single
	// one rather than a simplified copy.
	voice *audioStream
	music *audioStream
	// voicePrime controls the voice plane's startup buffer. Normal TTS keeps
	// the deep Wi-Fi jitter protection; live Sat Chat uses a few periods so a
	// conversation does not inherit roughly one second of announcement delay.
	voicePrime atomic.Int32
	// musicPrime normally protects network media with the same deep startup
	// buffer. Fullscreen video is decoded locally in realtime and needs a short
	// gate so sound begins with the first displayed frames instead of a second
	// later.
	musicPrime atomic.Int32

	// duckTarget is the gain applied to MUSIC while it plays under a voice
	// turn, Q15. Written from the control plane (SetDuck) and read by the
	// ALSA goroutine every period, hence atomic; the ramp toward it lives in
	// the Mixer, which is single-consumer and needs no synchronisation.
	duckTarget     atomic.Int32
	duckRampFrames atomic.Int64
	duckRevision   atomic.Uint64
	duckCompleted  atomic.Uint64
	mixer          Mixer

	// chain is the output chain (EQ, bass guard, limiter), run on the MIX,
	// after the duck and before the taps and the DAC. Inactive until the
	// controller says it has stopped processing (SetOutputChainActive), so
	// the chain never runs twice. chainBuf is where a silent period is
	// processed while filter tails decay: silencePeriod is shared and must
	// never be written.
	chain    *outchain.Chain
	chainBuf []byte

	// echoTap, when non-nil, receives every period pumped to ALSA — real
	// audio and silence alike — so an AEC reference stream advances in
	// lockstep with the playback clock. Fixed at construction (silenceLoop
	// starts inside New, so it can't be set later without a race). Must be
	// fast and non-blocking: it runs on the ALSA pump goroutine.
	echoTap func([]byte)

	// levelTap, when non-nil, receives the RMS level (0..1, normalized
	// int16 full-scale) of every period as it is pumped to ALSA — real
	// audio carries its measured level, silence reports 0. Drives the
	// energy-reactive LED ring ("meter" led_anim pattern): tapping at the
	// ALSA write means the throb tracks what is audible right now, not
	// what PumpPeriod queued ~5.5s of buffer ago. Fixed at construction
	// like echoTap (silenceLoop races any later setter) and must be fast
	// and non-blocking: it runs on the ALSA pump goroutine.
	levelTap func(rms float64)

	// statsCb, when set, receives one StreamStats once per completed
	// stream — at EOS consume in silenceLoop, flush included. Unlike echoTap
	// it fires at most once per response, so a setter (OnStreamStats) is
	// fine: silenceLoop reads it under statsMu only on that cold path.
	statsMu sync.Mutex
	statsCb func(StreamStats)

	// src is Sendspin's pull producer for the music plane. It is consulted
	// only while the native local-scene music plane is empty. The ALSA writer
	// owns the DAC timing measurement, so timestamped audio must be pulled
	// here rather than pushed into the several-seconds-deep music queue.
	src atomic.Pointer[sourceBox]
	// srcTimelineBuf is one corrected hardware-accounting period. Sendspin
	// fills that full timeline on affected MediaTek paths; it is compressed
	// into srcBuf for mixing and expanded back at the ALSA write. Without this
	// bridge Sendspin consumed only 2048 timestamped frames while ALSA advanced
	// 2064, forcing a sample drop every period and a hard resync about once a
	// second (heard as a faint pop on Biscuit).
	srcBuf         []byte
	srcTimelineBuf []byte
	// sourceLastFill records Sendspin audio actually handed to the ALSA pump.
	// Active() can clear just before the last hardware-buffered period finishes;
	// the hold in MusicAudible bridges that tail for microphone-side AEC.
	sourceLastFill atomic.Int64
}

// MusicSource fills one stereo S16LE period whose first frame reaches the DAC
// at playAt. uncertain is half the duration of the ALSA status measurement.
type MusicSource interface {
	Active() bool
	Fill(out []byte, playAt time.Time, uncertain time.Duration) bool
}

type sourceBox struct{ source MusicSource }

// SetMusicSource installs Sendspin's pull source, or removes it when nil.
func (p *PcmSpeaker) SetMusicSource(source MusicSource) {
	if source == nil {
		p.src.Store(nil)
		return
	}
	p.src.Store(&sourceBox{source: source})
}

func (p *PcmSpeaker) playbackDriftCorrectionActive() bool {
	if p.alsaWriteFrames != periodSize {
		return false
	}
	return p.dotDriftCorrection || strings.EqualFold(strings.TrimSpace(p.target), "rook")
}

func (p *PcmSpeaker) fillMusicSource(source MusicSource, playAt time.Time, uncertain time.Duration) []byte {
	if p.playbackDriftCorrectionActive() {
		if !source.Fill(p.srcTimelineBuf, playAt, uncertain) {
			return nil
		}
		return compressPlaybackDrift(p.srcBuf, p.srcTimelineBuf)
	}
	if !source.Fill(p.srcBuf, playAt, uncertain) {
		return nil
	}
	return p.srcBuf
}

// pullSource measures when the next ALSA period reaches the DAC and asks
// Sendspin for exactly the audio due at that instant.
func (p *PcmSpeaker) pullSource() []byte {
	box := p.src.Load()
	if box == nil || !box.source.Active() {
		return nil
	}
	before := time.Now()
	status, err := os.ReadFile(statusPath(cardNr, deviceNr))
	readDuration := time.Since(before)
	if err != nil {
		return nil
	}
	queuedFrames, ok := pcmDelay(string(status))
	if !ok {
		return nil
	}
	measuredAt := before.Add(readDuration / 2)
	playAt := measuredAt.Add(time.Duration(queuedFrames) * time.Second / 48000)
	out := p.fillMusicSource(box.source, playAt, readDuration/2)
	if out == nil {
		return nil
	}
	p.sourceLastFill.Store(time.Now().UnixNano())
	return out
}

// MusicPlaneBusy gives direct controller audio priority over Sendspin.
func (p *PcmSpeaker) MusicPlaneBusy() bool {
	return p.music.playedWithin(time.Now(), 100*time.Millisecond)
}

// MusicAudible reports whether either the buffered music plane or Sendspin is
// currently reaching the speaker. The microphone pipeline uses this to select
// stronger correlation-only residual suppression while preserving real wake
// speech at unity gain.
func (p *PcmSpeaker) MusicAudible(hold time.Duration) bool {
	now := time.Now()
	if p.music.playedWithin(now, hold) {
		return true
	}
	if box := p.src.Load(); box != nil && box.source.Active() {
		return true
	}
	last := p.sourceLastFill.Load()
	return last > 0 && now.UnixNano()-last < int64(hold)
}

type playbackSession interface {
	Pump([]byte) error
	Close()
}

// OnStreamStats registers a per-stream stats callback, reported once when a
// stream reaches its EOS. Invoked on its own goroutine so a slow consumer
// (network send) can never stall the ALSA pump. Safe to call any time
// after New.
func (p *PcmSpeaker) OnStreamStats(cb func(StreamStats)) {
	p.statsMu.Lock()
	p.statsCb = cb
	p.statsMu.Unlock()
}

func NewPcmSpeaker(echoTap func([]byte), levelTap func(rms float64)) (*PcmSpeaker, error) {
	return NewPcmSpeakerForTarget("biscuit", echoTap, levelTap)
}

// NewPcmSpeakerForTarget opens the measured playback endpoint and applies
// board-specific codec/amp semantics. The Puffin targets use pcm23p; the
// screen targets use their measured MediaTek DL1 paths.
func NewPcmSpeakerForTarget(target string, echoTap func([]byte), levelTap func(rms float64)) (*PcmSpeaker, error) {
	s := &PcmSpeaker{
		stopCh:         make(chan struct{}),
		deadCh:         make(chan struct{}),
		target:         target,
		echoTap:        echoTap,
		levelTap:       levelTap,
		chain:          outchain.New(48000),
		chainBuf:       make([]byte, periodBytes),
		srcBuf:         make([]byte, periodBytes),
		srcTimelineBuf: make([]byte, driftOutputFrames*4),
	}
	s.voice = newAudioStream(audioChanDepth, s.deadCh)
	s.music = newAudioStream(audioChanDepth, s.deadCh)
	s.voicePrime.Store(primePeriods)
	s.musicPrime.Store(primePeriods)
	s.duckTarget.Store(unityGain)
	s.duckRampFrames.Store(periodSize)
	s.duckRevision.Store(1)
	s.duckCompleted.Store(1)
	s.mixer.SetGainImmediate(unityGain)
	if err := s.Init(); err != nil {
		return nil, err
	}
	return s, nil
}

func isLinuxScreenPlaybackTarget(target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	return target == "checkers" || target == "crown" || target == "athena" || target == "rook"
}

func hasNoHeadphoneJack(target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	return target == "checkers" || target == "crown" || target == "athena"
}

func isRadarPlaybackTarget(target string) bool {
	return strings.EqualFold(strings.TrimSpace(target), "radar")
}

func isLinuxDotPlaybackTarget(target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	return target == "biscuit" || target == "radar"
}

func linuxDotPlaybackPeriodFrames(target string) int {
	if isLinuxDotPlaybackTarget(target) {
		return periodSize
	}
	return alsaPeriodSize
}

func (p *PcmSpeaker) Init() error {
	// Startup order matters for the audible click (2026-07-10): the amp
	// must come up onto a DAC that is already clocking silence, and the
	// unmute must come last. The old order (amp on → unmute → open PCM)
	// unmuted a floating DAC and then hit it with the stream-open
	// transient — the "click" on every service start.
	if platform.Base() != platform.TaterLinux && !isLinuxScreenPlaybackTarget(p.target) {
		exec.Command("stop", "mixer").Run()
	}
	// Android's media stack takes the speaker for itself when a headphone
	// plug is present at boot, and ALSA parks a blocking open behind it with
	// no timeout — stranding the whole device, since everything else in
	// main() is initialised after the speaker (issue #80). Same stock-service
	// takeover as `stop mixer` above and `stop smarthomewifid` in main: on a
	// device where Tater drives the codec directly, mediaserver has no
	// work to do and is only ever in the way.
	if platform.Base() != platform.TaterLinux && !isLinuxScreenPlaybackTarget(p.target) {
		exec.Command("stop", "media").Run()
	}
	waitForFreePcm(cardNr, deviceNr, pcmFreeTimeout)
	// Connect the DAC to the output mixer before opening the stream: DAPM
	// decides what to power at stream open, and an unrouted DAC is powered
	// down, which presents as a clean "voice stream complete, underruns=0"
	// into silence. See the codec package.
	codec.EnsureRoutes(p.target)
	mixer.SetPlaybackLevel(0, 127) // mute before touching amp or stream
	if strings.EqualFold(strings.TrimSpace(p.target), "rook") {
		// Rook's mono speaker needs its right-channel driver and gain set before
		// the DAC begins clocking. Biscuit's current path does not set these.
		for _, write := range jackRoutingForTarget("rook", false) {
			if write.Ctl != mixer.SpeakerAmp {
				mixer.Set(write.Ctl, write.Args...)
			}
		}
	}
	linuxScreenPlayback := isLinuxScreenPlaybackTarget(p.target)
	directLinuxPlayback := linuxScreenPlayback ||
		(platform.Base() == platform.TaterLinux && isLinuxDotPlaybackTarget(p.target))
	if linuxScreenPlayback && !strings.EqualFold(strings.TrimSpace(p.target), "athena") {
		// The vendor kernel chooses DL1's ring memory at open. With no other
		// AFE node held, it chooses a broken SRAM path; TECHO5 holds this
		// unused capture node so playback takes the working DRAM path.
		hold, err := os.OpenFile("/dev/snd/pcmC0D1c", os.O_RDWR|syscall.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		p.dramHold = hold
	}

	var session playbackSession
	var err error
	if directLinuxPlayback {
		// Every Tater Linux device uses the same direct ALSA ioctl session.
		// Shows retain their established 768 x 4 configuration. Rook and the two
		// Dot boards use a 2048 x 4 ring; affected DL1 paths compensate their
		// measured playback drift at write time.
		p.alsaWriteFrames = alsaPeriodSize
		if strings.EqualFold(strings.TrimSpace(p.target), "rook") {
			session, err = checkersalsa.OpenPlaybackWithPeriodFrames(periodSize)
			if err != nil {
				log.Printf("[speaker] Spot 2048 x 4 playback unavailable (%v); falling back to 1536 x 4", err)
				session, err = checkersalsa.OpenPlaybackWithPeriodFrames(1536)
				if err != nil {
					log.Printf("[speaker] Spot 1536 x 4 playback unavailable (%v); falling back to 768 x 4", err)
					session, err = checkersalsa.OpenPlayback()
				}
			} else {
				p.alsaWriteFrames = periodSize
			}
		} else if strings.EqualFold(strings.TrimSpace(p.target), "athena") {
			session, err = checkersalsa.OpenAthenaPlayback()
			p.alsaWriteFrames = 1536
		} else if linuxScreenPlayback {
			session, err = checkersalsa.OpenPlayback()
			p.alsaWriteFrames = checkersalsa.PeriodFrames
		} else {
			// Keep the larger 2048 x 4 hardware ring that eliminated scheduler
			// starvation. Bench testing also found a repeatable playback/ring drift,
			// so pump linearly expands each complete 2048-frame mixer block to 2064.
			// The earlier 1024 -> 1040 trial made twice as many corrections and was
			// audibly distorted.
			p.alsaWriteFrames = linuxDotPlaybackPeriodFrames(p.target)
			session, err = checkersalsa.OpenPlaybackWithPeriodFrames(p.alsaWriteFrames)
			if err != nil {
				log.Printf("[speaker] Dot corrected 2048 x 4 playback unavailable (%v); falling back to uncorrected 1024 x 4", err)
				p.alsaWriteFrames = alsaPeriodSize
				session, err = checkersalsa.OpenPlaybackWithPeriodFrames(p.alsaWriteFrames)
			} else {
				p.dotDriftCorrection = true
				log.Printf("[speaker] Dot DL1 drift compensation enabled: %d mixer frames -> %d write frames", periodSize, driftOutputFrames)
			}
		}
		if err == nil && (strings.EqualFold(strings.TrimSpace(p.target), "rook") || p.dotDriftCorrection) {
			if playback, ok := session.(*checkersalsa.Playback); ok {
				// This vendor copy callback silently accepts a write larger than
				// its private free-space count, copies only the aligned space, and
				// still reports success to ALSA. Keep at least one corrected write
				// free in the ring before submitting it. Without this guard the
				// missing tail accumulates until the pointer reports an XRUN.
				playback.SetMaximumQueuedFrames(2 * periodSize)
			}
		}
	} else {
		device := tinyalsa.NewDevice(cardNr, deviceNr, pcm.Config{
			Channels:         2,
			SampleRate:       48000,
			PeriodSize:       alsaPeriodSize,
			PeriodCount:      alsaPeriodCount,
			Format:           tinyalsa.PCM_FORMAT_S16_LE,
			StartThreshold:   alsaPeriodSize,
			StopThreshold:    alsaBufferFrames,
			SilenceThreshold: alsaBufferFrames,
		})
		var tinySession tinyalsa.AudioSession
		tinySession, err = device.NewAudioSession()
		session = &tinySession
	}
	if err != nil {
		if p.dramHold != nil {
			p.dramHold.Close()
			p.dramHold = nil
		}
		return err
	}
	p.session = session

	go p.silenceLoop()

	time.Sleep(100 * time.Millisecond) // silence reaches the DAC (~2 periods)
	mixer.SetSpeakerEnabled(true)      // enable amp onto a clocked, silent DAC
	time.Sleep(50 * time.Millisecond)  // let amp settle
	if isRadarPlaybackTarget(p.target) {
		// Radar's MFP2 gate is the final release in its tested vendor path.
		// Releasing it before the PCM and external amp settle produces a
		// floating/noisy speaker input rather than clean audio.
		mixer.Set("MFP Gpio Mute", "Off")
	}
	mixer.SetPlaybackLevel(100, 127) // unmute

	log.Println("PcmSpeaker initialised — silence stream running")
	return nil
}

// pcmFreeTimeout bounds the wait for another process to release the speaker.
//
// Generous, because the wait is the good outcome: releasing takes Android well
// under a second once `stop media` lands, and a device that waits ten seconds
// and then works is enormously better than one that gives up early. It is a
// backstop against a holder that never lets go, not a tuning parameter.
const pcmFreeTimeout = 10 * time.Second

// waitForFreePcm blocks until the playback substream is released, or the
// timeout expires.
//
// On timeout it RETURNS ANYWAY and lets the open proceed. That open may then
// block forever, which is the pre-existing behaviour — this function's job is
// to make the common case work and to leave a log line naming the holder when
// it does not. Refusing to open would be a bigger behaviour change than the
// bug warrants, and the proper fix for a permanently-held device is to stop
// gating the rest of main() on the speaker at all.
func waitForFreePcm(card, device int, timeout time.Duration) {
	path := statusPath(card, device)
	b, err := os.ReadFile(path)
	if err != nil || pcmFree(string(b)) {
		return // free, or no status file to consult — open immediately
	}

	log.Printf("[speaker] %s held by pid %d — waiting up to %s", path, pcmOwner(string(b)), timeout)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		b, err := os.ReadFile(path)
		if err != nil || pcmFree(string(b)) {
			log.Printf("[speaker] speaker released after %s", time.Since(deadline.Add(-timeout)).Round(time.Millisecond))
			return
		}
	}
	b, _ = os.ReadFile(path)
	log.Printf("[speaker] speaker STILL held by pid %d after %s — opening anyway, this may block",
		pcmOwner(string(b)), timeout)
}

// SetJackRouting puts the codec into the state the current plug position
// needs. Safe to call repeatedly with the same position — every write is
// idempotent, which is what lets Watch dispatch the state it booted into
// without special-casing it.
//
// It replaces EnableSpeakerAmp, which acted only on REMOVAL and only on the
// amp switch. Both halves of that were wrong, and each hid a symptom:
//
//   - Insert was left entirely to accdet. accdet does mute the internal amp
//     on the insert transition, but it also drops HP Driver Gain to the floor
//     of its range, and on a stock Dot the audio HAL raises it back. Nothing
//     of ours did, so the external output was inaudible — not quiet, silent —
//     and the old comment here asserting the amp switch was "the ONLY control
//     involved" is what stopped anyone looking (falsified 2026-09-03: writing
//     ctl 62 with music playing took the jack from silent to audible, with the
//     loopback tap `ref` unchanged, so it is a post-DAC analog stage).
//   - Neither accdet nor the old code ran at BOOT, because both are
//     edge-triggered and a boot has no edge. Init unconditionally sets the amp
//     ON, so a device booted with a cable in played out its internal speaker
//     until someone unplugged and replugged — which is exactly why replugging
//     was the folk remedy. It manufactures the edge the boot never had.
func (p *PcmSpeaker) SetJackRouting(inserted bool) {
	if hasNoHeadphoneJack(p.target) {
		return
	}
	p.jackMu.Lock()
	p.jackInserted = inserted
	p.jackKnown = true
	p.jackMu.Unlock()

	p.applyJackWrites(jackRoutingForTarget(p.target, inserted))
	log.Printf("[speaker] jack routing applied (%s)",
		map[bool]string{true: "external", false: "internal"}[inserted])
}

func (p *PcmSpeaker) applyJackWrites(ws []mixerWrite) {
	for _, w := range ws {
		// mixer.Set logs a failing control itself
		mixer.Set(w.Ctl, w.Args...)
	}
}

// JackReconcileInterval bounds how long the jack can sit at the wrong gain
// after Android's HAL has rewritten it.
//
// 30s is a compromise, not a measurement. Shorter closes the window of silence
// after each mediaserver restart. It dates from when each read spawned tinymix;
// the reads are now two ioctls, but the reverts only arrive every 60-90s, so
// there is nothing to gain from polling faster.
const JackReconcileInterval = 30 * time.Second

// ReconcileJackRouting reads the routing controls back and rewrites only the
// ones that have moved. Returns the number of controls it had to correct, so a
// caller can tell "the HAL is fighting us" from "nothing is happening" — which
// are identical from the outside and want opposite investigations.
//
// Does nothing until a jack position has actually been observed: before
// jack.Watch has run there is no desired state, and guessing one would fight
// whatever Init established.
func (p *PcmSpeaker) ReconcileJackRouting() int {
	if hasNoHeadphoneJack(p.target) {
		return 0
	}
	p.jackMu.Lock()
	inserted, known := p.jackInserted, p.jackKnown
	p.jackMu.Unlock()
	if !known {
		return 0
	}

	current := map[string]string{}
	for _, write := range jackRoutingForTarget(p.target, inserted) {
		ctl := write.Ctl
		if v, err := mixer.Get(ctl); err == nil {
			current[ctl] = v
		} // a failed read is not evidence of drift
	}

	drift := jackRoutingDriftForTarget(p.target, inserted, current)
	if len(drift) == 0 {
		return 0
	}
	p.applyJackWrites(drift)
	return len(drift)
}

// WatchJackRouting re-applies the routing for as long as ctx lives.
//
// Runs regardless of plug position: the HAL's rewrite is not specific to the
// jack, and a gain left at the floor is just as wrong for the internal driver.
func (p *PcmSpeaker) WatchJackRouting(ctx context.Context) {
	if hasNoHeadphoneJack(p.target) {
		return
	}
	t := time.NewTicker(JackReconcileInterval)
	defer t.Stop()
	var corrected int
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n := p.ReconcileJackRouting(); n > 0 {
				corrected += n
				// Logged per event rather than counted silently: the RATE is
				// the diagnostic, and it is the only place the HAL's
				// interference is visible at all.
				log.Printf("[speaker] jack routing drifted — %d control(s) rewritten (total %d)", n, corrected)
			}
		}
	}
}

// silenceLoop is the ALSA write path: every period, take what each plane has
// to offer, mix them, and pump. When neither has audio it pumps silence,
// which is what paces this loop — ALSA blocks in Pump at realtime rate, so
// there is no timer anywhere and no busy-waiting.
//
// It replaced a blocking select on a single channel. Two planes cannot be
// waited on that way without one starving the other, and mixing needs both
// in hand at the same instant; a non-blocking take from each, with silence
// as the floor, keeps the pacing property that made the original correct.
//
// Closes deadCh on any exit so blocked pump callers unblock with an error
// rather than hanging.
func (p *PcmSpeaker) silenceLoop() {
	defer close(p.deadCh)
	var meter writeLoopMeter // bench builds only; empty otherwise
	var duckRevision uint64
	for {
		select {
		case <-p.stopCh:
			return
		default:
		}

		var voice, music []byte
		voicePrime := int(p.voicePrime.Load())
		if voicePrime <= 0 {
			voicePrime = primePeriods
		}
		if p.voice.ready(voicePrime) {
			voice = p.voice.take()
		} else if p.voice.playing {
			p.report(p.voice.drained(), "voice")
		}
		musicPrime := int(p.musicPrime.Load())
		if musicPrime <= 0 {
			musicPrime = primePeriods
		}
		if p.music.ready(musicPrime) {
			music = p.music.take()
		} else if p.music.playing {
			p.report(p.music.drained(), "music")
		}
		if music == nil && !p.music.playing && !p.music.isActive() {
			music = p.pullSource()
		}

		// The ring's level must be measured BEFORE mixing: Mix sums into the
		// voice buffer in place, so afterwards there is no voice-only signal
		// left to measure. Synchronized stereo replies travel on the timed
		// music plane, so fall back to that plane whenever no foreground voice
		// period is present. This keeps both playback paths genuinely reactive
		// to their audible PCM rather than inventing motion in the animator.
		var level float64
		if p.levelTap != nil {
			level = playbackMeterLevel(voice, music)
		}

		if revision := p.duckRevision.Load(); revision != duckRevision {
			p.mixer.SetRamp(p.duckTarget.Load(), p.duckRampFrames.Load())
			duckRevision = revision
		}
		out := p.mixer.MixConfigured(voice, music, periodSize)
		if p.mixer.Gain() == p.duckTarget.Load() {
			p.duckCompleted.Store(duckRevision)
		}
		process := out != nil
		if out == nil {
			out = silencePeriod
			if !p.chain.Idle() {
				// Filter tails still ringing out of the last audio.
				copy(p.chainBuf, silencePeriod)
				out, process = p.chainBuf, true
			}
		}
		if process {
			if applied := p.chain.Process(out); applied != nil {
				log.Printf("[speaker] output chain: %s", applied)
			}
		}

		// Taps see the MIXED output, which is what the speaker actually
		// emits. That matters for AEC: the far-end reference is what needs
		// cancelling from the mic, and with music playing under a response
		// the echo is the sum, not the voice alone. It was already correct
		// by accident when only one stream existed.
		if p.echoTap != nil {
			p.echoTap(out)
		}
		// Prefer VOICE when it exists so ducked background music cannot drive
		// the response ring. Media is used only when voice is absent, which is
		// the synchronized-stereo TTS path.
		if p.levelTap != nil {
			p.levelTap(level)
		}
		meter.observeAudio(out, len(voice) > 0 || len(music) > 0)
		meter.beforeWrite()
		if err := p.pump(out); err != nil {
			log.Printf("silenceLoop: pump error: %v", err)
			return
		}
		meter.afterWrite()
	}
}

// pump writes one mixed period to ALSA in pieces matching the hardware period
// actually selected at open. Biscuit and Radar expand one complete 2048-frame
// mixer block by the measured 16-frame playback correction; targets with
// smaller hardware periods retain established split writes without correction.
func (p *PcmSpeaker) pump(out []byte) error {
	if strings.EqualFold(strings.TrimSpace(p.target), "athena") {
		out = p.athenaUpsampler.expand(out)
	}
	if p.playbackDriftCorrectionActive() {
		return p.session.Pump(p.playbackDrift.expand(out))
	}
	writeFrames := p.alsaWriteFrames
	if writeFrames <= 0 {
		writeFrames = alsaPeriodSize
	}
	chunk := writeFrames * 4 // stereo S16
	for off := 0; off < len(out); off += chunk {
		end := off + chunk
		if end > len(out) {
			end = len(out)
		}
		if err := p.session.Pump(out[off:end]); err != nil {
			return err
		}
	}
	return nil
}

// report logs and forwards a completed stream's stats. A nil st is an
// underrun, which is counted inside the stream and only logged here.
func (p *PcmSpeaker) report(st *StreamStats, plane string) {
	if st == nil {
		log.Printf("[speaker] UNDERRUN: %s channel drained mid-stream — injecting silence", plane)
		return
	}
	if p.chain.Active() {
		// What the chain DID over the stream, as em_eq.describe_activity
		// reports it controller-side. Maxima since the last stream ended.
		cs := p.chain.TakeStats()
		log.Printf("[speaker] %s output chain: guard_reduction=%.2fdB limiter_reduction=%.2fdB clipped=%d/%d bypassed",
			plane, cs.GuardReductionDb, cs.LimiterReductionDb, cs.Clipped, cs.ClippedBypassed)
	}
	log.Printf("[speaker] %s stream complete — returning to silence "+
		"(periods=%d underruns=%d minDepth=%d primeWait=%dms recvSpan=%dms maxGap=%dms)",
		plane, st.Periods, st.Underruns, st.MinDepth,
		st.PrimeWaitMs, st.RecvSpanMs, st.MaxGapMs)
	// Only the voice plane reports upstream: the controller attaches these
	// to the turn that produced them (device.last_turn_id), and a music
	// stream ending would overwrite a turn's delivery figures with a song's.
	if plane != "voice" || st.Periods == 0 {
		return
	}
	p.statsMu.Lock()
	cb := p.statsCb
	p.statsMu.Unlock()
	if cb != nil {
		go cb(*st)
	}
}

// toStereo converts a mono S16 wire period into the stereo frames the ALSA
// device requires. The stereo config is an I2S/codec-path constraint, not a
// wire one — shipping two identical channels would double bandwidth for
// nothing on links that are already marginal.
func toStereo(data []byte) []byte {
	n := len(data) / 2
	period := make([]byte, n*4)
	for i := 0; i < n; i++ {
		lo, hi := data[i*2], data[i*2+1]
		period[i*4+0], period[i*4+1] = lo, hi // L
		period[i*4+2], period[i*4+3] = lo, hi // R
	}
	return period
}

// PumpPeriod queues one period of VOICE audio (TTS, announcements). Called by
// the WS client for each incoming 0x02 frame. Blocks until the ALSA loop has
// consumed a slot (rate-limiting to playback speed), or returns an error if
// that loop has died — preventing an infinite block on a dead consumer.
func (p *PcmSpeaker) PumpPeriod(data []byte) error {
	_, err := p.voice.pump(toStereo(data), len(data))
	return err
}

// PumpMusic queues one period of MUSIC audio (0x04). Identical handling to
// the voice plane — it is a full stream with its own prime gate, buffer and
// discard semantics, not a lesser one — but kept separate so a voice turn
// can duck it rather than stopping it.
func (p *PcmSpeaker) PumpMusic(data []byte) error {
	_, err := p.music.pump(toStereo(data), len(data))
	return err
}

// SetDuck sets the gain applied to music while it plays under voice, in dB of
// attenuation (0 = no ducking). The change is ramped by the mixer rather than
// applied at once, because a gain step at a period boundary is a click — and
// it would land on exactly the moment the user started speaking.
func (p *PcmSpeaker) SetDuck(db float64) {
	p.SetDuckRamp(db, time.Duration(periodSize)*time.Second/48000)
}

// SetDuckRamp changes music gain over the exact requested renderer duration.
// The request is atomic and the ALSA goroutine owns the envelope itself.
func (p *PcmSpeaker) SetDuckRamp(db float64, duration time.Duration) {
	frames := int64(math.Round(duration.Seconds() * 48000))
	if duration > 0 && frames < 1 {
		frames = 1
	}
	p.duckRampFrames.Store(frames)
	p.duckTarget.Store(DuckGain(db))
	p.duckRevision.Add(1)
}

// WaitDuckRamp waits until the ALSA renderer has actually consumed the whole
// sample-counted gain envelope most recently requested by SetDuckRamp. The
// control goroutine cannot safely approximate this with a sleep because the
// render loop may not observe the request until its next period.
func (p *PcmSpeaker) WaitDuckRamp(ctx context.Context) error {
	revision := p.duckRevision.Load()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if p.duckCompleted.Load() >= revision {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.deadCh:
			return errors.New("speaker playback loop stopped")
		case <-ticker.C:
		}
	}
}

// SetOutputChain sets the output chain's configuration; it lands on the next
// period, keeping filter and limiter state, so a change mid-song is heard
// within ~43ms and does not click.
func (p *PcmSpeaker) SetOutputChain(params outchain.Params) { p.chain.SetParams(params) }

// SetOutputChainActive hands the output chain to this device (true) or back
// to the controller (false). Legacy sessions enable it only when the
// controller announces `output_chain`; direct Tater-native playback is
// unprocessed and therefore enables it locally. The two paths must not both
// process the same audio, because that would double the EQ curve and limiter.
func (p *PcmSpeaker) SetOutputChainActive(on bool) {
	if on == p.chain.Active() {
		return
	}
	where := "the controller"
	if on {
		where = "this device"
	}
	log.Printf("[speaker] output chain now runs on %s", where)
	p.chain.SetActive(on)
}

// VoiceAudible reports whether a VOICE stream is audible: arriving, queued,
// or played within hold. The hold lets a caller cover what follows the last
// period out of the speaker, such as a model whose window still holds echo.
//
// It replaced IsStreaming, which reported only "still arriving on the wire"
// and so dropped the barge bar ~0.1s into a 3s reply (2026-09-22).
//
// Added for on-device wake word scoring: while the speaker is playing, the
// controller lowers its wake threshold to bargeInThreshold, because echo at the
// mic is ~25dB louder than the person and speech-over-TTS scores are depressed.
// The device has to know when it is playing in order to mirror that, or it
// disagrees with the controller on every barge-in.
//
// Music deliberately does NOT count here. It is a quieter, continuous bed
// rather than a response the user is talking over, and the controller applies
// the same rule on its side (wake-over-music scores against bargeInThreshold
// only when barge-in is enabled). Reporting music as "streaming" would drop
// the device's bar for as long as a song plays.
func (p *PcmSpeaker) VoiceAudible(hold time.Duration) bool {
	return p.voice.playedWithin(time.Now(), hold)
}

// WaitVoiceIdle blocks until all queued voice audio has actually left the
// speaker. PumpPeriod only waits for room in the deep queue, so returning as
// soon as the final period is queued can precede the audible end by several
// seconds. The small hold also covers the final ~85ms already handed to ALSA.
func (p *PcmSpeaker) WaitVoiceIdle(ctx context.Context) error {
	const hardwareDrainHold = 150 * time.Millisecond
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for p.VoiceAudible(hardwareDrainHold) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	return nil
}

// EndStream marks the in-flight voice stream complete (0x03). Always arrives
// after every 0x02 period of that stream has been handed to PumpPeriod —
// frames are processed sequentially on the read loop — so by the time the
// ALSA loop drains the buffer the flag is already set.
func (p *PcmSpeaker) EndStream() { p.voice.endStream() }

// SetVoiceLive switches the foreground voice plane between buffered TTS and
// low-latency live audio. It does not alter the music plane or ALSA cadence.
func (p *PcmSpeaker) SetVoiceLive(active bool) {
	if active {
		p.voicePrime.Store(3)
		return
	}
	p.voicePrime.Store(primePeriods)
}

// SetMusicLive selects the short startup gate used by locally decoded video.
// It changes buffering only; video audio remains on the normal music plane so
// TTS ducking, AEC reference, volume, EQ, and limiting stay unchanged.
func (p *PcmSpeaker) SetMusicLive(active bool) {
	if active {
		p.musicPrime.Store(1)
		return
	}
	p.musicPrime.Store(primePeriods)
}

// EndMusicStream marks the in-flight music stream complete (0x05).
func (p *PcmSpeaker) EndMusicStream() { p.music.endStream() }

// Flush cuts a playing VOICE stream immediately (barge-in). Two parts:
//  1. Drain the buffer — kills up to ~5.5s already queued on-device.
//  2. Arm discarding (if a stream is mid-flight) — subsequent periods of
//     this stream are dropped until its EOS arrives. Necessary because the
//     controller writes the whole response into the WebSocket ahead of
//     playback: at barge time the rest of the stream is already in TCP
//     buffers and would refill the channel right after the drain (the
//     pre-2026-07-08 version drained only, and playback resumed after a
//     ~1.3s skip). The controller sends the EOS on the cancel path too, so
//     the discard always terminates.
//
// Up to alsaBufferFrames (~85ms) already handed to the hardware
// still play — cutting those needs a stream restart, which costs more in
// click/pop than it saves.
func (p *PcmSpeaker) Flush() { p.voice.flush() }

// FlushMusic stops music the same way. This is now reserved for the user
// GENUINELY stopping or pausing playback: a voice turn ducks instead, which
// is the whole point of holding the two planes apart. Flushing here would
// throw away the buffered audio that makes ducking instant, and on a
// non-seekable stream that audio cannot be recovered.
func (p *PcmSpeaker) FlushMusic() { p.music.flush() }

// Close shuts the speaker down in the reverse of Init's bring-up: mute,
// amp off, then tear the stream down. Muting first makes the PCM-close
// transient inaudible, and leaving the amp off means an idle DAC can't
// hiss while the server isn't running (OTA gaps, crashes, service stop —
// the "speaker noise between OTAs"). start_server.sh repeats the mute +
// amp-off after every server exit as a belt-and-braces for paths where
// this never runs (SIGKILL, panic).
func (p *PcmSpeaker) Close() {
	p.SetMusicSource(nil)
	mixer.SetPlaybackLevel(0, 127) // mute
	if isRadarPlaybackTarget(p.target) {
		mixer.Set("MFP Gpio Mute", "On")
	}
	mixer.SetSpeakerEnabled(false) // amp off
	close(p.stopCh)
	p.session.Close()
	if p.dramHold != nil {
		p.dramHold.Close()
	}
	log.Println("PcmSpeaker closed — output muted, amp off")
}
