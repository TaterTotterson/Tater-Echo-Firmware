package client

import (
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/aec"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/beamformer"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/config"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/processor"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/wakeword/microwakeword"
	"github.com/TaterTotterson/Tater-Echo-Firmware/pkg/mic"
	"github.com/TaterTotterson/Tater-Echo-Firmware/pkg/speaker"
)

const doaActivityPreGainThreshold = 0.0001

// doaActivity uses the minimum calibrated pre-gain RMS rather than a fraction
// of the turn VAD threshold. Its 0.004 boot default is far above the
// measured 0.0001–0.0006 FS speech range. Scaling this floor by the current
// fixed mic gain keeps the decision stable when gain changes.
func doaActivity(rms, gain float64, speech bool) bool {
	return speech || rms >= doaActivityPreGainThreshold*gain
}

const (
	wakeChunkBytes        = 1280 * 2 // 2560 bytes = 80ms
	wakeLaneCaptureFrames = 38       // just over 3s, matching trainer capture history
	// The verifier gets a phrase-aligned two-second view even when dual wake
	// agreement completes later. Twenty-five 80ms frames is the protocol's
	// maximum and covers the slower "Hey Tater" utterances that a one-second
	// callback-relative tail can reduce to only "Tater".
	wakeVerifierCaptureFrames = 25
	// A little post-crossing context helps short-word STT without allowing a
	// delayed OWW callback to slide the whole window past the phrase. When that
	// context has not arrived yet, selection simply fills from older frames.
	wakeVerifierPostRoll = 200 * time.Millisecond
	// The speaker reports music synchronously from the mic loop. A short hold
	// spans the final ALSA-buffered period and normal Sendspin scheduling gaps,
	// preventing the stronger residual guard from fluttering between chunks.
	musicAECHold = 300 * time.Millisecond
)

func vadPeriodRMS(mono []byte) float64 {
	n := len(mono) / 2
	if n == 0 {
		return 0
	}
	var sum float64
	for i := 0; i < n; i++ {
		s := int16(binary.LittleEndian.Uint16(mono[i*2:]))
		f := float64(s) / 32768.0
		sum += f * f
	}
	return math.Sqrt(sum / float64(n))
}

// prepareWakeLanePCM keeps the continuous wake detector off the real-time
// playback critical path. During playback, mainPCM has already passed through
// the target's one AEC path; running the directional wake candidate through a
// second retained hardware-reference filter caused Biscuit/Radar and the
// hardware-loopback displays to switch AEC paths twice per mic batch and
// starve ALSA. The software-reference targets already needed this same reuse
// because their far-end ring can only be consumed once. With no playback there
// is no echo to cancel, so the directional candidate remains the better wake
// signal. A missing candidate always falls back to the main stream.
func prepareWakeLanePCM(candidate, mainPCM []byte, playbackActive bool) []byte {
	if len(candidate) == 0 {
		return append([]byte(nil), mainPCM...)
	}
	if playbackActive {
		return append(candidate[:0], mainPCM...)
	}
	return candidate
}

// ─── DataClient ───────────────────────────────────────────────────────────────

// Beam lock request states — see DataClient.beamReq.
const (
	beamReqNone         int32 = 0
	beamReqLock         int32 = 1
	beamReqUnlock       int32 = 2
	beamReqLockOnSpeech int32 = 3
)

type DataClient struct {
	mic mic.Subscribable
	spk speaker.Speaker

	micMu     sync.Mutex
	micActive bool
	micStopCh chan struct{}

	// beamReq carries a pending beam lock/unlock request from the native
	// state callback to the mic goroutine. Beamformer methods are not safe
	// to call from other goroutines (same reason beam.Unlock is deferred
	// inside streamMic rather than called from StopMic), so the native state
	// handler only sets this flag; streamMic consumes it with Swap at the
	// top of each period. This locks the beamformer onto the
	// speaker's perimeter mic mid-stream at wake detection — wake-triggered
	// turns don't restart the stream (P0-1), so without this they ran the
	// entire turn on ch6 omni and the mic array did nothing for them.
	beamReq int32

	beam              beamformer.FrontEnd
	proc              *processor.Processor
	wakeProc          *processor.Processor
	aec               *aec.Canceller
	onDirectionChange func(angle float64, activity bool, speech bool)
	directionMu       sync.Mutex

	// pcmObserver receives the native 80 ms transport stream. Local MWW and
	// OWW share one continuous primary beamformed stream; there is never a
	// second detector lane or model reset.
	pcmObserverMu       sync.RWMutex
	pcmObserver         func([]byte)
	pcmActivityObserver func([]byte, bool)

	// MWW and OWW observe the same continuous primary wake beam. Dual mode keeps
	// one instance of each model warm in parallel so agreement adds no replay
	// delay. Direction changes never reset either model or splice pre-roll.
	mwwShadowMu        sync.Mutex
	mwwShadowScorers   []*microwakeword.ShadowScorer
	owwShadowScorers   []*microwakeword.ShadowScorer
	wakePlaybackActive atomic.Bool
	wakeLaneDir        [2]atomic.Int32
	wakeWinnerDir      atomic.Int32
	wakeWinnerValid    atomic.Bool
	wakeLastClaimNS    atomic.Int64
	wakeAssignmentMu   sync.RWMutex
	wakeAssignments    [2][]wakeLaneAssignment
	wakeAudioMu        sync.Mutex
	wakeLaneAudio      [2][]wakeAudioFrame
	wakeWinnerAudio    [][]byte
	wakeWinnerPostWake [][]byte
	wakeVerifierAudio  [][]byte

	// Hardware echo reference detection (#385). Supported capture front ends
	// expose one playback-loopback channel from the same frame as the microphone
	// samples, which makes it a far-end reference that needs no software-ring
	// alignment. Touched only from the mic goroutine.
	//
	// DETECTED, NEVER ASSUMED, and the discriminator is deliberately narrow:
	// a reference channel is BIT-EXACT ZERO when nothing is playing and
	// carries audio when something is, and only both facts together confirm
	// it. "Has energy" alone would promote a genuine microphone on a board
	// that wires ch8 differently, and cancelling the near-end against
	// another mic is far worse than not cancelling at all. A board that
	// fails either test simply keeps the software tap.
	hwRefSeenSilent bool
	hwRefSeenAudio  bool
	hwRefOn         bool
	echoRefScratch  []byte // reused until the synchronous AEC call returns

	// hwRefMode is the operator's override, config.AecRef{Auto,HW,SW}. It
	// is written from the native settings goroutine, so it is atomic while the rest stay
	// plain: they are the detector's own state and never leave the mic
	// goroutine.
	hwRefMode atomic.Int32

	// pipeMu serialises access to beam and proc, which hold unsynchronised
	// per-period state (reused analysis buffers, EWMA smoothers, AGC gain).
	// Both are normally touched by a single streamMic goroutine, but a quick
	// mute/unmute can start the replacement while the old goroutine drains a
	// final period. The lock keeps their processor state disjoint.
	pipeMu sync.Mutex
}

