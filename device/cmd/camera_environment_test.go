package main

import (
	"strings"
	"testing"
)

func TestSlottedCameraEnablesQualifiedEncoderWithoutChangingHardwarePlayback(t *testing.T) {
	if !cameraH264CaptureEnabled {
		t.Fatal("Checkers/Cronos hardware capture integration must remain enabled")
	}
	for _, target := range []string{"checkers", "cronos", "crown", "athena", "rook", ""} {
		base := []string{"PATH=/usr/bin", "TATER_CAMERA_ADDRESS=old", "TATER_VENC_ENABLE=0", "TATER_VDEC_ENABLE=1", "TATER_GPU_ENABLE=1"}
		got := slottedCameraEnvironment(base, target, "127.0.0.1:43824")
		expected := "0"
		if target == "checkers" || target == "cronos" || target == "crown" || target == "rook" {
			expected = "1"
		}
		joined := strings.Join(got, "\n")
		if strings.Count(joined, "TATER_VENC_ENABLE=") != 1 || !strings.Contains(joined, "TATER_VENC_ENABLE="+expected) ||
			strings.Count(joined, "TATER_CAMERA_ADDRESS=") != 1 || !strings.Contains(joined, "TATER_CAMERA_ADDRESS=127.0.0.1:43824") {
			t.Fatal(target, got)
		}
		if base[1] != "TATER_CAMERA_ADDRESS=old" {
			t.Fatal("mutated inherited environment")
		}
		for _, playback := range []string{"TATER_VDEC_ENABLE=1", "TATER_GPU_ENABLE=1"} {
			if !strings.Contains(joined, playback) {
				t.Fatal("camera policy disabled playback", target, got)
			}
		}
	}
}

func TestHardwareCapturePreservesNegotiatedProfilesAndMJPEGFallback(t *testing.T) {
	for _, profile := range []string{"echo-h264", "echo-h264-balanced", "", "video", "echo-low", "echo-balanced"} {
		if got := satChatCameraCaptureProfile(profile); got != profile {
			t.Fatal("changed negotiated capture profile", profile, got)
		}
	}
}

func TestAthenaCameraRequiresSeparateMatchedSystemOptIn(t *testing.T) {
	for _, value := range []string{"", "0", "1", "true"} {
		got := slottedCameraEnvironment([]string{"TATER_ATHENA_AP_ENABLE=" + value}, "athena", "127.0.0.1:43824")
		enabled := "0"
		if value == "1" {
			enabled = "1"
		}
		if !strings.Contains(strings.Join(got, "\n"), "TATER_VENC_ENABLE="+enabled) {
			t.Fatal(value, got)
		}
	}
}
