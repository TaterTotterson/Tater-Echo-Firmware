package microwakeword

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/clock"
)

const packageDownloadTimeout = 12 * time.Second

var companionInstallState struct {
	sync.RWMutex
	errors map[string]string
}

// CompanionInstallError reports why an optional dual-model companion was not
// installed. MWW installation still succeeds so a trainer/server that has not
// published OWW yet can never disable the existing wake word.
func CompanionInstallError(packageName string) string {
	companionInstallState.RLock()
	defer companionInstallState.RUnlock()
	return companionInstallState.errors[packageName]
}

// CompanionReady reports whether packageName has a complete, hash-verified
// openWakeWord companion. Dual mode uses this after MWW installation so it
// cannot silently pair an independently selected OWW phrase with that MWW.
func CompanionReady(packageName string) bool {
	return cachedCompanionValid(packageName)
}

// PairedCompanionPackage returns packageName only when its MWW package and
// OWW companion are both present and verified under the same cache identity.
// An OWW-only package is deliberately rejected even if its companion is valid.
func PairedCompanionPackage(packageName string) (string, error) {
	packageName = strings.TrimSpace(packageName)
	if packageName == "" {
		return "", fmt.Errorf("microwakeword: paired companion package is empty")
	}
	dir := PackageDir()
	if !cachedPackageValid(
		filepath.Join(dir, packageName+".json"),
		filepath.Join(dir, packageName+".tflite"),
	) {
		return "", fmt.Errorf("microwakeword: paired companion has no matching MWW package")
	}
	if companionErr := CompanionInstallError(packageName); companionErr != "" {
		return "", fmt.Errorf("microwakeword: paired companion installation failed: %s", companionErr)
	}
	if !cachedCompanionValid(packageName) {
		return "", fmt.Errorf("microwakeword: paired companion is unavailable")
	}
	return packageName, nil
}

func setCompanionInstallError(packageName string, err error) {
	companionInstallState.Lock()
	defer companionInstallState.Unlock()
	if companionInstallState.errors == nil {
		companionInstallState.errors = make(map[string]string)
	}
	if err == nil {
		delete(companionInstallState.errors, packageName)
	} else {
		companionInstallState.errors[packageName] = err.Error()
	}
}

// InstallPackageURL downloads and validates a Tater/ESPHome microWakeWord
// manifest plus its relative model into PackageDir. The URL-derived package
// name is stable, path traversal is rejected by ParseManifest, and both files
// become visible only through atomic renames.
func InstallPackageURL(parent context.Context, manifestURL string) (string, error) {
	return InstallPackageURLRevision(parent, manifestURL, "")
}

// InstallPackageURLRevision installs a package using Tater's model revision as
// part of the cache identity. A trainer may publish new bytes at a stable JSON
// URL; without the revision, a live settings push would incorrectly retain the
// previous validated package forever.
func InstallPackageURLRevision(parent context.Context, manifestURL, revision string) (string, error) {
	parsed, err := validatedHTTPURL(manifestURL)
	if err != nil {
		return "", fmt.Errorf("microwakeword: %w", err)
	}
	digest := sha256.Sum256([]byte(parsed.String() + "\n" + strings.TrimSpace(revision)))
	packageName := "tater_custom_" + hex.EncodeToString(digest[:8])
	dir := PackageDir()
	manifestPath := filepath.Join(dir, packageName+".json")
	modelPath := filepath.Join(dir, packageName+".tflite")

	ctx, cancel := context.WithTimeout(parent, packageDownloadTimeout)
	defer cancel()
	client := packageHTTPClient(packageDownloadTimeout)
	if cachedPackageValid(manifestPath, modelPath) {
		if !cachedCompanionValid(packageName) {
			setCompanionInstallError(packageName,
				installCompanion(ctx, client, parsed, packageName, nil, nil))
		}
		return packageName, nil
	}
	manifestBody, finalManifestURL, err := downloadPackageFile(ctx, client, parsed.String(), maxManifestBytes)
	if err != nil {
		return "", err
	}
	publishedManifestBody := append([]byte(nil), manifestBody...)
	manifest, err := ParseManifest(manifestBody)
	if err != nil {
		return "", err
	}
	modelReference, err := url.Parse(manifest.Model)
	if err != nil {
		return "", fmt.Errorf("microwakeword: invalid model URL: %w", err)
	}
	modelURL := finalManifestURL.ResolveReference(modelReference)
	if _, err := validatedHTTPURL(modelURL.String()); err != nil {
		return "", fmt.Errorf("microwakeword: model %w", err)
	}
	modelBody, _, err := downloadPackageFile(ctx, client, modelURL.String(), maxModelBytes)
	if err != nil {
		return "", err
	}
	if len(modelBody) < 8 || string(modelBody[4:8]) != "TFL3" {
		return "", fmt.Errorf("microwakeword: downloaded model is not a TFLite FlatBuffer")
	}
	manifest.Model = filepath.Base(modelPath)
	manifestBody, err = json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("microwakeword: encode installed manifest: %w", err)
	}
	manifestBody = append(manifestBody, '\n')
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("microwakeword: create package directory: %w", err)
	}
	if err := atomicPackageWrite(modelPath, modelBody, 0o644); err != nil {
		return "", err
	}
	if err := atomicPackageWrite(manifestPath, manifestBody, 0o644); err != nil {
		_ = os.Remove(modelPath)
		return "", err
	}
	setCompanionInstallError(packageName,
		installCompanion(ctx, client, finalManifestURL, packageName, publishedManifestBody, modelBody))
	return packageName, nil
}

