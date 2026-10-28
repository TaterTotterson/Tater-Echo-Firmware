//go:build linux

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/linuxinput"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

func TestSpotThemeControlsOptionalReplyRim(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.October, 3, 10, 20, 0, 0, time.UTC)
	r := &renderer{
		faces: faces, spotScreen: true, animationStart: now,
		state: show.Snapshot{Phase: "speaking", Connected: true, AssistantName: "Tater", DisplayEdgeGlow: true, AudioLevel: .8},
	}
	tater := image.NewRGBA(image.Rect(0, 0, 480, 480))
	r.state.DisplayTheme = "tater"
	r.render(tater, now)
	forest := image.NewRGBA(tater.Rect)
	r.state.DisplayTheme = "forest"
	r.render(forest, now)
	if bytes.Equal(tater.Pix, forest.Pix) {
		t.Fatal("changing display_theme did not change the Rook render")
	}
}

func TestSpotPhotoHomeUsesStaticWeatherArtAndRoundLayout(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.October, 2, 10, 20, 0, 0, time.UTC)
	r := &renderer{
		faces: faces, spotScreen: true, target: "rook", screenWidth: 480, screenHeight: 480,
		animationStart: now,
		state: show.Snapshot{
			Phase:         "idle",
			Connected:     true,
			AssistantName: "Jarvis",
			Room:          "Living room",
			Weather: &show.Weather{
				TemperatureText:       "72°",
				HumidityText:          "48%",
				WindText:              "8 mph",
				RainText:              "65%",
				LightningText:         "3 strikes",
				Condition:             "Partly cloudy",
				ConditionKind:         "partly",
				FeelsLikeText:         "Feels like 70°",
				IndoorTemperatureText: "69°",
				IndoorHumidityText:    "42%",
			},
		},
	}
	first := image.NewRGBA(image.Rect(0, 0, 480, 480))
	r.render(first, now)
	if got, want := r.defaultWallpaperImage.Bounds().Size(), image.Pt(480, 480); got != want {
		t.Fatalf("prepared Rook wallpaper size = %v, want %v", got, want)
	}
	second := image.NewRGBA(first.Rect)
	r.render(second, now.Add(750*time.Millisecond))
	if !bytes.Equal(first.Pix, second.Pix) {
		t.Fatal("Rook photo home changed even though its weather artwork is static")
	}
	if bytes.Equal(first.Pix, image.NewRGBA(first.Rect).Pix) {
		t.Fatal("Spot layout rendered blank")
	}
	if corner := second.RGBAAt(0, 0); corner != (color.RGBA{0, 0, 0, 255}) {
		t.Fatalf("round photo home corner = %v, want black mask", corner)
	}
	if previewDir := os.Getenv("SPOT_PREVIEW_DIR"); previewDir != "" {
		writeSpotPreview(t, filepath.Join(previewDir, "spot-weather.png"), second)
		r.state.Weather.Condition = "Rain showers"
		r.state.Weather.ConditionKind = "rain"
		r.render(second, now.Add(750*time.Millisecond))
		writeSpotPreview(t, filepath.Join(previewDir, "spot-weather-rain.png"), second)
		r.state.Weather.FeelsLikeText = "Feels like 77°"
		r.render(second, now.Add(750*time.Millisecond))
		writeSpotPreview(t, filepath.Join(previewDir, "spot-weather-warmer.png"), second)
		r.state.Weather.HumidityText = ""
		r.state.Weather.WindText = ""
		r.state.Weather.RainText = ""
		r.state.Weather.LightningText = ""
		r.render(second, now.Add(750*time.Millisecond))
		writeSpotPreview(t, filepath.Join(previewDir, "spot-weather-two-sensors.png"), second)
		r.state.Phase = "setup"
		r.state.Room = "Tater-Setup-Rook"
		r.render(second, now)
		writeSpotPreview(t, filepath.Join(previewDir, "spot-setup.png"), second)
		r.state.Phase = "offline"
		r.state.Connected = false
		r.render(second, now)
		writeSpotPreview(t, filepath.Join(previewDir, "spot-connecting.png"), second)
		r.state.Phase = "idle"
		r.state.Connected = true
		r.state.Room = "Living room"
		r.state.Notification = &show.Notification{Title: "Front door", CameraName: "Front door", Description: "A person was detected at your front door."}
		r.render(second, now)
		writeSpotPreview(t, filepath.Join(previewDir, "spot-awareness.png"), second)
		r.state.Notification = nil
		r.state.Timer = &show.Timer{Active: true, Label: "Pasta", OriginalDurationMS: 300_000, RemainingMS: 165_000}
		r.render(second, now)
		writeSpotPreview(t, filepath.Join(previewDir, "spot-timer.png"), second)
		r.state.Timer = nil
		r.state.Media = &show.Media{
			Active: true, PlaybackState: "playing", GroupName: "Downstairs",
			Title: "Garden Song", Artist: "The Taters", AccentColor: [3]uint8{255, 124, 44},
			ProgressMS: 75_000, DurationMS: 240_000, PlaybackSpeed: 1000,
			ProgressUpdatedAtUnixMS: now.UnixMilli(), Spectrum: []float64{.12, .28, .72, .55, .9, .38},
		}
		r.render(second, now)
		writeSpotPreview(t, filepath.Join(previewDir, "spot-now-playing.png"), second)
	}
}

