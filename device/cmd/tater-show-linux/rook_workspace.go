//go:build linux

package main

import (
	"fmt"
	"image"
	"image/draw"
	"math"
	"strings"
	"time"
)

// Rook shares the workspace's palette, assets and settings, not its rectangular
// layout. Every content rectangle must stay inside the physical round panel.
const rookPanelRadius = 236

func rookContainedArtwork(size image.Point, radius int) image.Rectangle {
	if size.X <= 0 || size.Y <= 0 || radius <= 0 {
		return image.Rectangle{}
	}
	scale := float64(2*radius) / math.Hypot(float64(size.X), float64(size.Y))
	w, h := max(1, int(float64(size.X)*scale)), max(1, int(float64(size.Y)*scale))
	return image.Rect(240-w/2, 240-h/2, 240+(w+1)/2, 240+(h+1)/2)
}

func (r *renderer) drawRookHeader(canvas *image.RGBA, title, subtitle string, back bool) {
	r.centeredText(canvas, 240, 104, fitTextToWidth(title, r.faces.regular[26], 240), 26, false, workspaceText)
	if subtitle != "" {
		r.centeredText(canvas, 240, 126, fitTextToWidth(subtitle, r.faces.regular[16], 290), 16, false, workspaceMuted)
	}
	if back {
		box := settingsBackBounds(true)
		cx, cy := box.Min.X+box.Dx()/2, box.Min.Y+box.Dy()/2
		line(canvas, cx+3, cy-6, cx-3, cy, 2, workspaceText)
		line(canvas, cx-3, cy, cx+3, cy+6, 2, workspaceText)
	}
}

func (r *renderer) drawRookNotification(canvas *image.RGBA, now time.Time, motion bool) {
	n := r.state.Notification
	if n == nil {
		return
	}
	art, _ := notificationArtwork(r.notificationImage, "contain")
	drawPopupCompanionBackdrop(canvas, art)
	if art != nil {
		drawContain(canvas, rookContainedArtwork(art.Bounds().Size(), 228), art)
	}
	drawPopupShade(canvas, 160, 248, .45)
	ink := r.notificationPopupInk()
	r.centeredText(canvas, 240, 95, fitTextToWidth(firstNonEmpty(n.Source, "Notification"), r.faces.regular[17], 270), 17, false, ink)
	title := firstNonEmpty(n.CameraName, n.Title, notificationFallbackTitle(n))
	lines := limitedPopupLines(title, r, 26, 310, 2)
	for index, value := range lines {
		r.centeredText(canvas, 240, 309+index*31, value, 26, false, workspaceText)
	}
	baseline := 309 + len(lines)*31
	for index, value := range limitedPopupLines(n.Description, r, 17, 294, 2) {
		r.centeredText(canvas, 240, baseline+index*23, value, 17, false, workspaceText)
	}
	if n.ExpiresAtUnixMS > 0 {
		fraction := 0.0
		if motion {
			fraction = notificationProgress(n, r.currentTaterUnixMS(now))
		}
		drawTaterTubeProgressBar(canvas, 150, 330, 419, 5, fraction, workspaceControl, ink)
	}
	maskOutsideSpotCircle(canvas, rookPanelRadius)
}

func (r *renderer) drawRookMedia(canvas *image.RGBA, now time.Time, seconds float64, tube, motion bool) {
	art, title, detail, brand, footer := r.mediaImage, "Music", "", "Sendspin", "Playing"
	var progress, duration int64
	if tube {
		m := r.state.TaterTube
		if m == nil {
			return
		}
		art, title, brand = r.taterTubeImage, taterTubeDisplayTitle(m), "Tater Tube"
		detail, footer = taterTubeDetail(m), firstNonEmpty(m.PlayerName, "Media player")
		progress, duration = taterTubeProgress(m, now)
		if strings.EqualFold(m.PlaybackState, "paused") {
			footer += " · Paused"
		}
	} else {
		m := r.state.Media
		if m == nil {
			return
		}
		title, detail = firstNonEmpty(m.Title, "Music"), firstNonEmpty(m.Artist, m.AlbumArtist, m.GroupName)
		progress, duration = mediaProgress(m, now)
		if strings.EqualFold(m.PlaybackState, "paused") {
			footer = "Paused"
		}
	}
	drawPopupCompanionBackdrop(canvas, art)
	r.centeredText(canvas, 240, 86, brand, 18, false, r.menuPalette().primary)
	box := image.Rect(149, 115, 331, 297)
	drawMenuRoundedRect(canvas, box, 18, workspacePanel)
	if art != nil {
		drawTaterTubePoster(canvas, box, art, 18)
	} else if tube {
		r.drawTaterTubeArtworkPlaceholder(canvas, box, r.menuPalette().primary)
	} else {
		r.drawMusicVisualizerBackdrop(canvas, box.Inset(15), r.menuPalette().primary)
	}
	r.centeredText(canvas, 240, 338, fitTextToWidth(title, r.faces.regular[26], 340), 26, false, workspaceText)
	r.centeredText(canvas, 240, 363, fitTextToWidth(detail, r.faces.regular[18], 330), 18, false, workspaceMuted)
	if duration > 0 {
		drawTaterTubeProgressBar(canvas, 110, 370, 381, 5, 0, workspaceBorder, r.menuPalette().primary)
		if motion {
			r.drawRookMediaProgress(canvas, progress, duration)
		}
	}
	r.centeredText(canvas, 240, 432, fitTextToWidth(footer, r.faces.regular[15], 250), 15, false, workspaceMuted)
	maskOutsideSpotCircle(canvas, rookPanelRadius)
}

