package taternative

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/platform"
)

const maxSystemFirmwareBytes = 512 * 1024 * 1024

type linuxSystemManifest struct {
	Schema  int          `json:"schema"`
	Kind    string       `json:"kind"`
	Target  string       `json:"target"`
	BaseOS  string       `json:"base_os"`
	Version string       `json:"version"`
	RootFS  linuxAppFile `json:"rootfs"`
}

// InstallSystem downloads a target-scoped rootfs wrapper, verifies both the
// wrapper and embedded rootfs, and lets slotctl stage the inactive root slot.
// The live root filesystem is never modified. Slotctl's existing trial boot,
// health commit, and retry limit provide rollback if the new slot cannot boot.
func (i *OTAInstaller) InstallSystem(ctx context.Context, req OTARequest, report func(string, int, string)) error {
	if err := validateOTARequest(req); err != nil {
		return err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if !isLinuxFirmwareTarget(i.Target) {
		return fmt.Errorf("unsupported Tater Linux System OTA target %q", i.Target)
	}
	if i.baseOS() != platform.TaterLinux {
		return fmt.Errorf("%s System OTA requires Tater Linux; use the USB factory installer for conversion", i.Target)
	}
	stateDir := i.StateDir
	if stateDir == "" {
		stateDir = linuxAppState
	}
	systemDir := filepath.Join(stateDir, "system-ota")
	if err := os.MkdirAll(systemDir, 0o700); err != nil {
		return fmt.Errorf("create Tater Linux System OTA state: %w", err)
	}
	bundle := filepath.Join(systemDir, "system.download")
	rootfs := filepath.Join(systemDir, "rootfs.download")
	defer os.Remove(bundle)
	defer os.Remove(rootfs)
	if err := os.Remove(rootfs); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale System OTA rootfs: %w", err)
	}
	if err := i.downloadSystem(ctx, req, bundle, report); err != nil {
		return err
	}
	manifest, err := extractVerifiedSystemRootFS(bundle, rootfs, i.Target)
	if err != nil {
		return err
	}
	if report != nil {
		report("installing", 94, fmt.Sprintf("Staging Tater Linux %s in the inactive root slot", manifest.Version))
	}
	if err := i.installRootFS(ctx, rootfs); err != nil {
		return err
	}
	if report != nil {
		report("installing", 100, "Tater Linux installed; rebooting into its trial slot")
	}
	if i.Restart != nil {
		go func() {
			time.Sleep(time.Second)
			i.Restart()
		}()
	}
	return nil
}

func (i *OTAInstaller) installRootFS(ctx context.Context, rootfs string) error {
	if i.InstallRootFS != nil {
		return i.InstallRootFS(ctx, rootfs)
	}
	command := exec.CommandContext(ctx, "/usr/local/sbin/slotctl", "install", rootfs)
	output, err := command.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail != "" {
			return fmt.Errorf("stage inactive Tater Linux root slot: %w: %s", err, detail)
		}
		return fmt.Errorf("stage inactive Tater Linux root slot: %w", err)
	}
	return nil
}

