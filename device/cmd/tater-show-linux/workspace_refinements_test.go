//go:build linux

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"strings"
	"testing"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/linuxinput"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

func TestWorkspaceHomeHeadingUsesAssistantStatusAndUnlabelledRoomTemperature(t *testing.T) {
	r, canvas, _ := gpuTestRenderer(t)
	r.state.AssistantName, r.state.Room = "Potato Friend", "Office"
	r.state.Weather = &show.Weather{TemperatureText: "68°", Condition: "Sunny", FeelsLikeText: "Feels like 66°", IndoorTemperatureText: "72°"}
	heading := r.workspaceHomeHeadingLayout(960, 36, 22)
	if heading.assistant != "Potato" || heading.room != "Office" || heading.temperature != "72°" {
		t.Fatalf("incorrect home heading: %+v", heading)
	}
	if heading.dot.X <= heading.assistantAt.X || heading.dot.X >= heading.roomAt.X || heading.temperatureAt.X <= heading.roomAt.X+textWidth(r.faces.regular[22], heading.room) {
		t.Fatal("assistant, status dot, room and temperature are not ordered")
	}
	r.drawWorkspaceHomeHeading(canvas, 36, 22)
	if got := canvas.RGBAAt(heading.dot.X, heading.dot.Y); got.G <= got.R || got.G <= got.B {
		t.Fatalf("connected idle dot is not green: %v", got)
	}
	readings := strings.Join(workspaceHomeReadings(r.state.Weather), "|")
	if readings != "Sunny|Feels like 66°" || strings.Contains(readings, "72°") {
		t.Fatalf("conditions/temperature incorrectly placed: %s", readings)
	}
	r.state.Muted = true
	if got := r.workspaceHomeHeadingLayout(960, 36, 22).ink; got.R <= got.G {
		t.Fatal("muted status still appears online-green")
	}
	r.state.AssistantName, r.state.Room = strings.Repeat("VeryLong", 40), strings.Repeat("Room ", 40)
	for _, width := range []int{960, 1280} {
		h := r.workspaceHomeHeadingLayout(width, 54, 30)
		if h.temperatureAt.X+textWidth(r.faces.regular[30], h.temperature) > width-54 {
			t.Fatal("long assistant/room labels pushed the temperature outside the screen")
		}
	}
	r.state.Weather = nil
	if r.workspaceHomeHeadingLayout(960, 36, 22).temperature != "" {
		t.Fatal("missing sensor created a placeholder")
	}
}

func TestWorkspaceVerticalEqualizerAllBandsFitAndCommitIndependently(t *testing.T) {
	for _, size := range []image.Point{image.Pt(960, 480), image.Pt(1280, 800)} {
		for band := 0; band < 8; band++ {
			t.Run(fmt.Sprintf("%dx%d/band%d", size.X, size.Y, band), func(t *testing.T) {
				r, _, _ := gpuTestRenderer(t)
				r.screenWidth, r.screenHeight = size.X, size.Y
				r.settingsOpen, r.settingsPage = true, "equalizer"
				r.state.Settings = show.ScreenSettingDefaults()
				r.state.Settings["eq_bands"] = []float64{0, 1, 2, 3, 4, 5, 6, 7}
				cell, track := equalizerBandBounds(size.X, size.Y, band), equalizerTrackBounds(size.X, size.Y, band)
				if !cell.In(image.Rectangle{Max: size}) || cell.Dx() < 44 || !track.In(cell) || cell.Overlaps(equalizerPresenceBounds(size.X, size.Y)) {
					t.Fatal("equalizer band is not a separate, visible touch target")
				}
				var wire bytes.Buffer
				client := &socketClient{writer: bufio.NewWriter(&wire)}
				x := cell.Min.X + cell.Dx()/2
				r.handleSettingsTouch(client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: x, Y: track.Min.Y}, size.X, size.Y)
				key := fmt.Sprintf("eq_band_%d", band)
				if settingFloat(r.settingValue(key)) != 12 {
					t.Fatal("top of vertical fader is not +12 dB")
				}
				// Leave the original column during the drag: ownership must not jump.
				r.handleSettingsTouch(client, linuxinput.TouchEvent{Kind: linuxinput.Move, X: 0, Y: track.Max.Y + 200}, size.X, size.Y)
				if wire.Len() != 0 || settingFloat(r.settingValue(key)) != -12 {
					t.Fatal("drag sent early or did not clamp to -12 dB")
				}
				r.handleSettingsTouch(client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: 0, Y: track.Max.Y + 200}, size.X, size.Y)
				var command show.Command
				if err := json.Unmarshal(bytes.TrimSpace(wire.Bytes()), &command); err != nil {
					t.Fatal(err)
				}
				bands, ok := command.Settings["eq_bands"].([]any)
				if !ok || len(bands) != 8 || command.Action != "screen.settings.set" {
					t.Fatalf("bad EQ request: %#v", command)
				}
				for index, value := range bands {
					want := float64(index)
					if index == band {
						want = -12
					}
					if value != want {
						t.Fatalf("band %d changed unexpectedly: %v", index, value)
					}
				}
				if r.settingsDragging != "" {
					t.Fatal("release kept the fader captured")
				}
				r.drawWorkspaceSettings(image.NewRGBA(image.Rectangle{Max: size}))
				if r.settingsIndex != 0 {
					t.Fatal("vertical EQ still paginates")
				}
			})
		}
	}
}