func TestSpotUploadedWallpaperUsesCenteredRoundCrop(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	source := image.NewRGBA(image.Rect(0, 0, 960, 480))
	draw.Draw(source, source.Bounds(), image.NewUniform(color.RGBA{180, 30, 30, 255}), image.Point{}, draw.Src)
	draw.Draw(source, image.Rect(240, 0, 720, 480), image.NewUniform(color.RGBA{30, 180, 70, 255}), image.Point{}, draw.Src)
	prepared := prepareCoverImage(source, image.Pt(480, 480))

	now := time.Date(2026, time.October, 14, 9, 0, 0, 0, time.UTC)
	r := &renderer{
		faces: faces, spotScreen: true, target: "rook", screenWidth: 480, screenHeight: 480,
		animationStart: now, wallpaperID: "custom", wallpaperImage: prepared,
		state: show.Snapshot{
			Phase: "idle", Connected: true, AssistantName: "Spud", Room: "Living Room",
			WallpaperURLs: []string{"custom"},
		},
	}
	canvas := image.NewRGBA(image.Rect(0, 0, 480, 480))
	r.render(canvas, now)
	if got, want := canvas.RGBAAt(240, 100), (color.RGBA{30, 180, 70, 255}); got != want {
		t.Fatalf("Rook uploaded wallpaper center = %v, want centered crop color %v", got, want)
	}
	if got, want := canvas.RGBAAt(0, 0), (color.RGBA{0, 0, 0, 255}); got != want {
		t.Fatalf("Rook uploaded wallpaper corner = %v, want round-screen mask %v", got, want)
	}
}

func TestSpotNowPlayingUsesRoundSafeLayoutAndTimerPriority(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.October, 3, 10, 20, 0, 0, time.UTC)
	r := &renderer{faces: faces, spotScreen: true, animationStart: now, state: show.Snapshot{
		Phase: "idle", Connected: true,
		Media: &show.Media{
			Active: true, PlaybackState: "playing", Title: "Garden Song", Artist: "The Taters",
			DurationMS: 180_000, ProgressMS: 60_000, PlaybackSpeed: 1000,
			ProgressUpdatedAtUnixMS: now.UnixMilli(), AccentColor: [3]uint8{255, 124, 44},
			Spectrum: []float64{.1, .4, .9, .5},
		},
		Weather: &show.Weather{TemperatureText: "72°", Condition: "Sunny", ConditionKind: "sun"},
	}}
	mediaFrame := image.NewRGBA(image.Rect(0, 0, 480, 480))
	r.render(mediaFrame, now)
	r.state.Media = nil
	weatherFrame := image.NewRGBA(mediaFrame.Rect)
	r.render(weatherFrame, now)
	if bytes.Equal(mediaFrame.Pix, weatherFrame.Pix) {
		t.Fatal("Rook now playing did not replace weather")
	}
	if corner := mediaFrame.RGBAAt(0, 0); corner.A != 255 {
		t.Fatalf("Rook now playing did not paint an opaque round-screen background: %v", corner)
	}

	r.state.Media = &show.Media{Active: true, Title: "Garden Song"}
	r.state.Timer = &show.Timer{Active: true, ID: "tea", RemainingMS: 30_000, OriginalDurationMS: 60_000}
	withMedia := image.NewRGBA(mediaFrame.Rect)
	r.render(withMedia, now)
	r.state.Media = nil
	withoutMedia := image.NewRGBA(mediaFrame.Rect)
	r.render(withoutMedia, now)
	if !bytes.Equal(withMedia.Pix, withoutMedia.Pix) {
		t.Fatal("Rook now playing overrode its timer")
	}
}

