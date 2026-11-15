//go:build linux

package main

import (
	"image"
	"image/color"
	"image/draw"
	"strings"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

// Quiet clock keeps the home screen's hierarchy independent of weather being
// enabled. All geometry is native 480x480 and stays within Rook's round panel.
const (
	rookHomeClockSize = 82
	rookHomeClockY    = 227
	rookHomeDateY     = 266
)

type rookHomeWeatherRow struct {
	temperature, condition     string
	icon, separator            image.Rectangle
	temperatureAt, conditionAt image.Point
}

func (r *renderer) rookHomeWeatherLayout() rookHomeWeatherRow {
	weather := r.state.Weather
	if !workspaceWeatherVisible(weather) {
		return rookHomeWeatherRow{}
	}
	const icon, gap, maxWidth = 36, 8, 344
	row := rookHomeWeatherRow{
		temperature: fitTextToWidth(strings.TrimSpace(weather.TemperatureText), r.faces.regular[32], 112),
	}
	width := icon + gap + textWidth(r.faces.regular[32], row.temperature)
	// Reserve the separator and its breathing room before fitting long labels.
	row.condition = fitTextToWidth(strings.TrimSpace(weather.Condition), r.faces.regular[18], maxWidth-width-25)
	if row.condition != "" {
		width += 25 + textWidth(r.faces.regular[18], row.condition)
	}
	left := (480 - width) / 2
	row.icon = image.Rect(left, 298, left+icon, 298+icon)
	row.temperatureAt = image.Pt(row.icon.Max.X+gap, 328)
	if row.condition != "" {
		x := row.temperatureAt.X + textWidth(r.faces.regular[32], row.temperature) + 12
		row.separator = image.Rect(x, 304, x+1, 328)
		row.conditionAt = image.Pt(x+13, 323)
	}
	return row
}

func (r *renderer) rookHomeHeading() workspaceHomeHeading {
	h := r.workspaceHomeHeadingLayout(480, 72, 17)
	end := h.roomAt.X + textWidth(r.faces.regular[17], h.room)
	if h.temperature != "" {
		end = h.temperatureAt.X + textWidth(r.faces.regular[17], h.temperature)
	}
	offset := image.Pt((480-(end-h.assistantAt.X))/2-h.assistantAt.X, 112-h.assistantAt.Y)
	h.assistantAt, h.roomAt = h.assistantAt.Add(offset), h.roomAt.Add(offset)
	h.temperatureAt, h.dot = h.temperatureAt.Add(offset), h.dot.Add(offset)
	h.radius = 3
	return h
}

func rookHomeFeelsLike(weather *show.Weather) string {
	if weather == nil {
		return ""
	}
	readings := workspaceHomeReadings(&show.Weather{FeelsLikeText: weather.FeelsLikeText})
	if len(readings) == 0 {
		return ""
	}
	return readings[0]
}

// Keep enabled readings grouped by sensor, without repeating the condition,
// feels-like or room temperature. Two rows taper with the physical circle.
func (r *renderer) rookHomeSensorLines() ([]string, int) {
	if r.state.Weather == nil {
		return nil, 15
	}
	weather := *r.state.Weather
	weather.TemperatureText, weather.Condition, weather.FeelsLikeText = "", "", ""
	items := workspaceHomeReadings(&weather)
	for size := 15; size >= 12; size-- {
		var lines []string
		current := ""
		for _, item := range items {
			width := rookHomeSensorWidth(len(lines))
			item = fitTextToWidth(item, r.faces.regular[size], width)
			candidate := item
			if current != "" {
				candidate = current + " · " + item
			}
			if current != "" && textWidth(r.faces.regular[size], candidate) > width {
				lines = append(lines, current)
				current = fitTextToWidth(item, r.faces.regular[size], rookHomeSensorWidth(len(lines)))
			} else {
				current = candidate
			}
		}
		if current != "" {
			lines = append(lines, current)
		}
		if len(lines) <= 2 {
			return lines, size
		}
		if size == 12 {
			// Unusually long host labels must not spill beyond the panel. Fit
			// the remaining text with an ellipsis rather than silently dropping it.
			return []string{lines[0], fitTextToWidth(strings.Join(lines[1:], " · "), r.faces.regular[size], rookHomeSensorWidth(1))}, size
		}
	}
	return nil, 15
}

func rookHomeSensorWidth(line int) int {
	if line == 0 {
		return 330
	}
	return 270
}

func (r *renderer) drawRookHome(canvas *image.RGBA, now time.Time) {
	if wallpaper := r.activeWallpaper(now); wallpaper != nil {
		drawPreparedCover(canvas, canvas.Rect, wallpaper)
	} else {
		draw.Draw(canvas, canvas.Rect, image.NewUniform(workspaceBackground), image.Point{}, draw.Src)
	}
	draw.Draw(canvas, canvas.Rect, image.NewUniform(color.NRGBA{R: 17, G: 18, B: 19, A: 70}), image.Point{}, draw.Over)
	drawPopupShade(canvas, 70, 180, .58)
	h := r.rookHomeHeading()
	r.text(canvas, h.assistantAt.X, h.assistantAt.Y, h.assistant, 17, false, workspaceText)
	circle(canvas, h.dot.X, h.dot.Y, h.radius, h.ink)
	r.text(canvas, h.roomAt.X, h.roomAt.Y, h.room, 17, false, workspaceText)
	r.text(canvas, h.temperatureAt.X, h.temperatureAt.Y, h.temperature, 17, false, workspaceText)

	local := r.localTime(now)
	digits, suffix := r.clockParts(local)
	// The round mockup uses a quieter AM/PM marker than the larger Show UI.
	width := textWidth(r.faces.regular[rookHomeClockSize], digits)
	if suffix != "" {
		width += 9 + textWidth(r.faces.regular[24], suffix)
	}
	x := (480 - width) / 2
	r.text(canvas, x, rookHomeClockY, digits, rookHomeClockSize, false, workspaceText)
	if suffix != "" {
		r.text(canvas, x+textWidth(r.faces.regular[rookHomeClockSize], digits)+9, rookHomeClockY, suffix, 24, false, workspaceText)
	}
	r.centeredText(canvas, 240, rookHomeDateY, local.Format("Monday, January 2"), 18, false, workspaceMuted)
	row := r.rookHomeWeatherLayout()
	if !row.icon.Empty() {
		drawWorkspaceWeatherGlyph(canvas, row.icon, weatherDisplayKind(r.state.Weather, local), r.menuPalette().primary)
		r.text(canvas, row.temperatureAt.X, row.temperatureAt.Y, row.temperature, 32, false, workspaceText)
		if !row.separator.Empty() {
			draw.Draw(canvas, row.separator, image.NewUniform(color.NRGBA{R: 241, G: 238, B: 232, A: 92}), image.Point{}, draw.Over)
			r.text(canvas, row.conditionAt.X, row.conditionAt.Y, row.condition, 18, false, workspaceText)
		}
	}
	r.centeredText(canvas, 240, 366, fitTextToWidth(rookHomeFeelsLike(r.state.Weather), r.faces.regular[17], 330), 17, false, workspaceMuted)
	lines, size := r.rookHomeSensorLines()
	for index, value := range lines {
		r.centeredText(canvas, 240, 403+index*24, value, size, false, workspaceMuted)
	}
	maskOutsideSpotCircle(canvas, rookPanelRadius)
}
