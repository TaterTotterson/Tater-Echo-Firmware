package microwakeword

import (
	"strings"
	"time"
)

const wakeScoreStep = 1.0 / 255.0

// DetectionPolicy is the shared Tater acceptance policy layered over the raw
// microWakeWord probabilities. It mirrors the ESP32 satellites so selecting a
// room profile has the same false-wake/far-field behavior on an Echo.
type DetectionPolicy struct {
	Profile             string
	Threshold           float32
	PeakThreshold       float32
	MinimumRiseScore    float32
	MinimumActiveWindow int
	Refractory          time.Duration
	RequireVerification bool
}

// MakeDetectionPolicy resolves Tater's sensitivity/environment vocabulary.
func MakeDetectionPolicy(sensitivity, environment string, configuredThreshold float32, window int) DetectionPolicy {
	threshold := clampProbability(configuredThreshold)
	sensitivity = strings.ToLower(strings.TrimSpace(sensitivity))
	switch sensitivity {
	case "low", "conservative":
		threshold = max32(threshold, 251*wakeScoreStep)
	case "high", "sensitive":
		threshold = min32(threshold, 242*wakeScoreStep)
	case "very_high", "very_sensitive":
		threshold = min32(threshold, 235*wakeScoreStep)
	}
	if window < 1 {
		window = 1
	}
	policy := DetectionPolicy{
		Profile:             "balanced",
		Threshold:           threshold,
		PeakThreshold:       threshold,
		MinimumRiseScore:    -1,
		MinimumActiveWindow: (window + 1) / 2,
		Refractory:          1200 * time.Millisecond,
	}

	environment = strings.ToLower(strings.TrimSpace(environment))
	switch environment {
	case "far_field", "quiet", "quiet_room", "very_sensitive":
		policy.Profile = "far_field"
		policy.MinimumActiveWindow = 1
		policy.Refractory = 800 * time.Millisecond
	case "strict":
		policy.Profile = "strict"
		policy.Threshold = max32(policy.Threshold, 247*wakeScoreStep)
		policy.PeakThreshold = policy.Threshold
		policy.MinimumRiseScore = -8 * wakeScoreStep
		policy.MinimumActiveWindow = ((window * 2) + 2) / 3
		if policy.MinimumActiveWindow < 2 && window >= 2 {
			policy.MinimumActiveWindow = 2
		}
		policy.Refractory = 1600 * time.Millisecond
	case "tv_nearby", "tv", "near_tv":
		policy.Profile = "tv_nearby"
		ceiling := float32(224 * wakeScoreStep)
		switch sensitivity {
		case "low", "conservative":
			ceiling = 235 * wakeScoreStep
		case "high", "sensitive":
			ceiling = 219 * wakeScoreStep
		case "very_high", "very_sensitive":
			ceiling = 209 * wakeScoreStep
		}
		policy.Threshold = min32(policy.Threshold, ceiling)
		policy.PeakThreshold = max32(policy.Threshold, 235*wakeScoreStep)
		policy.MinimumRiseScore = -8 * wakeScoreStep
		policy.MinimumActiveWindow = (window + 1) / 2
		policy.Refractory = 2400 * time.Millisecond
		policy.RequireVerification = true
	}
	if policy.MinimumActiveWindow > window {
		policy.MinimumActiveWindow = window
	}
	policy.Threshold = clampProbability(policy.Threshold)
	return policy
}

func clampProbability(value float32) float32 {
	if value < 0.01 {
		return 0.01
	}
	if value > 0.99 {
		return 0.99
	}
	return value
}

func min32(left, right float32) float32 {
	if left < right {
		return left
	}
	return right
}

func max32(left, right float32) float32 {
	if left > right {
		return left
	}
	return right
}