func TestSpotSecondOutdoorReadingUsesRightSideNotRoomClimate(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	r := &renderer{faces: faces, state: show.Snapshot{Weather: &show.Weather{
		TemperatureText: "72°", HumidityText: "68%", Condition: "Cloudy", ConditionKind: "cloud",
	}}}
	canvas := image.NewRGBA(image.Rect(0, 0, 480, 480))
	r.drawSpotWeather(canvas, 0, color.RGBA{255, 132, 48, 255}, true)
	withoutRight := image.NewRGBA(canvas.Rect)
	r.state.Weather.HumidityText = ""
	r.drawSpotWeather(withoutRight, 0, color.RGBA{255, 132, 48, 255}, true)
	if bytes.Equal(canvas.Pix, withoutRight.Pix) {
		t.Fatal("second outdoor reading did not render on the right side")
	}
	if got := canvas.RGBAAt(184, 391); got.A != 0 {
		t.Fatalf("second outdoor reading leaked into room climate: %v", got)
	}
}

func TestSpotOutsideTemperatureAndHumidityUseSameLargeType(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	r := &renderer{faces: faces}
	if got := r.spotOuterMetricSize("72°", "48%"); got != 64 {
		t.Fatalf("ordinary outside readings use font size %d, want 64", got)
	}
	if number, percent := spotPercentValue(" 48 % "); number != "48" || !percent {
		t.Fatalf("percentage split = %q, %v; want 48, true", number, percent)
	}
	if got := r.spotOuterMetricSize("123456789", "48%"); got != 48 {
		t.Fatalf("oversized outside readings use font size %d, want shared fallback 48", got)
	}
}

func TestSpotEmptySensorProfileDrawsOnlyCenteredWeatherAnimation(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	weather := &show.Weather{Condition: "Partly cloudy", ConditionKind: "partly"}
	r := &renderer{faces: faces, state: show.Snapshot{Weather: weather}}
	actual := image.NewRGBA(image.Rect(0, 0, 480, 480))
	r.drawSpotWeather(actual, 1.25, color.RGBA{255, 132, 48, 255}, true)
	want := image.NewRGBA(actual.Rect)
	r.drawWeatherIcon(want, 240, 272, 64, weather.ConditionKind, 1.25, weatherColor(weather.ConditionKind))
	if !bytes.Equal(actual.Pix, want.Pix) {
		t.Fatal("empty sensor profile rendered text, placeholders, or room readings around the weather animation")
	}
	if previewDir := os.Getenv("SPOT_PREVIEW_DIR"); previewDir != "" {
		writeSpotPreview(t, filepath.Join(previewDir, "spot-weather-animation-only.png"), actual)
	}
}

func TestSpotFeelsBadgeTemperatureRelation(t *testing.T) {
	for _, test := range []struct {
		actual, feels, relation, want string
		warmer                        bool
		cooler                        bool
	}{
		{"72°", "Feels like 75°", "", "75°", true, false},
		{"72°", "Feels like 69°", "", "69°", false, true},
		{"72°", "Feels like 72°", "", "72°", false, false},
		{"--°", "Feels like 70°", "cooler", "70°", false, true},
		{"-4°", "Feels like -7°", "", "-7°", false, true},
	} {
		value, ink := spotFeelsBadge(&show.Weather{
			TemperatureText: test.actual, FeelsLikeText: test.feels, FeelsLikeRelation: test.relation,
		})
		if value != test.want || (ink.R > ink.B) != test.warmer || (ink.B > ink.R) != test.cooler {
			t.Errorf("actual=%q feels=%q: badge=%q color=%v", test.actual, test.feels, value, ink)
		}
	}
}

func TestSpotRoomClimateHasNoEnclosingCard(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	r := &renderer{faces: faces}
	canvas := image.NewRGBA(image.Rect(0, 0, 480, 480))
	background := canvas.RGBAAt(240, 390)
	r.drawSpotRoomClimate(canvas, color.RGBA{255, 132, 48, 255}, "69°", "42%")
	if got := canvas.RGBAAt(240, 390); got != background {
		t.Fatalf("room climate still draws a surrounding card: got=%v want=%v", got, background)
	}
	if got := canvas.RGBAAt(132, 391); got == background {
		t.Fatal("room temperature glyph did not render")
	}
	if got := canvas.RGBAAt(266, 391); got == background {
		t.Fatal("room humidity glyph did not render")
	}
}

