package beamformer

import (
	"encoding/binary"
	"math"
	"testing"
)

func rookFrame(samples [rookChannels]int32) []byte {
	frame := make([]byte, 0, rookFrameSize)
	for _, sample := range samples {
		frame = append(frame, byte(sample), byte(sample>>8), byte(sample>>16))
	}
	return frame
}

func TestRookSixChannelLayoutKeepsLoopbackOutOfSpeech(t *testing.T) {
	front, ok := NewForTarget("rook").(*RookFrontEnd)
	if !ok {
		t.Fatal("rook selected another target's frontend")
	}
	raw := rookFrame([rookChannels]int32{2560, 5120, 7680, 10240, 25600, -25600})
	mono, angle := front.Process(raw, 0, 1)
	if angle != -1 {
		t.Fatalf("unmapped Rook array published a bearing: %v", angle)
	}
	if got := int16(binary.LittleEndian.Uint16(mono)); got != 25 {
		t.Fatalf("four-mic average = %d, want 25 (loopback must be excluded)", got)
	}
	ref := front.EchoRefInto(raw, nil)
	if got := int16(binary.LittleEndian.Uint16(ref)); got != 100 {
		t.Fatalf("loopback reference = %d, want 100", got)
	}
	if front.Diagnostics().HealthyMicChannels != 4 {
		t.Fatal("all four synthetic microphones should be healthy")
	}
}

func TestRookReferenceReusesBufferAndRejectsShortFrame(t *testing.T) {
	front := NewRook()
	if front.EchoRefInto(make([]byte, rookFrameSize-1), nil) != nil {
		t.Fatal("short capture frame produced a reference")
	}
	raw := append(rookFrame([rookChannels]int32{0, 0, 0, 0, 512, 0}), rookFrame([rookChannels]int32{0, 0, 0, 0, -512, 0})...)
	storage := make([]byte, 0, 4)
	ref := front.EchoRefInto(raw, storage)
	if &ref[0] != &storage[:cap(storage)][0] || len(ref) != 4 {
		t.Fatal("reference did not reuse caller buffer")
	}
	if int16(binary.LittleEndian.Uint16(ref[0:2])) != 2 || int16(binary.LittleEndian.Uint16(ref[2:4])) != -2 {
		t.Fatalf("reference sign or channel incorrect: %v", ref)
	}
}

func rookPlaneWave(direction, frames int, seed uint32, amplitude int32, live [rookMics]bool) []byte {
	arrival := rookArrivalSamples(direction)
	minimum := arrival[0]
	for _, value := range arrival[1:] {
		if value < minimum {
			minimum = value
		}
	}
	delays := [rookMics]float64{}
	for channel := range delays {
		delays[channel] = arrival[channel] - minimum
	}
	source := make([]float64, frames+16)
	state := seed
	for i := range source {
		state = state*1664525 + 1013904223
		source[i] = float64(int32(state>>16)-32768) * float64(amplitude) / 32768
	}
	raw := make([]byte, 0, frames*rookFrameSize)
	for frame := 0; frame < frames; frame++ {
		var samples [rookChannels]int32
		for channel := 0; channel < rookMics; channel++ {
			if !live[channel] {
				continue
			}
			position := float64(frame+8) - delays[channel]
			base := int(math.Floor(position))
			fraction := position - float64(base)
			value := source[base] + fraction*(source[base+1]-source[base])
			samples[channel] = int32(math.Round(value))
		}
		raw = append(raw, rookFrame(samples)...)
	}
	return raw
}

