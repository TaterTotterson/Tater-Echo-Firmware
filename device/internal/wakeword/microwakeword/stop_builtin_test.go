package microwakeword

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureBuiltinTimerStopPackage(t *testing.T) {
	checkedInManifest, err := os.ReadFile(filepath.Join("models", "stop.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(checkedInManifest) != timerStopManifestJSON {
		t.Fatal("generated timer-stop manifest does not match models/stop.json")
	}
	dir := t.TempDir()
	t.Setenv("TATER_MWW_DIR", dir)
	if err := EnsureBuiltinTimerStopPackage(); err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadPackageManifest(TimerStopPackage)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.WakeWord != "Stop" || manifest.Model != "stop.tflite" {
		t.Fatalf("timer-stop manifest = %#v", manifest)
	}
	valid, err := fileHasSHA256(filepath.Join(dir, "stop.tflite"), timerStopModelSHA)
	if err != nil || !valid {
		t.Fatalf("timer-stop model valid=%v err=%v", valid, err)
	}

	// A damaged package is repaired on the next alarm without requiring a
	// factory reinstall.
	if err := os.WriteFile(filepath.Join(dir, "stop.tflite"), []byte("damaged"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureBuiltinTimerStopPackage(); err != nil {
		t.Fatal(err)
	}
	valid, err = fileHasSHA256(filepath.Join(dir, "stop.tflite"), timerStopModelSHA)
	if err != nil || !valid {
		t.Fatalf("repaired timer-stop model valid=%v err=%v", valid, err)
	}
}
