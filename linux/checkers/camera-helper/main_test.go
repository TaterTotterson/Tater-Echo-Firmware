package main

import (
	"image"
	"testing"
	"time"
)

func TestEchoStreamUsesProfileFrameRates(t *testing.T) {
	if got := cameraStreamPeriod("echo-low"); got != time.Second/20 {
		t.Fatalf("echo stream period = %v, want %v", got, time.Second/20)
	}
	if got := cameraStreamPeriod("echo-balanced"); got != time.Second/15 {
		t.Fatalf("balanced stream period = %v, want %v", got, time.Second/15)
	}
	if got := cameraStreamPeriod("video"); got != time.Second/15 {
		t.Fatalf("normal stream period = %v, want %v", got, time.Second/15)
	}
}

func TestEchoBalancedVideoIs640By360AndReusesBuffer(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 800, 600))
	first := echoBalancedVideoImage(source, nil)
	if got, want := first.Bounds().Size(), image.Pt(640, 360); got != want {
		t.Fatalf("balanced Echo video = %v, want %v", got, want)
	}
	if second := echoBalancedVideoImage(source, first); second != first {
		t.Fatal("balanced Echo video did not reuse its frame buffer")
	}
}