func TestSpotRoomClimateHeadingUsesAssignedRoom(t *testing.T) {
	if got := spotRoomClimateHeading("Game room"); got != "GAME ROOM CLIMATE" {
		t.Fatalf("room climate heading = %q, want GAME ROOM CLIMATE", got)
	}
	if got := spotRoomClimateHeading(""); got != "ROOM CLIMATE" {
		t.Fatalf("empty room climate heading = %q, want ROOM CLIMATE", got)
	}
}

func TestSpotPhotoHomeAlwaysUsesAssignedRoom(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.October, 3, 10, 20, 0, 0, time.UTC)
	r := &renderer{faces: faces, spotScreen: true, animationStart: now, state: show.Snapshot{
		Connected: true,
		Room:      "Living room",
		Weather: &show.Weather{
			TemperatureText: "72°",
			HumidityText:    "48%",
			Condition:       "Partly cloudy",
			ConditionKind:   "partly",
		},
	}}
	livingRoom := image.NewRGBA(image.Rect(0, 0, 480, 480))
	r.render(livingRoom, now)
	r.state.Room = "Game room"
	gameRoom := image.NewRGBA(livingRoom.Rect)
	r.render(gameRoom, now)
	if bytes.Equal(livingRoom.Pix, gameRoom.Pix) {
		t.Fatal("assigned room name did not change the Rook photo home")
	}
}

func TestSpotReplyRimFollowsSpeakerAudioWithoutBottomBubble(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.October, 3, 10, 20, 0, 0, time.UTC)
	r := &renderer{
		faces: faces, spotScreen: true, animationStart: now,
		state: show.Snapshot{
			Phase: "speaking", Connected: true, AssistantName: "Jarvis", Room: "Living room", DisplayEdgeGlow: true,
			WallpaperIntervalMinutes: 1440,
			Weather:                  &show.Weather{TemperatureText: "72°", Condition: "Partly cloudy", ConditionKind: "partly"},
		},
	}
	quiet := image.NewRGBA(image.Rect(0, 0, 480, 480))
	r.render(quiet, now)
	r.state.AudioLevel = .7
	loud := image.NewRGBA(quiet.Rect)
	for frame := 1; frame <= 5; frame++ {
		r.render(loud, now.Add(time.Duration(frame)*33*time.Millisecond))
	}
	quietEdge, loudEdge := quiet.RGBAAt(240, 5), loud.RGBAAt(240, 5)
	if loudEdge.R <= quietEdge.R+60 || loudEdge.G <= quietEdge.G+50 {
		t.Fatalf("reply rim did not brighten with audio: quiet=%v loud=%v", quietEdge, loudEdge)
	}
	if quiet.RGBAAt(370, 393) != loud.RGBAAt(370, 393) {
		t.Fatal("reply audio changed the old bottom-corner bubble area")
	}
	if previewDir := os.Getenv("SPOT_PREVIEW_DIR"); previewDir != "" {
		writeSpotPreview(t, filepath.Join(previewDir, "spot-reply-quiet.png"), quiet)
		writeSpotPreview(t, filepath.Join(previewDir, "spot-reply-loud.png"), loud)
	}
}

func TestSpotCachesStaticPhotoHomeAndReplyComposition(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.October, 3, 10, 20, 0, 0, time.UTC)
	r := &renderer{
		faces: faces, spotScreen: true, animationStart: now,
		state: show.Snapshot{
			Phase: "idle", Connected: true, AudioLevel: .1, Room: "Living room", DisplayEdgeGlow: true,
			Weather: &show.Weather{TemperatureText: "72°", Condition: "Partly cloudy", ConditionKind: "partly"},
		},
	}
	canvas := image.NewRGBA(image.Rect(0, 0, 480, 480))
	r.render(canvas, now)
	base := r.spotFrameBase
	if base == nil || r.spotFrameBaseKey == "" {
		t.Fatal("Rook speaking/weather frame was not cached")
	}
	first := append([]byte(nil), canvas.Pix...)
	r.render(canvas, now.Add(1250*time.Millisecond))
	if r.spotFrameBase != base {
		t.Fatal("unchanged photo home rebuilt the static Rook UI")
	}
	if !bytes.Equal(first, canvas.Pix) {
		t.Fatal("static Rook photo home changed without new state")
	}
	if len(r.presentRegions) != 0 {
		t.Fatalf("static Rook photo home requested motion regions: %v", r.presentRegions)
	}

	r.state.Phase = "speaking"
	r.render(canvas, now.Add(1300*time.Millisecond))
	voiceBase := r.spotFrameBase
	r.state.AudioLevel = .8
	r.render(canvas, now.Add(1333*time.Millisecond))
	if r.spotFrameBase != voiceBase {
		t.Fatal("audio-level-only frame rebuilt the static Rook UI")
	}
}

