//go:build linux

// Package linuxscreen owns the Checkers, Crown, Athena, and Rook framebuffers. The framebuffer
// code is derived from TECHO5 (MIT); Checkers rotates its portrait panel,
// while Rook draws directly to its square panel.
package linuxscreen

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"log"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/gpucompositor"
	"golang.org/x/sys/unix"
)

const (
	fbioGetVScreenInfo = 0x4600
	fbioGetFScreenInfo = 0x4602
	fbioPanDisplay     = 0x4606
	BacklightMax       = 255
)

var backlightPaths = []string{
	"/sys/class/backlight/backlight-lcd0/brightness", // Athena mainline
	"/sys/class/leds/lcd-backlight/brightness",       // vendor Show kernels
}

func backlightPath() string {
	for _, path := range backlightPaths {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return backlightPaths[len(backlightPaths)-1]
}

func backlightHardwareMax(path string) int {
	contents, err := os.ReadFile(strings.TrimSuffix(path, "/brightness") + "/max_brightness")
	if err != nil {
		return BacklightMax
	}
	value, err := strconv.Atoi(strings.TrimSpace(string(contents)))
	if err != nil || value <= 0 {
		return BacklightMax
	}
	return value
}

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
	regionFullPending       []bool
	transientDirty          []bool
	bandRows                [][]byte
	directRow               []byte
	lastConvert, lastPan    time.Duration
	gpu                     *gpucompositor.Compositor
	gpuFrame                []byte
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
	d.shifts = pixelShifts(&d.variable)
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
	if d.gpuGeometrySupported() {
		compositor, gpuErr := gpucompositor.Open()
		if gpuErr != nil {
			log.Printf("GPU compositor unavailable; using CPU framebuffer path: %v", gpuErr)
		} else if compositor != nil {
			d.gpu = compositor
			log.Printf("GPU compositor active for %dx%d frames; partial UI updates remain on CPU", d.canvas.Rect.Dx(), d.canvas.Rect.Dy())
			if compositor.SupportsScenes() {
				log.Printf("GPU retained UI active: text/menu crossfades, voice glow, and thinking indicators")
			}
		}
	}
	return d, nil
}

func (d *Device) Canvas() *image.RGBA { return d.canvas }

// pixelShifts returns where RGBA bytes belong in a framebuffer pixel. DRM's
// fbdev emulation exposes XRGB8888 with a zero-length transparency field at
// offset zero. The unused X byte is actually at bit 24; placing canvas alpha
// at the reported offset would otherwise force the blue channel to 0xff.
func pixelShifts(variable *varInfo) [4]uint {
	shifts := [4]uint{
		uint(variable.Red[0]),
		uint(variable.Green[0]),
		uint(variable.Blue[0]),
		uint(variable.Transp[0]),
	}
	if variable.Transp[1] == 0 && shifts[0] < 24 && shifts[1] < 24 && shifts[2] < 24 {
		shifts[3] = 24
	}
	return shifts
}

func (d *Device) LastPresentTimings() (time.Duration, time.Duration) {
	return d.lastConvert, d.lastPan
}

func (d *Device) SupportsGPUScenes() bool {
	return d.gpuGeometrySupported() && d.gpu.SupportsScenes()
}

func (d *Device) SupportsAnimatedText() bool {
	return d.SupportsGPUScenes() && d.gpu.SupportsAnimatedText()
}

func (d *Device) SupportsGPUVideo() bool {
	return d.SupportsGPUScenes() && d.gpu.SupportsVideoScenes()
}

// PresentGPUScene receives into normal RAM before changing an offscreen page.
// A rejected/failed helper leaves the framebuffer intact and returns false so
// the caller can render the current snapshot through the CPU path immediately.
func (d *Device) PresentGPUScene(source []byte, effects gpucompositor.Effects) (bool, error) {
	if !d.SupportsGPUScenes() {
		return false, nil
	}
	started := time.Now()
	if len(d.gpuFrame) != d.panelWidth*d.panelHeight*4 {
		d.gpuFrame = make([]byte, d.panelWidth*d.panelHeight*4)
	}
	if err := d.gpu.CompositeScene(source, d.gpuFrame, effects); err != nil {
		log.Printf("GPU scene failed; continuing with CPU renderer: %v", err)
		_ = d.gpu.Close()
		d.gpu = nil
		return false, nil
	}
	next := d.page
	if d.pages > 1 {
		next = (next + 1) % d.pages
	}
	copy(d.memory[next*d.pageBytes:(next+1)*d.pageBytes], d.gpuFrame)
	// Scene pixels never enter the CPU canvas. Invalidate every page so the
	// next normal/video presentation restores a complete, coherent canvas.
	for index := range d.sourceValid {
		d.sourceValid[index] = false
	}
	if next < len(d.shadow) {
		d.shadow[next] = nil
	}
	d.lastConvert = time.Since(started)
	return true, d.pan(next, started)
}

