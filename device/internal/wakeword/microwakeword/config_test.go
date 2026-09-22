package microwakeword

import (
	"path/filepath"
	"strings"
	"testing"
)

const validManifest = `{
  "type": "micro",
  "wake_word": "hey_tater",
  "label": "Hey Tater",
  "author": "Tater Totterson",
  "model": "hey_tater.tflite",
  "trained_languages": ["en"],
  "version": 2,
  "model_format": "tflite_stream_state_internal_quant",
  "quantization": "int8",
  "sample_rate": 16000,
  "micro": {
    "probability_cutoff": 0.98,
    "sliding_window_size": 5,
    "feature_step_size": 10,
    "tensor_arena_size": 30000
  },
  "tater_native": {
    "format_version": 1,
    "wake_threshold": 0.97,
    "wake_sliding_window": 4,
    "close_miss_threshold": 0.81,
    "frontend": {
      "name": "tflm_microfrontend",
      "sample_rate": 16000,
      "feature_duration_ms": 30,
      "feature_step_ms": 10,
      "feature_size": 40,
      "input_feature_frames": 2,
      "lower_band_limit": 125.0,
      "upper_band_limit": 7500.0
    }
  },
  "calibration": {"false_accepts_per_hour": 0.0}
}`

func mustManifest(t *testing.T) Manifest {
	t.Helper()
	m, err := ParseManifest([]byte(validManifest))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	return m
}

func TestParseManifestResolvesTaterSettings(t *testing.T) {
	m := mustManifest(t)
	got := m.RuntimeConfig()
	if got.WakeWord != "hey_tater" || got.Label != "Hey Tater" {
		t.Fatalf("unexpected identity: %+v", got)
	}
	if got.Threshold != 0.97 || got.SlidingWindow != 4 || got.CloseMissThreshold != 0.81 {
		t.Fatalf("Tater overrides not resolved: %+v", got)
	}
	if got.TensorArenaSize != 30000 {
		t.Fatalf("TensorArenaSize = %d, want 30000", got.TensorArenaSize)
	}
	if got.Frontend.FeatureSize != 40 || got.Frontend.InputFeatureFrames != 2 {
		t.Fatalf("unexpected frontend: %+v", got.Frontend)
	}

	dir := t.TempDir()
	path, err := m.ResolveModelPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "hey_tater.tflite"); path != want {
		t.Fatalf("model path = %q, want %q", path, want)
	}
}

func TestRuntimeConfigDefaultsForPortableManifest(t *testing.T) {
	m := mustManifest(t)
	m.Label = ""
	m.TaterNative = TaterNativeConfig{}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	got := m.RuntimeConfig()
	if got.Label != m.WakeWord || got.Threshold != 0.98 || got.SlidingWindow != 5 {
		t.Fatalf("portable settings not resolved: %+v", got)
	}
	f := got.Frontend
	if f.Name != "tflm_microfrontend" || f.SampleRate != 16000 ||
		f.FeatureDurationMS != 30 || f.FeatureStepMS != 10 ||
		f.FeatureSize != 40 || f.InputFeatureFrames != 2 ||
		f.LowerBandLimit != 125 || f.UpperBandLimit != 7500 {
		t.Fatalf("unexpected default frontend: %+v", f)
	}
}

func TestManifestValidationRejectsUnsupportedPackages(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Manifest)
		want string
	}{
		{"wrong type", func(m *Manifest) { m.Type = "openwakeword" }, "type must"},
		{"empty wake word", func(m *Manifest) { m.WakeWord = " " }, "wake_word"},
		{"path traversal", func(m *Manifest) { m.Model = "../hey.tflite" }, "bare filename"},
		{"windows path", func(m *Manifest) { m.Model = `dir\\hey.tflite` }, "bare filename"},
		{"wrong extension", func(m *Manifest) { m.Model = "hey.onnx" }, ".tflite"},
		{"wrong sample rate", func(m *Manifest) { m.SampleRate = 8000 }, "sample_rate"},
		{"wrong format", func(m *Manifest) { m.ModelFormat = "tflite" }, "model_format"},
		{"wrong quantization", func(m *Manifest) { m.Quantization = "float32" }, "quantization"},
		{"zero cutoff", func(m *Manifest) { m.Micro.ProbabilityCutoff = 0 }, "probability_cutoff"},
		{"zero window", func(m *Manifest) { m.Micro.SlidingWindowSize = 0 }, "sliding_window_size"},
		{"wrong feature step", func(m *Manifest) { m.Micro.FeatureStepSize = 20 }, "feature_step_size"},
		{"small arena", func(m *Manifest) { m.Micro.TensorArenaSize = 100 }, "tensor_arena_size"},
		{"future native format", func(m *Manifest) { m.TaterNative.FormatVersion = 2 }, "format_version"},
		{"missing native format", func(m *Manifest) { m.TaterNative.FormatVersion = 0 }, "format_version is required"},
		{"close miss above wake", func(m *Manifest) { m.TaterNative.CloseMissThreshold = 0.99 }, "exceeds wake threshold"},
		{"wrong frontend", func(m *Manifest) { m.TaterNative.Frontend.Name = "mfcc" }, "frontend name"},
		{"wrong frontend rate", func(m *Manifest) { m.TaterNative.Frontend.SampleRate = 8000 }, "frontend sample_rate"},
		{"wrong frontend duration", func(m *Manifest) { m.TaterNative.Frontend.FeatureDurationMS = 20 }, "feature_duration_ms"},
		{"wrong frontend step", func(m *Manifest) { m.TaterNative.Frontend.FeatureStepMS = 20 }, "frontend feature_step_ms"},
		{"wrong feature size", func(m *Manifest) { m.TaterNative.Frontend.FeatureSize = 32 }, "feature_size"},
		{"wrong input frames", func(m *Manifest) { m.TaterNative.Frontend.InputFeatureFrames = 1 }, "input_feature_frames"},
		{"bad bands", func(m *Manifest) { m.TaterNative.Frontend.LowerBandLimit = 7600 }, "band limits"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := mustManifest(t)
			tc.edit(&m)
			err := m.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestParseManifestRejectsMultipleJSONValues(t *testing.T) {
	_, err := ParseManifest([]byte(validManifest + `{}`))
	if err == nil || !strings.Contains(err.Error(), "multiple JSON values") {
		t.Fatalf("ParseManifest error = %v", err)
	}
}
