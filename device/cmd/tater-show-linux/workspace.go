//go:build linux

package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/linuxinput"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

var (
	workspaceBackground = color.RGBA{17, 18, 19, 255}
	workspacePanel      = color.RGBA{27, 28, 30, 255}
	workspaceControl    = color.RGBA{34, 36, 38, 255}
	workspaceBorder     = color.RGBA{52, 53, 54, 255}
	workspaceText       = color.RGBA{241, 238, 232, 255}
	workspaceMuted      = color.RGBA{176, 167, 156, 255}
)

var workspaceMenuItems = []struct {
	kind  actionMenuItem
	label string
}{
	{actionMenuSatChat, "Sat Chat"}, {actionMenuCameras, "Cameras"}, {actionMenuClimate, "Climate"},
	{actionMenuSound, "Sound & Wake"}, {actionMenuDisplay, "Display"}, {actionMenuInfo, "Device Info"},
}

func (r *renderer) drawPhotoHome(canvas *image.RGBA, now time.Time, accent color.RGBA) {
	r.drawWorkspaceHome(canvas, now, accent)
}

func (r *renderer) drawWorkspaceMenu(canvas *image.RGBA, spot bool) {
	width, height := canvas.Rect.Dx(), canvas.Rect.Dy()
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(workspaceBackground), image.Point{}, draw.Src)
	name := firstNonEmpty(r.state.Room, r.state.DeviceName, "Tater")
	size, baseline := 28, 51
	if height >= 600 {
		size, baseline = 42, 76
	}
	if spot {
		r.centeredText(canvas, 240, 100, fitTextToWidth(name, r.faces.regular[24], 240), 24, false, workspaceText)
	} else {
		r.text(canvas, 32, baseline, fitTextToWidth(name, r.faces.regular[size], width-170), size, false, workspaceText)
	}
	drawClimateCloseGlyph(canvas, actionMenuCloseBounds(width, height, spot), workspaceMuted)
	for index, item := range workspaceMenuItems {
		bounds := actionMenuBounds(width, height, index, spot)
		drawMenuRoundedRect(canvas, bounds, 12, workspacePanel)
		drawMenuRoundedOutline(canvas, bounds, 12, 1, workspaceBorder)
		if item.kind == r.menuPressed {
			drawMenuRoundedOutline(canvas, bounds, 12, 2, r.menuPalette().primary)
		}
		iconSize, labelSize := 26, 24
		if height >= 600 {
			iconSize, labelSize = 42, 34
		}
		if spot {
			iconSize, labelSize = 16, 14
		}
		cx, cy := bounds.Min.X+bounds.Dx()/2, bounds.Min.Y+bounds.Dy()/3
		r.drawWorkspaceGlyph(canvas, item.kind, cx, cy, iconSize, workspaceMuted)
		r.centeredText(canvas, cx, bounds.Max.Y-bounds.Dy()/6, fitTextToWidth(item.label, r.faces.regular[labelSize], bounds.Dx()-12), labelSize, false, workspaceText)
	}
	footer := actionMenuBroadcastBounds(width, height, spot)
	r.centeredText(canvas, width/2, footer.Min.Y+footer.Dy()/2+5, "Hold to broadcast", 14, false, workspaceMuted)
	if spot {
		maskOutsideSpotCircle(canvas, 236)
	}
}

func (r *renderer) drawWorkspaceGlyph(canvas *image.RGBA, kind actionMenuItem, cx, cy, size int, ink color.RGBA) {
	switch kind {
	case actionMenuSound:
		line(canvas, cx-size, cy-size/3, cx-size/3, cy-size/3, 2, ink)
		line(canvas, cx-size, cy+size/3, cx-size/3, cy+size/3, 2, ink)
		line(canvas, cx-size, cy-size/3, cx-size, cy+size/3, 2, ink)
		line(canvas, cx-size/3, cy-size/3, cx+size/3, cy-size, 2, ink)
		line(canvas, cx-size/3, cy+size/3, cx+size/3, cy+size, 2, ink)
		line(canvas, cx+size/3, cy-size, cx+size/3, cy+size, 2, ink)
		circleOutline(canvas, cx+size/2, cy, size/2, 2, ink)
	case actionMenuDisplay:
		drawMenuRoundedOutline(canvas, image.Rect(cx-size, cy-size*2/3, cx+size, cy+size/2), 4, 2, ink)
		line(canvas, cx, cy+size/2, cx, cy+size, 2, ink)
		line(canvas, cx-size/2, cy+size, cx+size/2, cy+size, 2, ink)
	case actionMenuInfo:
		circleOutline(canvas, cx, cy, size, 2, ink)
		circle(canvas, cx, cy-size/2, 2, ink)
		line(canvas, cx, cy-size/6, cx, cy+size/2, 3, ink)
	default:
		if kind == actionMenuSatChat {
			drawMenuSatelliteGlyph(canvas, cx, cy, size, ink, ink)
		} else if kind == actionMenuClimate {
			drawMenuThermometerGlyph(canvas, cx, cy, size, ink, ink)
		} else {
			r.drawActionMenuGlyph(canvas, kind, cx, cy, size, ink)
		}
	}
}

