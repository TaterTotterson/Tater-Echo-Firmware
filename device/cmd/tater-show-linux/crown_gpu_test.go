//go:build linux

package main

import (
	"image"
	"os"
	"testing"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/gpucompositor"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

func TestCrownRetainsNativeWorkspaceAndVideoGeometry(t *testing.T) {
	r, _, now := gpuTestRenderer(t)
	r.target, r.screenWidth, r.screenHeight = "crown", 1280, 800
	canvas := image.NewRGBA(image.Rect(0, 0, 1280, 800))
	r.menuOpen = true
	first, ok := r.prepareGPUScene(canvas, now, true, true, true)
	if !ok || len(first.source) != 1280*800*4 {
		t.Fatal("Crown menu did not use its native GPU workspace")
	}
	r.state.Settings = show.ScreenSettingDefaults()
	r.state.Settings["screen_popup_effect"] = "slide"
	r.menuOpen = false
	r.settingsOpen = true
	r.settingsPage = "display"
	r.prepareGPUScene(canvas, now.Add(time.Second), true, true, true)
	frame, _ := r.prepareGPUScene(canvas, now.Add(time.Second+50*time.Millisecond), true, true, true)
	if frame.effects.Text != nil && (frame.effects.Text.Width != 1280 || frame.effects.Text.Height != 800) {
		t.Fatal("slide uses smaller screen geometry")
	}
	r.settingsOpen, r.cameraOpen = false, true
	r.state.Cameras = &show.Cameras{SelectedID: "camera"}
	r.cameraFrameImage = image.NewRGBA(image.Rect(0, 0, 768, 432))
	r.cameraFrameShown = "test"
	video, ok := r.prepareGPUScene(canvas, now.Add(2*time.Second), true, true, true)
	if !ok || video.effects.Video == nil || video.effects.Video.Destination != image.Rect(0, 40, 1280, 760) {
		t.Fatalf("wrong Crown video: %+v", video.effects.Video)
	}
	if video.effects.Text.Width != 2048 || video.effects.Text.Height != 1024 {
		t.Fatal("video controls clipped to smaller atlas")
	}
}

// Explicit owner-device test: generated pixels only, never fb0 or a camera.
func TestCrownGPUGeneratedOffscreen(t *testing.T) {
	if os.Getenv("TATER_GPU_TEST_CROWN") != "1" {
		t.Skip("owner-local Crown GPU opt-in required")
	}
	testNativeGPUGeneratedOffscreen(t, "crown")
}

func TestAthenaGPUGeneratedOffscreen(t *testing.T) {
	if os.Getenv("TATER_GPU_TEST_ATHENA") != "1" {
		t.Skip("owner-local Athena GPU opt-in required")
	}
	testNativeGPUGeneratedOffscreen(t, "athena")
}

func testNativeGPUGeneratedOffscreen(t *testing.T, target string) {
	t.Helper()
	compositor, err := gpucompositor.Open()
	if err != nil || compositor == nil {
		t.Fatalf("Crown GPU unavailable: %v", err)
	}
	defer compositor.Close()
	r, _, now := gpuTestRenderer(t)
	r.target, r.screenWidth, r.screenHeight = target, 1280, 800
	canvas := image.NewRGBA(image.Rect(0, 0, 1280, 800))
	r.menuOpen = true
	frame, ok := r.prepareGPUScene(canvas, now, true, true, true)
	if !ok {
		t.Fatal("native Crown menu rejected")
	}
	frame.effects.Blend = 1
	panel := make([]byte, 1280*800*4)
	if err := compositor.CompositeScene(frame.source, panel, frame.effects); err != nil {
		t.Fatal(err)
	}
	for y := 0; y < 800; y++ {
		for x := 0; x < 1280; x++ {
			from, to := (y*1280+x)*4, (x*800+799-y)*4
			if panel[to] != r.gpuScene.layer.Pix[from+2] || panel[to+1] != r.gpuScene.layer.Pix[from+1] || panel[to+2] != r.gpuScene.layer.Pix[from] {
				t.Fatalf("menu color/rotation mismatch at %d,%d", x, y)
			}
		}
	}
	r.menuOpen, r.cameraOpen = false, true
	r.state.Cameras = &show.Cameras{SelectedID: "generated", SelectedName: "Generated camera", SelectedTalkback: true}
	r.cameraFrameImage = cameraFixture(image.Pt(768, 432))
	r.cameraFrameShown = "generated-1"
	frame, ok = r.prepareGPUScene(canvas, now.Add(time.Second), true, true, true)
	if !ok || frame.effects.Video == nil {
		t.Fatal("native video did not use direct GPU scene")
	}
	if err := compositor.CompositeScene(frame.source, panel, frame.effects); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	for index := 0; index < 60; index++ {
		r.cameraFrameShown = time.Duration(index).String()
		frame, _ = r.prepareGPUScene(canvas, now.Add(time.Second+time.Duration(index)*time.Second/30), true, true, true)
		if !frame.present || frame.effects.Text == nil || len(frame.effects.Text.Pixels) != 0 {
			t.Fatal("steady video retransmitted controls")
		}
		if err := compositor.CompositeScene(frame.source, panel, frame.effects); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("%s: exact native menu pixels, direct video/retained overlay, %.2f offscreen FPS", target, 60/time.Since(started).Seconds())
}