func (d *Device) Size() (int, int) { return d.canvas.Rect.Dx(), d.canvas.Rect.Dy() }

func (d *Device) String() string {
	return fmt.Sprintf("%dx%d panel, %d page(s), %d bytes/line, rgba at %d/%d/%d/%d",
		d.panelWidth, d.panelHeight, d.pages, d.lineBytes,
		d.shifts[0], d.shifts[1], d.shifts[2], d.shifts[3])
}

const rotateBand = 8

func (d *Device) gpuGeometrySupported() bool {
	known := (d.panelWidth == 480 && (d.panelHeight == 960 || d.panelHeight == 480)) || (d.panelWidth == 800 && d.panelHeight == 1280)
	return known && d.canvas != nil && d.canvas.Rect.Dx() == d.panelHeight &&
		d.canvas.Rect.Dy() == d.panelWidth && d.canvas.Stride == d.panelHeight*4 &&
		d.lineBytes == d.panelWidth*4 && d.shifts == [4]uint{16, 8, 0, 24}
}

func shouldUseGPU(regions []image.Rectangle, bounds image.Rectangle) bool {
	if bounds.Empty() {
		return false
	}
	area := 0
	for _, region := range regions {
		clipped := region.Intersect(bounds)
		if clipped == bounds {
			return true
		}
		area += clipped.Dx() * clipped.Dy()
	}
	// The pbuffer path always transfers a complete frame. Below half a screen,
	// the cache-friendly CPU region writer is cheaper and avoids needless pipe
	// traffic. Overlaps can only select the safe GPU path a little early.
	return area >= bounds.Dx()*bounds.Dy()/2
}

func (d *Device) compositeGPU(destination []byte, next int, updateCaches bool) bool {
	if d.gpu == nil || len(destination) < d.panelWidth*d.panelHeight*4 {
		return false
	}
	if len(d.gpuFrame) != d.panelWidth*d.panelHeight*4 {
		d.gpuFrame = make([]byte, d.panelWidth*d.panelHeight*4)
	}
	if err := d.gpu.Composite(d.canvas.Pix, d.gpuFrame); err != nil {
		log.Printf("GPU compositor failed; continuing with CPU framebuffer path: %v", err)
		_ = d.gpu.Close()
		d.gpu = nil
		d.sourceValid[next] = false
		return false
	}
	// Pipes into uncached framebuffer memory are materially slower on MT8163.
	// Receive into reusable normal RAM, then issue one contiguous page write.
	// A short helper response can therefore never leave half a visible frame.
	copy(destination[:len(d.gpuFrame)], d.gpuFrame)
	if updateCaches {
		copy(d.shadow[next], d.gpuFrame)
		copy(d.sourceShadow[next], d.canvas.Pix)
		d.sourceValid[next] = true
		d.transientDirty[next] = false
	} else {
		d.transientDirty[next] = true
	}
	return true
}

