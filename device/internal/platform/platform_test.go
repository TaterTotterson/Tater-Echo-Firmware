package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// mkroot builds a fake filesystem root. Files are given as path → contents;
// an empty content string makes a directory instead, which is what
// /dev/__properties__ is on a real device.
func mkroot(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, content := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if content == "" {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const taterLinuxRelease = `NAME="Tater Linux"
ID=tater-linux
PRETTY_NAME="Tater Linux 2.0.0"
VERSION_ID="2.0.0"
`

func TestDetect(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name:  "Tater Linux: full Checkers rootfs",
			files: map[string]string{"etc/os-release": taterLinuxRelease},
			want:  TaterLinux,
		},
		{
			name:  "FireOS: Android's property service",
			files: map[string]string{"dev/__properties__": ""},
			want:  FireOS,
		},
		{
			// Neither marker. Must NOT guess: a wrong answer here sends a
			// device payloads it cannot use.
			name:  "neither marker present",
			files: map[string]string{},
			want:  Unknown,
		},
		{
			// Some other distribution's os-release must not read as Tater Linux.
			name:  "os-release from something else",
			files: map[string]string{"etc/os-release": "NAME=\"Debian\"\nID=debian\n"},
			want:  Unknown,
		},
		{
			name: "Tater Linux wins over a stale Android property marker",
			files: map[string]string{
				"etc/os-release":     taterLinuxRelease,
				"dev/__properties__": "",
			},
			want: TaterLinux,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Detect(mkroot(t, tc.files)); got != tc.want {
				t.Errorf("Detect() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReleaseReadsOnlyTaterLinuxVersion(t *testing.T) {
	root := mkroot(t, map[string]string{"etc/os-release": taterLinuxRelease})
	if got := Release(root); got != "2.0.0" {
		t.Fatalf("Release() = %q, want 2.0.0", got)
	}
	other := mkroot(t, map[string]string{
		"etc/os-release": "ID=debian\nVERSION_ID=12\n",
	})
	if got := Release(other); got != "" {
		t.Fatalf("Release() for non-Tater Linux = %q, want empty", got)
	}
}

// Base caches, and the value must be stable for the life of the process: a
// field that flapped would read as a device changing platform underneath the
// controller.
func TestBaseIsStable(t *testing.T) {
	first := Base()
	for i := 0; i < 3; i++ {
		if got := Base(); got != first {
			t.Fatalf("Base() returned %q then %q", first, got)
		}
	}
	switch first {
	case TaterLinux, FireOS, Unknown:
	default:
		t.Errorf("Base() = %q, not one of the three defined answers", first)
	}
}

func TestKernelReportsTheHost(t *testing.T) {
	m, r := Kernel()
	if runtime.GOOS == "linux" && (m == "" || r == "") {
		t.Fatalf("Kernel() = %q, %q on linux; want both set", m, r)
	}
	if strings.ContainsRune(m+r, 0) {
		t.Fatalf("Kernel() leaked a NUL: %q %q", m, r)
	}
}
