package taternative

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/platform"
)

func TestCheckersOTARejectsPreLinuxBase(t *testing.T) {
	installer := &OTAInstaller{Target: "checkers", BaseOS: func() string { return platform.FireOS }}
	err := installer.Install(context.Background(), OTARequest{
		URL: "https://example.invalid/firmware", SHA256: strings.Repeat("0", 64),
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "USB factory installer") {
		t.Fatalf("pre-Linux Checkers OTA error = %v", err)
	}
}

func TestRookOTARejectsPreLinuxBase(t *testing.T) {
	installer := &OTAInstaller{Target: "rook", BaseOS: func() string { return platform.FireOS }}
	err := installer.Install(context.Background(), OTARequest{
		URL: "https://example.invalid/firmware", SHA256: strings.Repeat("0", 64),
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "USB factory installer") {
		t.Fatalf("pre-Linux Rook OTA error = %v", err)
	}
}

func linuxAppBundle(t *testing.T, target, baseOS string, extra map[string]string) []byte {
	return linuxAppBundleWithExtras(t, target, baseOS, extra, extra)
}

func linuxAppBundleWithExtras(t *testing.T, target, baseOS string, manifestExtra, archiveExtra map[string]string) []byte {
	return linuxAppBundleWithVersionAndExtras(t, target, baseOS, "v2.0.0-test", manifestExtra, archiveExtra)
}

func linuxAppBundleWithVersionAndExtras(t *testing.T, target, baseOS, version string, manifestExtra, archiveExtra map[string]string) []byte {
	t.Helper()
	files := map[string][]byte{
		"tater-echo": []byte("\x7fELFdaemon"),
		"tater-show": []byte("\x7fELFrenderer"),
	}
	for name, contents := range manifestExtra {
		files[name] = []byte(contents)
	}
	manifest := linuxAppManifest{Schema: 1, Target: target, BaseOS: baseOS, Version: version, Files: map[string]linuxAppFile{}}
	for name, contents := range files {
		digest := sha256.Sum256(contents)
		manifest.Files[name] = linuxAppFile{SHA256: hex.EncodeToString(digest[:]), Size: int64(len(contents))}
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var result bytes.Buffer
	zipped := gzip.NewWriter(&result)
	archive := tar.NewWriter(zipped)
	entries := map[string][]byte{"manifest.json": manifestBytes}
	for name, contents := range files {
		entries[name] = contents
	}
	for _, name := range []string{"manifest.json", "tater-echo", "tater-show"} {
		contents := entries[name]
		header := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(contents)), Typeflag: tar.TypeReg}
		if err := archive.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write(contents); err != nil {
			t.Fatal(err)
		}
	}
	for name, contents := range archiveExtra {
		if name == "manifest.json" || name == "tater-echo" || name == "tater-show" {
			continue
		}
		header := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(contents)), Typeflag: tar.TypeReg}
		if err := archive.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zipped.Close(); err != nil {
		t.Fatal(err)
	}
	return result.Bytes()
}

func writeLinuxAppBundle(t *testing.T, payload []byte) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "app.tar.gz")
	if err := os.WriteFile(filename, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	return filename
}

func TestReadLinuxAppManifest(t *testing.T) {
	payload := linuxAppBundle(t, "checkers", "tater-linux", nil)
	manifest, err := readLinuxAppManifest(writeLinuxAppBundle(t, payload), "checkers")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "v2.0.0-test" {
		t.Fatalf("version = %q", manifest.Version)
	}
}

func TestReadLinuxAppManifestRejectsWrongIdentity(t *testing.T) {
	payload := linuxAppBundle(t, "biscuit", "tater-linux", nil)
	_, err := readLinuxAppManifest(writeLinuxAppBundle(t, payload), "checkers")
	if err == nil || !strings.Contains(err.Error(), "identity is incomplete") {
		t.Fatalf("wrong identity error = %v", err)
	}
}

func TestReadLinuxAppManifestAcceptsRookOnlyForRook(t *testing.T) {
	payload := linuxAppBundle(t, "rook", "tater-linux", nil)
	bundle := writeLinuxAppBundle(t, payload)
	if _, err := readLinuxAppManifest(bundle, "rook"); err != nil {
		t.Fatal(err)
	}
	if _, err := readLinuxAppManifest(bundle, "checkers"); err == nil {
		t.Fatal("Checkers accepted a Rook application bundle")
	}
}

func TestReadLinuxAppManifestRejectsUnsafeVersion(t *testing.T) {
	payload := linuxAppBundleWithVersionAndExtras(t, "checkers", "tater-linux", "v2.0.0\nprevious=system", nil, nil)
	_, err := readLinuxAppManifest(writeLinuxAppBundle(t, payload), "checkers")
	if err == nil || !strings.Contains(err.Error(), "identity is incomplete") {
		t.Fatalf("unsafe version error = %v", err)
	}
}

