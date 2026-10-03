package beamformer

import (
	"math"
	"testing"
)

// warmBeamformer returns a Beamformer with baseline warmed up and a uniform
// noise floor, as if it had been running in a quiet room.
func warmBeamformer(baseline float64) *Beamformer {
	b := New()
	b.baselineReady = 100
	for di := 0; di < nDirections; di++ {
		b.energyBaseline[di] = baseline
	}
	return b
}

// TestLockBackPicksPastBurst is the scenario that motivated lock-back:
// the wake word was spoken ~1s ago from direction 2, the fast smoother has
// since decayed and (thanks to a TV) now points at direction 5. Live onset
// selection picks the TV; lock-back must pick the speaker.
func TestLockBackPicksPastBurst(t *testing.T) {
	b := warmBeamformer(1e-6)

	// Fill the ring with baseline-level noise…
	for i := 0; i < historyPeriods; i++ {
		for di := 0; di < nDirections; di++ {
			b.energyHistory[i][di] = 1e-6
		}
	}
	b.historyCount = historyPeriods

	// …with a wake-word burst on direction 2, ~10 periods long, in the
	// middle of the window (well before "now").
	for i := 20; i < 30; i++ {
		b.energyHistory[i][2] = 5e-4
	}

	// TV on direction 5: elevated steady energy in both the ring and the
	// live smoother — loud in absolute terms, but not a burst relative to
	// its own baseline.
	b.energyBaseline[5] = 4e-4
	for i := 0; i < historyPeriods; i++ {
		b.energyHistory[i][5] = 5e-4
	}
	b.energySmooth[5] = 5e-4 // live smoother points at the TV
	b.energySmooth[2] = 2e-6 // speaker's onset has decayed

	b.Lock(true)

	if b.lockedChannel != directionToChannel[2] {
		t.Fatalf("lock-back picked ch%d, want ch%d (direction 2 burst)",
			b.lockedChannel, directionToChannel[2])
	}
	if angle := b.LockedAngle(); angle != candidateAngles[2] {
		t.Fatalf("locked angle = %.0f°, want %.0f°", angle, candidateAngles[2])
	}
}

// TestLockFallsBackToOnsetRatioWithoutHistory — fresh start: baseline warm
// (carried into the ready state quickly) but ring not yet populated. Must
// use the live onset ratio, not a zero-filled ring.
func TestLockFallsBackToOnsetRatioWithoutHistory(t *testing.T) {
	b := warmBeamformer(1e-6)
	b.historyCount = 0
	b.energySmooth[4] = 3e-4 // live onset on direction 4

	b.Lock(true)

	if b.lockedChannel != directionToChannel[4] {
		t.Fatalf("fallback picked ch%d, want ch%d (live onset direction 4)",
			b.lockedChannel, directionToChannel[4])
	}
}

func TestContinuedChatLockUsesCurrentSpeechNotPreviousHistory(t *testing.T) {
	b := warmBeamformer(1e-6)
	b.historyCount = historyPeriods
	for i := 0; i < historyPeriods; i++ {
		b.energyHistory[i][2] = 5e-4 // previous turn came from direction 2
	}
	b.energySmooth[4] = 3e-4 // current follow-up speech is direction 4

	b.LockCurrent(true)

	if b.lockedChannel != directionToChannel[4] {
		t.Fatalf("continued lock picked ch%d, want current speech ch%d",
			b.lockedChannel, directionToChannel[4])
	}
}

// TestLockDisabledIsNoOp — beamforming off must leave the channel unlocked
// (ch6 omni output path).
func TestLockDisabledIsNoOp(t *testing.T) {
	b := warmBeamformer(1e-6)
	b.historyCount = historyPeriods
	b.energyHistory[0][3] = 1.0

	b.Lock(false)

	if b.lockedChannel != -1 {
		t.Fatalf("Lock(false) locked to ch%d, want unlocked (-1)", b.lockedChannel)
	}
	if angle := b.LockedAngle(); angle != -1 {
		t.Fatalf("unlocked angle = %.0f°, want -1", angle)
	}
}

