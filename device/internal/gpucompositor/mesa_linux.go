//go:build linux

package gpucompositor

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	athenaRuntimeID      = "athena-mt8183-panfrost-mesa-24.2.8-1"
	athenaManifestSHA256 = "ab77ccdd446064c327bf3fe150b3eeb73d65cc8f9bf3c5aeb79f7376725a5ef8"
	athenaHelperSHA256   = "600a8e39e542c506ad1efe73923d1a49bf5cabc2ed93cd2e293b902c73b639bc"
)

// Athena uses native musl/Mesa/Panfrost, never the MT8163 Android linker or
// owner-local Mali runtime. Invoke the matching isolated loader too, so a
// system libc update cannot silently mix this runtime's ABIs.
func (value config) invocation(arguments ...string) (string, []string) {
	if value.target == "athena" {
		return value.root + "/lib/ld-musl-armhf.so.1", append([]string{value.helper}, arguments...)
	}
	return value.helper, arguments
}

type mesaFile struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type mesaManifest struct {
	Schema     int                 `json:"schema"`
	RuntimeID  string              `json:"runtime_id"`
	TreeSHA256 string              `json:"tree_sha256"`
	Files      map[string]mesaFile `json:"files"`
}

func parseMesaManifest(value []byte) (mesaManifest, error) {
	var manifest mesaManifest
	// The exact manifest pins every dependency, not only EGL/GLES entry points.
	if fmt.Sprintf("%x", sha256.Sum256(value)) != athenaManifestSHA256 ||
		json.Unmarshal(value, &manifest) != nil || manifest.Schema != 3 ||
		manifest.RuntimeID != athenaRuntimeID || len(manifest.Files) != 124 {
		return manifest, errors.New("Athena Mesa runtime identity is not allowlisted")
	}
	encoded, err := json.Marshal(manifest.Files)
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(encoded)) != manifest.TreeSHA256 {
		return manifest, errors.New("Athena Mesa runtime tree is invalid")
	}
	return manifest, nil
}

func validateMesaRuntime(value config) error {
	contents, err := boundedFile(value.manifest, manifestMaximumBytes)
	if err != nil {
		return fmt.Errorf("Athena Mesa manifest unavailable: %w", err)
	}
	manifest, err := parseMesaManifest(contents)
	if err != nil {
		return err
	}
	for name, expected := range manifest.Files {
		if filepath.Clean(name) != name || filepath.IsAbs(name) || strings.Contains(name, "..") ||
			(!strings.HasPrefix(name, "lib/") && !strings.HasPrefix(name, "dri/")) {
			return errors.New("unsafe Athena Mesa runtime path")
		}
		path := filepath.Join(value.root, name)
		info, statErr := os.Lstat(path)
		if statErr != nil || !info.Mode().IsRegular() || info.Size() != expected.Size {
			return fmt.Errorf("Athena Mesa file unavailable: %s", name)
		}
		if actual, hashErr := fileSHA256(path); hashErr != nil || actual != expected.SHA256 {
			return fmt.Errorf("Athena Mesa file hash mismatch: %s", name)
		}
	}
	if digest, err := fileSHA256(value.helper); err != nil || digest != athenaHelperSHA256 {
		return errors.New("Athena GPU helper does not match this firmware")
	}
	metadata, err := boundedFile("/sys/class/drm/renderD128/device/uevent", 4096)
	if err != nil || !bytes.HasPrefix(metadata, []byte("DRIVER=panfrost\n")) {
		return errors.New("Athena Panfrost device unavailable")
	}
	return nil
}
