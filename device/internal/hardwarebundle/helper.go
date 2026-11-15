// Package hardwarebundle keeps open hardware helpers with their owning binary.
// Private runtime libraries remain separately provisioned and hash-qualified.
package hardwarebundle

import (
	"os"
	"path/filepath"
)

// Helper never consults /data's shared helper or another application/system
// slot. A missing sidecar must fail preflight, not silently mix versions.
func Helper(name string) string {
	executable, err := os.Executable()
	if err != nil {
		return "/missing-tater-hardware/" + name
	}
	return Beside(executable, name)
}

func Beside(executable, name string) string {
	return filepath.Join(filepath.Dir(executable), name)
}