func (r *renderer) drawWorkspaceHome(canvas *image.RGBA, now time.Time, accent color.RGBA) {
	if wallpaper := r.activeWallpaper(now); wallpaper != nil {
		drawPreparedCover(canvas, canvas.Bounds(), wallpaper)
	} else {
		draw.Draw(canvas, canvas.Bounds(), image.NewUniform(workspaceBackground), image.Point{}, draw.Src)
	}
	width, height := canvas.Rect.Dx(), canvas.Rect.Dy()
	// A gentle veil covers the scene uniformly: never add a solid top bar.
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.NRGBA{A: 45}), image.Point{}, draw.Over)
	r.drawClassicWorkspaceHome(canvas, now)
	stage := checkersVoiceStageLabel(r.state.Phase)
	if r.state.Phase == "tool_call" {
		stage = "Working"
	}
	if stage != "" {
		// Keep transient status clear of the bottom-right home information.
		margin := classicWorkspaceHomeGeometry(width, height).margin
		r.text(canvas, margin, margin+14, stage, 14, false, workspaceText)
	}
}

func (r *renderer) workspaceCacheKey() string {
	return fmt.Sprintf("%t|%s|%d|%s|%s|%s|%t|%d", r.settingsOpen, r.settingsPage, r.settingsIndex, stableCacheValue(r.state.Settings), stableCacheValue(r.settingsDraft), r.state.SettingsError, r.gpuAvailable, r.menuPressed) + "|" + r.directoryCacheKey()
}

type settingRow struct {
	key, label, kind, options, target string
	minimum, maximum                  int
}

func (r *renderer) settingRows() (string, []settingRow) {
	switch r.settingsPage {
	case "display":
		return "Display", []settingRow{
			{"screen_brightness", "Screen brightness", "slider", "", "", 0, 100},
			{"screen_auto_brightness_enabled", "Automatic brightness", "toggle", "", "", 0, 0},
			{"", "Night mode", "link", "", "night", 0, 0},
			{"screen_clock_format", "Clock format", "choice", "12h|24h", "", 0, 0},
			{"", "Appearance & motion", "link", "", "appearance", 0, 0},
		}
	case "appearance":
		return "Appearance & motion", []settingRow{
			{"display_theme", "Color theme", "choice", "tater|ocean|violet|forest|sunset", "", 0, 0},
			{"screen_popup_effect", "Popup effect", "choice", "fade|slide|disabled", "", 0, 0},
			{"screen_text_style", "Conversation text", "choice", "smooth|matrix", "", 0, 0},
			{"display_wallpaper_interval_minutes", "Photo rotation", "choice", "0|60|360|720|1440", "", 0, 0},
			{"screen_auto_brightness_minimum", "Dimmest automatic level", "slider", "", "", 1, 50},
		}
	case "night":
		return "Night mode", []settingRow{
			{"screen_night_mode_enabled", "Scheduled night mode", "toggle", "", "", 0, 0},
			{"screen_night_start", "Dim at", "time", "", "", 0, 0},
			{"screen_night_end", "Restore at", "time", "", "", 0, 0},
			{"screen_night_display", "Night display", "choice", "red_clock|dim|off", "", 0, 0},
			{"screen_night_brightness", "Night brightness", "slider", "", "", 0, 100},
		}
	case "equalizer":
		rows := []settingRow{}
		for index, label := range []string{"125 Hz", "250 Hz", "500 Hz", "1 kHz", "2 kHz", "3.5 kHz", "5.5 kHz", "8 kHz"} {
			rows = append(rows, settingRow{key: fmt.Sprintf("eq_band_%d", index), label: label, kind: "slider", minimum: -12, maximum: 12})
		}
		rows = append(rows, settingRow{key: "eq_loudness", label: "Presence boost", kind: "toggle"})
		return "Equalizer", rows
	case "dnd":
		return "Do Not Disturb", []settingRow{
			{"dnd_enabled", "Do Not Disturb", "toggle", "", "", 0, 0},
			{"dnd_schedule_enabled", "Daily schedule", "toggle", "", "", 0, 0},
			{"dnd_start", "Start at", "time", "", "", 0, 0},
			{"dnd_end", "End at", "time", "", "", 0, 0},
		}
	case "info":
		graphics := "Software"
		if r.gpuAvailable {
			graphics = "GPU"
		}
		uptime := "Unavailable"
		if r.state.StartedAtUnixMS > 0 {
			uptime = time.Since(time.UnixMilli(r.state.StartedAtUnixMS)).Round(time.Minute).String()
		}
		return "Device Info", []settingRow{
			{label: "Firmware", kind: "read", options: firstNonEmpty(r.state.FirmwareVersion, version)},
			{label: "Device", kind: "read", options: r.showTarget()},
			{label: "Network", kind: "read", options: firstNonEmpty(r.state.DeviceAddress, "Unavailable")},
			{label: "Graphics", kind: "read", options: graphics},
			{label: "Uptime", kind: "read", options: uptime},
		}
	default:
		return "Sound & Wake", []settingRow{
			{"volume_percent", "Speaker volume", "slider", "", "", 0, 100},
			{"display_edge_glow", "Voice state edge glow", "toggle", "", "", 0, 0},
			{"", "Equalizer", "link", "", "equalizer", 0, 0},
			{"", "Do Not Disturb", "link", "", "dnd", 0, 0},
		}
	}
}

