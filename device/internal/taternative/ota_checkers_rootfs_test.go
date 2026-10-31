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

func TestCrownOTARejectsPreLinuxBase(t *testing.T) {
	installer := &OTAInstaller{Target: "crown", BaseOS: func() string { return platform.FireOS }}
	err := installer.Install(context.Background(), OTARequest{
		URL: "https://example.invalid/firmware", SHA256: strings.Repeat("0", 64),
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "USB factory installer") {
		t.Fatalf("pre-Linux Crown OTA error = %v", err)
	}
}

func TestAthenaOTARejectsPreLinuxBase(t *testing.T) {
	installer := &OTAInstaller{Target: "athena", BaseOS: func() string { return platform.FireOS }}
	err := installer.Install(context.Background(), OTARequest{
		URL: "https://example.invalid/firmware", SHA256: strings.Repeat("0", 64),
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "USB factory installer") {
		t.Fatalf("pre-Linux Athena OTA error = %v", err)
	}
}

func TestDotOTARejectsPreLinuxBase(t *testing.T) {
	for _, target := range []string{"biscuit", "radar"} {
		installer := &OTAInstaller{Target: target, BaseOS: func() string { return platform.FireOS }}
		err := installer.Install(context.Background(), OTARequest{
			URL: "https://example.invalid/firmware", SHA256: strings.Repeat("0", 64),
		}, nil)
		if err == nil || !strings.Contains(err.Error(), "factory installer") {
			t.Fatalf("pre-Linux %s OTA error = %v", target, err)
		}
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
	}
	orderedFiles := []string{"tater-echo"}
	if target == "checkers" || target == "crown" || target == "athena" || target == "rook" {
		files["tater-show"] = []byte("\x7fELFrenderer")
		orderedFiles = append(orderedFiles, "tater-show")
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
	for _, name := range append([]string{"manifest.json"}, orderedFiles...) {
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

func TestReadLinuxAppManifestAcceptsCrownOnlyForCrown(t *testing.T) {
	payload := linuxAppBundle(t, "crown", "tater-linux", nil)
	bundle := writeLinuxAppBundle(t, payload)
	if _, err := readLinuxAppManifest(bundle, "crown"); err != nil {
		t.Fatal(err)
	}
	if _, err := readLinuxAppManifest(bundle, "checkers"); err == nil {
		t.Fatal("Checkers accepted a Crown application bundle")
	}
}

func TestReadLinuxAppManifestAcceptsDaemonOnlyForDotTargets(t *testing.T) {
	for _, target := range []string{"biscuit", "radar"} {
		payload := linuxAppBundle(t, target, "tater-linux", nil)
		manifest, err := readLinuxAppManifest(writeLinuxAppBundle(t, payload), target)
		if err != nil {
			t.Fatalf("%s daemon-only bundle: %v", target, err)
		}
		if len(manifest.Files) != 1 {
			t.Fatalf("%s files = %v", target, manifest.Files)
		}
	}
}

func TestReadLinuxAppManifestRejectsRendererForDotTargets(t *testing.T) {
	payload := linuxAppBundle(t, "biscuit", "tater-linux", map[string]string{
		"tater-show": "\x7fELFrenderer",
	})
	_, err := readLinuxAppManifest(writeLinuxAppBundle(t, payload), "biscuit")
	if err == nil || !strings.Contains(err.Error(), "unexpected files") {
		t.Fatalf("unexpected renderer error = %v", err)
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

func TestLinuxAppBundleInstallsOptionalStreamingCamera(t *testing.T) {
	stateDir := t.TempDir()
	payload := linuxAppBundle(t, "crown", "tater-linux", map[string]string{
		"tater-camera": "\x7fELFcamera",
	})
	bundle := writeLinuxAppBundle(t, payload)
	manifest, err := readLinuxAppManifest(bundle, "crown")
	if err != nil {
		t.Fatal(err)
	}
	if err := installLinuxAppBundle(bundle, stateDir, manifest); err != nil {
		t.Fatal(err)
	}
	camera := filepath.Join(stateDir, "app", "slots", "a", "tater-camera")
	if err := verifyELF(camera); err != nil {
		t.Fatalf("installed camera helper: %v", err)
	}
	if info, err := os.Stat(camera); err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("camera helper executable mode: info=%v err=%v", info, err)
	}
}

func TestLinuxAppBundleInstallsOptionalVideoDecoder(t *testing.T) {
	stateDir := t.TempDir()
	payload := linuxAppBundle(t, "athena", "tater-linux", map[string]string{
		"tater-ffmpeg":        "\x7fELFdecoder",
		"FFMPEG-LGPL-2.1.txt": "LGPL license",
	})
	bundle := writeLinuxAppBundle(t, payload)
	manifest, err := readLinuxAppManifest(bundle, "athena")
	if err != nil {
		t.Fatal(err)
	}
	if err := installLinuxAppBundle(bundle, stateDir, manifest); err != nil {
		t.Fatal(err)
	}
	decoder := filepath.Join(stateDir, "app", "slots", "a", "tater-ffmpeg")
	if err := verifyELF(decoder); err != nil {
		t.Fatalf("installed video decoder: %v", err)
	}
	if info, err := os.Stat(decoder); err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("video decoder executable mode: info=%v err=%v", info, err)
	}
	slot := filepath.Dir(decoder)
	if info, err := os.Stat(slot); err != nil || info.Mode().Perm() != linuxAppDirectoryMode {
		t.Fatalf("video decoder slot mode: info=%v err=%v", info, err)
	}
	license := filepath.Join(stateDir, "app", "slots", "a", "FFMPEG-LGPL-2.1.txt")
	if info, err := os.Stat(license); err != nil || info.Mode()&0o111 != 0 {
		t.Fatalf("video decoder license mode: info=%v err=%v", info, err)
	}
}

func TestLinuxAppBundleRejectsVideoDecoderWithoutLicense(t *testing.T) {
	payload := linuxAppBundle(t, "checkers", "tater-linux", map[string]string{
		"tater-ffmpeg": "\x7fELFdecoder",
	})
	_, err := readLinuxAppManifest(writeLinuxAppBundle(t, payload), "checkers")
	if err == nil || !strings.Contains(err.Error(), "decoder and its license together") {
		t.Fatalf("unlicensed decoder error = %v", err)
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
	for _, name := range []string{"tater-echo", "tater-show"} {
		if _, err := os.Stat(filepath.Join(stateDir, "app", "slots", "a", name)); err != nil {
			t.Fatalf("slot file %s: %v", name, err)
		}
	}
	pending, err := os.ReadFile(filepath.Join(stateDir, "app", "pending.env"))
	if err != nil || !strings.Contains(string(pending), "previous=system\nnew=a") {
		t.Fatalf("pending = %q, %v", pending, err)
	}
}

func TestInstallLinuxAppBundleContinuesPendingTrialImmediately(t *testing.T) {
	stateDir := t.TempDir()
	payload := linuxAppBundleWithVersionAndExtras(t, "checkers", "tater-linux", "v2.0.0-test", nil, nil)
	bundle := writeLinuxAppBundle(t, payload)
	manifest, err := readLinuxAppManifest(bundle, "checkers")
	if err != nil {
		t.Fatal(err)
	}
	if err := installLinuxAppBundle(bundle, stateDir, manifest); err != nil {
		t.Fatal(err)
	}
	pendingPath := filepath.Join(stateDir, "app", "pending.env")
	pendingBefore, err := os.ReadFile(pendingPath)
	if err != nil {
		t.Fatal(err)
	}
	completedPayload := linuxAppBundleWithVersionAndExtras(t, "checkers", "tater-linux", "v2.0.0-test",
		map[string]string{"tater-camera": "\x7fELFcamera"}, map[string]string{"tater-camera": "\x7fELFcamera"})
	completedBundle := writeLinuxAppBundle(t, completedPayload)
	completedManifest, err := readLinuxAppManifest(completedBundle, "checkers")
	if err != nil {
		t.Fatal(err)
	}
	if err := installLinuxAppBundle(completedBundle, stateDir, completedManifest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "app", "slots", "a", "tater-camera")); err != nil {
		t.Fatalf("continued trial camera: %v", err)
	}
	pendingAfter, err := os.ReadFile(pendingPath)
	if err != nil || string(pendingAfter) != string(pendingBefore) {
		t.Fatalf("pending changed from %q to %q: %v", pendingBefore, pendingAfter, err)
	}
	link, err := os.Readlink(filepath.Join(stateDir, "app", "current"))
	if err != nil || link != filepath.Join("slots", "a") {
		t.Fatalf("continued current = %q, %v", link, err)
	}
}

func TestInstallLinuxAppBundleRejectsDifferentPendingVersion(t *testing.T) {
	stateDir := t.TempDir()
	firstBundle := writeLinuxAppBundle(t, linuxAppBundleWithVersionAndExtras(t, "checkers", "tater-linux", "v2.0.0-test", nil, nil))
	firstManifest, err := readLinuxAppManifest(firstBundle, "checkers")
	if err != nil {
		t.Fatal(err)
	}
	if err := installLinuxAppBundle(firstBundle, stateDir, firstManifest); err != nil {
		t.Fatal(err)
	}
	secondBundle := writeLinuxAppBundle(t, linuxAppBundleWithVersionAndExtras(t, "checkers", "tater-linux", "v2.0.1-test", nil, nil))
	secondManifest, err := readLinuxAppManifest(secondBundle, "checkers")
	if err != nil {
		t.Fatal(err)
	}
	if err := installLinuxAppBundle(secondBundle, stateDir, secondManifest); err == nil || !strings.Contains(err.Error(), "different Linux application trial") {
		t.Fatalf("pending trial version error = %v", err)
	}
}

func TestInstallLinuxAppBundleRejectsMismatchedPendingSlot(t *testing.T) {
	stateDir := t.TempDir()
	appDir := filepath.Join(stateDir, "app")
	if err := os.MkdirAll(filepath.Join(appDir, "slots", "a"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "pending.env"), []byte("version=v2.0.0-test\nprevious=system\nnew=a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("slots", "b"), filepath.Join(appDir, "current")); err != nil {
		t.Fatal(err)
	}
	bundle := writeLinuxAppBundle(t, linuxAppBundleWithVersionAndExtras(t, "checkers", "tater-linux", "v2.0.0-test", nil, nil))
	manifest, err := readLinuxAppManifest(bundle, "checkers")
	if err != nil {
		t.Fatal(err)
	}
	if err := installLinuxAppBundle(bundle, stateDir, manifest); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("pending trial slot error = %v", err)
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
