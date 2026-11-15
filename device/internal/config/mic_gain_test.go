package config

import "testing"

func TestMicGainDefaultsByTarget(t *testing.T) {
	t.Setenv("MIC_GAIN_DB", "")

	tests := []struct {
		target string
		want   int
	}{
		{target: "biscuit", want: 0},
		{target: "radar", want: 0},
		{target: "checkers", want: 28},
		{target: "cronos", want: 28},
		{target: "crown", want: 28},
		{target: "athena", want: 28},
		{target: "rook", want: 28},
		{target: "", want: 28},
	}

	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			d := &Device{firmwareTarget: tt.target}
			d.Apply(ConfigMessage{})
			got := d.Snapshot()
			if got.MicGainDb == nil || *got.MicGainDb != tt.want {
				t.Fatalf("MicGainDb = %v, want %d", got.MicGainDb, tt.want)
			}
		})
	}
}

func TestMicGainEnvironmentOverrideWins(t *testing.T) {
	t.Setenv("MIC_GAIN_DB", "36")
	d := &Device{firmwareTarget: "checkers"}
	d.Apply(ConfigMessage{})
	got := d.Snapshot()
	if got.MicGainDb == nil || *got.MicGainDb != 36 {
		t.Fatalf("MicGainDb = %v, want environment override 36", got.MicGainDb)
	}
}

func TestHardwareGainTargetsRejectStackedSoftwareGain(t *testing.T) {
	for _, target := range []string{"biscuit", "radar", " RADAR "} {
		t.Run(target, func(t *testing.T) {
			t.Setenv("MIC_GAIN_DB", "40")
			d := &Device{firmwareTarget: target}
			d.Apply(ConfigMessage{})
			if got := d.Snapshot(); got.MicGainDb == nil || *got.MicGainDb != 0 {
				t.Fatalf("legacy environment stacked software gain: %v", got.MicGainDb)
			}
			for _, db := range []int{0, 28, 40, 42} {
				d.Apply(ConfigMessage{MicGainDb: &db})
				if got := d.Snapshot(); got.MicGainDb == nil || *got.MicGainDb != 0 {
					t.Fatalf("legacy settings stacked software gain: %v", got.MicGainDb)
				}
			}
		})
	}
}
