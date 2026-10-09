package main

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/config"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/outchain"
)

// nativeOutputSetting accepts Tater's native snake_case vocabulary and the
// camelCase names used by the older Echo configuration protocol. Native Tater
// is authoritative when both happen to be present.
func nativeOutputSetting(values map[string]any, nativeKey, legacyKey string) (any, bool) {
	if value, ok := values[nativeKey]; ok {
		return value, true
	}
	value, ok := values[legacyKey]
	return value, ok
}

func nativeOutputNumber(value any, key string, minimum, maximum float64) (float64, error) {
	var number float64
	switch typed := value.(type) {
	case float64:
		number = typed
	case float32:
		number = float64(typed)
	case int:
		number = float64(typed)
	case int8:
		number = float64(typed)
	case int16:
		number = float64(typed)
	case int32:
		number = float64(typed)
	case int64:
		number = float64(typed)
	case uint:
		number = float64(typed)
	case uint8:
		number = float64(typed)
	case uint16:
		number = float64(typed)
	case uint32:
		number = float64(typed)
	case uint64:
		number = float64(typed)
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, fmt.Errorf("%s must be a number", key)
		}
		number = parsed
	default:
		return 0, fmt.Errorf("%s must be a number", key)
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, fmt.Errorf("%s must be a number", key)
	}
	if number < minimum || number > maximum {
		return 0, fmt.Errorf("%s must be between %g and %g", key, minimum, maximum)
	}
	return number, nil
}

func nativeOutputBool(value any, key string) (bool, error) {
	enabled, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be true or false", key)
	}
	return enabled, nil
}

func nativeOutputBands(value any) ([]float64, error) {
	var raw []any
	switch bands := value.(type) {
	case []any:
		raw = bands
	case []float64:
		raw = make([]any, len(bands))
		for index, band := range bands {
			raw[index] = band
		}
	default:
		return nil, fmt.Errorf("eq_bands must be an array of %d numbers", outchain.NumBands)
	}
	if len(raw) != outchain.NumBands {
		return nil, fmt.Errorf("eq_bands must contain exactly %d bands", outchain.NumBands)
	}
	result := make([]float64, outchain.NumBands)
	for index, value := range raw {
		band, err := nativeOutputNumber(value, fmt.Sprintf("eq_bands[%d]", index), -12, 12)
		if err != nil {
			return nil, err
		}
		result[index] = band
	}
	return result, nil
}

// applyNativeOutputSettings validates the device-owned speaker chain and maps
// it into the existing config package. Applied values are reported back in
// Tater's snake_case vocabulary even when a legacy camelCase key arrived.
func applyNativeOutputSettings(values, applied map[string]any, msg *config.ConfigMessage) error {
	if value, ok := nativeOutputSetting(values, "eq_bands", "eqBands"); ok {
		bands, err := nativeOutputBands(value)
		if err != nil {
			return err
		}
		msg.EqBands = bands
		delete(applied, "eqBands")
		applied["eq_bands"] = bands
	}
	if value, ok := nativeOutputSetting(values, "eq_loudness", "eqLoudness"); ok {
		enabled, err := nativeOutputBool(value, "eq_loudness")
		if err != nil {
			return err
		}
		msg.EqLoudness = &enabled
		delete(applied, "eqLoudness")
		applied["eq_loudness"] = enabled
	}
	if value, ok := nativeOutputSetting(values, "bass_guard_enabled", "bassGuardEnabled"); ok {
		enabled, err := nativeOutputBool(value, "bass_guard_enabled")
		if err != nil {
			return err
		}
		msg.BassGuardEnabled = &enabled
		delete(applied, "bassGuardEnabled")
		applied["bass_guard_enabled"] = enabled
	}
	if value, ok := nativeOutputSetting(values, "bass_guard_db", "bassGuardDb"); ok {
		level, err := nativeOutputNumber(value, "bass_guard_db", -60, 0)
		if err != nil {
			return err
		}
		msg.BassGuardDb = &level
		delete(applied, "bassGuardDb")
		applied["bass_guard_db"] = level
	}
	if value, ok := nativeOutputSetting(values, "limiter_enabled", "limiterEnabled"); ok {
		enabled, err := nativeOutputBool(value, "limiter_enabled")
		if err != nil {
			return err
		}
		msg.LimiterEnabled = &enabled
		delete(applied, "limiterEnabled")
		applied["limiter_enabled"] = enabled
	}
	if value, ok := nativeOutputSetting(values, "limiter_threshold_db", "limiterThreshold"); ok {
		level, err := nativeOutputNumber(value, "limiter_threshold_db", -12, 0)
		if err != nil {
			return err
		}
		msg.LimiterThreshold = &level
		delete(applied, "limiterThreshold")
		applied["limiter_threshold_db"] = level
	}
	if value, ok := nativeOutputSetting(values, "limiter_release_ms", "limiterRelease"); ok {
		release, err := nativeOutputNumber(value, "limiter_release_ms", 20, 1000)
		if err != nil {
			return err
		}
		msg.LimiterRelease = &release
		delete(applied, "limiterRelease")
		applied["limiter_release_ms"] = release
	}
	return nil
}
