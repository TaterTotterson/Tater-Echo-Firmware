//go:build linux

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/linuxinput"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

func cameraFixture(size image.Point) *image.RGBA {
	frame := image.NewRGBA(image.Rectangle{Max: size})
	draw.Draw(frame, frame.Bounds(), image.NewUniform(color.RGBA{90, 100, 110, 255}), image.Point{}, draw.Src)
	// Four edge strips make accidental crop/stretch visible to the assertions.
	for _, edge := range []struct {
		box image.Rectangle
		ink color.RGBA
	}{
		{image.Rect(0, 0, max(1, size.X/10), size.Y), color.RGBA{200, 20, 20, 255}},
		{image.Rect(size.X-max(1, size.X/10), 0, size.X, size.Y), color.RGBA{20, 200, 20, 255}},
		{image.Rect(size.X/10, 0, size.X*9/10, max(1, size.Y/10)), color.RGBA{20, 20, 200, 255}},
		{image.Rect(size.X/10, size.Y-max(1, size.Y/10), size.X*9/10, size.Y), color.RGBA{200, 200, 20, 255}},
	} {
		draw.Draw(frame, edge.box, image.NewUniform(edge.ink), image.Point{}, draw.Src)
	}
	return frame
}

func TestCameraViewerMaximizesContainsAndRoundsActualPicture(t *testing.T) {
	for _, target := range []struct {
		name string
		size image.Point
		spot bool
	}{{"checkers", image.Pt(960, 480), false}, {"crown", image.Pt(1280, 800), false}, {"rook", image.Pt(480, 480), true}} {
		for _, talk := range []bool{false, true} {
			for _, sourceSize := range []image.Point{image.Pt(160, 90), image.Pt(120, 90), image.Pt(90, 160), image.Pt(200, 50)} {
				t.Run(fmt.Sprintf("%s/%t/%dx%d", target.name, talk, sourceSize.X, sourceSize.Y), func(t *testing.T) {
					r, _, _ := gpuTestRenderer(t)
					r.target, r.spotScreen = target.name, target.spot
					r.screenWidth, r.screenHeight = target.size.X, target.size.Y
					r.state.Cameras = &show.Cameras{SelectedID: "test", StreamURL: "https://camera.invalid/live", SelectedTalkback: talk}
					viewport := r.cameraVideoBounds()
					identity := cameraStreamIdentity(r.state.Cameras, viewport.Size())
					if viewport.Dx()%2 != 0 || viewport.Dy()%2 != 0 {
						t.Fatal("decoder viewport must stay even")
					}
					for _, hardware := range []bool{false, true} {
						source := cameraFixture(sourceSize)
						if hardware {
							prepared := image.NewRGBA(image.Rectangle{Max: viewport.Size()})
							fitCameraStreamFrame(prepared, source)
							// Also exercise non-zero source origins/stride after fitting.
							r.cameraFrameImage = prepared
						} else {
							r.cameraFrameImage = prepareContainImage(source, viewport.Size())
						}
						content := cameraImageContentBounds(r.cameraFrameImage)
						picture := content.Add(viewport.Min)
						if target.spot {
							picture = rookContainedArtwork(content.Size(), 228)
						}
						if !picture.In(viewport) || (!target.spot && picture.Dx() != viewport.Dx() && picture.Dy() != viewport.Dy()) {
							t.Fatalf("picture %v is not maximized in %v", picture, viewport)
						}
						if errorPixels := math.Abs(float64(picture.Dx()*sourceSize.Y-picture.Dy()*sourceSize.X)) / float64(max(sourceSize.X, sourceSize.Y)); errorPixels > 1 {
							t.Fatalf("aspect changed by %.2f pixels", errorPixels)
						}
						canvas := image.NewRGBA(image.Rectangle{Max: target.size})
						// Verify the picture before the deliberately overlapping UI.
						drawWorkspaceVideoPicture(canvas, viewport, r.cameraFrameImage)
						if got := canvas.RGBAAt(picture.Min.X, picture.Min.Y); got != workspaceBackground {
							t.Fatalf("picture corner is square: %v", got)
						}
						for _, edge := range []struct {
							point image.Point
							ink   color.RGBA
						}{
							{image.Pt(picture.Min.X+3, picture.Min.Y+picture.Dy()/2), color.RGBA{200, 20, 20, 255}},
							{image.Pt(picture.Max.X-4, picture.Min.Y+picture.Dy()/2), color.RGBA{20, 200, 20, 255}},
							{image.Pt(picture.Min.X+picture.Dx()/2, picture.Min.Y+3), color.RGBA{20, 20, 200, 255}},
							{image.Pt(picture.Min.X+picture.Dx()/2, picture.Max.Y-4), color.RGBA{200, 200, 20, 255}},
						} {
							if got := canvas.RGBAAt(edge.point.X, edge.point.Y); got != edge.ink {
								t.Fatalf("source edge cropped or altered at %v: got %v want %v", edge.point, got, edge.ink)
							}
						}
						if r.cameraVideoBounds() != viewport || cameraStreamIdentity(r.state.Cameras, r.cameraVideoBounds().Size()) != identity {
							t.Fatal("source aspect changed decoder identity")
						}
					}
				})
			}
		}
	}
}

