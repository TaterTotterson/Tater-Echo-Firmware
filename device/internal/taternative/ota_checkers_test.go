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

func testCheckersModule(t *testing.T, version, marker string) []byte {
	t.Helper()
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	for _, file := range checkersModuleFiles {
		entry, err := archive.Create(file.Path)
		if err != nil {
			t.Fatal(err)
		}
		contents := []byte(marker + "-" + file.Path)
		if file.Path == "module.prop" {
			contents = []byte("id=tater_checkers\nversion=" + strings.TrimPrefix(version, "v") + "\n")
		}
		_, _ = entry.Write(contents)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func testAPKWithModule(t *testing.T, marker string, module []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	for name, data := range map[string][]byte{
		"AndroidManifest.xml": []byte("manifest-" + marker),
		"classes.dex":         []byte(marker),
		checkersModuleAsset:   module,
	} {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = entry.Write(data)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func testCheckersBundle(t *testing.T, version string, server, apk, module []byte) []byte {
	t.Helper()
	digest := func(data []byte) string {
		hash := sha256.Sum256(data)
		return hex.EncodeToString(hash[:])
	}
	manifest, err := json.Marshal(checkersBundleManifest{
		Schema: 1, Target: "checkers", Version: version,
		Files: map[string]checkersBundleFile{
			"server":        {Path: "server", SHA256: digest(server), Size: int64(len(server))},
			"screen_apk":    {Path: "screen.apk", SHA256: digest(apk), Size: int64(len(apk))},
			"magisk_module": {Path: "module.zip", SHA256: digest(module), Size: int64(len(module))},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	for name, data := range map[string][]byte{
		"manifest.json": manifest, "module.zip": module, "server": server, "screen.apk": apk,
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
	newModule := testCheckersModule(t, "v0.2.0", "new")
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
	bundle := testCheckersBundle(t, "v0.2.0", newServer, newAPK, newModule)
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
	installer.CheckersModuleDir = filepath.Join(dir, "modules", "tater_checkers")
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
	if got, _ := os.ReadFile(filepath.Join(stateDir, "module.zip")); !bytes.Equal(got, newModule) {
		t.Fatal("new Checkers boot module was not staged")
	}
	pending, _ := os.ReadFile(filepath.Join(stateDir, "pending.env"))
	if !strings.Contains(string(pending), "version=v0.2.0") ||
		!strings.Contains(string(pending), "previous_slot=server_a") ||
		!strings.Contains(string(pending), "new_slot=server_b") ||
		!strings.Contains(string(pending), "staged_module="+filepath.Join(stateDir, "module.zip")) {
		t.Fatalf("pending state = %q", pending)
	}
	commandMu.Lock()
	joined := strings.Join(commands, "\n")
	commandMu.Unlock()
	if !strings.Contains(joined, checkersAndroidShell+" "+checkersPackageManager+
		" install -r -d -g "+filepath.Join(stateDir, "screen.apk")) {
		t.Fatalf("APK install commands = %q", joined)
	}
	select {
	case <-restarted:
	case <-time.After(2 * time.Second):
		t.Fatal("coordinated update did not request restart")
	}
}

func TestCheckersPackageManagerWrapperAlwaysRunsThroughAndroidShell(t *testing.T) {
	installer := NewOTAInstallerForTarget("checkers")
	var commands []string
	installer.RunCommand = func(_ context.Context, name string, args ...string) ([]byte, error) {
		commands = append(commands, name+" "+strings.Join(args, " "))
		if len(args) >= 2 && args[1] == "path" {
			return []byte("package:/data/app/com.tatertotterson.show/base.apk\n"), nil
		}
		return []byte("Success\n"), nil
	}
	path, err := installer.currentAPK(context.Background(), checkersPackage)
	if err != nil || path != "/data/app/com.tatertotterson.show/base.apk" {
		t.Fatalf("current APK = %q, %v", path, err)
	}
	installer.restoreCheckersAPK(context.Background(), "/data/local/etc/tater/ota/rollback.apk")

	want := []string{
		checkersAndroidShell + " " + checkersPackageManager + " path " + checkersPackage,
		checkersAndroidShell + " " + checkersPackageManager +
			" install -r -d -g /data/local/etc/tater/ota/rollback.apk",
	}
	if strings.Join(commands, "\n") != strings.Join(want, "\n") {
		t.Fatalf("package manager commands = %q, want %q", commands, want)
	}
	for _, command := range commands {
		if strings.HasPrefix(command, checkersPackageManager+" ") || strings.HasPrefix(command, "pm ") {
			t.Fatalf("package manager executed directly: %q", command)
		}
	}
}

func TestCheckersModuleArchiveRequiresExactFilesAndMatchingVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "module.zip")
	if err := os.WriteFile(path, testCheckersModule(t, "v0.2.3", "valid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyCheckersModuleArchive(path, "v0.2.3"); err != nil {
		t.Fatalf("valid module rejected: %v", err)
	}
	if err := verifyCheckersModuleArchive(path, "v0.2.4"); err == nil {
		t.Fatal("module from a different generation was accepted")
	}

	var incomplete bytes.Buffer
	archive := zip.NewWriter(&incomplete)
	entry, err := archive.Create("module.prop")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte("version=0.2.3\n"))
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, incomplete.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyCheckersModuleArchive(path, "v0.2.3"); err == nil {
		t.Fatal("incomplete module was accepted")
	}
}

func TestMarkCheckersOTAHealthyRequiresMatchingNativeAndAPKVersions(t *testing.T) {
	stateDir := t.TempDir()
	moduleDir := filepath.Join(t.TempDir(), "tater_checkers")
	t.Setenv("TATER_CHECKERS_OTA_STATE", stateDir)
	t.Setenv("TATER_CHECKERS_MODULE_DIR", moduleDir)
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "module.prop"), []byte("version=0.2.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "pending.env"), []byte("version=v0.2.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, reboot, err := MarkCheckersOTAHealthy("v0.2.0", "v0.1.9"); err != nil || ok || reboot {
		t.Fatalf("mismatched APK health = %v, reboot=%v, %v", ok, reboot, err)
	}
	if ok, reboot, err := MarkCheckersOTAHealthy("v0.2.0", "v0.2.0"); err != nil || !ok || reboot {
		t.Fatalf("matching generation health = %v, reboot=%v, %v", ok, reboot, err)
	}
	value, err := os.ReadFile(filepath.Join(stateDir, "healthy"))
	if err != nil || string(value) != "v0.2.0\n" {
		t.Fatalf("health marker = %q, %v", value, err)
	}
}

func TestMarkCheckersOTAHealthyInstallsStagedModuleAndRequestsReboot(t *testing.T) {
	stateDir := t.TempDir()
	moduleDir := filepath.Join(t.TempDir(), "tater_checkers")
	t.Setenv("TATER_CHECKERS_OTA_STATE", stateDir)
	t.Setenv("TATER_CHECKERS_MODULE_DIR", moduleDir)
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "module.prop"), []byte("version=0.2.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	module := testCheckersModule(t, "v0.2.3", "new")
	staged := filepath.Join(stateDir, checkersModuleStageName)
	if err := os.WriteFile(staged, module, 0o600); err != nil {
		t.Fatal(err)
	}
	rollback := filepath.Join(filepath.Dir(moduleDir), checkersModuleBackupName)
	pending := "version=v0.2.3\nstaged_module=" + staged + "\nrollback_module=" + rollback + "\n"
	if err := os.WriteFile(filepath.Join(stateDir, "pending.env"), []byte(pending), 0o600); err != nil {
		t.Fatal(err)
	}
	ok, reboot, err := MarkCheckersOTAHealthy("v0.2.3", "v0.2.3")
	if err != nil || ok || !reboot {
		t.Fatalf("module generation health = %v, reboot=%v, %v", ok, reboot, err)
	}
	if !checkersModuleVersionMatches(moduleDir, "v0.2.3") {
		t.Fatal("new Checkers boot module was not activated")
	}
	if !checkersModuleVersionMatches(rollback, "v0.2.0") {
		t.Fatal("old Checkers boot module was not preserved")
	}
	ok, reboot, err = MarkCheckersOTAHealthy("v0.2.3", "v0.2.3")
	if err != nil || !ok || reboot {
		t.Fatalf("post-reboot module health = %v, reboot=%v, %v", ok, reboot, err)
	}
}

func TestMarkCheckersOTAHealthyBootstrapsModuleFromSignedAPKAsset(t *testing.T) {
	stateDir := t.TempDir()
	moduleDir := filepath.Join(t.TempDir(), "tater_checkers")
	t.Setenv("TATER_CHECKERS_OTA_STATE", stateDir)
	t.Setenv("TATER_CHECKERS_MODULE_DIR", moduleDir)
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "module.prop"), []byte("version=0.2.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	module := testCheckersModule(t, "v0.2.3", "bootstrap")
	apkPath := filepath.Join(t.TempDir(), "screen.apk")
	if err := os.WriteFile(apkPath, testAPKWithModule(t, "new", module), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TATER_CHECKERS_APK_PATH", apkPath)
	// This is the pending format written by v0.2.2, before module-aware OTA.
	if err := os.WriteFile(filepath.Join(stateDir, "pending.env"), []byte("version=v0.2.3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ok, reboot, err := MarkCheckersOTAHealthy("v0.2.3", "v0.2.3")
	if err != nil || ok || !reboot {
		t.Fatalf("bootstrap generation health = %v, reboot=%v, %v", ok, reboot, err)
	}
	if !checkersModuleVersionMatches(moduleDir, "v0.2.3") {
		t.Fatal("signed APK module was not activated")
	}
	ok, reboot, err = MarkCheckersOTAHealthy("v0.2.3", "v0.2.3")
	if err != nil || !ok || reboot {
		t.Fatalf("post-reboot bootstrap health = %v, reboot=%v, %v", ok, reboot, err)
	}
}
