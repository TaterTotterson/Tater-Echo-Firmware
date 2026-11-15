//go:build linux

package main

import (
	"image"
	"image/color"
	"strings"
)

type workspaceHomeHeading struct {
	assistant, room, temperature            string
	assistantAt, roomAt, temperatureAt, dot image.Point
	ink                                     color.RGBA
	radius                                  int
}

func workspaceHomeStatusColor(stateConnected, muted bool, phase, theme string) color.RGBA {
	if !stateConnected || phase == "offline" {
		return color.RGBA{255, 132, 102, 255}
	}
	if muted {
		return color.RGBA{255, 112, 126, 255}
	}
	if voiceEdgeActive(phase) || phase == "error" {
		return accentForTheme(phase, theme)
	}
	if phase == "connecting" {
		return color.RGBA{232, 174, 86, 255}
	}
	return color.RGBA{110, 182, 134, 255}
}

func (r *renderer) workspaceHomeHeadingLayout(width, margin, size int) workspaceHomeHeading {
	face := r.faces.regular[size]
	gap, baseline := max(10, size/2), margin+size
	result := workspaceHomeHeading{radius: max(4, size/5), assistantAt: image.Pt(margin, baseline),
		ink: workspaceHomeStatusColor(r.state.Connected, r.state.Muted, strings.ToLower(strings.TrimSpace(r.state.Phase)), r.state.DisplayTheme)}
	if r.state.Weather != nil {
		result.temperature = strings.TrimSpace(r.state.Weather.IndoorTemperatureText)
	}
	available := width - margin*2
	result.temperature = fitTextToWidth(result.temperature, face, max(1, available/4))
	reserved := 0
	if result.temperature != "" {
		reserved = textWidth(face, result.temperature) + gap
	}
	result.assistant = fitTextToWidth(assistantFirstName(r.state.AssistantName), face, max(1, (available-reserved)*2/5))
	result.dot = image.Pt(margin+textWidth(face, result.assistant)+gap+result.radius, baseline-size/3)
	result.roomAt = image.Pt(result.dot.X+result.radius+gap, baseline)
	result.room = fitTextToWidth(firstNonEmpty(r.state.Room, r.showTarget()), face, max(1, width-margin-result.roomAt.X-reserved))
	result.temperatureAt = image.Pt(result.roomAt.X+textWidth(face, result.room)+gap, baseline)
	return result
}

func (r *renderer) drawWorkspaceHomeHeading(canvas *image.RGBA, margin, size int) {
	heading := r.workspaceHomeHeadingLayout(canvas.Rect.Dx(), margin, size)
	r.text(canvas, heading.assistantAt.X, heading.assistantAt.Y, heading.assistant, size, false, workspaceText)
	circle(canvas, heading.dot.X, heading.dot.Y, heading.radius, heading.ink)
	r.text(canvas, heading.roomAt.X, heading.roomAt.Y, heading.room, size, false, workspaceText)
	r.text(canvas, heading.temperatureAt.X, heading.temperatureAt.Y, heading.temperature, size, false, workspaceText)
}