func TestReadLinuxAppManifestRejectsUnexpectedPath(t *testing.T) {
	payload := linuxAppBundle(t, "checkers", "tater-linux", map[string]string{"../escape": "bad"})
	_, err := readLinuxAppManifest(writeLinuxAppBundle(t, payload), "checkers")
	if err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("unsafe path error = %v", err)
	}
}

func TestReadLinuxAppManifestRejectsExtraManifestFile(t *testing.T) {
	payload := linuxAppBundleWithExtras(t, "checkers", "tater-linux", map[string]string{"extra": "bad"}, nil)
	_, err := readLinuxAppManifest(writeLinuxAppBundle(t, payload), "checkers")
	if err == nil || !strings.Contains(err.Error(), "manifest has unexpected files") {
		t.Fatalf("unexpected file error = %v", err)
	}
}

func TestInstallLinuxAppBundleSwitchesApplicationSlot(t *testing.T) {
	stateDir := t.TempDir()
	payload := linuxAppBundle(t, "checkers", "tater-linux", nil)
	bundle := writeLinuxAppBundle(t, payload)
	manifest, err := readLinuxAppManifest(bundle, "checkers")
	if err != nil {
		t.Fatal(err)
	}
	if err := installLinuxAppBundle(bundle, stateDir, manifest); err != nil {
		t.Fatal(err)
	}
	link, err := os.Readlink(filepath.Join(stateDir, "app", "current"))
	if err != nil || link != filepath.Join("slots", "a") {
		t.Fatalf("current = %q, %v", link, err)
	}
	for _, name := range linuxAppRequiredFiles {
		if _, err := os.Stat(filepath.Join(stateDir, "app", "slots", "a", name)); err != nil {
			t.Fatalf("slot file %s: %v", name, err)
		}
	}
	pending, err := os.ReadFile(filepath.Join(stateDir, "app", "pending.env"))
	if err != nil || !strings.Contains(string(pending), "previous=system\nnew=a") {
		t.Fatalf("pending = %q, %v", pending, err)
	}
}

func TestInstallLinuxAppBundleRejectsPendingTrial(t *testing.T) {
	stateDir := t.TempDir()
	payload := linuxAppBundle(t, "checkers", "tater-linux", nil)
	bundle := writeLinuxAppBundle(t, payload)
	manifest, err := readLinuxAppManifest(bundle, "checkers")
	if err != nil {
		t.Fatal(err)
	}
	if err := installLinuxAppBundle(bundle, stateDir, manifest); err != nil {
		t.Fatal(err)
	}
	if err := installLinuxAppBundle(bundle, stateDir, manifest); err == nil || !strings.Contains(err.Error(), "trial is already pending") {
		t.Fatalf("pending trial error = %v", err)
	}
}

func TestInstallDispatchesCheckersLinuxAppBundle(t *testing.T) {
	payload := linuxAppBundle(t, "checkers", "tater-linux", nil)
	digest := sha256.Sum256(payload)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = writer.Write(payload)
	}))
	defer server.Close()

	restarted := make(chan struct{}, 1)
	stateDir := t.TempDir()
	installer := &OTAInstaller{
		Target:   "checkers",
		HTTP:     server.Client(),
		StateDir: stateDir,
		BaseOS:   func() string { return platform.TaterLinux },
		Restart:  func() { restarted <- struct{}{} },
	}
	err := installer.Install(context.Background(), OTARequest{
		URL: server.URL, SHA256: hex.EncodeToString(digest[:]), SizeBytes: int64(len(payload)),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "app", "slots", "a", "tater-echo")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-restarted:
	case <-time.After(2 * time.Second):
		t.Fatal("installer did not request restart")
	}
}

func TestInstallDispatchesRookLinuxAppBundle(t *testing.T) {
	payload := linuxAppBundle(t, "rook", "tater-linux", nil)
	digest := sha256.Sum256(payload)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(payload)
	}))
	defer server.Close()
	stateDir := t.TempDir()
	installer := &OTAInstaller{
		Target: "rook", HTTP: server.Client(), StateDir: stateDir,
		BaseOS: func() string { return platform.TaterLinux },
	}
	if err := installer.Install(context.Background(), OTARequest{
		URL: server.URL, SHA256: hex.EncodeToString(digest[:]), SizeBytes: int64(len(payload)),
	}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "app", "slots", "a", "tater-echo")); err != nil {
		t.Fatal(err)
	}
}
