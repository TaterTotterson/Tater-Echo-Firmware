// Package microwakeword defines the Tater microWakeWord package contract and
// the boundary to the native TFLite Micro runtime.
package microwakeword

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

const (
	SupportedFormatVersion = 1
	SupportedSampleRate    = 16000
	SupportedFeatureMS     = 30
	SupportedStepMS        = 10
	SupportedFeatureSize   = 40
	SupportedInputFrames   = 2
	DefaultLowerBandLimit  = 125.0
	DefaultUpperBandLimit  = 7500.0

	modelFormat  = "tflite_stream_state_internal_quant"
	quantization = "int8"
)

// Manifest is the JSON file shipped beside a Tater microWakeWord .tflite
// model. Calibration and other producer metadata are intentionally ignored:
// consumers must tolerate packages gaining descriptive fields.
type Manifest struct {
	Type             string            `json:"type"`
	WakeWord         string            `json:"wake_word"`
	Label            string            `json:"label"`
	Author           string            `json:"author"`
	Website          string            `json:"website"`
	Model            string            `json:"model"`
	TrainedLanguages []string          `json:"trained_languages"`
	Version          int               `json:"version"`
	ModelFormat      string            `json:"model_format"`
	Quantization     string            `json:"quantization"`
	SampleRate       int               `json:"sample_rate"`
	Micro            MicroConfig       `json:"micro"`
	TaterNative      TaterNativeConfig `json:"tater_native"`
}

// MicroConfig contains the portable microWakeWord detector settings.
type MicroConfig struct {
	ProbabilityCutoff     float32 `json:"probability_cutoff"`
	SlidingWindowSize     int     `json:"sliding_window_size"`
	FeatureStepSize       int     `json:"feature_step_size"`
	TensorArenaSize       int     `json:"tensor_arena_size"`
	MinimumESPHomeVersion string  `json:"minimum_esphome_version"`
}

// TaterNativeConfig carries Tater-specific tuning while preserving the
// portable microWakeWord fields above. A zero-value block is allowed for
// standard ESPHome-compatible packages and resolves to the supported defaults.
type TaterNativeConfig struct {
	FormatVersion      int            `json:"format_version"`
	WakeThreshold      float32        `json:"wake_threshold"`
	WakeSlidingWindow  int            `json:"wake_sliding_window"`
	CloseMissThreshold float32        `json:"close_miss_threshold"`
	Frontend           FrontendConfig `json:"frontend"`
}

// FrontendConfig describes the exact audio-to-feature transform expected by
// the model. Zero fields resolve to the Tater defaults in RuntimeConfig.
type FrontendConfig struct {
	Name               string  `json:"name"`
	SampleRate         int     `json:"sample_rate"`
	FeatureDurationMS  int     `json:"feature_duration_ms"`
	FeatureStepMS      int     `json:"feature_step_ms"`
	FeatureSize        int     `json:"feature_size"`
	InputFeatureFrames int     `json:"input_feature_frames"`
	LowerBandLimit     float32 `json:"lower_band_limit"`
	UpperBandLimit     float32 `json:"upper_band_limit"`
}

// RuntimeSettings is the fully-resolved configuration consumed by the native
// engine. It contains no optional values.
type RuntimeSettings struct {
	WakeWord           string
	Label              string
	Threshold          float32
	SlidingWindow      int
	CloseMissThreshold float32
	TensorArenaSize    int
	Frontend           FrontendConfig
}

