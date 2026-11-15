package codec

import (
	"testing"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/mixer"
)

func TestHardwareFilterRequiresCompleteVerifiedLocalDriverProfile(t *testing.T) {
	for _, values := range [][]int{
		nil, {1, 0, 0, 0}, {1, 80, 127, 16000}, {1, 80, 255, 48000},
		{2, 80, 255, 16000}, {1, 100, 255, 16000}, {1, 80, 255}, {1, 80, 255, 16000, 1},
		{1, 80, 255, 16000},
	} {
		f := useCaptureGainFake(t)
		f.values["Tater ADC HPF Status"] = values
		want := len(values) == 4 && values[0] == 1 && values[1] == 80 && values[2] == 255 && values[3] == 16000
		for _, target := range []string{"biscuit", "radar"} {
			if got := CaptureHighPassOffloaded(target); got != want {
				t.Fatalf("%s values %v accepted=%v, want %v", target, values, got, want)
			}
		}
		for _, target := range []string{"checkers", "cronos", "crown", "athena", "rook", ""} {
			if CaptureHighPassOffloaded(target) {
				t.Fatalf("%s accepted a Puffin-only filter", target)
			}
		}
	}
	// Old backends expose no stereo-array read; do not fabricate success.
	mixer.Use(&codecMixerFake{})
	if CaptureHighPassOffloaded("biscuit") {
		t.Fatal("old backend disabled the software filter")
	}
}

func TestMissingFilterControlKeepsSoftwareFallback(t *testing.T) {
	useCaptureGainFake(t)
	if CaptureHighPassOffloaded("radar") {
		t.Fatal("missing kernel control was treated as active hardware filtering")
	}
}
