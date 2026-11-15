//go:build linux

package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"strings"
	"time"
)

func (r *renderer) drawWorkspaceNotification(canvas *image.RGBA, now time.Time, motion bool) {
	item := r.state.Notification
	if item == nil {
		return
	}
	artwork, _ := notificationArtwork(r.notificationImage, "contain")
	drawPopupArtwork(canvas, artwork)
	drawPopupShade(canvas, 184, 248, .39)
	w, h := canvas.Rect.Dx(), canvas.Rect.Dy()
	left, right := w*5/100, w*95/100
	title, body, footer := 36, 20, 14
	if h >= 600 {
		title, body, footer = 56, 28, 18
	}
	ink := r.notificationPopupInk()
	category := firstNonEmpty(item.Label, titleWords(strings.ToLower(notificationCategory(item))))
	r.drawPopupHeader(canvas, "notification", firstNonEmpty(item.Source, "Notification"), category, ink)
	titleText := firstNonEmpty(item.CameraName, item.Title, notificationFallbackTitle(item))
	titles := limitedPopupLines(titleText, r, title, right-left, 2)
	description := limitedPopupLines(item.Description, r, body, right-left, 2)
	footerY, gap := h*9/10, w*14/1000
	y := footerY - footer - gap - len(description)*(body+6) - gap - (len(titles)-1)*(title+8)
	for _, value := range titles {
		r.text(canvas, left, y, value, title, false, workspaceText)
		y += title + 8
	}
	y += body - title + gap
	for _, value := range description {
		r.text(canvas, left, y, value, body, false, workspaceText)
		y += body + 6
	}
	stamp := strings.Trim(strings.TrimSpace(item.Source)+" · "+r.workspaceNotificationStamp(now), " ·")
	dismiss := "Tap anywhere to dismiss"
	dismissWidth := textWidth(r.faces.regular[footer], dismiss)
	r.text(canvas, right-dismissWidth, footerY, dismiss, footer, false, workspaceMuted)
	r.text(canvas, left, footerY, fitTextToWidth(stamp, r.faces.regular[footer], right-left-dismissWidth-24), footer, false, workspaceMuted)
	barLeft, barRight, barY, thickness := popupNotificationProgressGeometry(w, h)
	if item.ExpiresAtUnixMS > 0 {
		drawTaterTubeProgressBar(canvas, barLeft, barRight, barY, thickness, 0, workspaceControl, ink)
	}
	if motion {
		r.drawFullscreenNotificationProgress(canvas, now, ink)
	}
}

func (r *renderer) drawWorkspaceMusic(canvas *image.RGBA, now time.Time, seconds float64, motion bool) {
	media := r.state.Media
	if media == nil {
		return
	}
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(workspaceBackground), image.Point{}, draw.Src)
	ink := r.menuPalette().primary
	art, content, bars := r.fullscreenMusicLayout(canvas.Rect.Dx(), canvas.Rect.Dy())
	drawMenuRoundedRect(canvas, art, 12, workspacePanel)
	if r.mediaImage != nil {
		drawTaterTubePoster(canvas, art, r.mediaImage, 12)
	} else {
		r.drawMusicVisualizerBackdrop(canvas, art.Inset(30), ink)
	}
	heading, title, artist := 18, 36, 24
	if r.isCrown() {
		heading, title, artist = 24, 48, 30
	}
	header := "Sendspin"
	if group := strings.TrimSpace(media.GroupName); group != "" {
		header += " · " + group
	}
	y := content.Min.Y + heading
	r.text(canvas, content.Min.X, y, fitTextToWidth(header, r.faces.regular[heading], content.Dx()), heading, false, ink)
	state := "Now playing"
	if strings.EqualFold(media.PlaybackState, "paused") {
		state = "Paused"
	}
	y += heading + 18
	r.text(canvas, content.Min.X, y, state, heading, false, workspaceMuted)
	y += title + 16
	for index, line := range wrapTextToWidth(firstNonEmpty(media.Title, "Music"), r.faces.regular[title], content.Dx()) {
		if index >= 2 {
			break
		}
		r.text(canvas, content.Min.X, y, line, title, false, workspaceText)
		y += title + 5
	}
	if name := firstNonEmpty(media.Artist, media.AlbumArtist); name != "" {
		y += artist
		r.text(canvas, content.Min.X, y, fitTextToWidth(name, r.faces.regular[artist], content.Dx()), artist, false, workspaceText)
	}
	if media.Album != "" {
		y += artist + 9
		r.text(canvas, content.Min.X, y, fitTextToWidth(media.Album, r.faces.regular[artist-4], content.Dx()), artist-4, false, workspaceMuted)
	}
	if _, duration := mediaProgress(media, now); duration > 0 {
		line(canvas, content.Min.X, bars.Min.Y-34, content.Max.X, bars.Min.Y-34, 6, workspaceBorder)
	}
	if motion {
		r.drawFullscreenMusicMotion(canvas, now, seconds, ink, art, content, bars)
	}
}