// ParseManifest decodes and validates exactly one JSON manifest.
func ParseManifest(raw []byte) (Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("microwakeword: decode manifest: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Manifest{}, fmt.Errorf("microwakeword: manifest contains multiple JSON values")
		}
		return Manifest{}, fmt.Errorf("microwakeword: trailing manifest data: %w", err)
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// Validate rejects packages the initial Tater runtime cannot execute safely.
// Validation stays strict around the tensor/audio contract so an incompatible
// model fails at install time rather than silently never detecting a wake.
func (m Manifest) Validate() error {
	if m.Type != "micro" {
		return fmt.Errorf("microwakeword: type must be %q, got %q", "micro", m.Type)
	}
	if strings.TrimSpace(m.WakeWord) == "" {
		return fmt.Errorf("microwakeword: wake_word is required")
	}
	if err := validateModelName(m.Model); err != nil {
		return err
	}
	if m.Version < 1 {
		return fmt.Errorf("microwakeword: version must be at least 1, got %d", m.Version)
	}
	if m.ModelFormat != modelFormat {
		return fmt.Errorf("microwakeword: unsupported model_format %q", m.ModelFormat)
	}
	if m.Quantization != quantization {
		return fmt.Errorf("microwakeword: unsupported quantization %q", m.Quantization)
	}
	if m.SampleRate != SupportedSampleRate {
		return fmt.Errorf("microwakeword: sample_rate must be %d, got %d", SupportedSampleRate, m.SampleRate)
	}
	if err := probability("micro.probability_cutoff", m.Micro.ProbabilityCutoff, false); err != nil {
		return err
	}
	if err := window("micro.sliding_window_size", m.Micro.SlidingWindowSize, false); err != nil {
		return err
	}
	if m.Micro.FeatureStepSize != SupportedStepMS {
		return fmt.Errorf("microwakeword: micro.feature_step_size must be %d, got %d", SupportedStepMS, m.Micro.FeatureStepSize)
	}
	if m.Micro.TensorArenaSize < 1024 || m.Micro.TensorArenaSize > 2*1024*1024 {
		return fmt.Errorf("microwakeword: micro.tensor_arena_size must be between 1024 and 2097152, got %d", m.Micro.TensorArenaSize)
	}

	tn := m.TaterNative
	if tn.FormatVersion < 0 || tn.FormatVersion > SupportedFormatVersion {
		return fmt.Errorf("microwakeword: unsupported tater_native.format_version %d", tn.FormatVersion)
	}
	hasNativeSettings := tn.WakeThreshold != 0 || tn.WakeSlidingWindow != 0 ||
		tn.CloseMissThreshold != 0 || tn.Frontend != (FrontendConfig{})
	if hasNativeSettings && tn.FormatVersion == 0 {
		return fmt.Errorf("microwakeword: tater_native.format_version is required when native settings are present")
	}
	if err := probability("tater_native.wake_threshold", tn.WakeThreshold, true); err != nil {
		return err
	}
	if err := probability("tater_native.close_miss_threshold", tn.CloseMissThreshold, true); err != nil {
		return err
	}
	if err := window("tater_native.wake_sliding_window", tn.WakeSlidingWindow, true); err != nil {
		return err
	}
	effectiveThreshold := tn.WakeThreshold
	if effectiveThreshold == 0 {
		effectiveThreshold = m.Micro.ProbabilityCutoff
	}
	if tn.CloseMissThreshold > effectiveThreshold {
		return fmt.Errorf("microwakeword: close-miss threshold %g exceeds wake threshold %g", tn.CloseMissThreshold, effectiveThreshold)
	}

	f := resolvedFrontend(tn.Frontend)
	if f.Name != "tflm_microfrontend" {
		return fmt.Errorf("microwakeword: unsupported frontend name %q", f.Name)
	}
	if f.SampleRate != SupportedSampleRate {
		return fmt.Errorf("microwakeword: frontend sample_rate must be %d, got %d", SupportedSampleRate, f.SampleRate)
	}
	if f.FeatureDurationMS != SupportedFeatureMS {
		return fmt.Errorf("microwakeword: frontend feature_duration_ms must be %d, got %d", SupportedFeatureMS, f.FeatureDurationMS)
	}
	if f.FeatureStepMS != SupportedStepMS {
		return fmt.Errorf("microwakeword: frontend feature_step_ms must be %d, got %d", SupportedStepMS, f.FeatureStepMS)
	}
	if f.FeatureSize != SupportedFeatureSize {
		return fmt.Errorf("microwakeword: frontend feature_size must be %d, got %d", SupportedFeatureSize, f.FeatureSize)
	}
	if f.InputFeatureFrames != SupportedInputFrames {
		return fmt.Errorf("microwakeword: frontend input_feature_frames must be %d, got %d", SupportedInputFrames, f.InputFeatureFrames)
	}
	if f.LowerBandLimit <= 0 || f.UpperBandLimit <= f.LowerBandLimit || f.UpperBandLimit > SupportedSampleRate/2 {
		return fmt.Errorf("microwakeword: invalid frontend band limits %.1f..%.1f", f.LowerBandLimit, f.UpperBandLimit)
	}
	return nil
}

// RuntimeConfig resolves optional Tater tuning to a complete native-engine
// configuration. Call Validate first; ParseManifest already does so.
func (m Manifest) RuntimeConfig() RuntimeSettings {
	threshold := m.TaterNative.WakeThreshold
	if threshold == 0 {
		threshold = m.Micro.ProbabilityCutoff
	}
	sliding := m.TaterNative.WakeSlidingWindow
	if sliding == 0 {
		sliding = m.Micro.SlidingWindowSize
	}
	label := strings.TrimSpace(m.Label)
	if label == "" {
		label = m.WakeWord
	}
	return RuntimeSettings{
		WakeWord:           m.WakeWord,
		Label:              label,
		Threshold:          threshold,
		SlidingWindow:      sliding,
		CloseMissThreshold: m.TaterNative.CloseMissThreshold,
		TensorArenaSize:    m.Micro.TensorArenaSize,
		Frontend:           resolvedFrontend(m.TaterNative.Frontend),
	}
}

// ResolveModelPath returns the absolute model path inside packageDir. The
// manifest model must be a bare filename, so it can never escape that root.
func (m Manifest) ResolveModelPath(packageDir string) (string, error) {
	if err := m.Validate(); err != nil {
		return "", err
	}
	if strings.TrimSpace(packageDir) == "" {
		return "", fmt.Errorf("microwakeword: package directory is required")
	}
	root, err := filepath.Abs(packageDir)
	if err != nil {
		return "", fmt.Errorf("microwakeword: resolve package directory: %w", err)
	}
	return filepath.Join(root, m.Model), nil
}

func validateModelName(name string) error {
	if name == "" || name != strings.TrimSpace(name) {
		return fmt.Errorf("microwakeword: model must be a non-empty bare filename")
	}
	if filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) {
		return fmt.Errorf("microwakeword: model must be a bare filename, got %q", name)
	}
	if !strings.EqualFold(filepath.Ext(name), ".tflite") {
		return fmt.Errorf("microwakeword: model must end in .tflite, got %q", name)
	}
	return nil
}

