//go:build linux

package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/linuxinput"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

func TestDisplayThemesKeepTaterDefaultAndUsePrimaryColorForReply(t *testing.T) {
	wantTater := color.RGBA{255, 132, 48, 255}
	if got := accentForTheme("idle", ""); got != wantTater {
		t.Fatalf("empty theme did not preserve the Tater default: got=%v want=%v", got, wantTater)
	}
	if got := accentForTheme("speaking", "tater"); got != wantTater {
		t.Fatalf("Tater reply glow did not use the main orange: got=%v want=%v", got, wantTater)
	}

	seen := map[color.RGBA]string{}
	for _, theme := range []string{"tater", "ocean", "violet", "forest", "sunset"} {
		main := accentForTheme("idle", theme)
		if previous := seen[main]; previous != "" {
			t.Fatalf("themes %s and %s share the same primary color %v", previous, theme, main)
		}
		seen[main] = theme
		if reply := accentForTheme("speaking", theme); reply != main {
			t.Errorf("%s reply glow=%v, want main screen color=%v", theme, reply, main)
		}
	}
	if got := accentForTheme("idle", "not-a-theme"); got != wantTater {
		t.Fatalf("unknown theme did not safely fall back to Tater: %v", got)
	}
}

func TestCheckersThemeChangesRenderedScreen(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	r := &renderer{faces: faces, animationStart: now, state: show.Snapshot{Phase: "idle", Connected: true}}
	tater := image.NewRGBA(image.Rect(0, 0, 960, 480))
	r.state.DisplayTheme = "tater"
	r.render(tater, now)
	ocean := image.NewRGBA(tater.Rect)
	r.state.DisplayTheme = "ocean"
	r.render(ocean, now)
	if bytes.Equal(tater.Pix, ocean.Pix) {
		t.Fatal("changing display_theme did not change the Checkers render")
	}
}

func TestIntercomIsLowerLeftQuarterCircle(t *testing.T) {
	if intercomCenterX > -20 || intercomCenterY < 500 || micIconLeft+micIconWidth/2 >= 34 || micIconTop+micIconHeight/2 <= 432 {
		t.Fatal("intercom control and icon did not move down and left")
	}
	if !inCircle(24, 452, intercomCenterX, intercomCenterY, intercomRadius) {
		t.Fatal("visible microphone must be inside the touch target")
	}
	if !inCircle(80, 479, intercomCenterX, intercomCenterY, intercomRadius) ||
		!inCircle(0, 398, intercomCenterX, intercomCenterY, intercomRadius) {
		t.Fatal("corner circle is too small after moving off-screen")
	}
	if !inCircle(micIconLeft+micIconWidth, micIconTop, intercomCenterX, intercomCenterY, intercomRadius) {
		t.Fatal("microphone icon no longer fits inside the circle")
	}
	if inCircle(200, 440, intercomCenterX, intercomCenterY, intercomRadius) ||
		inCircle(24, 400, intercomCenterX, intercomCenterY, intercomRadius) {
		t.Fatal("intercom touch target extends past its visible corner")
	}
}

func TestIntercomGlyphIsWhiteAndVisible(t *testing.T) {
	icon := newMicrophoneIcon()
	for _, point := range []image.Point{{20, 10}, {20, 21}, {20, 45}} {
		pixel := icon.RGBAAt(point.X, point.Y)
		if pixel.A < 200 || pixel.R != pixel.G || pixel.G != pixel.B {
			t.Fatalf("microphone glyph at %v is not solid white: %v", point, pixel)
		}
	}
	canvas := image.NewRGBA(image.Rect(0, 0, 960, 480))
	r := &renderer{state: show.Snapshot{Connected: true}}
	r.drawIntercom(canvas, color.RGBA{76, 157, 255, 255})
	if pixel := canvas.RGBAAt(micIconLeft+20, micIconTop+10); pixel.R < 200 || pixel.G < 200 || pixel.B < 200 {
		t.Fatalf("visible microphone is not white: %v", pixel)
	}
}

