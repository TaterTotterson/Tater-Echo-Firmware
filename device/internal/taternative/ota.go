package taternative

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/platform"
)

const maxFirmwareBytes = 128 * 1024 * 1024

// OTAInstaller installs a downloaded firmware binary into the inactive Echo
// A/B slot and atomically flips the server symlink. The existing supervisor
// performs its normal fast-exit rollback if the new slot cannot start.
type OTAInstaller struct {
	HTTP          *http.Client
	Restart       func()
	Target        string
	StateDir      string
	BaseOS        func() string
	InstallRootFS func(context.Context, string) error
	mu            sync.Mutex
}

// NewOTAInstallerForTarget selects the transaction appropriate to the target.
// Every current target uses application slots on Tater Linux. A pre-Linux
// Biscuit or Radar must use the USB factory installer once for conversion.
func NewOTAInstallerForTarget(target string) *OTAInstaller {
	return &OTAInstaller{
		HTTP:   &http.Client{Timeout: 5 * time.Minute},
		Target: strings.ToLower(strings.TrimSpace(target)),
	}
}

func validateOTARequest(req OTARequest) error {
	if req.URL == "" {
		return errors.New("OTA URL is required")
	}
	if len(req.SHA256) != 64 {
		return errors.New("OTA SHA-256 is required")
	}
	if _, err := hex.DecodeString(req.SHA256); err != nil {
		return errors.New("OTA SHA-256 is invalid")
	}
	return nil
}

func (i *OTAInstaller) Install(ctx context.Context, req OTARequest, report func(string, int, string)) error {
	if err := validateOTARequest(req); err != nil {
		return err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if !isLinuxFirmwareTarget(i.Target) {
		return fmt.Errorf("unsupported Tater Linux OTA target %q", i.Target)
	}
	if i.baseOS() != platform.TaterLinux {
		return fmt.Errorf("%s Linux firmware requires Tater Linux; use the USB factory installer for conversion", i.Target)
	}
	return i.installLinuxApp(ctx, req, report)
}

func isLinuxFirmwareTarget(target string) bool {
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "biscuit", "radar", "checkers", "crown", "athena", "rook":
		return true
	default:
		return false
	}
}

func (i *OTAInstaller) baseOS() string {
	if i.BaseOS != nil {
		return i.BaseOS()
	}
	return platform.Base()
}
