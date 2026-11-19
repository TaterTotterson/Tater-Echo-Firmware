package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/config"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/outchain"
)

func TestApplyNativeOutputSettingsAcceptsControllerWireKeys(t *testing.T) {
	values := map[string]any{
		"eqBands":          []any{1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0},
		"eqLoudness":       true,
		"bassGuardEnabled": false,
		"bassGuardDb":      -24.0,
		"limiterEnabled":   true,
		"limiterThreshold": -2.5,
		"limiterRelease":   220.0,
	}
	applied := map[string]any{}
	for key, value := range values {
		applied[key] = value
	}
	msg := config.ConfigMessage{}

	if err := applyNativeOutputSettings(values, applied, &msg); err != nil {
		t.Fatal(err)
	}
	if len(msg.EqBands) != 8 || msg.EqBands[0] != 1 || msg.EqBands[7] != 8 {
		t.Fatalf("eq bands = %#v", msg.EqBands)
	}
	if msg.EqLoudness == nil || !*msg.EqLoudness {
		t.Fatalf("eq loudness = %#v", msg.EqLoudness)
	}
	if msg.BassGuardEnabled == nil || *msg.BassGuardEnabled || msg.BassGuardDb == nil || *msg.BassGuardDb != -24 {
		t.Fatalf("bass guard = enabled %#v floor %#v", msg.BassGuardEnabled, msg.BassGuardDb)
	}
	if msg.LimiterEnabled == nil || !*msg.LimiterEnabled || msg.LimiterThreshold == nil || *msg.LimiterThreshold != -2.5 || msg.LimiterRelease == nil || *msg.LimiterRelease != 220 {
		t.Fatalf("limiter = enabled %#v threshold %#v release %#v", msg.LimiterEnabled, msg.LimiterThreshold, msg.LimiterRelease)
	}
	for _, legacyKey := range []string{"eqBands", "eqLoudness", "bassGuardEnabled", "bassGuardDb", "limiterEnabled", "limiterThreshold", "limiterRelease"} {
		if _, ok := applied[legacyKey]; ok {
			t.Errorf("applied response kept legacy key %q", legacyKey)
		}
	}
	for _, nativeKey := range []string{"eq_bands", "eq_loudness", "bass_guard_enabled", "bass_guard_db", "limiter_enabled", "limiter_threshold_db", "limiter_release_ms"} {
		if _, ok := applied[nativeKey]; !ok {
			t.Errorf("applied response missing native key %q", nativeKey)
		}
	}
}

func TestNativeEQPresetsApplyCanonicalToneOnly(t *testing.T) {
	for _, preset := range outchain.EQPresets() {
		msg, applied := config.ConfigMessage{}, map[string]any{}
		if err := applyNativeOutputSettings(map[string]any{"eqPreset": preset.ID}, applied, &msg); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(msg.EqBands, preset.Bands[:]) || msg.EqLoudness == nil || *msg.EqLoudness || applied["eq_preset"] != preset.ID {
			t.Fatal("native preset was not resolved/reported", msg, applied)
		}
		if msg.BassGuardDb != nil || msg.BassGuardEnabled != nil || msg.LimiterEnabled != nil || msg.LimiterThreshold != nil || msg.LimiterRelease != nil {
			t.Fatal("preset changed hardware protection")
		}
	}
	msg, applied := config.ConfigMessage{}, map[string]any{}
	if err := applyNativeOutputSettings(map[string]any{"eq_bands": make([]float64, 8)}, applied, &msg); err != nil || applied["eq_preset"] != "manual" {
		t.Fatal("an explicit curve must select Manual", applied, err)
	}
	msg = config.ConfigMessage{}
	if err := applyNativeOutputSettings(map[string]any{"eq_preset": "manual"}, applied, &msg); err != nil || msg.EqBands != nil || msg.EqLoudness != nil {
		t.Fatal("selecting Manual must leave tone untouched", msg, err)
	}
	if err := applyNativeOutputSettings(map[string]any{"eq_preset": "unknown"}, applied, &msg); err == nil {
		t.Fatal("invalid native preset accepted")
	}
}

func TestApplyNativeOutputSettingsRejectsUnsafeValues(t *testing.T) {
	tests := []struct {
		name, want string
		values     map[string]any
	}{
		{name: "band count", values: map[string]any{"eq_bands": []any{0.0, 0.0}}, want: "exactly 8"},
		{name: "band gain", values: map[string]any{"eq_bands": []any{0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 13.0}}, want: "between -12 and 12"},
		{name: "guard floor", values: map[string]any{"bass_guard_db": -61.0}, want: "between -60 and 0"},
		{name: "limiter ceiling", values: map[string]any{"limiter_threshold_db": 1.0}, want: "between -12 and 0"},
		{name: "limiter release", values: map[string]any{"limiter_release_ms": 10.0}, want: "between 20 and 1000"},
		{name: "boolean", values: map[string]any{"limiter_enabled": "sometimes"}, want: "true or false"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := applyNativeOutputSettings(tc.values, map[string]any{}, &config.ConfigMessage{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}
