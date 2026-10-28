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

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/linuxinput"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
	"golang.org/x/image/font"
)

func (r *renderer) homeContextVisible() bool {
	return !r.notificationActive(time.Now()) &&
		(r.state.Timer == nil || !r.state.Timer.Active) &&
		(r.state.Media == nil || !r.state.Media.Active) &&
		!r.satChatVisible()
}

func climateHomeBounds(width, height int, spot bool) image.Rectangle {
	if spot {
		return image.Rect(48, 282, 432, 468)
	}
	return image.Rect(max(0, width-min(580, width*62/100)), height*48/100, width, height)
}

func climateCloseBounds(width, height int, spot bool) image.Rectangle {
	if spot {
		return image.Rect(370, 66, 420, 116)
	}
	size := max(48, min(72, height/10))
	return image.Rect(width-34-size, 28, width-34, 28+size)
}

func spotClimateRoomBounds(index int) image.Rectangle {
	bounds := []image.Rectangle{
		image.Rect(86, 362, 170, 406),
		image.Rect(190, 370, 290, 416),
		image.Rect(310, 362, 394, 406),
	}
	if index < 0 || index >= len(bounds) {
		return image.Rectangle{}
	}
	return bounds[index]
}

func spotClimateNextBounds() image.Rectangle {
	return image.Rect(328, 408, 368, 448)
}

func climateRoomPageCount(total int) int {
	return max(1, (total+2)/3)
}

func climateSetpointBounds(width, height int, plus bool, spot bool) image.Rectangle {
	if spot {
		cx := 72
		if plus {
			cx = 408
		}
		return image.Rect(cx-42, 190, cx+42, 274)
	}
	leftWidth := width * 52 / 100
	cx := leftWidth * 18 / 100
	if plus {
		cx = leftWidth * 82 / 100
	}
	cy, size := height*52/100, max(48, min(72, height/9))
	return image.Rect(cx-size/2, cy-size/2, cx+size/2, cy+size/2)
}

func climateModeBounds(width, height, index int, spot bool) image.Rectangle {
	if spot {
		left, right, gap, top, bottom := 62, 418, 7, 320, 354
		cell := (right - left - 3*gap) / 4
		x := left + index*(cell+gap)
		return image.Rect(x, top, x+cell, bottom)
	}
	leftWidth := width * 52 / 100
	left, right := max(22, leftWidth*12/100), leftWidth-max(22, leftWidth*12/100)
	gap := max(6, width/160)
	cell := (right - left - 3*gap) / 4
	top, bottom := height*82/100, height*91/100
	x := left + index*(cell+gap)
	return image.Rect(x, top, x+cell, bottom)
}

func (r *renderer) handleClimateTouch(client *socketClient, event linuxinput.TouchEvent, width, height int) {
	point := image.Pt(event.X, event.Y)
	spot := width == height
	if point.In(climateCloseBounds(width, height, spot)) {
		r.climateOpen = false
		r.climatePage = 0
		return
	}
	climate := r.state.Climate
	if spot && climate != nil && len(climate.RoomTemperatures) > 3 && point.In(spotClimateNextBounds()) {
		r.climatePage = (r.climatePage + 1) % climateRoomPageCount(len(climate.RoomTemperatures))
		return
	}
	if climate == nil || !climate.Available {
		return
	}
	if climate.CanSetTemperature && (point.In(climateSetpointBounds(width, height, false, spot)) || point.In(climateSetpointBounds(width, height, true, spot))) {
		value := 0.0
		if climate.TargetTemperature != nil {
			value = *climate.TargetTemperature
		} else if climate.CurrentTemperature != nil {
			value = *climate.CurrentTemperature
		} else {
			return
		}
		step := climate.TemperatureStep
		if step <= 0 {
			step = 1
		}
		if point.In(climateSetpointBounds(width, height, true, spot)) {
			value += step
		} else {
			value -= step
		}
		value = math.Max(climate.MinimumTemperature, math.Min(climate.MaximumTemperature, value))
		climate.TargetTemperature = &value
		client.sendClimateTemperature(climate.RoomID, value, climate.TemperatureUnit)
		return
	}
	if !climate.CanSetMode {
		return
	}
	for index, mode := range []string{"heat", "cool", "auto", "off"} {
		if point.In(climateModeBounds(width, height, index, spot)) && climateModeAvailable(climate, mode) {
			climate.HVACMode = mode
			client.sendClimateMode(climate.RoomID, mode)
			return
		}
	}
}

