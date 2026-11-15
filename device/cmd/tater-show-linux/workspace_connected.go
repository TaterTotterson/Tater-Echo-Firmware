//go:build linux

package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"strings"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

// Directory geometry is shared by drawing and input: capability icons are
// descriptive, not separate call-mode buttons.
func connectedListBounds(width, height int) image.Rectangle {
	margin, top, bottom := 28, 116, 68
	if height >= 600 {
		margin, top, bottom = 42, 164, 94
	}
	return image.Rect(margin, top, width-margin, height-bottom)
}

func connectedRowBounds(bounds image.Rectangle, index, total int) image.Rectangle {
	if index < 0 || index >= total {
		return image.Rectangle{}
	}
	return image.Rect(bounds.Min.X, bounds.Min.Y+bounds.Dy()*index/total, bounds.Max.X, bounds.Min.Y+bounds.Dy()*(index+1)/total)
}

func connectedPreviousBounds(width, height int) image.Rectangle {
	return image.Rect(width-286, height-60, width-142, height-12)
}

func connectedNextBounds(width, height int) image.Rectangle {
	return image.Rect(width-138, height-60, width-28, height-12)
}

func connectedFill(canvas *image.RGBA) {
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(workspaceBackground), image.Point{}, draw.Src)
}

func (r *renderer) connectedHeader(canvas *image.RGBA, title, subtitle string, close image.Rectangle, spot bool) {
	if spot {
		r.centeredText(canvas, 240, 99, title, 26, false, workspaceText)
		if subtitle != "" {
			r.centeredText(canvas, 240, 122, fitTextToWidth(subtitle, r.faces.regular[12], 264), 12, false, workspaceMuted)
		}
	} else {
		size, subSize, left, top := 30, 19, 36, 55
		if canvas.Rect.Dy() >= 600 {
			size, subSize, left, top = 42, 26, 48, 78
		}
		right := close.Min.X
		if close.Empty() {
			right = canvas.Rect.Max.X - left
		}
		r.text(canvas, left, top, fitTextToWidth(title, r.faces.regular[size], right-left-24), size, false, workspaceText)
		if subtitle != "" {
			r.text(canvas, left, top+subSize+16, fitTextToWidth(subtitle, r.faces.regular[subSize], canvas.Rect.Dx()-left*2), subSize, false, workspaceMuted)
		}
	}
	if !close.Empty() {
		drawClimateCloseGlyph(canvas, close, workspaceMuted)
	}
}

func connectedPanel(canvas *image.RGBA, bounds image.Rectangle) {
	drawMenuRoundedRect(canvas, bounds, 14, workspacePanel)
	drawMenuRoundedOutline(canvas, bounds, 14, 1, workspaceBorder)
}

func drawWorkspaceSecurityCameraGlyph(canvas *image.RGBA, cx, cy, size int, ink color.RGBA) {
	// Quiet outline CCTV icon, distinct from a video-call capability glyph.
	drawMenuRoundedOutline(canvas, image.Rect(cx-size, cy-size*2/3, cx+size, cy+size/4), 3, 2, ink)
	line(canvas, cx, cy+size/4, cx, cy+size, 2, ink)
	line(canvas, cx-size/2, cy+size, cx+size/2, cy+size, 2, ink)
}

func connectedDivider(canvas *image.RGBA, row image.Rectangle, last bool) {
	if !last {
		line(canvas, row.Min.X+1, row.Max.Y, row.Max.X-2, row.Max.Y, 1, workspaceBorder)
	}
}

