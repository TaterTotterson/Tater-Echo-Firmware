//go:build linux

package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"strings"
	"sync"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/linuxinput"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
	"golang.org/x/image/vector"
)

type actionMenuItem uint8

const (
	actionMenuNone actionMenuItem = iota
	actionMenuSatChat
	actionMenuCameras
	actionMenuClimate
)

var (
	menuBackdropInk = color.RGBA{5, 17, 28, 224}
	menuCardInk     = color.RGBA{17, 35, 51, 238}
	menuControlInk  = color.RGBA{34, 43, 57, 238}
	menuMutedInk    = color.RGBA{30, 32, 38, 220}
	menuOutlineInk  = color.RGBA{87, 131, 160, 172}
	menuSubtitleInk = color.RGBA{205, 222, 239, 255}
	menuCyanInk     = color.RGBA{27, 205, 255, 255}
	menuOrangeInk   = color.RGBA{255, 132, 24, 255}
	menuMaskCache   sync.Map
)

type menuMaskKey struct {
	width, height, radius, thickness int
	shape                            uint8
}

func actionMenuPanelBounds(width, height int, spot bool) image.Rectangle {
	if spot {
		return image.Rect(52, 70, 428, 446)
	}
	panelWidth, panelHeight := min(width-48, 510), min(height-30, 352)
	if height >= 600 {
		panelWidth = min(width-96, height*11/10)
		panelHeight = min(height-64, 690)
	}
	return image.Rect((width-panelWidth)/2, (height-panelHeight)/2, (width+panelWidth)/2, (height+panelHeight)/2)
}

func actionMenuCloseBounds(width, height int, spot bool) image.Rectangle {
	if spot {
		return image.Rect(366, 76, 420, 130)
	}
	panel := actionMenuPanelBounds(width, height, false)
	size := 44
	margin := 14
	if height >= 600 {
		size, margin = 62, 24
	}
	return image.Rect(panel.Max.X-margin-size, panel.Min.Y+margin, panel.Max.X-margin, panel.Min.Y+margin+size)
}

func actionMenuBounds(width, height, index int, spot bool) image.Rectangle {
	if spot {
		switch index {
		case 0:
			return image.Rect(80, 147, 400, 210)
		case 1:
			return image.Rect(67, 220, 413, 283)
		case 2:
			return image.Rect(75, 293, 405, 356)
		default:
			return image.Rectangle{}
		}
	}
	if index < 0 || index > 2 {
		return image.Rectangle{}
	}
	panel := actionMenuPanelBounds(width, height, false)
	inset, headerHeight, footerHeight, gap := 18, 64, 45, 7
	if height >= 600 {
		inset, headerHeight, footerHeight, gap = 36, 122, 78, 14
	}
	top := panel.Min.Y + headerHeight
	bottom := panel.Max.Y - footerHeight - gap
	rowHeight := (bottom - top - 2*gap) / 3
	rowTop := top + index*(rowHeight+gap)
	return image.Rect(panel.Min.X+inset, rowTop, panel.Max.X-inset, rowTop+rowHeight)
}

func actionMenuBroadcastBounds(width, height int, spot bool) image.Rectangle {
	if spot {
		return image.Rect(104, 368, 376, 416)
	}
	panel := actionMenuPanelBounds(width, height, false)
	inset, bottom, height := 18, 11, 38
	if panel.Dy() >= 600 {
		inset, bottom, height = 36, 18, 58
	}
	return image.Rect(panel.Min.X+inset, panel.Max.Y-bottom-height, panel.Max.X-inset, panel.Max.Y-bottom)
}

func actionMenuItemAt(point image.Point, width, height int, spot bool) actionMenuItem {
	for index, item := range []actionMenuItem{actionMenuSatChat, actionMenuCameras, actionMenuClimate} {
		if point.In(actionMenuBounds(width, height, index, spot)) {
			return item
		}
	}
	return actionMenuNone
}

func (r *renderer) handleActionMenuTouch(client *socketClient, event linuxinput.TouchEvent, width, height int, spot bool) {
	point := image.Pt(event.X, event.Y)
	if point.In(actionMenuCloseBounds(width, height, spot)) {
		r.menuOpen = false
		return
	}
	switch actionMenuItemAt(point, width, height, spot) {
	case actionMenuSatChat:
		r.menuOpen = false
		r.satChatPage = 0
		r.satChatOpen = true
	case actionMenuCameras:
		r.menuOpen = false
		r.cameraPage = 0
		r.cameraOpen = true
		client.sendCamera("camera.open", "")
	case actionMenuClimate:
		r.menuOpen = false
		r.climatePage = 0
		r.climateOpen = true
	}
}

func (r *renderer) handleCameraTouch(client *socketClient, event linuxinput.TouchEvent, width, height int, spot bool) {
	point := image.Pt(event.X, event.Y)
	selected := r.state.Cameras != nil && r.state.Cameras.SelectedID != ""
	if event.Kind == linuxinput.Down && selected && r.state.Cameras.SelectedTalkback && point.In(r.cameraTalkBounds(width, height, spot)) {
		r.cameraTalkPressed = true
		client.sendCamera("camera.talk.start", r.state.Cameras.SelectedID)
		return
	}
	if event.Kind != linuxinput.Up {
		return
	}
	if r.cameraTalkPressed {
		r.cameraTalkPressed = false
		client.sendCamera("camera.talk.stop", "")
		return
	}
	if point.In(r.cameraCloseBounds(width, height, spot, selected)) {
		r.cameraOpen = false
		client.sendCamera("camera.close", "")
		return
	}
	if selected {
		if point.In(cameraBackBounds(width, height, spot)) {
			client.sendCamera("camera.close", "")
		}
		return
	}
	cameras := []show.Camera{}
	if r.state.Cameras != nil {
		cameras = r.state.Cameras.Items
	}
	perPage := cameraPageSize(width, height, spot)
	pages := max(1, (len(cameras)+perPage-1)/perPage)
	if point.In(cameraPreviousBounds(width, height, spot)) && pages > 1 {
		if r.cameraPage > 0 {
			r.cameraPage--
		}
		return
	}
	if point.In(cameraNextBounds(width, height, spot)) && pages > 1 {
		if r.cameraPage+1 < pages {
			r.cameraPage++
		}
		return
	}
	if r.cameraPage < 0 {
		r.cameraPage = 0
	} else if r.cameraPage >= pages {
		r.cameraPage = pages - 1
	}
	start := r.cameraPage * perPage
	for index := 0; index < perPage && start+index < len(cameras); index++ {
		if point.In(cameraCardBounds(width, height, index, spot)) && cameras[start+index].Available {
			client.sendCamera("camera.select", cameras[start+index].ID)
			return
		}
	}
}

