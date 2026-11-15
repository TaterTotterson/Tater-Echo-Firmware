//go:build linux

package main

import (
	"image"
	"testing"
)

func TestParseDimensions(t *testing.T) {
	width, height, err := parseDimensions("128x96")
	if err != nil || width != 128 || height != 96 {
		t.Fatalf("parseDimensions = %dx%d, %v", width, height, err)
	}
	for _, value := range []string{"", "128", "0x96", "128x0", "5000x96"} {
		if _, _, err := parseDimensions(value); err == nil {
			t.Fatalf("parseDimensions(%q) unexpectedly succeeded", value)
		}
	}
}

func TestScaleNearestPreservesCorners(t *testing.T) {
	source := []byte{
		255, 0, 0, 255, 0, 255, 0, 255,
		0, 0, 255, 255, 255, 255, 255, 255,
	}
	canvas := image.NewRGBA(image.Rect(0, 0, 4, 4))
	scaleNearest(canvas, source, 2, 2, canvas.Bounds())
	checks := map[image.Point][4]uint8{
		{X: 0, Y: 0}: {255, 0, 0, 255},
		{X: 3, Y: 0}: {0, 255, 0, 255},
		{X: 0, Y: 3}: {0, 0, 255, 255},
		{X: 3, Y: 3}: {255, 255, 255, 255},
	}
	for point, expected := range checks {
		actual := canvas.RGBAAt(point.X, point.Y)
		if [4]uint8{actual.R, actual.G, actual.B, actual.A} != expected {
			t.Fatalf("pixel %v = %v, want %v", point, actual, expected)
		}
	}
}

func TestFrameSlicesAreConsecutive(t *testing.T) {
	sequence := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	frameBytes := 4
	for index, want := range []byte{1, 5, 1} {
		frame := index % 2
		got := sequence[frame*frameBytes : (frame+1)*frameBytes]
		if got[0] != want {
			t.Fatalf("frame %d begins %d, want %d", frame, got[0], want)
		}
	}
}
