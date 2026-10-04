//go:build linux

// Package linuxinput reads Checkers and Rook touchscreens directly from evdev.
// Its small evdev reader is derived from TECHO5 (MIT).
package linuxinput

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

const (
	evSyn           = 0
	evKey           = 1
	evAbs           = 3
	synReport       = 0
	synDropped      = 3
	btnTouch        = 0x14a
	absMTSlot       = 0x2f
	absMTPositionX  = 0x35
	absMTPositionY  = 0x36
	absMTTrackingID = 0x39
	absPositionX    = 0x00
	absPositionY    = 0x01
	kernelLongBytes = strconv.IntSize / 8
	inputEventBytes = 2*kernelLongBytes + 8
	inputEventType  = 2 * kernelLongBytes
	inputEventCode  = inputEventType + 2
	inputEventValue = inputEventCode + 2
)

type EventKind uint8

const (
	Down EventKind = iota + 1
	Move
	Up
)

type TouchEvent struct {
	Kind EventKind
	X    int
	Y    int
}

type rawEvent struct {
	typeCode uint16
	code     uint16
	value    int32
}

type position struct {
	x, y         int32
	seenX, seenY bool
}

type absInfo struct {
	Value, Min, Max, Fuzz, Flat, Resolution int32
}

type Device struct {
	file      *os.File
	path      string
	rawWidth  int
	rawHeight int
	protocolA bool
	closeOnce sync.Once
}

func Open() (*Device, error) {
	paths, err := filepath.Glob("/dev/input/event*")
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		name, err := os.ReadFile("/sys/class/input/" + filepath.Base(path) + "/device/name")
		if err != nil {
			continue
		}
		inputName := strings.TrimSpace(string(name))
		if inputName != "goodix-ts" && inputName != "mtk-tpd" {
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		device := &Device{file: file, path: path, rawWidth: 480, rawHeight: 960, protocolA: inputName == "mtk-tpd"}
		xAxis, yAxis := uint16(absMTPositionX), uint16(absMTPositionY)
		if device.protocolA {
			device.rawHeight = 480
		}
		if device.protocolA {
			if x, errX := device.absolute(xAxis); errX != nil || x.Max <= x.Min {
				xAxis, yAxis = absPositionX, absPositionY
			}
		}
		if x, errX := device.absolute(xAxis); errX == nil && x.Max > x.Min {
			device.rawWidth = int(x.Max-x.Min) + 1
		}
		if y, errY := device.absolute(yAxis); errY == nil && y.Max > y.Min {
			device.rawHeight = int(y.Max-y.Min) + 1
		}
		return device, nil
	}
	return nil, fmt.Errorf("no input device named goodix-ts or mtk-tpd")
}

func (d *Device) String() string {
	return fmt.Sprintf("%s (%dx%d raw)", d.path, d.rawWidth, d.rawHeight)
}

func (d *Device) Close() error {
	var err error
	d.closeOnce.Do(func() { err = d.file.Close() })
	return err
}

func (d *Device) absolute(code uint16) (absInfo, error) {
	var result absInfo
	request := uintptr(0x80184540 + uint32(code))
	connection, err := d.file.SyscallConn()
	if err != nil {
		return result, err
	}
	var errno syscall.Errno
	if err := connection.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(unsafe.Pointer(&result)))
	}); err != nil {
		return result, err
	}
	if errno != 0 {
		return result, errno
	}
	return result, nil
}

func (d *Device) read() (rawEvent, error) {
	buffer := make([]byte, inputEventBytes)
	if _, err := io.ReadFull(d.file, buffer); err != nil {
		return rawEvent{}, err
	}
	return rawEvent{
		typeCode: binary.LittleEndian.Uint16(buffer[inputEventType:]),
		code:     binary.LittleEndian.Uint16(buffer[inputEventCode:]),
		value:    int32(binary.LittleEndian.Uint32(buffer[inputEventValue:])),
	}, nil
}

