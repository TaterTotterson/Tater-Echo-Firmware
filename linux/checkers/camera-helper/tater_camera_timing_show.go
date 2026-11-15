//go:build !dot && !spot && !panel

package camera

import (
	"encoding/binary"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

var taterClockOnce sync.Once

// Read-only timing diagnostics through a DUP of the already-owned ISP file.
// Never open another camera-isp file: its driver's release resets VF_CON even
// when another camera owner exists. Only known, ordinary R/W clock registers
// are read, with PROT_READ mappings; no write or ioctl is issued here.
func taterCameraTiming() {
	files, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return
	}
	for _, file := range files {
		target, err := os.Readlink("/proc/self/fd/" + file.Name())
		if err != nil || target != "/dev/camera-isp" {
			continue
		}
		original, err := strconv.Atoi(file.Name())
		if err != nil {
			continue
		}
		fd, err := syscall.Dup(original)
		if err != nil {
			return
		}
		defer syscall.Close(fd)
		top, err := unix.Mmap(fd, 0x10000000, 4096, unix.PROT_READ, unix.MAP_SHARED)
		if err != nil {
			return
		}
		defer unix.Munmap(top)
		sen, err := unix.Mmap(fd, seninfBase, 4096, unix.PROT_READ, unix.MAP_SHARED)
		if err != nil {
			return
		}
		defer unix.Munmap(sen)
		source := binary.LittleEndian.Uint32(top[0x60:]) & 3
		divisor := ((binary.LittleEndian.Uint32(sen[regTG1SenCk:]) >> 16) & 63) + 1
		var rate uint32
		switch source {
		case 0:
			rate = 26000000
		case 1:
			rate = 48000000
		case 2:
			rate = 52000000
		}
		slog.Debug("camera clock readback", "source", source, "divider", divisor, "sensor_mclk_hz", rate/divisor)
		return
	}
}
