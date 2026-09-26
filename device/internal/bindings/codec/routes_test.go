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