func (d *Device) pan(next int, started time.Time) error {
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
		d.transientDirty = make([]bool, max(d.pages, 1))
	}
	if len(d.transientDirty) == max(d.pages, 1) && d.transientDirty[next] {
		d.sourceValid[next] = false
		// The cached packed pixels describe the frame before the transient
		// write. Re-seed them from the actual page so Present cannot mistake an
		// old matching cache row for pixels that are already on screen.
		d.shadow[next] = nil
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
	if d.gpu != nil && d.gpuGeometrySupported() {
		if d.compositeGPU(destination, next, true) {
			d.lastConvert = time.Since(started)
			return d.pan(next, started)
		}
		shadow = d.shadow[next]
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
	if len(d.transientDirty) == max(d.pages, 1) {
		d.transientDirty[next] = false
	}
	d.lastConvert = time.Since(started)
	return d.pan(next, started)
}

// PresentRegions updates only the supplied logical-canvas rectangles before
// flipping the next framebuffer page. Native Show renderers use it after their
// static UI has been cached: rotating a complete frame just to move a weather
// glyph, spectrum bar, or reply rim wastes the frame budget. A full-frame
// request is remembered for every framebuffer page so page flipping cannot
// reveal stale static content on following frames.
func (d *Device) PresentRegions(regions []image.Rectangle) error {
	return d.presentRegions(regions, true)
}

// PresentTransientRegions writes rapidly changing content, such as a video
// frame, without also copying it into the per-page comparison caches. Those
// caches are valuable for the mostly static UI but are pure memory bandwidth
// overhead for pixels that will be replaced by the next camera frame.
func (d *Device) PresentTransientRegions(regions []image.Rectangle) error {
	return d.presentRegions(regions, false)
}

func (d *Device) presentRegions(regions []image.Rectangle, updateCaches bool) error {
	started := time.Now()
	next := d.page
	if d.pages > 1 {
		next = (d.page + 1) % d.pages
	}
	destination := d.memory[next*d.pageBytes : (next+1)*d.pageBytes]
	imageData := d.canvas
	width, height := imageData.Rect.Dx(), imageData.Rect.Dy()
	canvasBounds := image.Rect(0, 0, min(width, d.panelHeight), min(height, d.panelWidth))
	red, green, blue, alpha := d.shifts[0], d.shifts[1], d.shifts[2], d.shifts[3]
	fast := green == 8 && alpha == 24 && ((red == 0 && blue == 16) || (red == 16 && blue == 0))
	swap := red == 16
	if d.shadow == nil {
		d.shadow = make([][]byte, max(d.pages, 1))
		d.sourceShadow = make([][]byte, max(d.pages, 1))
		d.sourceValid = make([]bool, max(d.pages, 1))
		d.transientDirty = make([]bool, max(d.pages, 1))
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
	if len(d.regionFullPending) != max(d.pages, 1) {
		d.regionFullPending = make([]bool, max(d.pages, 1))
		for index := range d.regionFullPending {
			d.regionFullPending[index] = true
		}
	}
	if len(d.transientDirty) != max(d.pages, 1) {
		d.transientDirty = make([]bool, max(d.pages, 1))
	}

	clipped := make([]image.Rectangle, 0, len(regions))
	fullRequested := false
	for _, region := range regions {
		region = region.Intersect(canvasBounds)
		if region.Empty() {
			continue
		}
		if region == canvasBounds {
			fullRequested = true
		}
		clipped = append(clipped, region)
	}
	if fullRequested {
		for index := range d.regionFullPending {
			d.regionFullPending[index] = true
		}
	}
	if !d.sourceValid[next] || d.regionFullPending[next] || (updateCaches && d.transientDirty[next]) {
		clipped = []image.Rectangle{canvasBounds}
		d.regionFullPending[next] = false
		// A page that has not yet received the static call backdrop, or whose
		// uncached video pixels must be replaced by normal UI, needs one complete
		// cached update. Later video frames can return to the transient path.
		updateCaches = true
		d.transientDirty[next] = false
	}
	if len(clipped) == 0 {
		d.lastConvert, d.lastPan = 0, 0
		return nil
	}
	if d.gpu != nil && d.gpuGeometrySupported() && shouldUseGPU(clipped, canvasBounds) {
		if d.compositeGPU(destination, next, updateCaches) {
			d.lastConvert = time.Since(started)
			return d.pan(next, started)
		}
		// The staging buffer kept the framebuffer untouched; retry this update as
		// one complete CPU frame so every page remains coherent after fallback.
		clipped = []image.Rectangle{canvasBounds}
		updateCaches = true
		shadow = d.shadow[next]
		sourceShadow = d.sourceShadow[next]
		d.transientDirty[next] = false
	}

	if d.panelWidth == d.panelHeight {
		for _, region := range clipped {
			rowBytes := region.Dx() * 4
			if len(d.directRow) < rowBytes {
				d.directRow = make([]byte, rowBytes)
			}
			for y := region.Min.Y; y < region.Max.Y; y++ {
				from := y*imageData.Stride + region.Min.X*4
				source := imageData.Pix[from : from+rowBytes]
				row := d.directRow[:rowBytes]
				for x := 0; x < rowBytes; x += 4 {
					pixel := source[x : x+4]
					switch {
					case fast && swap:
						row[x+0] = pixel[2]
						row[x+1] = pixel[1]
						row[x+2] = pixel[0]
						row[x+3] = pixel[3]
					case fast:
						copy(row[x:x+4], pixel)
					default:
						packed := uint32(pixel[0])<<red | uint32(pixel[1])<<green | uint32(pixel[2])<<blue | uint32(pixel[3])<<alpha
						binary.LittleEndian.PutUint32(row[x:x+4], packed)
					}
				}
				offset := y*d.lineBytes + region.Min.X*4
				copy(destination[offset:offset+rowBytes], row)
				if updateCaches {
					copy(shadow[offset:offset+rowBytes], row)
					copy(sourceShadow[from:from+rowBytes], source)
				}
			}
		}
	} else {
		if len(d.bandRows) != rotateBand || len(d.bandRows[0]) != d.panelWidth*4 {
			d.bandRows = make([][]byte, rotateBand)
			for index := range d.bandRows {
				d.bandRows[index] = make([]byte, d.panelWidth*4)
			}
		}
		for _, region := range clipped {
			// A canvas column becomes one contiguous framebuffer row. Build several
			// columns together in ordinary RAM, then copy each changed row segment to
			// the mapped framebuffer once. Direct pixel-at-a-time writes to that memory
			// are exceptionally slow on Crown; this is the same cache-friendly shape
			// used by the full-frame TECHO5-derived rotation path above.
			panelStart := (d.panelWidth - region.Max.Y) * 4
			panelEnd := (d.panelWidth - region.Min.Y) * 4
			for x0 := region.Min.X; x0 < region.Max.X; x0 += rotateBand {
				count := min(rotateBand, region.Max.X-x0)
				switch {
				case fast && swap:
					for y := region.Min.Y; y < region.Max.Y; y++ {
						source := imageData.Pix[y*imageData.Stride+x0*4 : y*imageData.Stride+(x0+count)*4]
						offset := (d.panelWidth - 1 - y) * 4
						for index := 0; index < count; index++ {
							pixel := source[index*4 : index*4+4]
							row := d.bandRows[index]
							row[offset+0] = pixel[2]
							row[offset+1] = pixel[1]
							row[offset+2] = pixel[0]
							row[offset+3] = pixel[3]
						}
					}
				case fast:
					for y := region.Min.Y; y < region.Max.Y; y++ {
						source := imageData.Pix[y*imageData.Stride+x0*4 : y*imageData.Stride+(x0+count)*4]
						offset := (d.panelWidth - 1 - y) * 4
						for index := 0; index < count; index++ {
							copy(d.bandRows[index][offset:offset+4], source[index*4:index*4+4])
						}
					}
				default:
					for y := region.Min.Y; y < region.Max.Y; y++ {
						source := imageData.Pix[y*imageData.Stride+x0*4 : y*imageData.Stride+(x0+count)*4]
						offset := (d.panelWidth - 1 - y) * 4
						for index := 0; index < count; index++ {
							pixel := source[index*4 : index*4+4]
							packed := uint32(pixel[0])<<red | uint32(pixel[1])<<green | uint32(pixel[2])<<blue | uint32(pixel[3])<<alpha
							binary.LittleEndian.PutUint32(d.bandRows[index][offset:offset+4], packed)
						}
					}
				}
				for index := 0; index < count; index++ {
					rowOffset := (x0 + index) * d.lineBytes
					start := rowOffset + panelStart
					end := rowOffset + panelEnd
					row := d.bandRows[index][panelStart:panelEnd]
					copy(destination[start:end], row)
					if updateCaches {
						copy(shadow[start:end], row)
					}
				}
			}
			// Preserve the source-page cache with one contiguous copy per logical
			// row. The former per-pixel copy dominated Crown's partial-frame path.
			if updateCaches {
				for y := region.Min.Y; y < region.Max.Y; y++ {
					start := y*imageData.Stride + region.Min.X*4
					end := y*imageData.Stride + region.Max.X*4
					copy(sourceShadow[start:end], imageData.Pix[start:end])
				}
			}
		}
	}
	if updateCaches {
		d.sourceValid[next] = true
		d.transientDirty[next] = false
	} else {
		d.transientDirty[next] = true
	}
	d.lastConvert = time.Since(started)
	return d.pan(next, started)
}

func (d *Device) Close() error {
	if d.gpu != nil {
		_ = d.gpu.Close()
		d.gpu = nil
	}
	if d.memory != nil {
		_ = unix.Munmap(d.memory)
		d.memory = nil
	}
	return d.file.Close()
}

func SetBacklight(level int) error {
	level = min(max(level, 0), BacklightMax)
	path := backlightPath()
	hardware := (level*backlightHardwareMax(path) + BacklightMax/2) / BacklightMax
	return os.WriteFile(path, []byte(strconv.Itoa(hardware)), 0o644)
}

func Backlight() (int, error) {
	path := backlightPath()
	contents, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	hardware, err := strconv.Atoi(strings.TrimSpace(string(contents)))
	if err != nil {
		return 0, err
	}
	maximum := backlightHardwareMax(path)
	return (hardware*BacklightMax + maximum/2) / maximum, nil
}
