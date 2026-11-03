package privacy

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

const (
	cronosPrivacyState  = "/sys/devices/platform/gpio-privacy/state"
	cronosPrivacyEnable = "/sys/devices/platform/gpio-privacy/enable"
	cronosPrivacyPoll   = 100 * time.Millisecond
)

// CronosController follows the Show 5 second generation's hardware privacy
// latch. The latch can be engaged from software, but only the physical button
// may release it; the red indicator follows the latch in hardware.
type CronosController struct {
	statePath  string
	enablePath string
}

func OpenCronos() (*CronosController, error) {
	if _, err := readState(cronosPrivacyState); err != nil {
		return nil, err
	}
	return &CronosController{statePath: cronosPrivacyState, enablePath: cronosPrivacyEnable}, nil
}

func (c *CronosController) State() (bool, error) {
	if c == nil || strings.TrimSpace(c.statePath) == "" {
		return false, os.ErrNotExist
	}
	return readState(c.statePath)
}

func (c *CronosController) Engage() error {
	if c == nil || strings.TrimSpace(c.enablePath) == "" {
		return os.ErrNotExist
	}
	if err := os.WriteFile(c.enablePath, []byte("1\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", c.enablePath, err)
	}
	return nil
}

func (c *CronosController) Watch(ctx context.Context, initial bool, callback func(bool) error) {
	ticker := time.NewTicker(cronosPrivacyPoll)
	defer ticker.Stop()
	last := initial
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			muted, err := c.State()
			if err != nil {
				log.Printf("[privacy] Cronos state unavailable: %v", err)
				continue
			}
			if muted == last {
				continue
			}
			if callback != nil {
				if err := callback(muted); err != nil {
					log.Printf("[privacy] Cronos mute synchronization deferred: %v", err)
					continue
				}
			}
			last = muted
		}
	}
}