// TestBurstRatioTopNMean checks the allocation-free partial selection:
// history 1..64 on direction 0 → top 8 are 57..64, mean 60.5.
func TestBurstRatioTopNMean(t *testing.T) {
	b := warmBeamformer(1.0)
	for i := 0; i < historyPeriods; i++ {
		b.energyHistory[i][0] = float64(i + 1)
	}
	b.historyCount = historyPeriods

	got := b.burstRatio(0)
	want := 60.5 // mean of 57..64, baseline 1.0
	if got != want {
		t.Fatalf("burstRatio = %v, want %v", got, want)
	}
}

// TestBurstRatioPartialHistory — fewer samples than burstTopN averages what
// exists instead of diluting with zeros.
func TestBurstRatioPartialHistory(t *testing.T) {
	b := warmBeamformer(1.0)
	b.energyHistory[0][0] = 4.0
	b.energyHistory[1][0] = 2.0
	b.historyCount = 2

	got := b.burstRatio(0)
	want := 3.0
	if got != want {
		t.Fatalf("burstRatio = %v, want %v", got, want)
	}
}

// ─── Hardware echo reference (#385) ───────────────────────────────────────────

// raw9 builds one period of 9-channel S24_3LE with a per-channel constant, so
// each channel is identifiable by value alone.
func raw9(frames int, valueFor func(ch int) int32) []byte {
	buf := make([]byte, frames*frameSize)
	for f := 0; f < frames; f++ {
		for ch := 0; ch < nChannels; ch++ {
			v := valueFor(ch)
			b := f*frameSize + ch*byteSample
			buf[b] = byte(v)
			buf[b+1] = byte(v >> 8)
			buf[b+2] = byte(v >> 16)
		}
	}
	return buf
}

func rawDirectionWindow(direction, frames, startFrame int) []byte {
	buf := make([]byte, frames*frameSize)
	for frame := startFrame; frame < frames; frame++ {
		value := int32(0)
		if frame%4 >= 2 {
			value = 0x300000
		} else {
			value = -0x300000
		}
		base := frame*frameSize + direction*byteSample
		buf[base] = byte(value)
		buf[base+1] = byte(value >> 8)
		buf[base+2] = byte(value >> 16)
	}
	return buf
}

func rawDirection(direction int) []byte {
	return rawDirectionWindow(direction, periodFrames, 0)
}

func TestUnlockedFollowUpReportsVisualDirection(t *testing.T) {
	b := New()
	b.PrepareSpeechLock()
	_, angle := b.Process(rawDirection(2), -1, 1)

	if b.lockedChannel != -1 {
		t.Fatalf("visual DOA unexpectedly locked audio to ch%d", b.lockedChannel)
	}
	if angle != candidateAngles[2] {
		t.Fatalf("unlocked visual angle = %.0f°, want %.0f°", angle, candidateAngles[2])
	}
}

func TestVisualDOAAnalyzesWholeMicrophoneBatch(t *testing.T) {
	b := New()
	b.PrepareSpeechLock()

	// ALSA delivers 5 x 512-frame periods together (160ms). Speech beginning
	// after the first 32ms must still drive this batch's real-time DOA.
	raw := rawDirectionWindow(4, periodFrames*5, periodFrames)
	_, angle := b.Process(raw, -1, 1)
	if angle != candidateAngles[4] {
		t.Fatalf("late-batch speech angle = %.0f°, want %.0f°", angle, candidateAngles[4])
	}
}

func TestHardwareBatchAdvancesPhysicalPeriodCadence(t *testing.T) {
	b := New()
	b.Process(rawDirectionWindow(2, periodFrames*5, 0), -1, 1)

	if b.baselineReady != 5 {
		t.Fatalf("baseline advanced %d times for a five-period batch, want 5", b.baselineReady)
	}
	if b.historyCount != 5 {
		t.Fatalf("history holds %d periods after a five-period batch, want 5", b.historyCount)
	}
}

