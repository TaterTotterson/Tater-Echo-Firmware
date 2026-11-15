//go:build linux

package main

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"strings"
	"time"
)

const popupFadeDuration = 380 * time.Millisecond

// Popup artwork is prepared only when the retained scene changes. The complete
// foreground is contained, never cropped; a dim cover copy fills its letterbox.
// This also preserves transparency in the default Tater logo.
func drawPopupArtwork(canvas *image.RGBA, artwork image.Image) {
	draw.Draw(canvas, canvas.Rect, image.NewUniform(workspaceBackground), image.Point{}, draw.Src)
	if artwork == nil || artwork.Bounds().Empty() {
		return
	}
	background := prepareCoverImage(artwork, canvas.Rect.Size())
	draw.Draw(canvas, canvas.Rect, background, background.Bounds().Min, draw.Over)
	draw.Draw(canvas, canvas.Rect, image.NewUniform(color.NRGBA{R: 17, G: 18, B: 19, A: 196}), image.Point{}, draw.Over)
	drawContain(canvas, canvas.Rect, artwork)
}

// Static shaded overlays, not a live blur or per-frame image filter. The same
// pixels are retained in the GPU atlas and used by the software fallback.
func drawPopupShade(canvas *image.RGBA, top, bottom uint8, bottomStart float64) {
	for y := canvas.Rect.Min.Y; y < canvas.Rect.Max.Y; y++ {
		position := float64(y-canvas.Rect.Min.Y) / float64(max(1, canvas.Rect.Dy()-1))
		topAlpha := float64(top) * math.Max(0, 1-position/.24)
		bottomAlpha := float64(bottom) * math.Max(0, math.Min(1, (position-bottomStart)/(1-bottomStart)))
		alpha := uint8(math.Round(math.Max(topAlpha, bottomAlpha)))
		if alpha > 0 {
			draw.Draw(canvas, image.Rect(canvas.Rect.Min.X, y, canvas.Rect.Max.X, y+1),
				image.NewUniform(color.NRGBA{R: 8, G: 9, B: 10, A: alpha}), image.Point{}, draw.Over)
		}
	}
}

func popupNotificationProgressGeometry(width, height int) (left, right, y, thickness int) {
	return width * 5 / 100, width * 95 / 100, height * 955 / 1000, 4
}

func drawPopupCompanionBackdrop(canvas *image.RGBA, artwork image.Image) {
	draw.Draw(canvas, canvas.Rect, image.NewUniform(workspaceBackground), image.Point{}, draw.Src)
	if artwork != nil {
		prepared := prepareCoverImage(artwork, canvas.Rect.Size())
		draw.Draw(canvas, canvas.Rect, prepared, prepared.Bounds().Min, draw.Over)
	}
	for x := 0; x < canvas.Rect.Dx(); x++ {
		position := float64(x) / float64(max(1, canvas.Rect.Dx()-1))
		alpha := .87
		if position > .43 {
			alpha = .88 - .28*(position-.43)/.57
		}
		draw.Draw(canvas, image.Rect(x, 0, x+1, canvas.Rect.Dy()),
			image.NewUniform(color.NRGBA{R: 17, G: 18, B: 19, A: uint8(alpha * 255)}), image.Point{}, draw.Over)
	}
}

func popupNotificationProgressBounds(width, height int) image.Rectangle {
	left, right, y, thickness := popupNotificationProgressGeometry(width, height)
	return image.Rect(left-2, y-thickness, right+2, y+thickness+1)
}

func (r *renderer) notificationPopupInk() color.RGBA {
	if r.state.Notification != nil && strings.EqualFold(r.state.Notification.Priority, "critical") {
		return color.RGBA{255, 92, 92, 255}
	}
	return r.menuPalette().primary
}

// Small source marks share the workspace's quiet charcoal tile and thin icons.
func drawPopupSourceMark(canvas *image.RGBA, bounds image.Rectangle, kind string, ink color.RGBA) {
	drawMenuRoundedRect(canvas, bounds, max(8, bounds.Dx()/4), mix(workspacePanel, ink, .12))
	cx, cy, size := bounds.Min.X+bounds.Dx()/2, bounds.Min.Y+bounds.Dy()/2, bounds.Dx()/5
	switch kind {
	case "notification":
		circleOutline(canvas, cx, cy-size/3, size*3/4, 1, ink)
		drawMenuRoundedRect(canvas, image.Rect(cx-size, cy, cx+size+1, cy+size/2), 1, ink)
		circle(canvas, cx, cy+size, max(1, size/6), ink)
	case "video":
		drawMenuRoundedOutline(canvas, image.Rect(cx-size, cy-size/2, cx+size, cy+size), 2, 1, ink)
		line(canvas, cx-size, cy-size, cx+size, cy-size*3/2, 1, ink)
		line(canvas, cx-size/3, cy-size, cx-size/2, cy-size*3/2, 1, ink)
	default:
		drawMenuRoundedOutline(canvas, image.Rect(cx-size, cy-size*3/4, cx+size, cy+size/2), 2, 1, ink)
		line(canvas, cx, cy+size/2, cx, cy+size, 1, ink)
		line(canvas, cx-size/2, cy+size, cx+size/2, cy+size, 1, ink)
	}
}

func (r *renderer) drawPopupHeader(canvas *image.RGBA, kind, source, category string, ink color.RGBA) {
	w, h := canvas.Rect.Dx(), canvas.Rect.Dy()
	margin, top, side, size := w*5/100, h*57/1000, w*42/1000, 16
	if h >= 600 {
		size = 22
	}
	mark := image.Rect(margin, top, margin+side, top+side)
	drawPopupSourceMark(canvas, mark, kind, ink)
	baseline := top + side/2 + size/3
	right := w - margin
	category = fitTextToWidth(category, r.faces.regular[size], w/3)
	categoryWidth := textWidth(r.faces.regular[size], category)
	if category != "" {
		r.text(canvas, right-categoryWidth, baseline, category, size, false, workspaceMuted)
		right -= categoryWidth + margin/2
	}
	left := mark.Max.X + w/80
	r.text(canvas, left, baseline, fitTextToWidth(source, r.faces.regular[size], right-left), size, false, workspaceText)
}

func limitedPopupLines(value string, r *renderer, size, width, count int) []string {
	if count <= 0 || width <= 0 {
		return nil
	}
	lines := wrapTextToWidth(strings.TrimSpace(value), r.faces.regular[size], width)
	if len(lines) > count {
		last := fitTextToWidth(strings.Join(lines[count-1:], " "), r.faces.regular[size], width)
		lines = append([]string(nil), lines[:count]...)
		lines[count-1] = last
	}
	return lines
}

func (r *renderer) drawPopupTrackedLabel(canvas *image.RGBA, center, baseline int, value string, size int, ink color.RGBA) int {
	letters := []rune(value)
	tracking := max(1, size/6)
	width := max(0, len(letters)-1) * tracking
	for _, letter := range letters {
		width += textWidth(r.faces.regular[size], string(letter))
	}
	left := center - width/2
	for _, letter := range letters {
		r.text(canvas, left, baseline, string(letter), size, false, ink)
		left += textWidth(r.faces.regular[size], string(letter)) + tracking
	}
	return width
}
