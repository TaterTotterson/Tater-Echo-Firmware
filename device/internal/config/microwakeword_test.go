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
	t.Setenv("OWW_ENABLED", "false")
	t.Setenv("OWW_MODEL", "hey_tater")
	t.Setenv("OWW_THRESHOLD", "0")
	t.Setenv("OWW_PATIENCE", "0")
	t.Setenv("NWW_ENABLED", "false")
	t.Setenv("NWW_MODEL", "")
	t.Setenv("NWW_ARCHITECTURE", "cnn")
	t.Setenv("NWW_THRESHOLD", "0")
	t.Setenv("NWW_PATIENCE", "0")
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
	if initial.OwwEnabled == nil || *initial.OwwEnabled || initial.OwwModel != "hey_tater" {
		t.Fatalf("unexpected openWakeWord defaults: %+v", initial)
	}
	if initial.NwwEnabled == nil || *initial.NwwEnabled || initial.NwwModel != "hey_tater" || initial.NwwArchitecture != "cnn" {
		t.Fatalf("unexpected NanoWakeWord defaults: %+v", initial)
	}

	enabled := true
	threshold := 0.83
	window := 3
	closeMiss := 0.65
	owwEnabled := true
	nwwEnabled := true
	owwThreshold := 0.74
	owwPatience := 2
	nwwThreshold := 0.99
	nwwPatience := 2
	d.Apply(ConfigMessage{
		MwwShadowEnabled: &enabled,
		MwwThreshold:     &threshold,
		MwwSlidingWindow: &window,
		MwwCloseMiss:     &closeMiss,
		MwwModel:         "kitchen_tater",
		MwwSensitivity:   "high",
		MwwEnvironment:   "far_field",
		OwwEnabled:       &owwEnabled,
		OwwModel:         "kitchen_oww",
		OwwThreshold:     &owwThreshold,
		OwwPatience:      &owwPatience,
		NwwEnabled:       &nwwEnabled,
		NwwModel:         "kitchen_nww",
		NwwArchitecture:  "gru",
		NwwThreshold:     &nwwThreshold,
		NwwPatience:      &nwwPatience,
	})
	d.Apply(ConfigMessage{VadThreshold: 0.01})
	got := d.Snapshot()
	if got.MwwShadowEnabled == nil || !*got.MwwShadowEnabled ||
		got.MwwThreshold == nil || *got.MwwThreshold != threshold ||
		got.MwwSlidingWindow == nil || *got.MwwSlidingWindow != window ||
		got.MwwCloseMiss == nil || *got.MwwCloseMiss != closeMiss ||
		got.MwwModel != "kitchen_tater" || got.MwwSensitivity != "high" ||
		got.MwwEnvironment != "far_field" || got.OwwEnabled == nil || !*got.OwwEnabled ||
		got.OwwModel != "kitchen_oww" || got.OwwThreshold == nil || *got.OwwThreshold != owwThreshold ||
		got.OwwPatience == nil || *got.OwwPatience != owwPatience || got.NwwEnabled == nil || !*got.NwwEnabled ||
		got.NwwModel != "kitchen_nww" || got.NwwArchitecture != "gru" ||
		got.NwwThreshold == nil || *got.NwwThreshold != nwwThreshold ||
		got.NwwPatience == nil || *got.NwwPatience != nwwPatience {
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

func TestMicroWakeWordModelURLCanBeSetAndCleared(t *testing.T) {
	d := &Device{}
	customURL := " https://wake.example/computer.json "
	d.Apply(ConfigMessage{MwwModel: "tater_custom_computer", MwwModelURL: &customURL})
	got := d.Snapshot()
	if got.MwwModelURL == nil || *got.MwwModelURL != "https://wake.example/computer.json" {
		t.Fatalf("MwwModelURL = %v", got.MwwModelURL)
	}

	empty := ""
	d.Apply(ConfigMessage{MwwModel: "hey_tater", MwwModelURL: &empty})
	got = d.Snapshot()
	if got.MwwModelURL == nil || *got.MwwModelURL != "" {
		t.Fatalf("cleared MwwModelURL = %v", got.MwwModelURL)
	}
}