func TestAwarenessDescriptionUsesReadableTypeWithoutCoveringControls(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	width := rightPane(960, 480).Dx()
	shortSize, shortBaseline, shortLineHeight, shortLines := notificationDescriptionLayout(faces, "A person was detected at the front door", width)
	if shortSize != 36 || len(shortLines) == 0 || len(shortLines) > 3 ||
		shortBaseline+(len(shortLines)-1)*shortLineHeight+faces.regular[shortSize].Metrics().Descent.Ceil() >= 396 {
		t.Fatalf("short awareness description size=%d lines=%d, want 36px in up to 3 lines", shortSize, len(shortLines))
	}
	long := strings.Repeat("The front door camera detected motion nearby. ", 5)
	size, baseline, lineHeight, lines := notificationDescriptionLayout(faces, long, width)
	if size != 30 || len(lines) != 4 || baseline+(len(lines)-1)*lineHeight+faces.regular[size].Metrics().Descent.Ceil() >= 396 {
		t.Fatalf("long awareness description size=%d lines=%d last baseline=%d", size, len(lines), baseline+(len(lines)-1)*lineHeight)
	}
	if !strings.HasSuffix(lines[len(lines)-1], "…") {
		t.Fatal("truncated awareness description has no ellipsis")
	}
}

func TestIntercomTouchStartsAndStopsOnlyInVisibleCorner(t *testing.T) {
	var commands bytes.Buffer
	client := &socketClient{writer: bufio.NewWriter(&commands)}
	r := &renderer{state: show.Snapshot{Connected: true}}
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: 24, Y: 452})
	if !r.intercomPressed {
		t.Fatal("touching the visible microphone did not press intercom")
	}
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: 24, Y: 452})
	if r.intercomPressed || !strings.Contains(commands.String(), `"action":"intercom.start"`) ||
		!strings.Contains(commands.String(), `"action":"intercom.stop"`) {
		t.Fatalf("intercom did not start and stop: %s", commands.String())
	}
	commands.Reset()
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: 200, Y: 452})
	if r.intercomPressed || commands.Len() != 0 {
		t.Fatal("touch outside the quarter circle activated intercom")
	}
	r.state.Muted = true
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: 24, Y: 452})
	if r.intercomPressed || commands.Len() != 0 {
		t.Fatal("muted microphone activated intercom")
	}
}

func TestTimerRemainingAndProgress(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	timer := &show.Timer{
		Active:             true,
		OriginalDurationMS: 120_000,
		DeadlineUnixMS:     now.Add(30 * time.Second).UnixMilli(),
	}
	if got := timerRemaining(timer, now); got != 30*time.Second {
		t.Fatalf("timerRemaining() = %s, want 30s", got)
	}
	if got := timerProgress(timer, now); got != .25 {
		t.Fatalf("timerProgress() = %v, want .25", got)
	}
	timer.Ringing = true
	if got := timerProgress(timer, now); got != 0 {
		t.Fatalf("ringing timerProgress() = %v, want 0", got)
	}
}

func TestTimerTouchMatchesExpandedButton(t *testing.T) {
	button := timerStopBounds(960, 480)
	pane := rightPane(960, 480)
	if pane.Min.X >= 480 || pane.Max.Y < 450 || button.Min.Y < 380 || button.Max.Y != pane.Max.Y {
		t.Fatalf("right-side content did not expand as expected: pane=%v button=%v", pane, button)
	}
	var commands bytes.Buffer
	client := &socketClient{writer: bufio.NewWriter(&commands)}
	r := &renderer{state: show.Snapshot{Connected: true, Timer: &show.Timer{Active: true}}}
	point := image.Pt((button.Min.X+button.Max.X)/2, (button.Min.Y+button.Max.Y)/2)
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: point.X, Y: point.Y})
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: point.X, Y: point.Y})
	if !strings.Contains(commands.String(), `"action":"timer.stop"`) {
		t.Fatalf("expanded timer button did not stop timer: %s", commands.String())
	}
}

func TestTranslucentRoundedRectKeepsItsColor(t *testing.T) {
	canvas := image.NewRGBA(image.Rect(0, 0, 60, 40))
	for index := 0; index < len(canvas.Pix); index += 4 {
		canvas.Pix[index], canvas.Pix[index+1], canvas.Pix[index+2], canvas.Pix[index+3] = 10, 10, 10, 255
	}
	roundedRect(canvas, image.Rect(5, 5, 55, 35), 12, color.RGBA{255, 147, 66, 48})
	center := canvas.RGBAAt(30, 20)
	if center.R < 50 || center.G < 30 || center.G > center.R || center.B >= center.G || center.A != 255 {
		t.Fatalf("translucent orange fill has incorrect color: %v", center)
	}
	if corner := canvas.RGBAAt(5, 5); corner.R != 10 || corner.G != 10 || corner.B != 10 {
		t.Fatalf("rounded corner was filled: %v", corner)
	}
}

