package config

import "testing"

func TestAecPolicyIsOwnedByFirmware(t *testing.T) {
	t.Setenv("AEC_ENABLED", "true")
	t.Setenv("AEC_DELAY_MS", "37")

	d := &Device{}
	d.Apply(ConfigMessage{})
	initial := d.Snapshot()
	if initial.AecEnabled == nil || !*initial.AecEnabled {
		t.Fatalf("firmware default AEC enabled = %v, want true", initial.AecEnabled)
	}
	if initial.AecDelayMs == nil || *initial.AecDelayMs != 37 {
		t.Fatalf("firmware default AEC delay = %v, want 37", initial.AecDelayMs)
	}

	disabled := false
	legacyDelay := 211
	d.Apply(ConfigMessage{
		AecEnabled: &disabled,
		AecDelayMs: &legacyDelay,
	})

	got := d.Snapshot()
	if got.AecEnabled == nil || !*got.AecEnabled {
		t.Fatalf("legacy controller disabled firmware AEC: %v", got.AecEnabled)
	}
	if got.AecDelayMs == nil || *got.AecDelayMs != 37 {
		t.Fatalf("legacy controller changed firmware AEC delay: %v", got.AecDelayMs)
	}
}