func TestPlaybackDoesNotEnterAmbientHistory(t *testing.T) {
	b := New()
	raw := rawDirectionWindow(3, periodFrames*5, 0)
	for frame := 0; frame < periodFrames*5; frame++ {
		off := frame*frameSize + echoRefCh*byteSample
		raw[off+1] = 1 // non-zero loopback: speaker is active
	}
	b.Process(raw, -1, 1)

	if b.historyCount != 0 || b.baselineReady != 0 {
		t.Fatalf("playback contaminated ambient estimator: history=%d ready=%d",
			b.historyCount, b.baselineReady)
	}
	if b.playbackReady != 5 || !b.Diagnostics().PlaybackActive {
		t.Fatalf("playback floor did not advance for all subframes: ready=%d active=%v",
			b.playbackReady, b.Diagnostics().PlaybackActive)
	}
}

func TestUncertainLockStaysOnCentreMic(t *testing.T) {
	b := warmBeamformer(1)
	b.historyCount = historyPeriods
	for period := range b.energyHistory {
		for direction := range b.energyHistory[period] {
			b.energyHistory[period][direction] = 1
		}
	}
	b.Lock(true)
	if b.lockedChannel != -1 {
		t.Fatalf("equal scores locked to ch%d, want centre fallback", b.lockedChannel)
	}
}

func TestSpatialScoresUseAllPerimeterMics(t *testing.T) {
	const wanted = 2 // 90 degrees
	b := New()
	frames := periodFrames
	raw := make([]byte, frames*frameSize)
	// Deterministic broadband source with enough prefix for geometry delays.
	source := make([]float64, frames+16)
	seed := uint32(0x9e3779b9)
	for i := range source {
		seed = seed*1664525 + 1013904223
		source[i] = float64(int32(seed)) / 2147483648.0
	}
	theta := candidateAngles[wanted] * math.Pi / 180
	sx, sy := math.Sin(theta), math.Cos(theta)
	var arrival [nDirections]float64
	minArrival := math.MaxFloat64
	for ch := 0; ch < nDirections; ch++ {
		a := micAngles[ch] * math.Pi / 180
		x, y := micRadiusMetres*math.Sin(a), micRadiusMetres*math.Cos(a)
		arrival[ch] = -(x*sx + y*sy) * sampleRate / speedOfSound
		if arrival[ch] < minArrival {
			minArrival = arrival[ch]
		}
	}
	for frame := 0; frame < frames; frame++ {
		for ch := 0; ch < nDirections; ch++ {
			delay := int(math.Round(arrival[ch] - minArrival))
			v := int32(source[frame+8-delay] * 0x300000)
			off := frame*frameSize + ch*byteSample
			raw[off], raw[off+1], raw[off+2] = byte(v), byte(v>>8), byte(v>>16)
		}
	}
	b.ensureAnalysisFrames(frames)
	b.decodeChannels(raw)
	b.bandDiff()
	scores := b.spatialScores(0, frames)
	got, score, confidence := bestDirection(scores)
	if got != wanted {
		t.Fatalf("spatial bearing=%d (%.0f°), want %d (%.0f°); scores=%v",
			got, candidateAngles[got], wanted, candidateAngles[wanted], scores)
	}
	if confidence < 0.05 {
		t.Fatalf("spatial confidence %.3f too low; scores=%v", confidence, scores)
	}
	if score < minSpatialScore {
		t.Fatalf("spatial coherence %.3f below %.3f; scores=%v", score, minSpatialScore, scores)
	}
}