// InstallCompanionURLRevision installs a trainer-produced .wake-bundle.json
// without requiring its microWakeWord half to be active. This is the source
// path used when OWW is the primary detector or uses a separately selected
// model. The bundle still authenticates every OWW artifact by size and hash.
func InstallCompanionURLRevision(parent context.Context, bundleURL, revision string) (string, error) {
	parsed, err := validatedHTTPURL(bundleURL)
	if err != nil {
		return "", fmt.Errorf("microwakeword: %w", err)
	}
	digest := sha256.Sum256([]byte(parsed.String() + "\n" + strings.TrimSpace(revision)))
	packageName := "tater_oww_" + hex.EncodeToString(digest[:8])
	if cachedCompanionValid(packageName) {
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
	if err := installCompanionArtifacts(ctx, client, finalBundleURL, packageName, bundle); err != nil {
		setCompanionInstallError(packageName, err)
		return "", err
	}
	setCompanionInstallError(packageName, nil)
	return packageName, nil
}

func installCompanion(ctx context.Context, client *http.Client, manifestURL *url.URL,
	packageName string, downloadedManifest, downloadedModel []byte) error {
	if manifestURL == nil {
		return fmt.Errorf("microwakeword: companion manifest URL is unavailable")
	}
	bundleURL := *manifestURL
	bundleURL.Path = strings.TrimSuffix(bundleURL.Path, filepath.Ext(bundleURL.Path)) + CompanionBundleSuffix
	bundleURL.RawPath = ""
	bundleRaw, finalBundleURL, found, err := downloadOptionalPackageFile(
		ctx, client, bundleURL.String(), maxCompanionBundleBytes)
	if err != nil || !found {
		if !found && err == nil {
			return ErrCompanionUnavailable
		}
		return err
	}
	bundle, err := ParseWakeBundle(bundleRaw)
	if err != nil {
		return err
	}
	if len(downloadedManifest) > 0 {
		if !digestMatches(downloadedManifest, bundle.MicroWakeWord.ManifestSHA256) ||
			!digestMatches(downloadedModel, bundle.MicroWakeWord.ModelSHA256) {
			return fmt.Errorf("microwakeword: companion bundle does not match its MWW package")
		}
	} else {
		installedModel, readErr := readLimited(
			filepath.Join(PackageDir(), packageName+".tflite"), maxModelBytes)
		if readErr != nil || !digestMatches(installedModel, bundle.MicroWakeWord.ModelSHA256) {
			return fmt.Errorf("microwakeword: companion bundle does not match the cached MWW model")
		}
	}
	return installCompanionArtifacts(ctx, client, finalBundleURL, packageName, bundle)
}

func installCompanionArtifacts(ctx context.Context, client *http.Client, finalBundleURL *url.URL,
	packageName string, bundle WakeBundle) error {
	metadataRef, err := url.Parse(bundle.OpenWakeWord.Metadata)
	if err != nil {
		return fmt.Errorf("microwakeword: invalid OWW metadata URL: %w", err)
	}
	metadata, _, err := downloadPackageFile(ctx, client,
		finalBundleURL.ResolveReference(metadataRef).String(), maxCompanionMetadataBytes)
	if err != nil {
		return err
	}
	if !digestMatches(metadata, bundle.OpenWakeWord.MetadataSHA256) {
		return fmt.Errorf("microwakeword: OWW metadata SHA-256 mismatch")
	}
	artifact, _ := companionONNXArtifact(bundle.OpenWakeWord.Artifacts)
	modelRef, err := url.Parse(artifact.File)
	if err != nil {
		return fmt.Errorf("microwakeword: invalid OWW model URL: %w", err)
	}
	model, _, err := downloadPackageFile(ctx, client,
		finalBundleURL.ResolveReference(modelRef).String(), maxCompanionModelBytes)
	if err != nil {
		return err
	}
	if int64(len(model)) != artifact.SizeBytes || !digestMatches(model, artifact.SHA256) {
		return fmt.Errorf("microwakeword: OWW model size or SHA-256 mismatch")
	}
	if len(model) < 2 || model[0] != 0x08 {
		return fmt.Errorf("microwakeword: downloaded OWW model is not an ONNX ModelProto")
	}

	dir := PackageDir()
	prefix, _ := companionPrefix(packageName)
	metadataName := prefix + CompanionMetadataSuffix
	modelName := prefix + CompanionONNXModelSuffix
	bundleName := prefix + CompanionBundleSuffix
	bundle.OpenWakeWord.Metadata = metadataName
	bundle.OpenWakeWord.Artifacts = map[string]BundleArtifact{
		"onnx": {File: modelName, SHA256: artifact.SHA256, SizeBytes: int64(len(model))},
	}
	bundleBody, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return fmt.Errorf("microwakeword: encode installed wake bundle: %w", err)
	}
	bundleBody = append(bundleBody, '\n')
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("microwakeword: create package directory: %w", err)
	}
	if err := atomicPackageWrite(filepath.Join(dir, modelName), model, 0o644); err != nil {
		return err
	}
	if err := atomicPackageWrite(filepath.Join(dir, metadataName), metadata, 0o644); err != nil {
		return err
	}
	return atomicPackageWrite(filepath.Join(dir, bundleName), bundleBody, 0o644)
}

