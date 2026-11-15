package client

import (
	"bytes"
	"testing"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/beamformer"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/micgain"
)

// Generated low-level microphone signal, with independent unamplified loopback
// channels. This checks gain staging, not real-world ADC headroom or SNR.
func gainStagingRaw(gain int32) []byte {
	raw := make([]byte, 2560*27)
	for f := 0; f < 2560; f++ {
		for ch := 0; ch < 9; ch++ {
			value := int32((f%97)-48) * 19
			if ch < 7 {
				value *= gain
			}
			base := f*27 + ch*3
			raw[base], raw[base+1], raw[base+2] = byte(value), byte(value>>8), byte(value>>16)
		}
	}
	return raw
}

func TestHardwareGainPreservesPCMAndEchoReference(t *testing.T) {
	for _, target := range []string{"biscuit", "radar"} {
		t.Run(target, func(t *testing.T) {
			low, high := gainStagingRaw(1), gainStagingRaw(100)
			baseline, hardware := beamformer.New(), beamformer.New()
			pcmGain, _ := micgain.Factors(target, 40) // stale config cannot double it
			want, _ := baseline.Process(low, -1, 100)
			got, _ := hardware.Process(high, -1, pcmGain)
			if !bytes.Equal(want, got) || bytes.Equal(got, make([]byte, len(got))) {
				t.Fatal("hardware40+software0 does not match software40 PCM")
			}
			if !bytes.Equal(baseline.EchoRefInto(low, nil), hardware.EchoRefInto(high, nil)) {
				t.Fatal("microphone gain changed the hardware echo reference")
			}
			for direction := 0; direction < 6; direction++ {
				want := baseline.WakeBeam(low, direction, 100).PCM
				got := hardware.WakeBeam(high, direction, pcmGain).PCM
				if !bytes.Equal(want, got) {
					t.Fatalf("wake beam %d changed after equivalent gain relocation", direction)
				}
			}
			if baseline.ClippedSamples() != hardware.ClippedSamples() {
				t.Fatal("equivalent waveform software-clipping count changed")
			}
		})
	}
}

func TestHardwareGainPreservesLevelDecisions(t *testing.T) {
	for _, target := range []string{"biscuit", "radar"} {
		_, levelGain := micgain.Factors(target, 0)
		for _, rms := range []float64{0, 0.001, 0.009, 0.01, 0.02, 0.39, 0.4, 0.41} {
			for _, speech := range []bool{false, true} {
				if doaActivity(rms, 100, speech) != doaActivity(rms, levelGain, speech) {
					t.Fatalf("%s DOA decision changed at rms=%v", target, rms)
				}
			}
			for _, threshold := range []float64{0.001, 0.004, 0.01} {
				if (rms >= threshold*100) != (rms >= threshold*levelGain) {
					t.Fatalf("%s VAD decision changed at rms=%v threshold=%v", target, rms, threshold)
				}
			}
		}
	}
}