func (r *renderer) connectedPagination(canvas *image.RGBA, start, end, total, page, pages int, spot bool) {
	if total == 0 {
		return
	}
	width, height := canvas.Rect.Dx(), canvas.Rect.Dy()
	if spot {
		if pages <= 1 {
			return
		}
		for index, bounds := range []image.Rectangle{cameraPreviousBounds(width, height, true), cameraNextBounds(width, height, true)} {
			ink, label := workspaceMuted, "Previous"
			if index == 1 {
				label = "Next"
			}
			if index == 0 && page == 0 || index == 1 && page == pages-1 {
				ink = workspaceBorder
			}
			r.centeredText(canvas, bounds.Min.X+bounds.Dx()/2, bounds.Min.Y+27, label, 14, false, ink)
		}
		return
	}
	labelSize := 17
	if height >= 600 {
		labelSize = 22
	}
	r.text(canvas, connectedListBounds(width, height).Min.X+8, height-28, fmt.Sprintf("%d–%d of %d", start+1, end, total), labelSize, false, workspaceMuted)
	if pages <= 1 {
		return
	}
	for index, bounds := range []image.Rectangle{connectedPreviousBounds(width, height), connectedNextBounds(width, height)} {
		ink, label := r.menuPalette().primary, "Previous"
		if index == 1 {
			label = "Next"
		}
		if index == 0 && page == 0 || index == 1 && page == pages-1 {
			ink = workspaceMuted
			ink = mix(workspaceBackground, ink, .45)
		}
		r.centeredText(canvas, bounds.Min.X+bounds.Dx()/2, height-28, label, labelSize, false, ink)
	}
}

func (r *renderer) drawWorkspaceSatChat(canvas *image.RGBA) {
	connectedFill(canvas)
	width, height := canvas.Rect.Dx(), canvas.Rect.Dy()
	chat := r.state.SatChat
	if chat != nil && chat.Call != nil {
		r.connectedHeader(canvas, "Sat Chat", "", image.Rectangle{}, false)
		call := chat.Call
		name := firstNonEmpty(call.TargetName, call.CallerName, "Satellite")
		if call.Target == chat.Self {
			name = firstNonEmpty(call.CallerName, "Satellite")
		}
		r.centeredText(canvas, width/2, height*43/100, fitTextToWidth(name, r.faces.regular[34], width-100), 34, false, workspaceText)
		r.centeredText(canvas, width/2, height*53/100, "Audio call · "+firstNonEmpty(call.State, "Connecting"), 20, false, workspaceMuted)
		end := r.satChatEndBounds(width, height)
		drawMenuRoundedRect(canvas, end, 12, color.RGBA{120, 38, 40, 255})
		r.centeredText(canvas, width/2, end.Min.Y+end.Dy()*2/3, "End Sat Chat", 20, false, workspaceText)
		return
	}
	r.connectedHeader(canvas, "Sat Chat", r.directorySubtitle("sat-chat", "Choose a satellite to start a call"), satChatCloseBounds(width), false)
	participants := []show.SatChatParticipant{}
	if chat != nil {
		participants = chat.Participants
	}
	layout := satChatDirectoryLayoutFor(width, height)
	connectedPanel(canvas, layout.content)
	if len(participants) == 0 {
		r.centeredText(canvas, width/2, height/2, "No satellites available", 22, false, workspaceMuted)
		return
	}
	perPage := r.satChatPageSize(width, height)
	page := r.satChatPageFor(len(participants), perPage)
	start, end := page*perPage, min(len(participants), (page+1)*perPage)
	for index, participant := range participants[start:end] {
		row := layout.participantBounds(index)
		r.drawDirectoryRowFeedback(canvas, row, "sat-chat", participant.ID)
		connectedDivider(canvas, row, index == perPage-1)
		nameSize, subSize, iconSize := 24, 18, 13
		if height >= 600 {
			nameSize, subSize, iconSize = 34, 24, 19
		}
		cy := row.Min.Y + row.Dy()/2
		nameInk, status := workspaceText, color.RGBA{110, 182, 134, 255}
		if participant.Busy {
			nameInk, status = mix(workspacePanel, workspaceMuted, .65), workspaceMuted
		}
		circle(canvas, row.Min.X+30, cy, 5, status)
		left := row.Min.X + 64
		r.text(canvas, left, cy-4, fitTextToWidth(firstNonEmpty(participant.Name, participant.Room, "Satellite"), r.faces.regular[nameSize], row.Dx()-230), nameSize, false, nameInk)
		detail := "Audio only"
		if participant.Video {
			detail = "Video + audio"
		}
		if participant.Room != "" {
			detail = participant.Room + " · " + detail
		}
		if participant.Busy {
			detail += " · Busy"
		}
		if r.directoryRowPending("sat-chat", participant.ID) {
			detail = "Starting call…"
		}
		r.text(canvas, left, cy+subSize+7, fitTextToWidth(detail, r.faces.regular[subSize], row.Dx()-230), subSize, false, workspaceMuted)
		if participant.Audio {
			drawSatChatMicGlyph(canvas, row.Max.X-90, cy, iconSize, workspaceMuted)
		}
		if participant.Video {
			drawSatChatVideoGlyph(canvas, row.Max.X-150, cy, iconSize, workspaceMuted)
		}
		if !participant.Busy {
			drawMenuChevron(canvas, row.Max.X-30, cy, 8, workspaceText)
		}
	}
	r.connectedPagination(canvas, start, end, len(participants), page, satChatPageCount(len(participants), perPage), false)
}

