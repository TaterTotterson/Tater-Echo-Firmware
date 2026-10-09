//go:build server

package mixer

// Radar's TLV320AIC3204 exposes its speaker filter as an ALSA byte control.
// The Fire OS tinyalsa libraries do not share a dependable array-write symbol,
// so this small path uses the stable kernel control ioctl ABI directly. The
// layout and name lookup follow echolocal's independently hardware-tested
// Radar mixer implementation (MIT; attribution in NOTICE.md).

import (
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"syscall"
	"unsafe"
)

const (
	alsaElemIDSize      = 64
	alsaElemInfoSize    = 272
	alsaIDNameOffset    = 16
	alsaIDNameLength    = 44
	alsaInfoTypeOffset  = alsaElemIDSize
	alsaInfoCountOffset = alsaElemIDSize + 8
	alsaValueDataOffset = alsaElemIDSize + 8
	alsaTypeBytes       = 4
	alsaMaxByteValues   = 512
	alsaLongSize        = strconv.IntSize / 8
	alsaElemValueSize   = alsaValueDataOffset + 128*alsaLongSize + 128
)

func alsaIOC(dir, typ, nr, size uintptr) uintptr {
	return dir<<30 | size<<16 | typ<<8 | nr
}

var (
	alsaElemInfo  = alsaIOC(3, 'U', 0x11, alsaElemInfoSize)
	alsaElemWrite = alsaIOC(3, 'U', 0x13, alsaElemValueSize)
)

func alsaIoctl(fd, request uintptr, data []byte) error {
	if len(data) == 0 {
		return syscall.EINVAL
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(unsafe.Pointer(&data[0])))
	runtime.KeepAlive(data)
	if errno != 0 {
		return errno
	}
	return nil
}

func alsaCString(data []byte) string {
	for i, b := range data {
		if b == 0 {
			return string(data[:i])
		}
	}
	return string(data)
}

func findByteControl(fd uintptr, name string) (uint32, uint32, error) {
	// ALSA assigns dense numids on this fixed card. Stop at the first gap, as
	// walking arbitrary ids after it would turn a missing exact name into a
	// slow device-start timeout.
	for numid := uint32(1); numid <= 4096; numid++ {
		info := make([]byte, alsaElemInfoSize)
		binary.LittleEndian.PutUint32(info, numid)
		if err := alsaIoctl(fd, alsaElemInfo, info); err != nil {
			break
		}
		have := alsaCString(info[alsaIDNameOffset : alsaIDNameOffset+alsaIDNameLength])
		if have != name {
			continue
		}
		typ := binary.LittleEndian.Uint32(info[alsaInfoTypeOffset:])
		if typ != alsaTypeBytes {
			return 0, 0, fmt.Errorf("mixer: %q is type %d, not bytes", name, typ)
		}
		count := binary.LittleEndian.Uint32(info[alsaInfoCountOffset:])
		if count == 0 || count > alsaMaxByteValues {
			return 0, 0, fmt.Errorf("mixer: %q has invalid byte count %d", name, count)
		}
		return numid, count, nil
	}
	return 0, 0, fmt.Errorf("mixer: no byte control named %q on card", name)
}

func setByteControl(card uint, name string, values []byte) error {
	f, err := os.OpenFile(fmt.Sprintf("/dev/snd/controlC%d", card), os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("mixer: open control card %d: %w", card, err)
	}
	defer f.Close()

	numid, count, err := findByteControl(f.Fd(), name)
	if err != nil {
		return err
	}
	if len(values) != int(count) {
		return fmt.Errorf("mixer: %q takes %d bytes, given %d", name, count, len(values))
	}
	value := make([]byte, alsaElemValueSize)
	binary.LittleEndian.PutUint32(value, numid)
	copy(value[alsaValueDataOffset:], values)
	if err := alsaIoctl(f.Fd(), alsaElemWrite, value); err != nil {
		return fmt.Errorf("mixer: write byte control %q: %w", name, err)
	}
	return nil
}
