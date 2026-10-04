//go:build linux

// Package linuxscreen owns the Checkers and Rook framebuffers. The framebuffer
// code is derived from TECHO5 (MIT); Checkers rotates its portrait panel,
// while Rook draws directly to its square panel.
package linuxscreen

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	fbioGetVScreenInfo = 0x4600
	fbioGetFScreenInfo = 0x4602
	fbioPanDisplay     = 0x4606
	backlightPath      = "/sys/class/leds/lcd-backlight/brightness"
	BacklightMax       = 255
)

type varInfo struct {
	Xres, Yres, XresVirtual, YresVirtual, Xoffset, Yoffset uint32
	BitsPerPixel, Grayscale                                uint32
	Red, Green, Blue, Transp                               [3]uint32
	Nonstd, Activate, Height, Width, AccelFlags            uint32
	Pixclock, LeftMargin, RightMargin, UpperMargin         uint32
	LowerMargin, HsyncLen, VsyncLen, Sync, Vmode, Rotate   uint32
	Colorspace                                             uint32
	Reserved                                               [4]uint32
}

type fixInfo struct {
	ID                    [16]byte
	SmemStart             uint
	SmemLen               uint32
	Type, TypeAux, Visual uint32
	Xpanstep, Ypanstep    uint16
	Ywrapstep             uint16
	_                     uint16
	LineLength            uint32
	MmioStart             uint
	MmioLen, Accel        uint32
	Capabilities          uint16
	_                     [2]uint16
	_                     uint16
}

func ioctl(fd uintptr, request uintptr, arg unsafe.Pointer) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(arg)); errno != 0 {
		return errno
	}
	return nil
}

type Device struct {
	file                    *os.File
	memory                  []byte
	variable                varInfo
	lineBytes, pageBytes    int
	pages, page             int
	panelWidth, panelHeight int
	shifts                  [4]uint
	canvas                  *image.RGBA
	shadow                  [][]byte
	sourceShadow            [][]byte
	sourceValid             []bool
	bandRows                [][]byte
	directRow               []byte
	lastConvert, lastPan    time.Duration
}