func (r *renderer) drawWorkspaceCameras(canvas *image.RGBA, spot bool) {
	if spot {
		r.drawRookCameras(canvas)
		return
	}
	connectedFill(canvas)
	width, height := canvas.Rect.Dx(), canvas.Rect.Dy()
	cameras := r.state.Cameras
	selected := cameras != nil && cameras.SelectedID != ""
	close := r.cameraCloseBounds(width, height, spot, selected)
	if selected {
		r.drawWorkspaceCameraViewer(canvas, spot)
		return
	}
	r.connectedHeader(canvas, "Cameras", r.directorySubtitle("camera", "Choose a camera to view"), close, spot)
	layout := cameraDirectoryLayoutFor(width, height, spot)
	connectedPanel(canvas, layout.content)
	items := []show.Camera{}
	if cameras != nil {
		items = cameras.Items
	}
	if len(items) == 0 {
		r.centeredText(canvas, width/2, height/2, "No cameras available", 18, false, workspaceMuted)
		return
	}
	perPage := cameraPageSize(width, height, spot)
	pages := satChatPageCount(len(items), perPage)
	r.cameraPage = max(0, min(r.cameraPage, pages-1))
	start, end := r.cameraPage*perPage, min(len(items), (r.cameraPage+1)*perPage)
	for index, item := range items[start:end] {
		row := layout.cardBounds(index)
		r.drawDirectoryRowFeedback(canvas, row, "camera", item.ID)
		connectedDivider(canvas, row, index == perPage-1)
		nameSize, subSize, iconSize, left := 24, 18, 18, row.Min.X+124
		if height >= 600 {
			nameSize, subSize, iconSize, left = 34, 24, 25, row.Min.X+148
		}
		if spot {
			nameSize, subSize, iconSize, left = 16, 11, 11, row.Min.X+54
		}
		cy := row.Min.Y + row.Dy()/2
		ink, status := workspaceText, color.RGBA{110, 182, 134, 255}
		if !item.Available {
			ink, status = mix(workspacePanel, workspaceMuted, .65), workspaceMuted
		}
		circle(canvas, row.Min.X+func() int {
			if spot {
				return 10
			}
			return 30
		}(), cy, func() int {
			if spot {
				return 3
			}
			return 5
		}(), status)
		iconX := row.Min.X + 80
		if spot {
			iconX = row.Min.X + 32
		}
		if item.Doorbell {
			drawMenuRoundedOutline(canvas, image.Rect(iconX-iconSize/2, cy-iconSize, iconX+iconSize/2, cy+iconSize), 5, 2, workspaceMuted)
			circleOutline(canvas, iconX, cy+iconSize/3, max(2, iconSize/5), 1, workspaceMuted)
			circle(canvas, iconX, cy-iconSize/2, 2, workspaceMuted)
		} else {
			drawWorkspaceSecurityCameraGlyph(canvas, iconX, cy, iconSize, workspaceMuted)
		}
		textWidth := row.Max.X - left - 44
		r.text(canvas, left, cy-3, fitTextToWidth(firstNonEmpty(item.Name, "Camera"), r.faces.regular[nameSize], textWidth), nameSize, false, ink)
		parts := []string{}
		if item.Room != "" {
			parts = append(parts, item.Room)
		}
		if item.Doorbell {
			parts = append(parts, "Doorbell")
		}
		if !item.Available {
			parts = append(parts, "Unavailable")
		} else if item.Talkback {
			parts = append(parts, "Two-way audio")
		} else {
			parts = append(parts, "View only")
		}
		if r.directoryRowPending("camera", item.ID) {
			parts = []string{"Opening camera…"}
		}
		r.text(canvas, left, cy+subSize+5, fitTextToWidth(strings.Join(parts, " · "), r.faces.regular[subSize], textWidth), subSize, false, workspaceMuted)
		if item.Available {
			drawMenuChevron(canvas, row.Max.X-22, cy, 6, workspaceText)
		}
	}
	r.connectedPagination(canvas, start, end, len(items), r.cameraPage, pages, spot)
	if spot {
		maskOutsideSpotCircle(canvas, 236)
	}
}