func TestDirectoryFeedbackUploadsBeforeHostResponseAndDoesNotDuplicateRequests(t *testing.T) {
	for _, kind := range []string{"camera", "sat-chat"} {
		t.Run(kind, func(t *testing.T) {
			r, canvas, _ := gpuTestRenderer(t)
			now := time.Now()
			row := satChatDirectoryLayoutFor(960, 480).participantBounds(0)
			if kind == "camera" {
				r.cameraOpen = true
				r.state.Cameras = &show.Cameras{Items: []show.Camera{{ID: "selected", Name: "Camera", Available: true}}}
				row = cameraCardBounds(960, 480, 0, false)
			} else {
				r.satChatOpen = true
				r.state.SatChat = &show.SatChat{Participants: []show.SatChatParticipant{{ID: "selected", Name: "Satellite", Audio: true, Video: true}}}
			}
			r.prepareGPUScene(canvas, now, true, true)
			before := bytes.Clone(r.gpuScene.layer.Pix)
			var wire bytes.Buffer
			client := &socketClient{writer: bufio.NewWriter(&wire)}
			x, y := row.Min.X+row.Dx()/2, row.Min.Y+row.Dy()/2
			handleQueuedTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: x, Y: y})
			pressed, _ := r.prepareGPUScene(canvas, time.Now(), true, true)
			if len(pressed.source) == 0 || bytes.Equal(before, r.gpuScene.layer.Pix) || wire.Len() != 0 {
				t.Fatal("press was not immediately visible before the request")
			}
			handleQueuedTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: x, Y: y})
			pending, _ := r.prepareGPUScene(canvas, time.Now(), true, true)
			if len(pending.source) == 0 || !r.directoryRowPending(kind, "selected") || !strings.Contains(r.directorySubtitle(kind, ""), "…") {
				t.Fatal("selection did not show an immediate pending state")
			}
			for attempt := 0; attempt < 3; attempt++ {
				handleQueuedTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: x, Y: y})
				handleQueuedTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: x, Y: y})
			}
			if strings.Count(wire.String(), `"type":"command"`) != 1 {
				t.Fatalf("repeated taps duplicated startup: %s", wire.String())
			}
			if kind == "camera" {
				r.state.Cameras.SelectedID = "selected"
			} else {
				r.state.SatChat.Call = &show.SatChatCall{ID: "call", Mode: "video"}
			}
			r.updateDirectoryInteraction(time.Now())
			if r.directoryUI.pendingKind != "" || r.directoryUI.awaitingKind != kind {
				t.Fatal("real host response did not retire pending state")
			}
			started, _ := r.prepareGPUScene(canvas, time.Now(), true, true)
			if !started.present || len(started.source) == 0 || r.gpuScene.textKind != "video" {
				t.Fatal("host response did not immediately switch to the video view")
			}
		})
	}
}

func TestDirectoryTimeoutCanRetryAndDragCannotSelect(t *testing.T) {
	r, _, _ := gpuTestRenderer(t)
	r.cameraOpen = true
	r.state.Cameras = &show.Cameras{Items: []show.Camera{{ID: "one", Available: true}}}
	now := time.Now()
	r.beginDirectoryRequest("camera", "one", now)
	r.updateDirectoryInteraction(now.Add(13 * time.Second))
	if r.directoryUI.pendingKind != "" || !strings.Contains(r.directorySubtitle("camera", ""), "try again") {
		t.Fatal("unanswered request remained stuck")
	}
	if !r.beginDirectoryRequest("camera", "one", now.Add(14*time.Second)) {
		t.Fatal("timed out request could not retry")
	}
	r.directoryUI = directoryInteraction{}
	row := cameraCardBounds(960, 480, 0, false)
	x, y := row.Min.X+row.Dx()/2, row.Min.Y+row.Dy()/2
	var wire bytes.Buffer
	client := &socketClient{writer: bufio.NewWriter(&wire)}
	handleQueuedTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: x, Y: y})
	handleQueuedTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Move, X: x + 40, Y: y})
	handleQueuedTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: x + 40, Y: y})
	if wire.Len() != 0 || r.directoryUI.pressedID != "" {
		t.Fatal("drag started a camera or left its row highlighted")
	}
}