func climateModeAvailable(climate *show.Climate, mode string) bool {
	if climate == nil || !climate.Available || !climate.CanSetMode {
		return false
	}
	if len(climate.AvailableHVACModes) == 0 {
		return true
	}
	for _, available := range climate.AvailableHVACModes {
		if strings.EqualFold(strings.TrimSpace(available), mode) {
			return true
		}
	}
	return false
}

func climateTemperature(value *float64, _ string) string {
	if value == nil {
		return "--°"
	}
	return fmt.Sprintf("%.0f°", math.Round(*value))
}

func climateRoomTemperature(value float64) string {
	return fmt.Sprintf("%.0f°", math.Round(value))
}

func (r *renderer) climateBackground(canvas *image.RGBA, now time.Time, accent color.RGBA, alpha uint8) {
	if wallpaper := r.activeWallpaper(now); wallpaper != nil {
		drawPreparedCover(canvas, canvas.Bounds(), wallpaper)
	} else {
		fillGradient(canvas, color.RGBA{12, 14, 19, 255}, mix(color.RGBA{12, 14, 19, 255}, accent, .06))
	}
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.NRGBA{R: 5, G: 6, B: 8, A: alpha}), image.Point{}, draw.Over)
}

func (r *renderer) drawClimate(canvas *image.RGBA, now time.Time, accent color.RGBA) {
	width, height := canvas.Rect.Dx(), canvas.Rect.Dy()
	palette := r.menuPalette()
	r.climateBackground(canvas, now, accent, 88)
	panel := image.Rect(24, 20, width-24, height-20)
	drawMenuGlassPanel(canvas, panel, max(24, height/22), palette.primary)
	climate := r.state.Climate
	enabled := climate != nil && climate.Available
	ink := palette.primary
	if !enabled {
		ink = color.RGBA{130, 143, 160, 255}
	}
	headerSize := 30
	roomSize, targetSize, targetLabelSize, currentSize, actionSize, modeSize, roomLabelSize, roomValueSize := 18, 64, 12, 14, 14, 11, 12, 19
	if height >= 700 {
		headerSize = 36
		roomSize, targetSize, targetLabelSize, currentSize, actionSize, modeSize, roomLabelSize, roomValueSize = 25, 96, 18, 20, 22, 16, 19, 26
	}
	iconSize := 50
	if height >= 700 {
		iconSize = 68
	}
	iconBounds := image.Rect(panel.Min.X+22, panel.Min.Y+20, panel.Min.X+22+iconSize, panel.Min.Y+20+iconSize)
	drawMenuIconMedallion(canvas, iconBounds, palette.primary)
	r.drawActionMenuGlyph(canvas, actionMenuClimate, (iconBounds.Min.X+iconBounds.Max.X)/2, (iconBounds.Min.Y+iconBounds.Max.Y)/2, iconSize*32/100, palette.primary)
	r.text(canvas, iconBounds.Max.X+16, panel.Min.Y+48, "CLIMATE", headerSize, true, color.White)
	closeBounds := climateCloseBounds(width, height, false)
	drawMenuCloseButton(canvas, closeBounds)

	leftWidth := width * 52 / 100
	dividerX := leftWidth
	line(canvas, dividerX, height*25/100, dividerX, height*91/100, 1, color.RGBA{119, 124, 134, 120})
	room := "THERMOSTAT UNAVAILABLE"
	if enabled {
		room = strings.ToUpper(firstNonEmpty(climate.RoomName, "CLIMATE"))
	}
	r.centeredText(canvas, leftWidth/2, height*24/100, fitTextToWidth(room, r.faces.bold[roomSize], leftWidth-80), roomSize, true, color.RGBA{238, 244, 251, 255})

	cx, cy := leftWidth/2, height*52/100
	radius := max(72, min(height*25/100, leftWidth*25/100))
	cool := palette.primary
	warm := palette.secondary
	arc(canvas, cx, cy, radius, 135, 108, max(5, height/80), color.RGBA{cool.R, cool.G, cool.B, 220})
	arc(canvas, cx, cy, radius, 297, 108, max(5, height/80), color.RGBA{warm.R, warm.G, warm.B, 225})
	target := "--°"
	if enabled {
		target = climateTemperature(climate.TargetTemperature, climate.TemperatureUnit)
	}
	r.centeredText(canvas, cx, cy+targetSize/4, target, targetSize, true, color.White)
	r.centeredText(canvas, cx, cy+targetSize/2+20, "TARGET", targetLabelSize, true, color.RGBA{174, 192, 215, 255})
	current := "CURRENT --°"
	humidity := ""
	if enabled {
		current = "CURRENT " + climateTemperature(climate.CurrentTemperature, climate.TemperatureUnit)
		if climate.CurrentHumidity != nil {
			humidity = fmt.Sprintf("  •  %.0f%%", *climate.CurrentHumidity)
		}
	}
	r.centeredText(canvas, cx, cy+radius*62/100, current+humidity, currentSize, false, color.RGBA{231, 238, 247, 255})
	action := "UNAVAILABLE"
	if enabled {
		action = strings.ToUpper(firstNonEmpty(climate.HVACAction, climate.HVACMode, "IDLE"))
	}
	r.centeredText(canvas, cx, cy+radius*90/100, action, actionSize, true, warm)
	drawClimateRoundControl(canvas, climateSetpointBounds(width, height, false, false), "−", ink, enabled && climate.CanSetTemperature, r)
	drawClimateRoundControl(canvas, climateSetpointBounds(width, height, true, false), "+", ink, enabled && climate.CanSetTemperature, r)

	for index, mode := range []string{"HEAT", "COOL", "AUTO", "OFF"} {
		bounds := climateModeBounds(width, height, index, false)
		available := enabled && climateModeAvailable(climate, strings.ToLower(mode))
		selected := available && strings.EqualFold(climate.HVACMode, mode)
		fill := menuCardInk
		stroke := menuOutlineInk
		textInk := color.RGBA{179, 191, 207, 255}
		if selected {
			fill, stroke, textInk = color.RGBA{87, 45, 18, 235}, warm, color.RGBA{255, 255, 255, 255}
		} else if !available {
			textInk = color.RGBA{100, 111, 126, 255}
		}
		drawMenuRoundedRect(canvas, bounds, bounds.Dy()/2, fill)
		drawMenuRoundedOutline(canvas, bounds, bounds.Dy()/2, 2, stroke)
		r.centeredText(canvas, (bounds.Min.X+bounds.Max.X)/2, bounds.Min.Y+bounds.Dy()*65/100, mode, modeSize, true, textInk)
	}

	right := image.Rect(dividerX+max(28, width/40), height*24/100, width-50, height*88/100)
	r.text(canvas, right.Min.X, right.Min.Y, "ROOM TEMPERATURES", roomSize, true, color.White)
	rooms := []show.RoomTemperature{}
	if climate != nil {
		rooms = climate.RoomTemperatures
	}
	if len(rooms) == 0 {
		r.centeredText(canvas, (right.Min.X+right.Max.X)/2, (right.Min.Y+right.Max.Y)/2, "NO ROOM TEMPERATURES SELECTED", currentSize, true, color.RGBA{138, 153, 172, 255})
		return
	}
	cardsTop := right.Min.Y + max(30, height/20)
	gap := max(8, width/100)
	cardWidth := (right.Dx() - gap) / 2
	cardHeight := (right.Max.Y - cardsTop - 2*gap) / 3
	for index, roomTemperature := range rooms {
		if index >= 6 {
			break
		}
		column, row := index%2, index/2
		bounds := image.Rect(right.Min.X+column*(cardWidth+gap), cardsTop+row*(cardHeight+gap), right.Min.X+column*(cardWidth+gap)+cardWidth, cardsTop+row*(cardHeight+gap)+cardHeight)
		drawMenuCard(canvas, bounds, max(10, height/55), false, palette.primary)
		iconX := bounds.Min.X + max(22, bounds.Dx()/10)
		r.drawWeatherMetricGlyph(canvas, iconX, (bounds.Min.Y+bounds.Max.Y)/2, max(8, height/65), "temperature", ink)
		textLeft := iconX + max(22, bounds.Dx()/9)
		value := climateRoomTemperature(roomTemperature.Temperature)
		valueWidth := textWidth(r.faces.bold[roomValueSize], value)
		r.text(canvas, textLeft, bounds.Min.Y+bounds.Dy()*45/100, fitTextToWidth(roomTemperature.RoomName, r.faces.regular[roomLabelSize], bounds.Max.X-textLeft-valueWidth-18), roomLabelSize, false, color.RGBA{229, 236, 246, 255})
		r.text(canvas, bounds.Max.X-valueWidth-16, bounds.Min.Y+bounds.Dy()*63/100, value, roomValueSize, true, color.White)
	}
}