func TestCheckersVoiceStagesDoNotDrawOrbs(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	r := &renderer{faces: faces, animationStart: now, state: show.Snapshot{Connected: true}}
	for _, phase := range []string{"listening", "thinking", "tool_call", "speaking", "intercom"} {
		r.state.Phase = phase
		accent := accentFor(phase)
		if phase == "speaking" {
			accent = color.RGBA{255, 132, 48, 255}
		}
		background := image.NewRGBA(image.Rect(0, 0, 960, 480))
		fillGradient(background, color.RGBA{6, 11, 19, 255}, mix(color.RGBA{6, 11, 19, 255}, accent, .13))

		r.state.Weather = &show.Weather{TemperatureText: "72°", Condition: "Sunny", ConditionKind: "sunny"}
		withWeather := image.NewRGBA(background.Rect)
		r.render(withWeather, now)
		if got, want := withWeather.RGBAAt(480, 434), background.RGBAAt(480, 434); got != want {
			t.Errorf("%s drew a compact voice orb over the weather: got=%v background=%v", phase, got, want)
		}

		r.state.Weather = nil
		withoutWeather := image.NewRGBA(background.Rect)
		r.render(withoutWeather, now)
		if got, want := withoutWeather.RGBAAt(678, 340), background.RGBAAt(678, 340); got != want {
			t.Errorf("%s drew the fallback voice orb: got=%v background=%v", phase, got, want)
		}
	}
}

func TestCheckersReplyRimFollowsAudioWithoutCoveringWeather(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	r := &renderer{faces: faces, animationStart: now, state: show.Snapshot{
		Phase: "speaking", Connected: true,
		Weather: &show.Weather{TemperatureText: "72°", Condition: "Sunny", ConditionKind: "sunny"},
	}}
	quiet := image.NewRGBA(image.Rect(0, 0, 960, 480))
	r.render(quiet, now)
	r.state.AudioLevel = .7
	loud := image.NewRGBA(quiet.Rect)
	for frame := 1; frame <= 5; frame++ {
		r.render(loud, now.Add(time.Duration(frame)*33*time.Millisecond))
	}
	for _, point := range []image.Point{{X: 480, Y: 3}, {X: 3, Y: 240}} {
		low, high := quiet.RGBAAt(point.X, point.Y), loud.RGBAAt(point.X, point.Y)
		if high.R <= low.R+60 || high.G <= low.G+30 {
			t.Errorf("reply rim did not brighten with audio at %v: quiet=%v loud=%v", point, low, high)
		}
	}
	if quiet.RGBAAt(480, 434) != loud.RGBAAt(480, 434) {
		t.Fatal("reply audio changed the former compact-orb area")
	}
}

func TestToolCallKeepsWeatherCardAndHasNoSpinningFallback(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	weather := &show.Weather{TemperatureText: "72°", Condition: "Partly cloudy", ConditionKind: "partly"}
	r := &renderer{faces: faces, state: show.Snapshot{Phase: "idle", Weather: weather}}
	idle := image.NewRGBA(image.Rect(0, 0, 960, 480))
	r.drawRight(idle, now, 960, 480, 2, color.RGBA{255, 147, 66, 255})
	r.state.Phase = "tool_call"
	r.state.ToolName = "home_assistant"
	working := image.NewRGBA(idle.Rect)
	r.drawRight(working, now, 960, 480, 2, color.RGBA{255, 147, 66, 255})
	if bytes.Equal(idle.Pix, working.Pix) {
		t.Fatal("tool status did not update the weather heading")
	}
	if !bytes.Equal(idle.Pix[65*idle.Stride:], working.Pix[65*working.Stride:]) {
		t.Fatal("tool call replaced more than the weather heading")
	}

	r.state.Weather = nil
	first := image.NewRGBA(idle.Rect)
	second := image.NewRGBA(idle.Rect)
	r.drawRight(first, now, 960, 480, 2, color.RGBA{255, 147, 66, 255})
	r.drawRight(second, now, 960, 480, 9, color.RGBA{255, 147, 66, 255})
	if !bytes.Equal(first.Pix, second.Pix) {
		t.Fatal("tool status fallback is still animated")
	}
}

