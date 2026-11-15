//go:build linux

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
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

func TestDeviceInfoGraphicsRendererLabels(t *testing.T) {
	for _, test := range []struct {
		gpu  bool
		want string
	}{{false, "Software"}, {true, "GPU"}} {
		t.Run(test.want, func(t *testing.T) {
			r := &renderer{settingsPage: "info", gpuAvailable: test.gpu}
			_, rows := r.settingRows()
			for _, row := range rows {
				if row.label == "Graphics" {
					if row.options != test.want {
						t.Fatalf("Graphics = %q, want %q", row.options, test.want)
					}
					return
				}
			}
			t.Fatal("Device Info is missing its Graphics row")
		})
	}
}

func TestWorkspaceSettingsStayLocalAndSlidersCommitOnlyOnRelease(t *testing.T) {
	r, _, _ := gpuTestRenderer(t)
	r.settingsOpen, r.settingsPage = true, "sound"
	var wire bytes.Buffer
	client := &socketClient{writer: bufio.NewWriter(&wire)}
	bounds := settingsRowBounds(960, 480, 0, false)
	r.handleSettingsTouch(client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: bounds.Min.X + 100, Y: bounds.Max.Y - 15}, 960, 480)
	r.handleSettingsTouch(client, linuxinput.TouchEvent{Kind: linuxinput.Move, X: bounds.Max.X - 22, Y: bounds.Max.Y - 15}, 960, 480)
	if wire.Len() != 0 || settingNumber(r.settingValue("volume_percent")) != 100 {
		t.Fatal("slider either sent premature settings or failed to preview")
	}
	r.handleSettingsTouch(client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: bounds.Max.X - 22, Y: bounds.Max.Y - 15}, 960, 480)
	var command show.Command
	if err := json.Unmarshal(bytes.TrimSpace(wire.Bytes()), &command); err != nil {
		t.Fatal(err)
	}
	if command.Action != "screen.settings.set" || command.Settings["volume_percent"] != float64(100) {
		t.Fatalf("wrong local request: %#v", command)
	}
	if _, ok := command.Settings["wake_threshold"]; ok {
		t.Fatal("slider leaked wake settings")
	}
	r.state.Conversation = &show.Conversation{TurnID: "test", Phase: "listening"}
	if r.settingsVisible(time.Now()) {
		t.Fatal("hidden settings can receive input through voice UI")
	}
}

func TestWorkspaceSettingsPagesFitAndHaveSafeRoundHitTargets(t *testing.T) {
	r, _, _ := gpuTestRenderer(t)
	for _, page := range []string{"sound", "display", "appearance", "night", "dnd", "equalizer", "info"} {
		r.settingsPage = page
		_, rows := r.settingRows()
		if len(rows) == 0 {
			t.Fatalf("empty %s page", page)
		}
		for index := 0; index < 3 && index < len(rows); index++ {
			box := settingsRowBounds(480, 480, index, true)
			for _, point := range []image.Point{box.Min, box.Max.Sub(image.Pt(1, 1))} {
				dx, dy := point.X-240, point.Y-240
				if dx*dx+dy*dy > 236*236 {
					t.Fatalf("%s row falls outside round screen", page)
				}
			}
		}
	}
}