type musicAudibleSpeaker interface {
	MusicAudible(hold time.Duration) bool
}

type wakeAudioFrame struct {
	at  time.Time
	pcm []byte
}

// WakeAudioCapture keeps the detector-crossing boundary and phrase-aligned
// verifier clip independent. PreRoll ends with the frame that crossed;
// PostWake contains later audio captured while model agreement completed.
type WakeAudioCapture struct {
	PreRoll  [][]byte
	PostWake [][]byte
	Verifier [][]byte
}

type wakeLaneAssignment struct {
	from      time.Time
	direction int32
}

// NewDataClient wires the mic/speaker pipeline. canceller is the shared AEC
// instance — its far-end side is fed by the speaker's echo tap; this client
// runs its near-end side on the mono mic stream. Disabled cancellers pass
// audio through untouched.
func NewDataClient(deviceID string, microphone mic.Subscribable, spk speaker.Speaker, canceller *aec.Canceller) *DataClient {
	return NewDataClientForTarget(deviceID, microphone, spk, canceller, "biscuit")
}

// NewDataClientForTarget wires the shared pipeline to the board's raw-capture
// front end. The default constructor remains Biscuit for tests and existing
// callers; release startup always supplies its compiled firmware target.
func NewDataClientForTarget(deviceID string, microphone mic.Subscribable, spk speaker.Speaker, canceller *aec.Canceller, target string) *DataClient {
	_ = deviceID // retained in the constructor contract for hardware tools
	d := &DataClient{
		mic:      microphone,
		spk:      spk,
		beam:     beamformer.NewForTarget(target),
		proc:     processor.New(),
		wakeProc: processor.New(),
		aec:      canceller,
	}
	for lane := range d.wakeLaneDir {
		d.wakeLaneDir[lane].Store(-1)
	}
	d.wakeWinnerDir.Store(-1)
	// Seeded from the local default; native settings may supersede it.
	d.hwRefMode.Store(aecRefModeOf(config.Get().Snapshot().AecRefSource))
	return d
}

// Far-end reference override, as an int so it can be atomic. Mirrors
// config.AecRef{Auto,HW,SW} — converted once on the control goroutine rather
// than string-compared per period on the mic goroutine.
const (
	hwRefAuto int32 = iota
	hwRefForceHW
	hwRefForceSW
)

func aecRefModeOf(v string) int32 {
	switch v {
	case config.AecRefHW:
		return hwRefForceHW
	case config.AecRefSW:
		return hwRefForceSW
	default:
		return hwRefAuto
	}
}

// SetAecRefSource applies the operator's far-end reference override. Called
// from the control goroutine on every config push; a no-op when unchanged.
//
// The SWITCH itself is left to the mic goroutine (noteEchoRef), which owns
// the canceller's source. Flipping it here would race a period already
// mid-flight: ProcessWithRef checks the canceller's own hwRef flag, and
// changing that between the caller's extraction and the call turns a
// perfectly good reference into a "missing or mismatched" pass-through.
func (d *DataClient) SetAecRefSource(v string) {
	mode := aecRefModeOf(v)
	if d.hwRefMode.Swap(mode) == mode {
		return
	}
	log.Printf("[aec] far-end reference source: %s", v)
}

