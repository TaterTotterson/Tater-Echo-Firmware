//go:build linux

package main

import (
	"fmt"
	"image"
	"image/draw"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/linuxinput"
)

type rookSettingControl struct {
	row    settingRow
	bounds image.Rectangle
}

func (r *renderer) rookSettingControls() (string, []rookSettingControl, int) {
	title, rows := r.settingRows()
	count := 3
	if r.settingsPage == "equalizer" {
		return title, nil, 2
	}
	if r.settingsPage == "sound" || r.settingsPage == "" || r.settingsPage == "dnd" {
		count = 4
	}
	pages := max(1, (len(rows)+count-1)/count)
	r.settingsIndex = max(0, min(pages-1, r.settingsIndex))
	start := r.settingsIndex * count
	controls := make([]rookSettingControl, 0, count)
	y := 134
	for index := start; index < min(len(rows), start+count); index++ {
		row, height := rows[index], 64
		if row.kind == "slider" {
			height = 86
		}
		box := image.Rect(77, y, 403, y+height)
		if count == 4 && r.settingsPage != "dnd" {
			if index == 2 {
				box = image.Rect(77, 304, 235, 368)
			} else if index == 3 {
				box = image.Rect(245, 304, 403, 368)
			}
		} else if r.settingsPage == "dnd" {
			y = 130 + index*57
			if index >= 2 {
				y += 8
			}
			box = image.Rect(77, y, 403, y+55)
		}
		controls = append(controls, rookSettingControl{row, box})
		y += height + 10
	}
	return title, controls, pages
}

func (r *renderer) drawRookSettings(canvas *image.RGBA) {
	draw.Draw(canvas, canvas.Rect, image.NewUniform(workspaceBackground), image.Point{}, draw.Src)
	title, controls, pages := r.rookSettingControls()
	r.drawRookHeader(canvas, title, "", true)
	if r.settingsPage == "equalizer" {
		r.drawRookEqualizer(canvas)
	} else {
		for _, control := range controls {
			r.drawRookSetting(canvas, control)
		}
	}
	if pages > 1 {
		for _, next := range []bool{false, true} {
			box := settingsPageButton(480, 480, next, true)
			drawMenuRoundedRect(canvas, box, box.Dy()/2, workspacePanel)
			ink := workspaceText
			if next && r.settingsIndex == pages-1 || !next && r.settingsIndex == 0 {
				ink = workspaceBorder
			}
			cx, cy := box.Min.X+box.Dx()/2, box.Min.Y+box.Dy()/2
			direction := -1
			if next {
				direction = 1
			}
			line(canvas, cx-direction*3, cy-6, cx+direction*3, cy, 2, ink)
			line(canvas, cx+direction*3, cy, cx-direction*3, cy+6, 2, ink)
		}
		r.centeredText(canvas, 240, 415, rookPageLabel(r.settingsIndex, pages), 16, false, workspaceMuted)
	}
	if r.state.SettingsError != "" {
		r.centeredText(canvas, 240, 378, fitTextToWidth(r.state.SettingsError, r.faces.regular[13], 260), 13, false, workspaceMuted)
	}
	maskOutsideSpotCircle(canvas, rookPanelRadius)
}