func (r *renderer) cameraCloseBounds(width, height int, spot, selected bool) image.Rectangle {
	if spot {
		return image.Rect(370, 66, 420, 116)
	}
	panel := cameraDirectoryLayoutFor(width, height, false).panel
	size, inset := 46, 14
	if height >= 700 {
		size, inset = 62, 24
	}
	return image.Rect(panel.Max.X-inset-size, panel.Min.Y+inset, panel.Max.X-inset, panel.Min.Y+inset+size)
}

func (r *renderer) cameraTalkBounds(width, height int, spot bool) image.Rectangle {
	return cameraTalkBounds(width, height, spot)
}

func (r *renderer) actionMenuTemperature() string {
	if climate := r.state.Climate; climate != nil && climate.Available {
		if climate.TargetTemperature != nil {
			return climateTemperature(climate.TargetTemperature, climate.TemperatureUnit)
		}
		if climate.CurrentTemperature != nil {
			return climateTemperature(climate.CurrentTemperature, climate.TemperatureUnit)
		}
	}
	if weather := r.state.Weather; weather != nil {
		return strings.TrimSpace(weather.IndoorTemperatureText)
	}
	return "--°"
}

func (r *renderer) drawActionMenuBackground(canvas *image.RGBA, now time.Time, accent color.RGBA, alpha uint8) {
	if wallpaper := r.activeWallpaper(now); wallpaper != nil {
		drawPreparedCover(canvas, canvas.Bounds(), wallpaper)
	} else {
		fillGradient(canvas, color.RGBA{12, 14, 19, 255}, mix(color.RGBA{12, 14, 19, 255}, accent, .06))
	}
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.NRGBA{R: 5, G: 6, B: 8, A: alpha}), image.Point{}, draw.Over)
}

func (r *renderer) drawActionMenu(canvas *image.RGBA, now time.Time, accent color.RGBA) {
	width, height := canvas.Rect.Dx(), canvas.Rect.Dy()
	r.drawActionMenuBackground(canvas, now, accent, 86)
	panel := actionMenuPanelBounds(width, height, false)
	large := height >= 600
	radius := 24
	if large {
		radius = 38
	}
	drawMenuShadow(canvas, panel, radius, 12)
	drawMenuRoundedRect(canvas, panel, radius, menuBackdropInk)
	drawMenuRoundedOutline(canvas, panel, radius, 2, color.RGBA{menuCyanInk.R, menuCyanInk.G, menuCyanInk.B, 232})
	drawMenuRoundedOutline(canvas, panel.Inset(3), max(1, radius-3), 1, color.RGBA{255, 255, 255, 30})

	titleSize, onlineSize := 28, 13
	if large {
		titleSize, onlineSize = 42, 19
	}
	name := assistantFirstName(r.state.AssistantName)
	textX := panel.Min.X + 28
	titleY := panel.Min.Y + 31
	if large {
		textX, titleY = panel.Min.X+52, panel.Min.Y+62
	}
	if large {
		r.text(canvas, textX, titleY, strings.ToUpper(name)+" MENU", titleSize, true, color.White)
	} else {
		r.centeredText(canvas, panel.Min.X+panel.Dx()/2, titleY, strings.ToUpper(name)+" MENU", titleSize, true, color.White)
	}

	close := actionMenuCloseBounds(width, height, false)
	drawMenuRoundedRect(canvas, close, close.Dx()/2, menuControlInk)
	drawMenuRoundedOutline(canvas, close, close.Dx()/2, 2, color.RGBA{172, 205, 230, 150})
	drawClimateCloseGlyph(canvas, close, color.RGBA{255, 255, 255, 255})

	online := name + " Online"
	dotX, onlineY := textX+5, titleY+22
	if large {
		dotX, onlineY = textX+8, titleY+39
	} else {
		onlineWidth := textWidth(r.faces.regular[onlineSize], online)
		dotX = panel.Min.X + (panel.Dx()-onlineWidth)/2 - 10
	}
	drawMenuRoundedRect(canvas, image.Rect(dotX-5, onlineY-onlineSize+3, dotX+5, onlineY-onlineSize+13), 5, color.RGBA{52, 240, 105, 255})
	r.text(canvas, dotX+14, onlineY, online, onlineSize, false, color.RGBA{222, 235, 247, 255})

	labels := []struct {
		title, subtitle string
		kind            actionMenuItem
	}{
		{"SAT CHAT", "Satellite calls", actionMenuSatChat},
		{"CAMERAS", "Live view & talk", actionMenuCameras},
		{"CLIMATE", "Thermostat & room temps", actionMenuClimate},
	}
	for index, label := range labels {
		bounds := actionMenuBounds(width, height, index, false)
		r.drawWideActionCard(canvas, bounds, label.kind, label.title, label.subtitle, large, false)
	}
	r.drawActionMenuBroadcast(canvas, actionMenuBroadcastBounds(width, height, false), large, false)
}

func (r *renderer) drawWideActionCard(canvas *image.RGBA, bounds image.Rectangle, kind actionMenuItem, title, subtitle string, large, spot bool) {
	radius := max(17, bounds.Dy()/3)
	if spot {
		radius = bounds.Dy() / 2
	}
	drawMenuShadow(canvas, bounds, radius, 5)
	drawMenuRoundedRect(canvas, bounds, radius, menuCardInk)
	drawMenuRoundedOutline(canvas, bounds, radius, 1, menuOutlineInk)
	drawMenuRoundedOutline(canvas, bounds.Inset(2), max(1, radius-2), 1, color.RGBA{255, 255, 255, 18})

	iconDiameter := min(bounds.Dy()-14, 58)
	titleSize, subtitleSize, tempSize := 19, 13, 28
	iconInset := 10
	if large {
		iconDiameter, titleSize, subtitleSize, tempSize, iconInset = min(bounds.Dy()-22, 94), 30, 20, 42, 18
	}
	if spot {
		iconDiameter, titleSize, subtitleSize, tempSize, iconInset = 48, 17, 11, 26, 8
	}
	iconX, iconY := bounds.Min.X+iconInset+iconDiameter/2, bounds.Min.Y+bounds.Dy()/2
	iconBounds := image.Rect(iconX-iconDiameter/2, iconY-iconDiameter/2, iconX+iconDiameter/2, iconY+iconDiameter/2)
	drawMenuRoundedRect(canvas, iconBounds, iconDiameter/2, color.RGBA{4, 25, 39, 224})
	drawMenuRoundedOutline(canvas, iconBounds, iconDiameter/2, max(2, iconDiameter/28), color.RGBA{menuCyanInk.R, menuCyanInk.G, menuCyanInk.B, 245})
	r.drawActionMenuGlyph(canvas, kind, iconX, iconY, max(14, iconDiameter*34/100), menuCyanInk)

	textX := iconBounds.Max.X + max(13, iconDiameter/3)
	titleY := iconY - 3
	subtitleY := iconY + subtitleSize + 5
	if large {
		titleY, subtitleY = iconY-8, iconY+subtitleSize+10
	}
	copyRight := bounds.Max.X - max(40, bounds.Dy()/3)
	if kind == actionMenuClimate {
		copyRight -= max(74, bounds.Dx()/5)
	}
	r.text(canvas, textX, titleY, fitTextToWidth(title, r.faces.bold[titleSize], max(20, copyRight-textX)), titleSize, true, color.White)
	r.text(canvas, textX, subtitleY, fitTextToWidth(subtitle, r.faces.regular[subtitleSize], max(20, copyRight-textX)), subtitleSize, false, menuSubtitleInk)

	chevronX := bounds.Max.X - max(22, bounds.Dy()/5)
	if kind == actionMenuClimate {
		dividerX := chevronX - max(66, bounds.Dx()/9)
		line(canvas, dividerX, bounds.Min.Y+bounds.Dy()/5, dividerX, bounds.Max.Y-bounds.Dy()/5, max(1, bounds.Dy()/60), color.RGBA{148, 181, 207, 155})
		temp := r.actionMenuTemperature()
		tempWidth := textWidth(r.faces.bold[tempSize], temp)
		tempX := dividerX + max(11, bounds.Dy()/9)
		r.text(canvas, min(tempX, chevronX-tempWidth-12), iconY+tempSize/3, temp, tempSize, true, color.White)
	}
	drawMenuChevron(canvas, chevronX, iconY, max(7, bounds.Dy()/12), color.RGBA{244, 249, 255, 245})
}

