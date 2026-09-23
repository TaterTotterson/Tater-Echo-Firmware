package taternative

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResetToSetupClearsProvisioningAndRestarts(t *testing.T) {
	dir := t.TempDir()
	restarted := make(chan struct{}, 1)
	opts := SetupResetOptions{
		WiFiPath:      filepath.Join(dir, "emos", "wpa.conf"),
		BootstrapPath: filepath.Join(dir, "tater", "native.json"),
		TokenPath:     filepath.Join(dir, "tater", "device_token"),
		MarkerPath:    filepath.Join(dir, "tater", "setup_enabled"),
		RestartDelay:  time.Nanosecond,
		Restart:       func() { restarted <- struct{}{} },
	}
	for path, data := range map[string]string{
		opts.WiFiPath:      "network={}\n",
		opts.BootstrapPath: `{"url":"ws://tater.test"}`,
		opts.TokenPath:     "paired-token\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := ResetToSetup(opts); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{opts.WiFiPath, opts.BootstrapPath, opts.TokenPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("provisioning file still exists: %s (%v)", path, err)
		}
	}
	marker, err := os.ReadFile(opts.MarkerPath)
	if err != nil || string(marker) != "1\n" {
		t.Fatalf("setup marker = %q, err=%v", marker, err)
	}
	select {
	case <-restarted:
	case <-time.After(time.Second):
		t.Fatal("setup reset did not request restart")
	}
}