func TestSpotStaticStateChangeInvalidatesFrameCache(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	r := &renderer{faces: faces, spotScreen: true, state: show.Snapshot{
		Phase: "idle", Connected: true, Room: "Kitchen",
		Weather: &show.Weather{TemperatureText: "72°", Condition: "Clear", ConditionKind: "sun"},
	}}
	canvas := image.NewRGBA(image.Rect(0, 0, 480, 480))
	r.render(canvas, now)
	base := r.spotFrameBase
	r.state.Room = "Office"
	r.render(canvas, now.Add(time.Second/30))
	if r.spotFrameBase == base {
		t.Fatal("room change did not invalidate the static Rook frame")
	}
	if len(r.presentRegions) != 1 || r.presentRegions[0] != canvas.Rect {
		t.Fatalf("Rook cache rebuild regions = %v, want full frame", r.presentRegions)
	}
}

func TestCachedSpotFrameMatchesFullComposition(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.October, 3, 10, 20, 0, 0, time.UTC)
	state := show.Snapshot{
		Phase: "speaking", Connected: true, AudioLevel: .65, Room: "Living room",
		Weather: &show.Weather{
			TemperatureText: "72°", Condition: "Partly cloudy", ConditionKind: "partly",
			HumidityText: "48%", IndoorTemperatureText: "71°", IndoorHumidityText: "44%",
		},
	}
	cachedRenderer := &renderer{faces: faces, spotScreen: true, animationStart: now, state: state}
	cached := image.NewRGBA(image.Rect(0, 0, 480, 480))
	cachedRenderer.render(cached, now.Add(1250*time.Millisecond))

	fullRenderer := &renderer{faces: faces, spotScreen: true, animationStart: now, state: state}
	full := image.NewRGBA(cached.Rect)
	seconds := now.Add(1250 * time.Millisecond).Sub(now).Seconds()
	fullRenderer.renderSpot(full, now.Add(1250*time.Millisecond), seconds, accentForTheme(state.Phase, state.DisplayTheme))
	if !bytes.Equal(cached.Pix, full.Pix) {
		bounds, differences := image.Rectangle{}, 0
		for y := 0; y < cached.Rect.Dy(); y++ {
			for x := 0; x < cached.Rect.Dx(); x++ {
				if cached.RGBAAt(x, y) == full.RGBAAt(x, y) {
					continue
				}
				point := image.Rect(x, y, x+1, y+1)
				if differences == 0 {
					bounds = point
				} else {
					bounds = bounds.Union(point)
				}
				differences++
			}
		}
		t.Fatalf("cached Rook composition differs from a full render at %d pixels in %v", differences, bounds)
	}
}

func TestStaticSpotVoiceFrameSkipsUnchangedPresent(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	r := &renderer{faces: faces, spotScreen: true, state: show.Snapshot{Phase: "listening", Connected: true}}
	canvas := image.NewRGBA(image.Rect(0, 0, 480, 480))
	r.render(canvas, now)
	r.render(canvas, now.Add(time.Second/30))
	if len(r.presentRegions) != 0 {
		t.Fatalf("unchanged static Rook frame regions = %v, want none", r.presentRegions)
	}
}