func (r *renderer) drawSpotActionMenu(canvas *image.RGBA, now time.Time, accent color.RGBA) {
	r.drawActionMenuBackground(canvas, now, accent, 82)
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.NRGBA{R: 2, G: 9, B: 17, A: 34}), image.Point{}, draw.Over)
	panel := actionMenuPanelBounds(480, 480, true)
	drawMenuSuperellipseShadow(canvas, panel, 9)
	drawMenuSuperellipse(canvas, panel, menuBackdropInk)
	drawMenuSuperellipseOutline(canvas, panel, 2, color.RGBA{menuCyanInk.R, menuCyanInk.G, menuCyanInk.B, 226})
	name := assistantFirstName(r.state.AssistantName)
	r.centeredText(canvas, 240, 113, strings.ToUpper(name)+" MENU", 28, true, color.White)
	online := fitTextToWidth(name+" Online", r.faces.regular[13], 150)
	onlineWidth := textWidth(r.faces.regular[13], online)
	dotX := 240 - onlineWidth/2 - 11
	drawMenuRoundedRect(canvas, image.Rect(dotX-5, 127, dotX+5, 137), 5, color.RGBA{52, 240, 105, 255})
	r.text(canvas, dotX+11, 138, online, 13, false, color.RGBA{222, 235, 247, 255})

	close := actionMenuCloseBounds(480, 480, true)
	drawMenuRoundedRect(canvas, close, close.Dx()/2, menuControlInk)
	drawMenuRoundedOutline(canvas, close, close.Dx()/2, 2, color.RGBA{172, 205, 230, 165})
	drawClimateCloseGlyph(canvas, close, color.RGBA{255, 255, 255, 255})

	for index, item := range []struct {
		kind            actionMenuItem
		title, subtitle string
	}{
		{actionMenuSatChat, "SAT CHAT", "Satellite calls"},
		{actionMenuCameras, "CAMERAS", "Live view & talk"},
		{actionMenuClimate, "CLIMATE", "Thermostat & room temps"},
	} {
		bounds := actionMenuBounds(480, 480, index, true)
		r.drawWideActionCard(canvas, bounds, item.kind, item.title, item.subtitle, false, true)
	}
	r.drawActionMenuBroadcast(canvas, actionMenuBroadcastBounds(480, 480, true), false, true)
	maskOutsideSpotCircle(canvas, 236)
}

func (r *renderer) drawActionMenuGlyph(canvas *image.RGBA, kind actionMenuItem, cx, cy, size int, ink color.RGBA) {
	switch kind {
	case actionMenuSatChat:
		drawMenuSatelliteGlyph(canvas, cx, cy, size)
	case actionMenuCameras:
		drawMenuCameraGlyph(canvas, cx, cy, size, ink)
	case actionMenuClimate:
		drawMenuThermometerGlyph(canvas, cx, cy, size)
	}
}

func (r *renderer) drawActionMenuBroadcast(canvas *image.RGBA, bounds image.Rectangle, large, spot bool) {
	radius := bounds.Dy() / 2
	for spread := 7; spread >= 2; spread -= 2 {
		glow := bounds.Inset(-spread)
		drawMenuRoundedOutline(canvas, glow, radius+spread, 1, color.RGBA{menuOrangeInk.R, menuOrangeInk.G, menuOrangeInk.B, uint8(8 + spread*2)})
	}
	drawMenuRoundedRect(canvas, bounds, radius, color.RGBA{20, 28, 36, 225})
	drawMenuRoundedOutline(canvas, bounds, radius, 2, menuOrangeInk)
	fontSize := 14
	if large {
		fontSize = 20
	} else if spot {
		fontSize = 13
	}
	label := "Hold anywhere to broadcast"
	labelWidth := textWidth(r.faces.regular[fontSize], label)
	iconSize := max(12, bounds.Dy()/4)
	totalWidth := iconSize*2 + 13 + labelWidth
	iconX := bounds.Min.X + (bounds.Dx()-totalWidth)/2 + iconSize
	centerY := bounds.Min.Y + bounds.Dy()/2
	drawMenuBroadcastGlyph(canvas, iconX, centerY, iconSize, menuOrangeInk)
	r.text(canvas, iconX+iconSize+13, centerY+fontSize/3, label, fontSize, false, color.RGBA{244, 248, 252, 255})
}

func drawMenuCameraGlyph(canvas *image.RGBA, cx, cy, size int, ink color.RGBA) {
	body := image.Rect(cx-size, cy-size*2/3, cx+size/3, cy+size*2/3)
	drawMenuRoundedOutline(canvas, body, max(3, size/4), max(2, size/7), ink)
	wing := image.NewRGBA(image.Rect(0, 0, size, size*2))
	path := vector.NewRasterizer(wing.Rect.Dx(), wing.Rect.Dy())
	w, h := float32(wing.Rect.Dx()), float32(wing.Rect.Dy())
	path.MoveTo(0, h*.35)
	path.LineTo(w, h*.12)
	path.LineTo(w, h*.88)
	path.LineTo(0, h*.65)
	path.ClosePath()
	path.Draw(wing, wing.Bounds(), image.White, image.Point{})
	draw.DrawMask(canvas, image.Rect(body.Max.X, cy-size, body.Max.X+size, cy+size), image.NewUniform(ink), image.Point{}, wing, image.Point{}, draw.Over)
}

