// Package micgain keeps hardware, sample and acoustic-level gain in agreement.
package micgain

import (
	"math"
	"strings"
)

// HardwareDB is the gain relocated from software into the microphone PGA.
// It is NOT the absolute PGA setting of every board: the Shows' existing
// analog calibration is unchanged and must not be counted a second time.
func HardwareDB(target string) int {
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "biscuit", "radar":
		return 40
	default:
		return 0
	}
}

// SoftwareDB prevents a legacy environment/config push from stacking the old
// +40 dB digital boost on top of the new +40 dB analog gain.
func SoftwareDB(target string, requested int) int {
	if HardwareDB(target) != 0 {
		return 0
	}
	return requested
}

// Factors returns the multiplier applied to microphone PCM and the separate
// total-gain calibration for DOA/VAD decisions. Never apply the second factor
// to PCM or to the synchronized playback reference.
func Factors(target string, softwareDB int) (samples, levels float64) {
	softwareDB = SoftwareDB(target, softwareDB)
	samples = math.Pow(10, float64(softwareDB)/20)
	levels = math.Pow(10, float64(softwareDB+HardwareDB(target))/20)
	return samples, levels
}
