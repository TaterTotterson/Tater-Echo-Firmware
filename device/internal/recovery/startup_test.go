package recovery

import (
	"os"
	"path/filepath"
	"strings"
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

func TestStartupRecoveryCommandForScreenTargets(t *testing.T) {
	for _, target := range []string{"checkers", "crown", "athena", "rook"} {
		command, err := StartupRecoveryCommand(target)
		if err != nil || command.Path != "/bin/sh" || len(command.Args) != 2 ||
			command.Args[0] != "-c" ||
			command.Args[1] != "/usr/local/sbin/slotctl rescue && exec /usr/local/bin/tater-reboot-now" {
			t.Fatalf("%s command=%+v err=%v", target, command, err)
		}
	}
	if _, err := StartupRecoveryCommand("unknown"); err == nil {
		t.Fatal("expected unsupported target error")
	}
}

func TestStartupRecoveryCommandForDotTargetsUsesSafeTWRPHandoff(t *testing.T) {
	for _, target := range []string{"biscuit", "radar"} {
		command, err := StartupRecoveryCommand(target)
		if err != nil || command.Path != "/bin/sh" || len(command.Args) != 2 ||
			command.Args[0] != "-c" {
			t.Fatalf("%s command=%+v err=%v", target, command, err)
		}
		if !strings.Contains(command.Args[1], "/store/techo5/twrp-handoff.img") ||
			!strings.Contains(command.Args[1], "/usr/local/sbin/to-twrp --handoff") ||
			!strings.Contains(command.Args[1], "/usr/local/sbin/slotctl rescue") {
			t.Fatalf("%s command lacks handoff or safe fallback: %q", target, command.Args[1])
		}
	}
}
