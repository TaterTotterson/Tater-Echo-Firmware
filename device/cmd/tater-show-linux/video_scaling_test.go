//go:build linux

package main

import (
	"bytes"
	"image"
	"image/color"
	"testing"
)

func TestVideoScalingPreservesPixelsOriginsAndPaddedStrides(t *testing.T) {
	for _, sizes := range [][4]int{{9, 7, 13, 11}, {13, 11, 9, 7}, {1280, 960, 640, 480}, {1280, 720, 852, 480}} {
		sw, sh, dw, dh := sizes[0], sizes[1], sizes[2], sizes[3]
		sp := image.NewRGBA(image.Rect(-9, -7, sw+17, sh+19))
		source := sp.SubImage(image.Rect(-3, 2, sw-3, sh+2)).(*image.RGBA)
		for y := source.Rect.Min.Y; y < source.Rect.Max.Y; y++ {
			for x := source.Rect.Min.X; x < source.Rect.Max.X; x++ {
				source.SetRGBA(x, y, color.RGBA{uint8(x*17 + y*3), uint8(x + y*11), uint8(x*5 - y*7), uint8(x*9 + y*13)})
			}
		}
		parent := image.NewRGBA(image.Rect(-11, -13, dw+23, dh+29))
		wantParent := image.NewRGBA(parent.Rect)
		for n := range parent.Pix {
			parent.Pix[n] = 73
			wantParent.Pix[n] = 73
		}
		bounds := image.Rect(3, -2, dw+3, dh-2)
		destination := parent.SubImage(bounds).(*image.RGBA)
		want := wantParent.SubImage(bounds).(*image.RGBA)
		scaleNearestRGBA(destination, source)
		for y := 0; y < dh; y++ {
			for x := 0; x < dw; x++ {
				want.SetRGBA(bounds.Min.X+x, bounds.Min.Y+y, source.RGBAAt(source.Rect.Min.X+x*sw/dw, source.Rect.Min.Y+y*sh/dh))
			}
		}
		if !bytes.Equal(parent.Pix, wantParent.Pix) {
			t.Fatal("scaled pixels or surrounding padding changed", sizes)
		}
	}
}
