//go:build linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/linuxinput"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

func TestSatChatVideoRetainsControlArtworkAndEndButton(t *testing.T) {
	for _, target := range []struct {
		name          string
		width, height int
		spot          bool
	}{{"checkers", 960, 480, false}, {"crown", 1280, 800, false}, {"rook", 480, 480, true}} {
		t.Run(target.name, func(t *testing.T) {
			r, _, now := gpuTestRenderer(t)
			r.target, r.screenWidth, r.screenHeight, r.spotScreen = target.name, target.width, target.height, target.spot
			r.state.SatChat = &show.SatChat{Self: "self", Call: &show.SatChatCall{ID: "call", Mode: "video", State: "active", TargetName: "Kitchen Show"}}
			viewport := r.satChatVideoBounds()
			canvas := image.NewRGBA(image.Rect(0, 0, target.width, target.height))
			r.satChatVideoImage = prepareContainImage(cameraFixture(image.Pt(160, 90)), viewport.Size())
			r.satChatVideoShown = "frame-one"
			r.render(canvas, now)
			controls, controlsKey := r.videoControls.layer, r.videoControls.key
			header := append([]byte(nil), canvas.Pix[:canvas.PixOffset(0, viewport.Min.Y)]...)
			footer := append([]byte(nil), canvas.Pix[canvas.PixOffset(0, viewport.Max.Y):]...)
			// A different aspect exercises old-bar cleanup on the partial path.
			r.satChatVideoImage = prepareContainImage(cameraFixture(image.Pt(90, 160)), viewport.Size())
			r.satChatVideoShown = "frame-two"
			r.render(canvas, now.Add(time.Second/30))
			if len(r.presentRegions) != 1 || r.presentRegions[0] != viewport || !r.transientPresent {
				t.Fatalf("video lost transient region path: %v", r.presentRegions)
			}
			if !bytes.Equal(header, canvas.Pix[:len(header)]) || !bytes.Equal(footer, canvas.Pix[canvas.PixOffset(0, viewport.Max.Y):]) {
				t.Fatal("new video frame repainted title or End Chat")
			}
			picture := cameraImageContentBounds(r.satChatVideoImage).Add(viewport.Min)
			if target.spot {
				picture = rookContainedArtwork(picture.Size(), 228)
			}
			if got := canvas.RGBAAt(picture.Min.X, picture.Min.Y); got != workspaceBackground {
				t.Fatal("Sat Chat lost its rounded picture corners")
			}
			if got := canvas.RGBAAt(viewport.Min.X+viewport.Dx()/4, viewport.Min.Y+viewport.Dy()/2); got != workspaceBackground {
				t.Fatal("old wide picture left a ghost")
			}
			end := r.satChatEndBounds(target.width, target.height)
			{
				if !end.In(viewport) || controls != r.videoControls.layer || controlsKey != r.videoControls.key {
					t.Fatal("floating End Chat must reuse cached artwork inside the video area")
				}
				want := image.NewRGBA(canvas.Rect)
				drawWorkspaceVideoPicture(want, viewport, r.satChatVideoImage)
				r.drawWorkspaceVideoControls(want, "sat-chat")
				if !bytes.Equal(canvas.Pix, want.Pix) {
					t.Fatal("floating call controls disappeared or accumulated alpha")
				}
			}
			var wire bytes.Buffer
			client := &socketClient{writer: bufio.NewWriter(&wire)}
			event := linuxinput.TouchEvent{Kind: linuxinput.Up, X: end.Min.X + end.Dx()/2, Y: end.Min.Y + end.Dy()/2}
			if target.spot {
				r.handleSpotSatChatTouch(client, event)
			} else {
				r.handleSatChatTouch(client, event, target.width, target.height)
			}
			if !strings.Contains(wire.String(), `"action":"sat_chat.end"`) {
				t.Fatalf("End Chat hit target failed: %s", wire.String())
			}
			r.render(canvas, now.Add(time.Second/15))
			if len(r.presentRegions) != 0 {
				t.Fatal("unchanged video frame was redrawn")
			}
		})
	}
}

func TestSatChatSnapshotLoaderContainsInsteadOfCropping(t *testing.T) {
	source := cameraFixture(image.Pt(160, 90))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _ = png.Encode(w, source) }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	requests := make(chan screenImageRequest, 1)
	results := make(chan screenImageResult, 1)
	go runLatestScreenImageLoader(ctx, requests, results)
	requests <- screenImageRequest{kind: satChatVideoScreenImage, identity: "test", session: "call", rawURL: server.URL, size: image.Pt(936, 324)}
	select {
	case result := <-results:
		if result.image == nil {
			t.Fatal("snapshot did not decode")
		}
		content := cameraImageContentBounds(result.image)
		if content.Size() != image.Pt(576, 324) {
			t.Fatalf("16:9 snapshot was cropped/stretched: %v", content)
		}
		if got := result.image.At(content.Min.X+3, content.Min.Y+content.Dy()/2); got != source.RGBAAt(0, 45) {
			t.Fatalf("left source edge was cropped: %v", got)
		}
		if result.image.Bounds().Size() != image.Pt(936, 324) || result.session != "call" {
			t.Fatal("snapshot lost bounded viewport or call identity")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("snapshot loader stalled")
	}
}

func TestSatChatH264FramesKeepAspectAndDecoderIdentity(t *testing.T) {
	r, _, _ := gpuTestRenderer(t)
	r.state.SatChat = &show.SatChat{Call: &show.SatChatCall{ID: "call", State: "active", Mode: "video"}, VideoStreamURL: "http://192.0.2.10/video"}
	viewport := r.satChatVideoBounds()
	identity := satChatH264Identity(r.state.SatChat, viewport.Size())
	controller := &cameraStreamController{identity: identity, recycle: make(chan *image.RGBA, 3)}
	for index, size := range []image.Point{image.Pt(160, 90), image.Pt(120, 90), image.Pt(90, 160)} {
		frame := image.NewRGBA(image.Rectangle{Max: viewport.Size()})
		fitCameraStreamFrame(frame, cameraFixture(size))
		r.applySatChatH264(controller, cameraStreamResult{identity: identity, sequence: uint64(index + 1), image: frame})
		if r.satChatVideoImage != frame || r.satChatStreamImage != frame {
			t.Fatal("H.264 frame rejected by the new layout")
		}
		want := containBounds(image.Rectangle{Max: viewport.Size()}, size)
		if got := cameraImageContentBounds(r.satChatVideoImage); got != want {
			t.Fatalf("H.264 source aspect lost: got %v want %v", got, want)
		}
		if satChatH264Identity(r.state.SatChat, r.satChatVideoBounds().Size()) != identity {
			t.Fatal("source aspect restarted decoder identity")
		}
	}
}

func BenchmarkPreparedRoundedSatChatFrame(b *testing.B) {
	viewport := workspaceVideoViewport(960, 480, false, true)
	frame := prepareContainImage(cameraFixture(image.Pt(640, 360)), viewport.Size())
	canvas := image.NewRGBA(image.Rect(0, 0, 960, 480))
	// Warm the retained corner masks; measured work is only a new video frame.
	drawWorkspaceVideoPicture(canvas, viewport, frame)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		drawWorkspaceVideoPicture(canvas, viewport, frame)
	}
}