func TestVerticalEqualizerPresenceBoostStaysOnTheSamePage(t *testing.T) {
	r, _, _ := gpuTestRenderer(t)
	r.settingsOpen, r.settingsPage = true, "equalizer"
	var wire bytes.Buffer
	client := &socketClient{writer: bufio.NewWriter(&wire)}
	box := equalizerPresenceBounds(960, 480)
	event := linuxinput.TouchEvent{Kind: linuxinput.Down, X: box.Max.X - 40, Y: box.Min.Y + box.Dy()/2}
	r.handleSettingsTouch(client, event, 960, 480)
	if wire.Len() != 0 {
		t.Fatal("presence boost committed before release")
	}
	event.Kind = linuxinput.Up
	r.handleSettingsTouch(client, event, 960, 480)
	var command show.Command
	if err := json.Unmarshal(bytes.TrimSpace(wire.Bytes()), &command); err != nil {
		t.Fatal(err)
	}
	if command.Settings["eq_loudness"] != true || command.Settings["eq_preset"] != "manual" || len(command.Settings) != 2 || r.settingsPage != "equalizer" {
		t.Fatalf("presence boost changed unrelated settings/page: %#v", command)
	}
}

func TestDirectoryDisabledRowsAndCPUFeedback(t *testing.T) {
	for _, target := range []string{"checkers", "crown", "rook"} {
		for _, kind := range []string{"camera", "sat-chat"} {
			r, _, _ := gpuTestRenderer(t)
			r.target, r.spotScreen = target, target == "rook"
			if target == "crown" {
				r.screenWidth, r.screenHeight = 1280, 800
			}
			if target == "rook" {
				r.screenWidth, r.screenHeight = 480, 480
			}
			width, height := r.showSize()
			row := satChatDirectoryLayoutFor(width, height).participantBounds(0)
			if r.spotScreen {
				row = image.Rect(90, 128, 390, 176)
			}
			if kind == "camera" {
				r.cameraOpen = true
				r.state.Cameras = &show.Cameras{Items: []show.Camera{{ID: "one", Name: "Camera", Available: true}}}
				row = cameraCardBounds(width, height, 0, r.spotScreen)
			} else {
				r.satChatOpen = true
				r.state.SatChat = &show.SatChat{Participants: []show.SatChatParticipant{{ID: "one", Name: "Satellite", Audio: true}}}
			}
			canvas := image.NewRGBA(image.Rect(0, 0, width, height))
			now := time.Now()
			r.render(canvas, now)
			before := bytes.Clone(canvas.Pix)
			x, y := row.Min.X+row.Dx()/2, row.Min.Y+row.Dy()/2
			// Press feedback must invalidate the CPU cache as well as the GPU one.
			r.noteDirectoryTouch(linuxinput.TouchEvent{Kind: linuxinput.Down, X: x, Y: y})
			r.render(canvas, now.Add(time.Millisecond))
			if bytes.Equal(before, canvas.Pix) {
				t.Fatalf("%s/%s CPU did not show pressed row", target, kind)
			}
			if kind == "camera" {
				r.state.Cameras.Items[0].Available = false
			} else {
				r.state.SatChat.Participants[0].Busy = true
			}
			r.noteDirectoryTouch(linuxinput.TouchEvent{Kind: linuxinput.Down, X: x, Y: y})
			if r.directoryUI.pressedID != "" {
				t.Fatalf("%s/%s disabled row appeared touchable", target, kind)
			}
		}
	}
}

func TestSatChatDoesNotRenderTheHiddenPhotoHome(t *testing.T) {
	r, canvas, now := gpuTestRenderer(t)
	r.satChatOpen = true
	r.renderCheckers(canvas, now, 960, 480, 0, r.menuPalette().primary, false)
	if r.defaultWallpaperImage != nil || r.defaultWallpaperID != "" {
		t.Fatal("Sat Chat loaded/rasterized an invisible home background")
	}
}
