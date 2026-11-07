package client

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/aec"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/beamformer"
)

func TestBeamLockOnSpeechRequestReplacesImmediateLock(t *testing.T) {
	d := &DataClient{}
	d.RequestBeamLock()
	if got := atomic.LoadInt32(&d.beamReq); got != beamReqLock {
		t.Fatalf("immediate beam request = %d, want %d", got, beamReqLock)
	}
	d.RequestBeamLockOnSpeech()
	if got := atomic.LoadInt32(&d.beamReq); got != beamReqLockOnSpeech {
		t.Fatalf("continued-chat beam request = %d, want %d", got, beamReqLockOnSpeech)
	}
	d.RequestBeamUnlock()
	if got := atomic.LoadInt32(&d.beamReq); got != beamReqUnlock {
		t.Fatalf("unlock beam request = %d, want %d", got, beamReqUnlock)
	}
}

func TestNativeListeningStateAlwaysUsesSpeechLock(t *testing.T) {
	d := &DataClient{}
	d.ApplyNativeBeamState("listening")
	if got := atomic.LoadInt32(&d.beamReq); got != beamReqLockOnSpeech {
		t.Fatalf("native listening request = %d, want speech lock %d", got, beamReqLockOnSpeech)
	}

	d.ApplyNativeBeamState("idle")
	if got := atomic.LoadInt32(&d.beamReq); got != beamReqUnlock {
		t.Fatalf("native idle request = %d, want unlock %d", got, beamReqUnlock)
	}
}

func TestNativeWakeCarriesWinningBeamIntoListening(t *testing.T) {
	d := NewDataClient("winner-test", nil, nil, aec.New())
	d.wakeLaneDir[1].Store(4)
	if !d.ClaimWakeLane(1, time.Now()) {
		t.Fatal("first wake lane was not accepted")
	}
	d.ApplyNativeBeamState("listening")
	if got := atomic.LoadInt32(&d.beamReq); got != beamReqLock {
		t.Fatalf("winning wake request = %d, want exact lock %d", got, beamReqLock)
	}
	d.lockWakeOrBest(true)
	if got := d.beam.(*beamformer.Beamformer).LockedAngle(); got != 210 {
		t.Fatalf("winning wake angle = %.0f, want 210", got)
	}
}

func TestEveryTargetUsesOneContinuousWakeStream(t *testing.T) {
	if got := NewDataClientForTarget("biscuit", nil, nil, aec.New(), "biscuit").WakeScorerCount(); got != 1 {
		t.Fatalf("Biscuit wake scorer count = %d, want 1 stable stream", got)
	}
	checkers := NewDataClientForTarget("checkers", nil, nil, aec.New(), "checkers")
	if got := checkers.WakeScorerCount(); got != 1 {
		t.Fatalf("Checkers wake scorer count = %d, want 1 stable stream", got)
	}
	if !checkers.ClaimWakeLane(0, time.Now()) {
		t.Fatal("Checkers first beam crossing was not accepted")
	}
	checkers.ApplyNativeBeamState("listening")
	if got := atomic.LoadInt32(&checkers.beamReq); got != beamReqLock {
		t.Fatalf("Checkers listening request = %d, want winning-beam lock %d", got, beamReqLock)
	}
}

func TestAdjacentWakeBeamCrossingsCollapseToOneClaim(t *testing.T) {
	d := NewDataClient("claim-test", nil, nil, aec.New())
	d.wakeLaneDir[0].Store(2)
	d.wakeLaneDir[1].Store(3)
	now := time.Now()
	if !d.ClaimWakeLane(0, now) {
		t.Fatal("first candidate crossing was rejected")
	}
	if d.ClaimWakeLane(1, now) {
		t.Fatal("adjacent candidate crossing escaped shared refractory")
	}
	if got := d.wakeWinnerDir.Load(); got != 2 {
		t.Fatalf("winner direction = %d, want first lane direction 2", got)
	}
}