func Open() (*Device, error) {
	var file *os.File
	var err error
	for _, name := range []string{"/dev/graphics/fb0", "/dev/fb0"} {
		file, err = os.OpenFile(name, os.O_RDWR, 0)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("open framebuffer: %w", err)
	}
	d := &Device{file: file}
	var fixed fixInfo
	if err := ioctl(file.Fd(), fbioGetVScreenInfo, unsafe.Pointer(&d.variable)); err != nil {
		file.Close()
		return nil, fmt.Errorf("FBIOGET_VSCREENINFO: %w", err)
	}
	if err := ioctl(file.Fd(), fbioGetFScreenInfo, unsafe.Pointer(&fixed)); err != nil {
		file.Close()
		return nil, fmt.Errorf("FBIOGET_FSCREENINFO: %w", err)
	}
	if d.variable.BitsPerPixel != 32 {
		file.Close()
		return nil, fmt.Errorf("unsupported framebuffer depth %d", d.variable.BitsPerPixel)
	}
	d.panelWidth, d.panelHeight = int(d.variable.Xres), int(d.variable.Yres)
	d.lineBytes = int(fixed.LineLength)
	if d.lineBytes == 0 {
		d.lineBytes = int(d.variable.XresVirtual) * 4
	}
	d.pageBytes = d.lineBytes * d.panelHeight
	d.pages = max(int(d.variable.YresVirtual)/max(d.panelHeight, 1), 1)
	if fixed.SmemLen > 0 && int(fixed.SmemLen) < d.pageBytes*d.pages {
		d.pages = max(int(fixed.SmemLen)/d.pageBytes, 1)
	}
	d.shifts = [4]uint{uint(d.variable.Red[0]), uint(d.variable.Green[0]), uint(d.variable.Blue[0]), uint(d.variable.Transp[0])}
	for d.pages >= 1 {
		d.memory, err = unix.Mmap(int(file.Fd()), 0, d.pageBytes*d.pages, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
		if err == nil {
			break
		}
		if d.pages == 1 {
			file.Close()
			return nil, fmt.Errorf("map framebuffer: %w", err)
		}
		d.pages--
	}
	d.page = int(d.variable.Yoffset) / max(d.panelHeight, 1)
	if d.page >= d.pages {
		d.page = 0
	}
	if d.panelWidth == d.panelHeight {
		d.canvas = image.NewRGBA(image.Rect(0, 0, d.panelWidth, d.panelHeight))
	} else {
		// Checkers has a 480x960 portrait panel mounted as a 960x480 display.
		d.canvas = image.NewRGBA(image.Rect(0, 0, d.panelHeight, d.panelWidth))
	}
	return d, nil
}

func (d *Device) Canvas() *image.RGBA { return d.canvas }

func (d *Device) LastPresentTimings() (time.Duration, time.Duration) {
	return d.lastConvert, d.lastPan
}

func (d *Device) Size() (int, int) { return d.canvas.Rect.Dx(), d.canvas.Rect.Dy() }

func (d *Device) String() string {
	return fmt.Sprintf("%dx%d panel, %d page(s), %d bytes/line", d.panelWidth, d.panelHeight, d.pages, d.lineBytes)
}

const rotateBand = 8

func (d *Device) Present() error {
	started := time.Now()
	next := d.page
	if d.pages > 1 {
		next = (d.page + 1) % d.pages
	}
	destination := d.memory[next*d.pageBytes : (next+1)*d.pageBytes]
	imageData := d.canvas
	width, height := imageData.Rect.Dx(), imageData.Rect.Dy()
	red, green, blue, alpha := d.shifts[0], d.shifts[1], d.shifts[2], d.shifts[3]
	fast := green == 8 && alpha == 24 && ((red == 0 && blue == 16) || (red == 16 && blue == 0))
	swap := red == 16
	if d.shadow == nil {
		d.shadow = make([][]byte, max(d.pages, 1))
		d.sourceShadow = make([][]byte, max(d.pages, 1))
		d.sourceValid = make([]bool, max(d.pages, 1))
	}
	shadow := d.shadow[next]
	if shadow == nil {
		shadow = append([]byte(nil), destination...)
		d.shadow[next] = shadow
	}
	sourceShadow := d.sourceShadow[next]
	if sourceShadow == nil {
		sourceShadow = make([]byte, len(imageData.Pix))
		d.sourceShadow[next] = sourceShadow
	}
	rowBytes := d.panelWidth * 4
	if d.panelWidth == d.panelHeight {
		if len(d.directRow) != rowBytes {
			d.directRow = make([]byte, rowBytes)
		}
		for y := 0; y < d.panelHeight; y++ {
			from := y * imageData.Stride
			source := imageData.Pix[from : from+rowBytes]
			if d.sourceValid[next] && bytes.Equal(source, sourceShadow[from:from+rowBytes]) {
				continue
			}
			copy(sourceShadow[from:from+rowBytes], source)
			row := d.directRow
			for x := 0; x < rowBytes; x += 4 {
				pixel := source[x : x+4]
				switch {
				case fast && swap:
					binary.LittleEndian.PutUint32(row[x:x+4], uint32(pixel[2])|uint32(pixel[1])<<8|uint32(pixel[0])<<16|uint32(pixel[3])<<24)
				case fast:
					copy(row[x:x+4], pixel)
				default:
					binary.LittleEndian.PutUint32(row[x:x+4], uint32(pixel[0])<<red|uint32(pixel[1])<<green|uint32(pixel[2])<<blue|uint32(pixel[3])<<alpha)
				}
			}
			offset := y * d.lineBytes
			if !bytes.Equal(row, shadow[offset:offset+rowBytes]) {
				copy(shadow[offset:offset+rowBytes], row)
				copy(destination[offset:offset+rowBytes], row)
			}
		}
	} else {
		if len(d.bandRows) != rotateBand || len(d.bandRows[0]) != rowBytes {
			d.bandRows = make([][]byte, rotateBand)
			for index := range d.bandRows {
				d.bandRows[index] = make([]byte, rowBytes)
			}
		}
		columns := min(width, d.panelHeight)
		drawHeight := min(height, d.panelWidth)
		for x0 := 0; x0 < columns; x0 += rotateBand {
			count := min(rotateBand, columns-x0)
			// Each framebuffer page can be two frames behind the visible page.
			// Compare against this page's own last source frame, not the most
			// recent frame globally, before skipping an unchanged rotated band.
			if d.sourceValid[next] {
				unchanged := true
				for y := 0; y < drawHeight; y++ {
					start := y*imageData.Stride + x0*4
					end := start + count*4
					if !bytes.Equal(imageData.Pix[start:end], sourceShadow[start:end]) {
						unchanged = false
						break
					}
				}
				if unchanged {
					continue
				}
			}
			for y := 0; y < drawHeight; y++ {
				source := imageData.Pix[y*imageData.Stride+x0*4 : y*imageData.Stride+(x0+count)*4]
				copy(sourceShadow[y*imageData.Stride+x0*4:], source)
				offset := (d.panelWidth - 1 - y) * 4
				for index := 0; index < count; index++ {
					pixel := source[index*4 : index*4+4]
					row := d.bandRows[index]
					switch {
					case fast && swap:
						binary.LittleEndian.PutUint32(row[offset:offset+4], uint32(pixel[2])|uint32(pixel[1])<<8|uint32(pixel[0])<<16|uint32(pixel[3])<<24)
					case fast:
						copy(row[offset:offset+4], pixel)
					default:
						binary.LittleEndian.PutUint32(row[offset:offset+4], uint32(pixel[0])<<red|uint32(pixel[1])<<green|uint32(pixel[2])<<blue|uint32(pixel[3])<<alpha)
					}
				}
			}
			for index := 0; index < count; index++ {
				offset := (x0 + index) * d.lineBytes
				row := d.bandRows[index][:rowBytes]
				if bytes.Equal(row, shadow[offset:offset+rowBytes]) {
					continue
				}
				copy(shadow[offset:offset+rowBytes], row)
				copy(destination[offset:offset+rowBytes], row)
			}
		}
	}
	d.sourceValid[next] = true
	d.lastConvert = time.Since(started)
	v := d.variable
	v.Xoffset = 0
	v.Yoffset = uint32(next * d.panelHeight)
	if err := ioctl(d.file.Fd(), fbioPanDisplay, unsafe.Pointer(&v)); err != nil {
		return fmt.Errorf("FBIOPAN_DISPLAY: %w", err)
	}
	d.lastPan = time.Since(started) - d.lastConvert
	d.page = next
	d.variable.Yoffset = v.Yoffset
	return nil
}

func (d *Device) Close() error {
	if d.memory != nil {
		_ = unix.Munmap(d.memory)
		d.memory = nil
	}
	return d.file.Close()
}

func SetBacklight(level int) error {
	level = min(max(level, 0), BacklightMax)
	return os.WriteFile(backlightPath, []byte(strconv.Itoa(level)), 0o644)
}

func Backlight() (int, error) {
	contents, err := os.ReadFile(backlightPath)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(contents)))
}