func TestConnectedStatusUsesAssistantFirstName(t *testing.T) {
	if got := connectedStatus("Jarvis"); got != "Jarvis Connected" {
		t.Fatalf("connected status = %q", got)
	}
	if got := connectedStatus("  "); got != "Tater Connected" {
		t.Fatalf("empty assistant name fallback = %q", got)
	}
}

func TestWeatherIconMovesAcrossFrames(t *testing.T) {
	for _, kind := range []string{"sun", "partly", "cloud", "rain", "storm", "snow", "fog", "wind"} {
		first := image.NewRGBA(image.Rect(0, 0, 120, 120))
		second := image.NewRGBA(image.Rect(0, 0, 120, 120))
		r := &renderer{}
		r.drawWeatherIcon(first, 60, 48, 35, kind, 0, weatherColor(kind))
		r.drawWeatherIcon(second, 60, 48, 35, kind, 1.3, weatherColor(kind))
		if sha256.Sum256(first.Pix) == sha256.Sum256(second.Pix) {
			t.Errorf("%s icon did not animate", kind)
		}
	}
}

func TestRepresentativeStatesRenderDistinctFrames(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	direction := 55.0
	base := show.Snapshot{
		Phase:           "idle",
		Connected:       true,
		DeviceName:      "Tater Checkers",
		Room:            "Family Room",
		Message:         "Ready when you are",
		VolumePercent:   45,
		TaterTimeUnixMS: now.UnixMilli(),
	}
	cases := map[string]show.Snapshot{
		"idle": base,
		"voice": func() show.Snapshot {
			state := base
			state.Phase = "listening"
			state.AudioLevel = .72
			state.DirectionDegrees = &direction
			return state
		}(),
		"tool": func() show.Snapshot {
			state := base
			state.Phase = "tool_call"
			state.ToolName = "home_assistant"
			state.ToolMessage = "Checking the lights"
			return state
		}(),
		"weather": func() show.Snapshot {
			state := base
			state.Weather = &show.Weather{TemperatureText: "72°", TemperatureUnit: "F", Condition: "Partly cloudy", ConditionKind: "cloudy", FeelsLikeText: "Feels like 74°", FeelsLikeRelation: "warmer", HumidityText: "44%", WindText: "8 mph", RainText: "10%", IndoorTemperatureText: "71°", IndoorHumidityText: "42%"}
			return state
		}(),
		"weather-voice": func() show.Snapshot {
			state := base
			state.Phase = "speaking"
			state.AudioLevel = .68
			state.Weather = &show.Weather{TemperatureText: "72°", ConditionKind: "rain", Condition: "Rain showers"}
			return state
		}(),
		"timer": func() show.Snapshot {
			state := base
			state.TimerActive = true
			state.Timer = &show.Timer{Active: true, ID: "dinner", Label: "Dinner", OriginalDurationMS: 600_000, DeadlineUnixMS: now.Add(4 * time.Minute).UnixMilli()}
			return state
		}(),
		"notification": func() show.Snapshot {
			state := base
			state.Notification = &show.Notification{ID: "door", CameraName: "Front Door", Description: "A person was detected at the front door", Priority: "critical", ExpiresAtUnixMS: now.Add(time.Minute).UnixMilli()}
			return state
		}(),
		"setup": func() show.Snapshot {
			state := base
			state.Phase = "setup"
			state.Connected = false
			return state
		}(),
		"setup-hotspot": {Phase: "setup", Connected: false, DeviceName: "Tater Checkers", Room: "Tater-Setup-D4B7", Message: "192.168.4.1"},
		"connecting":    {Phase: "offline", Message: "Connecting to Tater", DeviceName: "Tater Checkers"},
	}
	seen := map[string]string{}
	for name, state := range cases {
		canvas := image.NewRGBA(image.Rect(0, 0, 960, 480))
		renderer := &renderer{faces: faces, state: state, received: now, animationStart: now.Add(-2 * time.Second)}
		renderer.render(canvas, now)
		digest := fmt.Sprintf("%x", sha256.Sum256(canvas.Pix))
		if previous, duplicate := seen[digest]; duplicate {
			t.Errorf("%s and %s produced the same frame", name, previous)
		}
		seen[digest] = name
		if canvas.RGBAAt(0, 0).A != 255 || canvas.RGBAAt(959, 479).A != 255 {
			t.Errorf("%s did not paint an opaque full-screen frame", name)
		}
	}
}