func TestWorkspaceSoundMenuKeepsSharedVoiceControlsHostManaged(t *testing.T) {
	for _, target := range []string{"checkers", "crown", "rook"} {
		t.Run(target, func(t *testing.T) {
			r, _, _ := gpuTestRenderer(t)
			r.target, r.spotScreen = target, target == "rook"
			if target == "crown" {
				r.screenWidth, r.screenHeight = 1280, 800
			}
			if target == "rook" {
				r.screenWidth, r.screenHeight = 480, 480
			}
			r.settingsOpen, r.settingsPage = true, "sound"
			_, rows := r.settingRows()
			want := []string{"volume_percent", "display_edge_glow", "equalizer", "dnd"}
			if len(rows) != len(want) {
				t.Fatalf("unexpected sound menu: %#v", rows)
			}
			for index, row := range rows {
				if firstNonEmpty(row.key, row.target) != want[index] {
					t.Fatalf("unexpected sound row %d: %#v", index, row)
				}
			}
			for _, page := range []string{"sound", "display", "appearance", "night", "dnd", "equalizer", "info"} {
				r.settingsPage = page
				_, rows := r.settingRows()
				for _, row := range rows {
					switch row.key {
					case "wake_sound_enabled", "wake_sound", "wake_sound_url", "continued_chat", "barge_in_enabled":
						t.Fatalf("%s exposes host-managed %s", page, row.key)
					}
					if row.target == "voice" {
						t.Fatal("sound menu still links to removed voice options")
					}
				}
			}
			width, height := r.showSize()
			count := 5
			if r.spotScreen {
				count = 3
			}
			var wire bytes.Buffer
			client := &socketClient{writer: bufio.NewWriter(&wire)}
			for index, page := range map[int]string{2: "equalizer", 3: "dnd"} {
				r.settingsPage, r.settingsIndex = "sound", index/count
				box := settingsRowBounds(width, height, index%count, r.spotScreen)
				if r.spotScreen {
					r.settingsIndex = 0
					_, controls, _ := r.rookSettingControls()
					box = controls[index].bounds
				}
				r.handleSettingsTouch(client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: box.Min.X + box.Dx()/2, Y: box.Min.Y + box.Dy()/2}, width, height)
				if r.settingsPage != page || r.settingsIndex != 0 {
					t.Fatalf("sound link opened %q instead of %q", r.settingsPage, page)
				}
				back := settingsBackBounds(r.spotScreen)
				r.handleSettingsTouch(client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: back.Min.X + back.Dx()/2, Y: back.Min.Y + back.Dy()/2}, width, height)
				if r.settingsPage != "sound" || !r.settingsOpen || wire.Len() != 0 {
					t.Fatal("sound submenu back navigation changed settings or closed the menu")
				}
			}
		})
	}
}

func TestGPUProgressUsesSmallRetainedAtlasAndNoArtworkRefresh(t *testing.T) {
	r, canvas, now := gpuTestRenderer(t)
	r.state.Media = &show.Media{Active: true, Title: "Evening Signals", Artist: "Northbound", ProgressMS: 10000, DurationMS: 180000, PlaybackSpeed: 1000, ProgressUpdatedAtUnixMS: now.UnixMilli(), Spectrum: []float64{.2, .8}}
	first, ok := r.prepareGPUScene(canvas, now, true, true)
	if !ok || len(first.source) == 0 || first.effects.Text == nil || len(first.effects.Text.Pixels) == 0 {
		t.Fatal("music did not retain its artwork and overlay")
	}
	next, _ := r.prepareGPUScene(canvas, now.Add(33*time.Millisecond), true, true)
	if len(next.source) != 0 || len(next.effects.Text.Pixels) != 0 || len(next.effects.Text.Quads) < 24 {
		t.Fatal("spectrum animation retransmitted static pixels")
	}
	second, _ := r.prepareGPUScene(canvas, now.Add(1100*time.Millisecond), true, true)
	if len(second.source) != 0 || len(second.effects.Text.Pixels) > 1024*128*4 {
		t.Fatal("time change repainted full artwork")
	}
	r.state.Media.ProgressMS += 1000
	refresh, _ := r.prepareGPUScene(canvas, now.Add(1200*time.Millisecond), true, true)
	if len(refresh.source) != 0 {
		t.Fatal("ordinary media refresh restarted the scene")
	}
}

func TestGPUMediaLeaseRefreshAndCriticalNotificationPriority(t *testing.T) {
	r, canvas, now := gpuTestRenderer(t)
	r.state.TaterTube = &show.TaterTubeMedia{Active: true, EventID: "movie", Title: "Film", DurationMS: 100000, ProgressMS: 1000, ExpiresAtUnixMS: now.Add(time.Hour).UnixMilli()}
	r.prepareGPUScene(canvas, now, true, true)
	r.state.TaterTube.Revision++
	r.state.TaterTube.ExpiresAtUnixMS += 5000
	next, _ := r.prepareGPUScene(canvas, now.Add(33*time.Millisecond), true, true)
	if len(next.source) != 0 {
		t.Fatal("lease renewal restarted media artwork")
	}
	r.state.Notification = &show.Notification{ID: "critical", Priority: "critical", Title: "Warning"}
	critical, ok := r.prepareGPUScene(canvas, now.Add(time.Second), true, true)
	if !ok || critical.effects.Blend != 1 {
		t.Fatal("critical event waited for a decorative fade")
	}
}