func TestSpotOtherVoiceStagesOnlyChangeTheHomeStatusWhenGlowIsDisabled(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.October, 3, 10, 20, 0, 0, time.UTC)
	r := &renderer{
		faces: faces, spotScreen: true, animationStart: now,
		state: show.Snapshot{Connected: true, Room: "Living room", Weather: &show.Weather{
			TemperatureText: "72°", Condition: "Partly cloudy", ConditionKind: "partly",
		}},
	}
	home := image.NewRGBA(image.Rect(0, 0, 480, 480))
	r.state.Phase = "idle"
	r.render(home, now)
	for _, phase := range []string{"listening", "thinking", "tool_call", "intercom"} {
		r.state.Phase = phase
		r.intercomPressed = phase == "intercom"
		frame := image.NewRGBA(home.Rect)
		r.render(frame, now)
		if bytes.Equal(frame.Pix, home.Pix) {
			t.Errorf("%s did not update the assistant status", phase)
		}
		if !imagesEqualOutside(home, frame, image.Rect(0, 324, 480, 348)) {
			t.Errorf("%s changed the orb-free photo home outside the room/status row", phase)
		}
		if previewDir := os.Getenv("SPOT_PREVIEW_DIR"); previewDir != "" {
			writeSpotPreview(t, filepath.Join(previewDir, "spot-"+phase+".png"), frame)
		}
	}
	r.intercomPressed = false
	r.state.Phase = "listening"
	r.state.Weather = nil
	withoutWeatherIdle := image.NewRGBA(image.Rect(0, 0, 480, 480))
	r.state.Phase = "idle"
	r.render(withoutWeatherIdle, now)
	r.state.Phase = "listening"
	frame := image.NewRGBA(withoutWeatherIdle.Rect)
	r.render(frame, now)
	if bytes.Equal(frame.Pix, withoutWeatherIdle.Pix) {
		t.Error("weather-free listening stage did not update the assistant status")
	}
	if !imagesEqualOutside(withoutWeatherIdle, frame, image.Rect(0, 324, 480, 348)) {
		t.Error("weather-free listening stage changed content outside the room/status row")
	}
}

func TestSpotThinkingGlowIsStatic(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.October, 3, 10, 20, 0, 0, time.UTC)
	state := show.Snapshot{
		Phase: "thinking", Connected: true, DisplayEdgeGlow: true, DisplayTheme: "forest",
		Room: "Living room", AssistantName: "Spud", WallpaperIntervalMinutes: 1440,
	}
	r := &renderer{faces: faces, spotScreen: true, target: "rook", screenWidth: 480, screenHeight: 480, animationStart: now, state: state}
	quiet := image.NewRGBA(image.Rect(0, 0, 480, 480))
	r.render(quiet, now)
	r.state.AudioLevel = .95
	loud := image.NewRGBA(quiet.Rect)
	r.render(loud, now.Add(time.Second/30))
	if !bytes.Equal(quiet.Pix, loud.Pix) {
		t.Fatal("thinking glow reacted to speaker audio; only reply glow should be reactive")
	}

	withoutGlow := &renderer{faces: faces, spotScreen: true, target: "rook", screenWidth: 480, screenHeight: 480, animationStart: now, state: state}
	withoutGlow.state.DisplayEdgeGlow = false
	plain := image.NewRGBA(quiet.Rect)
	withoutGlow.render(plain, now)
	if quiet.RGBAAt(240, 5) == plain.RGBAAt(240, 5) {
		t.Fatal("thinking state did not add its themed Rook edge glow")
	}
	if previewDir := os.Getenv("SPOT_PREVIEW_DIR"); previewDir != "" {
		writeSpotPreview(t, filepath.Join(previewDir, "spot-thinking-glow.png"), quiet)
	}
}

func TestSpotTapOpensActionMenuAndHoldStartsIntercom(t *testing.T) {
	var commands bytes.Buffer
	client := &socketClient{writer: bufio.NewWriter(&commands)}
	r := &renderer{spotScreen: true, state: show.Snapshot{Connected: true}}
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: 240, Y: 100})
	r.updateSpotHold(client, r.spotTouchAt.Add(spotHoldDelay-time.Millisecond))
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: 240, Y: 100})
	if commands.Len() != 0 || !r.menuOpen {
		t.Fatalf("quick tap did not open the action menu cleanly: commands=%q open=%t", commands.String(), r.menuOpen)
	}
	r.menuOpen = false
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: 370, Y: 300})
	r.updateSpotHold(client, r.spotTouchAt.Add(spotHoldDelay))
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: 370, Y: 300})
	if !strings.Contains(commands.String(), `"action":"intercom.start"`) || !strings.Contains(commands.String(), `"action":"intercom.stop"`) {
		t.Fatalf("Spot intercom hold = %q", commands.String())
	}
	if r.intercomPressed || r.spotTouchDown {
		t.Fatal("intercom remained active after release")
	}
	commands.Reset()
	r.state.Muted = true
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: 240, Y: 240})
	r.updateSpotHold(client, r.spotTouchAt.Add(spotHoldDelay))
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: 240, Y: 240})
	if commands.Len() != 0 {
		t.Fatalf("muted hold started intercom: %q", commands.String())
	}
}