func (r *renderer) drawRookSetting(canvas *image.RGBA, control rookSettingControl) {
	row, box := control.row, control.bounds
	drawMenuRoundedRect(canvas, box, 14, workspacePanel)
	left, right, baseline := box.Min.X+16, box.Max.X-16, box.Min.Y+box.Dy()/2+6
	value := r.settingValue(row.key)
	if row.kind == "read" {
		r.text(canvas, left, box.Min.Y+21, row.label, 15, false, workspaceMuted)
		r.text(canvas, left, box.Min.Y+47, fitTextToWidth(row.options, r.faces.regular[20], box.Dx()-32), 20, false, workspaceText)
		return
	}
	labelWidth := box.Dx() - 144
	label := row.label
	if row.key == "display_edge_glow" {
		label = "Voice edge glow"
	}
	if row.kind == "toggle" {
		labelWidth = box.Dx() - 104
	}
	if row.kind == "slider" || row.kind == "link" {
		labelWidth = box.Dx() - 56
	}
	size := 20
	if box.Dx() < 200 {
		size = 17
	}
	if row.kind == "slider" {
		baseline = box.Min.Y + 28
	}
	lines := wrapTextToWidth(label, r.faces.regular[size], labelWidth)
	if len(lines) > 1 && row.kind == "link" {
		for index, text := range lines[:min(2, len(lines))] {
			r.text(canvas, left, baseline-10+index*21, text, size, false, workspaceText)
		}
	} else {
		r.text(canvas, left, baseline, fitTextToWidth(label, r.faces.regular[size], labelWidth), size, false, workspaceText)
	}
	switch row.kind {
	case "slider":
		text := fmt.Sprintf("%d%%", settingNumber(value))
		r.text(canvas, right-textWidth(r.faces.regular[18], text), baseline, text, 18, false, workspaceMuted)
		y := box.Max.Y - 22
		line(canvas, left, y, right, y, 6, workspaceBorder)
		fraction := math.Max(0, math.Min(1, (settingFloat(value)-float64(row.minimum))/float64(max(1, row.maximum-row.minimum))))
		x := left + int(fraction*float64(right-left))
		line(canvas, left, y, x, y, 6, r.menuPalette().primary)
		circle(canvas, x, y, 10, workspaceText)
	case "toggle":
		r.drawRookToggle(canvas, image.Rect(right-52, baseline-22, right, baseline+6), settingBool(value))
	case "link":
		drawMenuChevron(canvas, right-2, baseline-6, 4, workspaceMuted)
	default:
		text := settingLabel(value)
		if row.kind == "time" {
			text = "−  " + fmt.Sprint(value) + "  +"
		}
		text = fitTextToWidth(text, r.faces.regular[18], 124)
		r.text(canvas, right-textWidth(r.faces.regular[18], text), baseline, text, 18, false, workspaceMuted)
	}
}

func (r *renderer) drawRookToggle(canvas *image.RGBA, box image.Rectangle, on bool) {
	ink, x := workspaceControl, box.Min.X+box.Dy()/2
	if on {
		ink, x = r.menuPalette().primary, box.Max.X-box.Dy()/2
	}
	drawMenuRoundedRect(canvas, box, box.Dy()/2, ink)
	circle(canvas, x, box.Min.Y+box.Dy()/2, box.Dy()/2-4, workspaceText)
}

func rookEqualizerCell(index int) image.Rectangle {
	return image.Rect(92+index*74, 145, 92+(index+1)*74, 327)
}

func rookEqualizerTrack(index int) image.Rectangle {
	cell := rookEqualizerCell(index)
	x := cell.Min.X + cell.Dx()/2
	return image.Rect(x-3, 173, x+3, 293)
}

func (r *renderer) drawRookEqualizer(canvas *image.RGBA) {
	_, rows := r.settingRows()
	start := max(0, min(1, r.settingsIndex)) * 4
	for index := 0; index < 4; index++ {
		row, cell, track := rows[start+index], rookEqualizerCell(index), rookEqualizerTrack(index)
		cx := cell.Min.X + cell.Dx()/2
		value := math.Max(-12, math.Min(12, settingFloat(r.settingValue(row.key))))
		r.centeredText(canvas, cx, 156, fmt.Sprintf("%+.1f dB", value), 14, false, workspaceMuted)
		line(canvas, cx, track.Min.Y, cx, track.Max.Y, 6, workspaceBorder)
		zero := track.Min.Y + track.Dy()/2
		y := track.Max.Y - int((value+12)*float64(track.Dy())/24)
		line(canvas, cx, min(y, zero), cx, max(y, zero), 6, r.menuPalette().primary)
		drawMenuRoundedRect(canvas, image.Rect(cx-14, y-5, cx+14, y+5), 5, workspaceText)
		r.centeredText(canvas, cx, 321, strings.ReplaceAll(strings.TrimSuffix(row.label, " Hz"), " kHz", "k"), 15, false, workspaceMuted)
	}
	if start > 0 {
		r.text(canvas, 101, 359, "Presence boost", 17, false, workspaceText)
		r.drawRookToggle(canvas, image.Rect(327, 339, 379, 367), settingBool(r.settingValue("eq_loudness")))
	} else {
		r.centeredText(canvas, 240, 353, "Frequency · Hz", 15, false, workspaceMuted)
	}
}