func (r *renderer) drawSpotClimate(canvas *image.RGBA, now time.Time, accent color.RGBA) {
	palette := r.menuPalette()
	r.climateBackground(canvas, now, accent, 84)
	panel := cameraDirectoryLayoutFor(480, 480, true).panel
	drawMenuSuperellipseShadow(canvas, panel, 8)
	drawMenuSuperellipse(canvas, panel, menuBackdropInk)
	drawMenuSuperellipseOutline(canvas, panel, 2, menuAlpha(palette.primary, 230))
	climate := r.state.Climate
	enabled := climate != nil && climate.Available
	ink, warm := palette.primary, palette.secondary
	if !enabled {
		ink, warm = color.RGBA{118, 132, 150, 255}, color.RGBA{118, 132, 150, 255}
	}
	iconBounds := image.Rect(218, 60, 262, 104)
	drawMenuIconMedallion(canvas, iconBounds, palette.primary)
	r.drawActionMenuGlyph(canvas, actionMenuClimate, 240, 82, 15, ink)
	r.centeredText(canvas, 240, 124, "CLIMATE", 28, true, color.White)
	drawMenuCloseButton(canvas, climateCloseBounds(480, 480, true))
	room := "UNAVAILABLE"
	if enabled {
		room = strings.ToUpper(firstNonEmpty(climate.RoomName, "CLIMATE"))
	}
	r.centeredText(canvas, 240, 150, fitTextToWidth(room, r.faces.bold[18], 270), 18, true, color.RGBA{237, 243, 250, 255})

	cx, cy, radius := 240, 238, 83
	arc(canvas, cx, cy, radius, 135, 108, 7, menuAlpha(palette.primary, 225))
	arc(canvas, cx, cy, radius, 297, 108, 7, color.RGBA{warm.R, warm.G, warm.B, 230})
	target := "--°"
	if enabled {
		target = climateTemperature(climate.TargetTemperature, climate.TemperatureUnit)
	}
	r.centeredText(canvas, cx, 246, target, 64, true, color.White)
	r.centeredText(canvas, cx, 268, "TARGET", 11, true, color.RGBA{174, 192, 215, 255})
	current, humidity := "CURRENT --°", ""
	if enabled {
		current = "CURRENT " + climateTemperature(climate.CurrentTemperature, climate.TemperatureUnit)
		if climate.CurrentHumidity != nil {
			humidity = fmt.Sprintf("  •  %.0f%%", *climate.CurrentHumidity)
		}
	}
	r.centeredText(canvas, cx, 288, current+humidity, 13, false, color.RGBA{233, 239, 247, 255})
	action := "UNAVAILABLE"
	if enabled {
		action = strings.ToUpper(firstNonEmpty(climate.HVACAction, climate.HVACMode, "IDLE"))
	}
	r.centeredText(canvas, cx, 309, action, 15, true, warm)
	drawClimateRoundControl(canvas, climateSetpointBounds(480, 480, false, true), "−", ink, enabled && climate.CanSetTemperature, r)
	drawClimateRoundControl(canvas, climateSetpointBounds(480, 480, true, true), "+", ink, enabled && climate.CanSetTemperature, r)

	for index, mode := range []string{"HEAT", "COOL", "AUTO", "OFF"} {
		bounds := climateModeBounds(480, 480, index, true)
		available := enabled && climateModeAvailable(climate, strings.ToLower(mode))
		selected := available && strings.EqualFold(climate.HVACMode, mode)
		fill, stroke, textInk := menuCardInk, menuOutlineInk, color.RGBA{181, 193, 209, 255}
		if selected {
			fill, stroke, textInk = color.RGBA{82, 43, 18, 238}, warm, color.RGBA{255, 255, 255, 255}
		} else if !available {
			textInk = color.RGBA{95, 106, 120, 255}
		}
		drawMenuRoundedRect(canvas, bounds, 18, fill)
		drawMenuRoundedOutline(canvas, bounds, 18, 1, stroke)
		r.centeredText(canvas, (bounds.Min.X+bounds.Max.X)/2, bounds.Min.Y+23, mode, 10, true, textInk)
	}

	rooms := []show.RoomTemperature{}
	if climate != nil {
		rooms = climate.RoomTemperatures
	}
	if len(rooms) == 0 {
		r.centeredText(canvas, 240, 392, "NO ROOM TEMPERATURES SELECTED", 10, true, color.RGBA{133, 148, 168, 255})
		maskOutsideSpotCircle(canvas, 236)
		return
	}
	pages := climateRoomPageCount(len(rooms))
	if r.climatePage < 0 {
		r.climatePage = 0
	} else if r.climatePage >= pages {
		r.climatePage = pages - 1
	}
	start := r.climatePage * 3
	end := min(len(rooms), start+3)
	for visibleIndex, row := range rooms[start:end] {
		bounds := spotClimateRoomBounds(visibleIndex)
		centerX := (bounds.Min.X + bounds.Max.X) / 2
		drawMenuCard(canvas, bounds, bounds.Dy()/2, false, palette.primary)
		r.centeredText(canvas, centerX, bounds.Min.Y+17, fitTextToWidth(row.RoomName, r.faces.regular[10], bounds.Dx()-10), 10, false, color.RGBA{228, 235, 245, 255})
		r.centeredText(canvas, centerX, bounds.Min.Y+35, climateRoomTemperature(row.Temperature), 16, true, color.White)
	}
	if pages > 1 {
		dotStart := 240 - (pages-1)*9/2
		for index := 0; index < pages; index++ {
			dotInk := color.RGBA{130, 151, 171, 220}
			if index == r.climatePage {
				dotInk = palette.secondary
			}
			circle(canvas, dotStart+index*9, 438, 3, dotInk)
		}
		next := spotClimateNextBounds()
		drawMenuRoundedRect(canvas, next, next.Dx()/2, menuControlInk)
		drawMenuRoundedOutline(canvas, next, next.Dx()/2, 2, menuOutlineInk)
		drawMenuChevron(canvas, (next.Min.X+next.Max.X)/2, (next.Min.Y+next.Max.Y)/2, 8, color.RGBA{255, 255, 255, 255})
	}
	maskOutsideSpotCircle(canvas, 236)
}