func drawMenuSatelliteGlyph(canvas *image.RGBA, cx, cy, size int) {
	side := max(18, size*2+4)
	icon := image.NewRGBA(image.Rect(0, 0, side, side))
	path := vector.NewRasterizer(side, side)
	s := float32(side)
	path.MoveTo(s*.25, s*.20)
	path.LineTo(s*.74, s*.69)
	path.CubeTo(s*.57, s*.87, s*.34, s*.83, s*.20, s*.66)
	path.CubeTo(s*.08, s*.51, s*.12, s*.32, s*.25, s*.20)
	path.ClosePath()
	path.Draw(icon, icon.Bounds(), image.White, image.Point{})
	destination := image.Rect(cx-side/2, cy-side/2, cx-side/2+side, cy-side/2+side)
	draw.DrawMask(canvas, destination, image.NewUniform(color.RGBA{244, 250, 255, 255}), image.Point{}, icon, image.Point{}, draw.Over)
	line(canvas, cx, cy+size/4, cx-size/3, cy+size*3/4, max(2, size/7), menuCyanInk)
	line(canvas, cx-size*2/3, cy+size*3/4, cx+size/3, cy+size*3/4, max(2, size/7), menuCyanInk)
	circle(canvas, cx+size/3, cy-size/3, max(2, size/8), menuOrangeInk)
	arc(canvas, cx+size/3, cy-size/3, max(5, size/2), 270, 85, max(2, size/8), menuOrangeInk)
	arc(canvas, cx+size/3, cy-size/3, max(8, size*4/5), 270, 85, max(2, size/9), menuOrangeInk)
}

func drawMenuThermometerGlyph(canvas *image.RGBA, cx, cy, size int) {
	stemWidth := max(7, size/3)
	stem := image.Rect(cx-stemWidth/2, cy-size, cx+(stemWidth+1)/2, cy+size/3)
	drawMenuRoundedOutline(canvas, stem, stemWidth/2, max(2, size/8), menuCyanInk)
	bulbRadius := max(6, size*2/5)
	bulb := image.Rect(cx-bulbRadius, cy+size/4-bulbRadius, cx+bulbRadius, cy+size/4+bulbRadius)
	drawMenuRoundedRect(canvas, bulb, bulbRadius, color.RGBA{7, 27, 40, 255})
	drawMenuRoundedOutline(canvas, bulb, bulbRadius, max(2, size/8), menuCyanInk)
	fillWidth := max(3, stemWidth/3)
	drawMenuRoundedRect(canvas, image.Rect(cx-fillWidth/2, cy-size/2, cx+(fillWidth+1)/2, cy+size/3), fillWidth/2, menuOrangeInk)
	innerBulb := bulb.Inset(max(3, size/6))
	drawMenuRoundedRect(canvas, innerBulb, innerBulb.Dx()/2, menuOrangeInk)
}

func drawMenuBroadcastGlyph(canvas *image.RGBA, cx, cy, size int, ink color.RGBA) {
	dot := image.Rect(cx-3, cy-3, cx+4, cy+4)
	drawMenuRoundedRect(canvas, dot, 4, ink)
	arc(canvas, cx, cy, max(7, size/2), 125, 110, max(2, size/7), ink)
	arc(canvas, cx, cy, max(7, size/2), 305, 110, max(2, size/7), ink)
	arc(canvas, cx, cy, max(12, size), 125, 110, max(2, size/7), ink)
	arc(canvas, cx, cy, max(12, size), 305, 110, max(2, size/7), ink)
}

func drawMenuShadow(canvas *image.RGBA, bounds image.Rectangle, radius, spread int) {
	for _, layer := range []int{spread, max(2, spread/2)} {
		shadow := image.Rect(bounds.Min.X-layer, bounds.Min.Y-layer/2, bounds.Max.X+layer, bounds.Max.Y+layer+2)
		alpha := uint8(24)
		if layer == spread {
			alpha = 18
		}
		drawMenuRoundedRect(canvas, shadow, radius+layer, color.RGBA{0, 3, 8, alpha})
	}
}

func drawMenuSuperellipseShadow(canvas *image.RGBA, bounds image.Rectangle, spread int) {
	for _, layer := range []int{spread, max(2, spread/2)} {
		shadow := image.Rect(bounds.Min.X-layer, bounds.Min.Y-layer/2, bounds.Max.X+layer, bounds.Max.Y+layer+2)
		alpha := uint8(24)
		if layer == spread {
			alpha = 18
		}
		drawMenuSuperellipse(canvas, shadow, color.RGBA{0, 3, 8, alpha})
	}
}

func drawMenuSuperellipse(canvas *image.RGBA, bounds image.Rectangle, ink color.RGBA) {
	if bounds.Empty() {
		return
	}
	mask := menuSuperellipseMask(bounds.Size())
	destination := bounds.Intersect(canvas.Bounds())
	if destination.Empty() {
		return
	}
	maskPoint := image.Pt(destination.Min.X-bounds.Min.X, destination.Min.Y-bounds.Min.Y)
	draw.DrawMask(canvas, destination, image.NewUniform(color.NRGBA{R: ink.R, G: ink.G, B: ink.B, A: ink.A}), image.Point{}, mask, maskPoint, draw.Over)
}

func drawMenuSuperellipseOutline(canvas *image.RGBA, bounds image.Rectangle, thickness int, ink color.RGBA) {
	if bounds.Empty() || thickness <= 0 {
		return
	}
	mask := menuSuperellipseOutlineMask(bounds.Size(), thickness)
	destination := bounds.Intersect(canvas.Bounds())
	if destination.Empty() {
		return
	}
	maskPoint := image.Pt(destination.Min.X-bounds.Min.X, destination.Min.Y-bounds.Min.Y)
	draw.DrawMask(canvas, destination, image.NewUniform(color.NRGBA{R: ink.R, G: ink.G, B: ink.B, A: ink.A}), image.Point{}, mask, maskPoint, draw.Over)
}

func drawMenuRoundedRect(canvas *image.RGBA, bounds image.Rectangle, radius int, ink color.RGBA) {
	if bounds.Empty() {
		return
	}
	mask := menuRoundedMask(bounds.Size(), radius)
	destination := bounds.Intersect(canvas.Bounds())
	if destination.Empty() {
		return
	}
	maskPoint := image.Pt(destination.Min.X-bounds.Min.X, destination.Min.Y-bounds.Min.Y)
	draw.DrawMask(canvas, destination, image.NewUniform(color.NRGBA{R: ink.R, G: ink.G, B: ink.B, A: ink.A}), image.Point{}, mask, maskPoint, draw.Over)
}

func drawMenuRoundedOutline(canvas *image.RGBA, bounds image.Rectangle, radius, thickness int, ink color.RGBA) {
	if bounds.Empty() || thickness <= 0 {
		return
	}
	outline := menuRoundedOutlineMask(bounds.Size(), radius, thickness)
	destination := bounds.Intersect(canvas.Bounds())
	if destination.Empty() {
		return
	}
	maskPoint := image.Pt(destination.Min.X-bounds.Min.X, destination.Min.Y-bounds.Min.Y)
	draw.DrawMask(canvas, destination, image.NewUniform(color.NRGBA{R: ink.R, G: ink.G, B: ink.B, A: ink.A}), image.Point{}, outline, maskPoint, draw.Over)
}

