//go:build linux

package main

import (
	"fmt"
	"image"
	"strings"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

func (r *renderer) drawRookPopupVideoControls(canvas *image.RGBA) {
	video := r.state.Video
	if video == nil {
		return
	}
	drawPopupShade(canvas, 180, 224, .35)
	status := "Paused"
	if video.State == "loading" || r.displayVideoImage == nil {
		status = "Loading"
	}
	if video.Error != "" {
		status = "Unavailable"
	}
	r.drawRookHeader(canvas, "Video", status, false)
	for index, line := range limitedPopupLines(firstNonEmpty(video.Title, "Video"), r, 28, 330, 2) {
		r.centeredText(canvas, 240, 224+index*34, line, 28, false, workspaceText)
	}
	detail := firstNonEmpty(video.Error, video.Subtitle)
	if detail != "" {
		r.centeredText(canvas, 240, 292, fitTextToWidth(detail, r.faces.regular[17], 320), 17, false, workspaceMuted)
	}
	if video.DurationMS > 0 {
		position := min(video.DurationMS, max(video.PositionMS, r.displayVideoPositionMS))
		drawTaterTubeProgressBar(canvas, 112, 368, 328, 5, float64(position)/float64(video.DurationMS), workspaceBorder, r.menuPalette().primary)
		r.text(canvas, 112, 358, formatMediaTime(position), 16, false, workspaceMuted)
		total := formatMediaTime(video.DurationMS)
		r.text(canvas, 368-textWidth(r.faces.regular[16], total), 358, total, 16, false, workspaceMuted)
	}
	play, stop := popupVideoButtonBounds(480, 480)
	if video.State == "paused" && video.Error == "" {
		drawMenuRoundedRect(canvas, play, 26, r.menuPalette().primary)
		r.centeredText(canvas, play.Min.X+play.Dx()/2, 408, "Resume", 18, false, workspaceBackground)
	}
	drawMenuRoundedRect(canvas, stop, 26, workspaceControl)
	r.centeredText(canvas, stop.Min.X+stop.Dx()/2, 408, "Stop", 18, false, workspaceText)
}

func (r *renderer) drawRookVolume(canvas *image.RGBA, percent int) {
	box := image.Rect(80, 174, 400, 306)
	drawMenuRoundedRect(canvas, box, 24, workspacePanel)
	drawMenuRoundedOutline(canvas, box, 24, 1, workspaceBorder)
	r.centeredText(canvas, 240, 210, "Volume", 18, false, workspaceMuted)
	r.centeredText(canvas, 240, 257, fmt.Sprintf("%d%%", percent), 38, false, workspaceText)
	drawTaterTubeProgressBar(canvas, 112, 368, 280, 6, float64(percent)/100, workspaceBorder, r.menuPalette().primary)
}

func rookDirectoryBounds(index int) image.Rectangle {
	if index < 0 || index >= 2 {
		return image.Rectangle{}
	}
	return image.Rect(77, 134+index*94, 403, 218+index*94)
}

func (r *renderer) drawRookPager(canvas *image.RGBA, page, pages int) {
	if pages < 2 {
		return
	}
	for _, next := range []bool{false, true} {
		box := settingsPageButton(480, 480, next, true)
		ink := workspaceText
		if next && page == pages-1 || !next && page == 0 {
			ink = workspaceBorder
		}
		drawMenuRoundedRect(canvas, box, box.Dy()/2, workspacePanel)
		cx, cy, direction := box.Min.X+box.Dx()/2, box.Min.Y+box.Dy()/2, -1
		if next {
			direction = 1
		}
		line(canvas, cx-direction*3, cy-6, cx+direction*3, cy, 2, ink)
		line(canvas, cx+direction*3, cy, cx-direction*3, cy+6, 2, ink)
	}
	r.centeredText(canvas, 240, 415, rookPageLabel(page, pages), 16, false, workspaceMuted)
}

func (r *renderer) drawRookSatChat(canvas *image.RGBA) {
	connectedFill(canvas)
	chat := r.state.SatChat
	if chat != nil && chat.Call != nil {
		if strings.EqualFold(chat.Call.Mode, "video") {
			r.drawWorkspaceSatChatVideo(canvas, 480, 480)
			return
		}
		name := firstNonEmpty(chat.Call.TargetName, chat.Call.CallerName, "Satellite")
		if chat.Call.Target == chat.Self {
			name = firstNonEmpty(chat.Call.CallerName, "Satellite")
		}
		r.drawRookHeader(canvas, "Sat Chat", "", false)
		drawSatChatNetworkGlyph(canvas, 240, 182, 22, r.menuPalette().primary)
		r.centeredText(canvas, 240, 240, fitTextToWidth(name, r.faces.regular[28], 320), 28, false, workspaceText)
		r.centeredText(canvas, 240, 274, fitTextToWidth("Audio call · "+chat.Call.State, r.faces.regular[18], 310), 18, false, workspaceMuted)
		r.drawRookEndCall(canvas)
		return
	}
	r.drawRookHeader(canvas, "Sat Chat", r.directorySubtitle("sat-chat", "Choose a satellite"), true)
	participants := []show.SatChatParticipant{}
	if chat != nil {
		participants = chat.Participants
	}
	if len(participants) == 0 {
		r.centeredText(canvas, 240, 242, "No satellites available", 22, false, workspaceMuted)
		return
	}
	page := r.satChatPageFor(len(participants), 2)
	start := page * 2
	for index, item := range participants[start:min(len(participants), start+2)] {
		box := rookDirectoryBounds(index)
		drawMenuRoundedRect(canvas, box, 14, workspacePanel)
		r.drawDirectoryRowFeedback(canvas, box, "sat-chat", item.ID)
		ink := workspaceText
		if item.Busy {
			ink = workspaceMuted
		}
		drawSatChatVideoGlyph(canvas, 105, box.Min.Y+42, 12, r.menuPalette().primary)
		if !item.Video {
			drawSatChatMicGlyph(canvas, 105, box.Min.Y+42, 10, r.menuPalette().primary)
		}
		r.text(canvas, 132, box.Min.Y+33, fitTextToWidth(firstNonEmpty(item.Name, item.Room, "Satellite"), r.faces.regular[21], 224), 21, false, ink)
		detail := "Audio only"
		if item.Video {
			detail = "Video + audio"
		}
		if item.Room != "" {
			detail = item.Room + " · " + detail
		}
		if item.Busy {
			detail = "Busy"
		}
		if r.directoryRowPending("sat-chat", item.ID) {
			detail = "Starting call…"
		}
		r.text(canvas, 132, box.Min.Y+59, fitTextToWidth(detail, r.faces.regular[15], 224), 15, false, workspaceMuted)
		if !item.Busy {
			circle(canvas, 379, box.Min.Y+42, 3, workspaceHomeStatusColor(true, false, "idle", r.state.DisplayTheme))
		}
	}
	r.drawRookPager(canvas, page, satChatPageCount(len(participants), 2))
	maskOutsideSpotCircle(canvas, rookPanelRadius)
}

func (r *renderer) drawRookEndCall(canvas *image.RGBA) {
	box := spotSatChatEndBounds()
	drawMenuRoundedRect(canvas, box, box.Dy()/2, mix(workspacePanel, r.notificationPopupInk(), .12))
	r.centeredText(canvas, 240, box.Min.Y+box.Dy()/2+6, "End chat", 18, false, workspaceText)
}

func (r *renderer) drawRookCameras(canvas *image.RGBA) {
	connectedFill(canvas)
	if r.state.Cameras != nil && r.state.Cameras.SelectedID != "" {
		r.drawWorkspaceCameraViewer(canvas, true)
		return
	}
	r.drawRookHeader(canvas, "Cameras", r.directorySubtitle("camera", "Choose a camera"), true)
	items := []show.Camera{}
	if r.state.Cameras != nil {
		items = r.state.Cameras.Items
	}
	if len(items) == 0 {
		r.centeredText(canvas, 240, 242, "No cameras available", 22, false, workspaceMuted)
		return
	}
	pages := satChatPageCount(len(items), 2)
	r.cameraPage = max(0, min(pages-1, r.cameraPage))
	start := r.cameraPage * 2
	for index, item := range items[start:min(len(items), start+2)] {
		box := rookDirectoryBounds(index)
		drawMenuRoundedRect(canvas, box, 14, workspacePanel)
		r.drawDirectoryRowFeedback(canvas, box, "camera", item.ID)
		drawWorkspaceSecurityCameraGlyph(canvas, 105, box.Min.Y+42, 12, r.menuPalette().primary)
		r.text(canvas, 132, box.Min.Y+33, fitTextToWidth(firstNonEmpty(item.Name, "Camera"), r.faces.regular[21], 222), 21, false, workspaceText)
		detail := "View only"
		if item.Talkback {
			detail = "Two-way audio"
		}
		if !item.Available {
			detail = "Unavailable"
		}
		if r.directoryRowPending("camera", item.ID) {
			detail = "Opening camera…"
		}
		r.text(canvas, 132, box.Min.Y+59, fitTextToWidth(detail, r.faces.regular[15], 222), 15, false, workspaceMuted)
		if item.Available {
			drawMenuChevron(canvas, 379, box.Min.Y+42, 5, workspaceMuted)
		}
	}
	r.drawRookPager(canvas, r.cameraPage, pages)
	maskOutsideSpotCircle(canvas, rookPanelRadius)
}

func (r *renderer) drawRookCameraControls(canvas *image.RGBA) {
	cameras := r.state.Cameras
	if cameras == nil {
		return
	}
	r.drawRookHeader(canvas, firstNonEmpty(cameras.SelectedName, "Camera"), "Live", true)
	if r.cameraFrameImage == nil {
		message := "Opening camera…"
		if cameras.Error != "" {
			message = cameras.Error
		}
		r.centeredText(canvas, 240, 246, fitTextToWidth(message, r.faces.regular[20], 310), 20, false, workspaceMuted)
	}
	close := r.cameraCloseBounds(480, 480, true, true)
	drawMenuRoundedRect(canvas, close, close.Dy()/2, workspaceControl)
	r.centeredText(canvas, close.Min.X+close.Dx()/2, close.Min.Y+close.Dy()/2+6, "Close", 18, false, workspaceText)
	if cameras.SelectedTalkback {
		talk := cameraTalkBounds(480, 480, true)
		ink, fill := workspaceText, workspaceControl
		label := "Hold to talk"
		if r.cameraTalkPressed {
			label, fill, ink = "Talking…", r.menuPalette().primary, workspaceBackground
		}
		drawMenuRoundedRect(canvas, talk, talk.Dy()/2, fill)
		r.centeredText(canvas, talk.Min.X+talk.Dx()/2, talk.Min.Y+talk.Dy()/2+6, label, 18, false, ink)
	}
}
