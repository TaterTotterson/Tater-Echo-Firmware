package codec

import (
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/mixer"
)

type codecMixerFake struct {
	writes     []Write
	byteWrites int
	lastBytes  []byte
}

func (f *codecMixerFake) Set(name string, values []string) error {
	for _, value := range values {
		f.writes = append(f.writes, Write{Name: name, Value: value})
	}
	return nil
}

func (f *codecMixerFake) Get(string) (string, error) { return "", nil }

func (f *codecMixerFake) SetBytes(_ string, value []byte) error {
	f.byteWrites++
	f.lastBytes = append([]byte(nil), value...)
	return nil
}

func TestInputGainsMatchEachTargetsPhysicalADCs(t *testing.T) {
	tests := map[string][]string{
		"biscuit":  {"A", "B", "C", "D"},
		"radar":    {"A", "B", "C", "D"},
		"checkers": {"A"},
		"cronos":   {"A"},
		"crown":    {"A", "B"},
		"athena":   {},
		"rook":     {"A", "B"},
	}
	for target, adcs := range tests {
		t.Run(target, func(t *testing.T) {
			got := inputGainsForTarget(target)
			if len(got) != len(adcs)*2 {
				t.Fatalf("%s has %d input-gain writes, want %d", target, len(got), len(adcs)*2)
			}
			want := make(map[string]bool, len(got))
			for _, adc := range adcs {
				want["ADC_"+adc+" DIF1_L Input Gain"] = true
				want["ADC_"+adc+" DIF1_R Input Gain"] = true
			}
			seen := make(map[string]bool, len(got))
			for _, write := range got {
				if seen[write.Name] {
					t.Errorf("duplicate input gain %q", write.Name)
				}
				seen[write.Name] = true
				if !want[write.Name] {
					t.Errorf("unexpected input gain %q", write.Name)
				}
				if write.Value != "0" {
					t.Errorf("%s = %q, want 0 (Off)", write.Name, write.Value)
				}
			}
			for name := range want {
				if !seen[name] {
					t.Errorf("missing input gain %q", name)
				}
			}
		})
	}
}

func TestInputGainsAreNotMixedIntoDAPMRoutes(t *testing.T) {
	for target, routes := range map[string][]Write{
		"biscuit":  Routes,
		"radar":    RadarRoutes,
		"checkers": CheckersRoutes,
		"cronos":   CronosRoutes,
		"crown":    CrownRoutes,
		"athena":   AthenaRoutes,
		"rook":     RookRoutes,
	} {
		for _, write := range routes {
			if strings.HasSuffix(write.Name, " Input Gain") {
				t.Errorf("%s route contains separately-applied input gain %q", target, write.Name)
			}
		}
	}
}

func TestEnsureRoutesWritesEveryTargetInputGain(t *testing.T) {
	for _, target := range []string{"biscuit", "radar", "checkers", "cronos", "crown", "athena", "rook"} {
		t.Run(target, func(t *testing.T) {
			once = sync.Once{}
			backend := &codecMixerFake{}
			mixer.Use(backend)
			EnsureRoutes(target)

			var got []Write
			for _, write := range backend.writes {
				if strings.HasSuffix(write.Name, " Input Gain") {
					got = append(got, write)
				}
			}
			want := inputGainsForTarget(target)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s input-gain writes = %v, want %v", target, got, want)
			}
			if target == "radar" && backend.byteWrites != 1 {
				t.Fatalf("Radar byte-typed speaker tuning writes = %d, want 1", backend.byteWrites)
			}
		})
	}
	once = sync.Once{}
}

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

func TestCrownRoutesUseTwoADCsAndCheckersPlayback(t *testing.T) {
	got := map[string]string{}
	for _, write := range CrownRoutes {
		if _, duplicate := got[write.Name]; duplicate {
			t.Fatalf("duplicate Crown route %q", write.Name)
		}
		got[write.Name] = write.Value
	}
	for _, name := range []string{
		"ADC_A Left Ip Select ADC_A DIF1_L switch",
		"ADC_A Right Ip Select ADC_A DIF1_R switch",
		"ADC_B Left Ip Select ADC_B DIF1_L switch",
		"ADC_B Right Ip Select ADC_B DIF1_R switch",
		"DAC MIXL INF1 Switch", "DAC MIXR INF1 Switch",
		"LOUT MIX OUTVOL L Switch", "LOUT MIX OUTVOL R Switch",
		"OUT Playback Switch",
	} {
		if got[name] != "1" {
			t.Errorf("Crown route %q = %q, want 1", name, got[name])
		}
	}
	for _, name := range []string{
		"ADC_A Left Mute", "ADC_A Right Mute",
		"ADC_B Left Mute", "ADC_B Right Mute",
	} {
		if got[name] != "Off" {
			t.Errorf("Crown route %q = %q, want Off", name, got[name])
		}
	}
	if _, exists := got["HPL Output Mixer L_DAC Switch"]; exists {
		t.Fatal("Crown inherited the wrong output codec route")
	}
}

func TestRadarRoutesKeepPuffinCaptureAndHoldItsAmpMute(t *testing.T) {
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
	if got["MFP Gpio Mute"] != "On" {
		t.Fatalf("Radar MFP2 gate = %q, want On while programming the path", got["MFP Gpio Mute"])
	}
	if got["Right Channel Only"] != "Off" {
		t.Fatalf("Radar speaker channel mode = %q, want Off (both channels)", got["Right Channel Only"])
	}
	if got["DRC Control"] != "Disabled" {
		t.Fatalf("Radar DRC = %q, want Disabled", got["DRC Control"])
	}
	if len(radarFlatEQ) != 117 {
		t.Fatalf("Radar flat EQ has %d bytes, want the 117-byte control", len(radarFlatEQ))
	}
	for block := 0; block < 6; block++ {
		off := block * 15
		if radarFlatEQ[off] != 128 || radarFlatEQ[off+2] != 1 {
			t.Fatalf("Radar unity EQ block %d is corrupt: %v", block, radarFlatEQ[off:off+15])
		}
	}
}

func TestAthenaRoutesUseNamedLowJitterMux(t *testing.T) {
	for _, write := range AthenaRoutes {
		if write.Name == "I2S2_HD_Mux" {
			if write.Value != "Low_Jitter" {
				t.Fatalf("Athena I2S2 high-definition mux = %q, want Low_Jitter enum name", write.Value)
			}
			return
		}
	}
	t.Fatal("Athena routes do not configure I2S2_HD_Mux")
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