func menuRoundedMask(size image.Point, radius int) *image.Alpha {
	if size.X <= 0 || size.Y <= 0 {
		return image.NewAlpha(image.Rectangle{})
	}
	radius = min(max(0, radius), min(size.X, size.Y)/2)
	key := menuMaskKey{width: size.X, height: size.Y, radius: radius}
	if cached, ok := menuMaskCache.Load(key); ok {
		return cached.(*image.Alpha)
	}
	mask := image.NewAlpha(image.Rect(0, 0, size.X, size.Y))
	if radius == 0 {
		draw.Draw(mask, mask.Bounds(), image.White, image.Point{}, draw.Src)
		actual, _ := menuMaskCache.LoadOrStore(key, mask)
		return actual.(*image.Alpha)
	}
	w, h, r := float32(size.X), float32(size.Y), float32(radius)
	k := r * .55228475
	path := vector.NewRasterizer(size.X, size.Y)
	path.MoveTo(r, 0)
	path.LineTo(w-r, 0)
	path.CubeTo(w-r+k, 0, w, r-k, w, r)
	path.LineTo(w, h-r)
	path.CubeTo(w, h-r+k, w-r+k, h, w-r, h)
	path.LineTo(r, h)
	path.CubeTo(r-k, h, 0, h-r+k, 0, h-r)
	path.LineTo(0, r)
	path.CubeTo(0, r-k, r-k, 0, r, 0)
	path.ClosePath()
	path.Draw(mask, mask.Bounds(), image.White, image.Point{})
	actual, _ := menuMaskCache.LoadOrStore(key, mask)
	return actual.(*image.Alpha)
}

func menuRoundedOutlineMask(size image.Point, radius, thickness int) *image.Alpha {
	radius = min(max(0, radius), min(size.X, size.Y)/2)
	key := menuMaskKey{width: size.X, height: size.Y, radius: radius, thickness: thickness}
	if cached, ok := menuMaskCache.Load(key); ok {
		return cached.(*image.Alpha)
	}
	outerSource := menuRoundedMask(size, radius)
	outline := image.NewAlpha(outerSource.Bounds())
	copy(outline.Pix, outerSource.Pix)
	innerSize := image.Pt(size.X-2*thickness, size.Y-2*thickness)
	if innerSize.X > 0 && innerSize.Y > 0 {
		inner := menuRoundedMask(innerSize, max(0, radius-thickness))
		for y := 0; y < innerSize.Y; y++ {
			for x := 0; x < innerSize.X; x++ {
				outerOffset := outline.PixOffset(x+thickness, y+thickness)
				innerAlpha := inner.AlphaAt(x, y).A
				outline.Pix[outerOffset] = uint8(uint16(outline.Pix[outerOffset]) * uint16(255-innerAlpha) / 255)
			}
		}
	}
	actual, _ := menuMaskCache.LoadOrStore(key, outline)
	return actual.(*image.Alpha)
}

// menuSuperellipseMask produces the Rook menu's oval surface. A cubic
// superellipse follows the round glass more closely than a rounded rectangle,
// while retaining enough width near the bottom for the broadcast control.
func menuSuperellipseMask(size image.Point) *image.Alpha {
	if size.X <= 0 || size.Y <= 0 {
		return image.NewAlpha(image.Rectangle{})
	}
	key := menuMaskKey{width: size.X, height: size.Y, shape: 1}
	if cached, ok := menuMaskCache.Load(key); ok {
		return cached.(*image.Alpha)
	}
	mask := image.NewAlpha(image.Rect(0, 0, size.X, size.Y))
	rx, ry := float64(size.X)/2, float64(size.Y)/2
	const samples = 4
	for y := 0; y < size.Y; y++ {
		for x := 0; x < size.X; x++ {
			inside := 0
			for sampleY := 0; sampleY < samples; sampleY++ {
				ny := (float64(y) + (float64(sampleY)+.5)/samples - ry) / ry
				if ny < 0 {
					ny = -ny
				}
				ny3 := ny * ny * ny
				for sampleX := 0; sampleX < samples; sampleX++ {
					nx := (float64(x) + (float64(sampleX)+.5)/samples - rx) / rx
					if nx < 0 {
						nx = -nx
					}
					if nx*nx*nx+ny3 <= 1 {
						inside++
					}
				}
			}
			mask.SetAlpha(x, y, color.Alpha{A: uint8(inside * 255 / (samples * samples))})
		}
	}
	actual, _ := menuMaskCache.LoadOrStore(key, mask)
	return actual.(*image.Alpha)
}

func menuSuperellipseOutlineMask(size image.Point, thickness int) *image.Alpha {
	key := menuMaskKey{width: size.X, height: size.Y, thickness: thickness, shape: 1}
	if cached, ok := menuMaskCache.Load(key); ok {
		return cached.(*image.Alpha)
	}
	outerSource := menuSuperellipseMask(size)
	outline := image.NewAlpha(outerSource.Bounds())
	copy(outline.Pix, outerSource.Pix)
	innerSize := image.Pt(size.X-2*thickness, size.Y-2*thickness)
	if innerSize.X > 0 && innerSize.Y > 0 {
		inner := menuSuperellipseMask(innerSize)
		for y := 0; y < innerSize.Y; y++ {
			for x := 0; x < innerSize.X; x++ {
				outerOffset := outline.PixOffset(x+thickness, y+thickness)
				innerAlpha := inner.AlphaAt(x, y).A
				outline.Pix[outerOffset] = uint8(uint16(outline.Pix[outerOffset]) * uint16(255-innerAlpha) / 255)
			}
		}
	}
	actual, _ := menuMaskCache.LoadOrStore(key, outline)
	return actual.(*image.Alpha)
}

func drawMenuSparkle(canvas *image.RGBA, cx, cy, size int, ink color.RGBA) {
	line(canvas, cx, cy-size, cx, cy+size, max(2, size/7), ink)
	line(canvas, cx-size, cy, cx+size, cy, max(2, size/7), ink)
	line(canvas, cx-size*2/3, cy-size*2/3, cx+size*2/3, cy+size*2/3, max(1, size/10), ink)
	line(canvas, cx+size*2/3, cy-size*2/3, cx-size*2/3, cy+size*2/3, max(1, size/10), ink)
	circle(canvas, cx, cy, max(2, size/5), ink)
}

func drawMenuChevron(canvas *image.RGBA, cx, cy, size int, ink color.RGBA) {
	line(canvas, cx-size/2, cy-size, cx+size/2, cy, 2, ink)
	line(canvas, cx+size/2, cy, cx-size/2, cy+size, 2, ink)
}

func drawMenuGlassPanel(canvas *image.RGBA, bounds image.Rectangle, radius int) {
	drawMenuShadow(canvas, bounds, radius, max(5, radius/3))
	drawMenuRoundedRect(canvas, bounds, radius, menuBackdropInk)
	drawMenuRoundedOutline(canvas, bounds, radius, 2, color.RGBA{menuCyanInk.R, menuCyanInk.G, menuCyanInk.B, 230})
	if bounds.Dx() > 8 && bounds.Dy() > 8 {
		drawMenuRoundedOutline(canvas, bounds.Inset(3), max(1, radius-3), 1, color.RGBA{255, 255, 255, 28})
	}
}

