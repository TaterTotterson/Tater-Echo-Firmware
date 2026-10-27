//go:build linux

package main

import (
	"errors"
	"image"
	"testing"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

func TestCameraStreamIdentityRequiresSelectedStream(t *testing.T) {
	size := image.Pt(896, 672)
	if got := cameraStreamIdentity(nil, size); got != "" {
		t.Fatalf("nil camera identity = %q", got)
	}
	if got := cameraStreamIdentity(&show.Cameras{SelectedID: "front"}, size); got != "" {
		t.Fatalf("snapshot-only camera identity = %q", got)
	}
	got := cameraStreamIdentity(&show.Cameras{SelectedID: "front", StreamURL: "https://ha.invalid/live"}, size)
	if got == "" {
		t.Fatal("stream-capable camera has no identity")
	}
}

func TestCameraStreamControllerRecyclesSupersededFrames(t *testing.T) {
	controller := newCameraStreamController()
	first := image.NewRGBA(image.Rect(0, 0, 16, 16))
	second := image.NewRGBA(image.Rect(0, 0, 16, 16))
	controller.publish(cameraStreamResult{identity: "one", image: first})
	controller.publish(cameraStreamResult{identity: "one", image: second})
	got := <-controller.frames
	if got.image != second {
		t.Fatal("latest stream frame was not retained")
	}
	select {
	case recycled := <-controller.recycle:
		if recycled != first {
			t.Fatal("wrong superseded frame was recycled")
		}
	default:
		t.Fatal("superseded frame was not recycled")
	}
}

func TestCameraStreamErrorKeepsSnapshotFallback(t *testing.T) {
	fallback := image.NewRGBA(image.Rect(0, 0, 16, 16))
	cameras := &show.Cameras{SelectedID: "front", StreamURL: "rtsps://camera.invalid/live"}
	renderer := &renderer{
		screenWidth:      960,
		screenHeight:     480,
		state:            show.Snapshot{Cameras: cameras},
		cameraFrameImage: fallback,
		cameraFrameShown: "snapshot:front",
	}
	controller := newCameraStreamController()
	controller.identity = cameraStreamIdentity(cameras, renderer.cameraVideoBounds().Size())

	renderer.applyCameraStream(controller, cameraStreamResult{
		identity: controller.identity,
		err:      errors.New("decoder stopped"),
	})

	if renderer.cameraFrameImage != fallback || renderer.cameraFrameShown != "snapshot:front" {
		t.Fatal("decoder retry cleared the snapshot fallback")
	}
	if renderer.cameraStreamShown {
		t.Fatal("snapshot fallback was incorrectly marked as a live stream")
	}
}

func TestCameraStreamErrorKeepsLastLiveFrame(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 16, 16))
	cameras := &show.Cameras{SelectedID: "front", StreamURL: "rtsps://camera.invalid/live"}
	renderer := &renderer{
		screenWidth:       960,
		screenHeight:      480,
		state:             show.Snapshot{Cameras: cameras},
		cameraFrameImage:  frame,
		cameraFrameShown:  "stream:front:1",
		cameraStreamImage: frame,
		cameraStreamShown: true,
	}
	controller := newCameraStreamController()
	controller.identity = cameraStreamIdentity(cameras, renderer.cameraVideoBounds().Size())

	renderer.applyCameraStream(controller, cameraStreamResult{
		identity: controller.identity,
		err:      errors.New("decoder stopped"),
	})

	if renderer.cameraStreamImage != frame || renderer.cameraFrameImage != frame || !renderer.cameraStreamShown {
		t.Fatal("decoder retry cleared the last good live frame")
	}
}

func TestSatChatVideoUsesTheVideoCoreFloor(t *testing.T) {
	state := show.Snapshot{SatChat: &show.SatChat{Call: &show.SatChatCall{State: "active", Mode: "video"}}}
	if !satChatVideoActive(state) {
		t.Fatal("active Sat Chat video was not recognized")
	}
	state.SatChat.Call.Mode = "audio"
	if satChatVideoActive(state) {
		t.Fatal("audio-only Sat Chat requested the video core floor")
	}
}

func TestNativeCameraRelayUsesTheVideoCoreFloor(t *testing.T) {
	state := show.Snapshot{Cameras: &show.Cameras{SelectedID: "show-kitchen", StreamActive: true}}
	if !cameraVideoActive(state) {
		t.Fatal("active native camera stream was not recognized")
	}
	state.Cameras.StreamActive = false
	if cameraVideoActive(state) {
		t.Fatal("snapshot-only camera requested the video core floor")
	}
}
