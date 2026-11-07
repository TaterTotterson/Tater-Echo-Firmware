package microwakeword

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	CompanionBundleSuffix         = ".wake-bundle.json"
	CompanionMetadataSuffix       = ".oww.json"
	CompanionONNXModelSuffix      = ".oww.onnx"
	NanoONNXModelSuffix           = ".nww.onnx"
	BoundaryTFLiteModelSuffix     = ".boundary.tflite"
	OWWORTRuntimeFilename         = "libonnxruntime.so"
	OWWMelspectrogramONNXFilename = "melspectrogram.onnx"
	OWWEmbeddingONNXFilename      = "embedding_model.onnx"
	maxCompanionBundleBytes       = 512 * 1024
	maxCompanionMetadataBytes     = 256 * 1024
	maxCompanionModelBytes        = 16 * 1024 * 1024
	maxBoundaryModelBytes         = 200 * 1024
	// MWW has already located the phrase near the end of its continuous wake
	// history. Replaying the full three-second trainer buffer makes bounded
	// OWW confirmation needlessly expensive on the Echo CPUs. Twenty-four
	// 80 ms frames retain 1.92 seconds around the crossing, including the
	// scorer's bounded inference lag, while discarding unrelated room audio.
	maxConfirmationFrames = 24

	// A local wake arriving later than this is worse than a miss: it can open
	// the microphone after the user has already moved on.
	MaximumConfirmationAge = 3 * time.Second
)

var ErrCompanionBusy = errors.New("microwakeword: openWakeWord confirmation already in progress")

func ConfirmationIsStale(capturedAt, now time.Time) bool {
	return capturedAt.IsZero() || now.Sub(capturedAt) > MaximumConfirmationAge
}

var ErrCompanionUnavailable = errors.New("openWakeWord companion is not installed")

