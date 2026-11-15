//go:build linux

package main

import (
	"bytes"
	"fmt"
	"image"
	"os"
	"testing"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/gpucompositor"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

// Explicit owner-device offscreen check only: synthetic pictures, no display,
// microphone, camera, network stream, or host command is opened by this test.
func TestGPUVideoHardwareOffscreen(t *testing.T) {
	if os.Getenv("TATER_GPU_TEST_VIDEO") != "checkers" {
		t.Skip("set TATER_GPU_TEST_VIDEO=checkers for the owner-device offscreen probe")
	}
	compositor, err := gpucompositor.Open()
	if err != nil {
		t.Fatal(err)
	}
	if compositor == nil || !compositor.SupportsVideoScenes() {
		t.Fatal("qualified v4 GPU runtime unavailable")
	}
	defer compositor.Close()
	for _, kind := range []string{"camera", "sat-chat"} {
		t.Run(kind, func(t *testing.T) {
			r, canvas, now := gpuTestRenderer(t)
			picture := prepareContainImage(cameraFixture(image.Pt(160, 90)), canvas.Rect.Size())
			if kind == "camera" {
				r.cameraOpen = true
				r.state.Cameras = &show.Cameras{SelectedID: "synthetic", SelectedName: "Offscreen test", SelectedTalkback: true}
				r.cameraFrameImage = picture
			} else {
				r.state.SatChat = &show.SatChat{Call: &show.SatChatCall{ID: "synthetic", Mode: "video", TargetName: "Offscreen test"}}
				r.satChatVideoImage = picture
			}
			panel := make([]byte, gpucompositor.PanelBytes)
			first, _ := r.prepareGPUScene(canvas, now, true, true, true)
			if err := compositor.CompositeScene(first.source, panel, first.effects); err != nil {
				t.Fatal(err)
			}
			// Compare actual GPU rotation/blending against the safe CPU path.
			r.render(canvas, now)
			bad := 0
			badBounds := image.Rectangle{}
			outsideTolerance := func(a, b uint8) bool {
				delta := int(a) - int(b)
				return delta > 8 || delta < -8
			}
			for y := 0; y < 480; y++ {
				for x := 0; x < 960; x++ {
					from := (x*480 + 479 - y) * 4
					want := canvas.RGBAAt(x, y)
					if outsideTolerance(panel[from+2], want.R) || outsideTolerance(panel[from+1], want.G) || outsideTolerance(panel[from], want.B) {
						if bad < 6 {
							t.Logf("sample difference (%d,%d): GPU RGB=%d,%d,%d CPU=%v", x, y, panel[from+2], panel[from+1], panel[from], want)
						}
						badBounds = badBounds.Union(image.Rect(x, y, x+1, y+1))
						bad++
					}
				}
			}
			const frames = 60
			started := time.Now()
			var slowest time.Duration
			for index := 0; index < frames; index++ {
				frameStart := time.Now()
				if kind == "camera" {
					r.cameraFrameShown = fmt.Sprint(index)
				} else {
					r.satChatVideoShown = fmt.Sprint(index)
				}
				frame, _ := r.prepareGPUScene(canvas, now.Add(time.Duration(index)*time.Second/30), true, true, true)
				if !frame.present || frame.effects.Text == nil || len(frame.effects.Text.Pixels) != 0 {
					t.Fatal("steady playback re-uploaded the floating control atlas")
				}
				if err := compositor.CompositeScene(frame.source, panel, frame.effects); err != nil {
					t.Fatal(err)
				}
				if elapsed := time.Since(frameStart); elapsed > slowest {
					slowest = elapsed
				}
			}
			t.Logf("synthetic full-screen %s: %.1f fps, slowest %s, GPU/CPU mismatched pixels=%d", kind, frames/time.Since(started).Seconds(), slowest.Round(time.Millisecond), bad)
			if bad > 460 { // Allow edge sampling differences, not a broken overlay.
				t.Fatalf("GPU/CPU overlay mismatch at %d pixels in %v", bad, badBounds)
			}
		})
	}
}

func TestGPUFullScreenVideoRetainsFloatingControls(t *testing.T) {
	for _, kind := range []string{"camera", "sat-chat"} {
		t.Run(kind, func(t *testing.T) {
			r, canvas, now := gpuTestRenderer(t)
			picture := prepareContainImage(cameraFixture(image.Pt(160, 90)), canvas.Rect.Size())
			if kind == "camera" {
				r.cameraOpen = true
				r.state.Cameras = &show.Cameras{SelectedID: "front", SelectedName: "Front door", SelectedTalkback: true}
				r.cameraFrameShown, r.cameraFrameImage = "one", picture
			} else {
				r.state.SatChat = &show.SatChat{Call: &show.SatChatCall{ID: "call", Mode: "video", State: "active", TargetName: "Kitchen"}}
				r.satChatVideoShown, r.satChatVideoImage = "one", picture
			}
			first, ok := r.prepareGPUScene(canvas, now, true, true)
			if !ok || !first.present || len(first.source) == 0 || first.effects.Text == nil || len(first.effects.Text.Pixels) == 0 {
				t.Fatal("entering video must upload picture and floating control atlas")
			}
			if first.effects.Text.Width != 1024 || first.effects.Text.Height != 512 {
				t.Fatal("floating controls must use pixel-exact power-of-two atlas coordinates")
			}
			if first.effects.Blend != 1 || first.effects.Rim || first.effects.Ambient || first.effects.Dots {
				t.Fatal("live video must not inherit a UI entrance or voice effect")
			}
			controlPixels := bytes.Clone(r.videoControls.layer.Pix)
			if kind == "camera" {
				r.cameraFrameShown = "two"
			} else {
				r.satChatVideoShown = "two"
			}
			next, _ := r.prepareGPUScene(canvas, now.Add(time.Second/30), true, true)
			if !next.present || len(next.source) == 0 || next.effects.Text == nil || len(next.effects.Text.Pixels) != 0 || len(next.effects.Text.Quads) < 2 {
				t.Fatal("new picture must reuse GPU control texture, without another atlas upload")
			}
			if !bytes.Equal(controlPixels, r.videoControls.layer.Pix) {
				t.Fatal("new decoded picture rebuilt the floating control artwork")
			}
			still, _ := r.prepareGPUScene(canvas, now.Add(time.Second/15), true, true)
			if still.present || len(still.source) != 0 {
				t.Fatal("unchanged video/control state must make no GPU job")
			}
			if kind == "camera" {
				r.cameraTalkPressed = true
			} else {
				r.state.SatChat.Call.TargetName = "Office"
			}
			controlChange, _ := r.prepareGPUScene(canvas, now.Add(time.Second/10), true, true)
			if !controlChange.present || len(controlChange.source) != 0 || len(controlChange.effects.Text.Pixels) == 0 {
				t.Fatal("changed controls must update only the atlas, retaining the GPU picture")
			}
			r.state.Muted = true
			muted, _ := r.prepareGPUScene(canvas, now.Add(120*time.Millisecond), true, true)
			if !muted.present || len(muted.source) != 0 || len(muted.effects.Text.Pixels) == 0 || r.videoControls.layer.RGBAAt(25, 430).A == 0 {
				t.Fatal("mute must immediately update its unobstructed badge, even without another picture")
			}
			// A volume overlay/failure must restore the complete CPU picture/UI.
			if _, ok := r.prepareGPUScene(canvas, now, false, true); ok {
				t.Fatal("disabled GPU did not fall back")
			}
			r.render(canvas, now)
			if len(r.presentRegions) == 0 {
				t.Fatal("GPU fallback did not restore the CPU canvas")
			}
		})
	}
}

func TestGPUVideoV4UploadsSourceGeometryWithoutCPUFit(t *testing.T) {
	r, canvas, now := gpuTestRenderer(t)
	picture := cameraFixture(image.Pt(640, 480))
	r.state.SatChat = &show.SatChat{Call: &show.SatChatCall{ID: "call", Mode: "video", State: "active", TargetName: "Kitchen"}}
	r.satChatVideoShown, r.satChatVideoImage = "one", picture
	first, ok := r.prepareGPUScene(canvas, now, true, true, true)
	video := first.effects.Video
	if !ok || !first.present || len(first.source) != 0 || video == nil ||
		len(video.Pixels) != 640*480*4 || video.Source != picture.Rect ||
		video.Destination != image.Rect(160, 0, 800, 480) || first.effects.Text == nil || len(first.effects.Text.Pixels) == 0 {
		t.Fatalf("source-sized picture did not use direct GPU video composition: %+v", video)
	}
	r.state.SatChat.Call.TargetName = "Office"
	controls, _ := r.prepareGPUScene(canvas, now.Add(time.Second/30), true, true, true)
	if controls.effects.Video == nil || len(controls.effects.Video.Pixels) != 0 ||
		controls.effects.Text == nil || len(controls.effects.Text.Pixels) == 0 {
		t.Fatal("control-only update did not retain the GPU video texture")
	}
}

func TestGPUVideoRequiresAtlasAndLeavesWithoutStaleSlideSource(t *testing.T) {
	r, canvas, now := gpuTestRenderer(t)
	r.cameraOpen = true
	r.state.Cameras = &show.Cameras{SelectedID: "test"}
	if _, ok := r.prepareGPUScene(canvas, now, true, false); ok {
		t.Fatal("old helper cannot composite floating controls; must use CPU fallback")
	}
	r.state.Settings = show.ScreenSettingDefaults()
	r.state.Settings["screen_popup_effect"] = "slide"
	r.prepareGPUScene(canvas, now, true, true)
	r.cameraOpen, r.menuOpen = false, true
	menu, _ := r.prepareGPUScene(canvas, now.Add(time.Second), true, true)
	if !menu.present || len(menu.source) == 0 || r.gpuScene.sliding {
		t.Fatal("leaving video must snapshot the displayed GPU frame, not slide an uncomposited picture")
	}
	r.prepareGPUScene(canvas, now.Add(1300*time.Millisecond), true, true)
	r.menuOpen, r.settingsOpen, r.settingsPage = false, true, "display"
	r.prepareGPUScene(canvas, now.Add(1400*time.Millisecond), true, true)
	if !r.gpuScene.sliding {
		t.Fatal("leaving video permanently disabled later menu slide animations")
	}
}
