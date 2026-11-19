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

// Follow the Shows' home order while fitting Rook's native 480x480 round panel:
// clock/weather, date/condition, assistant/room, then sensor readings.
const (
	rookHomeClockSize   = 74
	rookHomeClockY      = 250
	rookHomeWeatherSize = 38
	rookHomeDateY       = 289
	rookHomeHeadingY    = 327
	rookHomeSensorY     = 398
)

type rookHomeMainRow struct {
	temperature            string
	icon, separator        image.Rectangle
	clockAt, temperatureAt image.Point
}

func (r *renderer) rookHomeMainLayout(digits, suffix string) rookHomeMainRow {
	clockWidth := r.clockWidth(digits, suffix, rookHomeClockSize)
	row := rookHomeMainRow{}
	width := clockWidth
	visible := workspaceWeatherVisible(r.state.Weather)
	if visible {
		row.temperature = fitTextToWidth(strings.TrimSpace(r.state.Weather.TemperatureText), r.faces.regular[rookHomeWeatherSize], 95)
		width += 15 + 1 + 15 + 36 + 8 + textWidth(r.faces.regular[rookHomeWeatherSize], row.temperature)
	}
	row.clockAt = image.Pt((480-width)/2, rookHomeClockY)
	if visible {
		x := row.clockAt.X + clockWidth + 15
		row.separator = image.Rect(x, rookHomeClockY-50, x+1, rookHomeClockY+2)
		row.icon = image.Rect(x+16, rookHomeClockY-36, x+52, rookHomeClockY)
		row.temperatureAt = image.Pt(row.icon.Max.X+8, rookHomeClockY-4)
	}
	return row
}

func (r *renderer) rookHomeDate(local time.Time) string {
	detail := local.Format("Monday, January 2")
	if workspaceWeatherVisible(r.state.Weather) {
		if condition := strings.TrimSpace(r.state.Weather.Condition); condition != "" {
			detail += " · " + condition
		}
	}
	return fitTextToWidth(detail, r.faces.regular[16], 368)
}

func (r *renderer) rookHomeHeading() workspaceHomeHeading {
	h := r.workspaceHomeHeadingLayout(480, 72, 17)
	end := h.roomAt.X + textWidth(r.faces.regular[17], h.room)
	if h.temperature != "" {
		end = h.temperatureAt.X + textWidth(r.faces.regular[17], h.temperature)
	}
	offset := image.Pt((480-(end-h.assistantAt.X))/2-h.assistantAt.X, rookHomeHeadingY-h.assistantAt.Y)
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
	local := r.localTime(now)
	digits, suffix := r.clockParts(local)
	row := r.rookHomeMainLayout(digits, suffix)
	r.drawClock(canvas, row.clockAt.X, row.clockAt.Y, digits, suffix, rookHomeClockSize, workspaceText)
	if !row.icon.Empty() {
		line(canvas, row.separator.Min.X, row.separator.Min.Y, row.separator.Min.X, row.separator.Max.Y, 1, workspaceMuted)
		drawWorkspaceWeatherGlyph(canvas, row.icon, weatherDisplayKind(r.state.Weather, local), r.menuPalette().primary)
		r.text(canvas, row.temperatureAt.X, row.temperatureAt.Y, row.temperature, rookHomeWeatherSize, false, workspaceText)
	}
	r.centeredText(canvas, 240, rookHomeDateY, r.rookHomeDate(local), 16, false, workspaceMuted)
	h := r.rookHomeHeading()
	r.text(canvas, h.assistantAt.X, h.assistantAt.Y, h.assistant, 17, false, workspaceText)
	circle(canvas, h.dot.X, h.dot.Y, h.radius, h.ink)
	r.text(canvas, h.roomAt.X, h.roomAt.Y, h.room, 17, false, workspaceText)
	r.text(canvas, h.temperatureAt.X, h.temperatureAt.Y, h.temperature, 17, false, workspaceText)
	r.centeredText(canvas, 240, 363, fitTextToWidth(rookHomeFeelsLike(r.state.Weather), r.faces.regular[17], 330), 17, false, workspaceMuted)
	lines, size := r.rookHomeSensorLines()
	for index, value := range lines {
		r.centeredText(canvas, 240, rookHomeSensorY+index*24, value, size, false, workspaceMuted)
	}
	maskOutsideSpotCircle(canvas, rookPanelRadius)
}
