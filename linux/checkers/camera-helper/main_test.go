package main

import (
	"errors"
	"image"
	"testing"
	"time"
)

type countedLiveCamera struct {
	normal, motion, releases int
	err                      error
}

func (c *countedLiveCamera) lease() (func(), error) {
	if c.err != nil {
		return nil, c.err
	}
	return func() { c.releases++ }, nil
}
func (c *countedLiveCamera) Acquire() (func(), error) {
	c.normal++
	return c.lease()
}
func (c *countedLiveCamera) AcquireTaterLiveVideo() (func(), error) {
	c.motion++
	return c.lease()
}

func TestMotionExposureOnlyForQualifiedLiveStreams(t *testing.T) {
	for _, target := range []string{"checkers", "cronos", "crown", "athena", "rook", ""} {
		t.Run(target, func(t *testing.T) {
			t.Setenv("TATER_FIRMWARE_TARGET", target)
			device := &countedLiveCamera{}
			release, err := acquireLiveCamera(device)
			if err != nil {
				t.Fatal(err)
			}
			release()
			qualified := target == "checkers" || target == "cronos"
			if (device.motion == 1) != qualified || (device.normal == 1) == qualified || device.releases != 1 {
				t.Fatal("incorrect live camera lease", device)
			}
			device.err = errors.New("privacy is on")
			if release, err := acquireLiveCamera(device); err == nil || release != nil {
				t.Fatal("bypassed camera acquisition/privacy failure")
			}
		})
	}
}

func TestEchoStreamUsesProfileFrameRates(t *testing.T) {
	t.Setenv("TATER_FIRMWARE_TARGET", "crown")
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

func TestReducedLiveCaptureIsLimitedToCheckersAndCronos(t *testing.T) {
	for _, target := range []string{"checkers", "cronos", "crown", "athena", "rook", ""} {
		t.Run(target, func(t *testing.T) {
			t.Setenv("TATER_FIRMWARE_TARGET", target)
			qualified := target == "checkers" || target == "cronos"
			for _, profile := range []string{"video", "echo-balanced"} {
				frame := &countedSnapshotFrame{}
				got, _ := snapshotImage(frame, profile)
				want := image.Pt(640, 360)
				period := time.Second / 15
				if qualified {
					want = image.Pt(480, 270)
					period = time.Second / 20
					if frame.smooth != 1 || frame.live != 0 {
						t.Fatal("developed a larger intermediate image", profile, frame)
					}
				} else if frame.smooth != 0 || frame.live != 1 {
					t.Fatal("changed an unqualified camera", profile, frame)
				}
				if got.Bounds().Size() != want || cameraStreamPeriod(profile) != period || frame.full != 0 {
					t.Fatal(profile, got.Bounds(), cameraStreamPeriod(profile), frame)
				}
			}
			frame := &countedSnapshotFrame{}
			still, quality := snapshotImage(frame, "")
			if still.Bounds().Size() != image.Pt(1280, 720) || quality != snapshotJPEGQuality || frame.full != 1 || frame.smooth != 0 {
				t.Fatal("changed native still photos", frame)
			}
			low, _ := snapshotImage(frame, "echo-low")
			if low.Bounds().Size() != image.Pt(320, 180) || frame.low != 1 || cameraStreamPeriod("echo-low") != time.Second/20 {
				t.Fatal("changed existing low profile", frame)
			}
		})
	}
}

func TestSmoothLiveImageReusesItsBuffer(t *testing.T) {
	t.Setenv("TATER_FIRMWARE_TARGET", "cronos")
	frame := &countedSnapshotFrame{}
	first, quality := liveVideoImage(frame, "echo-balanced", nil)
	second, secondQuality := liveVideoImage(frame, "echo-balanced", first)
	if first != second || quality != balancedJPEGQuality || secondQuality != quality || frame.smooth != 2 || frame.live != 0 {
		t.Fatal("live video allocated/developed an unnecessary intermediate", frame)
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
