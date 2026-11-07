package microwakeword

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// NanoWakeWord uses the same 80 ms mel/embedding stream as openWakeWord. Its
// exported classifier consumes the same [1,16,96] tensor, so the Echo can
// reuse the already shipped ONNX feature models and runtime.
type NanoSettings struct {
	Architecture string
	Threshold    float32
	Patience     int
}

type nanoORTEngine struct{ OWWEngine }

func (engine *nanoORTEngine) Info() string {
	return strings.Replace(engine.OWWEngine.Info(), "openWakeWord", "NanoWakeWord", 1)
}

func (engine *nanoORTEngine) scorePeriod() time.Duration {
	return time.Duration(owwChunkSamples) * time.Second / owwSampleRate
}

func normalizeNanoArchitecture(value string) (string, error) {
	architecture := strings.ToLower(strings.TrimSpace(value))
	switch architecture {
	case "cnn", "gru":
		return architecture, nil
	default:
		return "", fmt.Errorf("microwakeword: unsupported NanoWakeWord architecture %q", value)
	}
}

func nanoVariant(bundle WakeBundle, requested string) (BundleNanoWakeWordVariant, string, error) {
	architecture := strings.ToLower(strings.TrimSpace(requested))
	if architecture == "" {
		architecture = bundle.NanoWakeWord.DefaultArchitecture
	}
	architecture, err := normalizeNanoArchitecture(architecture)
	if err != nil {
		return BundleNanoWakeWordVariant{}, "", err
	}
	variant, ok := bundle.NanoWakeWord.Variants[architecture]
	if !ok {
		return BundleNanoWakeWordVariant{}, "", fmt.Errorf(
			"microwakeword: NanoWakeWord %s model is unavailable", architecture)
	}
	return variant, architecture, nil
}

func OpenNanoShadow(packageName, architecture string, hooks ShadowHooks) (*ShadowScorer, WakeBundle, NanoSettings, error) {
	return OpenNanoShadowTuned(packageName, architecture, NanoSettings{}, hooks)
}

