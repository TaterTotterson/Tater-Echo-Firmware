package config

import "testing"

func TestMicGainDefaultsByTarget(t *testing.T) {
	t.Setenv("MIC_GAIN_DB", "")

	tests := []struct {
		target string
		want   int
	}{
		{target: "biscuit", want: 40},
		{target: "radar", want: 40},
		{target: "checkers", want: 28},
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
	d := &Device{firmwareTarget: "biscuit"}
	d.Apply(ConfigMessage{})
	got := d.Snapshot()
	if got.MicGainDb == nil || *got.MicGainDb != 36 {
		t.Fatalf("MicGainDb = %v, want environment override 36", got.MicGainDb)
	}
}
