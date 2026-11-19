//go:build linux

package main

import (
	"bytes"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

func rookHomeFixture(t *testing.T) (*renderer, *image.RGBA, time.Time) {
	t.Helper()
	r, canvas, _ := rookFixture(t)
	r.state.Room, r.state.DisplayTheme = "Game Room", "tater"
	r.state.Weather = &show.Weather{TemperatureText: "68°", Condition: "Partly cloudy", ConditionKind: "partly",
		Daylight: "day", FeelsLikeText: "66°", IndoorTemperatureText: "72°", IndoorHumidityText: "42%", WindText: "NW 5 mph"}
	art, _, err := readDefaultWallpaper("rook", 6) // alpine-sunset
	if err != nil {
		t.Fatal(err)
	}
	r.state.WallpaperURLs = []string{"preview-only"}
	r.wallpaperImage = prepareCoverImage(art, canvas.Rect.Size())
	return r, canvas, time.Date(2026, time.November, 18, 8, 40, 0, 0, time.UTC)
}

func assertRookHomeTextFits(t *testing.T, r *renderer, value string, size int, at image.Point) {
	t.Helper()
	if value == "" {
		return
	}
	metrics := r.faces.regular[size].Metrics()
	box := image.Rect(at.X, at.Y-metrics.Ascent.Ceil(), at.X+textWidth(r.faces.regular[size], value), at.Y+metrics.Descent.Ceil())
	for _, p := range []image.Point{box.Min, box.Max, image.Pt(box.Min.X, box.Max.Y), image.Pt(box.Max.X, box.Min.Y)} {
		if math.Hypot(float64(p.X-240), float64(p.Y-240)) > rookPanelRadius {
			t.Fatalf("home text %q escapes the round panel: %v", value, box)
		}
	}
}

func TestRookHomeMatchesApprovedShowOrder(t *testing.T) {
	r, canvas, now := rookHomeFixture(t)
	before := *r.state.Weather
	h := r.rookHomeHeading()
	digits, suffix := r.clockParts(r.localTime(now))
	row := r.rookHomeMainLayout(digits, suffix)
	if rookHomeClockSize != 74 || row.clockAt.Y != 250 || rookHomeDateY != 289 || h.assistantAt.Y != 327 || row.icon.Dx() != 36 || row.icon.Min.Y != 214 {
		t.Fatal("Rook no longer has the approved compact Show order", h, row)
	}
	if row.temperature != "68°" || row.separator.Dy() != 52 || row.separator.Min.X <= row.clockAt.X+r.clockWidth(digits, suffix, rookHomeClockSize) {
		t.Fatal("weather no longer shares the clock row with a divider", row)
	}
	if detail := r.rookHomeDate(r.localTime(now)); detail != "Wednesday, November 18 · Partly cloudy" {
		t.Fatal("date and condition should be together below the clock", detail)
	}
	if got := rookHomeFeelsLike(r.state.Weather); got != "Feels like 66°" {
		t.Fatal("feels-like should have its own formatted row", got)
	}
	lines, size := r.rookHomeSensorLines()
	if len(lines) != 1 || size != 15 || lines[0] != "Room humidity 42% · Wind NW 5 mph" {
		t.Fatal("ordinary sensors no longer fit the approved footer", lines, size)
	}
	r.drawRookHome(canvas, now)
	if *r.state.Weather != before {
		t.Fatal("home layout mutated the host weather snapshot")
	}
	if canvas.RGBAAt(h.dot.X, h.dot.Y) != h.ink {
		t.Fatal("connection status dot was lost")
	}
	for _, entry := range []struct {
		value string
		at    image.Point
	}{
		{h.assistant, h.assistantAt}, {h.room, h.roomAt}, {h.temperature, h.temperatureAt},
	} {
		assertRookHomeTextFits(t, r, entry.value, 17, entry.at)
	}
}

func TestRookHomeHandlesClockFormatsAndLongLabels(t *testing.T) {
	r, canvas, now := rookHomeFixture(t)
	r.state.AssistantName, r.state.Room = strings.Repeat("Long", 40), strings.Repeat("Room ", 50)
	r.state.Weather.Condition, r.state.Weather.TemperatureText = strings.Repeat("Thunderstorms ", 20), "-123.4°C"
	r.state.Weather.IndoorTemperatureText = "-100.5°C"
	weather := r.state.Weather
	for _, visible := range []bool{true, false} {
		if !visible {
			r.state.Weather = nil
		}
		for _, format := range []string{"12h", "24h"} {
			r.state.ScreenClockFormat = format
			for hour := 0; hour < 24; hour++ {
				local := time.Date(2026, time.September, 30, hour, 59, 0, 0, time.UTC)
				digits, suffix := r.clockParts(local)
				if (suffix == "") != (format == "24h") {
					t.Fatal("clock format was ignored", format, digits, suffix)
				}
				row := r.rookHomeMainLayout(digits, suffix)
				assertRookHomeTextFits(t, r, digits, rookHomeClockSize, row.clockAt)
				if suffix != "" {
					size := clockSuffixSize(rookHomeClockSize)
					at := row.clockAt.Add(image.Pt(textWidth(r.faces.regular[rookHomeClockSize], digits)+size/3, 0))
					assertRookHomeTextFits(t, r, suffix, size, at)
				}
				if visible {
					assertRookHomeTextFits(t, r, row.temperature, rookHomeWeatherSize, row.temperatureAt)
					if row.separator.Min.X <= row.clockAt.X+r.clockWidth(digits, suffix, rookHomeClockSize) || row.icon.Min.X <= row.separator.Max.X || row.temperatureAt.X <= row.icon.Max.X {
						t.Fatal("long clock/weather values overlap", row)
					}
				} else if row.clockAt.X != (480-r.clockWidth(digits, suffix, rookHomeClockSize))/2 || !row.icon.Empty() || !row.separator.Empty() {
					t.Fatal("weather-free clock is not centered on its own", row)
				}
				date := r.rookHomeDate(local)
				assertRookHomeTextFits(t, r, date, 16, image.Pt(240-textWidth(r.faces.regular[16], date)/2, rookHomeDateY))
			}
		}
	}
	r.state.Weather = weather
	h := r.rookHomeHeading()
	for value, at := range map[string]image.Point{h.assistant: h.assistantAt, h.room: h.roomAt, h.temperature: h.temperatureAt} {
		assertRookHomeTextFits(t, r, value, 17, at)
	}
	r.drawRookHome(canvas, now)
}

func TestRookHomeRetainsEveryNormalSensorAndFitsLongReadings(t *testing.T) {
	r, canvas, now := rookHomeFixture(t)
	w := r.state.Weather
	w.HumidityText, w.RainText, w.LightningText = "68%", "65%", "3 strikes"
	before := *w
	lines, size := r.rookHomeSensorLines()
	joined := strings.Join(lines, " · ")
	for _, reading := range []string{"Room humidity 42%", "Humidity 68%", "Wind NW 5 mph", "Rain 65%", "Lightning 3 strikes"} {
		if !strings.Contains(joined, reading) {
			t.Fatal("enabled reading disappeared", reading, lines, size)
		}
	}
	if strings.Contains(joined, "cloudy") || strings.Contains(joined, "Feels") || strings.Contains(joined, "72°") || strings.Contains(joined, "…") {
		t.Fatal("footer duplicates another row or truncates ordinary readings", lines)
	}
	for _, long := range []bool{false, true} {
		if long {
			w.WindText, w.LightningText = strings.Repeat("Extreme gusts ", 30), strings.Repeat("Nearby lightning ", 30)
		}
		lines, size = r.rookHomeSensorLines()
		if len(lines) > 2 || size < 12 || size > 15 {
			t.Fatal("footer exceeded its bounded layout", lines, size)
		}
		for index, value := range lines {
			width := textWidth(r.faces.regular[size], value)
			if width > rookHomeSensorWidth(index) {
				t.Fatal("sensor line exceeded its round-safe width", lines)
			}
			assertRookHomeTextFits(t, r, value, size, image.Pt(240-width/2, rookHomeSensorY+index*24))
		}
		r.drawRookHome(canvas, now)
	}
	// The intentionally long readings above are the only snapshot changes.
	before.WindText, before.LightningText = w.WindText, w.LightningText
	if !reflect.DeepEqual(*w, before) {
		t.Fatal("sensor formatting changed the host state")
	}
}

func TestRookHomeHidesOnlyDisabledWeather(t *testing.T) {
	r, canvas, now := rookHomeFixture(t)
	r.state.Weather.TemperatureText = " \t"
	digits, suffix := r.clockParts(r.localTime(now))
	if row := r.rookHomeMainLayout(digits, suffix); !row.icon.Empty() || !row.separator.Empty() || strings.Contains(r.rookHomeDate(r.localTime(now)), "cloudy") {
		t.Fatal("hidden weather left a condition, glyph or separator", row)
	}
	if lines, _ := r.rookHomeSensorLines(); len(lines) != 1 || rookHomeFeelsLike(r.state.Weather) != "Feels like 66°" {
		t.Fatal("independently enabled readings were hidden with weather")
	}
	r.drawRookHome(canvas, now)
	withStaleCondition := bytes.Clone(canvas.Pix)
	r.state.Weather.Condition, r.state.Weather.ConditionKind = "", ""
	r.drawRookHome(canvas, now)
	if !bytes.Equal(canvas.Pix, withStaleCondition) {
		t.Fatal("unselected weather still paints condition metadata")
	}
	r.state.Weather = nil
	if lines, _ := r.rookHomeSensorLines(); len(lines) != 0 || rookHomeFeelsLike(nil) != "" {
		t.Fatal("missing weather leaves placeholder readings")
	}
	r.drawRookHome(canvas, now)
}

func TestRookHomeGPUKeepsHomeUntilVisibleDataChanges(t *testing.T) {
	r, canvas, now := rookHomeFixture(t)
	frame, ok := r.prepareGPUScene(canvas, now, true, true, true)
	if !ok || len(frame.source) == 0 || r.gpuScene.surface != "home" {
		t.Fatal("Rook home was not uploaded through the retained GPU path")
	}
	want := image.NewRGBA(canvas.Rect)
	r.drawRookHome(want, now)
	if !bytes.Equal(frame.source, want.Pix) {
		t.Fatal("GPU home does not match the production renderer")
	}
	for _, delay := range []time.Duration{time.Millisecond, time.Second, 5 * time.Second} {
		copy := *r.state.Weather
		r.state.Weather = &copy
		same, _ := r.prepareGPUScene(canvas, now.Add(delay), true, true, true)
		if same.present || len(same.source) != 0 {
			t.Fatal("unchanged home/weather reuploaded", delay)
		}
	}
	for index, change := range []func(){
		func() { r.state.Weather.TemperatureText = "69°" },
		func() { r.state.Weather.TemperatureText = "" },
		func() { r.state.Weather.IndoorHumidityText = "43%" },
		func() { r.state.DisplayTheme = "ocean" },
		func() { r.state.ScreenClockFormat = "24h" },
	} {
		change()
		now = now.Add(time.Second)
		changed, _ := r.prepareGPUScene(canvas, now, true, true, true)
		if !changed.present || len(changed.source) == 0 {
			t.Fatal("visible home data did not replace the GPU layer", index)
		}
	}
	minute, _ := r.prepareGPUScene(canvas, now.Add(time.Minute), true, true, true)
	if !minute.present || len(minute.source) == 0 {
		t.Fatal("clock minute failed to refresh")
	}
}

// Preview uses the production renderer, not an approximation of its layout.
func TestRookHomeNativePreviews(t *testing.T) {
	directory := os.Getenv("TATER_ROOK_HOME_PREVIEWS")
	if directory == "" {
		t.Skip("set TATER_ROOK_HOME_PREVIEWS for native-size production previews")
	}
	for _, state := range []string{"show-order", "all-sensors", "no-weather", "long-room", "night", "24-hour", "ocean"} {
		r, canvas, now := rookHomeFixture(t)
		switch state {
		case "all-sensors":
			r.state.Weather.HumidityText, r.state.Weather.RainText, r.state.Weather.LightningText = "68%", "65%", "3 strikes"
		case "no-weather":
			r.state.Weather = nil
		case "long-room":
			r.state.Room = "Upstairs Reading Room"
		case "night":
			r.state.Weather.Daylight = "night"
		case "24-hour":
			r.state.ScreenClockFormat = "24h"
		case "ocean":
			r.state.DisplayTheme, r.state.Weather.Condition, r.state.Weather.ConditionKind = "ocean", "Sunny", "sun"
		}
		r.drawRookHome(canvas, now)
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(filepath.Join(directory, "rook-home-"+state+".png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(file, canvas)
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			t.Fatal("preview write failed", err, closeErr)
		}
	}
}
