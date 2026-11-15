//go:build linux

package main

import (
	"bytes"
	"image"
	"image/color"
	"math"
	"testing"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/linuxinput"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

func rookFixture(t *testing.T) (*renderer, *image.RGBA, time.Time) {
	t.Helper()
	r, _, now := gpuTestRenderer(t)
	r.target, r.spotScreen, r.screenWidth, r.screenHeight = "rook", true, 480, 480
	r.state.Settings = show.ScreenSettingDefaults()
	r.state.Room, r.state.AssistantName = "Living room", "Tater"
	return r, image.NewRGBA(image.Rect(0, 0, 480, 480)), now
}

func TestRookArtworkPreservesAspectAndAllFourCorners(t *testing.T) {
	for _, size := range []image.Point{image.Pt(1920, 1080), image.Pt(640, 480), image.Pt(100, 100), image.Pt(50, 200), image.Pt(200, 50)} {
		box := rookContainedArtwork(size, 228)
		if math.Abs(float64(box.Dx())/float64(box.Dy())-float64(size.X)/float64(size.Y)) > .04 {
			t.Fatal("aspect was stretched", size, box)
		}
		for _, p := range []image.Point{box.Min, image.Pt(box.Max.X, box.Min.Y), box.Max, image.Pt(box.Min.X, box.Max.Y)} {
			if math.Hypot(float64(p.X-240), float64(p.Y-240)) > 229 {
				t.Fatal("complete artwork escaped the round panel", size, box)
			}
		}
	}
	if !rookContainedArtwork(image.Point{}, 228).Empty() {
		t.Fatal("invalid source produced a picture")
	}
}

func TestRookSettingsAllPagesRenderWithinTheCircle(t *testing.T) {
	for _, page := range []string{"sound", "display", "appearance", "night", "dnd", "info", "equalizer"} {
		r, canvas, _ := rookFixture(t)
		r.settingsPage = page
		_, _, pages := r.rookSettingControls()
		for index := 0; index < pages; index++ {
			r.settingsIndex = index
			_, controls, _ := r.rookSettingControls()
			for _, control := range controls {
				b := control.bounds
				for _, p := range []image.Point{b.Min, image.Pt(b.Max.X, b.Min.Y), b.Max, image.Pt(b.Min.X, b.Max.Y)} {
					if math.Hypot(float64(p.X-240), float64(p.Y-240)) > rookPanelRadius {
						t.Fatal(page, index, "control clipped by panel", b)
					}
				}
			}
			r.drawRookSettings(canvas)
			if canvas.RGBAAt(0, 0) != (color.RGBA{A: 255}) || canvas.RGBAAt(240, 240).A != 255 {
				t.Fatal(page, index, "mask or opacity is invalid")
			}
		}
	}
}

func TestRookEqualizerHasEightIndependentlyEditableBands(t *testing.T) {
	r, _, _ := rookFixture(t)
	r.settingsPage, r.settingsOpen = "equalizer", true
	for band := 0; band < 8; band++ {
		r.settingsIndex = band / 4
		track := rookEqualizerTrack(band % 4)
		x := track.Min.X + track.Dx()/2
		r.handleRookSettingsTouch(nil, linuxinput.TouchEvent{Kind: linuxinput.Down, X: x, Y: track.Min.Y})
		r.handleRookSettingsTouch(nil, linuxinput.TouchEvent{Kind: linuxinput.Up, X: x, Y: track.Min.Y})
		if got := settingFloat(r.settingValue("eq_band_" + string(rune('0'+band)))); got != 12 {
			t.Fatal("wrong band or page updated", band, got)
		}
	}
	if r.settingsDragging != "" {
		t.Fatal("slider did not release")
	}
}

func TestRookHomeSuppressesStaleConditionsWithoutWeather(t *testing.T) {
	r, canvas, now := rookFixture(t)
	r.state.Weather = &show.Weather{Condition: "Sunny", ConditionKind: "sun"}
	r.drawRookHome(canvas, now)
	withStaleMetadata := bytes.Clone(canvas.Pix)
	r.state.Weather = nil
	r.drawRookHome(canvas, now)
	if !bytes.Equal(canvas.Pix, withStaleMetadata) {
		t.Fatal("disabled weather left an icon or condition on Rook")
	}
}

func TestRookHomeUsesShowsOutlineWeatherArtwork(t *testing.T) {
	for _, theme := range []string{"tater", "ocean"} {
		for _, kind := range []string{"sun", "partly", "cloud", "rain", "storm", "snow", "fog", "wind"} {
			for _, daylight := range []string{"day", "night"} {
				t.Run(theme+"/"+kind+"/"+daylight, func(t *testing.T) {
					r, canvas, now := rookFixture(t)
					r.state.DisplayTheme = theme
					r.state.Weather = &show.Weather{TemperatureText: "68°", ConditionKind: kind, Daylight: daylight}
					r.drawRookHome(canvas, now)
					bounds := r.rookHomeWeatherLayout().icon
					reference := image.NewRGBA(canvas.Rect)
					// Composite the shared glyph over the same wallpaper at its
					// new compact size, including the anti-aliased edge pixels.
					r.state.Weather.TemperatureText = ""
					r.drawRookHome(reference, now)
					r.state.Weather.TemperatureText = "68°"
					background := bytes.Clone(reference.Pix)
					drawWorkspaceWeatherGlyph(reference, bounds, weatherDisplayKind(r.state.Weather, r.localTime(now)), r.menuPalette().primary)
					checked := 0
					for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
						for x := bounds.Min.X; x < bounds.Max.X; x++ {
							want, got := reference.RGBAAt(x, y), canvas.RGBAAt(x, y)
							if want != got {
								t.Fatalf("weather pixel at (%d,%d) = %v, want Show artwork %v", x, y, got, want)
							}
							offset := reference.PixOffset(x, y)
							if !bytes.Equal(reference.Pix[offset:offset+4], background[offset:offset+4]) {
								checked++
							}
						}
					}
					if checked < 30 {
						t.Fatalf("weather reference has too few visible stroke pixels: %d", checked)
					}
				})
			}
		}
	}
}

func TestRookThinkingDotsUseTheSameRetainedAtlasAsShows(t *testing.T) {
	r, _, now := rookFixture(t)
	r.state.Phase = "thinking"
	r.state.Conversation = &show.Conversation{Phase: "thinking", TurnID: "rook-dots", UserText: "Clear tomorrow?"}
	r.updateConversationWords([]conversationWord{{value: "Tomorrow?", x: 150, y: 205, size: 30, ink: workspaceText}}, "rook-dots", now)
	atlas := r.gpuScene.words.atlas
	a, _ := r.gpuConversationText(now.Add(time.Second), "thinking")
	b, _ := r.gpuConversationText(now.Add(1300*time.Millisecond), "thinking")
	if len(a.Quads) != 4 || len(b.Quads) != 4 || len(b.Pixels) != 0 || r.gpuScene.words.atlas != atlas {
		t.Fatal("Rook dots rebuilt or failed to use the retained word atlas")
	}
	if a.Quads[3] == b.Quads[3] {
		t.Fatal("Rook dots did not breathe")
	}
}