func TestSpatialNoiseCannotBreakEnergyTie(t *testing.T) {
	b := New()
	b.trackSpeech = true
	b.batchScores = [nDirections]float64{1, 1, 1, 1, 1, 1}
	b.batchSpatial = [nDirections]float64{0.01, 0.02, 0.01, 0.01, 0.01, 0.01}
	_, _, mode := b.liveDirection()
	if mode != "batch_onset" {
		t.Fatalf("low-coherence noise selected mode %q, want batch_onset", mode)
	}
}

func TestUnlockedVisualDirectionUsesOnsetRatio(t *testing.T) {
	b := warmBeamformer(1)
	b.energyBaseline[1] = 100 // loud, steady source
	b.energySmooth[1] = 110
	b.energySmooth[4] = 8 // quieter absolute level, much stronger new onset

	direction, _, mode := b.liveDirection()
	if mode != "onset_ratio" {
		t.Fatalf("live direction mode = %q, want onset_ratio", mode)
	}
	if direction != 4 {
		t.Fatalf("live direction = %d, want current onset direction 4", direction)
	}
}

func TestImmediateLockUsesSmoothedDOA(t *testing.T) {
	b := New()
	for i := 0; i < 12; i++ {
		b.Process(rawDirection(2), -1, 1)
	}
	b.Lock(true)

	// The legacy immediate-lock path remains smoothed: one current-period
	// winner does not displace the selected DOA and make the ring bounce.
	_, angle := b.Process(rawDirection(5), -1, 1)
	if angle != candidateAngles[2] {
		t.Fatalf("single noisy period moved locked DOA to %.0f°, want %.0f°", angle, candidateAngles[2])
	}

	// It remains live rather than frozen: sustained speech from a new location
	// eventually becomes the smoothed winner.
	for i := 0; i < 20; i++ {
		_, angle = b.Process(rawDirection(5), -1, 1)
	}
	if angle != candidateAngles[5] {
		t.Fatalf("sustained locked DOA = %.0f°, want %.0f°", angle, candidateAngles[5])
	}
}

func TestPrepareSpeechLockPreservesInitialDirectionEstimator(t *testing.T) {
	b := warmBeamformer(2e-6)
	for i := range b.energySmooth {
		b.energySmooth[i] = float64(i + 1)
	}
	for i := range b.energyHistory {
		for direction := range b.energyHistory[i] {
			b.energyHistory[i][direction] = float64(i*nDirections + direction + 1)
		}
	}
	b.historyIdx = 17
	b.historyCount = historyPeriods
	b.lockedChannel = directionToChannel[3]

	wantSmooth := b.energySmooth
	wantHistory := b.energyHistory
	wantBaseline := b.energyBaseline
	wantHistoryIdx, wantHistoryCount := b.historyIdx, b.historyCount
	b.PrepareSpeechLock()

	if b.energySmooth != wantSmooth || b.energyHistory != wantHistory || b.energyBaseline != wantBaseline ||
		b.historyIdx != wantHistoryIdx || b.historyCount != wantHistoryCount {
		t.Fatal("continued-chat preparation changed the estimator used by the next initial turn")
	}
	if b.lockedChannel != -1 {
		t.Fatalf("continued-chat preparation left audio locked to ch%d", b.lockedChannel)
	}
}

func TestFollowUpVisualDOAIgnoresFixedAudioPickup(t *testing.T) {
	b := warmBeamformer(1)
	b.PrepareSpeechLock()
	b.energySmooth[2] = 10
	b.LockCurrent(true)

	// A fixed pickup may control the audio channel, but the reopened mic's
	// already-validated live visual DOA must remain independent after locking.
	_, angle := b.Process(rawDirection(5), 90, 1)
	if angle != candidateAngles[5] {
		t.Fatalf("fixed audio pickup replaced follow-up DOA with %.0f°, want %.0f°", angle, candidateAngles[5])
	}
}

