//go:build linux

package main

import (
	"bytes"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

func classicHomeFixture(t *testing.T, target string, extras bool) (*renderer, *image.RGBA, time.Time) {
	t.Helper()
	r, _, _ := gpuTestRenderer(t)
	width, height := 960, 480
	if target == "crown" || target == "athena" {
		width, height = 1280, 800
	}
	r.target, r.screenWidth, r.screenHeight = target, width, height
	r.state.AssistantName, r.state.Room, r.state.DisplayTheme = "Tater", "Living Room", "tater"
	r.state.Settings = show.ScreenSettingDefaults()
	r.state.Weather = &show.Weather{TemperatureText: "68°", Condition: "Partly cloudy", ConditionKind: "partly", FeelsLikeText: "Feels like 66°", IndoorTemperatureText: "72°"}
	if extras {
		r.state.Weather.IndoorHumidityText, r.state.Weather.HumidityText = "42%", "55%"
		r.state.Weather.WindText, r.state.Weather.RainText, r.state.Weather.LightningText = "NW 5 mph", "0.02 in", "0"
	}
	art, _, err := readDefaultWallpaper(target, 6) // alpine-sunset
	if err != nil {
		t.Fatal(err)
	}
	r.state.WallpaperURLs = []string{"preview-only"}
	r.wallpaperImage = prepareCoverImage(art, image.Pt(width, height))
	return r, image.NewRGBA(image.Rect(0, 0, width, height)), time.Date(2026, time.November, 14, 9, 41, 0, 0, time.FixedZone("CST", -6*60*60))
}

func TestClassicHomeLayoutRetainsApprovedSizesAndAlignment(t *testing.T) {
	for _, target := range []string{"checkers", "cronos", "crown", "athena"} {
		r, canvas, now := classicHomeFixture(t, target, true)
		width, height := canvas.Rect.Dx(), canvas.Rect.Dy()
		g := classicWorkspaceHomeGeometry(width, height)
		if g.clock != 96 || g.mainY <= height/2 || g.dateY <= g.mainY || g.roomY <= g.dateY {
			t.Fatalf("%s does not retain the approved lower-right hierarchy: %+v", target, g)
		}
		h := r.classicHomeHeading(width, g)
		if h.assistantAt.Y != g.roomY || h.temperatureAt.X+textWidth(r.faces.regular[g.heading], h.temperature) != width-g.margin {
			t.Fatal("room/temperature heading is not right aligned")
		}
		lines, size := r.classicHomeFooter(width, height, g)
		if strings.Contains(strings.Join(lines, "|"), "Partly cloudy") || r.state.Weather.Condition != "Partly cloudy" {
			t.Fatal("condition duplicated or host snapshot modified")
		}
		if height-22-(len(lines)-1)*(size+8)-size <= g.roomY {
			t.Fatal("optional sensor footer overlaps the room")
		}
		r.drawWorkspaceHome(canvas, now, r.menuPalette().primary)
		if canvas.RGBAAt(h.dot.X, h.dot.Y) != h.ink {
			t.Fatal("green connection dot was lost")
		}
	}
}

func TestClassicHomeHidesWeatherAndHandlesLongRoomAnd24HourClock(t *testing.T) {
	for _, target := range []string{"checkers", "crown"} {
		r, canvas, now := classicHomeFixture(t, target, true)
		r.state.ScreenClockFormat = "24h"
		r.state.AssistantName, r.state.Room = strings.Repeat("Long ", 70), strings.Repeat("Room ", 100)
		r.state.Weather.TemperatureText, r.state.Weather.IndoorTemperatureText = "", ""
		g := classicWorkspaceHomeGeometry(canvas.Rect.Dx(), canvas.Rect.Dy())
		if strings.Contains(r.classicHomeDate(now, canvas.Rect.Dx(), g), "cloudy") {
			t.Fatal("disabled weather leaves conditions beside the date")
		}
		h := r.classicHomeHeading(canvas.Rect.Dx(), g)
		if h.assistantAt.X < g.margin || h.roomAt.X+textWidth(r.faces.regular[g.heading], h.room) > canvas.Rect.Dx()-g.margin {
			t.Fatal("long names escape the native screen")
		}
		r.drawWorkspaceHome(canvas, now, r.menuPalette().primary)
		r.state.Weather = nil
		r.drawWorkspaceHome(canvas, now, r.menuPalette().primary)
	}
}

func TestClassicHomeMatchesApprovedNativeMockups(t *testing.T) {
	directory := os.Getenv("TATER_HOME_APPROVED_PREVIEWS")
	if directory == "" {
		t.Skip("set approved preview directory for exact native pixel comparison")
	}
	for target, name := range map[string]string{
		"crown":    "home-classic-layout-gpu-style-show8-v1-1280x800.png",
		"checkers": "home-classic-layout-gpu-style-show5-v1-960x480.png",
	} {
		r, canvas, now := classicHomeFixture(t, target, true)
		r.drawWorkspaceHome(canvas, now, r.menuPalette().primary)
		file, err := os.Open(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		want, err := png.Decode(file)
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
		rgba := image.NewRGBA(want.Bounds())
		draw.Draw(rgba, rgba.Bounds(), want, want.Bounds().Min, draw.Src)
		if !bytes.Equal(canvas.Pix, rgba.Pix) {
			t.Fatalf("%s differs from approved native-size mockup", target)
		}
	}
}