func drawClimateRoundControl(canvas *image.RGBA, bounds image.Rectangle, label string, ink color.RGBA, enabled bool, r *renderer) {
	cx, cy := (bounds.Min.X+bounds.Max.X)/2, (bounds.Min.Y+bounds.Max.Y)/2
	fill := menuControlInk
	stroke, labelInk := ink, color.RGBA{255, 255, 255, 255}
	if !enabled {
		fill, stroke, labelInk = color.RGBA{27, 34, 44, 220}, color.RGBA{83, 95, 109, 190}, color.RGBA{102, 113, 126, 255}
	}
	circle(canvas, cx, cy, bounds.Dx()/2, fill)
	circleOutline(canvas, cx, cy, bounds.Dx()/2, 2, stroke)
	fontSize := 28
	if bounds.Dy() >= 70 {
		fontSize = 36
	}
	r.centeredText(canvas, cx, cy+bounds.Dy()/7, label, fontSize, false, labelInk)
}

func drawClimateCloseGlyph(canvas *image.RGBA, bounds image.Rectangle, ink color.RGBA) {
	cx, cy := (bounds.Min.X+bounds.Max.X)/2, (bounds.Min.Y+bounds.Max.Y)/2
	size := bounds.Dx() / 5
	line(canvas, cx-size, cy-size, cx+size, cy+size, 3, ink)
	line(canvas, cx+size, cy-size, cx-size, cy+size, 3, ink)
}

