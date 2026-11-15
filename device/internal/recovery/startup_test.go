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

func TestBiscuitRadarRetainLateSlotRescueForLegacyBootImages(t *testing.T) {
	for _, target := range []string{"biscuit", "radar"} {
		commands, err := StartupRecoveryCommands(target)
		if err != nil || len(commands) != 2 {
			t.Fatalf("%s commands=%+v err=%v", target, commands, err)
		}
		if commands[0].Path != "/usr/local/sbin/slotctl" ||
			len(commands[0].Args) != 1 || commands[0].Args[0] != "rescue" {
			t.Fatalf("%s prepare=%+v", target, commands[0])
		}
		if commands[1].Path != "/usr/local/bin/tater-reboot-now" || len(commands[1].Args) != 0 {
			t.Fatalf("%s reboot=%+v", target, commands[1])
		}
	}
}

func TestScreenStartupRecoveryUsesNativeTWRPWithoutArmingSlotRescue(t *testing.T) {
	for _, target := range []string{"checkers", "cronos", "crown", "athena", "rook", " Rook "} {
		commands, err := StartupRecoveryCommands(target)
		if err != nil || len(commands) != 1 || commands[0].Path != "/usr/local/bin/rebootto" ||
			len(commands[0].Args) != 1 || commands[0].Args[0] != "recovery" {
			t.Fatalf("%s commands=%+v err=%v", target, commands, err)
		}
	}
}

func TestUnsupportedStartupRecoveryTargetIsRefused(t *testing.T) {
	if _, err := StartupRecoveryCommands("unknown"); err == nil {
		t.Fatal("expected unsupported target error")
	}
}
