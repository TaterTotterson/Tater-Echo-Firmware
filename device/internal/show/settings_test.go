package show

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/outchain"
)

func TestScreenSettingsRejectUnsafeAndMalformedChanges(t *testing.T) {
	for _, values := range []map[string]any{
		{"ssh_password": "secret"}, {"wake_word_url": "https://invalid"}, {"aec_enabled": false},
		{"screen_brightness": 101}, {"screen_brightness": math.NaN()}, {"screen_brightness": "35"}, {"screen_brightness": nil},
		{"screen_auto_brightness_enabled": 1}, {"display_theme": "tater,ocean"}, {"screen_popup_effect": "tilt"},
		{"screen_text_style": "rain"}, {"screen_text_style": true}, {"screen_text_style": nil},
		{"screen_night_start": "25:00"}, {"screen_auto_brightness_minimum": 0}, {"eq_bands": []float64{1}},
	} {
		if _, err := ValidateScreenSettings(values); err == nil {
			t.Fatalf("accepted invalid settings %#v", values)
		}
	}
	values, err := ValidateScreenSettings(map[string]any{"screen_brightness": float64(35), "screen_popup_effect": "fade", "eq_bands": make([]float64, 8)})
	if err != nil || values["screen_brightness"] != 35 {
		t.Fatalf("valid settings rejected: %#v %v", values, err)
	}
}

func TestScreenPreferencesRoundTripAndDoNotLeakSecretsOrSlices(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	values := ScreenSettingDefaults()
	values["ssh_password"] = "not saved"
	values["wake_word_url"] = "not saved"
	values["screen_popup_effect"] = "slide"
	values["screen_text_style"] = "matrix"
	copy := CopyScreenSettings(values)
	firstBand := values["eq_bands"].([]float64)[0]
	copy["eq_bands"].([]float64)[0] = 5
	if values["eq_bands"].([]float64)[0] != firstBand {
		t.Fatal("settings copy aliases the equalizer")
	}
	if err := SaveScreenSettings(path, values); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("preferences are not private")
	}
	if string(data) == "" {
		t.Fatal("empty preferences")
	}
	loaded := LoadScreenSettings(path)
	if loaded["screen_popup_effect"] != "slide" || loaded["screen_text_style"] != "matrix" || loaded["ssh_password"] != nil || loaded["wake_word_url"] != nil {
		t.Fatalf("invalid round trip: %#v", loaded)
	}
}

func TestEqualizerPresetValidationPersistenceAndLegacyManualMigration(t *testing.T) {
	if ScreenSettingDefaults()["eq_preset"] != "warm" {
		t.Fatal("new screen defaults must select Warm")
	}
	for _, preset := range outchain.EQPresets() {
		values, err := ValidateScreenSettings(map[string]any{"eq_preset": preset.ID})
		if err != nil || !reflect.DeepEqual(values["eq_bands"], preset.Bands[:]) || values["eq_loudness"] != false {
			t.Fatal("preset selection did not resolve its curve", preset, values, err)
		}
		path := filepath.Join(t.TempDir(), "preferences.json")
		if err := SaveScreenSettings(path, values); err != nil {
			t.Fatal(err)
		}
		if loaded := LoadScreenSettings(path); !reflect.DeepEqual(loaded, values) {
			t.Fatal("preset changed during save/restore", loaded, values)
		}
	}
	for _, data := range []string{
		`{"eq_bands":[0,0,0,0,0,0,0,0],"eq_loudness":false}`,
		`{"eq_bands":[3,3,-1,-3,-2,-1,-2,-3],"eq_loudness":false}`,
		`{"eq_preset":"warm","eq_bands":[1,2,3,4,5,6,7,8]}`,
	} {
		path := filepath.Join(t.TempDir(), "legacy.json")
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		var original map[string]any
		_ = json.Unmarshal([]byte(data), &original)
		loaded := LoadScreenSettings(path)
		actual, _ := json.Marshal(loaded["eq_bands"])
		expected, _ := json.Marshal(original["eq_bands"])
		if loaded["eq_preset"] != "manual" || string(actual) != string(expected) {
			t.Fatal("old user's EQ was reset or auto-assigned a preset", loaded)
		}
	}
	for _, invalid := range []any{"bass-boost", nil, 1} {
		if _, err := ValidateScreenSettings(map[string]any{"eq_preset": invalid}); err == nil {
			t.Fatal("invalid preset accepted", invalid)
		}
	}
}

func TestConversationTextStyleDefaultsAndValidation(t *testing.T) {
	if ScreenSettingDefaults()["screen_text_style"] != "smooth" {
		t.Fatal("existing speech text animation must remain the default")
	}
	for _, style := range []string{"smooth", "matrix"} {
		values, err := ValidateScreenSettings(map[string]any{"screen_text_style": style})
		if err != nil || values["screen_text_style"] != style {
			t.Fatalf("valid conversation text choice rejected: %s, %v", style, err)
		}
	}
	for _, value := range []any{nil, "", "unknown", 42} {
		if NormalizeTextStyle(value) != "smooth" {
			t.Fatalf("unknown style %v changed legacy behavior", value)
		}
	}
	if NormalizeTextStyle(" Matrix ") != "matrix" {
		t.Fatal("matrix style was not normalized")
	}
	path := filepath.Join(t.TempDir(), "old-preferences.json")
	if err := os.WriteFile(path, []byte(`{"screen_popup_effect":"slide","screen_text_style":"unknown"}`), 0600); err != nil {
		t.Fatal(err)
	}
	loaded := LoadScreenSettings(path)
	if NormalizeTextStyle(loaded["screen_text_style"]) != "smooth" || loaded["screen_popup_effect"] != "slide" {
		t.Fatal("legacy preference migration lost the default or unrelated preferences")
	}
}

func TestScreenSettingsKeepSharedVoiceControlsHostManaged(t *testing.T) {
	shared := map[string]any{
		"wake_sound_enabled": true,
		"wake_sound":         "waterdrop",
		"wake_sound_url":     "https://invalid/sound.wav",
		"continued_chat":     false,
		"barge_in_enabled":   true,
	}
	values := ScreenSettingDefaults()
	values["screen_popup_effect"] = "slide"
	for key, value := range shared {
		if _, err := ValidateScreenSettings(map[string]any{key: value, "screen_brightness": 50}); err == nil {
			t.Fatalf("host-managed %s accepted as a local change", key)
		}
		values[key] = value
	}
	path := filepath.Join(t.TempDir(), "preferences.json")
	if err := SaveScreenSettings(path, values); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	for key := range shared {
		if _, exists := saved[key]; exists {
			t.Fatalf("saved host-managed %s in local preferences", key)
		}
	}
	// An older UI may already have written these keys. Ignore them on restore
	// without losing the device's remaining display/EQ preferences.
	data, err = json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	loaded := LoadScreenSettings(path)
	for key := range shared {
		if _, exists := loaded[key]; exists {
			t.Fatalf("restored legacy host-managed %s", key)
		}
	}
	if loaded["screen_popup_effect"] != "slide" || len(loaded["eq_bands"].([]float64)) != 8 {
		t.Fatalf("shared voice preferences erased unrelated local settings: %#v", loaded)
	}
}
