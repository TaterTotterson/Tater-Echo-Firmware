//go:build linux

package linuxscreen

import (
	"image"
	"image/color"
	"os"
	"testing"
)

func TestPixelShiftsPlacesAlphaInUnusedXRGBByte(t *testing.T) {
	variable := varInfo{
		Red:    [3]uint32{16, 8, 0},
		Green:  [3]uint32{8, 8, 0},
		Blue:   [3]uint32{0, 8, 0},
		Transp: [3]uint32{0, 0, 0},
	}
	if got, want := pixelShifts(&variable), ([4]uint{16, 8, 0, 24}); got != want {
		t.Fatalf("pixelShifts(XRGB8888) = %v, want %v", got, want)
	}
}

func TestPixelShiftsKeepsReportedAlphaChannel(t *testing.T) {
	variable := varInfo{
		Red:    [3]uint32{0, 8, 0},
		Green:  [3]uint32{8, 8, 0},
		Blue:   [3]uint32{16, 8, 0},
		Transp: [3]uint32{24, 8, 0},
	}
	if got, want := pixelShifts(&variable), ([4]uint{0, 8, 16, 24}); got != want {
		t.Fatalf("pixelShifts(RGBA8888) = %v, want %v", got, want)
	}
}

func TestPresentTracksEachFramebufferPageSeparately(t *testing.T) {
	file, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	const panelWidth, panelHeight, pages = 8, 16, 3
	const lineBytes = panelWidth * 4
	const pageBytes = lineBytes * panelHeight
	d := &Device{
		file: file, memory: make([]byte, pageBytes*pages),
		panelWidth: panelWidth, panelHeight: panelHeight,
		lineBytes: lineBytes, pageBytes: pageBytes, pages: pages,
		shifts: [4]uint{0, 8, 16, 24},
		canvas: image.NewRGBA(image.Rect(0, 0, panelHeight, panelWidth)),
	}
	paint := func(ink color.RGBA) {
		for y := 0; y < panelWidth; y++ {
			for x := 0; x < panelHeight; x++ {
				d.canvas.SetRGBA(x, y, ink)
			}
		}
	}
	// /dev/null rejects FBIOPAN_DISPLAY, but the rotation and page write have
	// already happened by then. Simulate the three-page flip sequence.
	paint(color.RGBA{R: 255, A: 255})
	d.page = 0
	_ = d.Present() // page 1: red
	paint(color.RGBA{B: 255, A: 255})
	d.page = 1
	_ = d.Present() // page 2: blue
	d.page = 2
	_ = d.Present() // page 0: blue
	if d.memory[pageBytes+0] != 255 {
		t.Fatal("setup did not leave page 1 on the old red frame")
	}
	d.page = 0
	_ = d.Present() // page 1 must update from red to blue, despite unchanged global frame
	for page := 0; page < pages; page++ {
		for _, x := range []int{0, 7, 8, 15} {
			for _, y := range []int{0, 7} {
				offset := page*pageBytes + x*lineBytes + (panelWidth-1-y)*4
				pixel := d.memory[offset : offset+4]
				if pixel[0] != 0 || pixel[1] != 0 || pixel[2] != 255 || pixel[3] != 255 {
					t.Fatalf("page %d source (%d,%d): got %v, want opaque blue", page, x, y, pixel)
				}
			}
		}
	}
}

func TestSquarePanelKeepsNativeOrientationAndPerPageUpdates(t *testing.T) {
	file, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	const side, pages = 4, 2
	const rowBytes = side * 4
	const pageBytes = side * rowBytes
	d := &Device{
		file: file, memory: make([]byte, pageBytes*pages),
		panelWidth: side, panelHeight: side, lineBytes: rowBytes,
		pageBytes: pageBytes, pages: pages,
		shifts: [4]uint{0, 8, 16, 24},
		canvas: image.NewRGBA(image.Rect(0, 0, side, side)),
	}
	d.canvas.SetRGBA(1, 2, color.RGBA{R: 255, A: 255})
	_ = d.Present()
	if got := d.memory[pageBytes+2*rowBytes+1*4 : pageBytes+2*rowBytes+1*4+4]; got[0] != 255 || got[3] != 255 {
		t.Fatalf("square panel rotated pixel (1,2): %v", got)
	}
	d.canvas.SetRGBA(1, 2, color.RGBA{B: 255, A: 255})
	_ = d.Present()
	d.page = 0
	_ = d.Present()
	if got := d.memory[pageBytes+2*rowBytes+1*4 : pageBytes+2*rowBytes+1*4+4]; got[2] != 255 || got[0] != 0 {
		t.Fatalf("square panel did not update prior page: %v", got)
	}
}

