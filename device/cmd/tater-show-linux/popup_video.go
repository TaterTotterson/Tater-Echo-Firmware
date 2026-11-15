//go:build linux

package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"strings"
)

func (r *renderer) popupVideoOverlayVisible() bool {
	video := r.state.Video
	return video != nil && (r.displayVideoImage == nil || !strings.EqualFold(video.State, "playing") || video.Error != "")
}

func popupVideoButtonBounds(width, height int) (image.Rectangle, image.Rectangle) {
	if width == 480 && height == 480 {
		return image.Rect(128, 376, 250, 428), image.Rect(262, 376, 352, 428)
	}
	buttonHeight, buttonWidth, gap := max(58, height/10), max(160, width*16/100), max(14, width*14/1000)
	centerX, top := width/2, height*55/100
	return image.Rect(centerX-gap/2-buttonWidth, top, centerX-gap/2, top+buttonHeight),
		image.Rect(centerX+gap/2, top, centerX+gap/2+buttonWidth, top+buttonHeight)
}

func (r *renderer) popupVideoControlsKey() string {
	video := r.state.Video
	if video == nil {
		return ""
	}
	// During playback there is no permanent HUD. In particular the changing
	// position must not rebuild an invisible atlas on every decoded picture.
	if !r.popupVideoOverlayVisible() {
		return fmt.Sprintf("%q|playing|%q|%t", video.SessionID, r.state.DisplayTheme, r.state.Muted)
	}
	return fmt.Sprintf("%q|%q|%q|%q|%q|%q|%t|%t|%d|%d|%t", video.SessionID, video.State,
		video.Title, video.Subtitle, video.Error, r.state.DisplayTheme, r.state.Muted,
		r.displayVideoImage != nil, max(video.PositionMS, r.displayVideoPositionMS), video.DurationMS, video.Live)
}

func (r *renderer) popupVideoControls(bounds image.Rectangle) *workspaceVideoControlCache {
	cache := &r.playbackControls
	key := fmt.Sprint(bounds) + r.popupVideoControlsKey()
	if cache.layer != nil && cache.key == key {
		return cache
	}
	if cache.layer == nil || cache.layer.Rect != bounds {
		cache.layer = image.NewRGBA(bounds)
	} else {
		clear(cache.layer.Pix)
	}
	cache.regions = nil
	if r.popupVideoOverlayVisible() {
		r.drawPopupVideoControls(cache.layer)
		cache.regions = []image.Rectangle{bounds}
	}
	if r.state.Muted {
		box := image.Rect(bounds.Dx()-168, 16, bounds.Dx()-16, 52)
		if r.spotScreen {
			box = image.Rect(166, 140, 314, 176)
		}
		drawMenuRoundedRect(cache.layer, box, 12, color.RGBA{101, 27, 38, 235})
		r.text(cache.layer, box.Min.X+16, box.Min.Y+25, "MIC MUTED", 16, true, color.RGBA{255, 191, 197, 255})
		if !r.popupVideoOverlayVisible() {
			cache.regions = append(cache.regions, box)
		}
	}
	cache.key = key
	return cache
}

func (r *renderer) drawPopupVideoControls(canvas *image.RGBA) {
	if r.spotScreen {
		r.drawRookPopupVideoControls(canvas)
		return
	}
	video := r.state.Video
	if video == nil {
		return
	}
	w, h := canvas.Rect.Dx(), canvas.Rect.Dy()
	ink := r.menuPalette().primary
	drawPopupShade(canvas, 194, 220, .38)
	label, eyebrow := "Paused", "Take your time"
	loading := r.displayVideoImage == nil || strings.EqualFold(video.State, "loading")
	if loading {
		label, eyebrow = "Loading", "Getting things ready"
	}
	if video.Error != "" {
		label, eyebrow = "Unavailable", "Video unavailable"
	}
	r.drawPopupHeader(canvas, "video", "Video", label, ink)
	titleSize, bodySize := 42, 18
	if h >= 600 {
		titleSize, bodySize = 66, 24
	}
	title := firstNonEmpty(video.Title, "Video")
	lines := limitedPopupLines(title, r, titleSize, w*80/100, 2)
	baseline := h*44/100 - (len(lines)-1)*(titleSize+4)
	if h >= 600 {
		r.centeredText(canvas, w/2, baseline-titleSize-22, strings.ToUpper(eyebrow), 20, false, ink)
	}
	for _, value := range lines {
		r.centeredText(canvas, w/2, baseline, value, titleSize, false, workspaceText)
		baseline += titleSize + 4
	}
	subtitle := video.Subtitle
	if video.Error != "" {
		subtitle = video.Error
	}
	if subtitle != "" {
		r.centeredText(canvas, w/2, h*50/100, fitTextToWidth(subtitle, r.faces.regular[bodySize], w*80/100), bodySize, false, workspaceText)
	}
	play, stop := popupVideoButtonBounds(w, h)
	if strings.EqualFold(video.State, "paused") && video.Error == "" {
		drawMenuRoundedRect(canvas, play, max(12, w/80), ink)
		drawVideoPlayGlyph(canvas, play.Min.X+play.Dy()/2, play.Min.Y+play.Dy()/2, play.Dy()/8, workspaceBackground)
		r.centeredText(canvas, play.Min.X+play.Dx()/2+12, play.Min.Y+play.Dy()/2+bodySize/3, "Resume", bodySize, false, workspaceBackground)
	}
	drawMenuRoundedRect(canvas, stop, max(12, w/80), workspacePanel)
	drawMenuRoundedOutline(canvas, stop, max(12, w/80), 1, workspaceBorder)
	center := image.Pt(stop.Min.X+stop.Dy()/2, stop.Min.Y+stop.Dy()/2)
	drawMenuRoundedOutline(canvas, image.Rect(center.X-6, center.Y-6, center.X+7, center.Y+7), 1, 1, workspaceText)
	r.centeredText(canvas, stop.Min.X+stop.Dx()/2+10, center.Y+bodySize/3, "Stop", bodySize, false, workspaceText)
	left, right, barY := w*5/100, w*95/100, h*88/100
	if video.Live {
		r.text(canvas, left, barY, "LIVE", bodySize, false, workspaceText)
	} else if video.DurationMS > 0 {
		position := min(video.DurationMS, max(video.PositionMS, r.displayVideoPositionMS))
		r.text(canvas, left, barY-18, formatMediaTime(position), bodySize, false, workspaceText)
		total := formatMediaTime(video.DurationMS)
		r.text(canvas, right-textWidth(r.faces.regular[bodySize], total), barY-18, total, bodySize, false, workspaceText)
		drawTaterTubeProgressBar(canvas, left, right, barY, max(4, h/160), float64(position)/float64(video.DurationMS), workspaceBorder, ink)
	}
}

func (r *renderer) renderPopupVideo(canvas *image.RGBA) {
	var picture image.Image
	if r.displayVideoImage != nil {
		picture = r.displayVideoImage
	}
	drawWorkspaceVideoPicture(canvas, canvas.Rect, picture)
	controls := r.popupVideoControls(canvas.Rect)
	for _, region := range controls.regions {
		draw.Draw(canvas, region, controls.layer, region.Min, draw.Over)
	}
}