func TestGPUCompotatoSubsetDefaultsFadeAndSlidesOnGPU(t *testing.T) {
	r, canvas, now := gpuTestRenderer(t)
	r.state.Settings = show.ScreenSettingDefaults()
	r.state.Settings["screen_popup_effect"] = "slide"
	r.prepareGPUScene(canvas, now, true, true)
	r.menuOpen = true
	start, _ := r.prepareGPUScene(canvas, now.Add(time.Second), true, true)
	if start.effects.Text == nil || start.effects.Text.Quads[0].Y <= 0 || len(start.effects.Text.Pixels) == 0 {
		t.Fatal("slide did not use the GPU's retained atlas")
	}
	middle, _ := r.prepareGPUScene(canvas, now.Add(1110*time.Millisecond), true, true)
	if len(middle.source) != 0 || len(middle.effects.Text.Pixels) != 0 || middle.effects.Text.Quads[0].Y >= 28 {
		t.Fatal("slide retransmitted the canvas")
	}
	end, _ := r.prepareGPUScene(canvas, now.Add(1250*time.Millisecond), true, true)
	if len(end.source) == 0 || end.effects.Text != nil {
		t.Fatal("slide did not settle into the static retained scene")
	}
	r.state.Settings["screen_popup_effect"] = "disabled"
	r.menuOpen = false
	off, _ := r.prepareGPUScene(canvas, now.Add(2*time.Second), true, true)
	if off.effects.Blend != 1 {
		t.Fatal("Off still animated")
	}
}

func TestSwipeDoesNotBecomeABroadcastHold(t *testing.T) {
	r, _, now := gpuTestRenderer(t)
	var wire bytes.Buffer
	client := &socketClient{writer: bufio.NewWriter(&wire)}
	handleQueuedTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: 600, Y: 100})
	handleQueuedTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Move, X: 600, Y: 250})
	r.satChatTouchAt = now.Add(-time.Second)
	r.updateIntercomHold(client, now)
	if r.intercomPressed || strings.Contains(wire.String(), "intercom.start") {
		t.Fatal("swipe started microphone broadcast")
	}
	handleQueuedTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: 600, Y: 250})
	if r.menuOpen {
		t.Fatal("swipe opened the menu")
	}
}

func TestWorkspaceMenuOpensOnlyOnTap(t *testing.T) {
	for _, target := range []string{"checkers", "crown", "rook"} {
		for _, movesDelivered := range []bool{false, true} {
			r, _, _ := gpuTestRenderer(t)
			r.target, r.spotScreen = target, target == "rook"
			var wire bytes.Buffer
			client := &socketClient{writer: bufio.NewWriter(&wire)}
			handleQueuedTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: 240, Y: 170})
			if movesDelivered {
				handleQueuedTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Move, X: 240, Y: 300})
			}
			handleQueuedTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: 240, Y: 300})
			if r.menuOpen || r.intercomPressed {
				t.Fatalf("%s opened menu/broadcast for a drag (Move delivered: %t)", target, movesDelivered)
			}
			handleQueuedTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: 240, Y: 170})
			handleQueuedTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: 243, Y: 173})
			if !r.menuOpen {
				t.Fatalf("%s ignored a stationary tap", target)
			}
		}
	}
}

func TestWorkspaceHomeReadingsStayOrderedAndOptional(t *testing.T) {
	weather := &show.Weather{TemperatureText: "68°", Condition: "Sunny", FeelsLikeText: "Feels like 66°", IndoorTemperatureText: "72°", IndoorHumidityText: "42%", HumidityText: "55%", WindText: "NW 5 mph", RainText: "0.02 in", LightningText: "0"}
	items := workspaceHomeReadings(weather)
	want := []string{"Sunny", "Feels like 66°", "Room humidity 42%", "Humidity 55%", "Wind NW 5 mph", "Rain 0.02 in", "Lightning 0"}
	if strings.Join(items, "|") != strings.Join(want, "|") {
		t.Fatalf("unexpected bottom readings: %v", items)
	}
	if strings.Contains(strings.Join(items, "|"), "72°") || strings.Contains(strings.Join(items, "|"), "RH") {
		t.Fatal("duplicated room temperature or abbreviated room humidity")
	}
	if len(workspaceHomeReadings(nil)) != 0 || len(workspaceHomeReadings(&show.Weather{})) != 0 {
		t.Fatal("disabled/absent readings created placeholders")
	}
	r, _, _ := gpuTestRenderer(t)
	for _, dimensions := range []image.Point{image.Pt(960, 480), image.Pt(1280, 800)} {
		lines, size := r.workspaceHomeFooterLines(weather, dimensions.X-108, dimensions.Y)
		if len(lines) > 2 || len(lines) == 0 {
			t.Fatalf("ordinary enabled readings do not fit: %v", lines)
		}
		for _, line := range lines {
			if textWidth(r.faces.regular[size], line) > dimensions.X-108 {
				t.Fatal("bottom strip escapes screen margins")
			}
		}
		for _, item := range want {
			if !strings.Contains(strings.Join(lines, "|"), item) {
				t.Fatalf("enabled reading was dropped: %s", item)
			}
		}
	}
}