func drawMenuCloseButton(canvas *image.RGBA, bounds image.Rectangle) {
	drawMenuRoundedRect(canvas, bounds, bounds.Dx()/2, menuControlInk)
	drawMenuRoundedOutline(canvas, bounds, bounds.Dx()/2, 2, color.RGBA{172, 205, 230, 165})
	drawClimateCloseGlyph(canvas, bounds, color.RGBA{255, 255, 255, 255})
}

func drawMenuCard(canvas *image.RGBA, bounds image.Rectangle, radius int, muted bool) {
	fill, outline := menuCardInk, menuOutlineInk
	if muted {
		fill, outline = menuMutedInk, color.RGBA{100, 111, 127, 95}
	}
	drawMenuShadow(canvas, bounds, radius, 3)
	drawMenuRoundedRect(canvas, bounds, radius, fill)
	drawMenuRoundedOutline(canvas, bounds, radius, 1, outline)
	if bounds.Dx() > 5 && bounds.Dy() > 5 {
		drawMenuRoundedOutline(canvas, bounds.Inset(2), max(1, radius-2), 1, color.RGBA{255, 255, 255, 14})
	}
}

func drawMenuIconMedallion(canvas *image.RGBA, bounds image.Rectangle) {
	drawMenuRoundedRect(canvas, bounds, bounds.Dx()/2, color.RGBA{4, 25, 39, 224})
	drawMenuRoundedOutline(canvas, bounds, bounds.Dx()/2, 2, color.RGBA{menuCyanInk.R, menuCyanInk.G, menuCyanInk.B, 245})
}

// drawMenuRoundedImage keeps live camera repainting on the fast Src path and
// only blends the few pixels in the four rounded corners. Masking the complete
// Crown camera frame for every JPEG costs hundreds of thousands of extra
// alpha operations on ARMv7 for no visible benefit in the rectangular center.
func drawMenuRoundedImage(canvas *image.RGBA, bounds image.Rectangle, source image.Image, radius int, cornerInk color.RGBA) {
	bounds = bounds.Intersect(canvas.Rect)
	if bounds.Empty() || source == nil {
		return
	}
	draw.Draw(canvas, bounds, source, source.Bounds().Min, draw.Src)
	mask := menuRoundedMask(bounds.Size(), radius)
	cornerSize := min(radius+1, min(bounds.Dx(), bounds.Dy())/2)
	for y := 0; y < bounds.Dy(); y++ {
		if y >= cornerSize && y < bounds.Dy()-cornerSize {
			continue
		}
		for x := 0; x < bounds.Dx(); x++ {
			if x >= cornerSize && x < bounds.Dx()-cornerSize {
				continue
			}
			coverage := mask.AlphaAt(x, y).A
			if coverage == 255 {
				continue
			}
			destination := canvas.RGBAAt(bounds.Min.X+x, bounds.Min.Y+y)
			outside := uint16(255 - coverage)
			inside := uint16(coverage)
			canvas.SetRGBA(bounds.Min.X+x, bounds.Min.Y+y, color.RGBA{
				R: uint8((uint16(destination.R)*inside + uint16(cornerInk.R)*outside) / 255),
				G: uint8((uint16(destination.G)*inside + uint16(cornerInk.G)*outside) / 255),
				B: uint8((uint16(destination.B)*inside + uint16(cornerInk.B)*outside) / 255),
				A: 255,
			})
		}
	}
}

type cameraDirectoryLayout struct {
	panel, content image.Rectangle
	rows, columns  int
	gap            int
}

func cameraDirectoryLayoutFor(width, height int, spot bool) cameraDirectoryLayout {
	if spot {
		return cameraDirectoryLayout{
			panel: image.Rect(46, 56, 434, 444), content: image.Rect(90, 132, 390, 366),
			rows: 4, columns: 1, gap: 7,
		}
	}
	panel := satChatDirectoryLayoutFor(width, height).panel
	top, bottom, margin, gap := 120, 78, 48, 12
	if height >= 700 {
		top, bottom, margin, gap = 160, 100, 56, 18
	}
	return cameraDirectoryLayout{
		panel: panel, content: image.Rect(margin, top, width-margin, height-bottom),
		rows: 3, columns: 2, gap: gap,
	}
}

func (layout cameraDirectoryLayout) cardBounds(index int) image.Rectangle {
	if index < 0 || index >= layout.rows*layout.columns {
		return image.Rectangle{}
	}
	column, row := index%layout.columns, index/layout.columns
	cardWidth := (layout.content.Dx() - (layout.columns-1)*layout.gap) / layout.columns
	cardHeight := (layout.content.Dy() - (layout.rows-1)*layout.gap) / layout.rows
	left := layout.content.Min.X + column*(cardWidth+layout.gap)
	top := layout.content.Min.Y + row*(cardHeight+layout.gap)
	return image.Rect(left, top, left+cardWidth, top+cardHeight)
}

func cameraCardBounds(width, height, index int, spot bool) image.Rectangle {
	return cameraDirectoryLayoutFor(width, height, spot).cardBounds(index)
}

func cameraPageSize(width, height int, spot bool) int {
	if spot {
		return 4
	}
	return 5
}

func cameraPreviousBounds(width, height int, spot bool) image.Rectangle {
	if spot {
		return image.Rect(126, 392, 230, 434)
	}
	panel := cameraDirectoryLayoutFor(width, height, false).panel
	buttonWidth, buttonHeight, inset := 108, 40, 72
	if height >= 700 {
		buttonWidth, buttonHeight, inset = 148, 54, 94
	}
	bottom := panel.Max.Y - max(14, height/32)
	return image.Rect(panel.Min.X+inset, bottom-buttonHeight, panel.Min.X+inset+buttonWidth, bottom)
}
func cameraNextBounds(width, height int, spot bool) image.Rectangle {
	if spot {
		return image.Rect(250, 392, 354, 434)
	}
	panel := cameraDirectoryLayoutFor(width, height, false).panel
	buttonWidth, buttonHeight, inset := 108, 40, 72
	if height >= 700 {
		buttonWidth, buttonHeight, inset = 148, 54, 94
	}
	bottom := panel.Max.Y - max(14, height/32)
	return image.Rect(panel.Max.X-inset-buttonWidth, bottom-buttonHeight, panel.Max.X-inset, bottom)
}
func cameraBackBounds(width, height int, spot bool) image.Rectangle {
	if spot {
		return image.Rect(58, 72, 132, 116)
	}
	panel := cameraDirectoryLayoutFor(width, height, false).panel
	buttonWidth, buttonHeight, inset := 120, 44, 14
	if height >= 700 {
		buttonWidth, buttonHeight, inset = 154, 56, 24
	}
	return image.Rect(panel.Min.X+inset, panel.Min.Y+inset, panel.Min.X+inset+buttonWidth, panel.Min.Y+inset+buttonHeight)
}
func cameraTalkBounds(width, height int, spot bool) image.Rectangle {
	if spot {
		return image.Rect(140, 380, 340, 430)
	}
	panel := cameraDirectoryLayoutFor(width, height, false).panel
	buttonWidth, buttonHeight, bottom := 300, 48, 14
	if height >= 700 {
		buttonWidth, buttonHeight, bottom = 360, 58, 24
	}
	return image.Rect(width/2-buttonWidth/2, panel.Max.Y-bottom-buttonHeight, width/2+buttonWidth/2, panel.Max.Y-bottom)
}
func (r *renderer) cameraVideoBounds() image.Rectangle {
	width, height := r.showSize()
	if r.spotScreen {
		return image.Rect(54, 126, 426, 366)
	}
	panel := cameraDirectoryLayoutFor(width, height, false).panel
	top, bottom, inset := panel.Min.Y+76, panel.Max.Y-76, 18
	if height >= 700 {
		top, bottom, inset = panel.Min.Y+104, panel.Max.Y-106, 26
	}
	return image.Rect(panel.Min.X+inset, top, panel.Max.X-inset, bottom)
}

