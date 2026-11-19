package outchain

import (
	"reflect"
	"testing"
)

func TestWarmDefaultAndPresetCopies(t *testing.T) {
	warm, ok := FindEQPreset("warm")
	if !ok || DefaultParams().Bands != warm.Bands || DefaultParams().Loudness {
		t.Fatal("new devices must start with Warm and no extra presence boost")
	}
	choices := EQPresets()
	choices[1].Bands[0] = 12
	if got, _ := FindEQPreset("warm"); got.Bands[0] != 3 {
		t.Fatal("preset definitions were modified through a returned copy")
	}
}

func TestResolveEQSettingsPreservesManualCurvesAndProtection(t *testing.T) {
	for _, preset := range EQPresets() {
		values := map[string]any{"eq_preset": preset.ID, "bass_guard_db": -10., "limiter_enabled": true}
		ResolveEQSettings(values)
		if !reflect.DeepEqual(values["eq_bands"], preset.Bands[:]) || values["eq_loudness"] != false || values["bass_guard_db"] != -10. || values["limiter_enabled"] != true {
			t.Fatal("preset did not set tone independently of protection", values)
		}
	}
	for _, values := range []map[string]any{
		{"eq_bands": make([]float64, NumBands)},
		{"eq_loudness": false},
		{"eq_preset": "warm", "eq_bands": make([]float64, NumBands)},
		{"eq_preset": "warm", "eq_loudness": true},
	} {
		before := values["eq_bands"]
		ResolveEQSettings(values)
		if values["eq_preset"] != "manual" || !reflect.DeepEqual(before, values["eq_bands"]) {
			t.Fatal("a manual/legacy curve was replaced or incorrectly named", values)
		}
	}
	manual := map[string]any{"eq_preset": "manual"}
	ResolveEQSettings(manual)
	if len(manual) != 1 {
		t.Fatal("selecting Manual must preserve the current sound")
	}
}
