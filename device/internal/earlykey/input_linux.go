//go:build linux

package earlykey

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const bitmapBytes = 128

func inputRequest(number uintptr) uintptr {
	return 2<<30 | bitmapBytes<<16 | 'E'<<8 | number
}

func keySet(bitmap []byte, key int) bool {
	return bitmap[key/8]&(1<<uint(key%8)) != 0
}

func physicalVolumeInput(name string) bool {
	name = strings.TrimSpace(name)
	return name == "gpio-keys" || name == "keys"
}

func bitmap(file *os.File, number uintptr) ([bitmapBytes]byte, error) {
	var bits [bitmapBytes]byte
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), inputRequest(number), uintptr(unsafe.Pointer(&bits[0])))
	if errno != 0 {
		return bits, errno
	}
	return bits, nil
}

// VolumeUp discovers the physical volume input by its capabilities, not an
// unstable event number. EVIOCGKEY also sees a key held before userspace began.
func VolumeUp() (bool, error) {
	paths, err := filepath.Glob("/dev/input/event*")
	if err != nil {
		return false, err
	}
	for _, path := range paths {
		name, err := os.ReadFile(filepath.Join("/sys/class/input", filepath.Base(path), "device/name"))
		if err != nil || !physicalVolumeInput(string(name)) {
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		caps, err := bitmap(file, 0x21) // EVIOCGBIT(EV_KEY)
		if err != nil || !keySet(caps[:], 115) || !keySet(caps[:], 114) {
			file.Close()
			continue
		}
		state, err := bitmap(file, 0x18) // EVIOCGKEY
		file.Close()
		if err != nil {
			return false, err
		}
		return keySet(state[:], 115), nil
	}
	return false, fmt.Errorf("physical volume input is not ready")
}
