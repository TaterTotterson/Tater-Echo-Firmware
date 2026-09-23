package taternative

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// SetupResetOptions makes the physical reset path testable without touching
// the host's /data tree or signalling its PID 1.
type SetupResetOptions struct {
	WiFiPath      string
	BootstrapPath string
	TokenPath     string
	MarkerPath    string
	Restart       func()
	RestartDelay  time.Duration
}

func (o SetupResetOptions) defaults() SetupResetOptions {
	if o.WiFiPath == "" {
		o.WiFiPath = DefaultEmOSWiFiPath
	}
	if o.BootstrapPath == "" {
		o.BootstrapPath = DefaultBootstrapPath
	}
	if o.TokenPath == "" {
		o.TokenPath = DefaultDeviceToken
	}
	if o.MarkerPath == "" {
		o.MarkerPath = DefaultSetupMarker
	}
	if o.RestartDelay <= 0 {
		o.RestartDelay = 500 * time.Millisecond
	}
	if o.Restart == nil {
		o.Restart = func() { _ = syscall.Kill(1, syscall.SIGTERM) }
	}
	return o
}

// ResetToSetup clears private Wi-Fi and Tater pairing state, preserves the
// firmware itself, and asks emOS to perform its normal synced reboot. The
// setup marker is written first so even a partial removal cannot boot into an
// unrecoverable state; emOS will start Tater-Setup-XXXX whenever either half
// of provisioning is missing.
func ResetToSetup(options SetupResetOptions) error {
	opts := options.defaults()
	if err := writeSetupFile(opts.MarkerPath, []byte("1\n"), 0o600); err != nil {
		return fmt.Errorf("enable setup mode: %w", err)
	}
	// Remove native.json first. Once that succeeds, emOS is guaranteed to
	// choose setup on the next boot even if clearing a later file reports an
	// unexpected filesystem error.
	for _, path := range []string{opts.BootstrapPath, opts.WiFiPath, opts.TokenPath} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("clear %s: %w", filepath.Base(path), err)
		}
	}
	syscall.Sync()
	go func() {
		time.Sleep(opts.RestartDelay)
		opts.Restart()
	}()
	return nil
}
