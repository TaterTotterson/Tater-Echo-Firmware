package microwakeword

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// DefaultPackageDir is populated separately from the A/B firmware binary
	// because the runtime/model change less often and should not double every
	// OTA payload.
	DefaultPackageDir = "/data/local/share/tater/microwakeword"
	DefaultPackage    = "hey_tater"
	RuntimeFilename   = "libtater_microwakeword.so"
	maxManifestBytes  = 64 * 1024
	maxModelBytes     = 16 * 1024 * 1024
)

// PackageDir returns the on-device runtime/model directory. The override makes
// host tests and device bring-up possible without changing the firmware.
func PackageDir() string {
	if dir := strings.TrimSpace(os.Getenv("TATER_MWW_DIR")); dir != "" {
		return dir
	}
	return DefaultPackageDir
}

// ManifestFilename maps a configured package name to one JSON file while
// rejecting path traversal. A .json suffix is optional for convenience.
func ManifestFilename(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("microwakeword: no model package configured")
	}
	if filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) {
		return "", fmt.Errorf("microwakeword: model package must be a bare name, got %q", name)
	}
	if filepath.Ext(name) == "" {
		name += ".json"
	}
	if !strings.EqualFold(filepath.Ext(name), ".json") {
		return "", fmt.Errorf("microwakeword: model package must end in .json, got %q", name)
	}
	return name, nil
}

// ScorerOverrides contains optional live detector tuning. Zero values retain
// the package manifest's calibrated values.
type ScorerOverrides struct {
	Threshold          float32
	SlidingWindow      int
	CloseMissThreshold float32
}

// OpenShadow validates a model package, opens its native engine, and starts a
// non-blocking shadow scorer. thresholdOverride <= 0 uses the manifest value.
func OpenShadow(packageName string, thresholdOverride float32,
	onCross func(score float32, at time.Time)) (*ShadowScorer, error) {
	return OpenShadowTuned(packageName, ScorerOverrides{Threshold: thresholdOverride}, onCross)
}

// OpenShadowTuned is OpenShadow with live threshold/window/close-miss tuning.
func OpenShadowTuned(packageName string, overrides ScorerOverrides,
	onCross func(score float32, at time.Time)) (*ShadowScorer, error) {
	dir := PackageDir()
	manifestName, err := ManifestFilename(packageName)
	if err != nil {
		return nil, err
	}
	manifestPath := filepath.Join(dir, manifestName)
	raw, err := readLimited(manifestPath, maxManifestBytes)
	if err != nil {
		return nil, fmt.Errorf("microwakeword: read manifest %s: %w", manifestPath, err)
	}
	manifest, err := ParseManifest(raw)
	if err != nil {
		return nil, err
	}
	settings := manifest.RuntimeConfig()
	if overrides.Threshold > 0 {
		if overrides.Threshold > 1 {
			return nil, fmt.Errorf("microwakeword: threshold override must be in (0, 1], got %g", overrides.Threshold)
		}
		settings.Threshold = overrides.Threshold
	}
	if overrides.SlidingWindow != 0 {
		if overrides.SlidingWindow < 1 || overrides.SlidingWindow > 100 {
			return nil, fmt.Errorf("microwakeword: sliding-window override must be between 1 and 100, got %d", overrides.SlidingWindow)
		}
		settings.SlidingWindow = overrides.SlidingWindow
	}
	if overrides.CloseMissThreshold > 0 {
		if overrides.CloseMissThreshold > settings.Threshold {
			return nil, fmt.Errorf("microwakeword: close-miss override %g exceeds wake threshold %g", overrides.CloseMissThreshold, settings.Threshold)
		}
		settings.CloseMissThreshold = overrides.CloseMissThreshold
	} else if settings.CloseMissThreshold > settings.Threshold {
		// Lowering only the live wake threshold must not make an otherwise
		// valid manifest unloadable; at that point every sub-threshold score
		// is, by definition, at most a close miss.
		settings.CloseMissThreshold = settings.Threshold
	}
	modelPath, err := manifest.ResolveModelPath(dir)
	if err != nil {
		return nil, err
	}
	model, err := readLimited(modelPath, maxModelBytes)
	if err != nil {
		return nil, fmt.Errorf("microwakeword: read model %s: %w", modelPath, err)
	}
	if len(model) == 0 {
		return nil, fmt.Errorf("microwakeword: model at %s is empty", modelPath)
	}
	runtimePath := filepath.Join(dir, RuntimeFilename)
	if _, err := os.Stat(runtimePath); err != nil {
		return nil, fmt.Errorf("microwakeword: runtime not installed at %s: %w", runtimePath, err)
	}
	runtime, err := OpenNativeRuntime(runtimePath)
	if err != nil {
		return nil, err
	}
	engine, err := runtime.NewEngine(model, settings)
	if err != nil {
		return nil, err
	}
	scorer, err := NewShadowScorer(engine, settings.Threshold,
		settings.SlidingWindow, settings.CloseMissThreshold, onCross)
	if err != nil {
		_ = engine.Close()
		return nil, err
	}
	scorer.info = fmt.Sprintf("%s, package %s, threshold %.3f, window %d",
		engine.Info(), strings.TrimSuffix(manifestName, filepath.Ext(manifestName)),
		settings.Threshold, settings.SlidingWindow)
	return scorer, nil
}

func readLimited(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	return raw, nil
}
