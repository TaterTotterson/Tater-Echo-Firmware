package beamformer

import (
	"encoding/binary"
	"math"
	"testing"
)

func packed24(value int32) []byte {
	return []byte{byte(value), byte(value >> 8), byte(value >> 16)}
}

func TestCheckersFrontEndAveragesTheTwoADCChannels(t *testing.T) {
	raw := append([]byte{}, packed24(2560)...)
	raw = append(raw, packed24(7680)...)
	raw = append(raw, packed24(0)...)
	raw = append(raw, packed24(0)...)

	front := NewForTarget("checkers")
	mono, angle := front.Process(raw, 180, 1)
	if angle != -1 {
		t.Fatalf("angle = %v, want unavailable", angle)
	}
	if len(mono) != 2 {
		t.Fatalf("mono bytes = %d", len(mono))
	}
	// Average is 5120 in S24, or 20 after 24→16 conversion.
	if got := int16(binary.LittleEndian.Uint16(mono)); got != 20 {
		t.Fatalf("sample = %d, want 20", got)
	}
	if ref := front.EchoRefInto(raw, nil); ref != nil {
		t.Fatalf("unverified Checkers echo reference = %v", ref)
	}
}

func TestTargetFrontEndsStayDistinct(t *testing.T) {
	if _, ok := NewForTarget("biscuit").(*Beamformer); !ok {
		t.Fatal("biscuit did not get seven-mic beamformer")
	}
	if _, ok := NewForTarget("checkers").(*CheckersFrontEnd); !ok {
		t.Fatal("checkers did not get four-channel front end")
	}
}

func checkersRaw(left, right []int32) []byte {
	frames := len(left)
	if len(right) < frames {
		frames = len(right)
	}
	raw := make([]byte, 0, frames*checkersFrameSize)
	for i := 0; i < frames; i++ {
		raw = append(raw, packed24(left[i])...)
		raw = append(raw, packed24(right[i])...)
		raw = append(raw, 0, 0, 0, 0, 0, 0)
	}
	return raw
}

func checkersNoise(frames int, seed uint32, amplitude int32) []int32 {
	out := make([]int32, frames)
	state := seed
	for i := range out {
		state = state*1664525 + 1013904223
		out[i] = int32(int64(int32(state>>16)-32768) * int64(amplitude) / 32768)
	}
	return out
}

func pcmRMS(pcm []byte) float64 {
	var power float64
	for i := 0; i+1 < len(pcm); i += 2 {
		sample := float64(int16(binary.LittleEndian.Uint16(pcm[i:])))
		power += sample * sample
	}
	return math.Sqrt(power / float64(len(pcm)/2))
}

func TestCheckersFrontEndFindsAndAlignsInterMicDelay(t *testing.T) {
	const delay = 3
	left := checkersNoise(periodFrames, 1, 300000)
	right := make([]int32, periodFrames)
	copy(right[delay:], left[:periodFrames-delay])

	front := NewCheckers()
	mono, _ := front.Process(checkersRaw(left, right), -1, 1)
	diag := front.Diagnostics()
	if diag.DelaySamples < 2.5 || diag.DelaySamples > 3.5 {
		t.Fatalf("delay = %.3f samples, want about %d", diag.DelaySamples, delay)
	}
	if diag.Confidence < 0.90 || diag.Coherence < 0.90 {
		t.Fatalf("weak coherent lock: confidence=%.3f coherence=%.3f", diag.Confidence, diag.Coherence)
	}
	// A correctly aligned sum should retain essentially the source level;
	// an unaligned average of this wide-band sequence would be about -3 dB.
	want := float64(300000) / math.Sqrt(3) / 256
	if got := pcmRMS(mono); got < want*0.90 {
		t.Fatalf("aligned RMS = %.1f, want at least %.1f", got, want*0.90)
	}
}

