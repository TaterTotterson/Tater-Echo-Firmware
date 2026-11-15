package taternative

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/platform"
)

func writeOTAPlatform(t *testing.T, target string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ota-platform")
	if err := os.WriteFile(path, []byte(linuxOTAPlatform(target)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOTAPlatformPolicyRejectsLegacyAndCrossTargetPackages(t *testing.T) {
	for _, target := range []string{"biscuit", "radar", "checkers", "cronos", "crown", "athena", "rook"} {
		if err := validateLinuxOTAPolicy(2, linuxOTAPlatform(target), target); err != nil {
			t.Fatal(err)
		}
		for _, policy := range []struct {
			schema  int
			profile string
		}{
			{1, linuxOTAPlatform(target)}, {2, ""}, {2, "echo-hardware-v1/other"}, {3, linuxOTAPlatform(target)},
		} {
			if err := validateLinuxOTAPolicy(policy.schema, policy.profile, target); err == nil {
				t.Fatal("unsafe policy accepted")
			}
		}
		for _, system := range []bool{false, true} {
			staged := false
			installer := &OTAInstaller{Target: target, PlatformFile: filepath.Join(t.TempDir(), "missing"),
				BaseOS:        func() string { return platform.TaterLinux },
				InstallRootFS: func(context.Context, string) error { staged = true; return nil }}
			req := OTARequest{URL: "https://example.invalid/must-not-download", SHA256: strings.Repeat("a", 64)}
			var err error
			if system {
				err = installer.InstallSystem(context.Background(), req, nil)
			} else {
				err = installer.Install(context.Background(), req, nil)
			}
			if err == nil || !strings.Contains(err.Error(), "USB upgrade") || staged {
				t.Fatalf("system=%v err=%v staged=%v", system, err, staged)
			}
		}
	}
}

func TestInstalledOTAPlatformIsTargetSpecific(t *testing.T) {
	path := writeOTAPlatform(t, "rook")
	if installedLinuxOTAPlatform(path, "rook") == "" || installedLinuxOTAPlatform(path, "radar") != "" {
		t.Fatal("incorrect platform identity")
	}
}
