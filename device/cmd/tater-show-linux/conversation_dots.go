//go:build linux

package main

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"sync"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/gpucompositor"
)

type thinkingSpriteKey struct {
	width int
	ink   color.RGBA
}

var thinkingSpriteCache sync.Map

func thinkingDotSprites(width int, ink color.RGBA) [thinkingDotSteps]*image.RGBA {
	key := thinkingSpriteKey{width, ink}
	if cached, ok := thinkingSpriteCache.Load(key); ok {
		return cached.([thinkingDotSteps]*image.RGBA)
	}
	var sprites [thinkingDotSteps]*image.RGBA
	for step := range thinkingDotSteps {
		tile := image.NewRGBA(image.Rect(0, 0, thinkingDotTile, thinkingDotTile))
		radius := float64(width) * .0065 * (.8 + .36*float64(step)/float64(thinkingDotSteps-1))
		thinkingDotSprite(tile, radius, ink)
		sprites[step] = tile
	}
	cached, _ := thinkingSpriteCache.LoadOrStore(key, sprites)
	return cached.([thinkingDotSteps]*image.RGBA)
}

const (
	thinkingDotSteps = 16
	thinkingDotTile  = 48
	// Reserve one strip inside the existing, bounded 1024-high text atlas.
	conversationAtlasTextLimit = 960
)

func thinkingDotPulse(seconds float64, index int) float64 {
	// Smooth, staggered breathing rather than the old large flashing halos.
	phase := seconds/1.8 - float64(index)/6
	return math.Pow(.5-.5*math.Cos(2*math.Pi*phase), 1.6)
}

func thinkingDotSprite(tile *image.RGBA, radius float64, ink color.RGBA) {
	cx, cy := float64(tile.Rect.Dx())/2, float64(tile.Rect.Dy())/2
	for y := 0; y < tile.Rect.Dy(); y++ {
		for x := 0; x < tile.Rect.Dx(); x++ {
			distance := math.Hypot(float64(x)+.5-cx, float64(y)+.5-cy)
			core := math.Max(0, math.Min(1, radius+.5-distance))
			glow := .12 * math.Exp(-math.Pow(distance/(radius*1.5), 2))
			alpha := uint8(math.Round(255 * math.Min(1, core+glow)))
			// RGBA pixels must be premultiplied for both draw.Over and the GPU.
			tile.SetRGBA(x, y, color.RGBA{uint8(uint32(ink.R) * uint32(alpha) / 255), uint8(uint32(ink.G) * uint32(alpha) / 255), uint8(uint32(ink.B) * uint32(alpha) / 255), alpha})
		}
	}
}

func (r *renderer) conversationInk(phase string) color.RGBA {
	if phase == "thinking" || phase == "working" {
		return r.menuPalette().primary
	}
	return conversationAccent(phase, r.state.DisplayTheme)
}

func (r *renderer) appendThinkingDotAtlas(state *conversationWordState) {
	if state.atlas == nil {
		return
	}
	old := state.atlas
	state.textBounds = old.Rect
	height := (old.Rect.Dy() + thinkingDotTile + 63) / 64 * 64
	state.atlas = image.NewRGBA(image.Rect(0, 0, max(1024, old.Rect.Dx()), height))
	draw.Draw(state.atlas, old.Rect, old, old.Rect.Min, draw.Src)
	width, _ := r.showSize()
	if r.spotScreen {
		width = 1280
	}
	for step, tile := range thinkingDotSprites(width, r.menuPalette().primary) {
		bounds := image.Rect(step*thinkingDotTile, old.Rect.Dy(), (step+1)*thinkingDotTile, old.Rect.Dy()+thinkingDotTile)
		draw.Draw(state.atlas, bounds, tile, image.Point{}, draw.Src)
		state.dotSprites[step] = bounds
	}
	state.upload = true
}

func (r *renderer) gpuConversationText(now time.Time, phase string) (*gpucompositor.TextFrame, bool) {
	state := &r.gpuScene.words
	frame, moving := state.frame(now)
	if frame == nil || (phase != "thinking" && phase != "working") {
		return frame, moving
	}
	width, height := r.showSize()
	gap, baseline := width*285/10000, height*72/100
	if r.spotScreen {
		gap, baseline = 34, 360
	}
	seconds := now.Sub(r.animationStart).Seconds()
	if r.animationStart.IsZero() {
		seconds = float64(now.UnixMilli()%1800) / 1000
	}
	for index := 0; index < 3; index++ {
		pulse := thinkingDotPulse(seconds, index)
		step := int(math.Round(pulse * (thinkingDotSteps - 1)))
		frame.Quads = append(frame.Quads, gpucompositor.TextQuad{
			Atlas: state.dotSprites[step], X: float32(width/2 + (index-1)*gap - thinkingDotTile/2),
			Y: float32(baseline - thinkingDotTile/2), Opacity: float32(.3 + .7*pulse),
		})
	}
	return frame, true
}

func drawRookThinkingDots(canvas *image.RGBA, seconds float64, ink color.RGBA) {
	sprites := thinkingDotSprites(1280, ink)
	for index := 0; index < 3; index++ {
		pulse := thinkingDotPulse(seconds, index)
		tile := sprites[int(math.Round(pulse*(thinkingDotSteps-1)))]
		mask := image.NewUniform(color.Alpha{A: uint8(math.Round(255 * (.3 + .7*pulse)))})
		left := 240 + (index-1)*34 - thinkingDotTile/2
		bounds := image.Rect(left, 360-thinkingDotTile/2, left+thinkingDotTile, 360+thinkingDotTile/2)
		draw.DrawMask(canvas, bounds, tile, image.Point{}, mask, image.Point{}, draw.Over)
	}
}

func drawThinkingDots(canvas *image.RGBA, centerX, centerY int, seconds float64, ink color.RGBA) {
	width := canvas.Rect.Dx()
	sprites := thinkingDotSprites(width, ink)
	for index := 0; index < 3; index++ {
		pulse := thinkingDotPulse(seconds, index)
		tile := sprites[int(math.Round(pulse*(thinkingDotSteps-1)))]
		mask := image.NewUniform(color.Alpha{A: uint8(math.Round(255 * (.3 + .7*pulse)))})
		left := centerX + (index-1)*width*285/10000 - thinkingDotTile/2
		bounds := image.Rect(left, centerY-thinkingDotTile/2, left+thinkingDotTile, centerY+thinkingDotTile/2)
		draw.DrawMask(canvas, bounds, tile, image.Point{}, mask, image.Point{}, draw.Over)
	}
}