func (r *renderer) drawWorkspaceCameraViewer(canvas *image.RGBA, spot bool) {
	r.drawCameraVideoFrame(canvas)
}

func (r *renderer) drawWorkspaceCameraControls(canvas *image.RGBA, spot bool) {
	if spot {
		r.drawRookCameraControls(canvas)
		return
	}
	width, height := canvas.Rect.Dx(), canvas.Rect.Dy()
	cameras := r.state.Cameras
	if cameras == nil {
		return
	}
	back := cameraBackBounds(width, height, spot)
	if !spot {
		glass := color.RGBA{17, 18, 19, 210}
		drawMenuRoundedRect(canvas, back, 12, glass)
		drawMenuRoundedRect(canvas, r.cameraCloseBounds(width, height, false, true), 12, glass)
	}
	cx, cy := back.Min.X+back.Dx()/2, back.Min.Y+back.Dy()/2
	line(canvas, cx+10, cy, cx-10, cy, 2, workspaceMuted)
	line(canvas, cx-10, cy, cx-2, cy-8, 2, workspaceMuted)
	line(canvas, cx-10, cy, cx-2, cy+8, 2, workspaceMuted)
	drawClimateCloseGlyph(canvas, r.cameraCloseBounds(width, height, spot, true), workspaceMuted)
	label := firstNonEmpty(cameras.SelectedName, "Camera")
	if cameras.SelectedRoom != "" {
		label += " · " + cameras.SelectedRoom
	}
	size, baseline := 24, 52
	if height >= 600 {
		size, baseline = 34, 76
	}
	if spot {
		size, baseline = 14, 101
	}
	labelWidth := width - back.Max.X*2 - 12
	if !spot {
		labelWidth -= 108
	}
	label = fitTextToWidth(label, r.faces.regular[size], labelWidth)
	if !spot {
		r.drawWorkspaceVideoLabelBackground(canvas, label, size, baseline)
	}
	r.centeredText(canvas, width/2, baseline, label, size, false, workspaceText)
	bounds := r.cameraVideoBounds()
	if r.cameraFrameImage == nil {
		r.centeredText(canvas, width/2, bounds.Min.Y+bounds.Dy()/2, "Connecting to camera…", func() int {
			if spot {
				return 14
			}
			return 20
		}(), false, workspaceMuted)
	}
	if cameras.SelectedTalkback {
		talk := cameraTalkBounds(width, height, spot)
		fill := workspaceControl
		if r.cameraTalkPressed {
			fill = mix(workspacePanel, r.menuPalette().primary, .35)
		}
		drawMenuRoundedRect(canvas, talk, 12, fill)
		drawMenuRoundedOutline(canvas, talk, 12, 1, workspaceBorder)
		label := "Hold to talk"
		if r.cameraTalkPressed {
			label = "Talking…"
		}
		size := 18
		if spot {
			size = 14
		}
		drawSatChatMicGlyph(canvas, talk.Min.X+30, talk.Min.Y+talk.Dy()/2, 10, workspaceMuted)
		r.centeredText(canvas, width/2+12, talk.Min.Y+talk.Dy()/2+size/3, label, size, false, workspaceText)
	}
	if spot {
		maskOutsideSpotCircle(canvas, 236)
	}
}

