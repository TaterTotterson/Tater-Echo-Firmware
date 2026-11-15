//go:build linux

package main

import (
	"image"
	"strings"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

type classicHomeGeometry struct {
	margin, heading, clock, body, weather int
	mainY, dateY, roomY                   int
}

// Retain the current GPU workspace's typography/art sizes while restoring the
// approved original lower-right arrangement. Rook keeps its separate round UI.
func classicWorkspaceHomeGeometry(width, height int) classicHomeGeometry {
	if height >= 600 {
		return classicHomeGeometry{54, 30, 96, 34, 64, height - 193, height - 139, height - 85}
	}
	return classicHomeGeometry{36, 22, 96, 24, 48, height - 132, height - 88, height - 51}
}

func (r *renderer) classicHomeHeading(width int, g classicHomeGeometry) workspaceHomeHeading {
	h := r.workspaceHomeHeadingLayout(width, g.margin, g.heading)
	end := h.roomAt.X + textWidth(r.faces.regular[g.heading], h.room)
	if h.temperature != "" {
		end = h.temperatureAt.X + textWidth(r.faces.regular[g.heading], h.temperature)
	}
	offset := image.Pt(width-g.margin-end, g.roomY-h.assistantAt.Y)
	h.assistantAt = h.assistantAt.Add(offset)
	h.roomAt = h.roomAt.Add(offset)
	h.temperatureAt = h.temperatureAt.Add(offset)
	h.dot = h.dot.Add(offset)
	return h
}

func (r *renderer) classicHomeDate(now time.Time, width int, g classicHomeGeometry) string {
	detail := r.localTime(now).Format("Monday, January 2")
	if weather := r.state.Weather; workspaceWeatherVisible(weather) {
		if condition := strings.TrimSpace(weather.Condition); condition != "" {
			detail += " · " + condition
		}
	}
	return fitTextToWidth(detail, r.faces.regular[g.body], width-2*g.margin)
}

func (r *renderer) classicHomeFooter(width, height int, g classicHomeGeometry) ([]string, int) {
	var weather *show.Weather
	if r.state.Weather != nil {
		copy := *r.state.Weather
		// Conditions already follow the date. Never mutate the host's snapshot.
		copy.Condition = ""
		weather = &copy
	}
	return r.workspaceHomeFooterLines(weather, width-2*g.margin, height)
}

func (r *renderer) drawClassicWorkspaceHome(canvas *image.RGBA, now time.Time) {
	width, height := canvas.Rect.Dx(), canvas.Rect.Dy()
	g := classicWorkspaceHomeGeometry(width, height)
	right := width - g.margin
	digits, suffix := r.clockParts(r.localTime(now))
	timeWidth := r.clockWidth(digits, suffix, g.clock)
	mainWidth, temperature := timeWidth, ""
	if weather := r.state.Weather; workspaceWeatherVisible(weather) {
		available := max(1, width-2*g.margin-timeWidth-20-1-20-58-16)
		temperature = fitTextToWidth(strings.TrimSpace(weather.TemperatureText), r.faces.regular[g.weather], available)
		mainWidth += 20 + 1 + 20 + 58 + 16 + textWidth(r.faces.regular[g.weather], temperature)
	}
	left := max(g.margin, right-mainWidth)
	r.drawClock(canvas, left, g.mainY, digits, suffix, g.clock, workspaceText)
	if temperature != "" {
		separator := left + timeWidth + 20
		line(canvas, separator, g.mainY-58, separator, g.mainY+4, 1, workspaceMuted)
		bounds := image.Rect(separator+21, g.mainY-58, separator+79, g.mainY)
		drawWorkspaceWeatherGlyph(canvas, bounds, weatherDisplayKind(r.state.Weather, r.localTime(now)), r.menuPalette().primary)
		r.text(canvas, bounds.Max.X+16, g.mainY-5, temperature, g.weather, false, workspaceText)
	}
	detail := r.classicHomeDate(now, width, g)
	r.text(canvas, right-textWidth(r.faces.regular[g.body], detail), g.dateY, detail, g.body, false, workspaceText)
	h := r.classicHomeHeading(width, g)
	r.text(canvas, h.assistantAt.X, h.assistantAt.Y, h.assistant, g.heading, false, workspaceText)
	circle(canvas, h.dot.X, h.dot.Y, h.radius, h.ink)
	r.text(canvas, h.roomAt.X, h.roomAt.Y, h.room, g.heading, false, workspaceText)
	r.text(canvas, h.temperatureAt.X, h.temperatureAt.Y, h.temperature, g.heading, false, workspaceText)
	lines, size := r.classicHomeFooter(width, height, g)
	baseline := height - 22 - (len(lines)-1)*(size+8)
	for _, value := range lines {
		r.text(canvas, right-textWidth(r.faces.regular[size], value), baseline, value, size, false, workspaceMuted)
		baseline += size + 8
	}
}