func TestSpotActionMenuOpensClimate(t *testing.T) {
	var commands bytes.Buffer
	client := &socketClient{writer: bufio.NewWriter(&commands)}
	target := 21.5
	r := &renderer{spotScreen: true, screenWidth: 480, screenHeight: 480, state: show.Snapshot{
		Connected: true,
		Climate:   &show.Climate{Available: true, RoomID: "kitchen", RoomName: "Kitchen", TargetTemperature: &target, TemperatureUnit: "C", CanSetTemperature: true, TemperatureStep: .5, MinimumTemperature: 7, MaximumTemperature: 32},
	}}
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: 240, Y: 350})
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: 240, Y: 350})
	if !r.menuOpen || r.climateOpen {
		t.Fatalf("Rook home tap menu=%t climate=%t", r.menuOpen, r.climateOpen)
	}
	climateCard := actionMenuBounds(480, 480, 2, true)
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: (climateCard.Min.X + climateCard.Max.X) / 2, Y: (climateCard.Min.Y + climateCard.Max.Y) / 2})
	if !r.climateOpen || r.menuOpen || r.satChatOpen {
		t.Fatalf("Rook climate card menu=%t climate=%t sat-chat=%t", r.menuOpen, r.climateOpen, r.satChatOpen)
	}
	plus := climateSetpointBounds(480, 480, true, true)
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: (plus.Min.X + plus.Max.X) / 2, Y: (plus.Min.Y + plus.Max.Y) / 2})
	if !strings.Contains(commands.String(), `"action":"climate.set_temperature"`) || !strings.Contains(commands.String(), `"value":22`) {
		t.Fatalf("Rook setpoint command = %q", commands.String())
	}
}

func TestSpotClimateControlsStayInsideCircularScreen(t *testing.T) {
	inside := func(point image.Point) bool {
		dx, dy := point.X-240, point.Y-240
		return dx*dx+dy*dy <= 236*236
	}
	assertBoundsInside := func(name string, bounds image.Rectangle) {
		t.Helper()
		for _, point := range []image.Point{
			bounds.Min,
			{X: bounds.Max.X, Y: bounds.Min.Y},
			bounds.Max,
			{X: bounds.Min.X, Y: bounds.Max.Y},
		} {
			if !inside(point) {
				t.Fatalf("%s point %v lies outside Rook circle", name, point)
			}
		}
	}
	assertRoundControlInside := func(name string, bounds image.Rectangle) {
		t.Helper()
		cx, cy := (bounds.Min.X+bounds.Max.X)/2, (bounds.Min.Y+bounds.Max.Y)/2
		for _, point := range []image.Point{
			{X: bounds.Min.X, Y: cy}, {X: bounds.Max.X - 1, Y: cy},
			{X: cx, Y: bounds.Min.Y}, {X: cx, Y: bounds.Max.Y - 1},
		} {
			if !inside(point) {
				t.Fatalf("%s painted edge %v lies outside Rook circle", name, point)
			}
		}
	}

	assertRoundControlInside("close", climateCloseBounds(480, 480, true))
	assertRoundControlInside("next", spotClimateNextBounds())
	for index := 0; index < 3; index++ {
		assertBoundsInside(fmt.Sprintf("room %d", index+1), spotClimateRoomBounds(index))
	}
}

