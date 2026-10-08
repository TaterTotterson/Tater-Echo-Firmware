package microwakeword

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

const (
	defaultManifestFilename    = "hey_tater.json"
	defaultModelFilename       = "hey_tater.tflite"
	defaultOWWMetadataFilename = "hey_tater.oww.json"
	defaultOWWONNXFilename     = "hey_tater.oww.onnx"
	defaultBundleFilename      = "hey_tater.wake-bundle.json"
)

// The paired default package rides inside every daemon OTA. Factory images
// also install these files directly, but embedding guarantees an existing Echo
// receives the exact same MWW/OWW pair after an ordinary executable update.
//
//go:embed models/hey_tater.json
var embeddedDefaultManifest []byte

//go:embed models/hey_tater.tflite
var embeddedDefaultModel []byte

//go:embed models/hey_tater.oww.json
var embeddedDefaultOWWMetadata []byte

//go:embed models/hey_tater.oww.onnx
var embeddedDefaultOWWONNX []byte

//go:embed models/hey_tater.wake-bundle.json
var embeddedDefaultBundle []byte

// EnsureEmbeddedDefaultPackage verifies and atomically installs the built-in
// Hey Tater pair. It never touches downloaded custom wake-word packages.
func EnsureEmbeddedDefaultPackage() (bool, error) {
	manifest, err := ParseManifest(embeddedDefaultManifest)
	if err != nil {
		return false, fmt.Errorf("microwakeword: embedded default manifest: %w", err)
	}
	bundle, err := ParseWakeBundle(embeddedDefaultBundle)
	if err != nil {
		return false, fmt.Errorf("microwakeword: embedded default bundle: %w", err)
	}
	if manifest.Model != defaultModelFilename ||
		bundle.MicroWakeWord.Manifest != defaultManifestFilename ||
		bundle.MicroWakeWord.Model != defaultModelFilename ||
		bundle.OpenWakeWord.Metadata != defaultOWWMetadataFilename {
		return false, fmt.Errorf("microwakeword: embedded default filenames do not match the release contract")
	}
	onnxArtifact, err := companionONNXArtifact(bundle.OpenWakeWord.Artifacts)
	if err != nil || onnxArtifact.File != defaultOWWONNXFilename {
		return false, fmt.Errorf("microwakeword: embedded default ONNX OWW classifier is invalid")
	}
	if !isTFLite(embeddedDefaultModel) || len(embeddedDefaultOWWONNX) < 2 || embeddedDefaultOWWONNX[0] != 0x08 {
		return false, fmt.Errorf("microwakeword: embedded default model format is invalid")
	}
	if !digestMatches(embeddedDefaultManifest, bundle.MicroWakeWord.ManifestSHA256) ||
		!digestMatches(embeddedDefaultModel, bundle.MicroWakeWord.ModelSHA256) ||
		!digestMatches(embeddedDefaultOWWMetadata, bundle.OpenWakeWord.MetadataSHA256) ||
		int64(len(embeddedDefaultOWWONNX)) != onnxArtifact.SizeBytes ||
		!digestMatches(embeddedDefaultOWWONNX, onnxArtifact.SHA256) {
		return false, fmt.Errorf("microwakeword: embedded default package digest mismatch")
	}

	dir := PackageDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("microwakeword: create default package directory: %w", err)
	}
	files := []struct {
		name string
		body []byte
	}{
		{defaultModelFilename, embeddedDefaultModel},
		{defaultOWWONNXFilename, embeddedDefaultOWWONNX},
		{defaultOWWMetadataFilename, embeddedDefaultOWWMetadata},
		{defaultBundleFilename, embeddedDefaultBundle},
		{defaultManifestFilename, embeddedDefaultManifest},
	}
	updated := false
	for _, file := range files {
		path := filepath.Join(dir, file.name)
		if fileDigestMatches(path, companionDigestBytes(file.body)) {
			continue
		}
		if err := atomicPackageWrite(path, file.body, 0o644); err != nil {
			return updated, fmt.Errorf("microwakeword: install embedded %s: %w", file.name, err)
		}
		updated = true
	}
	setCompanionInstallError(DefaultPackage, nil)
	return updated, nil
}

func isTFLite(body []byte) bool {
	return len(body) >= 8 && string(body[4:8]) == "TFL3"
}

func companionDigestBytes(body []byte) string {
	digest := sha256.Sum256(body)
	return fmt.Sprintf("%x", digest[:])
}