func (r *renderer) settingValue(key string) any {
	if value, ok := r.settingsDraft[key]; ok {
		return value
	}
	if strings.HasPrefix(key, "eq_band_") {
		index, _ := strconv.Atoi(strings.TrimPrefix(key, "eq_band_"))
		data, _ := json.Marshal(r.settingValue("eq_bands"))
		var bands []float64
		if json.Unmarshal(data, &bands) == nil && index >= 0 && index < len(bands) {
			return bands[index]
		}
		return float64(0)
	}
	if key == "volume_percent" {
		return r.state.VolumePercent
	}
	if key == "display_theme" {
		return firstNonEmpty(r.state.DisplayTheme, "tater")
	}
	if key == "display_edge_glow" {
		return r.state.DisplayEdgeGlow
	}
	if value, ok := r.state.Settings[key]; ok {
		return value
	}
	return show.ScreenSettingDefaults()[key]
}

func settingNumber(value any) int {
	var number float64
	fmt.Sscan(fmt.Sprint(value), &number)
	return int(number)
}

func settingFloat(value any) float64 {
	var result float64
	fmt.Sscan(fmt.Sprint(value), &result)
	return result
}
func settingBool(value any) bool { return value == true }

func settingLabel(value any) string {
	text := fmt.Sprint(value)
	switch text {
	case "12h":
		return "12-hour"
	case "24h":
		return "24-hour"
	case "red_clock":
		return "Dim red clock"
	case "0":
		return "Fixed"
	case "60":
		return "Hourly"
	case "360":
		return "Every 6 hours"
	case "720":
		return "Every 12 hours"
	case "1440":
		return "Daily"
	case "disabled":
		return "Off"
	case "default":
		return "Default tone"
	case "pop-up-sound":
		return "Pop"
	case "notification-ding":
		return "Ding"
	case "phone-chime":
		return "Chime"
	}
	return titleWords(strings.ReplaceAll(text, "_", " "))
}

func settingsPanelBounds(width, height int, spot bool) image.Rectangle {
	if spot {
		return image.Rect(80, 140, 400, 354)
	}
	return image.Rect(28, 96, width-28, height-55)
}
func settingsRowBounds(width, height, index int, spot bool) image.Rectangle {
	panel := settingsPanelBounds(width, height, spot)
	count := 5
	if spot {
		count = 3
	}
	rowHeight := panel.Dy() / count
	return image.Rect(panel.Min.X, panel.Min.Y+index*rowHeight, panel.Max.X, panel.Min.Y+(index+1)*rowHeight)
}
func settingsBackBounds(spot bool) image.Rectangle {
	if spot {
		return image.Rect(70, 66, 124, 120)
	}
	return image.Rect(22, 20, 82, 80)
}
func settingsPageButton(width, height int, next, spot bool) image.Rectangle {
	if spot {
		if next {
			return image.Rect(300, 382, 354, 436)
		}
		return image.Rect(126, 382, 180, 436)
	}
	if next {
		return image.Rect(width-136, height-50, width-28, height-4)
	}
	return image.Rect(28, height-50, 136, height-4)
}

