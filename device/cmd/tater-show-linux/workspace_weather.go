//go:build linux

package main

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"strings"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/vector"
)

// The approved workspace uses quiet outline weather symbols, not the older
// shaded PNG set. Draw on a transparent oversampled layer so the same line art
// remains crisp on every panel and never paints a box over the wallpaper.
func drawWorkspaceWeatherGlyph(canvas *image.RGBA, bounds image.Rectangle, kind string, accent color.RGBA) {
	if bounds.Empty() {
		return
	}
	art := image.NewRGBA(image.Rect(0, 0, 200, 200))
	stroke := func(x0, y0, x1, y1 float64, ink color.RGBA) {
		line(art, int(x0*2), int(y0*2), int(x1*2), int(y1*2), 7, ink)
	}
	curve := func(x0, y0 float64, points [6]float64, ink color.RGBA) {
		px, py := x0, y0
		for step := 1; step <= 32; step++ {
			t := float64(step) / 32
			u := 1 - t
			x := u*u*u*x0 + 3*u*u*t*points[0] + 3*u*t*t*points[2] + t*t*t*points[4]
			y := u*u*u*y0 + 3*u*u*t*points[1] + 3*u*t*t*points[3] + t*t*t*points[5]
			stroke(px, py, x, y, ink)
			px, py = x, y
		}
	}
	sun := func(cx, cy, radius float64, partly bool) {
		circleOutline(art, int(cx*2), int(cy*2), int(radius*2), 7, accent)
		for index := 0; index < 8; index++ {
			angle := float64(index) * math.Pi / 4
			if partly && math.Sin(angle) > .4 {
				continue
			}
			stroke(cx+math.Cos(angle)*(radius+7), cy+math.Sin(angle)*(radius+7), cx+math.Cos(angle)*(radius+15), cy+math.Sin(angle)*(radius+15), accent)
		}
	}
	moon := func(cx, cy, radius float64) {
		curve(cx+radius*.65, cy-radius*.85, [6]float64{cx - radius*1.1, cy - radius, cx - radius*1.2, cy + radius, cx + radius*.5, cy + radius}, workspaceText)
		curve(cx+radius*.5, cy+radius, [6]float64{cx + radius, cy + radius*.9, cx + radius*1.1, cy + radius*.4, cx + radius, cy + radius*.2}, workspaceText)
		curve(cx+radius, cy+radius*.2, [6]float64{cx, cy + radius*.5, cx - radius*.1, cy - radius*.4, cx + radius*.65, cy - radius*.85}, workspaceText)
	}
	token := workspaceWeatherKind(kind)
	switch token {
	case "sun":
		sun(50, 50, 20, false)
	case "moon":
		moon(48, 47, 30)
	case "wind":
		stroke(12, 32, 65, 32, workspaceText)
		curve(65, 32, [6]float64{86, 32, 86, 8, 66, 14}, workspaceText)
		stroke(8, 50, 81, 50, workspaceText)
		curve(81, 50, [6]float64{98, 50, 98, 73, 80, 71}, workspaceText)
		stroke(15, 68, 53, 68, workspaceText)
	default:
		if token == "partly" {
			sun(64, 29, 13, true)
		} else if token == "partly-night" {
			moon(63, 28, 20)
		}
		segments := [][6]float64{
			{10, 78, 6, 72, 6, 61}, {6, 50, 16, 43, 26, 45},
			{29, 30, 39, 22, 53, 22}, {68, 22, 79, 32, 81, 47},
			{91, 47, 96, 54, 96, 64}, {96, 72, 91, 78, 82, 78},
			{62, 78, 42, 78, 22, 78},
		}
		// Knock out only this temporary layer's cloud interior, hiding the
		// rear sun/moon without filling the cloud against the real wallpaper.
		mask := image.NewAlpha(art.Bounds())
		path := vector.NewRasterizer(200, 200)
		path.MoveTo(44, 156)
		for _, segment := range segments {
			path.CubeTo(float32(segment[0]*2), float32(segment[1]*2), float32(segment[2]*2), float32(segment[3]*2), float32(segment[4]*2), float32(segment[5]*2))
		}
		path.ClosePath()
		path.Draw(mask, mask.Bounds(), image.White, image.Point{})
		for y := 0; y < art.Rect.Dy(); y++ {
			for x := 0; x < art.Rect.Dx(); x++ {
				keep := 255 - int(mask.AlphaAt(x, y).A)
				offset := art.PixOffset(x, y)
				for channel := 0; channel < 4; channel++ {
					art.Pix[offset+channel] = uint8(int(art.Pix[offset+channel]) * keep / 255)
				}
			}
		}
		x, y := 22., 78.
		for _, segment := range segments {
			curve(x, y, segment, workspaceText)
			x, y = segment[4], segment[5]
		}
		switch token {
		case "rain":
			for _, x := range []float64{29, 50, 71} {
				stroke(x, 85, x-5, 95, workspaceText)
			}
		case "storm":
			stroke(53, 78, 44, 88, accent)
			stroke(44, 88, 54, 88, accent)
			stroke(54, 88, 47, 97, accent)
		case "snow":
			for _, x := range []float64{29, 52, 75} {
				for index := 0; index < 3; index++ {
					angle := float64(index) * math.Pi / 3
					stroke(x-math.Cos(angle)*5, 90-math.Sin(angle)*5, x+math.Cos(angle)*5, 90+math.Sin(angle)*5, workspaceText)
				}
			}
		case "fog":
			stroke(20, 86, 85, 86, workspaceText)
			stroke(29, 95, 75, 95, workspaceText)
		}
	}
	xdraw.CatmullRom.Scale(canvas, bounds, art, art.Bounds(), draw.Over, nil)
}

