//go:build linux

package linuxscreen

import (
	"image"
	"image/color"
	"os"
	"testing"
)

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
