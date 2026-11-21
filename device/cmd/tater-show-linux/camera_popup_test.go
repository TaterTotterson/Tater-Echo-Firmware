//go:build linux

package main

import (
	"testing"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

func TestDoorbellPopupOpensWithoutMenuAndRestoresWorkspaceAtExpiry(t *testing.T) {
	for _, target := range []string{"checkers", "crown", "cronos", "athena", "rook"} {
		t.Run(target, func(t *testing.T) {
			now := time.Now()
			r := &renderer{target: target, settingsOpen: true, state: show.Snapshot{
				Connected: true, Phase: "idle", Cameras: &show.Cameras{
					SelectedID: "camera.front", PopupID: "ring-1", PopupExpiresAtUnixMS: now.Add(time.Minute).UnixMilli(),
				},
			}}
			if !r.cameraVisible(now) || r.cameraOpen || r.settingsVisible(now) {
				t.Fatal("doorbell must overlay the current workspace without opening the manual camera menu")
			}
			if got := r.gpuSurface(now); got != "camera-video:camera.front" {
				t.Fatalf("popup GPU surface = %q", got)
			}
			later := now.Add(2 * time.Minute)
			if r.cameraVisible(later) || !r.settingsVisible(later) || r.gpuSurface(later) != "settings::0" {
				t.Fatalf("expiry did not restore settings: visible=%t settings=%t surface=%s", r.cameraVisible(later), r.settingsVisible(later), r.gpuSurface(later))
			}
		})
	}
}

func TestDismissedPopupDoesNotReopenForNewFramesButNextPressCanOpen(t *testing.T) {
	now := time.Now()
	r := &renderer{state: show.Snapshot{Connected: true, Cameras: &show.Cameras{
		SelectedID: "camera.front", PopupID: "ring-1", PopupExpiresAtUnixMS: now.Add(time.Minute).UnixMilli(),
	}}}
	r.cameraPopupDismissedID = "ring-1"
	r.state.Cameras.FrameURL = "http://127.0.0.1/new-frame"
	if r.cameraVisible(now) {
		t.Fatal("dismissed popup reopened on a frame update")
	}
	r.state.Cameras.PopupID = "ring-2"
	if !r.cameraVisible(now) {
		t.Fatal("next doorbell press did not open")
	}
	r.state.Connected = false
	if r.cameraVisible(now) {
		t.Fatal("disconnected popup stayed visible")
	}
}

func TestOrdinaryCameraPayloadDoesNotOpenViewerAndManualMenuStillWorks(t *testing.T) {
	r := &renderer{state: show.Snapshot{Connected: true, Cameras: &show.Cameras{SelectedID: "camera.front"}}}
	if r.cameraVisible(time.Now()) {
		t.Fatal("legacy camera update unexpectedly opened the viewer")
	}
	r.cameraOpen = true
	if !r.cameraVideoActive() {
		t.Fatal("manual camera menu stopped working")
	}
}
