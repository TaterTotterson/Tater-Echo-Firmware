//go:build linux

package main

import (
	"image"
	"image/color"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/linuxinput"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

const thermostatIdleTimeout = 45 * time.Second

func thermostatDial(width int) (int, int, int) {
	pane := rightPane(width, 480)
	return (pane.Min.X + pane.Max.X) / 2, 228, 126
}

func thermostatModeBounds(width int, mode string) image.Rectangle {
	cx, _, _ := thermostatDial(width)
	switch mode {
	case "heat":
		return image.Rect(cx-196, 406, cx-103, 452)
	case "cool":
		return image.Rect(cx-98, 406, cx-5, 452)
	case "auto":
		return image.Rect(cx, 406, cx+93, 452)
	default:
		return image.Rect(cx+98, 406, cx+191, 452)
	}
}

func thermostatStepBounds(width int, plus bool) image.Rectangle {
	cx, _, _ := thermostatDial(width)
	if plus {
		return image.Rect(cx+156, 206, cx+206, 256)
	}
	return image.Rect(cx-206, 206, cx-156, 256)
}

func thermostatLimits(unit string) (float64, float64) {
	if strings.EqualFold(unit, "C") {
		return 10, 32
	}
	return 50, 90
}

func thermostatTargetFromPoint(x, y, cx, cy int, unit string) float64 {
	angle := math.Atan2(float64(y-cy), float64(x-cx))*180/math.Pi - 135
	for angle < 0 {
		angle += 360
	}
	// The missing bottom quarter of the dial is never a target. At its edge,
	// choose the nearer end of the 270-degree active arc.
	if angle > 270 {
		if angle < 315 {
			angle = 270
		} else {
			angle = 0
		}
	}
	low, high := thermostatLimits(unit)
	return math.Round(low + (high-low)*angle/270)
}

func thermostatCanChange(weather *show.Weather) bool {
	return weather != nil && weather.EnvironmentInstalled && weather.Thermostat != nil &&
		weather.Thermostat.Available && weather.Thermostat.ID != ""
}

func (r *renderer) handleThermostatTouch(client *socketClient, event linuxinput.TouchEvent) bool {
	if !r.state.Connected || r.state.Phase == "setup" || r.notificationActive(time.Now()) ||
		(r.state.Timer != nil && r.state.Timer.Active) || r.state.Phase == "tool_call" {
		return false
	}
	pane := rightPane(960, 480)
	point := image.Pt(event.X, event.Y)
	if event.Kind == linuxinput.Down {
		if !point.In(pane) {
			return false
		}
		r.thermostatTracking = true
		r.thermostatDown = point
		r.thermostatDrag = false
		if r.thermostatOpen {
			r.thermostatTouched = time.Now()
			cx, cy, radius := thermostatDial(960)
			distance := math.Hypot(float64(event.X-cx), float64(event.Y-cy))
			if thermostatCanChange(r.state.Weather) && r.state.Weather.Thermostat.TargetWritable && distance >= float64(radius-34) && distance <= float64(radius+35) && event.Y < 354 {
				value := thermostatTargetFromPoint(event.X, event.Y, cx, cy, r.state.Weather.Thermostat.Unit)
				r.thermostatPreview = &value
				r.thermostatDrag = true
			}
		}
		return true
	}
	if !r.thermostatTracking {
		return false
	}
	if event.Kind == linuxinput.Move {
		if !r.thermostatOpen && r.thermostatDown.Y-event.Y >= 48 {
			r.thermostatOpen = true
			r.thermostatTouched = time.Now()
			return true
		}
		if r.thermostatOpen && r.thermostatDrag && thermostatCanChange(r.state.Weather) && r.state.Weather.Thermostat.TargetWritable {
			cx, cy, _ := thermostatDial(960)
			value := thermostatTargetFromPoint(event.X, event.Y, cx, cy, r.state.Weather.Thermostat.Unit)
			r.thermostatPreview = &value
			r.thermostatTouched = time.Now()
		}
		return true
	}
	if event.Kind != linuxinput.Up {
		return true
	}
	r.thermostatTracking = false
	if !r.thermostatOpen {
		if r.thermostatDown.Y-event.Y >= 48 {
			r.thermostatOpen = true
			r.thermostatTouched = time.Now()
		}
		return true
	}
	r.thermostatTouched = time.Now()
	if !r.thermostatDrag && event.Y-r.thermostatDown.Y >= 55 {
		r.thermostatOpen, r.thermostatPreview = false, nil
		return true
	}
	weather := r.state.Weather
	if !thermostatCanChange(weather) {
		return true
	}
	thermostat := weather.Thermostat
	if r.thermostatDrag {
		r.thermostatDrag = false
		if r.thermostatPreview != nil && *r.thermostatPreview != thermostat.Target {
			client.sendCommand(show.Command{Action: "thermostat.set", ThermostatID: thermostat.ID, Target: r.thermostatPreview, Unit: thermostat.Unit})
		}
		return true
	}
	for _, mode := range []string{"heat", "cool", "auto", "off"} {
		if point.In(thermostatModeBounds(960, mode)) {
			if mode != thermostat.Mode && thermostat.ModeWritable {
				client.sendCommand(show.Command{Action: "thermostat.set", ThermostatID: thermostat.ID, Mode: mode})
			}
			return true
		}
	}
	step := 0.0
	if point.In(thermostatStepBounds(960, false)) {
		step = -1
	} else if point.In(thermostatStepBounds(960, true)) {
		step = 1
	}
	if step != 0 && thermostat.TargetWritable {
		low, high := thermostatLimits(thermostat.Unit)
		value := math.Min(high, math.Max(low, math.Round(thermostat.Target)+step))
		r.thermostatPreview = &value
		if value != thermostat.Target {
			client.sendCommand(show.Command{Action: "thermostat.set", ThermostatID: thermostat.ID, Target: &value, Unit: thermostat.Unit})
		}
	}
	return true
}

func (r *renderer) drawWeatherUnavailable(canvas *image.RGBA, width int, accent color.RGBA) {
	pane := rightPane(width, canvas.Rect.Dy())
	r.text(canvas, pane.Min.X, 52, "WEATHER", 16, true, accent)
	roundedRect(canvas, image.Rect(pane.Min.X, 92, pane.Max.X, 352), 28, color.RGBA{25, 31, 43, 235})
	cx := (pane.Min.X + pane.Max.X) / 2
	circle(canvas, cx, 168, 42, color.RGBA{accent.R, accent.G, accent.B, 35})
	r.centeredText(canvas, cx, 179, "°", 48, true, accent)
	message, detail := "Loading weather", "Waiting for Environment Core"
	if r.state.Weather != nil && !r.state.Weather.EnvironmentInstalled {
		message, detail = "Please install", "Environment Core in Tater"
	} else if r.state.Weather != nil {
		message, detail = "Weather unavailable", "Waiting for Environment Core readings"
	}
	r.centeredText(canvas, cx, 254, message, 26, true, color.White)
	r.centeredText(canvas, cx, 287, detail, 18, false, color.RGBA{174, 189, 209, 255})
	r.centeredText(canvas, cx, 438, "SWIPE UP FOR THERMOSTAT", 14, true, accent)
}

func (r *renderer) drawThermostat(canvas *image.RGBA, width, height int, accent color.RGBA) {
	pane := rightPane(width, height)
	cx, cy, radius := thermostatDial(width)
	weather := r.state.Weather
	r.text(canvas, pane.Min.X, 52, "ROOM CLIMATE", 16, true, accent)
	if weather == nil || !weather.EnvironmentInstalled {
		r.centeredText(canvas, cx, 206, "Please install", 26, true, color.White)
		r.centeredText(canvas, cx, 241, "Environment Core", 22, false, color.RGBA{174, 189, 209, 255})
		r.centeredText(canvas, cx, 438, "SWIPE DOWN FOR WEATHER", 14, true, accent)
		return
	}
	thermostat := weather.Thermostat
	if thermostat == nil || !thermostat.Available {
		message := "No thermostat connected"
		detail := "Add a thermostat in Tater"
		if thermostat != nil && thermostat.Message != "" {
			message = thermostat.Message
			if message == "Multiple thermostats found" {
				detail = "Match its name to this room"
			}
		}
		r.centeredText(canvas, cx, 208, fitTextToWidth(message, r.faces.bold[24], pane.Dx()), 24, true, color.White)
		r.centeredText(canvas, cx, 244, detail, 18, false, color.RGBA{174, 189, 209, 255})
		r.centeredText(canvas, cx, 438, "SWIPE DOWN FOR WEATHER", 14, true, accent)
		return
	}
	r.text(canvas, pane.Min.X, 82, fitTextToWidth(thermostat.Name, r.faces.regular[18], pane.Dx()), 18, false, color.RGBA{173, 187, 207, 255})
	modeInk := color.RGBA{162, 174, 192, 255}
	if thermostat.Mode == "cool" {
		modeInk = color.RGBA{83, 178, 255, 255}
	} else if thermostat.Mode == "heat" {
		modeInk = color.RGBA{255, 123, 82, 255}
	} else if thermostat.Mode == "auto" {
		modeInk = color.RGBA{255, 174, 93, 255}
	}
	arc(canvas, cx, cy, radius, 135, 270, 16, color.RGBA{99, 112, 132, 95})
	low, high := thermostatLimits(thermostat.Unit)
	target := thermostat.Target
	if r.thermostatPreview != nil {
		target = *r.thermostatPreview
	}
	progress := math.Max(0, math.Min(1, (target-low)/(high-low)))
	arc(canvas, cx, cy, radius, 135, progress*270, 16, modeInk)
	angle := (135 + progress*270) * math.Pi / 180
	knobX := cx + int(math.Cos(angle)*float64(radius))
	knobY := cy + int(math.Sin(angle)*float64(radius))
	circle(canvas, knobX, knobY, 17, color.RGBA{234, 240, 249, 255})
	circle(canvas, knobX, knobY, 12, modeInk)
	r.centeredText(canvas, cx, cy+1, temperatureLabel(target), 82, true, color.White)
	r.centeredText(canvas, cx, cy+36, "CURRENT  "+temperatureLabel(thermostat.Current), 18, false, color.RGBA{185, 199, 217, 255})
	r.centeredText(canvas, cx, 375, strings.ToUpper(thermostat.Mode), 18, true, modeInk)
	if thermostat.Message != "" {
		r.centeredText(canvas, cx, 398, fitTextToWidth(thermostat.Message, r.faces.regular[14], pane.Dx()), 14, false, color.RGBA{255, 176, 133, 255})
	}
	for _, plus := range []bool{false, true} {
		bounds := thermostatStepBounds(width, plus)
		buttonX, buttonY := (bounds.Min.X+bounds.Max.X)/2, (bounds.Min.Y+bounds.Max.Y)/2
		circle(canvas, buttonX, buttonY, 23, color.RGBA{30, 39, 52, 255})
		circleOutline(canvas, buttonX, buttonY, 23, 2, modeInk)
		line(canvas, buttonX-9, buttonY, buttonX+9, buttonY, 3, color.RGBA{255, 255, 255, 255})
		if plus {
			line(canvas, buttonX, buttonY-9, buttonX, buttonY+9, 3, color.RGBA{255, 255, 255, 255})
		}
	}
	for _, mode := range []string{"heat", "cool", "auto", "off"} {
		bounds := thermostatModeBounds(width, mode)
		active := mode == thermostat.Mode
		fill := color.RGBA{30, 38, 50, 255}
		if active {
			fill = color.RGBA{modeInk.R, modeInk.G, modeInk.B, 80}
		}
		roundedRect(canvas, bounds, 20, fill)
		r.centeredText(canvas, (bounds.Min.X+bounds.Max.X)/2, bounds.Min.Y+30, strings.ToUpper(mode), 15, true, color.White)
	}
	if !thermostat.Writable {
		r.centeredText(canvas, cx, 397, "VIEW ONLY", 14, true, color.RGBA{255, 191, 122, 255})
	}
}

func temperatureLabel(value float64) string {
	if value == 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return "--°"
	}
	return strconv.FormatFloat(math.Round(value), 'f', 0, 64) + "°"
}
