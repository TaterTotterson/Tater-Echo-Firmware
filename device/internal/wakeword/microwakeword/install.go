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
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/clock"
)

const packageDownloadTimeout = 12 * time.Second

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
	if cachedPackageValid(manifestPath, modelPath) {
		return packageName, nil
	}

	ctx, cancel := context.WithTimeout(parent, packageDownloadTimeout)
	defer cancel()
	client := packageHTTPClient(packageDownloadTimeout)
	manifestBody, finalManifestURL, err := downloadPackageFile(ctx, client, parsed.String(), maxManifestBytes)
	if err != nil {
		return "", err
	}
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
	return packageName, nil
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