// noteEchoRef feeds one period of the candidate reference channel to the
// detector and reports whether the hardware path should be used for it.
//
// Confirmation is one-way. Once the candidate has been seen both bit-exact
// silent and carrying audio, it is a reference and stays one: the alternative
// is a device that flips sources mid-stream every time the room goes quiet,
// which throws away a converged filter for nothing.
func (d *DataClient) noteEchoRef(ref []byte) bool {
	switch d.hwRefMode.Load() {
	case hwRefForceSW:
		// Reversible, unlike the auto latch below: this is somebody running
		// an A/B, and a pin that could not be undone without a reboot would
		// make the comparison a one-way trip.
		if d.hwRefOn {
			d.hwRefOn = false
			d.aec.SetHardwareRef(false)
		}
		return false
	case hwRefForceHW:
		if !d.hwRefOn {
			d.hwRefOn = true
			d.aec.SetHardwareRef(true)
		}
		return true
	}
	if d.hwRefOn {
		return true
	}
	if ref == nil {
		return false
	}
	silent := true
	for _, b := range ref {
		if b != 0 {
			silent = false
			break
		}
	}
	if silent {
		d.hwRefSeenSilent = true
	} else {
		d.hwRefSeenAudio = true
	}
	if d.hwRefSeenSilent && d.hwRefSeenAudio {
		d.hwRefOn = true
		d.aec.SetHardwareRef(true)
		log.Printf("[aec] capture channel confirmed as the playback loopback " +
			"(bit-exact silent when idle, audio when playing) — " +
			"using the frame-aligned hardware reference")
		return true
	}
	return false
}

// SetMWWShadowScorer preserves the single-scorer API for non-array callers.
func (d *DataClient) SetMWWShadowScorer(s *microwakeword.ShadowScorer) {
	if s == nil {
		d.SetMWWShadowScorers(nil)
		return
	}
	d.SetMWWShadowScorers([]*microwakeword.ShadowScorer{s})
}

// SetMWWShadowScorers atomically installs the wake scorer bank. Old engines
// close only after the pointer lock is released, so the microphone goroutine
// never waits for native inference while holding the bank lock.
func (d *DataClient) SetMWWShadowScorers(scorers []*microwakeword.ShadowScorer) {
	d.SetWakeShadowScorers(scorers, nil)
}

// SetWakeShadowScorers atomically installs the independently streaming MWW and
// OWW banks. Installing one MWW bank deliberately clears OWW in the same swap.
func (d *DataClient) SetWakeShadowScorers(mww, oww []*microwakeword.ShadowScorer) {
	d.mwwShadowMu.Lock()
	playbackActive := d.wakePlaybackActive.Load()
	for _, scorer := range mww {
		if scorer != nil {
			scorer.SetPlaybackActive(playbackActive)
		}
	}
	for _, scorer := range oww {
		if scorer != nil {
			scorer.SetPlaybackActive(playbackActive)
		}
	}
	oldMWW := d.mwwShadowScorers
	oldOWW := d.owwShadowScorers
	d.mwwShadowScorers = append([]*microwakeword.ShadowScorer(nil), mww...)
	d.owwShadowScorers = append([]*microwakeword.ShadowScorer(nil), oww...)
	d.mwwShadowMu.Unlock()
	for _, scorer := range append(oldMWW, oldOWW...) {
		if scorer != nil {
			scorer.Close()
		}
	}
	d.resetWakeLaneAudio()
}

func (d *DataClient) setWakePlaybackActive(active bool) {
	if d.wakePlaybackActive.Swap(active) == active {
		return
	}
	d.mwwShadowMu.Lock()
	defer d.mwwShadowMu.Unlock()
	for _, scorer := range d.mwwShadowScorers {
		if scorer != nil {
			scorer.SetPlaybackActive(active)
		}
	}
	for _, scorer := range d.owwShadowScorers {
		if scorer != nil {
			scorer.SetPlaybackActive(active)
		}
	}
}

// MWWShadowScorer returns the currently active Tater microWakeWord observer.
func (d *DataClient) MWWShadowScorer() *microwakeword.ShadowScorer {
	d.mwwShadowMu.Lock()
	defer d.mwwShadowMu.Unlock()
	if len(d.mwwShadowScorers) == 0 {
		return nil
	}
	return d.mwwShadowScorers[0]
}

// MWWShadowScorers snapshots the scorer bank without exposing its backing
// slice to a concurrent config replacement.
func (d *DataClient) MWWShadowScorers() []*microwakeword.ShadowScorer {
	d.mwwShadowMu.Lock()
	defer d.mwwShadowMu.Unlock()
	return append([]*microwakeword.ShadowScorer(nil), d.mwwShadowScorers...)
}

func (d *DataClient) OWWShadowScorers() []*microwakeword.ShadowScorer {
	d.mwwShadowMu.Lock()
	defer d.mwwShadowMu.Unlock()
	return append([]*microwakeword.ShadowScorer(nil), d.owwShadowScorers...)
}

func (d *DataClient) WakeShadowScorers() (mww, oww []*microwakeword.ShadowScorer) {
	d.mwwShadowMu.Lock()
	defer d.mwwShadowMu.Unlock()
	return append([]*microwakeword.ShadowScorer(nil), d.mwwShadowScorers...),
		append([]*microwakeword.ShadowScorer(nil), d.owwShadowScorers...)
}

// WakeScorerCount is one on every target. Wake models need a stable continuous
// signal; a primary beam may follow the array estimator, but direction changes
// never create a second scorer or reset either neural detector.
func (d *DataClient) WakeScorerCount() int {
	return 1
}