func (d *Device) Run(ctx context.Context, events chan<- TouchEvent) error {
	stop := context.AfterFunc(ctx, func() { _ = d.Close() })
	defer stop()
	if d.protocolA {
		return d.runProtocolA(ctx, events)
	}
	slots := map[int]*position{}
	slot, activeSlot := 0, -1
	active := false
	lastX, lastY := -1, -1
	dropping := false
	for {
		event, err := d.read()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("read %s: %w", d.path, err)
		}
		if event.typeCode == evSyn && event.code == synDropped {
			slots = map[int]*position{}
			active, activeSlot, dropping = false, -1, true
			continue
		}
		if dropping {
			if event.typeCode == evSyn && event.code == synReport {
				dropping = false
			}
			continue
		}
		switch event.typeCode {
		case evAbs:
			if event.code == absMTSlot {
				slot = int(event.value)
				continue
			}
			point := slots[slot]
			if point == nil {
				point = &position{}
				slots[slot] = point
			}
			switch event.code {
			case absMTPositionX:
				point.x, point.seenX = event.value, true
			case absMTPositionY:
				point.y, point.seenY = event.value, true
			case absMTTrackingID:
				if event.value >= 0 && !active {
					active, activeSlot = true, slot
					lastX, lastY = d.frame(point)
					deliver(ctx, events, TouchEvent{Kind: Down, X: lastX, Y: lastY})
				} else if event.value < 0 && active && activeSlot == slot {
					x, y := d.frame(point)
					deliver(ctx, events, TouchEvent{Kind: Up, X: x, Y: y})
					active, activeSlot = false, -1
				}
			}
		case evKey:
			if event.code == btnTouch && event.value == 0 && active {
				position := slots[activeSlot]
				x, y := d.frame(position)
				deliver(ctx, events, TouchEvent{Kind: Up, X: x, Y: y})
				active, activeSlot = false, -1
			}
		case evSyn:
			if event.code == synReport && active {
				x, y := d.frame(slots[activeSlot])
				if x != lastX || y != lastY {
					lastX, lastY = x, y
					deliver(ctx, events, TouchEvent{Kind: Move, X: x, Y: y})
				}
			}
		}
	}
}

// Rook's mtk-tpd sends protocol-A positions and BTN_TOUCH transitions, not
// Checkers' slots and tracking IDs. Emit Down at SYN_REPORT so both coordinates
// have arrived before a tap can activate an on-screen control.
func (d *Device) runProtocolA(ctx context.Context, events chan<- TouchEvent) error {
	point := &position{}
	touching, active := false, false
	lastX, lastY := -1, -1
	for {
		event, err := d.read()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("read %s: %w", d.path, err)
		}
		switch event.typeCode {
		case evAbs:
			switch event.code {
			case absMTPositionX, absPositionX:
				point.x, point.seenX = event.value, true
			case absMTPositionY, absPositionY:
				point.y, point.seenY = event.value, true
			}
		case evKey:
			if event.code == btnTouch {
				touching = event.value != 0
				if !touching && active {
					deliver(ctx, events, TouchEvent{Kind: Up, X: lastX, Y: lastY})
					active = false
				}
			}
		case evSyn:
			if event.code == synDropped {
				if active {
					deliver(ctx, events, TouchEvent{Kind: Up, X: lastX, Y: lastY})
				}
				touching, active = false, false
				point = &position{}
				continue
			}
			if event.code != synReport || !touching || !point.seenX || !point.seenY {
				continue
			}
			x, y := d.frame(point)
			if !active {
				active = true
				deliver(ctx, events, TouchEvent{Kind: Down, X: x, Y: y})
			} else if x != lastX || y != lastY {
				deliver(ctx, events, TouchEvent{Kind: Move, X: x, Y: y})
			}
			lastX, lastY = x, y
		}
	}
}

func (d *Device) frame(position *position) (int, int) {
	if position == nil || !position.seenX || !position.seenY {
		return 0, 0
	}
	if d.protocolA {
		x := int(position.x) * 480 / max(d.rawWidth, 1)
		y := int(position.y) * 480 / max(d.rawHeight, 1)
		return min(max(x, 0), 479), min(max(y, 0), 479)
	}
	x := int(position.y) * 960 / max(d.rawHeight, 1)
	y := (d.rawWidth - 1 - int(position.x)) * 480 / max(d.rawWidth, 1)
	return min(max(x, 0), 959), min(max(y, 0), 479)
}

func deliver(ctx context.Context, events chan<- TouchEvent, event TouchEvent) {
	select {
	case events <- event:
	case <-ctx.Done():
	default:
	}
}
