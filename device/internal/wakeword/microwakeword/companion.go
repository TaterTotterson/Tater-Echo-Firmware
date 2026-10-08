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
	OWWORTRuntimeFilename         = "libonnxruntime.so"
	OWWMelspectrogramONNXFilename = "melspectrogram.onnx"
	OWWEmbeddingONNXFilename      = "embedding_model.onnx"
	maxCompanionBundleBytes       = 128 * 1024
	maxCompanionMetadataBytes     = 256 * 1024
	maxCompanionModelBytes        = 16 * 1024 * 1024
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

type WakeBundle struct {
	SchemaVersion int                 `json:"schema_version"`
	Type          string              `json:"type"`
	WakeWord      string              `json:"wake_word"`
	Key           string              `json:"key"`
	MicroWakeWord BundleMicroWakeWord `json:"micro_wake_word"`
	OpenWakeWord  BundleOpenWakeWord  `json:"open_wake_word"`
}

func ParseWakeBundle(raw []byte) (WakeBundle, error) {
	var bundle WakeBundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return WakeBundle{}, fmt.Errorf("microwakeword: decode wake bundle: %w", err)
	}
	if bundle.SchemaVersion != 1 || bundle.Type != "tater_wake_word_bundle" {
		return WakeBundle{}, fmt.Errorf("microwakeword: unsupported wake bundle schema")
	}
	if strings.TrimSpace(bundle.WakeWord) == "" {
		return WakeBundle{}, fmt.Errorf("microwakeword: bundle wake_word is required")
	}
	if err := validateBareBundleName(bundle.MicroWakeWord.Manifest, ".json"); err != nil {
		return WakeBundle{}, err
	}
	if err := validateBareBundleName(bundle.MicroWakeWord.Model, ".tflite"); err != nil {
		return WakeBundle{}, err
	}
	if err := validateSHA256(bundle.MicroWakeWord.ManifestSHA256); err != nil {
		return WakeBundle{}, fmt.Errorf("microwakeword: invalid MWW manifest digest: %w", err)
	}
	if err := validateSHA256(bundle.MicroWakeWord.ModelSHA256); err != nil {
		return WakeBundle{}, fmt.Errorf("microwakeword: invalid MWW model digest: %w", err)
	}
	oww := bundle.OpenWakeWord
	if err := validateBareBundleName(oww.Metadata, ".json"); err != nil {
		return WakeBundle{}, err
	}
	if err := validateSHA256(oww.MetadataSHA256); err != nil {
		return WakeBundle{}, fmt.Errorf("microwakeword: invalid OWW metadata digest: %w", err)
	}
	if oww.RecommendedThreshold <= 0 || oww.RecommendedThreshold > 1 ||
		oww.RecommendedPatience < 1 || oww.RecommendedPatience > 20 {
		return WakeBundle{}, fmt.Errorf("microwakeword: invalid OWW threshold/patience")
	}
	// Bundles created before dual calibration used one policy for both modes.
	// Preserve their behavior while allowing new bundles to use a more
	// recall-friendly threshold only after MWW has already gated the audio.
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
	return bundle, nil
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
	return openCompanionShadow(packageName, false, hooks)
}

// OpenCompanionAgreementShadow runs OWW continuously while using the
// recall-oriented confirmation calibration. MWW still has to cross within the
// bounded agreement window, so the stricter OWW-only policy is unnecessary
// and would hide valid phrases from the second half of the pair.
func OpenCompanionAgreementShadow(packageName string, hooks ShadowHooks) (*ShadowScorer, WakeBundle, error) {
	return openCompanionShadow(packageName, true, hooks)
}

func openCompanionShadow(packageName string, confirmationMode bool, hooks ShadowHooks) (*ShadowScorer, WakeBundle, error) {
	engine, settings, bundle, err := openCompanionEngine(packageName, true, confirmationMode)
	if err != nil {
		return nil, WakeBundle{}, err
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
	if _, err := readVerifiedFile(
		filepath.Join(dir, bundle.OpenWakeWord.Metadata),
		maxCompanionMetadataBytes,
		bundle.OpenWakeWord.MetadataSHA256,
	); err != nil {
		return nil, OWWSettings{}, WakeBundle{}, err
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