func TestContinuedChatDOAIgnoresRearPlaybackTail(t *testing.T) {
	b := warmBeamformer(1)
	b.PrepareSpeechLock()

	// Reopened listening begins immediately after playback. Several rear-facing
	// periods model the response tail leaking into the array; the first fresh
	// speech period then comes from direction 1. Visual DOA must follow that
	// current speech instead of remaining stuck on the playback tail.
	for i := 0; i < 6; i++ {
		b.Process(rawDirection(3), -1, 1)
	}
	_, angle := b.Process(rawDirection(1), -1, 1)
	if angle != candidateAngles[1] {
		t.Fatalf("continued-chat visual angle = %.0f°, want fresh speech %.0f°", angle, candidateAngles[1])
	}
}

func TestEchoRefReadsChannel8(t *testing.T) {
	b := New()
	// Every channel gets a distinct value; ch8 gets one we can recognise.
	raw := raw9(periodFrames, func(ch int) int32 {
		if ch == echoRefCh {
			return 0x200000 // +2097152 of 2^23 → 8192 after the 24→16 shift
		}
		return int32(ch) << 12
	})
	out := b.EchoRef(raw)
	if len(out) != periodFrames*2 {
		t.Fatalf("expected %d bytes, got %d", periodFrames*2, len(out))
	}
	got := int16(uint16(out[0]) | uint16(out[1])<<8)
	if got != 8192 {
		t.Fatalf("EchoRef read the wrong channel or gain: got %d, want 8192", got)
	}
}

func TestEchoRefIntoReusesCallerBuffer(t *testing.T) {
	b := New()
	raw := raw9(periodFrames*5, func(ch int) int32 {
		if ch == echoRefCh {
			return 0x123400
		}
		return 0
	})
	dst := make([]byte, 0, periodFrames*5*2)
	out := b.EchoRefInto(raw, dst)
	if len(out) != periodFrames*5*2 {
		t.Fatalf("EchoRefInto length=%d, want %d", len(out), periodFrames*5*2)
	}
	if &out[0] != &dst[:cap(dst)][0] {
		t.Fatal("EchoRefInto allocated despite sufficient caller capacity")
	}
	want := int16(0x1234)
	if got := int16(uint16(out[0]) | uint16(out[1])<<8); got != want {
		t.Fatalf("EchoRefInto sample=%d, want %d", got, want)
	}
}

func BenchmarkHardwareBatch(b *testing.B) {
	beam := New()
	raw := rawDirectionWindow(2, periodFrames*5, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		beam.Process(raw, -1, 1)
	}
}

func BenchmarkPreparedSpatialHardwareBatch(b *testing.B) {
	beam := New()
	beam.PrepareSpeechLock()
	raw := rawDirectionWindow(2, periodFrames*5, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		beam.Process(raw, -1, 1)
	}
}

// TestEchoRefIsUnityGain is the one that matters. Mic extraction applies
// micGainDb (+24dB by default) pre-truncation because speech sits at about
// -70dBFS. The reference is the playback stream at full digital scale — it
// measured -7.3dBFS on hardware — and the same gain on that is 17dB of hard
// clipping, which does not merely cancel badly: it teaches the adaptive
// filter a distorted echo path.
func TestEchoRefIsUnityGain(t *testing.T) {
	b := New()
	// Near full scale on ch8. Any gain above unity clamps this.
	raw := raw9(periodFrames, func(ch int) int32 {
		if ch == echoRefCh {
			return 0x7F0000 >> 0 // 8323072 — close to the 2^23 ceiling
		}
		return 0
	})
	out := b.EchoRef(raw)
	got := int16(uint16(out[0]) | uint16(out[1])<<8)
	if got == 32767 || got == -32768 {
		t.Fatalf("reference clipped at %d — EchoRef must extract at unity gain", got)
	}
	if b.ClippedSamples() != 0 {
		t.Fatalf("reference extraction clipped %d samples", b.ClippedSamples())
	}
}

func TestEchoRefRejectsShortBuffer(t *testing.T) {
	b := New()
	if out := b.EchoRef(make([]byte, frameSize-1)); out != nil {
		t.Fatal("a short period must report no reference, not a partial one")
	}
}