// ClaimWakeLane accepts a crossing from the single continuous wake stream and
// snapshots both the command pre-roll and a phrase-aligned verifier clip. The
// detector timestamp, not callback time, also selects the bearing carried into
// the subsequent voice turn.
func (d *DataClient) ClaimWakeLane(lane int, capturedAt time.Time) bool {
	if lane < 0 || lane >= len(d.wakeLaneDir) {
		lane = 0
	}
	now := time.Now().UnixNano()
	for {
		last := d.wakeLastClaimNS.Load()
		if last > 0 && now-last < int64(microwakeword.DefaultShadowRefractory) {
			return false
		}
		if d.wakeLastClaimNS.CompareAndSwap(last, now) {
			break
		}
	}
	direction := -1
	if _, ok := d.beam.(beamformer.WakeArray); ok {
		direction = d.WakeLaneDirectionAt(lane, capturedAt)
		d.wakeWinnerDir.Store(int32(direction))
		d.wakeWinnerValid.Store(true)
	}
	d.snapshotWinningWakeAudio(lane, capturedAt)
	return true
}

func (d *DataClient) WakeLaneDirectionAt(lane int, capturedAt time.Time) int {
	direction := int(d.wakeLaneDir[lane].Load())
	d.wakeAssignmentMu.RLock()
	assignments := d.wakeAssignments[lane]
	for i := len(assignments) - 1; i >= 0; i-- {
		if !capturedAt.Before(assignments[i].from) {
			direction = int(assignments[i].direction)
			break
		}
	}
	d.wakeAssignmentMu.RUnlock()
	return direction
}

func (d *DataClient) recordWakeLaneAudio(lane int, capturedAt time.Time, pcm []byte) {
	if lane < 0 || lane >= len(d.wakeLaneAudio) || len(pcm) == 0 {
		return
	}
	frame := wakeAudioFrame{at: capturedAt, pcm: append([]byte(nil), pcm...)}
	d.wakeAudioMu.Lock()
	roll := d.wakeLaneAudio[lane]
	if len(roll) >= wakeLaneCaptureFrames {
		copy(roll, roll[len(roll)-wakeLaneCaptureFrames+1:])
		roll = roll[:wakeLaneCaptureFrames-1]
	}
	d.wakeLaneAudio[lane] = append(roll, frame)
	d.wakeAudioMu.Unlock()
}

func cloneWakeAudioFrames(frames []wakeAudioFrame) [][]byte {
	result := make([][]byte, len(frames))
	for index := range frames {
		result[index] = append([]byte(nil), frames[index].pcm...)
	}
	return result
}

func clonePCMFrames(frames [][]byte) [][]byte {
	result := make([][]byte, len(frames))
	for index := range frames {
		result[index] = append([]byte(nil), frames[index]...)
	}
	return result
}

// splitWakeAudioAt places the detector-crossing frame on the wake side of the
// protocol boundary. Later frames may already contain an immediate command,
// especially while a second wake model is completing agreement.
func splitWakeAudioAt(frames []wakeAudioFrame, capturedAt time.Time) (preRoll, postWake [][]byte) {
	boundary := len(frames)
	if !capturedAt.IsZero() {
		boundary = 0
		for boundary < len(frames) && !frames[boundary].at.After(capturedAt) {
			boundary++
		}
	}
	return cloneWakeAudioFrames(frames[:boundary]), cloneWakeAudioFrames(frames[boundary:])
}

// verifierWakeAudio selects at most two seconds ending no later than 200ms
// after the detector crossing. Inference and dual-model agreement may call
// back much later; those callback-delay frames must not displace "Hey".
func verifierWakeAudio(frames []wakeAudioFrame, capturedAt time.Time) [][]byte {
	if len(frames) == 0 {
		return nil
	}
	end := len(frames)
	if !capturedAt.IsZero() {
		latest := capturedAt.Add(wakeVerifierPostRoll)
		end = 0
		for end < len(frames) && !frames[end].at.After(latest) {
			end++
		}
		if end == 0 {
			return nil
		}
	}
	start := 0
	if end > wakeVerifierCaptureFrames {
		start = end - wakeVerifierCaptureFrames
	}
	return cloneWakeAudioFrames(frames[start:end])
}

func (d *DataClient) snapshotWinningWakeAudio(lane int, capturedAt time.Time) {
	d.wakeAudioMu.Lock()
	d.wakeWinnerAudio = nil
	d.wakeWinnerPostWake = nil
	d.wakeVerifierAudio = nil
	if lane >= 0 && lane < len(d.wakeLaneAudio) {
		frames := d.wakeLaneAudio[lane]
		d.wakeWinnerAudio, d.wakeWinnerPostWake = splitWakeAudioAt(frames, capturedAt)
		d.wakeVerifierAudio = verifierWakeAudio(frames, capturedAt)
	}
	d.wakeAudioMu.Unlock()
}

// TakeWinningWakeCapture returns independent immutable views for command STT
// and wake verification. It is one-shot: a later wake must claim a fresh
// stream and detector timestamp.
func (d *DataClient) TakeWinningWakeCapture() WakeAudioCapture {
	d.wakeAudioMu.Lock()
	defer d.wakeAudioMu.Unlock()
	capture := WakeAudioCapture{
		PreRoll:  clonePCMFrames(d.wakeWinnerAudio),
		PostWake: clonePCMFrames(d.wakeWinnerPostWake),
		Verifier: clonePCMFrames(d.wakeVerifierAudio),
	}
	d.wakeWinnerAudio = nil
	d.wakeWinnerPostWake = nil
	d.wakeVerifierAudio = nil
	return capture
}