func downloadOptionalPackageFile(ctx context.Context, client *http.Client, rawURL string, limit int64) ([]byte, *url.URL, bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, nil, false, fmt.Errorf("microwakeword: create companion download: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, nil, false, fmt.Errorf("microwakeword: download %s: %w", rawURL, err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, nil, false, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, nil, false, fmt.Errorf("microwakeword: download %s: HTTP %d", rawURL, response.StatusCode)
	}
	finalURL, err := validatedHTTPURL(response.Request.URL.String())
	if err != nil {
		return nil, nil, false, err
	}
	if response.ContentLength > limit {
		return nil, nil, false, fmt.Errorf("microwakeword: companion download exceeds %d bytes", limit)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, nil, false, fmt.Errorf("microwakeword: read companion download: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, nil, false, fmt.Errorf("microwakeword: companion download exceeds %d bytes", limit)
	}
	return body, finalURL, true, nil
}

func digestMatches(body []byte, expected string) bool {
	digest := sha256.Sum256(body)
	return strings.EqualFold(hex.EncodeToString(digest[:]), strings.TrimSpace(expected))
}

func cachedCompanionValid(packageName string) bool {
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
	artifact, err := companionONNXArtifact(bundle.OpenWakeWord.Artifacts)
	if err != nil {
		return false
	}
	if _, err = readVerifiedFile(
		filepath.Join(PackageDir(), bundle.OpenWakeWord.Metadata),
		maxCompanionMetadataBytes,
		bundle.OpenWakeWord.MetadataSHA256,
	); err != nil {
		return false
	}
	_, err = readVerifiedFile(
		filepath.Join(PackageDir(), artifact.File),
		maxCompanionModelBytes,
		artifact.SHA256,
	)
	return err == nil
}

func packageHTTPClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	tlsConfig := transport.TLSClientConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{}
	} else {
		tlsConfig = tlsConfig.Clone()
	}
	tlsConfig.Time = clock.VerificationNow
	transport.TLSClientConfig = tlsConfig
	return &http.Client{Timeout: timeout, Transport: transport}
}

func validatedHTTPURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid package URL: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("package URL must use http:// or https://")
	}
	return parsed, nil
}

func downloadPackageFile(ctx context.Context, client *http.Client, rawURL string, limit int64) ([]byte, *url.URL, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("microwakeword: create download: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, nil, fmt.Errorf("microwakeword: download %s: %w", rawURL, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("microwakeword: download %s: HTTP %d", rawURL, response.StatusCode)
	}
	finalURL, err := validatedHTTPURL(response.Request.URL.String())
	if err != nil {
		return nil, nil, err
	}
	if response.ContentLength > limit {
		return nil, nil, fmt.Errorf("microwakeword: download exceeds %d bytes", limit)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, nil, fmt.Errorf("microwakeword: read download: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, nil, fmt.Errorf("microwakeword: download exceeds %d bytes", limit)
	}
	return body, finalURL, nil
}

func cachedPackageValid(manifestPath, modelPath string) bool {
	raw, err := readLimited(manifestPath, maxManifestBytes)
	if err != nil {
		return false
	}
	manifest, err := ParseManifest(raw)
	if err != nil || manifest.Model != filepath.Base(modelPath) {
		return false
	}
	model, err := readLimited(modelPath, maxModelBytes)
	return err == nil && len(model) >= 8 && string(model[4:8]) == "TFL3"
}

func atomicPackageWrite(path string, body []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".mww-*")
	if err != nil {
		return fmt.Errorf("microwakeword: create temporary package file: %w", err)
	}
	name := temporary.Name()
	defer os.Remove(name)
	if _, err := temporary.Write(body); err != nil {
		temporary.Close()
		return fmt.Errorf("microwakeword: write package file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("microwakeword: sync package file: %w", err)
	}
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return fmt.Errorf("microwakeword: chmod package file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("microwakeword: close package file: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("microwakeword: install package file: %w", err)
	}
	return nil
}
