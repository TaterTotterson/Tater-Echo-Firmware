package show

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/outchain"
)

// ScreenSettingDefaults is deliberately limited to safe local preferences.
// Shared wake sounds, continued chat, and barge-in stay host-managed. Model
// URLs, credentials, microphones, OTA, and speaker protection are not writable
// through the touch renderer either.
func ScreenSettingDefaults() map[string]any {
	preset, _ := outchain.FindEQPreset(outchain.DefaultEQPreset)
	return map[string]any{
		"volume_percent": 65,
		"display_theme":  "tater", "display_edge_glow": true, "screen_popup_effect": "fade",
		"screen_text_style":                  "smooth",
		"display_wallpaper_interval_minutes": 1440,
		"screen_brightness":                  35, "screen_auto_brightness_enabled": false,
		"screen_auto_brightness_minimum": 12, "screen_night_mode_enabled": false,
		"screen_night_brightness": 3, "screen_night_start": "22:00", "screen_night_end": "07:00",
		"screen_night_display": "red_clock", "screen_clock_format": "12h",
		"dnd_enabled": false, "dnd_schedule_enabled": false, "dnd_start": "22:00", "dnd_end": "07:00",
		"eq_preset":   preset.ID,
		"eq_loudness": false,
		"eq_bands":    append([]float64(nil), preset.Bands[:]...),
	}
}

func NormalizePopupEffect(value any) string {
	switch strings.ToLower(strings.TrimSpace(fmt.Sprint(value))) {
	case "slide":
		return "slide"
	case "disabled", "off":
		return "disabled"
	default:
		return "fade"
	}
}

// Missing or unknown preferences keep the existing speech-text animation.
func NormalizeTextStyle(value any) string {
	if strings.EqualFold(strings.TrimSpace(fmt.Sprint(value)), "matrix") {
		return "matrix"
	}
	return "smooth"
}

// ValidateScreenSettings rejects the whole request before any side effects.
func ValidateScreenSettings(values map[string]any) (map[string]any, error) {
	if len(values) == 0 || len(values) > 32 {
		return nil, fmt.Errorf("invalid screen settings request")
	}
	defaults := ScreenSettingDefaults()
	result := make(map[string]any, len(values))
	for key, value := range values {
		fallback, exists := defaults[key]
		if !exists {
			return nil, fmt.Errorf("setting %q is not available on this screen", key)
		}
		if value == nil {
			return nil, fmt.Errorf("%s must not be null", key)
		}
		switch fallback.(type) {
		case []float64:
			data, err := json.Marshal(value)
			var bands []float64
			if err != nil || json.Unmarshal(data, &bands) != nil || len(bands) != 8 {
				return nil, fmt.Errorf("equalizer requires eight bands")
			}
			for _, band := range bands {
				if math.IsNaN(band) || math.IsInf(band, 0) || band < -12 || band > 12 {
					return nil, fmt.Errorf("equalizer band is out of range")
				}
			}
			value = bands
		case bool:
			if _, ok := value.(bool); !ok {
				return nil, fmt.Errorf("%s must be a boolean", key)
			}
		case int:
			number, err := json.Marshal(value)
			var parsed float64
			if err != nil || json.Unmarshal(number, &parsed) != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed != math.Trunc(parsed) {
				return nil, fmt.Errorf("%s must be an integer", key)
			}
			low, high := 0., 100.
			if key == "screen_auto_brightness_minimum" {
				low, high = 1, 50
			}
			if key == "display_wallpaper_interval_minutes" {
				if parsed != 0 && parsed != 60 && parsed != 360 && parsed != 720 && parsed != 1440 {
					return nil, fmt.Errorf("invalid photo rotation")
				}
			} else if parsed < low || parsed > high {
				return nil, fmt.Errorf("%s is out of range", key)
			}
			value = int(parsed)
		case string:
			text, ok := value.(string)
			if !ok || len(text) > 80 {
				return nil, fmt.Errorf("invalid %s", key)
			}
			allowed := ""
			switch key {
			case "eq_preset":
				allowed = "flat,warm,voice,manual"
			case "display_theme":
				allowed = "tater,ocean,violet,forest,sunset"
			case "screen_popup_effect":
				allowed = "fade,slide,disabled"
			case "screen_text_style":
				allowed = "smooth,matrix"
			case "screen_clock_format":
				allowed = "12h,24h"
			case "screen_night_display":
				allowed = "red_clock,dim,off"
			default:
				parsed, err := time.Parse("15:04", text)
				if err != nil || parsed.Format("15:04") != text {
					return nil, fmt.Errorf("%s must be HH:MM", key)
				}
			}
			if allowed != "" {
				found := false
				for _, option := range strings.Split(allowed, ",") {
					if text == option {
						found = true
						break
					}
				}
				if !found {
					return nil, fmt.Errorf("unsupported %s", key)
				}
			}
		}
		result[key] = value
	}
	outchain.ResolveEQSettings(result)
	return result, nil
}

func CopyScreenSettings(values map[string]any) map[string]any {
	result := map[string]any{}
	for key := range ScreenSettingDefaults() {
		if value, ok := values[key]; ok {
			if key == "eq_bands" {
				data, _ := json.Marshal(value)
				var bands []float64
				if json.Unmarshal(data, &bands) != nil || len(bands) != 8 {
					continue
				}
				value = bands
			}
			result[key] = value
		}
	}
	return result
}

// SaveScreenSettings atomically preserves a small allowlisted file, never
// authentication or model-download configuration.
func SaveScreenSettings(path string, values map[string]any) error {
	data, err := json.Marshal(CopyScreenSettings(values))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".screen-settings-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}

func LoadScreenSettings(path string) map[string]any {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 16*1024+1))
	if err != nil || len(data) > 16*1024 {
		return nil
	}
	var values map[string]any
	if json.Unmarshal(data, &values) != nil {
		return nil
	}
	// Validate independently so stale or now-host-managed preferences from an
	// older screen menu cannot erase the remaining local preferences.
	result := map[string]any{}
	for key, value := range values {
		if accepted, err := ValidateScreenSettings(map[string]any{key: value}); err == nil {
			result[key] = accepted[key]
		}
	}
	// The mixer's existing volume persistence remains authoritative, including
	// physical button changes made after this preferences file was written.
	delete(result, "volume_percent")
	outchain.ResolveEQSettings(result)
	return result
}
