package microwakeword

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManifestFilenameRejectsPaths(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"hey_tater", "hey_tater.json"},
		{"hey_tater.json", "hey_tater.json"},
	} {
		got, err := ManifestFilename(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("ManifestFilename(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []string{"", "../hey_tater", "models/hey_tater", `models\\hey_tater`, "hey_tater.tflite"} {
		if _, err := ManifestFilename(bad); err == nil {
			t.Errorf("ManifestFilename(%q) unexpectedly succeeded", bad)
		}
	}
}

func TestOpenShadowNamesMissingManifest(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TATER_MWW_DIR", dir)
	_, err := OpenShadow("hey_tater", 0, nil)
	if err == nil || !strings.Contains(err.Error(), filepath.Join(dir, "hey_tater.json")) {
		t.Fatalf("OpenShadow error = %v, want missing manifest path", err)
	}
}

func TestOpenShadowRejectsOversizedManifest(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TATER_MWW_DIR", dir)
	path := filepath.Join(dir, "hey_tater.json")
	if err := os.WriteFile(path, make([]byte, maxManifestBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := OpenShadow("hey_tater", 0, nil)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("OpenShadow error = %v, want size limit", err)
	}
}

func TestOpenShadowRejectsOversizedModelBeforeLoadingRuntime(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TATER_MWW_DIR", dir)
	manifest, err := os.ReadFile(filepath.Join("models", "hey_tater.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hey_tater.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hey_tater.tflite"),
		make([]byte, maxModelBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = OpenShadow("hey_tater", 0, nil)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("OpenShadow error = %v, want model size limit", err)
	}
}

func TestShippedHeyTaterManifestMatchesRuntimeContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("models", "hey_tater.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := ParseManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	settings := manifest.RuntimeConfig()
	if settings.WakeWord != "hey_tater" || settings.Threshold != 0.98 ||
		settings.SlidingWindow != 5 || settings.CloseMissThreshold != 0.81 {
		t.Fatalf("unexpected shipped settings: %+v", settings)
	}
}
