//go:build linux

package main

import (
	"bytes"
	"image"
	"os"
	"testing"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/gpucompositor"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

func TestRookGPURendersRoundWorkspaceAndRetainsVideoControls(t *testing.T) {
	r, canvas, now := rookFixture(t)
	r.menuOpen = true
	frame, ok := r.prepareGPUScene(canvas, now, true, true, true)
	want := image.NewRGBA(canvas.Rect)
	r.drawRookMenu(want)
	if !ok || !bytes.Equal(frame.source, want.Pix) {
		t.Fatal("GPU layer used the rectangular Show renderer")
	}
	frame, ok = r.prepareGPUScene(canvas, now.Add(time.Second), true, true, true)
	if !ok || frame.present {
		t.Fatal("unchanged round menu was uploaded again")
	}
	r.menuOpen, r.cameraOpen = false, true
	r.state.Cameras = &show.Cameras{SelectedID: "generated", SelectedName: "Generated camera", SelectedTalkback: true}
	r.cameraFrameImage, r.cameraFrameShown = cameraFixture(image.Pt(640, 480)), "generated-1"
	frame, ok = r.prepareGPUScene(canvas, now.Add(2*time.Second), true, true, true)
	if !ok || frame.effects.Video == nil || frame.effects.Video.Destination != rookContainedArtwork(image.Pt(640, 480), 228) {
		t.Fatal("round GPU video cropped or stretched", frame.effects.Video)
	}
	atlas := r.videoControls.layer
	r.cameraFrameShown = "generated-2"
	frame, _ = r.prepareGPUScene(canvas, now.Add(3*time.Second), true, true, true)
	if frame.effects.Text == nil || len(frame.effects.Text.Pixels) != 0 || r.videoControls.layer != atlas {
		t.Fatal("steady round video rebuilt its floating controls")
	}
}

// Explicit generated-data-only qualification. Never opens fb0 or a camera.
func TestRookGPUGeneratedOffscreen(t *testing.T) {
	if os.Getenv("TATER_GPU_TEST_ROOK") != "1" {
		t.Skip("owner-local Rook GPU opt-in required")
	}
	compositor, err := gpucompositor.Open()
	if err != nil || compositor == nil {
		t.Fatalf("Rook GPU unavailable: %v", err)
	}
	defer compositor.Close()
	r, canvas, now := rookFixture(t)
	r.menuOpen = true
	frame, ok := r.prepareGPUScene(canvas, now, true, true, true)
	if !ok {
		t.Fatal("round GPU layer rejected")
	}
	frame.effects.Blend = 1
	panel := make([]byte, 480*480*4)
	if err := compositor.CompositeScene(frame.source, panel, frame.effects); err != nil {
		t.Fatal(err)
	}
	for from := 0; from < len(panel); from += 4 {
		pixel := r.gpuScene.layer.Pix[from : from+4]
		if !bytes.Equal(panel[from:from+4], []byte{pixel[2], pixel[1], pixel[0], pixel[3]}) {
			t.Fatalf("Rook menu orientation/color mismatch at pixel %d", from/4)
		}
	}
	r.menuOpen, r.cameraOpen = false, true
	r.state.Cameras = &show.Cameras{SelectedID: "generated", SelectedName: "Generated camera", SelectedTalkback: true}
	r.cameraFrameImage, r.cameraFrameShown = cameraFixture(image.Pt(640, 480)), "generated-first"
	frame, _ = r.prepareGPUScene(canvas, now.Add(time.Second), true, true, true)
	if err := compositor.CompositeScene(frame.source, panel, frame.effects); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	for index := 0; index < 120; index++ {
		r.cameraFrameShown = time.Duration(index).String()
		frame, _ = r.prepareGPUScene(canvas, now.Add(time.Second+time.Duration(index)*time.Second/30), true, true, true)
		if !frame.present || frame.effects.Text == nil || len(frame.effects.Text.Pixels) != 0 {
			t.Fatal("video atlas was not retained")
		}
		if err := compositor.CompositeScene(frame.source, panel, frame.effects); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("Rook exact native menu pixels, uncropped video and retained controls: %.2f offscreen FPS", 120/time.Since(started).Seconds())
}