func TestRookMeasuredArrayLocalizesAndSteersCardinalSpeech(t *testing.T) {
	allLive := [rookMics]bool{true, true, true, true}
	for direction, wantAngle := range rookAngles {
		t.Run(string(rune('0'+direction)), func(t *testing.T) {
			raw := rookPlaneWave(direction, periodFrames*5, uint32(100+direction), 320000, allLive)
			front := NewRook()
			front.PrepareSpeechLock()
			omni, angle := front.Process(raw, -1, 1)
			if got := rookNearestDirection(angle); got != direction {
				t.Fatalf("DOA %.1f° mapped to %d, want direction %d (%.0f°); scores=%v",
					angle, got, direction, wantAngle, front.batchSpatial)
			}
			if diag := front.Diagnostics(); diag.Confidence < rookMinConfidence || diag.HealthyMicChannels != 4 {
				t.Fatalf("weak or unhealthy array result: %+v", diag)
			}
			beams := front.WakeBeams(raw, 1)
			if len(beams) != 2 || beams[0].Direction != direction {
				t.Fatalf("wake beams = %+v, want primary direction %d", beams, direction)
			}

			front.LockCurrent(true)
			steered, _ := front.Process(raw, -1, 1)
			if front.OutputChannel() != direction {
				t.Fatalf("AEC path = %d, want steered path %d", front.OutputChannel(), direction)
			}
			if got, base := pcmRMS(steered), pcmRMS(omni); got < base*1.05 {
				t.Fatalf("steered RMS %.1f did not improve four-mic omni %.1f", got, base)
			}
		})
	}
}

func TestRookWakeWinnerCarriesExactBeamIntoTurn(t *testing.T) {
	front := NewRook()
	raw := rookPlaneWave(3, periodFrames*5, 900, 280000, [rookMics]bool{true, true, true, true})
	front.Process(raw, -1, 1)
	front.LockWakeDirection(3, true)
	front.Process(raw, -1, 1)
	if front.lockedDirection != 3 || front.OutputChannel() != 3 {
		t.Fatalf("winner was not retained: lock=%d path=%d", front.lockedDirection, front.OutputChannel())
	}
}

func TestRookArrayDegradesWithoutBecomingDeaf(t *testing.T) {
	front := NewRook()
	raw := rookPlaneWave(0, periodFrames*5, 77, 260000, [rookMics]bool{true, false, false, false})
	mono, _ := front.Process(raw, -1, 1)
	if front.Diagnostics().HealthyMicChannels != 1 || pcmRMS(mono) == 0 {
		t.Fatalf("one-mic fallback failed: diag=%+v rms=%.1f", front.Diagnostics(), pcmRMS(mono))
	}
	beams := front.WakeBeams(raw, 1)
	if len(beams) != 1 || beams[0].Direction != rookOmniPath || pcmRMS(beams[0].PCM) == 0 {
		t.Fatalf("degraded wake path = %+v", beams)
	}
}

func TestRookLevelCalibrationUsesDiffuseSoundAndStaysBounded(t *testing.T) {
	front := NewRook()
	for block := 0; block < 32; block++ {
		channels := [rookMics][]int32{}
		amplitudes := [rookMics]int32{300000, 150000, 150000, 150000}
		for channel := range channels {
			channels[channel] = checkersNoise(periodFrames, uint32(1000+block*rookMics+channel), amplitudes[channel])
		}
		raw := make([]byte, 0, periodFrames*rookFrameSize)
		for frame := 0; frame < periodFrames; frame++ {
			var samples [rookChannels]int32
			for channel := 0; channel < rookMics; channel++ {
				samples[channel] = channels[channel][frame]
			}
			raw = append(raw, rookFrame(samples)...)
		}
		front.Process(raw, -1, 1)
	}
	diag := front.Diagnostics()
	if !diag.Calibrated || diag.LevelBalanceDB < 5 || diag.LevelBalanceDB > 6.2 {
		t.Fatalf("four-mic calibration = %+v, want measured 2:1 channel spread", diag)
	}
	for channel, gain := range front.levelGain {
		if gain < 0.707 || gain > 1.415 {
			t.Fatalf("channel %d calibration gain %.3f escaped +/-3 dB", channel, gain)
		}
	}
}