func TestWakeLaneClaimUsesDirectionAtCaptureTime(t *testing.T) {
	d := NewDataClient("assignment-test", nil, nil, aec.New())
	first := time.Now()
	d.assignWakeLane(0, 1, first)
	d.assignWakeLane(0, 4, first.Add(time.Second))
	if !d.ClaimWakeLane(0, first.Add(500*time.Millisecond)) {
		t.Fatal("historical candidate crossing was rejected")
	}
	if got := d.wakeWinnerDir.Load(); got != 1 {
		t.Fatalf("historical winner direction = %d, want 1", got)
	}
}

func TestWakeLaneClaimSnapshotsWinningBeamThroughCallback(t *testing.T) {
	d := NewDataClient("winner-audio-test", nil, nil, aec.New())
	base := time.Now()
	d.recordWakeLaneAudio(0, base, []byte{1, 0})
	d.recordWakeLaneAudio(1, base, []byte{7, 0})
	d.recordWakeLaneAudio(1, base.Add(80*time.Millisecond), []byte{8, 0})
	d.recordWakeLaneAudio(1, base.Add(160*time.Millisecond), []byte{9, 0})
	d.wakeLaneDir[1].Store(1)
	if !d.ClaimWakeLane(1, base.Add(80*time.Millisecond)) {
		t.Fatal("winning wake lane was not accepted")
	}
	confirmation := d.CopyWinningWakeAudio()
	if len(confirmation) != 3 || confirmation[0][0] != 7 {
		t.Fatalf("confirmation pre-roll = %v, want a copy of lane 1", confirmation)
	}
	confirmation[0][0] = 99
	d.recordWakeLaneAudio(1, base.Add(240*time.Millisecond), []byte{10, 0})
	d.ExtendWinningWakeAudio(1, base.Add(80*time.Millisecond))
	frames := d.TakeWinningWakeAudio()
	if len(frames) != 4 || frames[0][0] != 7 || frames[1][0] != 8 || frames[2][0] != 9 || frames[3][0] != 10 {
		t.Fatalf("winning pre-roll = %v, want the unmodified lane 1 snapshot plus confirmation-delay audio", frames)
	}
	if again := d.TakeWinningWakeAudio(); len(again) != 0 {
		t.Fatalf("winning pre-roll was not one-shot: %v", again)
	}
}

func TestWakeVerifierCaptureStaysAnchoredWhenAgreementArrivesLater(t *testing.T) {
	d := NewDataClient("verifier-anchor-test", nil, nil, aec.New())
	base := time.Now()
	for index := 0; index < wakeLaneCaptureFrames; index++ {
		d.recordWakeLaneAudio(0, base.Add(time.Duration(index)*80*time.Millisecond), []byte{byte(index), 0})
	}
	phraseAt := base.Add(30 * 80 * time.Millisecond)
	if !d.ClaimWakeLane(0, phraseAt) {
		t.Fatal("wake lane was not accepted")
	}
	d.ExtendWinningWakeAudio(0, phraseAt)
	capture := d.TakeWinningWakeCapture()
	if len(capture.PreRoll) != 31 {
		t.Fatalf("wake pre-roll frames = %d, want 31 through the detector crossing", len(capture.PreRoll))
	}
	if len(capture.PostWake) != wakeLaneCaptureFrames-31 {
		t.Fatalf("post-wake frames = %d, want %d", len(capture.PostWake), wakeLaneCaptureFrames-31)
	}
	if got := len(capture.Verifier); got != wakeVerifierCaptureFrames {
		t.Fatalf("verifier frames = %d, want %d", got, wakeVerifierCaptureFrames)
	}
	if first, last := capture.Verifier[0][0], capture.Verifier[len(capture.Verifier)-1][0]; first != 8 || last != 32 {
		t.Fatalf("verifier frame range = %d..%d, want phrase-aligned 8..32", first, last)
	}
	if last := capture.PreRoll[len(capture.PreRoll)-1][0]; last != 30 {
		t.Fatalf("wake pre-roll crossed the detector boundary: last=%d", last)
	}
	if first, last := capture.PostWake[0][0], capture.PostWake[len(capture.PostWake)-1][0]; first != 31 || last != wakeLaneCaptureFrames-1 {
		t.Fatalf("post-wake agreement audio = %d..%d, want 31..%d", first, last, wakeLaneCaptureFrames-1)
	}
}

