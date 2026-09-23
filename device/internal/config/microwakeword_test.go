package config

import "testing"

func TestMicroWakeWordConfigDefaultsAndSparseUpdates(t *testing.T) {
	t.Setenv("MWW_SHADOW_ENABLED", "false")
	t.Setenv("MWW_THRESHOLD", "0")
	t.Setenv("MWW_MODEL", "hey_tater")
	t.Setenv("MWW_SLIDING_WINDOW", "0")
	t.Setenv("MWW_CLOSE_MISS_THRESHOLD", "0")
	t.Setenv("MWW_SENSITIVITY", "normal")
	t.Setenv("MWW_ENVIRONMENT", "balanced")
	d := &Device{}
	d.Apply(ConfigMessage{})
	initial := d.Snapshot()
	if initial.MwwShadowEnabled == nil || *initial.MwwShadowEnabled {
		t.Fatalf("default mwwShadowEnabled = %v, want false", initial.MwwShadowEnabled)
	}
	if initial.MwwThreshold == nil || *initial.MwwThreshold != 0 || initial.MwwModel != "hey_tater" ||
		initial.MwwSensitivity != "normal" || initial.MwwEnvironment != "balanced" {
		t.Fatalf("unexpected microWakeWord defaults: %+v", initial)
	}

	enabled := true
	threshold := 0.83
	window := 3
	closeMiss := 0.65
	d.Apply(ConfigMessage{
		MwwShadowEnabled: &enabled,
		MwwThreshold:     &threshold,
		MwwSlidingWindow: &window,
		MwwCloseMiss:     &closeMiss,
		MwwModel:         "kitchen_tater",
		MwwSensitivity:   "high",
		MwwEnvironment:   "far_field",
	})
	d.Apply(ConfigMessage{VadThreshold: 0.01})
	got := d.Snapshot()
	if got.MwwShadowEnabled == nil || !*got.MwwShadowEnabled ||
		got.MwwThreshold == nil || *got.MwwThreshold != threshold ||
		got.MwwSlidingWindow == nil || *got.MwwSlidingWindow != window ||
		got.MwwCloseMiss == nil || *got.MwwCloseMiss != closeMiss ||
		got.MwwModel != "kitchen_tater" || got.MwwSensitivity != "high" ||
		got.MwwEnvironment != "far_field" {
		t.Fatalf("sparse update lost microWakeWord config: %+v", got)
	}

	// Zero is an intentional value: it restores the model manifest's
	// calibrated threshold, so this field must not use non-zero semantics.
	manifestThreshold := float64(0)
	d.Apply(ConfigMessage{MwwThreshold: &manifestThreshold})
	if got := d.Snapshot(); got.MwwThreshold == nil || *got.MwwThreshold != 0 {
		t.Fatalf("could not restore manifest threshold: %+v", got)
	}
}