func outlineRoundedRect(canvas *image.RGBA, bounds image.Rectangle, radius, thickness int, ink color.RGBA) {
	radius = max(1, min(radius, min(bounds.Dx(), bounds.Dy())/2))
	line(canvas, bounds.Min.X+radius, bounds.Min.Y, bounds.Max.X-radius, bounds.Min.Y, thickness, ink)
	line(canvas, bounds.Min.X+radius, bounds.Max.Y-1, bounds.Max.X-radius, bounds.Max.Y-1, thickness, ink)
	line(canvas, bounds.Min.X, bounds.Min.Y+radius, bounds.Min.X, bounds.Max.Y-radius, thickness, ink)
	line(canvas, bounds.Max.X-1, bounds.Min.Y+radius, bounds.Max.X-1, bounds.Max.Y-radius, thickness, ink)
	arc(canvas, bounds.Min.X+radius, bounds.Min.Y+radius, radius, 180, 90, thickness, ink)
	arc(canvas, bounds.Max.X-radius, bounds.Min.Y+radius, radius, 270, 90, thickness, ink)
	arc(canvas, bounds.Max.X-radius, bounds.Max.Y-radius, radius, 0, 90, thickness, ink)
	arc(canvas, bounds.Min.X+radius, bounds.Max.Y-radius, radius, 90, 90, thickness, ink)
}

func textWidth(face font.Face, text string) int { return font.MeasureString(face, text).Round() }
