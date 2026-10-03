// Package checkersalsa drives Checkers' speaker through the same ALSA ioctl
// path as TECHO5. The MediaTek vendor kernel does not sustain playback through
// our generic TinyALSA path, even with its required DRAM hold in place.
//
// Adapted from echod/internal/lib/alsa/{alsa,playback}.go in TECHO5 v0.9.26,
// copyright 2026 TECHO5 contributors, MIT license (see LICENSE).
package checkersalsa

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"syscall"
	"unsafe"
)

const (
	channels     = 2
	rate         = 48000
	bits         = 16
	PeriodFrames = 768
	periods      = 4
	frameBytes   = channels * bits / 8

	longSize        = strconv.IntSize / 8
	maskOff         = 4
	maskSize        = 32
	intervalOff     = maskOff + 8*maskSize
	intervalLen     = 12
	rmaskOff        = intervalOff + 21*intervalLen
	infoOff         = rmaskOff + 8
	hwParamsSize    = rmaskOff + 6*4 + longSize + 64
	paramAccess     = 0
	paramFormat     = 1
	paramSubformat  = 2
	paramSampleBits = 8
	paramFrameBits  = 9
	paramChannels   = 10
	paramRate       = 11
	paramPeriodSize = 13
	paramPeriods    = 15
	xferiSize       = 3 * longSize
)

func ioc(dir, typ, nr, size uintptr) uintptr {
	return dir<<30 | size<<16 | typ<<8 | nr
}

var (
	ioctlHwParams = ioc(3, 'A', 0x11, hwParamsSize)
	ioctlPrepare  = ioc(0, 'A', 0x40, 0)
	ioctlStart    = ioc(0, 'A', 0x42, 0)
	ioctlDrop     = ioc(0, 'A', 0x43, 0)
	ioctlWritei   = ioc(1, 'A', 0x50, xferiSize)
)

type hwParams [hwParamsSize]byte

func (p *hwParams) set(off int, value uint32) {
	binary.LittleEndian.PutUint32(p[off:], value)
}

func (p *hwParams) setMask(param, bit int) {
	off := maskOff + param*maskSize
	for i := 0; i < maskSize/4; i++ {
		p.set(off+i*4, 0)
	}
	p.set(off+(bit>>5)*4, 1<<uint(bit&31))
}

func (p *hwParams) setInterval(param int, value uint32) {
	off := intervalOff + (param-paramSampleBits)*intervalLen
	p.set(off, value)
	p.set(off+4, value)
	p.set(off+8, 1<<2) // integer interval
}

func (p *hwParams) interval(param int) uint32 {
	off := intervalOff + (param-paramSampleBits)*intervalLen
	return binary.LittleEndian.Uint32(p[off:])
}

func (p *hwParams) init() {
	for mask := paramAccess; mask <= paramSubformat; mask++ {
		off := maskOff + mask*maskSize
		p.set(off, ^uint32(0))
		p.set(off+4, ^uint32(0))
	}
	for param := paramSampleBits; param <= 19; param++ {
		off := intervalOff + (param-paramSampleBits)*intervalLen
		p.set(off+4, ^uint32(0))
	}
	p.set(rmaskOff, ^uint32(0))
	p.set(infoOff, ^uint32(0))
}

type xferi struct {
	result int
	buf    uintptr
	frames uintptr
}

func ioctl(fd, request uintptr, arg unsafe.Pointer) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(arg)); errno != 0 {
		return errno
	}
	return nil
}

func ioctlArgless(fd, request uintptr) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, 0); errno != 0 {
		return errno
	}
	return nil
}

// Playback holds Checkers' pcmC0D23p stream. The caller must already hold
// pcmC0D1c open so the vendor driver allocates the ring in DRAM.
type Playback struct {
	file    *os.File
	started bool
}

func OpenPlayback() (*Playback, error) {
	const path = "/dev/snd/pcmC0D23p"
	file, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open Checkers playback: %w", err)
	}
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, file.Fd(), syscall.F_GETFL, 0)
	if errno != 0 {
		file.Close()
		return nil, fmt.Errorf("read Checkers playback flags: %w", errno)
	}
	if _, _, errno = syscall.Syscall(syscall.SYS_FCNTL, file.Fd(), syscall.F_SETFL, flags&^syscall.O_NONBLOCK); errno != 0 {
		file.Close()
		return nil, fmt.Errorf("clear Checkers playback nonblock: %w", errno)
	}

	var params hwParams
	params.init()
	params.setMask(paramAccess, 3) // RW_INTERLEAVED
	params.setMask(paramFormat, 2) // S16_LE
	params.setMask(paramSubformat, 0)
	params.setInterval(paramSampleBits, bits)
	params.setInterval(paramFrameBits, bits*channels)
	params.setInterval(paramChannels, channels)
	params.setInterval(paramRate, rate)
	params.setInterval(paramPeriodSize, PeriodFrames)
	params.setInterval(paramPeriods, periods)
	if err := ioctl(file.Fd(), ioctlHwParams, unsafe.Pointer(&params)); err != nil {
		file.Close()
		return nil, fmt.Errorf("Checkers playback hw_params: %w", err)
	}
	for _, granted := range []struct {
		name  string
		param int
		want  uint32
	}{
		{"period size", paramPeriodSize, PeriodFrames},
		{"period count", paramPeriods, periods},
		{"rate", paramRate, rate},
		{"channels", paramChannels, channels},
	} {
		if have := params.interval(granted.param); have != granted.want {
			file.Close()
			return nil, fmt.Errorf("Checkers playback %s: driver granted %d, requested %d", granted.name, have, granted.want)
		}
	}
	if err := ioctlArgless(file.Fd(), ioctlPrepare); err != nil {
		file.Close()
		return nil, fmt.Errorf("Checkers playback prepare: %w", err)
	}
	return &Playback{file: file}, nil
}

// Pump completes a buffer of stereo S16 frames. An underrun is re-prepared
// and retried, matching TECHO5's sender; repeated failures surface to the
// speaker loop instead of spinning forever.
func (p *Playback) Pump(data []byte) error {
	if len(data) == 0 || len(data)%frameBytes != 0 {
		return fmt.Errorf("Checkers playback needs whole stereo frames, got %d bytes", len(data))
	}
	recoveries := 0
	for len(data) > 0 {
		var transfer xferi
		transfer.buf = uintptr(unsafe.Pointer(&data[0]))
		transfer.frames = uintptr(len(data) / frameBytes)
		err := ioctl(p.file.Fd(), ioctlWritei, unsafe.Pointer(&transfer))
		runtime.KeepAlive(data)
		if errors.Is(err, syscall.EPIPE) {
			if prepareErr := ioctlArgless(p.file.Fd(), ioctlPrepare); prepareErr != nil {
				return fmt.Errorf("Checkers playback recover underrun: %w", prepareErr)
			}
			p.started = false
			recoveries++
			if recoveries >= 3 {
				return ErrUnderrun
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("Checkers playback write: %w", err)
		}
		written := int(transfer.result) * frameBytes
		if written <= 0 || written > len(data) {
			return fmt.Errorf("Checkers playback wrote %d of %d bytes", written, len(data))
		}
		if !p.started {
			p.started = true
			if err := ioctlArgless(p.file.Fd(), ioctlStart); err != nil && err != syscall.Errno(0x4d) {
				return fmt.Errorf("Checkers playback start: %w", err)
			}
		}
		data = data[written:]
	}
	return nil
}

func (p *Playback) Close() {
	_ = ioctlArgless(p.file.Fd(), ioctlDrop)
	_ = p.file.Close()
}

var ErrUnderrun = errors.New("Checkers playback underrun")
