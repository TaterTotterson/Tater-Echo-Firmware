//go:build linux

package main

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"

	xdraw "golang.org/x/image/draw"
)

// The primary transparent wordmark from the main Tater app's images directory.
//
//go:embed assets/tater-logo-primary.png
var taterPrimaryLogoPNG []byte

var taterPrimaryLogo = decodePrimaryLogo()

func decodePrimaryLogo() image.Image {
	logo, err := png.Decode(bytes.NewReader(taterPrimaryLogoPNG))
	if err != nil {
		panic("decode embedded Tater primary logo: " + err.Error())
	}
	return logo
}

func visibleLogoBounds(source image.Image) image.Rectangle {
	visible := image.Rectangle{}
	for y := source.Bounds().Min.Y; y < source.Bounds().Max.Y; y++ {
		for x := source.Bounds().Min.X; x < source.Bounds().Max.X; x++ {
			_, _, _, alpha := source.At(x, y).RGBA()
			if alpha < 256 { // ignore almost-transparent antialiasing fringes
				continue
			}
			point := image.Rect(x, y, x+1, y+1)
			if visible.Empty() {
				visible = point
			} else {
				visible = visible.Union(point)
			}
		}
	}
	return visible
}

func buildConnectingBase(width, height int) *image.RGBA {
	base := image.NewRGBA(image.Rect(0, 0, width, height))
	fillGradient(base, color.RGBA{3, 3, 5, 255}, color.RGBA{14, 7, 4, 255})
	// One very soft glow: mostly black, with the home screen's orange near the mark.
	cx, cy := float64(width)/2, float64(height)*.46
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			dx := (float64(x) - cx) / (float64(width) * .53)
			dy := (float64(y) - cy) / (float64(height) * .75)
			falloff := math.Max(0, 1-dx*dx-dy*dy)
			falloff *= falloff
			offset := base.PixOffset(x, y)
			base.Pix[offset] += uint8(29 * falloff)
			base.Pix[offset+1] += uint8(10 * falloff)
		}
	}

	trimmed := visibleLogoBounds(taterPrimaryLogo)
	if trimmed.Empty() {
		return base
	}
	scale := math.Min(float64(width)*.84/float64(trimmed.Dx()), float64(height)*.80/float64(trimmed.Dy()))
	logoWidth := max(1, int(math.Round(float64(trimmed.Dx())*scale)))
	logoHeight := max(1, int(math.Round(float64(trimmed.Dy())*scale)))
	logo := image.NewRGBA(image.Rect(0, 0, logoWidth, logoHeight))
	xdraw.CatmullRom.Scale(logo, logo.Bounds(), taterPrimaryLogo, trimmed, draw.Src, nil)
	left := (width - logoWidth) / 2
	top := int(float64(height)*.45) - logoHeight/2
	draw.Draw(base, image.Rect(left, top, left+logoWidth, top+logoHeight), logo, image.Point{}, draw.Over)
	return base
}

func (r *renderer) drawConnecting(canvas *image.RGBA, seconds float64) {
	width, height := canvas.Rect.Dx(), canvas.Rect.Dy()
	if r.connectingBase == nil || r.connectingBase.Rect.Dx() != width || r.connectingBase.Rect.Dy() != height {
		r.connectingBase = buildConnectingBase(width, height)
	}
	draw.Draw(canvas, canvas.Bounds(), r.connectingBase, image.Point{}, draw.Src)
	r.centeredText(canvas, width/2, height-39, "Connecting to Tater", 20, true, color.RGBA{208, 212, 218, 255})
	for index := 0; index < 3; index++ {
		alpha := uint8(75)
		if int(seconds*2)%3 == index {
			alpha = 230
		}
		circle(canvas, width/2-18+index*18, height-17, 3, color.RGBA{255, 132, 48, alpha})
	}
}