func TestCheckersFrontEndLearnsOnlyBoundedLevelBalance(t *testing.T) {
	front := NewCheckers()
	for block := 0; block < 48; block++ {
		left := checkersNoise(periodFrames, uint32(10+block), 300000)
		right := checkersNoise(periodFrames, uint32(1000+block), 150000)
		front.Process(checkersRaw(left, right), -1, 1)
	}
	diag := front.Diagnostics()
	if !diag.Calibrated {
		t.Fatal("front end did not complete level calibration")
	}
	if diag.LevelBalanceDB < 5.0 || diag.LevelBalanceDB > 6.1 {
		t.Fatalf("level balance = %.2f dB, want measured 2:1 mismatch", diag.LevelBalanceDB)
	}
}

func TestCheckersFrontEndCanCalibrateFromBroadsideSpeech(t *testing.T) {
	front := NewCheckers()
	for block := 0; block < 30; block++ {
		right := checkersNoise(periodFrames, uint32(300+block), 140000)
		left := make([]int32, len(right))
		for i := range left {
			left[i] = 2 * right[i]
		}
		front.Process(checkersRaw(left, right), -1, 1)
	}
	if diag := front.Diagnostics(); !diag.Calibrated || diag.LevelBalanceDB < 5.0 {
		t.Fatalf("broadside calibration = %+v, want a calibrated 2:1 level trim", diag)
	}
}

func TestCheckersFrontEndDoesNotLearnDirectionalSpeechAsADCMismatch(t *testing.T) {
	const delay = 3
	front := NewCheckers()
	for block := 0; block < 30; block++ {
		left := checkersNoise(periodFrames, uint32(600+block), 280000)
		right := make([]int32, periodFrames)
		for i := delay; i < len(right); i++ {
			right[i] = left[i-delay] / 2
		}
		front.Process(checkersRaw(left, right), -1, 1)
	}
	diag := front.Diagnostics()
	if diag.Calibrated || math.Abs(diag.LevelBalanceDB) > 0.25 {
		t.Fatalf("directional source polluted level calibration: %+v", diag)
	}
}

func TestCheckersSpatialSuppressionAndFastSpeechRecovery(t *testing.T) {
	front := NewCheckers()
	var noisy []byte
	for block := 0; block < 24; block++ {
		left := checkersNoise(periodFrames, uint32(200+block), 250000)
		right := checkersNoise(periodFrames, uint32(900+block), 250000)
		noisy, _ = front.Process(checkersRaw(left, right), -1, 1)
	}
	if gain := front.Diagnostics().NoiseGain; gain >= 0.80 || gain < checkersNoiseMinGain {
		t.Fatalf("diffuse-noise gain = %.3f, want calibrated conservative suppression", gain)
	}
	if pcmRMS(noisy) == 0 {
		t.Fatal("noise suppression became a hard gate")
	}

	speech := checkersNoise(periodFrames, 77, 250000)
	front.Process(checkersRaw(speech, speech), -1, 1)
	diag := front.Diagnostics()
	if diag.NoiseGain < 0.90 {
		t.Fatalf("speech recovery gain = %.3f, want fast recovery above 0.90", diag.NoiseGain)
	}
	if diag.Coherence < 0.98 {
		t.Fatalf("coherent speech score = %.3f, want near 1", diag.Coherence)
	}
}

func TestCheckersFrontEndFallsBackWhenOneMicIsSilent(t *testing.T) {
	left := checkersNoise(periodFrames, 42, 300000)
	right := make([]int32, periodFrames)
	front := NewCheckers()
	mono, _ := front.Process(checkersRaw(left, right), -1, 1)
	if got := front.Diagnostics().HealthyMicChannels; got != 1 {
		t.Fatalf("healthy channels = %d, want one-mic fallback", got)
	}
	want := float64(300000) / math.Sqrt(3) / 256
	if got := pcmRMS(mono); got < want*0.90 {
		t.Fatalf("fallback RMS = %.1f, want unhalved live mic near %.1f", got, want)
	}
}
