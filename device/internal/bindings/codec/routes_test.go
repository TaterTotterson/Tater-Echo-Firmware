package codec

import "testing"

// A wrong name here is silence rather than an error, and the two ends failed
// independently: the capture routes leave the ADCs powered down, the playback
// routes leave the DAC powered down, and either alone is a device that looks
// healthy in every log it writes.
func TestRoutesCoverBothEndsOfTheAudioPath(t *testing.T) {
	want := map[string]bool{
		// capture: the DIFFERENTIAL inputs into all four ADCs — the
		// single-ended "IN2" switches beside them are the wrong ones
		"ADC_D Right Ip Select ADC_D DIF1_R switch": true,
		"ADC_D Left Ip Select ADC_D DIF1_L switch":  true,
		"ADC_C Right Ip Select ADC_C DIF1_R switch": true,
		"ADC_C Left Ip Select ADC_C DIF1_L switch":  true,
		"ADC_B Right Ip Select ADC_B DIF1_R switch": true,
		"ADC_B Left Ip Select ADC_B DIF1_L switch":  true,
		"ADC_A Right Ip Select ADC_A DIF1_R switch": true,
		"ADC_A Left Ip Select ADC_A DIF1_L switch":  true,
		// playback: DAC into the output mixer
		"HPR Output Mixer R_DAC Switch": true,
		"HPL Output Mixer L_DAC Switch": true,
	}

	got := map[string]bool{}
	for _, w := range Routes {
		if got[w.Name] {
			t.Errorf("%s listed twice", w.Name)
		}
		if w.Value != "1" {
			t.Errorf("%s: value %q, want \"1\" — every route here is a switch to close", w.Name, w.Value)
		}
		got[w.Name] = true
	}
	for name := range want {
		if !got[name] {
			t.Errorf("missing %s", name)
		}
	}
	for name := range got {
		if !want[name] {
			t.Errorf("unexpected %s", name)
		}
	}
}

func TestCheckersRoutesUseItsMeasuredCodecs(t *testing.T) {
	want := map[string]string{
		"ADC_A Left Ip Select ADC_A DIF1_L switch":  "1",
		"ADC_A Right Ip Select ADC_A DIF1_R switch": "1",
		"ADC_A MICPGA Volume Ctrl":                  "40",
		"DAC MIXL INF1 Switch":                      "1",
		"DAC MIXR INF1 Switch":                      "1",
		"OUT MIXL DAC L1 Switch":                    "1",
		"OUT MIXR DAC R1 Switch":                    "1",
		"LOUT MIX OUTVOL L Switch":                  "1",
		"LOUT MIX OUTVOL R Switch":                  "1",
		"OUT Playback Switch":                       "1",
	}
	got := map[string]string{}
	for _, write := range CheckersRoutes {
		if _, duplicate := got[write.Name]; duplicate {
			t.Fatalf("duplicate Checkers route %q", write.Name)
		}
		got[write.Name] = write.Value
		// Biscuit's output mixers do not exist on the RT5616.
		if write.Name == "HPR Output Mixer R_DAC Switch" || write.Name == "HPL Output Mixer L_DAC Switch" {
			t.Fatalf("Checkers inherited Biscuit route %q", write.Name)
		}
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("Checkers route %q = %q, want %q", name, got[name], value)
		}
	}
}

func TestRadarRoutesKeepPuffinCaptureAndOpenItsAmpGate(t *testing.T) {
	got := map[string]string{}
	for _, write := range RadarRoutes {
		if _, duplicate := got[write.Name]; duplicate {
			t.Fatalf("duplicate Radar route %q", write.Name)
		}
		got[write.Name] = write.Value
	}
	for _, name := range []string{
		"ADC_A Left Ip Select ADC_A DIF1_L switch",
		"ADC_B Left Ip Select ADC_B DIF1_L switch",
		"ADC_C Left Ip Select ADC_C DIF1_L switch",
		"ADC_D Left Ip Select ADC_D DIF1_L switch",
		"HPL Output Mixer L_DAC Switch",
		"HPR Output Mixer R_DAC Switch",
	} {
		if got[name] != "1" {
			t.Errorf("Radar route %q = %q, want 1", name, got[name])
		}
	}
	if got["MFP Gpio Mute"] != "Off" {
		t.Fatalf("Radar MFP2 gate = %q, want Off (active-low enable)", got["MFP Gpio Mute"])
	}
	if got["Headphone_Speaker_Mux"] != "Speaker" {
		t.Fatalf("Radar output mux = %q, want Speaker", got["Headphone_Speaker_Mux"])
	}
	if got["Right Channel Only"] != "On" {
		t.Fatalf("Radar internal speaker channel = %q, want On", got["Right Channel Only"])
	}
	if len(radarSpeakerEQ) != 117 {
		t.Fatalf("Radar speaker EQ has %d bytes, want the measured 117-byte control", len(radarSpeakerEQ))
	}
	for block := 0; block < 6; block++ {
		off := block * 15
		if radarSpeakerEQ[off] != 128 || radarSpeakerEQ[off+2] != 1 {
			t.Fatalf("Radar unity EQ block %d is corrupt: %v", block, radarSpeakerEQ[off:off+15])
		}
	}
}

func TestRookRoutesUseTwoADCsAndDotStyleOutput(t *testing.T) {
	got := map[string]string{}
	for _, write := range RookRoutes {
		if _, duplicate := got[write.Name]; duplicate {
			t.Fatalf("duplicate Rook route %q", write.Name)
		}
		got[write.Name] = write.Value
	}
	for _, name := range []string{
		"ADC_A Left Ip Select ADC_A DIF1_L switch",
		"ADC_A Right Ip Select ADC_A DIF1_R switch",
		"ADC_B Left Ip Select ADC_B DIF1_L switch",
		"ADC_B Right Ip Select ADC_B DIF1_R switch",
		"HPL Output Mixer L_DAC Switch",
		"HPR Output Mixer R_DAC Switch",
	} {
		if got[name] != "1" {
			t.Errorf("Rook route %q = %q, want 1", name, got[name])
		}
	}
	if _, exists := got["ADC_C Left Ip Select ADC_C DIF1_L switch"]; exists {
		t.Fatal("Rook routes inherited nonexistent ADC_C")
	}
}
