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
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const linuxAppState = "/data/tater-linux"
const linuxAppDirectoryMode = 0o755

type linuxAppFile struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type linuxAppManifest struct {
	Schema  int                     `json:"schema"`
	Target  string                  `json:"target"`
	BaseOS  string                  `json:"base_os"`
	Version string                  `json:"version"`
	Files   map[string]linuxAppFile `json:"files"`
}

type linuxAppPending struct {
	Version  string
	Previous string
	New      string
}

var linuxAppKnownFiles = map[string]bool{
	"FFMPEG-LGPL-2.1.txt": true,
	"tater-echo":          true,
	"tater-show":          true,
	"tater-camera":        true,
	"tater-ffmpeg":        true,
}
var linuxAppVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?$`)

// installLinuxApp follows Biscuit's fast A/B application transaction.
// The immutable Linux rootfs and vendor layer change only through USB/rescue.
// Routine OTA stages the daemon and renderer on writable /data and atomically
// flips one symlink, so it never remounts its own live root filesystem.
func (i *OTAInstaller) installLinuxApp(ctx context.Context, req OTARequest, report func(string, int, string)) error {
	stateDir := i.StateDir
	if stateDir == "" {
		stateDir = linuxAppState
	}
	if err := os.MkdirAll(stateDir, linuxAppDirectoryMode); err != nil {
		return fmt.Errorf("create Tater Linux OTA state: %w", err)
	}
	if err := os.Chmod(stateDir, linuxAppDirectoryMode); err != nil {
		return fmt.Errorf("make Tater Linux OTA state traversable: %w", err)
	}
	bundle := filepath.Join(stateDir, "app.download")
	defer os.Remove(bundle)
	if err := i.downloadLinuxApp(ctx, req, bundle, report); err != nil {
		return err
	}
	manifest, err := readLinuxAppManifest(bundle, i.Target)
	if err != nil {
		return err
	}
	if report != nil {
		report("installing", 94, "Staging the inactive Tater application slot")
	}
	if err := installLinuxAppBundle(bundle, stateDir, manifest); err != nil {
		return err
	}
	if report != nil {
		report("installing", 100, "Tater application installed; rebooting into its trial slot")
	}
	if i.Restart != nil {
		go func() {
			time.Sleep(time.Second)
			i.Restart()
		}()
	}
	return nil
}

func (i *OTAInstaller) downloadLinuxApp(ctx context.Context, req OTARequest, destination string, report func(string, int, string)) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
	if err != nil {
		return fmt.Errorf("create Linux OTA request: %w", err)
	}
	response, err := i.HTTP.Do(request)
	if err != nil {
		return fmt.Errorf("download Linux application: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("download Linux application: HTTP %d", response.StatusCode)
	}
	expected := req.SizeBytes
	if expected <= 0 {
		expected = response.ContentLength
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create Linux application download: %w", err)
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
				return fmt.Errorf("write Linux application download: %w", err)
			}
			hash.Write(buffer[:n])
			written += int64(n)
			if written > maxFirmwareBytes {
				file.Close()
				return fmt.Errorf("Linux application exceeds %d bytes", maxFirmwareBytes)
			}
			if expected > 0 && report != nil {
				progress := clamp(int(written*90/expected), 0, 90)
				if progress >= lastProgress+5 {
					lastProgress = progress
					report("downloading", progress, "Downloading the Tater application")
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			file.Close()
			return fmt.Errorf("read Linux application download: %w", readErr)
		}
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync Linux application download: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close Linux application download: %w", err)
	}
	if req.SizeBytes > 0 && written != req.SizeBytes {
		return fmt.Errorf("Linux application size %d does not match expected %d", written, req.SizeBytes)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, req.SHA256) {
		return fmt.Errorf("Linux application SHA-256 mismatch: got %s", actual)
	}
	return nil
}

func readLinuxAppManifest(filename, target string) (linuxAppManifest, error) {
	archive, closeArchive, err := openLinuxAppArchive(filename)
	if err != nil {
		return linuxAppManifest{}, err
	}
	defer closeArchive()
	var manifest linuxAppManifest
	found := map[string]bool{}
	seen := map[string]bool{}
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return manifest, fmt.Errorf("read Linux application archive: %w", err)
		}
		name := path.Clean(strings.TrimPrefix(header.Name, "./"))
		if name == "." || strings.HasPrefix(name, "../") || path.IsAbs(name) {
			return manifest, fmt.Errorf("Linux application contains unsafe path %q", header.Name)
		}
		if seen[name] {
			return manifest, fmt.Errorf("Linux application contains duplicate path %q", header.Name)
		}
		seen[name] = true
		switch name {
		case "manifest.json":
			if header.Typeflag != tar.TypeReg || header.Size <= 0 || header.Size > 64*1024 {
				return manifest, errors.New("Linux application manifest is invalid")
			}
			if err := json.NewDecoder(io.LimitReader(archive, header.Size)).Decode(&manifest); err != nil {
				return manifest, fmt.Errorf("decode Linux application manifest: %w", err)
			}
		case "tater-echo", "tater-show", "tater-camera", "tater-ffmpeg", "FFMPEG-LGPL-2.1.txt":
			if header.Typeflag != tar.TypeReg || header.Size <= 0 {
				return manifest, fmt.Errorf("Linux application %s is invalid", name)
			}
			found[name] = true
		default:
			return manifest, fmt.Errorf("Linux application contains unexpected path %q", header.Name)
		}
	}
	if manifest.Schema != 1 || manifest.Target != target || manifest.BaseOS != "tater-linux" || !linuxAppVersionPattern.MatchString(manifest.Version) {
		return manifest, errors.New("Linux application identity is incomplete")
	}
	for name := range found {
		if _, declared := manifest.Files[name]; !declared {
			return manifest, errors.New("Linux application archive has undeclared files")
		}
	}
	required := []string{"tater-echo"}
	optional := map[string]bool{}
	if target == "checkers" || target == "crown" || target == "athena" || target == "rook" {
		required = append(required, "tater-show")
		optional["tater-camera"] = true
		optional["tater-ffmpeg"] = true
		optional["FFMPEG-LGPL-2.1.txt"] = true
	}
	allowed := map[string]bool{"tater-echo": true}
	for _, name := range required[1:] {
		allowed[name] = true
	}
	for name := range optional {
		allowed[name] = true
	}
	for name := range manifest.Files {
		if !linuxAppKnownFiles[name] || !allowed[name] {
			return manifest, errors.New("Linux application manifest has unexpected files")
		}
	}
	_, hasDecoder := manifest.Files["tater-ffmpeg"]
	_, hasDecoderLicense := manifest.Files["FFMPEG-LGPL-2.1.txt"]
	if hasDecoder != hasDecoderLicense {
		return manifest, errors.New("Linux application must include the video decoder and its license together")
	}
	for _, name := range required {
		entry, ok := manifest.Files[name]
		if !found[name] || !ok || entry.Size <= 0 || len(entry.SHA256) != 64 {
			return manifest, fmt.Errorf("Linux application manifest is missing %s", name)
		}
		if _, err := hex.DecodeString(entry.SHA256); err != nil {
			return manifest, fmt.Errorf("Linux application manifest has invalid %s digest", name)
		}
	}
	for name := range optional {
		entry, included := manifest.Files[name]
		if !included {
			continue
		}
		if !found[name] || entry.Size <= 0 || len(entry.SHA256) != 64 {
			return manifest, fmt.Errorf("Linux application manifest has invalid %s", name)
		}
		if _, err := hex.DecodeString(entry.SHA256); err != nil {
			return manifest, fmt.Errorf("Linux application manifest has invalid %s digest", name)
		}
	}
	return manifest, nil
}

func installLinuxAppBundle(bundle, stateDir string, manifest linuxAppManifest) error {
	appDir := filepath.Join(stateDir, "app")
	slotsDir := filepath.Join(appDir, "slots")
	for _, directory := range []string{stateDir, appDir, slotsDir} {
		if err := os.MkdirAll(directory, linuxAppDirectoryMode); err != nil {
			return fmt.Errorf("create Linux application directory %s: %w", directory, err)
		}
		if err := os.Chmod(directory, linuxAppDirectoryMode); err != nil {
			return fmt.Errorf("make Linux application directory %s traversable: %w", directory, err)
		}
	}
	// FFmpeg runs as techo5-video rather than root. Repair slots created by an
	// older installer so the decoder can traverse the active A/B directory.
	for _, slot := range []string{"a", "b"} {
		directory := filepath.Join(slotsDir, slot)
		if info, err := os.Stat(directory); err == nil && info.IsDir() {
			if err := os.Chmod(directory, linuxAppDirectoryMode); err != nil {
				return fmt.Errorf("make Linux application slot %s traversable: %w", slot, err)
			}
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect Linux application slot %s: %w", slot, err)
		}
	}
	pendingPath := filepath.Join(appDir, "pending.env")
	currentPath := filepath.Join(appDir, "current")
	pendingState, pendingContents, err := readLinuxAppPending(pendingPath)
	if err != nil {
		return err
	}
	if pendingState != nil {
		return continueLinuxAppTrial(bundle, slotsDir, currentPath, pendingPath, pendingContents, manifest, *pendingState)
	}
	previous := "system"
	if link, err := os.Readlink(currentPath); err == nil {
		base := filepath.Base(link)
		if base == "a" || base == "b" {
			previous = base
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read active Linux application slot: %w", err)
	}
	inactive := "a"
	if previous == "a" {
		inactive = "b"
	}
	stage := filepath.Join(slotsDir, "."+inactive+".ota")
	target := filepath.Join(slotsDir, inactive)
	backup := filepath.Join(slotsDir, "."+inactive+".old")
	_ = os.RemoveAll(stage)
	if err := os.MkdirAll(stage, linuxAppDirectoryMode); err != nil {
		return fmt.Errorf("create inactive Linux application slot: %w", err)
	}
	defer os.RemoveAll(stage)
	if err := extractLinuxApp(bundle, stage, manifest); err != nil {
		return err
	}
	_ = os.RemoveAll(backup)
	if _, err := os.Stat(target); err == nil {
		if err := os.Rename(target, backup); err != nil {
			return fmt.Errorf("preserve inactive Linux application slot: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect inactive Linux application slot: %w", err)
	}
	if err := os.Rename(stage, target); err != nil {
		_ = os.Rename(backup, target)
		return fmt.Errorf("commit inactive Linux application slot: %w", err)
	}
	if err := syncDirectory(slotsDir); err != nil {
		return fmt.Errorf("sync inactive Linux application slot: %w", err)
	}
	pending := fmt.Sprintf("version=%s\nprevious=%s\nnew=%s\n", manifest.Version, previous, inactive)
	if err := atomicWrite(pendingPath, []byte(pending), 0o600); err != nil {
		return fmt.Errorf("write Linux application rollback state: %w", err)
	}
	linkTemp := currentPath + ".new"
	_ = os.Remove(linkTemp)
	if err := os.Symlink(filepath.Join("slots", inactive), linkTemp); err != nil {
		return fmt.Errorf("create Linux application slot link: %w", err)
	}
	if err := os.Rename(linkTemp, currentPath); err != nil {
		_ = os.Remove(linkTemp)
		return fmt.Errorf("activate Linux application slot: %w", err)
	}
	return syncDirectory(appDir)
}

func readLinuxAppPending(filename string) (*linuxAppPending, []byte, error) {
	contents, err := os.ReadFile(filename)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("inspect Linux application rollback state: %w", err)
	}
	values := map[string]string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSuffix(string(contents), "\n"), "\n") {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 || seen[parts[0]] {
			return nil, nil, errors.New("Linux application rollback state is invalid")
		}
		switch parts[0] {
		case "version", "previous", "new":
			values[parts[0]] = parts[1]
			seen[parts[0]] = true
		default:
			return nil, nil, errors.New("Linux application rollback state is invalid")
		}
	}
	pending := &linuxAppPending{
		Version: values["version"], Previous: values["previous"], New: values["new"],
	}
	validPrevious := pending.Previous == "system" || pending.Previous == "a" || pending.Previous == "b"
	validNew := pending.New == "a" || pending.New == "b"
	if !linuxAppVersionPattern.MatchString(pending.Version) || !validPrevious || !validNew || pending.Previous == pending.New {
		return nil, nil, errors.New("Linux application rollback state is invalid")
	}
	return pending, contents, nil
}

// continueLinuxAppTrial completes a staged release without waiting for the
// existing health window to commit. Only another bundle for the exact same
// version may replace the active trial slot. The original pending marker is
// deliberately retained, so a bad completed bundle still rolls back to the
// slot that was active before the bridge was installed.
func continueLinuxAppTrial(bundle, slotsDir, currentPath, pendingPath string, pendingContents []byte, manifest linuxAppManifest, pending linuxAppPending) error {
	if manifest.Version != pending.Version {
		return errors.New("a different Linux application trial is already pending")
	}
	link, err := os.Readlink(currentPath)
	if err != nil {
		return fmt.Errorf("read active Linux application trial slot: %w", err)
	}
	if filepath.Clean(link) != filepath.Join("slots", pending.New) {
		return errors.New("Linux application trial slot does not match its rollback state")
	}

	stage := filepath.Join(slotsDir, "."+pending.New+".ota")
	target := filepath.Join(slotsDir, pending.New)
	backup := filepath.Join(slotsDir, "."+pending.New+".old")
	_ = os.RemoveAll(stage)
	if err := os.MkdirAll(stage, linuxAppDirectoryMode); err != nil {
		return fmt.Errorf("create Linux application continuation slot: %w", err)
	}
	defer os.RemoveAll(stage)
	if err := extractLinuxApp(bundle, stage, manifest); err != nil {
		return err
	}

	// The health watcher may commit near the end of its five-minute window.
	// Refuse to replace anything if the transaction changed while extracting.
	currentPending, err := os.ReadFile(pendingPath)
	if err != nil || string(currentPending) != string(pendingContents) {
		return errors.New("Linux application trial changed while its continuation was staged; retry the update")
	}
	_ = os.RemoveAll(backup)
	if err := os.Rename(target, backup); err != nil {
		return fmt.Errorf("preserve active Linux application trial slot: %w", err)
	}
	if err := os.Rename(stage, target); err != nil {
		_ = os.Rename(backup, target)
		return fmt.Errorf("commit Linux application trial continuation: %w", err)
	}
	if err := syncDirectory(slotsDir); err != nil {
		return fmt.Errorf("sync Linux application trial continuation: %w", err)
	}
	return nil
}

func extractLinuxApp(bundle, destination string, manifest linuxAppManifest) error {
	archive, closeArchive, err := openLinuxAppArchive(bundle)
	if err != nil {
		return err
	}
	defer closeArchive()
	extracted := map[string]bool{}
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read Linux application archive: %w", err)
		}
		name := path.Clean(strings.TrimPrefix(header.Name, "./"))
		entry, required := manifest.Files[name]
		if !required {
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Size != entry.Size {
			return fmt.Errorf("Linux application %s size does not match its manifest", name)
		}
		target := filepath.Join(destination, name)
		mode := os.FileMode(0o755)
		if name == "FFMPEG-LGPL-2.1.txt" {
			mode = 0o644
		}
		file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return fmt.Errorf("create Linux application %s: %w", name, err)
		}
		hash := sha256.New()
		_, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(archive, header.Size))
		syncErr := file.Sync()
		closeErr := file.Close()
		if copyErr != nil || syncErr != nil || closeErr != nil {
			return fmt.Errorf("write Linux application %s: %v", name, errors.Join(copyErr, syncErr, closeErr))
		}
		if actual := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(actual, entry.SHA256) {
			return fmt.Errorf("Linux application %s digest mismatch", name)
		}
		if name != "FFMPEG-LGPL-2.1.txt" {
			if err := verifyELF(target); err != nil {
				return fmt.Errorf("verify Linux application %s: %w", name, err)
			}
		}
		extracted[name] = true
	}
	for name := range manifest.Files {
		if !extracted[name] {
			return fmt.Errorf("Linux application did not extract %s", name)
		}
	}
	return syncDirectory(destination)
}

func openLinuxAppArchive(filename string) (*tar.Reader, func(), error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, nil, err
	}
	zipped, err := gzip.NewReader(file)
	if err != nil {
		file.Close()
		return nil, nil, fmt.Errorf("Linux OTA is not a gzip application bundle: %w", err)
	}
	return tar.NewReader(zipped), func() { _ = zipped.Close(); _ = file.Close() }, nil
}

func verifyELF(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	header := make([]byte, 4)
	if _, err := io.ReadFull(file, header); err != nil || string(header) != "\x7fELF" {
		return errors.New("Linux application payload is not an ELF executable")
	}
	return nil
}

func syncDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	temporary := path + ".new"
	_ = os.Remove(temporary)
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporary)
		}
	}()
	written, err := file.Write(data)
	if err == nil && written != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	removeTemporary = false
	return syncDirectory(filepath.Dir(path))
}