func (r *renderer) settingsVisible(now time.Time) bool {
	return r.settingsOpen && !r.cameraPopupActive(now) && r.state.Connected && !now.Before(r.volumeOverlayUntil) && !r.nightClockVisible(now) && !r.conversationActive() && !r.notificationActive(now) &&
		!(r.state.Timer != nil && r.state.Timer.Active) && !r.satChatVisible() && !r.displayVideoVisible()
}

func (r *renderer) drawWorkspaceSettings(canvas *image.RGBA) {
	if r.spotScreen {
		r.drawRookSettings(canvas)
		return
	}
	width, height := canvas.Rect.Dx(), canvas.Rect.Dy()
	spot := r.spotScreen
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(workspaceBackground), image.Point{}, draw.Src)
	title, rows := r.settingRows()
	labelSize, valueSize, titleSize := 22, 20, 28
	if height >= 600 {
		labelSize, valueSize, titleSize = 30, 28, 42
	}
	if spot {
		labelSize, valueSize, titleSize = 15, 14, 22
	}
	back := settingsBackBounds(spot)
	line(canvas, back.Min.X+28, back.Min.Y+12, back.Min.X+15, back.Min.Y+25, 2, workspaceText)
	line(canvas, back.Min.X+15, back.Min.Y+25, back.Min.X+28, back.Min.Y+38, 2, workspaceText)
	if spot {
		r.centeredText(canvas, 252, 119, fitTextToWidth(title, r.faces.regular[titleSize], 240), titleSize, false, workspaceText)
	} else {
		r.text(canvas, 96, 57, fitTextToWidth(title, r.faces.regular[titleSize], width-230), titleSize, false, workspaceText)
	}
	if r.settingsPage == "equalizer" && !spot {
		r.settingsIndex = 0
		r.drawWorkspaceEqualizer(canvas)
		return
	}
	panel := settingsPanelBounds(width, height, spot)
	drawMenuRoundedRect(canvas, panel, 12, workspacePanel)
	drawMenuRoundedOutline(canvas, panel, 12, 1, workspaceBorder)
	perPage := 5
	if spot {
		perPage = 3
	}
	pages := max(1, (len(rows)+perPage-1)/perPage)
	r.settingsIndex = max(0, min(r.settingsIndex, pages-1))
	start := r.settingsIndex * perPage
	for index := 0; index < perPage && start+index < len(rows); index++ {
		row := rows[start+index]
		bounds := settingsRowBounds(width, height, index, spot)
		if index > 0 {
			line(canvas, bounds.Min.X, bounds.Min.Y, bounds.Max.X, bounds.Min.Y, 1, workspaceBorder)
		}
		left, right, baseline := bounds.Min.X+22, bounds.Max.X-22, bounds.Min.Y+bounds.Dy()/2+labelSize/3
		if spot {
			left, right = bounds.Min.X+12, bounds.Max.X-12
		}
		labelWidth := bounds.Dx() * 55 / 100
		if row.kind == "slider" {
			labelWidth = bounds.Dx() - 100
			baseline = bounds.Min.Y + labelSize + 6
		}
		r.text(canvas, left, baseline, fitTextToWidth(row.label, r.faces.regular[labelSize], labelWidth), labelSize, false, workspaceText)
		value := r.settingValue(row.key)
		switch row.kind {
		case "toggle":
			ink := workspaceControl
			if settingBool(value) {
				ink = r.menuPalette().primary
			}
			w, h := 64, 32
			if spot {
				w, h = 46, 26
			}
			box := image.Rect(right-w, baseline-labelSize/2-h/2, right, baseline-labelSize/2+h/2)
			drawMenuRoundedRect(canvas, box, h/2, ink)
			cx := box.Min.X + h/2
			if settingBool(value) {
				cx = box.Max.X - h/2
			}
			circle(canvas, cx, (box.Min.Y+box.Max.Y)/2, h/2-4, workspaceText)
		case "slider":
			number := settingNumber(value)
			text := fmt.Sprintf("%d%%", number)
			if strings.HasPrefix(row.key, "eq_band_") {
				text = fmt.Sprintf("%.1f dB", settingFloat(value))
			}
			r.text(canvas, right-textWidth(r.faces.regular[valueSize], text), baseline, text, valueSize, false, workspaceMuted)
			y := bounds.Max.Y - 15
			line(canvas, left, y, right, y, 6, workspaceBorder)
			x := left + int(float64(right-left)*(settingFloat(value)-float64(row.minimum))/float64(max(1, row.maximum-row.minimum)))
			line(canvas, left, y, x, y, 6, r.menuPalette().primary)
			circle(canvas, x, y, 8, workspaceText)
		case "choice":
			if row.key == "screen_clock_format" && !spot {
				controlWidth, controlHeight, label := 220, 40, 16
				if height >= 600 {
					controlWidth, controlHeight, label = 280, 52, 22
				}
				box := image.Rect(right-controlWidth, bounds.Min.Y+(bounds.Dy()-controlHeight)/2, right, bounds.Min.Y+(bounds.Dy()+controlHeight)/2)
				drawMenuRoundedRect(canvas, box, 8, workspaceControl)
				for index, mode := range []string{"12h", "24h"} {
					segment := image.Rect(box.Min.X+index*controlWidth/2, box.Min.Y, box.Min.X+(index+1)*controlWidth/2, box.Max.Y)
					if fmt.Sprint(value) == mode {
						drawMenuRoundedRect(canvas, segment, 8, r.menuPalette().primary)
					}
					r.centeredText(canvas, (segment.Min.X+segment.Max.X)/2, segment.Min.Y+segment.Dy()/2+label/3, settingLabel(mode), label, false, workspaceText)
				}
				continue
			}
			fallthrough
		default:
			text := settingLabel(value)
			if row.kind == "link" {
				text = "›"
				if row.target == "night" {
					text = fmt.Sprint(r.settingValue("screen_night_start")) + " – " + fmt.Sprint(r.settingValue("screen_night_end")) + " ›"
				}
			}
			if row.kind == "read" {
				text = row.options
			}
			if row.kind == "time" {
				text = "−  " + fmt.Sprint(value) + "  +"
			}
			text = fitTextToWidth(text, r.faces.regular[valueSize], bounds.Dx()*42/100)
			r.text(canvas, right-textWidth(r.faces.regular[valueSize], text), baseline, text, valueSize, false, workspaceMuted)
		}
	}
	if pages > 1 {
		for _, next := range []bool{false, true} {
			box := settingsPageButton(width, height, next, spot)
			text := "Previous"
			if next {
				text = "Next"
			}
			r.centeredText(canvas, (box.Min.X+box.Max.X)/2, box.Min.Y+29, text, 14, false, workspaceText)
		}
	} else if !spot {
		r.centeredText(canvas, width/2, height-22, "Settings for this satellite", 14, false, workspaceMuted)
	}
	if r.state.SettingsError != "" && !spot {
		r.centeredText(canvas, width/2, height-4, fitTextToWidth(r.state.SettingsError, r.faces.regular[12], width-40), 12, false, color.RGBA{255, 110, 110, 255})
	}
	if spot {
		maskOutsideSpotCircle(canvas, 236)
	}
}