func (r *renderer) drawRookMediaProgress(canvas *image.RGBA, progress, duration int64) {
	if duration <= 0 {
		return
	}
	drawTaterTubeProgressBar(canvas, 110, 370, 381, 5, float64(progress)/float64(duration), workspaceBorder, r.menuPalette().primary)
	r.text(canvas, 110, 406, formatMediaTime(progress), 16, false, workspaceMuted)
	end := formatMediaTime(duration)
	r.text(canvas, 370-textWidth(r.faces.regular[16], end), 406, end, 16, false, workspaceMuted)
}

func (r *renderer) drawRookTimer(canvas *image.RGBA, now time.Time, seconds float64) {
	draw.Draw(canvas, canvas.Rect, image.NewUniform(workspaceBackground), image.Point{}, draw.Src)
	timer := r.state.Timer
	if timer == nil {
		return
	}
	ink := r.menuPalette().primary
	label := firstNonEmpty(timer.Label, timer.Name, "Timer")
	r.drawRookHeader(canvas, label, "", false)
	value := formatDuration(timerRemaining(timer, time.UnixMilli(r.currentTaterUnixMS(now))))
	if timer.Ringing {
		value = "00:00"
		r.drawSpotStateRim(canvas, ink)
	}
	size := fittingTextSize(value, r.faces.regular, 96, 56, 340)
	r.centeredText(canvas, 240, 274, value, size, false, workspaceText)
	caption := "Timer running"
	if timer.Ringing {
		caption = "Your timer is finished."
	}
	r.centeredText(canvas, 240, 316, caption, 18, false, workspaceMuted)
	box := spotTimerStopBounds()
	drawMenuRoundedRect(canvas, box, box.Dy()/2, workspaceControl)
	if r.timerPressed {
		drawMenuRoundedOutline(canvas, box, box.Dy()/2, 2, ink)
	}
	r.centeredText(canvas, 240, box.Min.Y+box.Dy()/2+6, "Stop timer", 18, false, workspaceText)
	maskOutsideSpotCircle(canvas, rookPanelRadius)
}

func (r *renderer) drawRookMenu(canvas *image.RGBA) {
	draw.Draw(canvas, canvas.Rect, image.NewUniform(workspaceBackground), image.Point{}, draw.Src)
	r.drawRookHeader(canvas, firstNonEmpty(r.state.Room, "Rook"), "", true)
	for index, item := range workspaceMenuItems {
		box := actionMenuBounds(480, 480, index, true)
		drawMenuRoundedRect(canvas, box, 14, workspacePanel)
		if r.menuPressed == item.kind {
			drawMenuRoundedOutline(canvas, box, 14, 2, r.menuPalette().primary)
		}
		cx := box.Min.X + box.Dx()/2
		r.drawWorkspaceGlyph(canvas, item.kind, cx, box.Min.Y+box.Dy()/3, 13, r.menuPalette().primary)
		r.centeredText(canvas, cx, box.Max.Y-12, item.label, 17, false, workspaceText)
	}
	box := actionMenuBroadcastBounds(480, 480, true)
	r.centeredText(canvas, 240, box.Min.Y+box.Dy()/2+5, "Hold to broadcast", 16, false, workspaceMuted)
	maskOutsideSpotCircle(canvas, rookPanelRadius)
}

// Keep the setup content factual: do not show a Wi-Fi network unless the
// device actually started its setup AP. USB provisioning stays available.
func (r *renderer) drawRookSetup(canvas *image.RGBA) {
	draw.Draw(canvas, canvas.Rect, image.NewUniform(workspaceBackground), image.Point{}, draw.Src)
	r.drawRookHeader(canvas, "Connect to Tater", "", false)
	lines := []string{"Connect USB to your computer", "Open Tater", "Choose Add Satellite", "Waiting for provisioning"}
	if strings.HasPrefix(r.state.Room, "Tater-Setup-") {
		lines = []string{"Join the Wi-Fi network", r.state.Room, "Then open on your phone", "192.168.4.1"}
	}
	for index, text := range lines {
		size, ink := 17, workspaceMuted
		if index%2 == 1 {
			size, ink = 24, workspaceText
		}
		r.centeredText(canvas, 240, 182+index*48, fitTextToWidth(text, r.faces.regular[size], 310), size, false, ink)
	}
	maskOutsideSpotCircle(canvas, rookPanelRadius)
}

func rookPageLabel(page, total int) string { return fmt.Sprintf("%d / %d", page+1, total) }