// OpenNanoShadowTuned applies optional live threshold/patience values. Zero
// values retain the selected architecture's validated wake-bundle policy.
func OpenNanoShadowTuned(packageName, architecture string, overrides NanoSettings, hooks ShadowHooks) (*ShadowScorer, WakeBundle, NanoSettings, error) {
	prefix, err := companionPrefix(packageName)
	if err != nil {
		return nil, WakeBundle{}, NanoSettings{}, err
	}
	dir := PackageDir()
	bundleRaw, err := readLimited(filepath.Join(dir, prefix+CompanionBundleSuffix), maxCompanionBundleBytes)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, WakeBundle{}, NanoSettings{}, ErrNanoUnavailable
		}
		return nil, WakeBundle{}, NanoSettings{}, fmt.Errorf("microwakeword: read NanoWakeWord bundle: %w", err)
	}
	bundle, err := ParseWakeBundle(bundleRaw)
	if err != nil {
		return nil, WakeBundle{}, NanoSettings{}, err
	}
	variant, architecture, err := nanoVariant(bundle, architecture)
	if err != nil {
		return nil, WakeBundle{}, NanoSettings{}, err
	}
	artifact, err := nanoONNXArtifact(variant)
	if err != nil {
		return nil, WakeBundle{}, NanoSettings{}, err
	}
	classifierPath := filepath.Join(dir, artifact.File)
	if _, err := readVerifiedFile(classifierPath, maxCompanionModelBytes, artifact.SHA256); err != nil {
		return nil, WakeBundle{}, NanoSettings{}, err
	}
	for _, shared := range []struct{ name, digest string }{
		{OWWMelspectrogramONNXFilename, openWakeWordMelspecONNXSHA},
		{OWWEmbeddingONNXFilename, openWakeWordEmbedONNXSHA},
		{OWWORTRuntimeFilename, expectedORTRuntimeSHA256()},
	} {
		if validateSHA256(shared.digest) != nil || !fileDigestMatches(filepath.Join(dir, shared.name), shared.digest) {
			return nil, WakeBundle{}, NanoSettings{}, fmt.Errorf(
				"microwakeword: verified ONNX NanoWakeWord asset %s is not installed", shared.name)
		}
	}
	settings := NanoSettings{
		Architecture: architecture,
		Threshold:    variant.RecommendedThreshold,
		Patience:     variant.RecommendedPatience,
	}
	if overrides.Threshold > 0 {
		if overrides.Threshold > 1 {
			return nil, WakeBundle{}, NanoSettings{}, fmt.Errorf("microwakeword: NanoWakeWord threshold override must be in (0, 1]")
		}
		settings.Threshold = overrides.Threshold
	}
	if overrides.Patience > 0 {
		if overrides.Patience > 20 {
			return nil, WakeBundle{}, NanoSettings{}, fmt.Errorf("microwakeword: NanoWakeWord patience override must be between 1 and 20")
		}
		settings.Patience = overrides.Patience
	}
	base, err := newORTOWWEngine(dir, classifierPath, OWWSettings{
		Threshold: settings.Threshold,
		Patience:  settings.Patience,
	})
	if err != nil {
		return nil, WakeBundle{}, NanoSettings{}, err
	}
	engine := &nanoORTEngine{OWWEngine: base}
	scorer, err := NewShadowScorerWithPolicy(engine, settings.Patience, 0, DetectionPolicy{
		Profile: "nanowakeword", Threshold: settings.Threshold,
		PeakThreshold: settings.Threshold, MinimumActiveWindow: settings.Patience,
		MinimumRiseScore: -1, Refractory: DefaultShadowRefractory,
	}, hooks)
	if err != nil {
		_ = engine.Close()
		return nil, WakeBundle{}, NanoSettings{}, err
	}
	return scorer, bundle, settings, nil
}

var ErrNanoUnavailable = fmt.Errorf("NanoWakeWord model is not installed")