func TestSpotClimatePreview(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	current, target, humidity := 71.0, 72.0, 42.0
	r := &renderer{
		faces: faces, target: "rook", spotScreen: true, screenWidth: 480, screenHeight: 480,
		climateOpen: true, received: now, animationStart: now,
		state: show.Snapshot{Connected: true, Room: "Family Room", Climate: &show.Climate{
			Available: true, RoomID: "living_room", RoomName: "Living Room",
			CurrentTemperature: &current, TargetTemperature: &target, CurrentHumidity: &humidity,
			TemperatureUnit: "F", HVACMode: "auto", HVACAction: "heating",
			AvailableHVACModes: []string{"heat", "cool", "auto", "off"}, CanSetTemperature: true, CanSetMode: true,
			RoomTemperatures: []show.RoomTemperature{
				{RoomName: "Living Room", Temperature: 71}, {RoomName: "Kitchen", Temperature: 73}, {RoomName: "Office", Temperature: 72},
				{RoomName: "Bedroom", Temperature: 68}, {RoomName: "Nursery", Temperature: 70}, {RoomName: "Garage", Temperature: 64},
			},
		}},
	}
	canvas := image.NewRGBA(image.Rect(0, 0, 480, 480))
	r.render(canvas, now)
	if previewDir := os.Getenv("SPOT_PREVIEW_DIR"); previewDir != "" {
		writeSpotPreview(t, filepath.Join(previewDir, "spot-climate.png"), canvas)
	}
	client := &socketClient{writer: bufio.NewWriter(&bytes.Buffer{})}
	next := spotClimateNextBounds()
	r.handleClimateTouch(client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: (next.Min.X + next.Max.X) / 2, Y: (next.Min.Y + next.Max.Y) / 2}, 480, 480)
	if r.climatePage != 1 {
		t.Fatalf("Rook climate next page = %d, want 1", r.climatePage)
	}
}

func TestSpotSatChatDirectoryPagesAndSelectsVisibleTarget(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	participants := make([]show.SatChatParticipant, 12)
	for index := range participants {
		participants[index] = show.SatChatParticipant{
			ID: fmt.Sprintf("native:rook-sat-%02d", index), Name: fmt.Sprintf("Room Satellite %02d", index+1), Audio: true, Video: index%2 == 0,
		}
	}
	var commands bytes.Buffer
	client := &socketClient{writer: bufio.NewWriter(&commands)}
	r := &renderer{
		faces: faces, target: "rook", spotScreen: true, screenWidth: 480, screenHeight: 480, satChatOpen: true,
		state: show.Snapshot{Connected: true, Phase: "idle", SatChat: &show.SatChat{Participants: participants}},
	}
	if got := r.satChatPageSize(480, 480); got != 5 {
		t.Fatalf("Rook page size = %d, want 5", got)
	}
	next := spotSatChatNextBounds()
	r.handleSpotSatChatTouch(client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: (next.Min.X + next.Max.X) / 2, Y: (next.Min.Y + next.Max.Y) / 2})
	if r.satChatPage != 1 {
		t.Fatalf("Rook next page = %d, want 1", r.satChatPage)
	}
	r.handleSpotSatChatTouch(client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: 240, Y: 130})
	if want := participants[5].ID; !strings.Contains(commands.String(), `"target":"`+want+`"`) {
		t.Fatalf("Rook first target on page 2 = %q, want %q", commands.String(), want)
	}

	canvas := image.NewRGBA(image.Rect(0, 0, 480, 480))
	r.render(canvas, time.Unix(1_800_000_000, 0))
	if previewDir := os.Getenv("SPOT_PREVIEW_DIR"); previewDir != "" {
		writeSpotPreview(t, filepath.Join(previewDir, "spot-sat-chat-directory-page-2.png"), canvas)
	}
	closeBounds := spotSatChatCloseBounds()
	r.handleSpotSatChatTouch(client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: (closeBounds.Min.X + closeBounds.Max.X) / 2, Y: (closeBounds.Min.Y + closeBounds.Max.Y) / 2})
	if r.satChatOpen || r.satChatPage != 0 {
		t.Fatalf("Rook close left menu open=%t page=%d", r.satChatOpen, r.satChatPage)
	}
}

func TestSpotTimerTapAndHoldDoDifferentThings(t *testing.T) {
	var commands bytes.Buffer
	client := &socketClient{writer: bufio.NewWriter(&commands)}
	r := &renderer{spotScreen: true, state: show.Snapshot{Connected: true, Timer: &show.Timer{Active: true}}}
	commands.Reset()
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: 240, Y: 394})
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: 240, Y: 394})
	if !strings.Contains(commands.String(), `"action":"timer.stop"`) {
		t.Fatalf("Spot timer tap = %q", commands.String())
	}
	commands.Reset()
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: 240, Y: 394})
	r.updateSpotHold(client, r.spotTouchAt.Add(spotHoldDelay))
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: 240, Y: 394})
	if strings.Contains(commands.String(), `"action":"timer.stop"`) || !strings.Contains(commands.String(), `"action":"intercom.start"`) {
		t.Fatalf("Spot timer hold should be intercom, got %q", commands.String())
	}
}

func writeSpotPreview(t *testing.T, path string, canvas *image.RGBA) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, canvas); err != nil {
		t.Fatal(err)
	}
}