func TestWorkspaceWeatherUsesTransparentOutlineAndThemeAccent(t *testing.T) {
	for _, kind := range []string{"sun", "moon", "partly", "partly-night", "cloud", "rain", "storm", "snow", "fog", "wind"} {
		art := image.NewRGBA(image.Rect(0, 0, 100, 100))
		drawWorkspaceWeatherGlyph(art, art.Bounds(), kind, color.RGBA{232, 123, 64, 255})
		pixels, accented := 0, 0
		for y := 0; y < 100; y++ {
			for x := 0; x < 100; x++ {
				if art.RGBAAt(x, y).A > 0 {
					pixels++
				}
				ink := art.RGBAAt(x, y)
				if int(ink.R) > int(ink.G)+20 && int(ink.G) > int(ink.B)+20 {
					accented++
				}
			}
		}
		if pixels < 80 || pixels > 3500 {
			t.Fatalf("%s is empty or is a filled/shaded icon: %d pixels", kind, pixels)
		}
		if art.RGBAAt(0, 0).A != 0 || art.RGBAAt(99, 99).A != 0 {
			t.Fatalf("%s adds an opaque icon box", kind)
		}
		if kind == "cloud" && art.RGBAAt(50, 55).A != 0 {
			t.Fatal("cloud interior is not transparent over the wallpaper")
		}
		if (kind == "sun" || kind == "partly" || kind == "storm") && accented < 20 {
			t.Fatalf("%s lost its theme accent behind the cloud mask", kind)
		}
	}
}

func TestWorkspaceMediaHasOneAlignedProgressTrack(t *testing.T) {
	for _, target := range []string{"checkers", "crown"} {
		t.Run(target, func(t *testing.T) {
			r, _, now := gpuTestRenderer(t)
			r.target = target
			width, height := 960, 480
			if target == "crown" {
				width, height = 1280, 800
			}
			r.state.TaterTube = &show.TaterTubeMedia{Active: true, Title: "Film", DurationMS: 100000, ProgressMS: 25000}
			canvas := image.NewRGBA(image.Rect(0, 0, width, height))
			r.drawWorkspaceTaterTube(canvas, now, true)
			_, content := r.taterTubeLayout(width, height)
			barY, thickness, _, _ := r.taterTubeProgressGeometry(content)
			runs, inside := 0, false
			for y := barY - 16; y <= barY+24; y++ {
				track := canvas.RGBAAt(content.Max.X-15, y) == workspaceBorder
				if track && !inside {
					runs++
				}
				inside = track
			}
			if runs != 1 || canvas.RGBAAt(content.Max.X-15, barY) != workspaceBorder {
				t.Fatalf("expected one progress track at %d with thickness %d; found %d", barY, thickness, runs)
			}
		})
	}
}

