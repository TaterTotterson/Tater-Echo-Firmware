//go:build linux

package main

import (
	"fmt"
	"image"
	"math"
	"strconv"
	"strings"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/linuxinput"
)

func equalizerBandBounds(width, height, index int) image.Rectangle {
	left, right := 48, width-48
	return image.Rect(left+(right-left)*index/8, 142, left+(right-left)*(index+1)/8, height-128)
}

func equalizerTrackBounds(width, height, index int) image.Rectangle {
	cell := equalizerBandBounds(width, height, index)
	return image.Rect(cell.Min.X+cell.Dx()/2-3, cell.Min.Y+12, cell.Min.X+cell.Dx()/2+3, cell.Max.Y-12)
}

func equalizerPresenceBounds(width, height int) image.Rectangle {
	return image.Rect(48, height-94, width-48, height-36)
}

func (r *renderer) drawWorkspaceEqualizer(canvas *image.RGBA) {
	width, height := canvas.Rect.Dx(), canvas.Rect.Dy()
	panel := image.Rect(28, 96, width-28, height-22)
	connectedPanel(canvas, panel)
	_, rows := r.settingRows()
	size := 17
	if height >= 600 {
		size = 24
	}
	for index, row := range rows[:8] {
		cell, track := equalizerBandBounds(width, height, index), equalizerTrackBounds(width, height, index)
		cx := cell.Min.X + cell.Dx()/2
		value := math.Max(-12, math.Min(12, settingFloat(r.settingValue(row.key))))
		r.centeredText(canvas, cx, cell.Min.Y-12, fmt.Sprintf("%+.1f dB", value), size, false, workspaceText)
		line(canvas, cx, track.Min.Y, cx, track.Max.Y, 6, workspaceBorder)
		zero := track.Min.Y + track.Dy()/2
		line(canvas, cx-12, zero, cx+12, zero, 1, workspaceMuted)
		y := track.Max.Y - int((value+12)*float64(track.Dy())/24)
		line(canvas, cx, min(y, zero), cx, max(y, zero), 6, r.menuPalette().primary)
		thumb := image.Rect(cx-17, y-6, cx+17, y+6)
		drawMenuRoundedRect(canvas, thumb, 5, workspaceText)
		if r.settingsDragging == row.key {
			drawMenuRoundedOutline(canvas, thumb.Inset(-3), 7, 2, r.menuPalette().primary)
		}
		r.centeredText(canvas, cx, cell.Max.Y+25, row.label, size, false, workspaceMuted)
	}
	box := equalizerPresenceBounds(width, height)
	line(canvas, box.Min.X, box.Min.Y-6, box.Max.X, box.Min.Y-6, 1, workspaceBorder)
	r.text(canvas, box.Min.X+12, box.Min.Y+box.Dy()/2+size/3, "Presence boost", size+1, false, workspaceText)
	toggle := image.Rect(box.Max.X-76, box.Min.Y+(box.Dy()-32)/2, box.Max.X-12, box.Min.Y+(box.Dy()+32)/2)
	ink, x := workspaceControl, toggle.Min.X+16
	if settingBool(r.settingValue("eq_loudness")) {
		ink, x = r.menuPalette().primary, toggle.Max.X-16
	}
	drawMenuRoundedRect(canvas, toggle, 16, ink)
	circle(canvas, x, toggle.Min.Y+16, 12, workspaceText)
	if r.state.SettingsError != "" {
		r.centeredText(canvas, width/2, height-4, fitTextToWidth(r.state.SettingsError, r.faces.regular[12], width-40), 12, false, workspaceMuted)
	}
}

func (r *renderer) handleWorkspaceEqualizerTouch(client *socketClient, event linuxinput.TouchEvent, width, height int) {
	point := image.Pt(event.X, event.Y)
	if event.Kind == linuxinput.Up && r.settingsDragging == "" && point.In(equalizerPresenceBounds(width, height)) {
		r.commitSettings(client, map[string]any{"eq_loudness": !settingBool(r.settingValue("eq_loudness"))})
		return
	}
	index := -1
	if strings.HasPrefix(r.settingsDragging, "eq_band_") {
		index, _ = strconv.Atoi(strings.TrimPrefix(r.settingsDragging, "eq_band_"))
	} else if event.Kind == linuxinput.Down {
		for band := 0; band < 8; band++ {
			if point.In(equalizerBandBounds(width, height, band)) {
				index = band
				break
			}
		}
	}
	if index < 0 || index >= 8 {
		return
	}
	track := equalizerTrackBounds(width, height, index)
	value := math.Round((12-float64(max(0, min(track.Dy(), event.Y-track.Min.Y)))*24/float64(max(1, track.Dy())))*2) / 2
	key := fmt.Sprintf("eq_band_%d", index)
	if r.settingsDraft == nil {
		r.settingsDraft = map[string]any{}
	}
	r.settingsDraft[key], r.settingsDragging = value, key
	if event.Kind == linuxinput.Up {
		r.commitSettings(client, map[string]any{key: value})
		r.settingsDragging = ""
	}
}