type BundleArtifact struct {
	File      string `json:"file"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

type BundleMicroWakeWord struct {
	Manifest       string `json:"manifest"`
	Model          string `json:"model"`
	ManifestSHA256 string `json:"manifest_sha256"`
	ModelSHA256    string `json:"model_sha256"`
}

type BundleOpenWakeWord struct {
	Metadata                         string                    `json:"metadata"`
	MetadataSHA256                   string                    `json:"metadata_sha256"`
	Artifacts                        map[string]BundleArtifact `json:"artifacts"`
	RecommendedThreshold             float32                   `json:"recommended_threshold"`
	RecommendedPatience              int                       `json:"recommended_patience"`
	RecommendedConfirmationThreshold float32                   `json:"recommended_confirmation_threshold,omitempty"`
	RecommendedConfirmationPatience  int                       `json:"recommended_confirmation_patience,omitempty"`
}

type BundleNanoWakeWordVariant struct {
	Type                 string                    `json:"type,omitempty"`
	Architecture         string                    `json:"architecture"`
	Mode                 string                    `json:"mode,omitempty"`
	Model                string                    `json:"model,omitempty"`
	ModelSHA256          string                    `json:"model_sha256,omitempty"`
	ModelSizeBytes       int64                     `json:"model_size_bytes,omitempty"`
	Artifacts            map[string]BundleArtifact `json:"artifacts,omitempty"`
	RecommendedThreshold float32                   `json:"recommended_threshold"`
	RecommendedPatience  int                       `json:"recommended_patience"`
}

type BundleNanoWakeWord struct {
	DefaultArchitecture string                               `json:"default_architecture"`
	Variants            map[string]BundleNanoWakeWordVariant `json:"variants"`
}

type BundleWakeBoundary struct {
	Type             string         `json:"type"`
	Model            string         `json:"model"`
	ModelSHA256      string         `json:"model_sha256"`
	ModelSizeBytes   int64          `json:"model_size_bytes"`
	ModelFormat      string         `json:"model_format"`
	Quantization     string         `json:"quantization"`
	SampleRate       int            `json:"sample_rate"`
	Threshold        float32        `json:"threshold"`
	TensorArenaSize  int            `json:"tensor_arena_size"`
	Frontend         FrontendConfig `json:"frontend"`
	SearchBeforeMS   int            `json:"search_before_ms"`
	SearchAfterMS    int            `json:"search_after_ms"`
	EndpointOffsetMS int            `json:"endpoint_offset_ms"`
}

type WakeBundle struct {
	SchemaVersion int                 `json:"schema_version"`
	Type          string              `json:"type"`
	WakeWord      string              `json:"wake_word"`
	Key           string              `json:"key"`
	MicroWakeWord BundleMicroWakeWord `json:"micro_wake_word"`
	OpenWakeWord  BundleOpenWakeWord  `json:"open_wake_word"`
	NanoWakeWord  BundleNanoWakeWord  `json:"nano_wake_word"`
	WakeBoundary  BundleWakeBoundary  `json:"wake_boundary"`
}

func ParseWakeBundle(raw []byte) (WakeBundle, error) {
	var bundle WakeBundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return WakeBundle{}, fmt.Errorf("microwakeword: decode wake bundle: %w", err)
	}
	if bundle.SchemaVersion < 1 || bundle.SchemaVersion > 4 || bundle.Type != "tater_wake_word_bundle" {
		return WakeBundle{}, fmt.Errorf("microwakeword: unsupported wake bundle schema")
	}
	if strings.TrimSpace(bundle.WakeWord) == "" {
		return WakeBundle{}, fmt.Errorf("microwakeword: bundle wake_word is required")
	}
	// Schema 1 references a separate ESPHome manifest. Schema 2/3 embeds the
	// MWW metadata directly and therefore only names the TFLite model. A bundle
	// may also intentionally contain just one companion engine.
	if bundle.MicroWakeWord.Model != "" {
		if err := validateBareBundleName(bundle.MicroWakeWord.Model, ".tflite"); err != nil {
			return WakeBundle{}, err
		}
		if err := validateSHA256(bundle.MicroWakeWord.ModelSHA256); err != nil {
			return WakeBundle{}, fmt.Errorf("microwakeword: invalid MWW model digest: %w", err)
		}
		if bundle.MicroWakeWord.Manifest != "" {
			if err := validateBareBundleName(bundle.MicroWakeWord.Manifest, ".json"); err != nil {
				return WakeBundle{}, err
			}
			if err := validateSHA256(bundle.MicroWakeWord.ManifestSHA256); err != nil {
				return WakeBundle{}, fmt.Errorf("microwakeword: invalid MWW manifest digest: %w", err)
			}
		}
	}
	oww := bundle.OpenWakeWord
	if oww.RecommendedThreshold != 0 || len(oww.Artifacts) != 0 || oww.Metadata != "" {
		// Metadata is a referenced artifact in schema 1 and embedded in newer
		// bundles, where these fields are intentionally empty.
		if oww.Metadata != "" {
			if err := validateBareBundleName(oww.Metadata, ".json"); err != nil {
				return WakeBundle{}, err
			}
			if err := validateSHA256(oww.MetadataSHA256); err != nil {
				return WakeBundle{}, fmt.Errorf("microwakeword: invalid OWW metadata digest: %w", err)
			}
		}
		if oww.RecommendedThreshold <= 0 || oww.RecommendedThreshold > 1 ||
			oww.RecommendedPatience < 1 || oww.RecommendedPatience > 20 {
			return WakeBundle{}, fmt.Errorf("microwakeword: invalid OWW threshold/patience")
		}
		// Bundles created before dual calibration used one policy for both modes.
		if oww.RecommendedConfirmationThreshold == 0 {
			oww.RecommendedConfirmationThreshold = oww.RecommendedThreshold
		}
		if oww.RecommendedConfirmationPatience == 0 {
			oww.RecommendedConfirmationPatience = oww.RecommendedPatience
		}
		if oww.RecommendedConfirmationThreshold <= 0 || oww.RecommendedConfirmationThreshold > 1 ||
			oww.RecommendedConfirmationPatience < 1 || oww.RecommendedConfirmationPatience > 20 {
			return WakeBundle{}, fmt.Errorf("microwakeword: invalid OWW confirmation threshold/patience")
		}
		bundle.OpenWakeWord = oww
		if _, err := companionONNXArtifact(oww.Artifacts); err != nil {
			return WakeBundle{}, err
		}
	}
	nanoVariants := make(map[string]BundleNanoWakeWordVariant, len(bundle.NanoWakeWord.Variants))
	for architecture, variant := range bundle.NanoWakeWord.Variants {
		normalized := strings.ToLower(strings.TrimSpace(architecture))
		if normalized != "cnn" && normalized != "gru" {
			return WakeBundle{}, fmt.Errorf("microwakeword: unsupported NanoWakeWord architecture %q", architecture)
		}
		if _, exists := nanoVariants[normalized]; exists {
			return WakeBundle{}, fmt.Errorf("microwakeword: duplicate NanoWakeWord architecture %q", architecture)
		}
		variant.Architecture = normalized
		if _, err := nanoONNXArtifact(variant); err != nil {
			return WakeBundle{}, err
		}
		if variant.RecommendedThreshold <= 0 || variant.RecommendedThreshold > 1 ||
			variant.RecommendedPatience < 1 || variant.RecommendedPatience > 20 {
			return WakeBundle{}, fmt.Errorf("microwakeword: invalid NanoWakeWord threshold/patience")
		}
		nanoVariants[normalized] = variant
	}
	bundle.NanoWakeWord.Variants = nanoVariants
	if len(bundle.NanoWakeWord.Variants) > 0 {
		architecture := strings.ToLower(strings.TrimSpace(bundle.NanoWakeWord.DefaultArchitecture))
		if architecture == "" {
			if _, ok := bundle.NanoWakeWord.Variants["cnn"]; ok {
				architecture = "cnn"
			} else {
				architecture = "gru"
			}
		}
		if _, ok := bundle.NanoWakeWord.Variants[architecture]; !ok {
			return WakeBundle{}, fmt.Errorf("microwakeword: NanoWakeWord default architecture is unavailable")
		}
		bundle.NanoWakeWord.DefaultArchitecture = architecture
	}
	boundary := bundle.WakeBoundary
	if boundary.Model != "" || boundary.Type != "" {
		if bundle.SchemaVersion < 4 || boundary.Type != "wake_word_end" {
			return WakeBundle{}, fmt.Errorf("microwakeword: invalid wake boundary type")
		}
		if err := validateBareBundleName(boundary.Model, ".tflite"); err != nil {
			return WakeBundle{}, err
		}
		if err := validateSHA256(boundary.ModelSHA256); err != nil {
			return WakeBundle{}, fmt.Errorf("microwakeword: invalid wake boundary digest: %w", err)
		}
		if boundary.ModelSizeBytes <= 0 || boundary.ModelSizeBytes > maxBoundaryModelBytes {
			return WakeBundle{}, fmt.Errorf("microwakeword: invalid wake boundary model size")
		}
		if boundary.ModelFormat != modelFormat || boundary.Quantization != quantization || boundary.SampleRate != SupportedSampleRate {
			return WakeBundle{}, fmt.Errorf("microwakeword: unsupported wake boundary model contract")
		}
		if boundary.Threshold <= 0 || boundary.Threshold > 1 {
			return WakeBundle{}, fmt.Errorf("microwakeword: invalid wake boundary threshold")
		}
		if boundary.TensorArenaSize < MinimumNativeArenaSize || boundary.TensorArenaSize > 256*1024 {
			return WakeBundle{}, fmt.Errorf("microwakeword: invalid wake boundary tensor arena")
		}
		if boundary.SearchBeforeMS <= 0 || boundary.SearchBeforeMS > 2000 || boundary.SearchAfterMS < 0 || boundary.SearchAfterMS > 500 {
			return WakeBundle{}, fmt.Errorf("microwakeword: invalid wake boundary search window")
		}
		probe := Manifest{
			Type: "micro", WakeWord: bundle.WakeWord, Model: boundary.Model, Version: 1,
			ModelFormat: boundary.ModelFormat, Quantization: boundary.Quantization, SampleRate: boundary.SampleRate,
			Micro:       MicroConfig{ProbabilityCutoff: boundary.Threshold, SlidingWindowSize: 1, FeatureStepSize: SupportedStepMS, TensorArenaSize: boundary.TensorArenaSize},
			TaterNative: TaterNativeConfig{FormatVersion: 1, WakeThreshold: boundary.Threshold, WakeSlidingWindow: 1, Frontend: boundary.Frontend},
		}
		if err := probe.Validate(); err != nil {
			return WakeBundle{}, fmt.Errorf("microwakeword: invalid wake boundary runtime: %w", err)
		}
	}
	return bundle, nil
}

func nanoONNXArtifact(variant BundleNanoWakeWordVariant) (BundleArtifact, error) {
	if strings.TrimSpace(variant.Model) != "" {
		artifact := BundleArtifact{File: variant.Model, SHA256: variant.ModelSHA256, SizeBytes: variant.ModelSizeBytes}
		if err := validateBareBundleName(artifact.File, ".onnx"); err != nil {
			return BundleArtifact{}, err
		}
		if err := validateSHA256(artifact.SHA256); err != nil {
			return BundleArtifact{}, fmt.Errorf("microwakeword: invalid NanoWakeWord model digest: %w", err)
		}
		if artifact.SizeBytes <= 0 || artifact.SizeBytes > maxCompanionModelBytes {
			return BundleArtifact{}, fmt.Errorf("microwakeword: invalid NanoWakeWord model size")
		}
		return artifact, nil
	}
	artifact, err := companionONNXArtifact(variant.Artifacts)
	if err != nil {
		return BundleArtifact{}, fmt.Errorf("microwakeword: NanoWakeWord %w", err)
	}
	return artifact, nil
}

func validateBareBundleName(name, extension string) error {
	if name == "" || name != strings.TrimSpace(name) || filepath.Base(name) != name ||
		strings.ContainsAny(name, `/\\`) || !strings.EqualFold(filepath.Ext(name), extension) {
		return fmt.Errorf("microwakeword: invalid bundle filename %q", name)
	}
	return nil
}

func validateSHA256(value string) error {
	decoded, err := hex.DecodeString(strings.TrimSpace(value))
	if err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("expected a 64-character SHA-256")
	}
	return nil
}

func companionONNXArtifact(artifacts map[string]BundleArtifact) (BundleArtifact, error) {
	artifact, ok := artifacts["onnx"]
	if !ok {
		for _, candidate := range artifacts {
			if !strings.EqualFold(filepath.Ext(candidate.File), ".onnx") {
				continue
			}
			if ok {
				return BundleArtifact{}, fmt.Errorf("microwakeword: wake bundle has ambiguous ONNX classifiers")
			}
			artifact, ok = candidate, true
		}
	}
	if !ok {
		return BundleArtifact{}, fmt.Errorf("microwakeword: wake bundle has no ONNX OWW classifier")
	}
	if err := validateBareBundleName(artifact.File, ".onnx"); err != nil {
		return BundleArtifact{}, err
	}
	if err := validateSHA256(artifact.SHA256); err != nil {
		return BundleArtifact{}, fmt.Errorf("microwakeword: invalid ONNX OWW model digest: %w", err)
	}
	if artifact.SizeBytes <= 0 || artifact.SizeBytes > maxCompanionModelBytes {
		return BundleArtifact{}, fmt.Errorf("microwakeword: invalid ONNX OWW model size")
	}
	return artifact, nil
}

func companionPrefix(packageName string) (string, error) {
	manifest, err := ManifestFilename(packageName)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(manifest, filepath.Ext(manifest)), nil
}

func CompanionBundleFilename(packageName string) (string, error) {
	prefix, err := companionPrefix(packageName)
	if err != nil {
		return "", err
	}
	return prefix + CompanionBundleSuffix, nil
}

// CompanionVerifier owns one independent OWW classifier pipeline. Confirm is
// serialized because the model session and rolling feature buffers are
// deliberately single-threaded.
type CompanionVerifier struct {
	mu       sync.Mutex
	engine   OWWEngine
	settings OWWSettings
	closed   bool
	inFlight atomic.Bool

	statsMu sync.Mutex
	stats   CompanionStats
}

type CompanionStats struct {
	Candidates  uint64  `json:"candidates"`
	Accepted    uint64  `json:"accepted"`
	Rejected    uint64  `json:"rejected"`
	Errors      uint64  `json:"errors"`
	BusyDrops   uint64  `json:"busyDrops"`
	StaleDrops  uint64  `json:"staleDrops"`
	MaxScore    float32 `json:"maxScore"`
	MaxInferMS  int64   `json:"maxInferMs"`
	LastInferMS int64   `json:"lastInferMs"`
	AudioMS     int64   `json:"audioMs"`
	InFlight    bool    `json:"inFlight"`
	LastScore   float32 `json:"lastScore"`
	LastRun     int     `json:"lastConsecutive"`
	LastError   string  `json:"lastError"`
}

func OpenCompanionVerifier(packageName string) (*CompanionVerifier, error) {
	engine, settings, _, err := openCompanionEngine(packageName, false, true)
	if err != nil {
		return nil, err
	}
	return &CompanionVerifier{engine: engine, settings: settings}, nil
}

// OpenCompanionShadow opens one continuous openWakeWord detector. Tater runs
// one detector state against the same uninterrupted audio stream as MWW.
func OpenCompanionShadow(packageName string, hooks ShadowHooks) (*ShadowScorer, WakeBundle, error) {
	return OpenCompanionShadowTuned(packageName, OWWSettings{}, hooks)
}

// OpenCompanionShadowTuned applies optional live threshold/patience values.
// Zero values retain the selected wake bundle's validated policy.
func OpenCompanionShadowTuned(packageName string, overrides OWWSettings, hooks ShadowHooks) (*ShadowScorer, WakeBundle, error) {
	return openCompanionShadow(packageName, false, overrides, hooks)
}

// OpenCompanionAgreementShadow runs OWW continuously while using the
// recall-oriented confirmation calibration. MWW still has to cross within the
// bounded agreement window, so the stricter OWW-only policy is unnecessary
// and would hide valid phrases from the second half of the pair.
func OpenCompanionAgreementShadow(packageName string, hooks ShadowHooks) (*ShadowScorer, WakeBundle, error) {
	return OpenCompanionAgreementShadowTuned(packageName, OWWSettings{}, hooks)
}

// OpenCompanionAgreementShadowTuned is the dual-detector counterpart of
// OpenCompanionShadowTuned.
func OpenCompanionAgreementShadowTuned(packageName string, overrides OWWSettings, hooks ShadowHooks) (*ShadowScorer, WakeBundle, error) {
	return openCompanionShadow(packageName, true, overrides, hooks)
}

func openCompanionShadow(packageName string, confirmationMode bool, overrides OWWSettings, hooks ShadowHooks) (*ShadowScorer, WakeBundle, error) {
	engine, settings, bundle, err := openCompanionEngine(packageName, true, confirmationMode)
	if err != nil {
		return nil, WakeBundle{}, err
	}
	if overrides.Threshold > 0 {
		if overrides.Threshold > 1 {
			_ = engine.Close()
			return nil, WakeBundle{}, fmt.Errorf("microwakeword: OWW threshold override must be in (0, 1]")
		}
		settings.Threshold = overrides.Threshold
	}
	if overrides.Patience > 0 {
		if overrides.Patience > 20 {
			_ = engine.Close()
			return nil, WakeBundle{}, fmt.Errorf("microwakeword: OWW patience override must be between 1 and 20")
		}
		settings.Patience = overrides.Patience
	}
	// The engine also owns these values for bounded callers. Continuous
	// scoring uses the policy immediately below, but keeping both aligned
	// prevents diagnostics and future call paths from disagreeing.
	if concrete, ok := engine.(*ortOWWEngine); ok {
		concrete.settings = settings
	}
	scorer, err := NewShadowScorerWithPolicy(engine, settings.Patience, 0, DetectionPolicy{
		Profile: "openwakeword", Threshold: settings.Threshold,
		PeakThreshold: settings.Threshold, MinimumActiveWindow: settings.Patience,
		MinimumRiseScore: -1, Refractory: DefaultShadowRefractory,
	}, hooks)
	if err != nil {
		_ = engine.Close()
		return nil, WakeBundle{}, err
	}
	return scorer, bundle, nil
}

func openCompanionEngine(packageName string, _ bool, confirmationMode bool) (OWWEngine, OWWSettings, WakeBundle, error) {
	prefix, err := companionPrefix(packageName)
	if err != nil {
		return nil, OWWSettings{}, WakeBundle{}, err
	}
	dir := PackageDir()
	bundleRaw, err := readLimited(filepath.Join(dir, prefix+CompanionBundleSuffix), maxCompanionBundleBytes)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, OWWSettings{}, WakeBundle{}, ErrCompanionUnavailable
		}
		return nil, OWWSettings{}, WakeBundle{}, fmt.Errorf("microwakeword: read companion bundle: %w", err)
	}
	bundle, err := ParseWakeBundle(bundleRaw)
	if err != nil {
		return nil, OWWSettings{}, WakeBundle{}, err
	}
	settings := companionSettings(bundle, confirmationMode)
	if !owwORTSupported() {
		return nil, OWWSettings{}, WakeBundle{}, fmt.Errorf("microwakeword: ONNX Runtime OWW is unavailable in this build")
	}
	classifierArtifact, err := companionONNXArtifact(bundle.OpenWakeWord.Artifacts)
	if err != nil {
		return nil, OWWSettings{}, WakeBundle{}, err
	}
	classifierPath := filepath.Join(dir, classifierArtifact.File)
	if _, err := readVerifiedFile(classifierPath, maxCompanionModelBytes, classifierArtifact.SHA256); err != nil {
		return nil, OWWSettings{}, WakeBundle{}, err
	}
	if bundle.OpenWakeWord.Metadata != "" {
		if _, err := readVerifiedFile(
			filepath.Join(dir, bundle.OpenWakeWord.Metadata),
			maxCompanionMetadataBytes,
			bundle.OpenWakeWord.MetadataSHA256,
		); err != nil {
			return nil, OWWSettings{}, WakeBundle{}, err
		}
	}
	runtimeDigest := expectedORTRuntimeSHA256()
	for _, shared := range []struct {
		name, digest string
	}{
		{OWWMelspectrogramONNXFilename, openWakeWordMelspecONNXSHA},
		{OWWEmbeddingONNXFilename, openWakeWordEmbedONNXSHA},
		{OWWORTRuntimeFilename, runtimeDigest},
	} {
		if validateSHA256(shared.digest) != nil || !fileDigestMatches(filepath.Join(dir, shared.name), shared.digest) {
			return nil, OWWSettings{}, WakeBundle{}, fmt.Errorf(
				"microwakeword: verified ONNX OWW asset %s is not installed", shared.name)
		}
	}
	engine, err := newORTOWWEngine(dir, classifierPath, settings)
	if err != nil {
		return nil, OWWSettings{}, WakeBundle{}, err
	}
	return engine, settings, bundle, nil
}

func companionSettings(bundle WakeBundle, confirmationMode bool) OWWSettings {
	if confirmationMode {
		return OWWSettings{
			Threshold: bundle.OpenWakeWord.RecommendedConfirmationThreshold,
			Patience:  bundle.OpenWakeWord.RecommendedConfirmationPatience,
		}
	}
	return OWWSettings{
		Threshold: bundle.OpenWakeWord.RecommendedThreshold,
		Patience:  bundle.OpenWakeWord.RecommendedPatience,
	}
}

func readVerifiedFile(path string, limit int64, expected string) ([]byte, error) {
	body, err := readLimited(path, limit)
	if err != nil {
		return nil, fmt.Errorf("microwakeword: read companion file %s: %w", path, err)
	}
	digest := sha256.Sum256(body)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), strings.TrimSpace(expected)) {
		return nil, fmt.Errorf("microwakeword: SHA-256 mismatch for %s", filepath.Base(path))
	}
	return body, nil
}

// ReserveCandidate admits at most one bounded confirmation at a time. MWW
// continues scoring while OWW runs, so blocking here would turn repeated wake
// attempts into a stale FIFO whose oldest entries can fire many seconds late.
func (v *CompanionVerifier) ReserveCandidate() bool {
	v.statsMu.Lock()
	v.stats.Candidates++
	v.statsMu.Unlock()
	if v.inFlight.CompareAndSwap(false, true) {
		return true
	}
	v.statsMu.Lock()
	v.stats.BusyDrops++
	v.statsMu.Unlock()
	return false
}

// CancelReservedCandidate releases a reservation when another gate (for
// example the shared two-beam refractory) rejects the candidate before OWW.
func (v *CompanionVerifier) CancelReservedCandidate() {
	v.inFlight.Store(false)
}

// MarkStaleCandidate records a completed confirmation that was deliberately
// not dispatched because its source audio had become too old.
func (v *CompanionVerifier) MarkStaleCandidate() {
	v.statsMu.Lock()
	v.stats.StaleDrops++
	v.statsMu.Unlock()
}

// ConfirmFrames is the safe one-shot API used by tests and non-coordinated
// callers. The native wake coordinator reserves before claiming a beam so a
// busy OWW stage cannot overwrite the winning audio for the active candidate.
func (v *CompanionVerifier) ConfirmFrames(frames [][]byte) (OWWResult, error) {
	if !v.ReserveCandidate() {
		return OWWResult{}, ErrCompanionBusy
	}
	return v.ConfirmReservedFrames(frames)
}

// ConfirmReservedFrames evaluates a candidate admitted by ReserveCandidate.
func (v *CompanionVerifier) ConfirmReservedFrames(frames [][]byte) (OWWResult, error) {
	if !v.inFlight.Load() {
		return OWWResult{}, fmt.Errorf("microwakeword: openWakeWord candidate was not reserved")
	}
	defer v.inFlight.Store(false)
	if len(frames) > maxConfirmationFrames {
		frames = frames[len(frames)-maxConfirmationFrames:]
	}
	var sampleCount int
	for _, frame := range frames {
		sampleCount += len(frame) / 2
	}
	if sampleCount == 0 {
		return OWWResult{}, fmt.Errorf("microwakeword: no wake-stream audio for OWW confirmation")
	}
	samples := make([]int16, 0, sampleCount)
	for _, frame := range frames {
		for offset := 0; offset+1 < len(frame); offset += 2 {
			samples = append(samples, int16(binary.LittleEndian.Uint16(frame[offset:])))
		}
	}
	started := time.Now()
	v.mu.Lock()
	if v.closed {
		v.mu.Unlock()
		return OWWResult{}, fmt.Errorf("microwakeword: OWW companion is closed")
	}
	result, err := v.engine.Confirm(samples)
	v.mu.Unlock()
	elapsed := time.Since(started).Milliseconds()
	v.statsMu.Lock()
	v.stats.LastScore = result.Score
	v.stats.LastRun = result.Consecutive
	v.stats.LastInferMS = elapsed
	v.stats.AudioMS = int64(sampleCount) * 1000 / 16000
	if elapsed > v.stats.MaxInferMS {
		v.stats.MaxInferMS = elapsed
	}
	if result.Score > v.stats.MaxScore {
		v.stats.MaxScore = result.Score
	}
	if err != nil {
		v.stats.Errors++
		v.stats.LastError = err.Error()
	} else if result.Accepted {
		v.stats.Accepted++
	} else {
		v.stats.Rejected++
	}
	v.statsMu.Unlock()
	return result, err
}

func (v *CompanionVerifier) Stats() CompanionStats {
	v.statsMu.Lock()
	defer v.statsMu.Unlock()
	stats := v.stats
	stats.InFlight = v.inFlight.Load()
	return stats
}

func (v *CompanionVerifier) Settings() OWWSettings { return v.settings }
func (v *CompanionVerifier) Info() string          { return v.engine.Info() }

func (v *CompanionVerifier) Close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.closed {
		v.closed = true
		_ = v.engine.Close()
	}
}
