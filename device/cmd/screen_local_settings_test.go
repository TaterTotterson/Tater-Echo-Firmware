//go:build linux

package main

import (
	"path/filepath"
	"testing"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

func TestLocalScreenCommandValidatesBeforeCallingHardware(t *testing.T) {
	for _, values := range []map[string]any{
		{"ssh_password": "no"},
		{"wake_sound_enabled": true, "screen_brightness": 50},
		{"wake_sound": "waterdrop"},
		{"continued_chat": false},
		{"barge_in_enabled": true},
	} {
		called := false
		if err := applyLocalScreenCommand(nil, values, func(map[string]any) error { called = true; return nil }); err == nil || called {
			t.Fatalf("unsafe or host-managed local command reached hardware: %#v", values)
		}
	}
	if settingsChangeWake(map[string]any{"screen_brightness": 80, "wake_sound_enabled": true, "volume_percent": 50}) {
		t.Fatal("ordinary controls restart wake detection")
	}
	if !settingsChangeWake(map[string]any{"wake_threshold": .7}) {
		t.Fatal("wake model change did not refresh detection")
	}
	if settingsChangeCapture(map[string]any{"screen_brightness": 80, "wake_sound_enabled": true}) || settingsChangeOutput(map[string]any{"display_theme": "forest"}) {
		t.Fatal("ordinary screen changes reconfigure audio")
	}
	if values := map[string]any{"screen_text_style": "matrix"}; settingsChangeWake(values) || settingsChangeCapture(values) || settingsChangeOutput(values) {
		t.Fatal("conversation text style must not reconfigure wake, microphones or audio output")
	}
}

func TestLocalPreferencesMergeAndPersistPresentation(t *testing.T) {
	t.Setenv("TATER_SCREEN_SETTINGS_PATH", filepath.Join(t.TempDir(), "settings.json"))
	screen := show.New("127.0.0.1:0", show.Snapshot{Settings: initialScreenSettings(42)}, nil)
	updateScreenPreferences(screen, map[string]any{"display_theme": "forest", "screen_brightness": 55, "screen_popup_effect": "slide", "screen_text_style": "matrix"})
	state := screen.Snapshot()
	if state.ScreenBrightness != 55 || state.DisplayTheme != "forest" || state.Settings["volume_percent"] != 42 || state.SettingsError != "" {
		t.Fatalf("invalid preferences: %#v", state)
	}
	loaded := show.LoadScreenSettings(screenSettingsPath())
	if loaded["screen_popup_effect"] != "slide" || loaded["screen_text_style"] != "matrix" {
		t.Fatal("local motion choice was not saved")
	}
}
