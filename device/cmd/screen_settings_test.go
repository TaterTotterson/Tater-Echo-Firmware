package main

import (
	"testing"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

func TestApplyScreenSettingsNormalizesDisplayPolicy(t *testing.T) {
	snapshot := show.Snapshot{
		ScreenBrightness:            35,
		ScreenAutoBrightnessMinimum: 12,
		ScreenNightBrightness:       3,
		ScreenNightStart:            "22:00",
		ScreenNightEnd:              "07:00",
		ScreenNightDisplay:          "red_clock",
	}
	applyScreenSettings(&snapshot, map[string]any{
		"screen_brightness":              140,
		"screen_auto_brightness_enabled": true,
		"screen_auto_brightness_minimum": -5,
		"screen_night_mode_enabled":      true,
		"screen_night_brightness":        3,
		"screen_night_start":             "21:30",
		"screen_night_end":               "invalid",
		"screen_night_display":           "red_clock",
		"screen_clock_format":            "24h",
	})
	if snapshot.ScreenBrightness != 100 || !snapshot.ScreenAutoBrightness || snapshot.ScreenAutoBrightnessMinimum != 1 {
		t.Fatalf("day screen settings = %#v", snapshot)
	}
	if !snapshot.ScreenNightMode || snapshot.ScreenNightBrightness != 3 || snapshot.ScreenNightStart != "21:30" || snapshot.ScreenNightEnd != "07:00" || snapshot.ScreenNightDisplay != "red_clock" || snapshot.ScreenClockFormat != "24h" {
		t.Fatalf("night screen settings = %#v", snapshot)
	}
}

func TestScreenSettingNormalizersHaveSafeFallbacks(t *testing.T) {
	if got := normalizeScreenNightDisplay("unknown"); got != "red_clock" {
		t.Fatalf("night display = %q", got)
	}
	for value, want := range map[string]string{"24h": "24h", " 24 ": "24h", "12h": "12h", "": "12h", "unknown": "12h"} {
		if got := normalizeScreenClockFormat(value); got != want {
			t.Fatalf("clock format %q = %q, want %q", value, got, want)
		}
	}
	if got := normalizeScreenClock("7:05", "22:00"); got != "07:05" {
		t.Fatalf("normalized non-padded time = %q", got)
	}
	if got := normalizeScreenClock("07:05", "22:00"); got != "07:05" {
		t.Fatalf("valid time = %q", got)
	}
}