func workspaceClimatePanel(width, height int) image.Rectangle {
	return image.Rect(28, 116, width-28, height-20)
}

func (r *renderer) drawLegacySpotClimate(canvas *image.RGBA) {
	connectedFill(canvas)
	climate := r.state.Climate
	enabled := climate != nil && climate.Available
	room := "Thermostat unavailable"
	if enabled {
		room = firstNonEmpty(climate.RoomName, "Climate")
	}
	r.connectedHeader(canvas, "Climate", room, climateCloseBounds(480, 480, true), true)
	target, current, action := "--°", "Current --°", "Unavailable"
	if enabled {
		target = climateTemperature(climate.TargetTemperature, climate.TemperatureUnit)
		current = "Current " + climateTemperature(climate.CurrentTemperature, climate.TemperatureUnit)
		if climate.CurrentHumidity != nil {
			current += fmt.Sprintf(" · Humidity %.0f%%", *climate.CurrentHumidity)
		}
		action = firstNonEmpty(climate.HVACAction, climate.HVACMode, "Idle")
	}
	r.centeredText(canvas, 240, 236, target, 64, false, workspaceText)
	r.centeredText(canvas, 240, 262, "Target", 14, false, workspaceMuted)
	r.centeredText(canvas, 240, 287, current, 14, false, workspaceMuted)
	r.centeredText(canvas, 240, 309, action, 16, false, r.menuPalette().primary)
	for _, plus := range []bool{false, true} {
		bounds := climateSetpointBounds(480, 480, plus, true)
		ink := workspaceMuted
		if !enabled || !climate.CanSetTemperature {
			ink = mix(workspacePanel, workspaceMuted, .4)
		}
		drawMenuRoundedRect(canvas, bounds, 12, workspaceControl)
		drawMenuRoundedOutline(canvas, bounds, 12, 1, workspaceBorder)
		label := "−"
		if plus {
			label = "+"
		}
		r.centeredText(canvas, bounds.Min.X+bounds.Dx()/2, bounds.Min.Y+bounds.Dy()*2/3, label, 34, false, ink)
	}
	r.drawWorkspaceClimateModes(canvas, 480, 480, true, 12)
	rooms := []show.RoomTemperature{}
	if climate != nil {
		rooms = climate.RoomTemperatures
	}
	if len(rooms) == 0 {
		r.centeredText(canvas, 240, 392, "No room readings selected", 13, false, workspaceMuted)
	} else {
		pages := climateRoomPageCount(len(rooms))
		r.climatePage = max(0, min(r.climatePage, pages-1))
		start := r.climatePage * 3
		for index, room := range rooms[start:min(len(rooms), start+3)] {
			bounds := spotClimateRoomBounds(index)
			drawMenuRoundedRect(canvas, bounds, 8, workspacePanel)
			r.centeredText(canvas, bounds.Min.X+bounds.Dx()/2, bounds.Min.Y+17, fitTextToWidth(room.RoomName, r.faces.regular[10], bounds.Dx()-10), 10, false, workspaceMuted)
			r.centeredText(canvas, bounds.Min.X+bounds.Dx()/2, bounds.Min.Y+35, climateRoomTemperature(room.Temperature), 16, false, workspaceText)
		}
		if pages > 1 {
			r.centeredText(canvas, 240, 440, fmt.Sprintf("%d / %d", r.climatePage+1, pages), 12, false, workspaceMuted)
			next := spotClimateNextBounds()
			drawMenuChevron(canvas, next.Min.X+next.Dx()/2, next.Min.Y+next.Dy()/2, 7, r.menuPalette().primary)
		}
	}
	maskOutsideSpotCircle(canvas, 236)
}