func TestConnectingScreenUsesPrimaryTaterLogo(t *testing.T) {
	const primaryLogoSHA256 = "c825e118c81171a4175a9870877131f09a148024d56aa1d45ad9a315bdcddb04"
	if got := fmt.Sprintf("%x", sha256.Sum256(taterPrimaryLogoPNG)); got != primaryLogoSHA256 {
		t.Fatalf("embedded logo is not the main Tater app's primary transparent asset: %s", got)
	}
	if _, _, _, alpha := taterPrimaryLogo.At(0, 0).RGBA(); alpha != 0 {
		t.Fatal("Tater logo lost its transparent background")
	}
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	first := image.NewRGBA(image.Rect(0, 0, 960, 480))
	second := image.NewRGBA(first.Bounds())
	now := time.Unix(1_800_000_000, 0)
	r := &renderer{faces: faces, state: show.Snapshot{Phase: "connecting"}, animationStart: now}
	r.render(first, now)
	background := r.connectingBase
	r.render(second, now.Add(600*time.Millisecond))
	if r.connectingBase != background {
		t.Fatal("connecting logo and glow were rebuilt every animation frame")
	}
	if sha256.Sum256(first.Pix) == sha256.Sum256(second.Pix) {
		t.Fatal("connection indicator stopped animating")
	}
	if corner := first.RGBAAt(0, 0); corner.R > 35 || corner.G > 20 || corner.B > 20 || corner.A != 255 {
		t.Fatalf("connecting screen is not an opaque near-black background: %v", corner)
	}
	bright := 0
	for y := 40; y < 400; y += 8 {
		for x := 110; x < 850; x += 8 {
			pixel := first.RGBAAt(x, y)
			if pixel.R > 190 && pixel.G > 170 && pixel.B > 140 {
				bright++
			}
		}
	}
	if bright < 100 {
		t.Fatalf("main Tater logo is missing or too small on the boot-style screen (%d bright samples)", bright)
	}
}

// Set TATER_SHOW_PREVIEW to inspect one representative composed screen without
// opening the hardware framebuffer. Ordinary test runs skip this artifact.
func TestRenderPreview(t *testing.T) {
	path := os.Getenv("TATER_SHOW_PREVIEW")
	if path == "" {
		t.Skip("set TATER_SHOW_PREVIEW to render a screenshot")
	}
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	state := show.Snapshot{
		Phase:           "speaking",
		Connected:       true,
		DeviceName:      "Tater Checkers",
		Room:            "Family Room",
		Message:         "Here is the forecast",
		AudioLevel:      .68,
		TaterTimeUnixMS: now.UnixMilli(),
		Weather: &show.Weather{
			TemperatureText: "72°", TemperatureUnit: "F", Condition: "Rain showers",
			ConditionKind: "rain", FeelsLikeText: "Feels like 69°", FeelsLikeRelation: "cooler",
			HumidityText: "68%", WindText: "8 mph", RainText: "65%",
			IndoorTemperatureText: "71°", IndoorHumidityText: "42%",
		},
	}
	switch os.Getenv("TATER_SHOW_PREVIEW_STATE") {
	case "timer":
		state.Weather = nil
		state.TimerActive = true
		state.Timer = &show.Timer{Active: true, ID: "dinner", Label: "Dinner", OriginalDurationMS: 600_000, DeadlineUnixMS: now.Add(4 * time.Minute).UnixMilli()}
		state.Message = "Your dinner timer is running"
	case "notification":
		state.Weather = nil
		state.Notification = &show.Notification{ID: "door", CameraName: "Front Door", Description: "A person was detected at the front door. Check the camera or ask Tater what happened.", Priority: "critical", ExpiresAtUnixMS: now.Add(time.Minute).UnixMilli()}
		state.Message = "Someone is at the door"
	case "setup-hotspot":
		state = show.Snapshot{Phase: "setup", Connected: false, DeviceName: "Tater Checkers", Room: "Tater-Setup-D4B7", Message: "192.168.4.1"}
	case "connecting":
		state = show.Snapshot{Phase: "connecting", Connected: false, DeviceName: "Tater Checkers", Message: "Connecting to Tater"}
	}
	canvas := image.NewRGBA(image.Rect(0, 0, 960, 480))
	r := &renderer{faces: faces, state: state, received: now, animationStart: now.Add(-2 * time.Second)}
	r.render(canvas, now)
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, canvas); err != nil {
		t.Fatal(err)
	}
}