func TestWorkspaceOfflinePreviews(t *testing.T) {
	directory := os.Getenv("TATER_WORKSPACE_PREVIEW_DIR")
	if directory == "" {
		t.Skip("set TATER_WORKSPACE_PREVIEW_DIR for software-rendered UI previews")
	}
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"checkers", "crown", "rook"} {
		width, height := 960, 480
		if target == "crown" {
			width, height = 1280, 800
		}
		if target == "rook" {
			width, height = 480, 480
		}
		for _, view := range []string{"home", "home-extras", "menu", "sound", "display", "appearance", "equalizer", "notification", "music", "tube", "sat-chat", "sat-chat-video", "cameras", "camera-view", "camera-view-only", "climate"} {
			r, _, now := gpuTestRenderer(t)
			now = time.Date(2026, time.November, 12, 9, 41, 0, 0, time.FixedZone("CST", -6*60*60))
			r.animationStart = now
			r.target = target
			r.spotScreen = target == "rook"
			r.screenWidth, r.screenHeight = width, height
			r.state.Room = "Living Room"
			r.state.AssistantName = "Tater"
			r.state.Settings = show.ScreenSettingDefaults()
			r.state.VolumePercent = 65
			r.state.Weather = &show.Weather{TemperatureText: "68°", Condition: "Partly cloudy", ConditionKind: "partly", FeelsLikeText: "Feels like 66°"}
			art, _, err := readDefaultWallpaper(target, 7)
			if err != nil {
				t.Fatal(err)
			}
			r.state.WallpaperURLs = []string{"preview"}
			r.wallpaperID = "preview"
			r.wallpaperImage = prepareCoverImage(art, image.Pt(width, height))
			switch view {
			case "sat-chat":
				r.satChatOpen = true
				r.state.SatChat = &show.SatChat{Participants: []show.SatChatParticipant{
					{ID: "kitchen", Name: "Kitchen Show", Room: "Kitchen", Audio: true, Video: true},
					{ID: "office", Name: "Office Rook", Room: "Office", Audio: true, Video: true},
					{ID: "bedroom", Name: "Bedroom Biscuit", Room: "Bedroom", Audio: true, Busy: true},
					{ID: "four", Name: "Dining Room", Audio: true}, {ID: "five", Name: "Studio", Audio: true}, {ID: "six", Name: "Hallway", Audio: true},
				}}
			case "sat-chat-video":
				r.state.SatChat = &show.SatChat{Self: "preview", Call: &show.SatChatCall{ID: "example-call", State: "active", Mode: "video", TargetName: "Kitchen Show"}}
				r.satChatVideoImage = prepareContainImage(prepareCoverImage(art, image.Pt(640, 360)), r.satChatVideoBounds().Size())
				r.satChatVideoShown = "preview"
			case "cameras":
				r.cameraOpen = true
				r.state.Cameras = &show.Cameras{Available: true, Items: []show.Camera{
					{ID: "front", Name: "Front door", Room: "Porch", Doorbell: true, Talkback: true, Available: true},
					{ID: "driveway", Name: "Driveway", Room: "Outside", Available: true},
					{ID: "nursery", Name: "Nursery", Room: "Nursery"},
				}}
			case "camera-view", "camera-view-only":
				r.cameraOpen = true
				r.state.Cameras = &show.Cameras{Available: true, SelectedID: "preview", SelectedName: "Camera preview", SelectedRoom: "Example landscape", SelectedTalkback: view == "camera-view"}
				// Bundled landscape is a synthetic camera fixture, not a private feed.
				r.cameraFrameImage = prepareContainImage(prepareCoverImage(art, image.Pt(640, 360)), r.cameraVideoBounds().Size())
				r.cameraFrameShown = "preview"
			case "climate":
				r.climateOpen = true
				current, target, humidity := 70.0, 72.0, 42.0
				r.state.Climate = &show.Climate{Available: true, RoomID: "living", RoomName: "Living Room", CurrentTemperature: &current, TargetTemperature: &target, CurrentHumidity: &humidity, TemperatureUnit: "F", CanSetTemperature: true, CanSetMode: true, HVACMode: "heat", HVACAction: "Heating", MinimumTemperature: 45, MaximumTemperature: 90, TemperatureStep: 1, AvailableHVACModes: []string{"heat", "cool", "auto", "off"}, RoomTemperatures: []show.RoomTemperature{{RoomName: "Living Room", Temperature: 70}, {RoomName: "Kitchen", Temperature: 69}, {RoomName: "Bedroom", Temperature: 68}}}
			case "home-extras":
				r.state.Weather.IndoorTemperatureText = "72°"
				r.state.Weather.IndoorHumidityText = "42%"
				r.state.Weather.HumidityText = "55%"
				r.state.Weather.WindText = "NW 5 mph"
				r.state.Weather.RainText = "0.02 in"
				r.state.Weather.LightningText = "0"
			case "menu":
				r.menuOpen = true
			case "sound", "display", "appearance", "equalizer":
				r.settingsOpen = true
				r.settingsPage = view
			case "notification":
				r.state.Notification = &show.Notification{ID: "test", Title: "Garage door is open", Label: "Home notification", Description: "The garage door has been open for 10 minutes.", Source: "Home Assistant", CreatedAtUnixMS: now.UnixMilli(), ExpiresAtUnixMS: now.Add(15 * time.Second).UnixMilli(), TTLMS: 15000}
			case "music":
				r.mediaImage = art
				r.state.Media = &show.Media{Active: true, Title: "Evening Signals", Artist: "Northbound", Album: "Quiet Hours", GroupName: "Living Room", ProgressMS: 134000, DurationMS: 248000, Spectrum: []float64{.2, .4, .8, .5}}
			case "tube":
				r.taterTubeImage = art
				r.state.TaterTube = &show.TaterTubeMedia{Active: true, EventID: "test", SeriesTitle: "The Last Horizon", Title: "Into the Storm", Season: 2, Episode: 4, PlayerName: "Family Room TV", ProgressMS: 1397000, DurationMS: 3242000}
			}
			canvas := image.NewRGBA(image.Rect(0, 0, width, height))
			r.render(canvas, now)
			file, err := os.Create(filepath.Join(directory, target+"-"+strings.ReplaceAll(view, "/", "-")+".png"))
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(file, canvas)
			file.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}