func (r *renderer) drawWorkspaceSpotClimate(canvas *image.RGBA) { r.drawRookClimate(canvas) }

func containBounds(viewport image.Rectangle, size image.Point) image.Rectangle {
	if viewport.Empty() || size.X <= 0 || size.Y <= 0 {
		return image.Rectangle{}
	}
	width, height := viewport.Dx(), viewport.Dy()
	if size.X*height > size.Y*width {
		height = max(1, size.Y*width/size.X)
	} else {
		width = max(1, size.X*height/size.Y)
	}
	left, top := viewport.Min.X+(viewport.Dx()-width)/2, viewport.Min.Y+(viewport.Dy()-height)/2
	return image.Rect(left, top, left+width, top+height)
}

// Camera preparation marks ONLY generated letterbox pixels transparent.
// Inspect that alpha geometry, never brightness (a dark room is not a bar).
// This costs O(width+height), not a frame-sized mask/scan on every video tick.
func cameraImageContentBounds(source image.Image) image.Rectangle {
	bounds := source.Bounds()
	rgba, ok := source.(*image.RGBA)
	if !ok || bounds.Empty() {
		return bounds
	}
	cx, cy := bounds.Min.X+bounds.Dx()/2, bounds.Min.Y+bounds.Dy()/2
	left, right, top, bottom := bounds.Min.X, bounds.Max.X, bounds.Min.Y, bounds.Max.Y
	for left < right && rgba.RGBAAt(left, cy).A == 0 {
		left++
	}
	for right > left && rgba.RGBAAt(right-1, cy).A == 0 {
		right--
	}
	for top < bottom && rgba.RGBAAt(cx, top).A == 0 {
		top++
	}
	for bottom > top && rgba.RGBAAt(cx, bottom-1).A == 0 {
		bottom--
	}
	if left == right || top == bottom {
		return bounds
	}
	return image.Rect(left, top, right, bottom)
}