// InstallNanoURLRevision installs one CNN or GRU variant from a canonical
// Tater wake bundle. The URL, revision, and architecture form the cache key so
// switching architecture never reuses a classifier with a different shape.
func InstallNanoURLRevision(parent context.Context, bundleURL, revision, requestedArchitecture string) (string, error) {
	parsed, err := validatedHTTPURL(bundleURL)
	if err != nil {
		return "", fmt.Errorf("microwakeword: %w", err)
	}
	architecture, err := normalizeNanoArchitecture(requestedArchitecture)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(parsed.String() + "\n" + strings.TrimSpace(revision) + "\n" + architecture))
	packageName := "tater_nww_" + architecture + "_" + hex.EncodeToString(digest[:8])
	if cachedNanoValid(packageName, architecture) {
		return packageName, nil
	}

	ctx, cancel := context.WithTimeout(parent, packageDownloadTimeout)
	defer cancel()
	client := packageHTTPClient(packageDownloadTimeout)
	bundleRaw, finalBundleURL, err := downloadPackageFile(ctx, client, parsed.String(), maxCompanionBundleBytes)
	if err != nil {
		return "", err
	}
	bundle, err := ParseWakeBundle(bundleRaw)
	if err != nil {
		return "", err
	}
	variant, architecture, err := nanoVariant(bundle, architecture)
	if err != nil {
		return "", err
	}
	artifact, err := nanoONNXArtifact(variant)
	if err != nil {
		return "", err
	}
	modelRef, err := url.Parse(artifact.File)
	if err != nil {
		return "", fmt.Errorf("microwakeword: invalid NanoWakeWord model URL: %w", err)
	}
	model, _, err := downloadPackageFile(ctx, client,
		finalBundleURL.ResolveReference(modelRef).String(), maxCompanionModelBytes)
	if err != nil {
		return "", err
	}
	if int64(len(model)) != artifact.SizeBytes || !digestMatches(model, artifact.SHA256) {
		return "", fmt.Errorf("microwakeword: NanoWakeWord model size or SHA-256 mismatch")
	}
	if len(model) < 2 || model[0] != 0x08 {
		return "", fmt.Errorf("microwakeword: downloaded NanoWakeWord model is not an ONNX ModelProto")
	}
	var boundaryModel []byte
	if bundle.WakeBoundary.Model != "" {
		boundaryRef, parseErr := url.Parse(bundle.WakeBoundary.Model)
		if parseErr != nil {
			return "", fmt.Errorf("microwakeword: invalid wake boundary model URL: %w", parseErr)
		}
		boundaryModel, _, err = downloadPackageFile(ctx, client,
			finalBundleURL.ResolveReference(boundaryRef).String(), maxBoundaryModelBytes)
		if err != nil {
			return "", err
		}
		if int64(len(boundaryModel)) != bundle.WakeBoundary.ModelSizeBytes ||
			!digestMatches(boundaryModel, bundle.WakeBoundary.ModelSHA256) {
			return "", fmt.Errorf("microwakeword: wake boundary model size or SHA-256 mismatch")
		}
		if len(boundaryModel) < 8 || string(boundaryModel[4:8]) != "TFL3" {
			return "", fmt.Errorf("microwakeword: downloaded wake boundary model is not a TFLite FlatBuffer")
		}
	}

	dir := PackageDir()
	prefix, _ := companionPrefix(packageName)
	modelName := prefix + "." + architecture + NanoONNXModelSuffix
	boundaryName := ""
	if len(boundaryModel) > 0 {
		boundaryName = prefix + BoundaryTFLiteModelSuffix
		bundle.WakeBoundary.Model = boundaryName
	}
	bundleName := prefix + CompanionBundleSuffix
	variant.Model = modelName
	variant.ModelSHA256 = artifact.SHA256
	variant.ModelSizeBytes = int64(len(model))
	variant.Artifacts = nil
	bundle.NanoWakeWord.Variants = map[string]BundleNanoWakeWordVariant{architecture: variant}
	bundle.NanoWakeWord.DefaultArchitecture = architecture
	bundleBody, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return "", fmt.Errorf("microwakeword: encode installed NanoWakeWord bundle: %w", err)
	}
	bundleBody = append(bundleBody, '\n')
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("microwakeword: create package directory: %w", err)
	}
	if err := atomicPackageWrite(filepath.Join(dir, modelName), model, 0o644); err != nil {
		return "", err
	}
	if boundaryName != "" {
		if err := atomicPackageWrite(filepath.Join(dir, boundaryName), boundaryModel, 0o644); err != nil {
			return "", err
		}
	}
	if err := atomicPackageWrite(filepath.Join(dir, bundleName), bundleBody, 0o644); err != nil {
		_ = os.Remove(filepath.Join(dir, modelName))
		return "", err
	}
	return packageName, nil
}

func cachedNanoValid(packageName, architecture string) bool {
	prefix, err := companionPrefix(packageName)
	if err != nil {
		return false
	}
	raw, err := readLimited(filepath.Join(PackageDir(), prefix+CompanionBundleSuffix), maxCompanionBundleBytes)
	if err != nil {
		return false
	}
	bundle, err := ParseWakeBundle(raw)
	if err != nil {
		return false
	}
	variant, _, err := nanoVariant(bundle, architecture)
	if err != nil {
		return false
	}
	artifact, err := nanoONNXArtifact(variant)
	if err != nil {
		return false
	}
	_, err = readVerifiedFile(filepath.Join(PackageDir(), artifact.File), maxCompanionModelBytes, artifact.SHA256)
	if err != nil {
		return false
	}
	return bundle.WakeBoundary.Model == "" || cachedBoundaryValid(packageName)
}