func (r *renderer) cameraVideoActive() bool {
	return r.cameraOpen && r.state.Cameras != nil && r.state.Cameras.SelectedID != ""
}

func (r *renderer) cameraVideoRenderKey(accent color.RGBA) string {
	if r.state.Cameras == nil {
		return ""
	}
	return r.state.Cameras.SelectedID + "|" + r.state.Cameras.SelectedName + "|" +
		r.state.Cameras.SelectedRoom + "|" + r.state.Cameras.Error + "|" +
		fmt.Sprint(r.state.Cameras.SelectedTalkback, r.cameraTalkPressed, accent)
}

func (r *renderer) drawCameraVideoFrame(canvas *image.RGBA) image.Rectangle {
	bounds := r.cameraVideoBounds().Intersect(canvas.Rect)
	radius := max(14, min(28, bounds.Dy()/12))
	drawMenuRoundedRect(canvas, bounds, radius, color.RGBA{0, 0, 0, 255})
	if r.cameraFrameImage != nil {
		drawMenuRoundedImage(canvas, bounds, r.cameraFrameImage, radius, menuBackdropInk)
	}
	drawMenuRoundedOutline(canvas, bounds, radius, 2, color.RGBA{menuCyanInk.R, menuCyanInk.G, menuCyanInk.B, 220})
	return bounds
}

func (r *renderer) drawCameraScreen(canvas *image.RGBA, now time.Time, accent color.RGBA) {
	width, height := canvas.Rect.Dx(), canvas.Rect.Dy()
	r.drawActionMenuBackground(canvas, now, accent, 88)
	panel := cameraDirectoryLayoutFor(width, height, false).panel
	drawMenuGlassPanel(canvas, panel, max(24, height/22))
	r.drawCameraContents(canvas, false)
}

func (r *renderer) drawSpotCameraScreen(canvas *image.RGBA, now time.Time, accent color.RGBA) {
	r.drawActionMenuBackground(canvas, now, accent, 84)
	panel := cameraDirectoryLayoutFor(480, 480, true).panel
	drawMenuSuperellipseShadow(canvas, panel, 8)
	drawMenuSuperellipse(canvas, panel, menuBackdropInk)
	drawMenuSuperellipseOutline(canvas, panel, 2, color.RGBA{menuCyanInk.R, menuCyanInk.G, menuCyanInk.B, 230})
	r.drawCameraContents(canvas, true)
	maskOutsideSpotCircle(canvas, 236)
}

