package privacy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCronosControllerReadsLatchAndEngagesThroughEnable(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	enable := filepath.Join(dir, "enable")
	if err := os.WriteFile(state, []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(enable, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	controller := &CronosController{statePath: state, enablePath: enable}
	if muted, err := controller.State(); err != nil || muted {
		t.Fatalf("initial state = %v, %v", muted, err)
	}
	if err := controller.Engage(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(enable); err != nil || string(got) != "1\n" {
		t.Fatalf("enable = %q, %v", got, err)
	}
	if err := os.WriteFile(state, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if muted, err := controller.State(); err != nil || !muted {
		t.Fatalf("engaged state = %v, %v", muted, err)
	}
}