func TestWakeCommandBoundaryCanPrecedeVerifierDetectionAnchor(t *testing.T) {
	d := NewDataClient("command-boundary-test", nil, nil, aec.New())
	base := time.Now()
	for index := 0; index < wakeLaneCaptureFrames; index++ {
		d.recordWakeLaneAudio(0, base.Add(time.Duration(index)*80*time.Millisecond), []byte{byte(index), 0})
	}
	detectedAt := base.Add(30 * 80 * time.Millisecond)
	commandBoundaryAt := detectedAt.Add(-80 * time.Millisecond)
	if !d.ClaimWakeLaneWithBoundary(0, detectedAt, commandBoundaryAt) {
		t.Fatal("wake lane with an earlier command boundary was not accepted")
	}
	d.ExtendWinningWakeAudioWithBoundary(0, detectedAt, commandBoundaryAt)
	capture := d.TakeWinningWakeCapture()

	if len(capture.PreRoll) != 30 || capture.PreRoll[len(capture.PreRoll)-1][0] != 29 {
		t.Fatalf("wake pre-roll did not end at adjusted boundary: frames=%d last=%d",
			len(capture.PreRoll), capture.PreRoll[len(capture.PreRoll)-1][0])
	}
	if len(capture.PostWake) == 0 || capture.PostWake[0][0] != 30 {
		t.Fatalf("command audio did not begin at retained score window: %v", capture.PostWake)
	}
	if first, last := capture.Verifier[0][0], capture.Verifier[len(capture.Verifier)-1][0]; first != 8 || last != 32 {
		t.Fatalf("verifier moved with command boundary: range=%d..%d, want 8..32", first, last)
	}
}

func TestWakeBoundarySplitsDetectorBatchAtCrossingSample(t *testing.T) {
	batch := make([]byte, wakeChunkBytes)
	for index := range batch {
		batch[index] = byte(index % 251)
	}
	batchEnd := time.Now()
	preRoll, postWake := splitWakeAudioAt([]wakeAudioFrame{{at: batchEnd, pcm: batch}},
		batchEnd.Add(-40*time.Millisecond))
	if len(preRoll) != 1 || len(postWake) != 1 {
		t.Fatalf("split frame counts = %d/%d, want 1/1", len(preRoll), len(postWake))
	}
	if len(preRoll[0]) != wakeChunkBytes/2 || len(postWake[0]) != wakeChunkBytes/2 {
		t.Fatalf("split byte counts = %d/%d, want %d/%d",
			len(preRoll[0]), len(postWake[0]), wakeChunkBytes/2, wakeChunkBytes/2)
	}
	joined := append(append([]byte(nil), preRoll[0]...), postWake[0]...)
	if string(joined) != string(batch) {
		t.Fatal("sample-accurate wake split changed the detector audio")
	}
}

func TestContinuousWakeAudioSurvivesDirectionChange(t *testing.T) {
	d := NewDataClient("continuous-wake-test", nil, nil, aec.New())
	base := time.Now()
	d.assignWakeLane(0, 1, base)
	d.recordWakeLaneAudio(0, base, []byte{1, 0})
	d.assignWakeLane(0, 4, base.Add(80*time.Millisecond))
	d.recordWakeLaneAudio(0, base.Add(80*time.Millisecond), []byte{2, 0})
	if !d.ClaimWakeLane(0, base.Add(40*time.Millisecond)) {
		t.Fatal("continuous wake stream was not accepted")
	}
	frames := d.TakeWinningWakeAudio()
	if len(frames) != 2 || frames[0][0] != 1 || frames[1][0] != 2 {
		t.Fatalf("wake pre-roll was split by a direction update: %v", frames)
	}
}

func TestDOAActivityDoesNotRequireStrictSpeechGate(t *testing.T) {
	const gain = 15.85 // +24dB default
	if !doaActivity(0.002, gain, false) {
		t.Fatal("quiet acoustic activity did not start DOA below the strict speech threshold")
	}
	if doaActivity(0.001, gain, false) {
		t.Fatal("sub-threshold room noise started DOA")
	}
	if !doaActivity(0, gain, true) {
		t.Fatal("strict speech decision did not start DOA")
	}
}