func (r *renderer) handleRookSettingsTouch(client *socketClient, event linuxinput.TouchEvent) {
	point := image.Pt(event.X, event.Y)
	if event.Kind == linuxinput.Up && point.In(settingsBackBounds(true)) {
		parent := map[string]string{"equalizer": "sound", "dnd": "sound", "appearance": "display", "night": "display"}[r.settingsPage]
		if parent == "" {
			r.settingsOpen, r.menuOpen = false, true
		} else {
			r.settingsPage = parent
		}
		r.settingsIndex, r.settingsDragging, r.settingsDraft = 0, "", nil
		return
	}
	_, controls, pages := r.rookSettingControls()
	if event.Kind == linuxinput.Up && r.settingsDragging == "" && pages > 1 {
		if point.In(settingsPageButton(480, 480, false, true)) {
			r.settingsIndex = max(0, r.settingsIndex-1)
			return
		}
		if point.In(settingsPageButton(480, 480, true, true)) {
			r.settingsIndex = min(pages-1, r.settingsIndex+1)
			return
		}
	}
	if r.settingsPage == "equalizer" {
		r.handleRookEqualizerTouch(client, event)
		return
	}
	for _, control := range controls {
		row, box := control.row, control.bounds
		if row.kind == "slider" && ((point.In(box) && point.Y >= box.Max.Y-42) || r.settingsDragging == row.key) {
			if r.settingsDragging == "" && event.Kind == linuxinput.Move {
				return
			}
			left, right := box.Min.X+16, box.Max.X-16
			selected := row.minimum + max(0, min(right-left, event.X-left))*(row.maximum-row.minimum)/max(1, right-left)
			if r.settingsDraft == nil {
				r.settingsDraft = map[string]any{}
			}
			r.settingsDraft[row.key], r.settingsDragging = selected, row.key
			if event.Kind == linuxinput.Up {
				r.commitSettings(client, map[string]any{row.key: selected})
				r.settingsDragging = ""
			}
			return
		}
		if !point.In(box) || event.Kind != linuxinput.Up {
			continue
		}
		value := r.settingValue(row.key)
		switch row.kind {
		case "link":
			r.settingsPage, r.settingsIndex = row.target, 0
		case "toggle":
			r.commitSettings(client, map[string]any{row.key: !settingBool(value)})
		case "choice":
			options, next := strings.Split(row.options, "|"), 0
			for index, option := range options {
				if option == fmt.Sprint(value) {
					next = (index + 1) % len(options)
					break
				}
			}
			var selected any = options[next]
			if row.key == "display_wallpaper_interval_minutes" {
				selected = settingNumber(selected)
			}
			r.commitSettings(client, map[string]any{row.key: selected})
		case "time":
			parsed, err := time.Parse("15:04", fmt.Sprint(value))
			if err == nil {
				delta := 15 * time.Minute
				if event.X < box.Max.X-76 {
					delta = -delta
				}
				r.commitSettings(client, map[string]any{row.key: parsed.Add(delta).Format("15:04")})
			}
		}
		return
	}
}

func (r *renderer) handleRookEqualizerTouch(client *socketClient, event linuxinput.TouchEvent) {
	point := image.Pt(event.X, event.Y)
	if r.settingsIndex == 1 && event.Kind == linuxinput.Up && r.settingsDragging == "" && point.In(image.Rect(90, 331, 390, 374)) {
		r.commitSettings(client, map[string]any{"eq_loudness": !settingBool(r.settingValue("eq_loudness"))})
		return
	}
	index := -1
	if strings.HasPrefix(r.settingsDragging, "eq_band_") {
		band, _ := strconv.Atoi(strings.TrimPrefix(r.settingsDragging, "eq_band_"))
		index = band - r.settingsIndex*4
	} else if event.Kind == linuxinput.Down {
		for band := 0; band < 4; band++ {
			if point.In(rookEqualizerCell(band)) {
				index = band
				break
			}
		}
	}
	if index < 0 || index >= 4 {
		return
	}
	track := rookEqualizerTrack(index)
	value := math.Round((12-float64(max(0, min(track.Dy(), event.Y-track.Min.Y)))*24/float64(track.Dy()))*2) / 2
	key := fmt.Sprintf("eq_band_%d", r.settingsIndex*4+index)
	if r.settingsDraft == nil {
		r.settingsDraft = map[string]any{}
	}
	r.settingsDraft[key], r.settingsDragging = value, key
	if event.Kind == linuxinput.Up {
		r.commitSettings(client, map[string]any{key: value})
		r.settingsDragging = ""
	}
}