func probability(name string, value float32, optional bool) error {
	if optional && value == 0 {
		return nil
	}
	if value <= 0 || value > 1 {
		return fmt.Errorf("microwakeword: %s must be in (0, 1], got %g", name, value)
	}
	return nil
}

func window(name string, value int, optional bool) error {
	if optional && value == 0 {
		return nil
	}
	if value < 1 || value > 100 {
		return fmt.Errorf("microwakeword: %s must be between 1 and 100, got %d", name, value)
	}
	return nil
}

func resolvedFrontend(f FrontendConfig) FrontendConfig {
	if f.Name == "" {
		f.Name = "tflm_microfrontend"
	}
	if f.SampleRate == 0 {
		f.SampleRate = SupportedSampleRate
	}
	if f.FeatureDurationMS == 0 {
		f.FeatureDurationMS = SupportedFeatureMS
	}
	if f.FeatureStepMS == 0 {
		f.FeatureStepMS = SupportedStepMS
	}
	if f.FeatureSize == 0 {
		f.FeatureSize = SupportedFeatureSize
	}
	if f.InputFeatureFrames == 0 {
		f.InputFeatureFrames = SupportedInputFrames
	}
	if f.LowerBandLimit == 0 {
		f.LowerBandLimit = DefaultLowerBandLimit
	}
	if f.UpperBandLimit == 0 {
		f.UpperBandLimit = DefaultUpperBandLimit
	}
	return f
}
