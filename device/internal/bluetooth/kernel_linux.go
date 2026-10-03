//go:build linux && !android

package bluetooth

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	kernelHCIDevice = "/sys/class/bluetooth/hci0"
	hciFilterOpt    = 2 // HCI_FILTER from BlueZ's lib/hci.h
)

// hciFilter matches the kernel's 16-byte struct hci_ufilter ABI.
type hciFilter struct {
	typeMask  uint32
	eventMask [2]uint32
	opcode    uint16
}

var _ [0]struct{} = [unsafe.Sizeof(hciFilter{}) - 16]struct{}{}

// openKernelTransport returns nil when hci0 is absent so Fire OS-style raw
// /dev/stpbt remains the fallback. Linux packets retain the H4 type byte, so
// the existing parser and command path are shared unchanged.
func openKernelTransport() (*os.File, bool, error) {
	if _, err := os.Stat(kernelHCIDevice); err != nil {
		return nil, false, nil
	}
	fd, err := unix.Socket(unix.AF_BLUETOOTH, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.BTPROTO_HCI)
	if err != nil {
		return nil, false, fmt.Errorf("open hci0 socket: %w", err)
	}
	failed := func(err error) (*os.File, bool, error) {
		_ = unix.Close(fd)
		return nil, false, err
	}
	all := ^uint32(0)
	filter := hciFilter{typeMask: 1 << h4TypeEvent, eventMask: [2]uint32{all, all}}
	if _, _, errno := unix.Syscall6(unix.SYS_SETSOCKOPT, uintptr(fd), unix.SOL_HCI, hciFilterOpt,
		uintptr(unsafe.Pointer(&filter)), unsafe.Sizeof(filter), 0); errno != 0 {
		return failed(fmt.Errorf("set hci0 event filter: %w", errno))
	}
	if err := unix.Bind(fd, &unix.SockaddrHCI{Dev: 0, Channel: unix.HCI_CHANNEL_RAW}); err != nil {
		return failed(fmt.Errorf("bind hci0 raw socket: %w", err))
	}
	return os.NewFile(uintptr(fd), "hci0"), true, nil
}