func (r *renderer) drawCameraContents(canvas *image.RGBA, spot bool) {
	width, height := canvas.Rect.Dx(), canvas.Rect.Dy()
	selected := r.state.Cameras != nil && r.state.Cameras.SelectedID != ""
	close := r.cameraCloseBounds(width, height, spot, selected)
	drawMenuCloseButton(canvas, close)
	cameras := r.state.Cameras
	if cameras != nil && cameras.SelectedID != "" {
		bounds := r.cameraVideoBounds()
		radius := max(14, min(28, bounds.Dy()/12))
		drawMenuRoundedRect(canvas, bounds, radius, color.RGBA{0, 0, 0, 255})
		if r.cameraFrameImage == nil {
			r.centeredText(canvas, width/2, (bounds.Min.Y+bounds.Max.Y)/2, "CONNECTING TO CAMERA…", func() int {
				if spot {
					return 14
				}
				return max(17, height/32)
			}(), true, color.RGBA{220, 232, 245, 255})
		} else {
			drawMenuRoundedImage(canvas, bounds, r.cameraFrameImage, radius, menuBackdropInk)
		}
		drawMenuRoundedOutline(canvas, bounds, radius, 2, color.RGBA{menuCyanInk.R, menuCyanInk.G, menuCyanInk.B, 225})
		back := cameraBackBounds(width, height, spot)
		drawMenuRoundedRect(canvas, back, back.Dy()/2, menuControlInk)
		drawMenuRoundedOutline(canvas, back, back.Dy()/2, 1, menuOutlineInk)
		r.centeredText(canvas, (back.Min.X+back.Max.X)/2, back.Min.Y+back.Dy()*2/3, "‹ BACK", func() int {
			if spot {
				return 12
			}
			return 17
		}(), true, color.White)
		nameSize := 17
		if spot {
			nameSize = 14
		} else if height >= 700 {
			nameSize = 25
		}
		label := cameras.SelectedName
		if cameras.SelectedRoom != "" {
			label += "  ·  " + cameras.SelectedRoom
		}
		r.centeredText(canvas, width/2, func() int {
			if spot {
				return 89
			}
			return max(58, height/13)
		}(), fitTextToWidth(label, r.faces.bold[nameSize], width/2), nameSize, true, color.White)
		if cameras.SelectedTalkback {
			talk := r.cameraTalkBounds(width, height, spot)
			ink, outline := color.RGBA{20, 28, 36, 235}, menuOrangeInk
			if r.cameraTalkPressed {
				ink = color.RGBA{94, 48, 14, 245}
			}
			drawMenuRoundedRect(canvas, talk, talk.Dy()/2, ink)
			drawMenuRoundedOutline(canvas, talk, talk.Dy()/2, 2, outline)
			micX := talk.Min.X + max(28, talk.Dy()*3/4)
			drawSatChatMicGlyph(canvas, micX, (talk.Min.Y+talk.Max.Y)/2-2, max(8, talk.Dy()/5), menuOrangeInk)
			r.centeredText(canvas, (talk.Min.X+talk.Max.X)/2+12, talk.Min.Y+talk.Dy()*2/3, "HOLD TO TALK", func() int {
				if spot {
					return 13
				}
				return 18
			}(), true, color.White)
		}
		if cameras.Error != "" {
			r.centeredText(canvas, width/2, bounds.Max.Y-14, fitTextToWidth(cameras.Error, r.faces.regular[13], bounds.Dx()-40), 13, false, color.RGBA{255, 166, 121, 255})
		}
		return
	}
	titleSize := 25
	if !spot && height >= 700 {
		titleSize = 42
	} else if !spot {
		titleSize = 30
	}
	if spot {
		r.centeredText(canvas, 240, 99, "CAMERAS", titleSize, true, color.White)
	} else {
		layout := cameraDirectoryLayoutFor(width, height, false)
		iconSize := 50
		if height >= 700 {
			iconSize = 68
		}
		iconBounds := image.Rect(layout.panel.Min.X+22, layout.panel.Min.Y+20, layout.panel.Min.X+22+iconSize, layout.panel.Min.Y+20+iconSize)
		drawMenuIconMedallion(canvas, iconBounds)
		r.drawActionMenuGlyph(canvas, actionMenuCameras, (iconBounds.Min.X+iconBounds.Max.X)/2, (iconBounds.Min.Y+iconBounds.Max.Y)/2, iconSize*28/100, menuCyanInk)
		textX := iconBounds.Max.X + 16
		r.text(canvas, textX, layout.panel.Min.Y+46, "CAMERAS", titleSize, true, color.White)
		r.text(canvas, textX, layout.panel.Min.Y+72, "Choose a camera to open live view", 15, false, menuSubtitleInk)
	}
	items := []show.Camera{}
	if cameras != nil {
		items = cameras.Items
	}
	if len(items) == 0 {
		r.drawActionMenuGlyph(canvas, actionMenuCameras, width/2, height/2-20, min(width, height)/10, color.RGBA{117, 151, 183, 255})
		r.centeredText(canvas, width/2, height/2+70, "NO CAMERA FEEDS AVAILABLE", func() int {
			if spot {
				return 16
			}
			return max(17, titleSize/2)
		}(), true, color.RGBA{224, 233, 244, 255})
		return
	}
	perPage := cameraPageSize(width, height, spot)
	pages := max(1, (len(items)+perPage-1)/perPage)
	if r.cameraPage < 0 {
		r.cameraPage = 0
	} else if r.cameraPage >= pages {
		r.cameraPage = pages - 1
	}
	start := r.cameraPage * perPage
	end := min(len(items), start+perPage)
	if spot {
		r.centeredText(canvas, 240, 119, fmt.Sprintf("%d–%d OF %d", start+1, end, len(items)), 10, true, color.RGBA{215, 224, 237, 245})
	} else if pages == 1 {
		panel := cameraDirectoryLayoutFor(width, height, false).panel
		r.centeredText(canvas, panel.Max.X-140, panel.Min.Y+48, fmt.Sprintf("%d AVAILABLE", len(items)), 12, true, menuSubtitleInk)
	}
	for index := 0; index < perPage && start+index < len(items); index++ {
		item := items[start+index]
		bounds := cameraCardBounds(width, height, index, spot)
		radius := max(12, bounds.Dy()/4)
		drawMenuCard(canvas, bounds, radius, !item.Available)
		avatarLimit := 26
		if !spot && height >= 700 {
			avatarLimit = 42
		}
		avatarRadius := min(avatarLimit, bounds.Dy()/2-7)
		avatarX, centerY := bounds.Min.X+avatarRadius+18, (bounds.Min.Y+bounds.Max.Y)/2
		avatar := image.Rect(avatarX-avatarRadius, centerY-avatarRadius, avatarX+avatarRadius, centerY+avatarRadius)
		drawMenuIconMedallion(canvas, avatar)
		r.drawActionMenuGlyph(canvas, actionMenuCameras, avatarX, centerY, max(8, avatarRadius/2), menuCyanInk)
		statusX := avatarX + avatarRadius + 20
		circle(canvas, statusX, centerY-8, 6, func() color.RGBA {
			if item.Available {
				return color.RGBA{42, 233, 113, 255}
			}
			return color.RGBA{126, 137, 150, 255}
		}())
		nameSize, subSize, capabilitySize := 19, 12, 10
		if spot {
			nameSize, subSize, capabilitySize = 16, 9, 9
		} else if height >= 700 {
			nameSize, subSize, capabilitySize = 24, 15, 14
		}
		nameLeft := statusX + 20
		r.text(canvas, nameLeft, centerY-2, fitTextToWidth(item.Name, r.faces.bold[nameSize], max(90, bounds.Max.X-nameLeft-116)), nameSize, true, color.White)
		sub := "LIVE VIEW"
		if item.Room != "" {
			sub = strings.ToUpper(item.Room)
		}
		if item.Talkback {
			sub += "  ·  TWO-WAY AUDIO"
		}
		r.text(canvas, nameLeft, centerY+20, fitTextToWidth(sub, r.faces.bold[subSize], max(90, bounds.Max.X-nameLeft-95)), subSize, true, menuSubtitleInk)
		iconInk := menuOrangeInk
		if item.Talkback {
			drawSatChatMicGlyph(canvas, bounds.Max.X-max(54, bounds.Dy()/3), centerY-4, capabilitySize, iconInk)
		}
		drawMenuCameraGlyph(canvas, bounds.Max.X-func() int {
			if item.Talkback {
				return max(94, bounds.Dy()*2/3)
			}
			return max(54, bounds.Dy()/3)
		}(), centerY-4, capabilitySize, iconInk)
		drawMenuChevron(canvas, bounds.Max.X-24, (bounds.Min.Y+bounds.Max.Y)/2, 7, color.RGBA{219, 232, 244, 235})
	}
	if pages > 1 {
		for index, button := range []struct {
			bounds image.Rectangle
			label  string
		}{{cameraPreviousBounds(width, height, spot), "‹ PREV"}, {cameraNextBounds(width, height, spot), "NEXT ›"}} {
			buttonInk := menuOrangeInk
			if (index == 0 && r.cameraPage == 0) || (index == 1 && r.cameraPage == pages-1) {
				buttonInk = color.RGBA{92, 97, 106, 160}
			}
			drawMenuRoundedRect(canvas, button.bounds, button.bounds.Dy()/2, menuControlInk)
			drawMenuRoundedOutline(canvas, button.bounds, button.bounds.Dy()/2, 2, buttonInk)
			r.centeredText(canvas, (button.bounds.Min.X+button.bounds.Max.X)/2, button.bounds.Min.Y+button.bounds.Dy()*2/3, button.label, func() int {
				if spot {
					return 11
				}
				return 12
			}(), true, buttonInk)
		}
		if !spot {
			previous := cameraPreviousBounds(width, height, false)
			r.centeredText(canvas, width/2, previous.Min.Y+previous.Dy()*2/3, fmt.Sprintf("%d–%d OF %d", start+1, end, len(items)), 12, true, menuSubtitleInk)
		}
	}
}