func TestPresentRegionsRotatesOnlyRequestedCanvasPixels(t *testing.T) {
	file, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	const panelWidth, panelHeight = 8, 16
	const lineBytes = panelWidth * 4
	const pageBytes = lineBytes * panelHeight
	d := &Device{
		file: file, memory: make([]byte, pageBytes),
		panelWidth: panelWidth, panelHeight: panelHeight,
		lineBytes: lineBytes, pageBytes: pageBytes, pages: 1,
		shifts: [4]uint{0, 8, 16, 24},
		canvas: image.NewRGBA(image.Rect(0, 0, panelHeight, panelWidth)),
	}
	for y := 0; y < panelWidth; y++ {
		for x := 0; x < panelHeight; x++ {
			d.canvas.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	_ = d.PresentRegions([]image.Rectangle{d.canvas.Rect})

	d.canvas.SetRGBA(3, 2, color.RGBA{B: 255, A: 255})
	d.canvas.SetRGBA(5, 4, color.RGBA{G: 255, A: 255})
	_ = d.PresentRegions([]image.Rectangle{image.Rect(3, 2, 4, 3)})
	pixelAt := func(x, y int) []byte {
		offset := x*lineBytes + (panelWidth-1-y)*4
		return d.memory[offset : offset+4]
	}
	if got := pixelAt(3, 2); got[0] != 0 || got[1] != 0 || got[2] != 255 || got[3] != 255 {
		t.Fatalf("requested regional pixel = %v, want opaque blue", got)
	}
	if got := pixelAt(5, 4); got[0] != 255 || got[1] != 0 || got[2] != 0 || got[3] != 255 {
		t.Fatalf("pixel outside requested region changed: %v", got)
	}
}

func TestGPUSelectionUsesOnlyLargeOrFullRegions(t *testing.T) {
	bounds := image.Rect(0, 0, 960, 480)
	if !shouldUseGPU([]image.Rectangle{bounds}, bounds) {
		t.Fatal("full frame did not select GPU compositor")
	}
	if shouldUseGPU([]image.Rectangle{image.Rect(0, 0, 200, 100)}, bounds) {
		t.Fatal("small partial update selected full-frame GPU compositor")
	}
	if !shouldUseGPU([]image.Rectangle{image.Rect(0, 0, 960, 240)}, bounds) {
		t.Fatal("half-frame update did not select GPU compositor")
	}
	// Cameras with talkback and Sat Chat share this maximum viewport, with
	// their controls outside it. Do not accidentally bypass Checkers' existing
	// GPU compositor just because the steady video update is a partial region.
	if !shouldUseGPU([]image.Rectangle{image.Rect(12, 86, 948, 410)}, bounds) {
		t.Fatal("large camera/Sat Chat frame did not select GPU compositor")
	}
	if shouldUseGPU([]image.Rectangle{image.Rect(-20, -20, 20, 20)}, bounds) {
		t.Fatal("mostly out-of-bounds update selected GPU compositor")
	}
}

func TestTransientRegionSkipsCachesAndNormalUpdateRepairsWholePage(t *testing.T) {
	file, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	const panelWidth, panelHeight = 8, 16
	const lineBytes = panelWidth * 4
	const pageBytes = lineBytes * panelHeight
	d := &Device{
		file: file, memory: make([]byte, pageBytes),
		panelWidth: panelWidth, panelHeight: panelHeight,
		lineBytes: lineBytes, pageBytes: pageBytes, pages: 1,
		shifts: [4]uint{0, 8, 16, 24},
		canvas: image.NewRGBA(image.Rect(0, 0, panelHeight, panelWidth)),
	}
	paint := func(ink color.RGBA) {
		for y := 0; y < panelWidth; y++ {
			for x := 0; x < panelHeight; x++ {
				d.canvas.SetRGBA(x, y, ink)
			}
		}
	}
	pixelAt := func(x, y int) []byte {
		offset := x*lineBytes + (panelWidth-1-y)*4
		return d.memory[offset : offset+4]
	}

	paint(color.RGBA{R: 255, A: 255})
	_ = d.PresentRegions([]image.Rectangle{d.canvas.Rect})
	d.canvas.SetRGBA(3, 2, color.RGBA{B: 255, A: 255})
	d.canvas.SetRGBA(5, 4, color.RGBA{G: 255, A: 255})
	_ = d.PresentTransientRegions([]image.Rectangle{image.Rect(3, 2, 4, 3)})
	if got := pixelAt(3, 2); got[2] != 255 || got[0] != 0 {
		t.Fatalf("transient requested pixel = %v, want blue", got)
	}
	if got := pixelAt(5, 4); got[0] != 255 || got[1] != 0 {
		t.Fatalf("transient write changed pixel outside its region: %v", got)
	}
	if !d.transientDirty[0] {
		t.Fatal("transient write did not mark comparison caches dirty")
	}

	// Even though the caller reports only the first pixel, a normal update must
	// restore the complete page because the transient frame bypassed its caches.
	d.canvas.SetRGBA(3, 2, color.RGBA{R: 255, A: 255})
	_ = d.PresentRegions([]image.Rectangle{image.Rect(3, 2, 4, 3)})
	if got := pixelAt(5, 4); got[0] != 0 || got[1] != 255 || got[2] != 0 {
		t.Fatalf("normal update did not repair uncached page: %v", got)
	}
	if d.transientDirty[0] {
		t.Fatal("normal update left comparison caches dirty")
	}

	// The older whole-frame API must also replace transient pixels even when
	// its desired static row happens to match the pre-video packed cache.
	d.canvas.SetRGBA(3, 2, color.RGBA{B: 255, A: 255})
	_ = d.PresentTransientRegions([]image.Rectangle{image.Rect(3, 2, 4, 3)})
	d.canvas.SetRGBA(3, 2, color.RGBA{R: 255, A: 255})
	_ = d.Present()
	if got := pixelAt(3, 2); got[0] != 255 || got[1] != 0 || got[2] != 0 {
		t.Fatalf("whole-frame present left transient pixel behind: %v", got)
	}
}

func TestSquareTransientRegionWritesOnlyRequestedPixels(t *testing.T) {
	file, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	const side = 6
	const rowBytes = side * 4
	const pageBytes = side * rowBytes
	d := &Device{
		file: file, memory: make([]byte, pageBytes),
		panelWidth: side, panelHeight: side, lineBytes: rowBytes,
		pageBytes: pageBytes, pages: 1, shifts: [4]uint{0, 8, 16, 24},
		canvas: image.NewRGBA(image.Rect(0, 0, side, side)),
	}
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			d.canvas.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	_ = d.PresentRegions([]image.Rectangle{d.canvas.Rect})
	d.canvas.SetRGBA(1, 2, color.RGBA{B: 255, A: 255})
	d.canvas.SetRGBA(4, 4, color.RGBA{G: 255, A: 255})
	_ = d.PresentTransientRegions([]image.Rectangle{image.Rect(1, 2, 2, 3)})
	pixel := func(x, y int) []byte {
		offset := y*rowBytes + x*4
		return d.memory[offset : offset+4]
	}
	if got := pixel(1, 2); got[2] != 255 || got[0] != 0 {
		t.Fatalf("square transient requested pixel = %v, want blue", got)
	}
	if got := pixel(4, 4); got[0] != 255 || got[1] != 0 {
		t.Fatalf("square transient write changed outside pixel: %v", got)
	}
}
