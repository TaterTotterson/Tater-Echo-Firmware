package outchain

// EQPreset changes tone only. Speaker protection and limiter settings remain
// independent because they depend on the speaker hardware.
type EQPreset struct {
	ID, Label string
	Bands     [NumBands]float64
}

const DefaultEQPreset = "warm"

func EQPresets() []EQPreset {
	return []EQPreset{
		{ID: "flat", Label: "Flat"},
		// Community listening curve from firmware issue #13, originally tried
		// on Radar. This is a starting point, not measured speaker calibration.
		{ID: "warm", Label: "Warm", Bands: [NumBands]float64{3, 3, -1, -3, -2, -1, -2, -3}},
		// Reduce bass/mud and gently lift speech without the separate +5 dB
		// Presence boost. All presets start with that optional boost off.
		{ID: "voice", Label: "Voice", Bands: [NumBands]float64{-3, -2, -1, 0, 2, 1, 0, -1}},
	}
}

func FindEQPreset(id string) (EQPreset, bool) {
	for _, preset := range EQPresets() {
		if preset.ID == id {
			return preset, true
		}
	}
	return EQPreset{}, false
}

// ResolveEQSettings accepts validated, canonical EQ values. An explicit named
// selection supplies its curve; explicit slider/boost edits without a name
// become Manual even when they happen to match a preset. Conflicting stored
// metadata must never overwrite a user's actual curve.
func ResolveEQSettings(values map[string]any) {
	id, selected := values["eq_preset"].(string)
	bands, hasBands := values["eq_bands"].([]float64)
	loudness, hasLoudness := values["eq_loudness"].(bool)
	if !selected {
		if hasBands || hasLoudness {
			values["eq_preset"] = "manual"
		}
		return
	}
	preset, named := FindEQPreset(id)
	if !named {
		return
	}
	if hasLoudness && loudness || hasBands && (len(bands) != NumBands || [NumBands]float64(bands) != preset.Bands) {
		values["eq_preset"] = "manual"
		return
	}
	values["eq_bands"] = append([]float64(nil), preset.Bands[:]...)
	values["eq_loudness"] = false
}