func TestCameraViewerAspectChangeClearsOldPictureAndReusesControls(t *testing.T) {
	r, canvas, now := gpuTestRenderer(t)
	r.cameraOpen = true
	r.state.Cameras = &show.Cameras{SelectedID: "test", SelectedName: "Test camera"}
	viewport := r.cameraVideoBounds()
	r.cameraFrameImage = prepareContainImage(cameraFixture(image.Pt(160, 90)), viewport.Size())
	r.cameraFrameShown = "one"
	r.render(canvas, now)
	controls := r.videoControls.layer
	key := r.videoControls.key
	artwork := bytes.Clone(controls.Pix)
	r.cameraFrameImage = prepareContainImage(cameraFixture(image.Pt(90, 160)), viewport.Size())
	r.cameraFrameShown = "two"
	r.render(canvas, now.Add(time.Second/30))
	if len(r.presentRegions) != 1 || r.presentRegions[0] != viewport {
		t.Fatalf("live update repainted full UI: %v", r.presentRegions)
	}
	if controls != r.videoControls.layer || key != r.videoControls.key || !bytes.Equal(artwork, controls.Pix) {
		t.Fatal("new video frame rebuilt the title/control artwork")
	}
	want := image.NewRGBA(canvas.Rect)
	drawWorkspaceVideoPicture(want, viewport, r.cameraFrameImage)
	r.drawWorkspaceVideoControls(want, "camera")
	if !bytes.Equal(canvas.Pix, want.Pix) {
		t.Fatal("cached controls disappeared or accumulated alpha on a new picture")
	}
	if got := canvas.RGBAAt(viewport.Min.X+viewport.Dx()/4, viewport.Min.Y+viewport.Dy()/2); got != workspaceBackground {
		t.Fatalf("old wide picture left a ghost: %v", got)
	}
	r.render(canvas, now.Add(time.Second/15))
	if len(r.presentRegions) != 0 {
		t.Fatal("unchanged frame was presented again")
	}
}

func TestCameraViewOnlyAndTalkbackBothUseFullScreen(t *testing.T) {
	r, _, _ := gpuTestRenderer(t)
	r.state.Cameras = &show.Cameras{SelectedID: "test", SelectedTalkback: true}
	talk := r.cameraVideoBounds()
	if talk != image.Rect(0, 0, 960, 480) || !cameraTalkBounds(960, 480, false).In(talk) || !r.cameraCloseBounds(960, 480, false, true).In(talk) {
		t.Fatal("video must extend behind the floating controls")
	}
	r.state.Cameras.SelectedTalkback = false
	view := r.cameraVideoBounds()
	if view != talk {
		t.Fatal("talkback controls must not shrink/restart the decoder viewport")
	}
}

