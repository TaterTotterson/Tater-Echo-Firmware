package microwakeword

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ReleaseRuntimeSHA256 is injected after the target-specific runtime is built.
// Empty in developer builds, which deliberately disables release bootstrapping.
var ReleaseRuntimeSHA256 string

// ReleaseORTRuntimeSHA256 is populated for every Echo release build. ONNX
// Runtime is kept separate from the daemon and MWW runtime so a missing OWW
// companion can never prevent the satellite from booting or using MWW alone.
var ReleaseORTRuntimeSHA256 string

// This is the pinned Android/Bionic artifact used by local Biscuit developer
// builds. Releases always embed their target-specific digest, including the
// separate Linux/musl ARMv7 build used by Checkers and Rook.
const openWakeWordORTRuntimeSHA = "174233cf1a3f841f1eac82a4328f4f23f8819d5c62969c09e994a6b1abf498c1"

const releaseRuntimeLimit = 16 * 1024 * 1024

var (
	openWakeWordMelspecONNXURL = "https://github.com/dscripka/openWakeWord/releases/download/v0.5.1/melspectrogram.onnx"
	openWakeWordMelspecONNXSHA = "ba2b0e0f8b7b875369a2c89cb13360ff53bac436f2895cced9f479fa65eb176f"
	openWakeWordEmbedONNXURL   = "https://github.com/dscripka/openWakeWord/releases/download/v0.5.1/embedding_model.onnx"
	openWakeWordEmbedONNXSHA   = "70d164290c1d095d1d4ee149bc5e00543250a7316b59f31d056cff7bd3075c1f"
	releaseRuntimeURL          = func(target, version string) string {
		return fmt.Sprintf(
			"https://github.com/TaterTotterson/Tater-Echo-Firmware/releases/download/%s/tater-echo-%s-%s-wake-runtime.so",
			version, target, version)
	}
	releaseORTRuntimeURL = func(target, version string) string {
		return fmt.Sprintf(
			"https://github.com/TaterTotterson/Tater-Echo-Firmware/releases/download/%s/tater-echo-%s-%s-onnxruntime.so",
			version, target, version)
	}
)

var releaseVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?$`)

func expectedORTRuntimeSHA256() string {
	if digest := strings.ToLower(strings.TrimSpace(ReleaseORTRuntimeSHA256)); validateSHA256(digest) == nil {
		return digest
	}
	return openWakeWordORTRuntimeSHA
}

// EnsureReleaseCompanionRuntime lets an ordinary executable-only OTA cross the
// one-time native ABI boundary safely. Biscuit release executables carry these
// files inside the OTA itself and install them before anything dlopen's them.
// Older or developer builds retain the verified download path as a fallback.
// Failure does not stop the daemon: MWW-only remains available, explicitly
// selected OWW modes fail closed, and the next boot retries installation.
func EnsureReleaseCompanionRuntime(parent context.Context, target, version string) (bool, error) {
	expectedRuntime := strings.ToLower(strings.TrimSpace(ReleaseRuntimeSHA256))
	target = strings.ToLower(strings.TrimSpace(target))
	version = strings.TrimSpace(version)
	if validateSHA256(expectedRuntime) != nil || !releaseVersionPattern.MatchString(version) {
		return false, nil
	}
	switch target {
	case "biscuit", "checkers", "rook":
	default:
		return false, fmt.Errorf("microwakeword: unsupported release runtime target %q", target)
	}
	dir := PackageDir()
	runtimePath := filepath.Join(dir, RuntimeFilename)
	expectedORT := strings.ToLower(strings.TrimSpace(ReleaseORTRuntimeSHA256))
	if validateSHA256(expectedORT) != nil {
		return false, fmt.Errorf("microwakeword: release ONNX Runtime digest is not embedded")
	}
	ortPath := filepath.Join(dir, OWWORTRuntimeFilename)
	melONNXPath := filepath.Join(dir, OWWMelspectrogramONNXFilename)
	embedONNXPath := filepath.Join(dir, OWWEmbeddingONNXFilename)
	if fileDigestMatches(runtimePath, expectedRuntime) && validateSHA256(expectedORT) == nil &&
		fileDigestMatches(ortPath, expectedORT) &&
		fileDigestMatches(melONNXPath, openWakeWordMelspecONNXSHA) &&
		fileDigestMatches(embedONNXPath, openWakeWordEmbedONNXSHA) {
		return false, nil
	}
	if available, updated, err := installEmbeddedReleaseCompanion(
		dir, expectedRuntime, expectedORT,
	); available {
		return updated, err
	}

	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	client := packageHTTPClient(45 * time.Second)
	runtimeURL := releaseRuntimeURL(target, version)
	runtimeBody, _, err := downloadPackageFile(ctx, client, runtimeURL, releaseRuntimeLimit)
	if err != nil {
		return false, fmt.Errorf("microwakeword: download release wake runtime: %w", err)
	}
	if len(runtimeBody) < 4 || string(runtimeBody[:4]) != "\x7fELF" || !digestMatches(runtimeBody, expectedRuntime) {
		return false, fmt.Errorf("microwakeword: release wake runtime failed ELF/SHA-256 verification")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("microwakeword: create runtime directory: %w", err)
	}
	if err := atomicPackageWrite(runtimePath, runtimeBody, 0o755); err != nil {
		return false, err
	}
	ortBody, _, err := downloadPackageFile(ctx, client,
		releaseORTRuntimeURL(target, version), releaseRuntimeLimit)
	if err != nil {
		return false, fmt.Errorf("microwakeword: download release ONNX Runtime: %w", err)
	}
	if len(ortBody) < 4 || string(ortBody[:4]) != "\x7fELF" || !digestMatches(ortBody, expectedORT) {
		return false, fmt.Errorf("microwakeword: release ONNX Runtime failed ELF/SHA-256 verification")
	}
	melONNX, _, err := downloadPackageFile(ctx, client, openWakeWordMelspecONNXURL, maxCompanionModelBytes)
	if err != nil || !digestMatches(melONNX, openWakeWordMelspecONNXSHA) {
		if err == nil {
			err = fmt.Errorf("SHA-256 mismatch")
		}
		return false, fmt.Errorf("microwakeword: fetch ONNX OWW melspectrogram model: %w", err)
	}
	embedONNX, _, err := downloadPackageFile(ctx, client, openWakeWordEmbedONNXURL, maxCompanionModelBytes)
	if err != nil || !digestMatches(embedONNX, openWakeWordEmbedONNXSHA) {
		if err == nil {
			err = fmt.Errorf("SHA-256 mismatch")
		}
		return false, fmt.Errorf("microwakeword: fetch ONNX OWW embedding model: %w", err)
	}
	for _, file := range []struct {
		path string
		body []byte
		mode os.FileMode
	}{
		{ortPath, ortBody, 0o755},
		{melONNXPath, melONNX, 0o644},
		{embedONNXPath, embedONNX, 0o644},
	} {
		if err := atomicPackageWrite(file.path, file.body, file.mode); err != nil {
			return false, err
		}
	}
	return true, nil
}

func installEmbeddedReleaseCompanion(dir, expectedRuntime, expectedORT string) (bool, bool, error) {
	assets := []struct {
		name       string
		body       []byte
		digest     string
		mode       os.FileMode
		requireELF bool
	}{
		{RuntimeFilename, embeddedReleaseRuntime, expectedRuntime, 0o755, true},
		{OWWORTRuntimeFilename, embeddedReleaseORTRuntime, expectedORT, 0o755, true},
		{OWWMelspectrogramONNXFilename, embeddedReleaseMelspectrogram, openWakeWordMelspecONNXSHA, 0o644, false},
		{OWWEmbeddingONNXFilename, embeddedReleaseEmbedding, openWakeWordEmbedONNXSHA, 0o644, false},
	}
	available := false
	for _, asset := range assets {
		available = available || len(asset.body) > 0
	}
	if !available {
		return false, false, nil
	}
	for _, asset := range assets {
		if len(asset.body) == 0 {
			return true, false, fmt.Errorf("microwakeword: embedded release asset %s is missing", asset.name)
		}
		if asset.requireELF && (len(asset.body) < 4 || string(asset.body[:4]) != "\x7fELF") {
			return true, false, fmt.Errorf("microwakeword: embedded release asset %s is not ELF", asset.name)
		}
		if !digestMatches(asset.body, asset.digest) {
			return true, false, fmt.Errorf("microwakeword: embedded release asset %s failed SHA-256 verification", asset.name)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return true, false, fmt.Errorf("microwakeword: create runtime directory: %w", err)
	}
	updated := false
	for _, asset := range assets {
		path := filepath.Join(dir, asset.name)
		if fileDigestMatches(path, asset.digest) {
			continue
		}
		if err := atomicPackageWrite(path, asset.body, asset.mode); err != nil {
			return true, updated, err
		}
		updated = true
	}
	return true, updated, nil
}

func fileDigestMatches(path, expected string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return false
	}
	return strings.EqualFold(hex.EncodeToString(digest.Sum(nil)), strings.TrimSpace(expected))
}
