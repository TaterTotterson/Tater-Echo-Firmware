package recovery

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseUptime(t *testing.T) {
	got, err := ParseUptime("12.34 99.99\n")
	if err != nil || got != 12340*time.Millisecond {
		t.Fatalf("uptime=%v err=%v", got, err)
	}
	if _, err := ParseUptime("not-a-number"); err == nil {
		t.Fatal("expected invalid uptime error")
	}
}

func TestStartupGestureWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uptime")
	if err := os.WriteFile(path, []byte("89.9 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	inside, err := InStartupGestureWindow(path)
	if err != nil || !inside {
		t.Fatalf("inside=%v err=%v", inside, err)
	}
	if err := os.WriteFile(path, []byte("90.1 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	inside, err = InStartupGestureWindow(path)
	if err != nil || inside {
		t.Fatalf("inside=%v err=%v", inside, err)
	}
}

func TestScreenTargetsDoNotRetainLinuxSideRecoveryGesture(t *testing.T) {
	for _, target := range []string{"checkers", "crown", "athena", "rook"} {
		if _, err := StartupTWRPCommand(target); err == nil {
			t.Fatalf("%s unexpectedly retained a Linux-side recovery gesture", target)
		}
	}
	if _, err := StartupTWRPCommand("unknown"); err == nil {
		t.Fatal("expected unsupported target error")
	}
}

func TestStartupTWRPCommandForDotTargetsUsesSafeHandoff(t *testing.T) {
	for _, target := range []string{"biscuit", "radar"} {
		command, err := StartupTWRPCommand(target)
		if err != nil || command.Path != "/usr/local/sbin/to-twrp" ||
			len(command.Args) != 1 || command.Args[0] != "--handoff" {
			t.Fatalf("%s command=%+v err=%v", target, command, err)
		}
	}
}
