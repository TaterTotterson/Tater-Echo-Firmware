package taternative

import (
	"archive/zip"
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
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	checkersPackage  = "com.tatertotterson.show"
	checkersOTAState = "/data/local/etc/tater/ota"
)

var checkersVersion = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?$`)

type checkersBundleFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type checkersBundleManifest struct {
	Schema  int                           `json:"schema"`
	Target  string                        `json:"target"`
	Version string                        `json:"version"`
	Files   map[string]checkersBundleFile `json:"files"`
}

func (i *OTAInstaller) installCheckers(ctx context.Context, req OTARequest, report func(string, int, string)) error {
	activePath := i.ActivePath
	if activePath == "" {
		activePath = "/data/local/bin/server"
	}
	stateDir := i.StateDir
	if stateDir == "" {
		stateDir = checkersOTAState
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("create Checkers OTA state: %w", err)
	}
	bundlePath := filepath.Join(stateDir, "update.download")
	defer os.Remove(bundlePath)
	if err := i.downloadCheckersBundle(ctx, req, bundlePath, report); err != nil {
		return err
	}

	manifest, archive, err := openCheckersBundle(bundlePath)
	if err != nil {
		return err
	}
	defer archive.Close()
	if report != nil {
		report("installing", 91, "Verifying native and screen components")
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

	serverTarget := filepath.Join(filepath.Dir(activePath), inactive)
	serverTemp := serverTarget + ".ota"
	apkStaged := filepath.Join(stateDir, "screen.apk")
	apkTemp := apkStaged + ".ota"
	rollbackAPK := filepath.Join(stateDir, "rollback.apk")
	rollbackTemp := rollbackAPK + ".ota"
	pendingPath := filepath.Join(stateDir, "pending.env")
	healthPath := filepath.Join(stateDir, "healthy")
	for _, path := range []string{serverTemp, apkTemp, rollbackTemp, healthPath} {
		_ = os.Remove(path)
	}

	if err := extractCheckersFile(archive, manifest.Files["server"], serverTemp, 0o755); err != nil {
		return fmt.Errorf("stage native firmware: %w", err)
	}
	if err := verifyELF(serverTemp); err != nil {
		return err
	}
	if err := extractCheckersFile(archive, manifest.Files["screen_apk"], apkTemp, 0o600); err != nil {
		return fmt.Errorf("stage screen APK: %w", err)
	}
	if err := verifyAPK(apkTemp); err != nil {
		return err
	}

	installedAPK, err := i.currentAPK(ctx, checkersPackage)
	if err != nil {
		return err
	}
	if err := copyOTAFile(installedAPK, rollbackTemp, 0o600); err != nil {
		return fmt.Errorf("back up installed screen APK: %w", err)
	}
	if err := os.Rename(rollbackTemp, rollbackAPK); err != nil {
		return fmt.Errorf("commit screen rollback APK: %w", err)
	}
	if err := os.Rename(serverTemp, serverTarget); err != nil {
		return fmt.Errorf("commit inactive native slot: %w", err)
	}
	if err := os.Rename(apkTemp, apkStaged); err != nil {
		return fmt.Errorf("commit staged screen APK: %w", err)
	}

	pending := strings.Join([]string{
		"version=" + manifest.Version,
		"previous_slot=" + active,
		"new_slot=" + inactive,
		"rollback_apk=" + rollbackAPK,
		"staged_apk=" + apkStaged,
	}, "\n") + "\n"
	if err := atomicWrite(pendingPath, []byte(pending), 0o600); err != nil {
		return fmt.Errorf("write Checkers rollback state: %w", err)
	}

	if report != nil {
		report("installing", 96, "Installing the coordinated Tater Show screen")
	}
	if _, err := i.run(ctx, "pm", "install", "-r", "-d", "-g", apkStaged); err != nil {
		_ = os.Remove(pendingPath)
		return fmt.Errorf("install Tater Show APK: %w", err)
	}
	linkTemp := activePath + ".new"
	_ = os.Remove(linkTemp)
	if err := os.Symlink(inactive, linkTemp); err != nil {
		i.restoreCheckersAPK(ctx, rollbackAPK)
		_ = os.Remove(pendingPath)
		return fmt.Errorf("create native slot link: %w", err)
	}
	if err := os.Rename(linkTemp, activePath); err != nil {
		_ = os.Remove(linkTemp)
		i.restoreCheckersAPK(ctx, rollbackAPK)
		_ = os.Remove(pendingPath)
		return fmt.Errorf("activate native slot: %w", err)
	}
	if i.Restart != nil {
		go func() {
			time.Sleep(time.Second)
			i.Restart()
		}()
	}
	return nil
}

func (i *OTAInstaller) downloadCheckersBundle(ctx context.Context, req OTARequest, destination string, report func(string, int, string)) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
	if err != nil {
		return fmt.Errorf("create OTA request: %w", err)
	}
	response, err := i.HTTP.Do(request)
	if err != nil {
		return fmt.Errorf("download Checkers OTA: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("download Checkers OTA: HTTP %d", response.StatusCode)
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create Checkers OTA bundle: %w", err)
	}
	hash := sha256.New()
	reader := io.LimitReader(response.Body, maxFirmwareBytes+1)
	written, copyErr := io.Copy(io.MultiWriter(file, hash), reader)
	syncErr := file.Sync()
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("download Checkers OTA: %w", copyErr)
	}
	if syncErr != nil {
		return fmt.Errorf("sync Checkers OTA: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close Checkers OTA: %w", closeErr)
	}
	if written > maxFirmwareBytes {
		return fmt.Errorf("firmware exceeds %d bytes", maxFirmwareBytes)
	}
	if req.SizeBytes > 0 && written != req.SizeBytes {
		return fmt.Errorf("firmware size %d does not match expected %d", written, req.SizeBytes)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, req.SHA256) {
		return fmt.Errorf("firmware SHA-256 mismatch: got %s", actual)
	}
	if report != nil {
		report("downloading", 90, "Checkers update downloaded")
	}
	return nil
}

func openCheckersBundle(path string) (checkersBundleManifest, *zip.ReadCloser, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return checkersBundleManifest{}, nil, fmt.Errorf("open Checkers OTA bundle: %w", err)
	}
	manifestFile := zipEntry(archive.File, "manifest.json")
	if manifestFile == nil || manifestFile.UncompressedSize64 > 64*1024 {
		archive.Close()
		return checkersBundleManifest{}, nil, errors.New("Checkers OTA manifest is missing or oversized")
	}
	reader, err := manifestFile.Open()
	if err != nil {
		archive.Close()
		return checkersBundleManifest{}, nil, err
	}
	var manifest checkersBundleManifest
	err = json.NewDecoder(io.LimitReader(reader, 64*1024+1)).Decode(&manifest)
	reader.Close()
	if err != nil || manifest.Schema != 1 || manifest.Target != "checkers" || !checkersVersion.MatchString(manifest.Version) {
		archive.Close()
		return checkersBundleManifest{}, nil, errors.New("Checkers OTA manifest is invalid")
	}
	for _, key := range []string{"server", "screen_apk"} {
		file, ok := manifest.Files[key]
		if !ok || file.Path == "" || filepath.Base(file.Path) != file.Path || file.Size <= 0 || len(file.SHA256) != 64 {
			archive.Close()
			return checkersBundleManifest{}, nil, fmt.Errorf("Checkers OTA %s metadata is invalid", key)
		}
		if _, err := hex.DecodeString(file.SHA256); err != nil || zipEntry(archive.File, file.Path) == nil {
			archive.Close()
			return checkersBundleManifest{}, nil, fmt.Errorf("Checkers OTA %s payload is invalid", key)
		}
	}
	return manifest, archive, nil
}

func extractCheckersFile(archive *zip.ReadCloser, file checkersBundleFile, destination string, mode os.FileMode) error {
	entry := zipEntry(archive.File, file.Path)
	if entry == nil || int64(entry.UncompressedSize64) != file.Size || file.Size > maxFirmwareBytes {
		return errors.New("payload size does not match manifest")
	}
	source, err := entry.Open()
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(target, hash), io.LimitReader(source, file.Size+1))
	syncErr := target.Sync()
	closeErr := target.Close()
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written != file.Size || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), file.SHA256) {
		return errors.New("payload failed manifest verification")
	}
	return os.Chmod(destination, mode)
}

func zipEntry(files []*zip.File, name string) *zip.File {
	var found *zip.File
	for _, file := range files {
		if file.Name == name {
			if found != nil {
				return nil
			}
			found = file
		}
	}
	return found
}

func verifyELF(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	header := make([]byte, 4)
	if _, err := io.ReadFull(file, header); err != nil || string(header) != "\x7fELF" {
		return errors.New("Checkers native payload is not an ELF executable")
	}
	return nil
}

func verifyAPK(path string) error {
	apk, err := zip.OpenReader(path)
	if err != nil {
		return errors.New("Checkers screen payload is not an APK archive")
	}
	defer apk.Close()
	if zipEntry(apk.File, "AndroidManifest.xml") == nil {
		return errors.New("Checkers screen APK has no Android manifest")
	}
	return nil
}

func (i *OTAInstaller) currentAPK(ctx context.Context, packageName string) (string, error) {
	if i.InstalledAPKPath != nil {
		return i.InstalledAPKPath(ctx, packageName)
	}
	output, err := i.run(ctx, "pm", "path", packageName)
	if err != nil {
		return "", fmt.Errorf("locate installed Tater Show APK: %w", err)
	}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "package:") {
			path := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "package:"))
			if filepath.IsAbs(path) {
				return path, nil
			}
		}
	}
	return "", errors.New("Android did not report the installed Tater Show APK")
}

func (i *OTAInstaller) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if i.RunCommand != nil {
		return i.RunCommand(ctx, name, args...)
	}
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func (i *OTAInstaller) restoreCheckersAPK(ctx context.Context, path string) {
	_, _ = i.run(ctx, "pm", "install", "-r", "-d", "-g", path)
}

func copyOTAFile(source, destination string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, io.LimitReader(in, maxFirmwareBytes+1))
	syncErr := out.Sync()
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	temporary := path + ".new"
	if err := os.WriteFile(temporary, data, mode); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

// MarkCheckersOTAHealthy commits a pending generation only after both the
// updated native daemon and the matching APK have connected to Tater. The
// supervisor removes rollback material after observing this marker.
func MarkCheckersOTAHealthy(version, appVersion string) (bool, error) {
	stateDir := strings.TrimSpace(os.Getenv("TATER_CHECKERS_OTA_STATE"))
	if stateDir == "" {
		stateDir = checkersOTAState
	}
	pending, err := os.ReadFile(filepath.Join(stateDir, "pending.env"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	wanted := ""
	for _, line := range strings.Split(string(pending), "\n") {
		if strings.HasPrefix(line, "version=") {
			wanted = strings.TrimSpace(strings.TrimPrefix(line, "version="))
		}
	}
	if wanted == "" || wanted != strings.TrimSpace(version) || wanted != strings.TrimSpace(appVersion) {
		return false, nil
	}
	if err := atomicWrite(filepath.Join(stateDir, "healthy"), []byte(wanted+"\n"), 0o600); err != nil {
		return false, err
	}
	return true, nil
}