func TestCameraTransparentGeometryDoesNotTreatDarkSceneAsPadding(t *testing.T) {
	frame := image.NewRGBA(image.Rect(12, 20, 112, 120))
	draw.Draw(frame, frame.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	if got := cameraImageContentBounds(frame); got != frame.Bounds() {
		t.Fatalf("opaque black scene cropped: %v", got)
	}
}

func TestConnectedRowsDisableBusyAndUnavailableActions(t *testing.T) {
	var wire bytes.Buffer
	client := &socketClient{writer: bufio.NewWriter(&wire)}
	r := &renderer{cameraOpen: true, satChatOpen: true, state: show.Snapshot{
		Cameras: &show.Cameras{Items: []show.Camera{{ID: "offline", Name: "Unavailable"}}},
		SatChat: &show.SatChat{Participants: []show.SatChatParticipant{{ID: "busy", Audio: true, Video: true, Busy: true}}},
	}}
	row := connectedRowBounds(connectedListBounds(960, 480), 0, 3)
	event := linuxinput.TouchEvent{Kind: linuxinput.Up, X: row.Min.X + row.Dx()/2, Y: row.Min.Y + row.Dy()/2}
	r.handleCameraTouch(client, event, 960, 480, false)
	r.handleSatChatTouch(client, event, 960, 480)
	if wire.Len() != 0 {
		t.Fatalf("disabled row sent action: %s", wire.String())
	}
	r.state.SatChat.Participants[0].Busy = false
	r.handleSatChatTouch(client, event, 960, 480)
	if !strings.Contains(wire.String(), `"mode":"video"`) {
		t.Fatalf("call capability routing changed: %s", wire.String())
	}
}

func TestWorkspaceClimateKeepsBoundsAndUnsupportedModesSafe(t *testing.T) {
	var wire bytes.Buffer
	client := &socketClient{writer: bufio.NewWriter(&wire)}
	value := 90.0
	r := &renderer{state: show.Snapshot{Climate: &show.Climate{Available: true, RoomID: "test", TargetTemperature: &value, CanSetTemperature: true, CanSetMode: true, MinimumTemperature: 45, MaximumTemperature: 90, TemperatureStep: 0.5, HVACMode: "heat", AvailableHVACModes: []string{"heat", "off"}}}}
	for _, size := range []image.Point{image.Pt(960, 480), image.Pt(1280, 800)} {
		for index := 0; index < 4; index++ {
			if !climateModeBounds(size.X, size.Y, index, false).In(image.Rectangle{Max: size}) {
				t.Fatal("climate hit target outside screen")
			}
		}
		plus := climateSetpointBounds(size.X, size.Y, true, false)
		r.handleClimateTouch(client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: plus.Min.X + plus.Dx()/2, Y: plus.Min.Y + plus.Dy()/2}, size.X, size.Y)
		if *r.state.Climate.TargetTemperature != 90 {
			t.Fatal("setpoint exceeded safe maximum")
		}
		wire.Reset()
		cool := climateModeBounds(size.X, size.Y, 1, false)
		r.handleClimateTouch(client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: cool.Min.X + cool.Dx()/2, Y: cool.Min.Y + cool.Dy()/2}, size.X, size.Y)
		if wire.Len() != 0 || r.state.Climate.HVACMode != "heat" {
			t.Fatal("unsupported cool mode activated")
		}
	}
}

func TestConnectedScreenCloseGlyphsRemainVisible(t *testing.T) {
	for _, spot := range []bool{false, true} {
		for _, view := range []string{"camera-directory", "camera-view", "sat-chat", "climate"} {
			r, _, now := gpuTestRenderer(t)
			width, height := 960, 480
			if spot {
				width = 480
				r.target = "rook"
				r.spotScreen = true
				r.screenWidth = 480
			}
			canvas := image.NewRGBA(image.Rect(0, 0, width, height))
			bounds := climateCloseBounds(width, height, spot)
			switch view {
			case "camera-directory", "camera-view":
				r.cameraOpen = true
				r.state.Cameras = &show.Cameras{}
				if view == "camera-view" {
					r.state.Cameras.SelectedID = "test"
					r.state.Cameras.SelectedTalkback = true
				}
				bounds = r.cameraCloseBounds(width, height, spot, view == "camera-view")
			case "sat-chat":
				r.satChatOpen = true
				bounds = satChatCloseBounds(width)
				if spot {
					bounds = spotSatChatCloseBounds()
				}
			case "climate":
				r.climateOpen = true
			}
			r.render(canvas, now)
			cx, cy := bounds.Min.X+bounds.Dx()/2, bounds.Min.Y+bounds.Dy()/2
			if spot {
				// The round workspace now uses a back chevron or labeled Close
				// pill, not the old X centered in this hit rectangle.
				found := false
				for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
					for x := bounds.Min.X; x < bounds.Max.X; x++ {
						if canvas.RGBAAt(x, y) == workspaceText {
							found = true
						}
					}
				}
				if !found {
					t.Errorf("%s round-screen back/close label missing", view)
				}
				continue
			}
			if got := canvas.RGBAAt(cx, cy); got != workspaceMuted {
				t.Errorf("%s spot=%t close glyph missing at %d,%d: %v", view, spot, cx, cy, got)
			}
		}
	}
}
