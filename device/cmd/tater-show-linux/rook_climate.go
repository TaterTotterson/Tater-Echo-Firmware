//go:build linux

package main

import (
	"fmt"
	"image"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

func rookClimateReadingsBounds() image.Rectangle { return image.Rect(130, 390, 350, 434) }

func (r *renderer) drawRookClimate(canvas *image.RGBA) {
	connectedFill(canvas)
	climate := r.state.Climate
	if r.rookClimateRooms {
		r.drawRookHeader(canvas, "Room readings", "Climate", true)
		rooms := []show.RoomTemperature{}
		if climate != nil {
			rooms = climate.RoomTemperatures
		}
		pages := climateRoomPageCount(len(rooms))
		r.climatePage = max(0, min(pages-1, r.climatePage))
		start := r.climatePage * 3
		for index, room := range rooms[start:min(len(rooms), start+3)] {
			box := spotClimateRoomBounds(index)
			drawMenuRoundedRect(canvas, box, 14, workspacePanel)
			r.text(canvas, box.Min.X+18, box.Min.Y+42, fitTextToWidth(room.RoomName, r.faces.regular[19], box.Dx()-110), 19, false, workspaceText)
			r.text(canvas, box.Max.X-75, box.Min.Y+44, climateRoomTemperature(room.Temperature), 26, false, r.menuPalette().primary)
		}
		r.drawRookPager(canvas, r.climatePage, pages)
		maskOutsideSpotCircle(canvas, rookPanelRadius)
		return
	}
	enabled := climate != nil && climate.Available
	room, target, current, action := "Thermostat unavailable", "--°", "Current --°", "Unavailable"
	if enabled {
		room = firstNonEmpty(climate.RoomName, "Climate")
		target = climateTemperature(climate.TargetTemperature, climate.TemperatureUnit)
		current = "Current " + climateTemperature(climate.CurrentTemperature, climate.TemperatureUnit)
		if climate.CurrentHumidity != nil {
			current += fmt.Sprintf(" · Humidity %.0f%%", *climate.CurrentHumidity)
		}
		action = firstNonEmpty(climate.HVACAction, climate.HVACMode, "Idle")
	}
	r.drawRookHeader(canvas, "Climate", room, true)
	r.centeredText(canvas, 240, 239, target, 86, false, workspaceText)
	r.centeredText(canvas, 240, 263, "Target temperature", 16, false, workspaceMuted)
	r.centeredText(canvas, 240, 290, current, 16, false, workspaceMuted)
	r.centeredText(canvas, 240, 315, action, 18, false, r.menuPalette().primary)
	for _, plus := range []bool{false, true} {
		box := climateSetpointBounds(480, 480, plus, true)
		ink, label := workspaceText, "−"
		if plus {
			label = "+"
		}
		if !enabled || !climate.CanSetTemperature {
			ink = workspaceBorder
		}
		drawMenuRoundedRect(canvas, box, 30, workspacePanel)
		r.centeredText(canvas, box.Min.X+box.Dx()/2, box.Min.Y+box.Dy()/2+10, label, 30, false, ink)
	}
	r.drawWorkspaceClimateModes(canvas, 480, 480, true, 16)
	if climate != nil && len(climate.RoomTemperatures) > 0 {
		r.centeredText(canvas, 240, 419, "Room readings  ›", 18, false, workspaceMuted)
	}
	maskOutsideSpotCircle(canvas, rookPanelRadius)
}
