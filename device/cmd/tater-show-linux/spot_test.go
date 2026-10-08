//go:build linux

package main

import (
	"bufio"
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/linuxinput"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

func TestSpotThemeChangesRenderedScreen(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.October, 3, 10, 20, 0, 0, time.UTC)
	r := &renderer{
		faces: faces, spotScreen: true, animationStart: now,
		state: show.Snapshot{Phase: "idle", Connected: true, AssistantName: "Tater"},
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

func TestSpotWeatherAnimationAndRoundLayout(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.October, 3, 10, 20, 0, 0, time.UTC)
	r := &renderer{
		faces:          faces,
		spotScreen:     true,
		animationStart: now,
		state: show.Snapshot{
			Phase:         "idle",
			Connected:     true,
			AssistantName: "Jarvis",
			Room:          "Living room",
			Weather: &show.Weather{
				TemperatureText:       "72°",
				HumidityText:          "48%",
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
	second := image.NewRGBA(first.Rect)
	r.render(second, now.Add(750*time.Millisecond))
	if bytes.Equal(first.Pix, second.Pix) {
		t.Fatal("Spot weather art did not animate")
	}
	if bytes.Equal(first.Pix, image.NewRGBA(first.Rect).Pix) {
		t.Fatal("Spot layout rendered blank")
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
	r.drawSpotWeather(canvas, 0, color.RGBA{255, 132, 48, 255})
	withoutRight := image.NewRGBA(canvas.Rect)
	r.state.Weather.HumidityText = ""
	r.drawSpotWeather(withoutRight, 0, color.RGBA{255, 132, 48, 255})
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
	r.drawSpotWeather(actual, 1.25, color.RGBA{255, 132, 48, 255})
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

func TestSpotRoomNameOnlyAppearsWithRoomClimate(t *testing.T) {
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
	if !bytes.Equal(livingRoom.Pix, gameRoom.Pix) {
		t.Fatal("room name still renders outside the room-climate section")
	}

	r.state.Weather.IndoorTemperatureText = "69°"
	withClimate := image.NewRGBA(livingRoom.Rect)
	r.render(withClimate, now)
	if bytes.Equal(gameRoom.Pix, withClimate.Pix) {
		t.Fatal("assigned room name did not render in the room-climate heading")
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
			Phase: "speaking", Connected: true, AssistantName: "Jarvis", Room: "Living room",
			Weather: &show.Weather{TemperatureText: "72°", Condition: "Partly cloudy", ConditionKind: "partly"},
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

func TestSpotOtherVoiceStagesDoNotDrawOrbs(t *testing.T) {
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
	for _, phase := range []string{"listening", "thinking", "tool_call", "intercom"} {
		r.state.Phase = phase
		r.intercomPressed = phase == "intercom"
		background := image.NewRGBA(image.Rect(0, 0, 480, 480))
		r.spotBackground(background, accentFor(phase))
		frame := image.NewRGBA(background.Rect)
		r.render(frame, now)
		if got, want := frame.RGBAAt(370, 393), background.RGBAAt(370, 393); got != want {
			t.Errorf("%s still draws the corner orb: got=%v background=%v", phase, got, want)
		}
		if previewDir := os.Getenv("SPOT_PREVIEW_DIR"); previewDir != "" {
			writeSpotPreview(t, filepath.Join(previewDir, "spot-"+phase+".png"), frame)
		}
	}
	r.intercomPressed = false
	r.state.Phase = "listening"
	r.state.Weather = nil
	background := image.NewRGBA(image.Rect(0, 0, 480, 480))
	r.spotBackground(background, accentFor("listening"))
	frame := image.NewRGBA(background.Rect)
	r.render(frame, now)
	if got, want := frame.RGBAAt(240, 235), background.RGBAAt(240, 235); got != want {
		t.Errorf("fallback still draws the center orb: got=%v background=%v", got, want)
	}
}

func TestSpotHoldAnywhereStartsIntercomButQuickTapDoesNot(t *testing.T) {
	var commands bytes.Buffer
	client := &socketClient{writer: bufio.NewWriter(&commands)}
	r := &renderer{spotScreen: true, state: show.Snapshot{Connected: true}}
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: 240, Y: 100})
	r.updateSpotHold(client, r.spotTouchAt.Add(spotHoldDelay-time.Millisecond))
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: 240, Y: 100})
	if commands.Len() != 0 {
		t.Fatalf("quick tap started intercom: %q", commands.String())
	}
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