// TakeWinningWakeAudio preserves the established command-pre-roll API for
// callers that do not need a separately anchored verifier clip.
func (d *DataClient) TakeWinningWakeAudio() [][]byte {
	capture := d.TakeWinningWakeCapture()
	return append(capture.PreRoll, capture.PostWake...)
}

// CopyWinningWakeAudio returns the same immutable wake-stream snapshot without
// consuming it. The accepted wake then still hands the original to
// verifier/trainer/STT.
func (d *DataClient) CopyWinningWakeAudio() [][]byte {
	d.wakeAudioMu.Lock()
	defer d.wakeAudioMu.Unlock()
	frames := clonePCMFrames(d.wakeWinnerAudio)
	return append(frames, clonePCMFrames(d.wakeWinnerPostWake)...)
}

func (d *DataClient) CopyWinningWakeVerifierAudio() [][]byte {
	d.wakeAudioMu.Lock()
	defer d.wakeAudioMu.Unlock()
	frames := make([][]byte, len(d.wakeVerifierAudio))
	for index := range d.wakeVerifierAudio {
		frames[index] = append([]byte(nil), d.wakeVerifierAudio[index]...)
	}
	return frames
}

// ExtendWinningWakeAudio adds command audio captured while wake agreement was
// being completed, while rebuilding the verifier view around the original
// detector crossing. Callback-delay audio can therefore help command pre-roll
// without pushing the first wake word out of STT verification.
func (d *DataClient) ExtendWinningWakeAudio(lane int, capturedAt time.Time) {
	if lane < 0 || lane >= len(d.wakeLaneAudio) {
		return
	}
	d.wakeAudioMu.Lock()
	defer d.wakeAudioMu.Unlock()
	frames := d.wakeLaneAudio[lane]
	d.wakeWinnerAudio, d.wakeWinnerPostWake = splitWakeAudioAt(frames, capturedAt)
	d.wakeVerifierAudio = verifierWakeAudio(frames, capturedAt)
}

func (d *DataClient) resetWakeLaneAudio() {
	d.wakeAudioMu.Lock()
	for lane := range d.wakeLaneAudio {
		d.wakeLaneAudio[lane] = nil
	}
	d.wakeWinnerAudio = nil
	d.wakeWinnerPostWake = nil
	d.wakeVerifierAudio = nil
	d.wakeAudioMu.Unlock()
}

func (d *DataClient) assignWakeLane(lane, direction int, from time.Time) {
	if lane < 0 || lane >= len(d.wakeLaneDir) {
		return
	}
	d.wakeLaneDir[lane].Store(int32(direction))
	d.wakeAssignmentMu.Lock()
	history := append(d.wakeAssignments[lane], wakeLaneAssignment{from: from, direction: int32(direction)})
	if len(history) > 8 {
		history = append(history[:0], history[len(history)-8:]...)
	}
	d.wakeAssignments[lane] = history
	d.wakeAssignmentMu.Unlock()
}

func (d *DataClient) OnDirectionChanged(cb func(angle float64, activity bool, speech bool)) {
	d.directionMu.Lock()
	d.onDirectionChange = cb
	d.directionMu.Unlock()
}

// OnPCM installs a non-blocking observer for processed 16 kHz mono S16_LE
// chunks. The callback runs on the microphone goroutine and therefore must
// copy or enqueue the slice before returning.
func (d *DataClient) OnPCM(cb func([]byte)) {
	d.pcmObserverMu.Lock()
	d.pcmObserver = cb
	d.pcmObserverMu.Unlock()
}

// OnPCMWithSpeech installs the same observer with the VAD decision belonging
// to the source microphone batch. Native Sat Chat uses this to reduce only
// the silence between utterances; the audio delivered to wake detection and
// ordinary Tater voice turns is unchanged.
func (d *DataClient) OnPCMWithSpeech(cb func([]byte, bool)) {
	d.pcmObserverMu.Lock()
	d.pcmActivityObserver = cb
	d.pcmObserverMu.Unlock()
}

func (d *DataClient) observePCM(pcm []byte, speech bool) {
	d.pcmObserverMu.RLock()
	cb := d.pcmObserver
	activityCB := d.pcmActivityObserver
	d.pcmObserverMu.RUnlock()
	if cb != nil {
		cb(pcm)
	}
	if activityCB != nil {
		activityCB(pcm, speech)
	}
}

// RequestBeamLock asks the running mic stream to lock the beamformer onto
// the best perimeter mic (respecting BeamformingEnabled config). Safe to call
// from any goroutine; consumed by streamMic on its next period. A later
// request overwrites an unconsumed earlier one.
func (d *DataClient) RequestBeamLock() {
	atomic.StoreInt32(&d.beamReq, beamReqLock)
}