func (i *OTAInstaller) downloadSystem(ctx context.Context, req OTARequest, destination string, report func(string, int, string)) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
	if err != nil {
		return fmt.Errorf("create System OTA request: %w", err)
	}
	response, err := i.HTTP.Do(request)
	if err != nil {
		return fmt.Errorf("download Tater Linux system: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("download Tater Linux system: HTTP %d", response.StatusCode)
	}
	expected := req.SizeBytes
	if expected <= 0 {
		expected = response.ContentLength
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create System OTA download: %w", err)
	}
	hash := sha256.New()
	reader := io.LimitReader(response.Body, maxSystemFirmwareBytes+1)
	buffer := make([]byte, 64*1024)
	var written int64
	lastProgress := -1
	for {
		n, readErr := reader.Read(buffer)
		if n > 0 {
			if _, err := file.Write(buffer[:n]); err != nil {
				file.Close()
				return fmt.Errorf("write System OTA download: %w", err)
			}
			hash.Write(buffer[:n])
			written += int64(n)
			if written > maxSystemFirmwareBytes {
				file.Close()
				return fmt.Errorf("Tater Linux system exceeds %d bytes", maxSystemFirmwareBytes)
			}
			if expected > 0 && report != nil {
				progress := clamp(int(written*90/expected), 0, 90)
				if progress >= lastProgress+5 {
					lastProgress = progress
					report("downloading", progress, "Downloading Tater Linux")
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			file.Close()
			return fmt.Errorf("read System OTA download: %w", readErr)
		}
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync System OTA download: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close System OTA download: %w", err)
	}
	if req.SizeBytes > 0 && written != req.SizeBytes {
		return fmt.Errorf("Tater Linux system size %d does not match expected %d", written, req.SizeBytes)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, req.SHA256) {
		return fmt.Errorf("Tater Linux system SHA-256 mismatch: got %s", actual)
	}
	return nil
}

func extractVerifiedSystemRootFS(bundle, destination, target string) (linuxSystemManifest, error) {
	file, err := os.Open(bundle)
	if err != nil {
		return linuxSystemManifest{}, fmt.Errorf("open System OTA: %w", err)
	}
	defer file.Close()
	zipped, err := gzip.NewReader(file)
	if err != nil {
		return linuxSystemManifest{}, fmt.Errorf("open System OTA compression: %w", err)
	}
	defer zipped.Close()
	archive := tar.NewReader(zipped)
	var manifest linuxSystemManifest
	seen := map[string]bool{}
	var rootfsSize int64
	var rootfsSHA string
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return manifest, fmt.Errorf("read System OTA archive: %w", err)
		}
		name := path.Clean(strings.TrimPrefix(header.Name, "./"))
		if name == "." || strings.HasPrefix(name, "../") || path.IsAbs(name) {
			return manifest, fmt.Errorf("System OTA contains unsafe path %q", header.Name)
		}
		if seen[name] {
			return manifest, fmt.Errorf("System OTA contains duplicate path %q", header.Name)
		}
		seen[name] = true
		if header.Typeflag != tar.TypeReg || header.Size <= 0 {
			return manifest, fmt.Errorf("System OTA %s is invalid", name)
		}
		switch name {
		case "manifest.json":
			if header.Size > 64*1024 {
				return manifest, errors.New("System OTA manifest is too large")
			}
			if err := json.NewDecoder(io.LimitReader(archive, header.Size)).Decode(&manifest); err != nil {
				return manifest, fmt.Errorf("decode System OTA manifest: %w", err)
			}
		case "rootfs.tar.gz":
			out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return manifest, fmt.Errorf("create verified rootfs: %w", err)
			}
			hash := sha256.New()
			rootfsSize, err = io.Copy(io.MultiWriter(out, hash), io.LimitReader(archive, maxSystemFirmwareBytes+1))
			if err != nil {
				out.Close()
				return manifest, fmt.Errorf("extract rootfs: %w", err)
			}
			if rootfsSize != header.Size || rootfsSize > maxSystemFirmwareBytes {
				out.Close()
				return manifest, errors.New("System OTA rootfs has an invalid size")
			}
			if err := out.Sync(); err != nil {
				out.Close()
				return manifest, fmt.Errorf("sync verified rootfs: %w", err)
			}
			if err := out.Close(); err != nil {
				return manifest, fmt.Errorf("close verified rootfs: %w", err)
			}
			rootfsSHA = hex.EncodeToString(hash.Sum(nil))
		default:
			return manifest, fmt.Errorf("System OTA contains unexpected path %q", header.Name)
		}
	}
	if manifest.Schema != 1 || manifest.Kind != "tater-linux-rootfs" ||
		manifest.Target != target || manifest.BaseOS != platform.TaterLinux ||
		!linuxAppVersionPattern.MatchString(manifest.Version) {
		return manifest, errors.New("System OTA identity is incomplete")
	}
	if !seen["manifest.json"] || !seen["rootfs.tar.gz"] || len(seen) != 2 {
		return manifest, errors.New("System OTA archive is incomplete")
	}
	if manifest.RootFS.Size != rootfsSize || !strings.EqualFold(manifest.RootFS.SHA256, rootfsSHA) {
		return manifest, errors.New("System OTA rootfs verification failed")
	}
	return manifest, nil
}