func TestWakeLaneReusesSingleAECStreamDuringPlayback(t *testing.T) {
	candidate := []byte{1, 2, 3, 4}
	mainPCM := []byte{9, 8, 7, 6}
	got := prepareWakeLanePCM(candidate, mainPCM, true)
	if string(got) != string(mainPCM) {
		t.Fatalf("playback wake PCM = %v, want already-cancelled main stream %v", got, mainPCM)
	}
	mainPCM[0] = 0
	if got[0] != 9 {
		t.Fatal("playback wake PCM aliases the mutable main stream")
	}
}

func TestWakeLaneKeepsDirectionalBeamWithoutPlayback(t *testing.T) {
	candidate := []byte{1, 2, 3, 4}
	mainPCM := []byte{9, 8, 7, 6}
	if got := prepareWakeLanePCM(candidate, mainPCM, false); &got[0] != &candidate[0] {
		t.Fatal("idle wake lane discarded its directional candidate")
	}
	if got := prepareWakeLanePCM(nil, mainPCM, false); string(got) != string(mainPCM) {
		t.Fatalf("missing candidate fallback = %v, want %v", got, mainPCM)
	}
}

// fanoutMic is a minimal mic.Subscribable: a background pump broadcasts raw
// 9ch S24_3LE periods to every subscriber until closed, mimicking
// PcmMicrophone's fan-out (including the drop-when-full behaviour).
type fanoutMic struct {
	mu     sync.Mutex
	subs   []chan []byte
	stopCh chan struct{}
}

func newFanoutMic() *fanoutMic {
	m := &fanoutMic{stopCh: make(chan struct{})}
	// One 512-frame period of 9ch S24_3LE (the minimum Process() analyses),
	// non-zero so the beamformer smoothers see real energy.
	raw := make([]byte, 512*9*3)
	for i := range raw {
		raw[i] = byte(i % 251)
	}
	go func() {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-m.stopCh:
				return
			case <-ticker.C:
				m.mu.Lock()
				for _, ch := range m.subs {
					select {
					case ch <- raw:
					default:
					}
				}
				m.mu.Unlock()
			}
		}
	}()
	return m
}

func (m *fanoutMic) Subscribe() chan []byte {
	ch := make(chan []byte, 32)
	m.mu.Lock()
	m.subs = append(m.subs, ch)
	m.mu.Unlock()
	return ch
}

func (m *fanoutMic) Unsubscribe(ch chan []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.subs {
		if s == ch {
			m.subs = append(m.subs[:i], m.subs[i+1:]...)
			close(ch)
			return
		}
	}
}

func (m *fanoutMic) close() { close(m.stopCh) }

// TestStreamRestartOverlapIsRaceFree drives rapid native mute/unmute restarts
// while mic data is flowing. The superseded streamMic goroutine can keep
// draining periods for a few iterations after its stopCh closes (select on a
// closed channel vs a ready mic channel picks randomly), so for a window the
// old and new goroutines run concurrently against the shared beamformer and
// AGC state. Run under -race: before pipeMu serialised the pipeline this
// reliably reported races on the beamformer's reused analysis buffers, and
// the old goroutine's deferred beam.Unlock could land after the new stream's
// Lock. Beam lock/unlock requests are mixed in to cover the mid-stream
// request path too.
func TestStreamRestartOverlapIsRaceFree(t *testing.T) {
	mic := newFanoutMic()
	defer mic.close()

	d := NewDataClient("race-test", mic, nil, aec.New())

	for i := 0; i < 100; i++ {
		d.StartLocalMic()
		d.RequestBeamLock()
		time.Sleep(2 * time.Millisecond) // let a couple of periods flow
		d.RequestBeamUnlock()
		d.StopMic()
		// No settling delay: the replacement StartLocalMic in the next iteration
		// racing the superseded goroutine's drain is the scenario under test.
	}

	d.StopMic()
	// Give lingering goroutines time to exit so their deferred cleanup runs
	// (and the race detector observes it) before the test tears down.
	time.Sleep(100 * time.Millisecond)
}