func (d *DataClient) lockWakeOrBest(enabled bool) {
	valid := d.wakeWinnerValid.Swap(false)
	direction := int(d.wakeWinnerDir.Swap(-1))
	if array, ok := d.beam.(beamformer.WakeArray); ok && valid && direction >= 0 {
		array.LockWakeDirection(direction, enabled)
		return
	}
	d.beam.Lock(enabled)
}

// RequestBeamLockOnSpeech releases any inherited bearing, exposes live visual
// DOA, and waits for near-end speech before choosing the audio pickup. Native
// initial and continued-chat turns deliberately share this path so their
// listening animations cannot diverge.
func (d *DataClient) RequestBeamLockOnSpeech() {
	atomic.StoreInt32(&d.beamReq, beamReqLockOnSpeech)
}

// RequestBeamUnlock asks the running mic stream to release the beam lock and
// return to ch6 omni. Safe to call from any goroutine.
func (d *DataClient) RequestBeamUnlock() {
	d.wakeWinnerValid.Store(false)
	d.wakeWinnerDir.Store(-1)
	atomic.StoreInt32(&d.beamReq, beamReqUnlock)
}

// ApplyNativeBeamState deliberately routes every native listening turn
// through the same speech-following path. Initial wake and continued-chat
// listening both unlock first, expose live visual DOA immediately, then choose
// the audio pickup when near-end speech arrives.
func (d *DataClient) ApplyNativeBeamState(state string) {
	switch state {
	case "listening":
		if d.wakeWinnerValid.Load() {
			d.RequestBeamLock()
		} else {
			d.RequestBeamLockOnSpeech()
		}
	case "idle", "error":
		d.RequestBeamUnlock()
	}
}

// StartLocalMic starts the permanent Tater-native wake and voice pipeline.
func (d *DataClient) StartLocalMic() {
	d.micMu.Lock()
	defer d.micMu.Unlock()
	if d.micActive {
		return
	}
	d.micActive = true
	d.micStopCh = make(chan struct{})
	go d.streamMic(d.micStopCh)
	log.Println("[data] local microphone pipeline started")
}

func (d *DataClient) StopMic() {
	d.stopMic()
}

// MicrophonePipelineActive reports whether the device currently owns and is
// draining the microphone stream. Unlike TurnStreamActive it includes the
// permanent local wake-word pipeline, which is the important signal when
// diagnosing a mute state that looks unmuted but cannot hear a wake word.
func (d *DataClient) MicrophonePipelineActive() bool {
	d.micMu.Lock()
	defer d.micMu.Unlock()
	return d.micActive
}

func (d *DataClient) stopMic() {
	d.micMu.Lock()
	defer d.micMu.Unlock()
	if !d.micActive {
		return
	}
	close(d.micStopCh)
	d.micActive = false
	// beam.Unlock() is deferred inside streamMic — always runs on the mic
	// goroutine, eliminating the data race with Process() and Lock().
	log.Println("[data] Mic streaming stopped")
}

