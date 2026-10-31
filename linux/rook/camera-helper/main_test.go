package main

import (
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEchoStreamUsesRookFrameRates(t *testing.T) {
	if got := cameraStreamPeriod("echo-low"); got != time.Second/20 {
		t.Fatalf("echo stream period = %v, want %v", got, time.Second/20)
	}
	if got := cameraStreamPeriod("echo-balanced"); got != time.Second/20 {
		t.Fatalf("balanced fallback stream period = %v, want %v", got, time.Second/20)
	}
	if got := cameraStreamPeriod("video"); got != time.Second/15 {
		t.Fatalf("normal stream period = %v, want %v", got, time.Second/15)
	}
}

func TestCameraMuteCombinesLogicalAndHardwarePrivacy(t *testing.T) {
	root := t.TempDir()
	logicalPath := filepath.Join(root, "state.json")
	privacyPath := filepath.Join(root, "privacy_state")
	write := func(path, value string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(logicalPath, `{"muted":false}`)
	write(privacyPath, "1\n")
	muted, err := currentMuteStateFromPaths(logicalPath, []string{privacyPath})
	if err != nil || !muted {
		t.Fatalf("hardware privacy was not enforced: muted=%t err=%v", muted, err)
	}

	write(logicalPath, `{"muted":true}`)
	write(privacyPath, "0\n")
	muted, err = currentMuteStateFromPaths(logicalPath, []string{privacyPath})
	if err != nil || !muted {
		t.Fatalf("logical mute was not enforced: muted=%t err=%v", muted, err)
	}
}

func TestCameraPrivacyStateFailsClosedWhenMalformed(t *testing.T) {
	root := t.TempDir()
	logicalPath := filepath.Join(root, "state.json")
	privacyPath := filepath.Join(root, "privacy_state")
	if err := os.WriteFile(logicalPath, []byte(`{"muted":false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privacyPath, []byte("maybe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := currentMuteStateFromPaths(logicalPath, []string{privacyPath}); err == nil {
		t.Fatal("malformed hardware privacy state did not fail closed")
	}
}

func TestParseMuteStateFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name, data string
		want       bool
		wantError  bool
	}{
		{"unmuted", `{"muted":false}`, false, false},
		{"muted", `{"muted":true}`, true, false},
		{"missing", `{}`, false, true},
		{"invalid", `{`, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseMuteState([]byte(test.data))
			if got != test.want || (err != nil) != test.wantError {
				t.Fatalf("parseMuteState(%q) = %t, %v", test.data, got, err)
			}
		})
	}
}

func TestEchoVideoImageCapsLargeFramesWithoutUpscalingSmallCamera(t *testing.T) {
	for _, test := range []struct {
		name   string
		source image.Point
		want   image.Point
	}{
		{name: "large", source: image.Pt(800, 600), want: image.Pt(400, 225)},
		{name: "native Rook", source: image.Pt(320, 240), want: image.Pt(320, 180)},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := echoVideoImage(image.NewRGBA(image.Rectangle{Max: test.source}), nil)
			if got.Bounds().Size() != test.want {
				t.Fatalf("echo video size = %v, want %v", got.Bounds().Size(), test.want)
			}
		})
	}
}
