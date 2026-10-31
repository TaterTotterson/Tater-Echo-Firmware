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
	"strings"
	"testing"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/platform"
)

func linuxSystemBundle(t *testing.T, target, version string, rootfs []byte, declared []byte) []byte {
	t.Helper()
	if declared == nil {
		declared = rootfs
	}
	digest := sha256.Sum256(declared)
	manifest := linuxSystemManifest{
		Schema: 1, Kind: "tater-linux-rootfs", Target: target,
		BaseOS: platform.TaterLinux, Version: version,
		RootFS: linuxAppFile{SHA256: hex.EncodeToString(digest[:]), Size: int64(len(declared))},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var result bytes.Buffer
	zipped := gzip.NewWriter(&result)
	archive := tar.NewWriter(zipped)
	for _, entry := range []struct {
		name string
		data []byte
	}{{"manifest.json", manifestBytes}, {"rootfs.tar.gz", rootfs}} {
		if err := archive.WriteHeader(&tar.Header{
			Name: entry.name, Mode: 0o644, Size: int64(len(entry.data)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write(entry.data); err != nil {
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

func writeSystemBundle(t *testing.T, payload []byte) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "system.tar.gz")
	if err := os.WriteFile(filename, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	return filename
}

func TestExtractVerifiedSystemRootFS(t *testing.T) {
	rootfs := []byte("root filesystem")
	destination := filepath.Join(t.TempDir(), "rootfs.tar.gz")
	manifest, err := extractVerifiedSystemRootFS(
		writeSystemBundle(t, linuxSystemBundle(t, "radar", "v2.0.0", rootfs, nil)),
		destination, "radar",
	)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "v2.0.0" {
		t.Fatalf("version = %q", manifest.Version)
	}
	installed, err := os.ReadFile(destination)
	if err != nil || !bytes.Equal(installed, rootfs) {
		t.Fatalf("rootfs = %q, err = %v", installed, err)
	}
}

func TestExtractVerifiedSystemRootFSRejectsWrongTarget(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "rootfs.tar.gz")
	_, err := extractVerifiedSystemRootFS(
		writeSystemBundle(t, linuxSystemBundle(t, "biscuit", "v2.0.0", []byte("rootfs"), nil)),
		destination, "radar",
	)
	if err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("wrong target error = %v", err)
	}
}

func TestExtractVerifiedSystemRootFSRejectsInnerDigestMismatch(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "rootfs.tar.gz")
	_, err := extractVerifiedSystemRootFS(
		writeSystemBundle(t, linuxSystemBundle(
			t, "checkers", "v2.0.0", []byte("tampered"), []byte("declared"),
		)),
		destination, "checkers",
	)
	if err == nil || !strings.Contains(err.Error(), "verification failed") {
		t.Fatalf("digest mismatch error = %v", err)
	}
}

func TestSystemOTAStagesRootFSAndRestarts(t *testing.T) {
	payload := linuxSystemBundle(t, "crown", "v2.0.0", []byte("rootfs contents"), nil)
	hash := sha256.Sum256(payload)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Write(payload)
	}))
	defer server.Close()
	var staged []byte
	restarted := make(chan struct{}, 1)
	installer := &OTAInstaller{
		HTTP: server.Client(), Target: "crown", StateDir: t.TempDir(),
		BaseOS: func() string { return platform.TaterLinux },
		InstallRootFS: func(_ context.Context, filename string) error {
			var err error
			staged, err = os.ReadFile(filename)
			return err
		},
		Restart: func() { restarted <- struct{}{} },
	}
	err := installer.InstallSystem(context.Background(), OTARequest{
		URL: server.URL, SHA256: hex.EncodeToString(hash[:]), SizeBytes: int64(len(payload)),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(staged) != "rootfs contents" {
		t.Fatalf("staged rootfs = %q", staged)
	}
	select {
	case <-restarted:
	case <-time.After(2 * time.Second):
		t.Fatal("System OTA did not request a restart")
	}
}