// streamMic owns the permanent local wake and Tater-native voice pipeline.
func (d *DataClient) streamMic(stopCh <-chan struct{}) {
	if d.mic == nil {
		log.Println("[data] streamMic: no mic")
		return
	}

	// Only the current pipeline may clear ownership or unlock the beam. A quick
	// mute/unmute can start its replacement before this goroutine observes stop.
	defer func() {
		d.micMu.Lock()
		owner := d.micStopCh == stopCh
		if owner {
			d.micActive = false
		}
		d.micMu.Unlock()
		if owner {
			d.pipeMu.Lock()
			d.beam.Unlock()
			d.pipeMu.Unlock()
		}
		log.Println("[data] streamMic: exited")
	}()

	// Native turns reuse this continuous stream. A replacement caused by
	// mute/unmute starts from clean beam and processor state.
	d.pipeMu.Lock()
	d.beam.Unlock()
	d.proc.ResetAGC()
	d.wakeProc.ResetAGC()
	d.pipeMu.Unlock()

	ch := d.mic.Subscribe()
	defer d.mic.Unsubscribe(ch)

	cfg := config.Get()

	buf := make([]byte, 0, wakeChunkBytes*4)
	wakeBuf := make([]byte, 0, wakeChunkBytes*4)
	wakeLaneDirection := [2]int{-2, -2}
	wakeDirection, hasWakeDirection := d.beam.(beamformer.WakeDirectionProvider)
	wakeArray, hasWakeArray := d.beam.(beamformer.WakeArray)
	wakeLatch := newWakeBeamLatch()
	d.resetWakeLaneAudio()
	var periodCount uint64 // periodic RMS diagnostic
	var lastClipped uint64 // clip count at last diag line
	lockOnSpeech := false

	// Memoized linear mic gain — recomputed only when the config dB value
	// changes (config push mid-stream). Sentinel forces computation on the
	// first period.
	gainDb := -1
	gainLin := 1.0

	// On-device microWakeWord scoring. Reset at stream start because a
	// A mute/unmute or pipeline restart creates an audio gap that the detector
	// must not splice across.
	//
	// The pointer is re-read PER FRAME below, not captured here. It used to be
	// captured, on the reasoning that a stream which began before a config
	// change could keep using the scorer it started with — but a config change
	// CLOSES that scorer, so the stream went on feeding a dead one and the
	// newly installed scorer never saw a single frame. Wake word detection
	// then stayed dead until the next pipeline restart.
	mwwScorers, owwScorers := d.WakeShadowScorers()
	for _, sc := range append(mwwScorers, owwScorers...) {
		sc.Reset()
	}

	for {
		select {
		case <-stopCh:
			return

		case raw, ok := <-ch:
			if !ok {
				return
			}
			// Stop has priority: select picks randomly among ready cases,
			// so without this a closed stopCh racing a ready mic channel
			// keeps this goroutine draining periods alongside its
			// replacement stream.
			select {
			case <-stopCh:
				return
			default:
			}

			snap := cfg.Snapshot()
			musicActive := false
			if audible, ok := d.spk.(musicAudibleSpeaker); ok {
				musicActive = audible.MusicAudible(musicAECHold)
			}
			d.aec.SetMusicMode(musicActive)
			d.setWakePlaybackActive(musicActive)
			threshold := snap.VadThreshold

			beamAngle := float64(-1)
			if snap.BeamAngle != nil {
				beamAngle = *snap.BeamAngle
			}
			// AGC is forced off on the continuous native stream. Adaptive
			// gain with persistent state on a stream that never
			// restarts is a rebaselining mechanism by construction: in a
			// room with steady background noise above vadThreshold, the
			// RMS gate calls every noisy period "speech", the release path
			// walks the gain up toward amplifying the noise floor, and the
			// fast attack then compresses the wake word's envelope
			// mid-utterance — depressing OWW scores. Wake word models
			// are trained level-diverse and don't need AGC. The config
			// models are trained level-diverse and do not need AGC.
			const agcEnabled = false

			if snap.MicGainDb != nil && *snap.MicGainDb != gainDb {
				gainDb = *snap.MicGainDb
				gainLin = math.Pow(10, float64(gainDb)/20.0)
				log.Printf("[data] mic gain: %ddB (linear %.2f)", gainDb, gainLin)
			}
			d.pipeMu.Lock()
			// Consume any pending beam lock/unlock request from the control
			// plane (wake detection → lock, turn end → unlock). Handled on
			// this goroutine because Beamformer methods aren't safe to call
			// from the control handler. Lock() no-ops if already locked or
			// if beamforming is disabled in config.
			switch atomic.SwapInt32(&d.beamReq, beamReqNone) {
			case beamReqLock:
				lockOnSpeech = false
				turnBeam := snap.BeamformingEnabled != nil && *snap.BeamformingEnabled
				d.lockWakeOrBest(turnBeam)
			case beamReqUnlock:
				lockOnSpeech = false
				d.beam.Unlock()
				wakeLatch.reset()
			case beamReqLockOnSpeech:
				lockOnSpeech = true
				d.beam.PrepareSpeechLock()
			}

			mono, angle := d.beam.Process(raw, beamAngle, gainLin)
			clipped := d.beam.ClippedSamples()
			beamDiag := d.beam.Diagnostics()
			mainPath := d.beam.OutputChannel()
			d.aec.SelectPath(mainPath)

			// AEC — subtract the speaker's own output before anything
			// measures or gates the signal. No-op while aecEnabled=false.
			// (Has its own mutex — inside pipeMu only for lock ordering
			// simplicity; aec.mu is a leaf lock, no inversion possible.)
			//
			// Two far-end sources (#385). The hardware reference comes from
			// THIS SAME PERIOD — the device's own playback, looped back in
			// the same TDM frame — so near and far end are aligned by
			// construction and aecDelayMs, the ring and the occupancy
			// governor are all bypassed. The software tap at the speaker
			// ALSA write remains the fallback for any board where ch8 does
			// not prove itself; see noteEchoRef for what counts as proof.
			// Only extract when cancellation is actually armed. EchoRefInto
			// reuses this client's scratch buffer, but the channel walk still
			// belongs to the deadline-bound mic goroutine.
			var echoRef []byte
			aecEnabled := d.aec.Enabled()
			if aecEnabled {
				echoRef = d.beam.EchoRefInto(raw, d.echoRefScratch)
				if echoRef != nil {
					d.echoRefScratch = echoRef
				}
			}
			hardwareRef := echoRef != nil && d.noteEchoRef(echoRef)
			if hardwareRef {
				mono = d.aec.ProcessWithRefInPlace(mono, echoRef)
			} else {
				mono = d.aec.ProcessInPlace(mono)
			}

			// Acquire freely while idle, then hold one direction once two active
			// batches agree. This stabilises the complete wake utterance without
			// creating another neural scorer or resetting either model. The latch
			// spans model agreement and verifier latency; the claimed historical
			// direction is still what the command turn locks. Uncertain/degraded
			// arrays fall through to safe omni.
			var wakePCM []byte
			wakePath := -1
			observedDirection, observedOK := -1, false
			if hasWakeDirection && hasWakeArray {
				observedDirection, observedOK = wakeDirection.WakeDirection()
			}
			batchAt := time.Now()
			wakeActive := doaActivity(vadPeriodRMS(mono), gainLin, false)
			selectedDirection, _, _ := wakeLatch.selectDirection(
				observedDirection,
				observedOK,
				wakeActive,
				batchAt,
			)
			if hasWakeArray && selectedDirection >= 0 {
				candidate := wakeArray.WakeBeam(raw, selectedDirection, gainLin)
				wakePCM = candidate.PCM
				wakePath = candidate.Direction
			}
			if wakePath >= 0 && wakeLaneDirection[0] != wakePath {
				wakeLaneDirection[0] = wakePath
				d.assignWakeLane(0, wakePath, batchAt)
			}

			// A single AEC pass serves both the command and continuous-wake lanes
			// while the speaker is active. Besides keeping the software reference
			// sample-ordered, this prevents the hardware-reference targets from
			// running a second filter and switching retained acoustic paths twice
			// per mic batch — the measured cause of recurring ALSA xruns on Biscuit.
			wakePCM = prepareWakeLanePCM(wakePCM, mono,
				aecEnabled && (beamDiag.PlaybackActive || musicActive))
			wakePCM = d.wakeProc.HighPass(wakePCM)

			// Remove DC and sub-speech rumble before either VAD or AGC sees it.
			// This mutates the batch owned by this stream; wake and turn audio
			// therefore pass through the same stable, time-invariant filter.
			mono = d.proc.HighPass(mono)

			// ── Processing pipeline ──────────────────────────────────────
			// VAD on beamformed, echo-cancelled, high-passed output — pre-AGC
			// so threshold is
			// consistent regardless of gain state.
			//
			// vadThreshold is calibrated in pre-gain (acoustic) units —
			// the values validated in the v2.6.3 session predate the fixed
			// mic gain and stay meaningful across gain changes. mono is
			// post-gain, so scale the threshold up by the same factor
			// rather than requiring every stored config to be retuned in
			// lockstep with micGainDb.
			rms := vadPeriodRMS(mono)
			speech := rms >= threshold*gainLin
			if lockOnSpeech && speech {
				turnBeam := snap.BeamformingEnabled != nil && *snap.BeamformingEnabled
				d.beam.LockCurrent(turnBeam)
				lockOnSpeech = false
			}

			// Periodic RMS diagnostic — every ~10 min, or within ~16s of
			// the mic gain clamping a sample (clipping is the one signal
			// that says micGainDb is too hot for the room, so it's
			// reported promptly; the %100 bound stops sustained clipping
			// becoming its own log flood). Was every 100 counts (~16s
			// measured on-device) while idle capture levels were being
			// characterised — that job is done (2026-07-07 fleet
			// analysis) and /tmp/server.log is RAM-backed and unrotated.
			if periodCount%3750 == 0 || (clipped != lastClipped && periodCount%100 == 0) {
				pairDiag := ""
				if beamDiag.HealthyMicChannels > 0 {
					pairDiag = fmt.Sprintf(
						" pair_cal=%v pair_delay=%.2f pair_level=%.1fdB pair_coh=%.2f pair_ns=%.2f pair_mics=%d",
						beamDiag.Calibrated, beamDiag.DelaySamples, beamDiag.LevelBalanceDB,
						beamDiag.Coherence, beamDiag.NoiseGain, beamDiag.HealthyMicChannels,
					)
				}
				log.Printf("[data] VAD diag: rms=%.5f threshold=%.5f gain=%ddB clipped=%d by_ch=%v "+
					"speech=%v agc=%v mic=ch%d doa_conf=%.2f spatial=%.2f playback=%v%s",
					rms, threshold*gainLin, gainDb, clipped, d.beam.ClippedByChannel(),
					speech, agcEnabled, beamDiag.OutputChannel, beamDiag.Confidence,
					beamDiag.Spatial, beamDiag.PlaybackActive, pairDiag)
				lastClipped = clipped
			}
			periodCount++

			// Native audio stays AGC-free so wake scoring and command capture
			// share the same stable signal.
			mono = d.proc.Process(mono, agcEnabled, speech)
			d.pipeMu.Unlock()
			// ─────────────────────────────────────────────────────────────

			// Always report the bearing with separate acoustic-activity and strict
			// speech decisions. Quiet valid speech can start DOA without weakening
			// the turn gate; strict speech is only reply-direction metadata.
			if angle >= 0 {
				d.directionMu.Lock()
				cb := d.onDirectionChange
				d.directionMu.Unlock()
				if cb != nil {
					cb(angle, doaActivity(rms, gainLin, speech), speech)
				}
			}

			// The continuous stream feeds both local wake scorers and the native
			// transport. There is no socket framing or remote listen gate.
			mwwScorers, owwScorers := d.WakeShadowScorers()
			buf = append(buf, mono...)
			wakeBuf = append(wakeBuf, wakePCM...)
			for len(buf) >= wakeChunkBytes && len(wakeBuf) >= wakeChunkBytes {
				chunk := make([]byte, wakeChunkBytes)
				copy(chunk, buf[:wakeChunkBytes])
				buf = buf[wakeChunkBytes:]
				wakeChunk := make([]byte, wakeChunkBytes)
				copy(wakeChunk, wakeBuf[:wakeChunkBytes])
				wakeBuf = wakeBuf[wakeChunkBytes:]
				at := time.Now()
				if len(mwwScorers) > 0 || len(owwScorers) > 0 {
					d.recordWakeLaneAudio(0, at, wakeChunk)
					if len(mwwScorers) > 0 {
						mwwScorers[0].PushBytesAt(wakeChunk, at)
					}
					if len(owwScorers) > 0 {
						owwScorers[0].PushBytesAt(wakeChunk, at)
					}
				}
				d.observePCM(chunk, speech)
			}
		}
	}
}