func (r *renderer) drawWorkspaceClimate(canvas *image.RGBA) {
	connectedFill(canvas)
	width, height := canvas.Rect.Dx(), canvas.Rect.Dy()
	climate := r.state.Climate
	enabled := climate != nil && climate.Available
	room := "Thermostat unavailable"
	if enabled {
		room = firstNonEmpty(climate.RoomName, "Climate")
	}
	r.connectedHeader(canvas, "Climate", room, climateCloseBounds(width, height, false), false)
	panel := workspaceClimatePanel(width, height)
	if height >= 600 {
		panel.Min.Y = 164
	}
	connectedPanel(canvas, panel)
	divider := width * 54 / 100
	line(canvas, divider, panel.Min.Y, divider, panel.Max.Y, 1, workspaceBorder)
	cx := (panel.Min.X + divider) / 2
	cy := panel.Min.Y + panel.Dy()*30/100
	targetSize, bodySize, roomSize := 74, 20, 24
	if height >= 600 {
		targetSize, bodySize, roomSize = 96, 28, 34
	}
	target := "--°"
	current, action := "Current --°", "Unavailable"
	if enabled {
		target = climateTemperature(climate.TargetTemperature, climate.TemperatureUnit)
		current = "Current " + climateTemperature(climate.CurrentTemperature, climate.TemperatureUnit)
		if climate.CurrentHumidity != nil {
			current += fmt.Sprintf(" · Humidity %.0f%%", *climate.CurrentHumidity)
		}
		action = firstNonEmpty(climate.HVACAction, climate.HVACMode, "Idle")
		if len(action) > 0 {
			action = strings.ToUpper(action[:1]) + action[1:]
		}
	}
	r.centeredText(canvas, cx, cy+targetSize/3, target, targetSize, false, workspaceText)
	r.centeredText(canvas, cx, cy+targetSize*2/3, "Target", bodySize, false, workspaceMuted)
	r.centeredText(canvas, cx, panel.Min.Y+panel.Dy()*62/100, fitTextToWidth(current, r.faces.regular[bodySize], divider-panel.Min.X-30), bodySize, false, workspaceMuted)
	r.centeredText(canvas, cx, panel.Min.Y+panel.Dy()*72/100, action, bodySize, false, r.menuPalette().primary)
	for _, plus := range []bool{false, true} {
		bounds := climateSetpointBounds(width, height, plus, false)
		ink := workspaceMuted
		if !enabled || !climate.CanSetTemperature {
			ink = mix(workspacePanel, workspaceMuted, .4)
		}
		drawMenuRoundedRect(canvas, bounds, 12, workspaceControl)
		drawMenuRoundedOutline(canvas, bounds, 12, 1, workspaceBorder)
		label := "−"
		if plus {
			label = "+"
		}
		r.centeredText(canvas, bounds.Min.X+bounds.Dx()/2, bounds.Min.Y+bounds.Dy()*2/3, label, 34, false, ink)
	}
	r.drawWorkspaceClimateModes(canvas, width, height, false, bodySize)
	right := image.Rect(divider+28, panel.Min.Y+36, panel.Max.X-24, panel.Max.Y-20)
	r.text(canvas, right.Min.X, right.Min.Y, "Room temperatures", roomSize, false, workspaceText)
	rooms := []show.RoomTemperature{}
	if climate != nil {
		rooms = climate.RoomTemperatures
	}
	if len(rooms) == 0 {
		r.text(canvas, right.Min.X, right.Min.Y+56, "No readings selected", bodySize, false, workspaceMuted)
		return
	}
	// All supplied readings remain visible; up to six are part of the existing
	// climate contract. The primary three-row layout matches the design board.
	rows := min(6, len(rooms))
	rowHeight := (right.Dy() - 24) / max(3, rows)
	labelSize := bodySize
	if rows > 4 {
		labelSize = min(labelSize, 18)
	}
	for index, room := range rooms[:rows] {
		y := right.Min.Y + 22 + index*rowHeight
		line(canvas, right.Min.X, y, right.Max.X, y, 1, workspaceBorder)
		value := climateRoomTemperature(room.Temperature)
		valueWidth := textWidth(r.faces.regular[labelSize], value)
		r.text(canvas, right.Min.X+4, y+rowHeight*2/3, fitTextToWidth(room.RoomName, r.faces.regular[labelSize], right.Dx()-valueWidth-28), labelSize, false, workspaceText)
		r.text(canvas, right.Max.X-valueWidth-2, y+rowHeight*2/3, value, labelSize, false, workspaceText)
	}
}

func (r *renderer) drawWorkspaceClimateModes(canvas *image.RGBA, width, height int, spot bool, fontSize int) {
	outer := climateModeBounds(width, height, 0, spot).Union(climateModeBounds(width, height, 3, spot))
	drawMenuRoundedRect(canvas, outer, 12, workspaceControl)
	for index, mode := range []string{"Heat", "Cool", "Auto", "Off"} {
		bounds := climateModeBounds(width, height, index, spot)
		if index > 0 {
			line(canvas, bounds.Min.X, bounds.Min.Y+8, bounds.Min.X, bounds.Max.Y-8, 1, workspaceBorder)
		}
		ink := workspaceText
		if !climateModeAvailable(r.state.Climate, strings.ToLower(mode)) {
			ink = mix(workspacePanel, workspaceMuted, .45)
		} else if strings.EqualFold(r.state.Climate.HVACMode, mode) {
			drawMenuRoundedRect(canvas, bounds, 10, r.menuPalette().primary)
		}
		r.centeredText(canvas, bounds.Min.X+bounds.Dx()/2, bounds.Min.Y+bounds.Dy()*2/3, mode, fontSize, false, ink)
	}
	drawMenuRoundedOutline(canvas, outer, 12, 1, workspaceBorder)
}