func workspaceWeatherKind(kind string) string {
	token := strings.ToLower(strings.TrimSpace(kind))
	switch {
	case strings.Contains(token, "partly") && strings.Contains(token, "night"):
		return "partly-night"
	case strings.Contains(token, "partly"):
		return "partly"
	case token == "moon" || strings.Contains(token, "night"):
		return "moon"
	case token == "sun" || token == "sunny" || token == "clear" || strings.Contains(token, "clear-day"):
		return "sun"
	case strings.Contains(token, "thunder") || strings.Contains(token, "storm") || strings.Contains(token, "lightning"):
		return "storm"
	case strings.Contains(token, "snow") || strings.Contains(token, "sleet") || strings.Contains(token, "ice"):
		return "snow"
	case strings.Contains(token, "rain") || strings.Contains(token, "shower") || strings.Contains(token, "drizzle"):
		return "rain"
	case strings.Contains(token, "fog") || strings.Contains(token, "mist") || strings.Contains(token, "haze"):
		return "fog"
	case strings.Contains(token, "wind"):
		return "wind"
	default:
		return "cloud"
	}
}

// Use the same presence check for the main weather block and its condition.
func workspaceWeatherVisible(weather *show.Weather) bool {
	return weather != nil && strings.TrimSpace(weather.TemperatureText) != ""
}

// The host omits disabled Environment Core slots. Never add placeholders or
// duplicate the indoor temperature in a second, right-hand group.
func workspaceHomeReadings(weather *show.Weather) []string {
	if weather == nil {
		return nil
	}
	items := []string{}
	// Condition metadata can remain in the host summary when the outdoor
	// weather slot is not selected. Match the main weather block's visibility,
	// without hiding independently enabled feels-like and sensor readings.
	if workspaceWeatherVisible(weather) {
		if condition := strings.TrimSpace(weather.Condition); condition != "" {
			items = append(items, condition)
		}
	}
	if feels := strings.TrimSpace(weather.FeelsLikeText); feels != "" {
		if !strings.HasPrefix(strings.ToLower(feels), "feels like") {
			feels = "Feels like " + feels
		}
		items = append(items, feels)
	}
	for _, item := range []struct{ label, value string }{
		{"Room humidity", weather.IndoorHumidityText},
		{"Humidity", weather.HumidityText}, {"Wind", weather.WindText},
		{"Rain", weather.RainText}, {"Lightning", weather.LightningText},
	} {
		if value := strings.TrimSpace(item.value); value != "" {
			items = append(items, item.label+" "+value)
		}
	}
	return items
}

func (r *renderer) workspaceHomeFooterLines(weather *show.Weather, width, height int) ([]string, int) {
	items := workspaceHomeReadings(weather)
	size := 14
	if height >= 600 {
		size = 20
	}
	text := strings.Join(items, " · ")
	for size > 12 && textWidth(r.faces.regular[size], text) > width {
		size--
	}
	var lines []string
	current := ""
	for _, item := range items {
		item = fitTextToWidth(item, r.faces.regular[size], width)
		candidate := item
		if current != "" {
			candidate = current + " · " + item
		}
		if current != "" && textWidth(r.faces.regular[size], candidate) > width {
			lines = append(lines, current)
			current = item
		} else {
			current = candidate
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines, size
}

func (r *renderer) drawWorkspaceHomeFooter(canvas *image.RGBA, weather *show.Weather, margin int) {
	lines, size := r.workspaceHomeFooterLines(weather, canvas.Rect.Dx()-margin*2, canvas.Rect.Dy())
	baseline := canvas.Rect.Dy() - 22 - (len(lines)-1)*(size+8)
	for _, text := range lines {
		r.text(canvas, margin, baseline, text, size, false, workspaceMuted)
		baseline += size + 8
	}
}
