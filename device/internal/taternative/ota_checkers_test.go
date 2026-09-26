package taternative

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testAPK(t *testing.T, marker string) []byte {
	t.Helper()
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	entry, err := archive.Create("AndroidManifest.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte("manifest-" + marker))
	entry, _ = archive.Create("classes.dex")
	_, _ = entry.Write([]byte(marker))
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func testCheckersBundle(t *testing.T, version string, server, apk []byte) []byte {
	t.Helper()
	digest := func(data []byte) string {
		hash := sha256.Sum256(data)
		return hex.EncodeToString(hash[:])
	}
	manifest, err := json.Marshal(checkersBundleManifest{
		Schema: 1, Target: "checkers", Version: version,
		Files: map[string]checkersBundleFile{
			"server":     {Path: "server", SHA256: digest(server), Size: int64(len(server))},
			"screen_apk": {Path: "screen.apk", SHA256: digest(apk), Size: int64(len(apk))},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	for name, data := range map[string][]byte{
		"manifest.json": manifest, "server": server, "screen.apk": apk,
	} {
		entry, createErr := archive.Create(name)
		if createErr != nil {
			t.Fatal(createErr)
		}
		_, _ = entry.Write(data)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestCheckersOTAStagesNativeAndAPKAsOneRollbackGeneration(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	stateDir := filepath.Join(dir, "ota")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldServer := append([]byte("\x7fELF"), []byte("old-server")...)
	newServer := append([]byte("\x7fELF"), []byte("new-server")...)
	oldAPK := testAPK(t, "old")
	newAPK := testAPK(t, "new")
	if err := os.WriteFile(filepath.Join(binDir, "server_a"), oldServer, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("server_a", filepath.Join(binDir, "server")); err != nil {
		t.Fatal(err)
	}
	installedAPK := filepath.Join(dir, "installed.apk")
	if err := os.WriteFile(installedAPK, oldAPK, 0o600); err != nil {
		t.Fatal(err)
	}
	bundle := testCheckersBundle(t, "v0.2.0", newServer, newAPK)
	hash := sha256.Sum256(bundle)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bundle)
	}))
	defer httpServer.Close()

	var commandMu sync.Mutex
	var commands []string
	restarted := make(chan struct{}, 1)
	installer := NewOTAInstallerForTarget("checkers")
	installer.ActivePath = filepath.Join(binDir, "server")
	installer.StateDir = stateDir
	installer.InstalledAPKPath = func(context.Context, string) (string, error) { return installedAPK, nil }
	installer.RunCommand = func(_ context.Context, name string, args ...string) ([]byte, error) {
		commandMu.Lock()
		commands = append(commands, name+" "+strings.Join(args, " "))
		commandMu.Unlock()
		return []byte("Success"), nil
	}
	installer.Restart = func() { restarted <- struct{}{} }
	if err := installer.Install(context.Background(), OTARequest{
		URL: httpServer.URL, SHA256: hex.EncodeToString(hash[:]), SizeBytes: int64(len(bundle)),
	}, nil); err != nil {
		t.Fatal(err)
	}
	link, err := os.Readlink(filepath.Join(binDir, "server"))
	if err != nil || link != "server_b" {
		t.Fatalf("active slot = %q, err=%v", link, err)
	}
	if got, _ := os.ReadFile(filepath.Join(binDir, "server_b")); !bytes.Equal(got, newServer) {
		t.Fatalf("new native slot = %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(stateDir, "rollback.apk")); !bytes.Equal(got, oldAPK) {
		t.Fatal("installed APK was not preserved for rollback")
	}
	if got, _ := os.ReadFile(filepath.Join(stateDir, "screen.apk")); !bytes.Equal(got, newAPK) {
		t.Fatal("new APK was not staged")
	}
	pending, _ := os.ReadFile(filepath.Join(stateDir, "pending.env"))
	if !strings.Contains(string(pending), "version=v0.2.0") ||
		!strings.Contains(string(pending), "previous_slot=server_a") ||
		!strings.Contains(string(pending), "new_slot=server_b") {
		t.Fatalf("pending state = %q", pending)
	}
	commandMu.Lock()
	joined := strings.Join(commands, "\n")
	commandMu.Unlock()
	if !strings.Contains(joined, "pm install -r -d -g "+filepath.Join(stateDir, "screen.apk")) {
		t.Fatalf("APK install commands = %q", joined)
	}
	select {
	case <-restarted:
	case <-time.After(2 * time.Second):
		t.Fatal("coordinated update did not request restart")
	}
}

func TestMarkCheckersOTAHealthyRequiresMatchingNativeAndAPKVersions(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("TATER_CHECKERS_OTA_STATE", stateDir)
	if err := os.WriteFile(filepath.Join(stateDir, "pending.env"), []byte("version=v0.2.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, err := MarkCheckersOTAHealthy("v0.2.0", "v0.1.9"); err != nil || ok {
		t.Fatalf("mismatched APK health = %v, %v", ok, err)
	}
	if ok, err := MarkCheckersOTAHealthy("v0.2.0", "v0.2.0"); err != nil || !ok {
		t.Fatalf("matching generation health = %v, %v", ok, err)
	}
	value, err := os.ReadFile(filepath.Join(stateDir, "healthy"))
	if err != nil || string(value) != "v0.2.0\n" {
		t.Fatalf("health marker = %q, %v", value, err)
	}
}
