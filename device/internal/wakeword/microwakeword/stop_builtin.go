package microwakeword

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	TimerStopPackage  = "stop"
	timerStopModelSHA = "020ef80d522cb09169a866f3aeeb58f2ad4045461937e78e8c806df29ff61eea"
)

// EnsureBuiltinTimerStopPackage materializes the tiny pinned timer-stop model
// carried by the daemon. Embedding it keeps Biscuit's single-binary OTA format
// useful: an existing install gains local "stop" support as soon as the new
// server starts, even before a factory bundle is installed again.
func EnsureBuiltinTimerStopPackage() error {
	dir := PackageDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("microwakeword: create timer-stop package directory: %w", err)
	}
	modelPath := filepath.Join(dir, "stop.tflite")
	valid, err := fileHasSHA256(modelPath, timerStopModelSHA)
	if err != nil {
		return err
	}
	if !valid {
		compressed, err := base64.StdEncoding.DecodeString(timerStopModelGzipBase64)
		if err != nil {
			return fmt.Errorf("microwakeword: decode built-in timer-stop model: %w", err)
		}
		reader, err := gzip.NewReader(bytes.NewReader(compressed))
		if err != nil {
			return fmt.Errorf("microwakeword: open built-in timer-stop model: %w", err)
		}
		model, readErr := io.ReadAll(io.LimitReader(reader, maxModelBytes+1))
		closeErr := reader.Close()
		if readErr != nil {
			return fmt.Errorf("microwakeword: decompress built-in timer-stop model: %w", readErr)
		}
		if closeErr != nil {
			return fmt.Errorf("microwakeword: finish built-in timer-stop model: %w", closeErr)
		}
		if len(model) == 0 || int64(len(model)) > maxModelBytes {
			return fmt.Errorf("microwakeword: invalid built-in timer-stop model size %d", len(model))
		}
		actual := fmt.Sprintf("%x", sha256.Sum256(model))
		if actual != timerStopModelSHA {
			return fmt.Errorf("microwakeword: built-in timer-stop model checksum %s", actual)
		}
		if err := atomicWrite(modelPath, model, 0o644); err != nil {
			return err
		}
	}

	manifestPath := filepath.Join(dir, "stop.json")
	installed, err := os.ReadFile(manifestPath)
	if err != nil || string(installed) != timerStopManifestJSON {
		if err := atomicWrite(manifestPath, []byte(timerStopManifestJSON), 0o644); err != nil {
			return err
		}
	}
	if _, err := ReadPackageManifest(TimerStopPackage); err != nil {
		return fmt.Errorf("microwakeword: validate built-in timer-stop package: %w", err)
	}
	return nil
}

func fileHasSHA256(path, expected string) (bool, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("microwakeword: read %s: %w", path, err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)) == expected, nil
}

func atomicWrite(path string, raw []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".tater-mww-*")
	if err != nil {
		return fmt.Errorf("microwakeword: create %s: %w", path, err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(raw); err != nil {
		temporary.Close()
		return fmt.Errorf("microwakeword: write %s: %w", path, err)
	}
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return fmt.Errorf("microwakeword: chmod %s: %w", path, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("microwakeword: close %s: %w", path, err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("microwakeword: install %s: %w", path, err)
	}
	return nil
}
