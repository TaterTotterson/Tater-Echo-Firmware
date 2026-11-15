//go:build linux

package main

import (
	"fmt"
	"image"
	"image/color"
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
		return settingsBackBounds(true)
	}
	size := max(48, min(72, height/10))
	return image.Rect(width-34-size, 28, width-34, 28+size)
}

func spotClimateRoomBounds(index int) image.Rectangle {
	bounds := []image.Rectangle{
		image.Rect(80, 140, 400, 210),
		image.Rect(80, 220, 400, 290),
		image.Rect(100, 300, 380, 370),
	}
	if index < 0 || index >= len(bounds) {
		return image.Rectangle{}
	}
	return bounds[index]
}

func spotClimateNextBounds() image.Rectangle {
	return settingsPageButton(480, 480, true, true)
}

func climateRoomPageCount(total int) int {
	return max(1, (total+2)/3)
}

func climateSetpointBounds(width, height int, plus bool, spot bool) image.Rectangle {
	if spot {
		cx := 92
		if plus {
			cx = 388
		}
		return image.Rect(cx-30, 182, cx+30, 242)
	}
	panel := workspaceClimatePanel(width, height)
	if height >= 600 {
		panel.Min.Y = 164
	}
	leftWidth := width * 54 / 100
	cx := panel.Min.X + 76
	if plus {
		cx = leftWidth - 76
	}
	cy, size := panel.Min.Y+panel.Dy()*30/100, max(64, min(84, height/7))
	return image.Rect(cx-size/2, cy-size/2, cx+size/2, cy+size/2)
}

func climateModeBounds(width, height, index int, spot bool) image.Rectangle {
	if spot {
		left, right, gap, top, bottom := 80, 400, 6, 330, 376
		cell := (right - left - 3*gap) / 4
		x := left + index*(cell+gap)
		return image.Rect(x, top, x+cell, bottom)
	}
	leftWidth := width * 54 / 100
	left, right := 46, leftWidth-18
	gap := 0
	cell := (right - left - 3*gap) / 4
	top, bottom := height*82/100, height*91/100
	x := left + index*(cell+gap)
	return image.Rect(x, top, x+cell, bottom)
}

func (r *renderer) handleClimateTouch(client *socketClient, event linuxinput.TouchEvent, width, height int) {
	point := image.Pt(event.X, event.Y)
	spot := width == height
	if point.In(climateCloseBounds(width, height, spot)) {
		if spot && r.rookClimateRooms {
			r.rookClimateRooms = false
			return
		}
		r.climateOpen = false
		r.climatePage = 0
		return
	}
	climate := r.state.Climate
	if spot {
		if !r.rookClimateRooms && point.In(rookClimateReadingsBounds()) && climate != nil && len(climate.RoomTemperatures) > 0 {
			r.rookClimateRooms, r.climatePage = true, 0
			return
		}
		if r.rookClimateRooms {
			if climate != nil {
				pages := climateRoomPageCount(len(climate.RoomTemperatures))
				if point.In(spotClimateNextBounds()) {
					r.climatePage = min(pages-1, r.climatePage+1)
				} else if point.In(settingsPageButton(480, 480, false, true)) {
					r.climatePage = max(0, r.climatePage-1)
				}
			}
			return
		}
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

func (r *renderer) drawClimate(canvas *image.RGBA, now time.Time, accent color.RGBA) {
	r.drawWorkspaceClimate(canvas)
}

func (r *renderer) drawSpotClimate(canvas *image.RGBA, now time.Time, accent color.RGBA) {
	r.drawWorkspaceSpotClimate(canvas)
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