func (r *renderer) handleSettingsTouch(client *socketClient, event linuxinput.TouchEvent, width, height int) {
	if r.spotScreen {
		r.handleRookSettingsTouch(client, event)
		return
	}
	point := image.Pt(event.X, event.Y)
	spot := r.spotScreen
	if event.Kind == linuxinput.Up && point.In(settingsBackBounds(spot)) {
		parent := map[string]string{"equalizer": "sound", "dnd": "sound", "appearance": "display", "night": "display"}[r.settingsPage]
		if parent == "" {
			r.settingsOpen = false
			r.menuOpen = true
		} else {
			r.settingsPage = parent
		}
		r.settingsIndex = 0
		r.settingsDragging = ""
		r.settingsDraft = nil
		return
	}
	if r.settingsPage == "equalizer" && !spot {
		r.handleWorkspaceEqualizerTouch(client, event, width, height)
		return
	}
	_, rows := r.settingRows()
	count := 5
	if spot {
		count = 3
	}
	pages := max(1, (len(rows)+count-1)/count)
	if event.Kind == linuxinput.Up && pages > 1 {
		if point.In(settingsPageButton(width, height, false, spot)) {
			r.settingsIndex = max(0, r.settingsIndex-1)
			return
		}
		if point.In(settingsPageButton(width, height, true, spot)) {
			r.settingsIndex = min(pages-1, r.settingsIndex+1)
			return
		}
	}
	for index := 0; index < count && r.settingsIndex*count+index < len(rows); index++ {
		row := rows[r.settingsIndex*count+index]
		bounds := settingsRowBounds(width, height, index, spot)
		if row.kind == "slider" && ((point.In(bounds) && point.Y >= bounds.Max.Y-35) || r.settingsDragging == row.key) {
			if r.settingsDragging == "" && event.Kind == linuxinput.Move {
				return
			}
			left, right := bounds.Min.X+22, bounds.Max.X-22
			if spot {
				left, right = bounds.Min.X+12, bounds.Max.X-12
			}
			number := row.minimum + max(0, min(right-left, event.X-left))*(row.maximum-row.minimum)/max(1, right-left)
			var selected any = number
			if strings.HasPrefix(row.key, "eq_band_") {
				selected = math.Round((float64(row.minimum)+float64(max(0, min(right-left, event.X-left)))*float64(row.maximum-row.minimum)/float64(max(1, right-left)))*2) / 2
			}
			if r.settingsDraft == nil {
				r.settingsDraft = map[string]any{}
			}
			r.settingsDraft[row.key] = selected
			r.settingsDragging = row.key
			if event.Kind == linuxinput.Up {
				r.commitSettings(client, map[string]any{row.key: selected})
				r.settingsDragging = ""
			}
			return
		}
		if !point.In(bounds) || event.Kind != linuxinput.Up {
			continue
		}
		value := r.settingValue(row.key)
		switch row.kind {
		case "link":
			r.settingsPage = row.target
			r.settingsIndex = 0
		case "toggle":
			r.commitSettings(client, map[string]any{row.key: !settingBool(value)})
		case "choice":
			if row.key == "screen_clock_format" && !spot {
				controlWidth := 220
				if height >= 600 {
					controlWidth = 280
				}
				right := bounds.Max.X - 22
				if event.X < right-controlWidth {
					return
				}
				mode := "12h"
				if event.X >= right-controlWidth/2 {
					mode = "24h"
				}
				r.commitSettings(client, map[string]any{row.key: mode})
				return
			}
			options := strings.Split(row.options, "|")
			selected := 0
			for n, option := range options {
				if option == fmt.Sprint(value) {
					selected = (n + 1) % len(options)
					break
				}
			}
			var next any = options[selected]
			if row.key == "display_wallpaper_interval_minutes" {
				next = settingNumber(next)
			}
			r.commitSettings(client, map[string]any{row.key: next})
		case "time":
			parsed, err := time.Parse("15:04", fmt.Sprint(value))
			if err != nil {
				return
			}
			delta := 15 * time.Minute
			if event.X < bounds.Max.X-90 {
				delta = -delta
			}
			r.commitSettings(client, map[string]any{row.key: parsed.Add(delta).Format("15:04")})
		}
		return
	}
}

