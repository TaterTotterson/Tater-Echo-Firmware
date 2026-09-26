package taternative

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxFirmwareBytes = 128 * 1024 * 1024

// OTAInstaller installs a downloaded firmware binary into the inactive Echo
// A/B slot and atomically flips the server symlink. The existing supervisor
// performs its normal fast-exit rollback if the new slot cannot start.
type OTAInstaller struct {
	ActivePath       string
	HTTP             *http.Client
	Restart          func()
	Target           string
	StateDir         string
	RunCommand       func(context.Context, string, ...string) ([]byte, error)
	InstalledAPKPath func(context.Context, string) (string, error)
}

func NewOTAInstaller() *OTAInstaller {
	return &OTAInstaller{
		ActivePath: "/data/local/bin/server",
		HTTP:       &http.Client{Timeout: 5 * time.Minute},
	}
}

// NewOTAInstallerForTarget selects the transaction appropriate to the target.
// Biscuit remains a single ELF A/B update; Checkers coordinates the native
// slot with its Android screen APK and leaves rollback state for the Magisk
// supervisor.
func NewOTAInstallerForTarget(target string) *OTAInstaller {
	installer := NewOTAInstaller()
	installer.Target = strings.ToLower(strings.TrimSpace(target))
	return installer
}

func (i *OTAInstaller) Install(ctx context.Context, req OTARequest, report func(string, int, string)) error {
	if req.URL == "" {
		return errors.New("OTA URL is required")
	}
	if len(req.SHA256) != 64 {
		return errors.New("OTA SHA-256 is required")
	}
	if _, err := hex.DecodeString(req.SHA256); err != nil {
		return errors.New("OTA SHA-256 is invalid")
	}
	if strings.EqualFold(i.Target, "checkers") {
		return i.installCheckers(ctx, req, report)
	}
	activePath := i.ActivePath
	if activePath == "" {
		activePath = "/data/local/bin/server"
	}
	link, err := os.Readlink(activePath)
	if err != nil {
		return fmt.Errorf("read active firmware slot: %w", err)
	}
	active := filepath.Base(link)
	var inactive string
	switch active {
	case "server_a":
		inactive = "server_b"
	case "server_b":
		inactive = "server_a"
	default:
		return fmt.Errorf("unknown active firmware slot %q", active)
	}
	directory := filepath.Dir(activePath)
	target := filepath.Join(directory, inactive)
	temporary := target + ".download"
	defer os.Remove(temporary)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
	if err != nil {
		return fmt.Errorf("create OTA request: %w", err)
	}
	response, err := i.HTTP.Do(request)
	if err != nil {
		return fmt.Errorf("download firmware: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("download firmware: HTTP %d", response.StatusCode)
	}
	expected := req.SizeBytes
	if expected <= 0 {
		expected = response.ContentLength
	}
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("create inactive firmware: %w", err)
	}
	hash := sha256.New()
	reader := io.LimitReader(response.Body, maxFirmwareBytes+1)
	buffer := make([]byte, 64*1024)
	var written int64
	lastProgress := -1
	for {
		n, readErr := reader.Read(buffer)
		if n > 0 {
			if _, err := file.Write(buffer[:n]); err != nil {
				file.Close()
				return fmt.Errorf("write inactive firmware: %w", err)
			}
			hash.Write(buffer[:n])
			written += int64(n)
			if written > maxFirmwareBytes {
				file.Close()
				return fmt.Errorf("firmware exceeds %d bytes", maxFirmwareBytes)
			}
			if expected > 0 && report != nil {
				progress := clamp(int(written*90/expected), 0, 90)
				if progress >= lastProgress+5 {
					lastProgress = progress
					report("downloading", progress, "")
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			file.Close()
			return fmt.Errorf("read firmware: %w", readErr)
		}
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync inactive firmware: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close inactive firmware: %w", err)
	}
	if req.SizeBytes > 0 && written != req.SizeBytes {
		return fmt.Errorf("firmware size %d does not match expected %d", written, req.SizeBytes)
	}
	actualHash := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actualHash, req.SHA256) {
		return fmt.Errorf("firmware SHA-256 mismatch: got %s", actualHash)
	}
	probe := make([]byte, 4)
	check, err := os.Open(temporary)
	if err != nil {
		return err
	}
	_, readErr := io.ReadFull(check, probe)
	check.Close()
	if readErr != nil || string(probe) != "\x7fELF" {
		return errors.New("downloaded firmware is not an ELF executable")
	}
	if report != nil {
		report("installing", 95, fmt.Sprintf("Writing inactive slot %s", inactive))
	}
	if err := os.Chmod(temporary, 0o755); err != nil {
		return fmt.Errorf("make firmware executable: %w", err)
	}
	if err := os.Rename(temporary, target); err != nil {
		return fmt.Errorf("commit inactive firmware: %w", err)
	}
	linkTemp := activePath + ".new"
	os.Remove(linkTemp)
	if err := os.Symlink(inactive, linkTemp); err != nil {
		return fmt.Errorf("create firmware slot link: %w", err)
	}
	if err := os.Rename(linkTemp, activePath); err != nil {
		os.Remove(linkTemp)
		return fmt.Errorf("activate firmware slot: %w", err)
	}
	if i.Restart != nil {
		go func() {
			time.Sleep(time.Second)
			i.Restart()
		}()
	}
	return nil
}