func (r *renderer) drawWorkspaceTaterTube(canvas *image.RGBA, now time.Time, motion bool) {
	media := r.state.TaterTube
	if media == nil {
		return
	}
	ink := r.menuPalette().primary
	drawPopupCompanionBackdrop(canvas, r.taterTubeImage)
	art, content := r.taterTubeLayout(canvas.Rect.Dx(), canvas.Rect.Dy())
	drawMenuRoundedRect(canvas, art, max(16, canvas.Rect.Dx()*17/1000), workspacePanel)
	if r.taterTubeImage != nil {
		drawTaterTubePoster(canvas, art, r.taterTubeImage, max(16, canvas.Rect.Dx()*17/1000))
	} else {
		r.drawTaterTubeArtworkPlaceholder(canvas, art, ink)
	}
	heading, title, detail := 16, 48, 22
	if r.isCrown() {
		heading, title, detail = 20, 64, 28
	}
	side := canvas.Rect.Dx() * 42 / 1000
	mark := image.Rect(content.Min.X, content.Min.Y, content.Min.X+side, content.Min.Y+side)
	drawPopupSourceMark(canvas, mark, "tube", ink)
	y := mark.Min.Y + side/2 + heading/3
	r.text(canvas, mark.Max.X+12, y, fitTextToWidth("Tater Tube · "+firstNonEmpty(media.PlayerName, "Media player"), r.faces.regular[heading], content.Max.X-mark.Max.X-12), heading, false, workspaceMuted)
	y += max(48, canvas.Rect.Dy()/10)
	state := "Now playing"
	if strings.EqualFold(media.PlaybackState, "paused") {
		state = "Paused"
	}
	r.text(canvas, content.Min.X, y, strings.ToUpper(state), heading, false, ink)
	y += title + 16
	lines := limitedPopupLines(taterTubeDisplayTitle(media), r, title, content.Dx(), 2)
	for _, line := range lines {
		r.text(canvas, content.Min.X, y, fitTextToWidth(line, r.faces.regular[title], content.Dx()), title, false, workspaceText)
		y += title + 6
	}
	y += detail + 6
	value := strings.Join(taterTubeDetailParts(media), " · ")
	r.text(canvas, content.Min.X, min(y, content.Max.Y-95), fitTextToWidth(value, r.faces.regular[detail], content.Dx()), detail, false, workspaceMuted)
	if _, duration := taterTubeProgress(media, now); duration > 0 {
		barY, thickness, _, _ := r.taterTubeProgressGeometry(content)
		drawTaterTubeProgressBar(canvas, content.Min.X, content.Max.X, barY, thickness, 0, workspaceBorder, ink)
	}
	if motion {
		r.drawFullscreenTaterTubeProgress(canvas, now, media, ink, content)
	}
}

func (r *renderer) workspaceNotificationStamp(now time.Time) string {
	item := r.state.Notification
	if item == nil || item.CreatedAtUnixMS <= 0 {
		return ""
	}
	age := r.currentTaterUnixMS(now) - item.CreatedAtUnixMS
	if age >= 0 && age < 60000 {
		return "Just now"
	}
	if age >= 60000 && age < 3600000 {
		return fmt.Sprintf("%dm ago", age/60000)
	}
	return r.lastEventTimestamp(item.CreatedAtUnixMS, now)
}

func (r *renderer) workspaceSpectrumLevel(index int, seconds float64) float64 {
	if r.state.Media != nil && strings.EqualFold(r.state.Media.PlaybackState, "paused") {
		return .04
	}
	position := float64(index) * 11 / 23
	left := int(position)
	fraction := position - float64(left)
	return r.musicBinLevel(left, seconds)*(1-fraction) + r.musicBinLevel(min(11, left+1), seconds)*fraction
}

func (r *renderer) drawWorkspaceSpectrum(canvas *image.RGBA, bounds image.Rectangle, seconds float64, ink color.RGBA) {
	const count = 24
	gap := 6
	width := max(2, (bounds.Dx()-(count-1)*gap)/count)
	for index := 0; index < count; index++ {
		level := r.workspaceSpectrumLevel(index, seconds)
		height := max(2, int(math.Min(1, level)*float64(min(48, bounds.Dy()))))
		x := bounds.Min.X + index*(width+gap)
		drawMenuRoundedRect(canvas, image.Rect(x, bounds.Max.Y-height, x+width, bounds.Max.Y), 2, ink)
	}
}