func (r *renderer) commitSettings(client *socketClient, values map[string]any) {
	for key, value := range values {
		if strings.HasPrefix(key, "eq_band_") {
			index, _ := strconv.Atoi(strings.TrimPrefix(key, "eq_band_"))
			data, _ := json.Marshal(r.settingValue("eq_bands"))
			bands := make([]float64, 8)
			_ = json.Unmarshal(data, &bands)
			if len(bands) != 8 {
				bands = make([]float64, 8)
			}
			if index < 0 || index >= 8 {
				return
			}
			bands[index] = settingFloat(value)
			delete(values, key)
			delete(r.settingsDraft, key)
			values["eq_bands"] = bands
		}
	}
	validated, err := show.ValidateScreenSettings(values)
	if err != nil {
		return
	}
	values = validated
	if r.state.Settings == nil {
		r.state.Settings = show.ScreenSettingDefaults()
	}
	for key, value := range values {
		r.state.Settings[key] = value
		delete(r.settingsDraft, key)
	}
	if value, ok := values["volume_percent"]; ok {
		r.state.VolumePercent = settingNumber(value)
	}
	if value, ok := values["display_theme"]; ok {
		r.state.DisplayTheme = fmt.Sprint(value)
	}
	if value, ok := values["display_edge_glow"]; ok {
		r.state.DisplayEdgeGlow = settingBool(value)
	}
	if client != nil {
		client.sendCommand(show.Command{Protocol: show.ProtocolVersion, Type: "command", Action: "screen.settings.set", Settings: values})
	}
	r.invalidateFrameCanvases()
}
