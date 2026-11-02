//go:build linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/linuxinput"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
	"golang.org/x/image/font"
)

func TestDisplayThemesKeepTaterDefaultAndUsePrimaryColorForReply(t *testing.T) {
	wantTater := color.RGBA{255, 132, 48, 255}
	if got := accentForTheme("idle", ""); got != wantTater {
		t.Fatalf("empty theme did not preserve the Tater default: got=%v want=%v", got, wantTater)
	}
	if got := accentForTheme("speaking", "tater"); got != wantTater {
		t.Fatalf("Tater reply glow did not use the main orange: got=%v want=%v", got, wantTater)
	}

	seen := map[color.RGBA]string{}
	for _, theme := range []string{"tater", "ocean", "violet", "forest", "sunset"} {
		main := accentForTheme("idle", theme)
		if previous := seen[main]; previous != "" {
			t.Fatalf("themes %s and %s share the same primary color %v", previous, theme, main)
		}
		seen[main] = theme
		if reply := accentForTheme("speaking", theme); reply != main {
			t.Errorf("%s reply glow=%v, want main screen color=%v", theme, reply, main)
		}
	}
	if got := accentForTheme("idle", "not-a-theme"); got != wantTater {
		t.Fatalf("unknown theme did not safely fall back to Tater: %v", got)
	}
}

func TestMenuThemesUseDedicatedTwoColorPalettes(t *testing.T) {
	expectedSecondary := map[string]color.RGBA{
		"tater":  {174, 189, 209, 255},
		"ocean":  {73, 230, 194, 255},
		"violet": {239, 141, 255, 255},
		"forest": {212, 185, 95, 255},
		"sunset": {255, 208, 106, 255},
	}
	for theme, secondary := range expectedSecondary {
		got := menuPaletteForTheme(theme)
		if want := paletteForTheme(theme).primary; got.primary != want {
			t.Errorf("%s menu primary=%v, want theme primary %v", theme, got.primary, want)
		}
		if got.secondary != secondary {
			t.Errorf("%s menu secondary=%v, want %v", theme, got.secondary, secondary)
		}
	}
	tater := menuPaletteForTheme("tater")
	if tater.primary != (color.RGBA{255, 132, 48, 255}) || tater.secondary.G >= 200 {
		t.Fatalf("Tater menu did not use orange and neutral gray: %+v", tater)
	}
}

func TestMenuNamesShrinkBeforeEllipsizing(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	r := &renderer{faces: faces}
	const name = "Tater Checkers Office"
	got, size := r.menuName(name, 16, 9, 154)
	if got != name {
		t.Fatalf("ordinary satellite name=%q, want full %q", got, name)
	}
	if size >= 16 {
		t.Fatalf("ordinary satellite name size=%d, want a smaller fitting size", size)
	}
	if textWidth(faces.bold[size], got) > 154 {
		t.Fatalf("ordinary satellite name width exceeds its Rook slot at size %d", size)
	}
	veryLong := "Tater Checkers Extremely Long Upstairs West Office"
	got, size = r.menuName(veryLong, 16, 9, 100)
	if !strings.HasSuffix(got, "…") || textWidth(faces.bold[size], got) > 100 {
		t.Fatalf("extreme menu name=%q size=%d does not use bounded fallback", got, size)
	}
}

func TestClimateTemperaturesAlwaysDisplayAsWholeDegrees(t *testing.T) {
	for _, test := range []struct {
		value float64
		want  string
	}{
		{value: 71, want: "71°"},
		{value: 71.4, want: "71°"},
		{value: 72.5, want: "73°"},
		{value: 71.6, want: "72°"},
		{value: -2.5, want: "-3°"},
	} {
		value := test.value
		if got := climateTemperature(&value, "F"); got != test.want {
			t.Errorf("thermostat temperature %v = %q, want %q", test.value, got, test.want)
		}
		if got := climateRoomTemperature(test.value); got != test.want {
			t.Errorf("room temperature %v = %q, want %q", test.value, got, test.want)
		}
	}
}

func TestRedNightClockYieldsToActiveSatelliteScreens(t *testing.T) {
	now := time.Date(2026, time.October, 24, 23, 15, 0, 0, time.UTC)
	idle := show.Snapshot{Connected: true, Phase: "idle", ScreenNightDisplay: "red_clock"}
	if !(&renderer{state: idle, nightModeActive: true}).nightClockVisible(now) {
		t.Fatal("idle scheduled red clock was not visible")
	}

	for name, state := range map[string]show.Snapshot{
		"voice": {Connected: true, Phase: "listening", ScreenNightDisplay: "red_clock"},
		"timer": {Connected: true, Phase: "idle", ScreenNightDisplay: "red_clock", Timer: &show.Timer{Active: true}},
		"media": {Connected: true, Phase: "idle", ScreenNightDisplay: "red_clock", Media: &show.Media{Active: true}},
		"call":  {Connected: true, Phase: "idle", ScreenNightDisplay: "red_clock", SatChat: &show.SatChat{Call: &show.SatChatCall{}}},
	} {
		if (&renderer{state: state, nightModeActive: true}).nightClockVisible(now) {
			t.Errorf("red clock covered the active %s screen", name)
		}
	}
	if (&renderer{state: idle, nightModeActive: false}).nightClockVisible(now) {
		t.Fatal("red clock was visible outside the scheduled night window")
	}
}

func TestRedNightClockReturnsImmediatelyAfterVolumeOverlay(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.October, 24, 23, 15, 0, 0, time.UTC)
	r := &renderer{
		faces: faces, target: "athena", screenWidth: 1280, screenHeight: 800,
		state: show.Snapshot{
			Connected: true, Phase: "idle", ScreenNightDisplay: "red_clock",
			TaterTimeUnixMS: now.UnixMilli(), VolumePercent: 42,
		},
		nightModeActive: true,
	}
	canvas := image.NewRGBA(image.Rect(0, 0, 1280, 800))
	r.render(canvas, now)
	clock := append([]byte(nil), canvas.Pix...)
	r.beginVolumeOverlay(42, now.Add(time.Second))
	r.render(canvas, now.Add(time.Second))
	if bytes.Equal(clock, canvas.Pix) {
		t.Fatal("volume overlay did not cover the red clock")
	}
	r.render(canvas, now.Add(time.Second+volumeOverlayDuration+time.Millisecond))
	if !bytes.Equal(clock, canvas.Pix) {
		t.Fatal("red clock did not redraw immediately after the volume overlay")
	}
}

func TestVolumeOverlayUsesThemeAccentAndExpiresCleanly(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	r := &renderer{
		faces: faces, target: "checkers", screenWidth: 960, screenHeight: 480,
		state: show.Snapshot{
			Phase: "idle", Connected: true, DeviceName: "Tater Checkers",
			Room: "Office", DisplayTheme: "tater", VolumePercent: 64,
			TaterTimeUnixMS: now.UnixMilli(),
		},
		animationStart: now,
	}
	canvas := image.NewRGBA(image.Rect(0, 0, 960, 480))
	r.beginVolumeOverlay(64, now)
	r.render(canvas, now)
	if !imageContainsColor(canvas, paletteForTheme("tater").primary) {
		t.Fatal("volume overlay did not use the Tater orange accent")
	}
	if r.volumeOverlayBase == nil || !r.volumeOverlayPainted {
		t.Fatal("volume overlay did not retain a clean base frame")
	}
	overlayHash := sha256.Sum256(canvas.Pix)

	r.render(canvas, now.Add(volumeOverlayDuration+time.Millisecond))
	if r.volumeOverlayBase != nil || r.volumeOverlayCanvas != nil || r.volumeOverlayPainted {
		t.Fatal("expired volume overlay retained transient frame state")
	}
	if got := sha256.Sum256(canvas.Pix); got == overlayHash {
		t.Fatal("expired volume overlay did not restore the current screen")
	}
}

func TestVolumeOverlayUpdatesAndExtendsForRepeatedButtonPresses(t *testing.T) {
	start := time.Unix(1_800_000_000, 0)
	r := &renderer{}
	r.beginVolumeOverlay(37, start)
	if r.volumeOverlayPercent != 37 || !r.volumeOverlayUntil.Equal(start.Add(volumeOverlayDuration)) {
		t.Fatalf("first overlay = %d until %v", r.volumeOverlayPercent, r.volumeOverlayUntil)
	}
	r.volumeOverlayPainted = true
	r.beginVolumeOverlay(44, start.Add(time.Second))
	if r.volumeOverlayPercent != 44 || !r.volumeOverlayUntil.Equal(start.Add(time.Second+volumeOverlayDuration)) {
		t.Fatalf("updated overlay = %d until %v", r.volumeOverlayPercent, r.volumeOverlayUntil)
	}
	if r.volumeOverlayPainted {
		t.Fatal("updated volume was not scheduled for repaint")
	}
}

func imageContainsColor(value *image.RGBA, want color.RGBA) bool {
	for y := value.Rect.Min.Y; y < value.Rect.Max.Y; y++ {
		for x := value.Rect.Min.X; x < value.Rect.Max.X; x++ {
			if value.RGBAAt(x, y) == want {
				return true
			}
		}
	}
	return false
}

func TestReducedSensorRowsKeepFullProfileSpacing(t *testing.T) {
	if got, want := rightAlignedMetricCell(100, 600, 6, 2, 0), image.Rect(500, 0, 600, 1); got != want {
		t.Fatalf("first reduced rectangular sensor cell = %v, want %v", got, want)
	}
	if got, want := rightAlignedMetricCell(100, 600, 6, 2, 1), image.Rect(600, 0, 700, 1); got != want {
		t.Fatalf("second reduced rectangular sensor cell = %v, want %v", got, want)
	}
	if got, want := centeredMetricCell(100, 600, 6, 2, 0), image.Rect(300, 0, 400, 1); got != want {
		t.Fatalf("first reduced Rook sensor cell = %v, want %v", got, want)
	}
	if got, want := centeredMetricCell(100, 600, 6, 2, 1), image.Rect(400, 0, 500, 1); got != want {
		t.Fatalf("second reduced Rook sensor cell = %v, want %v", got, want)
	}
}

func TestEmbeddedPhotoHomeAssetsCoverBothScreens(t *testing.T) {
	files, err := embeddedWallpaperFiles()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(files), defaultWallpaperCount()*2; got != want {
		t.Fatalf("embedded wallpaper count=%d, want %d: %v", got, want, files)
	}
	for _, target := range []struct {
		name          string
		width, height int
	}{{"checkers", 960, 480}, {"crown", 1280, 800}, {"athena", 1280, 800}, {"rook", 1280, 800}} {
		for index := 0; index < defaultWallpaperCount(); index++ {
			picture, identity, loadErr := readDefaultWallpaper(target.name, index)
			if loadErr != nil {
				t.Fatalf("read %s wallpaper %d: %v", target.name, index, loadErr)
			}
			if bounds := picture.Bounds(); bounds.Dx() != target.width || bounds.Dy() != target.height {
				t.Errorf("%s is %dx%d, want %dx%d", identity, bounds.Dx(), bounds.Dy(), target.width, target.height)
			}
		}
	}
	for _, kind := range []string{"sun", "moon", "partly", "partly-night", "cloud", "rain", "storm", "snow", "fog"} {
		art := weatherArt(kind)
		if art == nil {
			t.Errorf("missing static weather artwork for %q", kind)
			continue
		}
		if bounds := art.Bounds(); bounds.Dx() != 128 || bounds.Dy() != 128 {
			t.Errorf("%s weather artwork is %dx%d, want 128x128", kind, bounds.Dx(), bounds.Dy())
		}
		if weatherArtTouchesMargin(art, 6) {
			t.Errorf("%s weather artwork touches its safety margin; the icon may be cropped or contain a neighboring icon", kind)
		}
	}
	for input, want := range map[string]string{
		"clear": "sun", "sunny": "sun", "clear-night": "moon",
		"partly cloudy": "partly", "partly cloudy night": "partly-night",
		"overcast": "cloud", "rain showers": "rain", "thunderstorm": "storm",
		"snowy": "snow", "foggy": "fog",
	} {
		if got := weatherArt(input); got != weatherArt(want) {
			t.Errorf("weather artwork for %q did not select %q", input, want)
		}
	}
}

func weatherArtTouchesMargin(art image.Image, margin int) bool {
	bounds := art.Bounds()
	inner := bounds.Inset(margin)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if image.Pt(x, y).In(inner) {
				continue
			}
			_, _, _, alpha := art.At(x, y).RGBA()
			if alpha != 0 {
				return true
			}
		}
	}
	return false
}

func TestWallpaperRotationPresetsUseLocalBoundaries(t *testing.T) {
	location := time.FixedZone("test", -5*60*60)
	before := time.Date(2026, time.October, 14, 5, 59, 0, 0, location)
	after := time.Date(2026, time.October, 14, 6, 0, 0, 0, location)

	if got := wallpaperRotationIndex(before, 7, 0); got != 0 {
		t.Fatalf("fixed wallpaper index = %d, want 0", got)
	}
	if got, want := wallpaperRotationIndex(after, 7, 360), (wallpaperRotationIndex(before, 7, 360)+1)%7; got != want {
		t.Fatalf("six-hour boundary index = %d, want %d", got, want)
	}
	insideHour := time.Date(2026, time.October, 14, 5, 10, 0, 0, location)
	if got, want := wallpaperRotationIndex(insideHour.Add(30*time.Minute), 7, 60), wallpaperRotationIndex(insideHour, 7, 60); got != want {
		t.Fatalf("hourly index changed inside its hour: got=%d want=%d", got, want)
	}
	dailyBefore := time.Date(2026, time.October, 14, 23, 59, 0, 0, location)
	dailyAfter := dailyBefore.Add(time.Minute)
	if got, want := wallpaperRotationIndex(dailyAfter, 7, 1440), (wallpaperRotationIndex(dailyBefore, 7, 1440)+1)%7; got != want {
		t.Fatalf("daily boundary index = %d, want %d", got, want)
	}
}

func TestSatChatVideoQueueKeepsOnlyNewestPendingFrame(t *testing.T) {
	requests := make(chan screenImageRequest, 1)
	queueLatestScreenImageRequest(requests, screenImageRequest{
		kind: satChatVideoScreenImage, identity: "frame-1", session: "call-1", rawURL: "http://127.0.0.1/frame-1",
	})
	queueLatestScreenImageRequest(requests, screenImageRequest{
		kind: satChatVideoScreenImage, identity: "frame-2", session: "call-1", rawURL: "http://127.0.0.1/frame-2",
	})

	select {
	case request := <-requests:
		if request.identity != "frame-2" || request.rawURL != "http://127.0.0.1/frame-2" {
			t.Fatalf("pending video request = %+v, want newest frame", request)
		}
	default:
		t.Fatal("newest Sat Chat video request was not queued")
	}
}

func TestSatChatVideoAppliesCompletedFrameFromCurrentCall(t *testing.T) {
	current := image.NewRGBA(image.Rect(0, 0, 2, 2))
	other := image.NewRGBA(image.Rect(0, 0, 3, 3))
	r := &renderer{state: show.Snapshot{SatChat: &show.SatChat{
		Call: &show.SatChatCall{ID: "call-1", State: "active", Mode: "video"},
	}}}

	// A newer URL may already be queued by the time this sequential decode
	// completes. It still belongs to the live call and must reach the screen.
	r.satChatVideoID = "frame-2"
	r.applyScreenImage(screenImageResult{
		kind: satChatVideoScreenImage, identity: "frame-1", session: "call-1", image: current,
	})
	if r.satChatVideoImage == nil || r.satChatVideoShown != "frame-1" {
		t.Fatal("completed frame from the active call was discarded")
	}
	accepted := r.satChatVideoImage

	r.applyScreenImage(screenImageResult{
		kind: satChatVideoScreenImage, identity: "frame-3", session: "old-call", image: other,
	})
	if r.satChatVideoImage != accepted || r.satChatVideoShown != "frame-1" {
		t.Fatal("frame from a different call replaced the active call image")
	}
}

func TestSatChatVideoIsPreparedAtDisplaySizeAndStableFramesDoNotRepaint(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	r := &renderer{
		faces: faces, target: "crown", screenWidth: 1280, screenHeight: 800,
		state: show.Snapshot{Connected: true, SatChat: &show.SatChat{
			Call: &show.SatChatCall{ID: "call-1", State: "active", Mode: "video"},
		}},
	}
	r.applyScreenImage(screenImageResult{
		kind: satChatVideoScreenImage, identity: "frame-1", session: "call-1",
		image: prepareCoverImage(image.NewRGBA(image.Rect(0, 0, 640, 360)), r.satChatVideoBounds().Size()),
	})
	if got, want := r.satChatVideoImage.Bounds().Size(), r.satChatVideoBounds().Size(); got != want {
		t.Fatalf("prepared camera frame size = %v, want %v", got, want)
	}
	canvas := image.NewRGBA(image.Rect(0, 0, 1280, 800))
	r.render(canvas, now)
	if len(r.presentRegions) == 0 {
		t.Fatal("new Sat Chat frame was not presented")
	}
	r.render(canvas, now.Add(time.Second/30))
	if len(r.presentRegions) != 0 {
		t.Fatalf("unchanged Sat Chat frame repainted %d regions", len(r.presentRegions))
	}
	r.applyScreenImage(screenImageResult{
		kind: satChatVideoScreenImage, identity: "frame-2", session: "call-1",
		image: prepareCoverImage(image.NewRGBA(image.Rect(0, 0, 640, 360)), r.satChatVideoBounds().Size()),
	})
	r.render(canvas, now.Add(2*time.Second/30))
	if len(r.presentRegions) != 1 || r.presentRegions[0] != r.satChatVideoContentBounds(1280, 800) {
		t.Fatalf("new camera frame regions = %v, want only video content %v", r.presentRegions, r.satChatVideoContentBounds(1280, 800))
	}
	if !r.transientPresent {
		t.Fatal("steady Sat Chat frame did not select transient framebuffer presentation")
	}
}

func TestPrepareCoverImageExpandsNearestPixelsWithoutChangingColors(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 2, 2))
	source.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	source.SetRGBA(1, 0, color.RGBA{G: 255, A: 255})
	source.SetRGBA(0, 1, color.RGBA{B: 255, A: 255})
	source.SetRGBA(1, 1, color.RGBA{R: 255, G: 255, A: 255})
	prepared, ok := prepareCoverImage(source, image.Pt(6, 6)).(*image.RGBA)
	if !ok {
		t.Fatal("prepared frame is not RGBA")
	}
	for _, test := range []struct {
		point image.Point
		want  color.RGBA
	}{
		{image.Pt(0, 0), color.RGBA{R: 255, A: 255}},
		{image.Pt(2, 1), color.RGBA{R: 255, A: 255}},
		{image.Pt(3, 0), color.RGBA{G: 255, A: 255}},
		{image.Pt(0, 5), color.RGBA{B: 255, A: 255}},
		{image.Pt(5, 5), color.RGBA{R: 255, G: 255, A: 255}},
	} {
		if got := prepared.RGBAAt(test.point.X, test.point.Y); got != test.want {
			t.Fatalf("prepared pixel %v = %v, want %v", test.point, got, test.want)
		}
	}
}

func TestLeavingSatChatForcesImmediateFullHomeRestore(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	canvas := image.NewRGBA(image.Rect(0, 0, 960, 480))
	r := &renderer{
		faces: faces, target: "checkers", screenWidth: 960, screenHeight: 480,
		animationStart: now,
		state:          show.Snapshot{Connected: true, Phase: "idle", SatChat: &show.SatChat{}},
	}
	accent := accentForTheme(r.state.Phase, r.state.DisplayTheme)
	r.checkersBase = image.NewRGBA(canvas.Rect)
	draw.Draw(r.checkersBase, r.checkersBase.Rect, image.NewUniform(color.RGBA{R: 12, G: 24, B: 36, A: 255}), image.Point{}, draw.Src)
	r.checkersBaseKey = r.checkersCacheKey(now, accent)
	r.checkersCanvas = canvas
	r.satChatFrameCanvas = canvas
	r.satChatFrameKey = "active-video-call"
	draw.Draw(canvas, canvas.Rect, image.NewUniform(color.RGBA{R: 220, G: 40, B: 30, A: 255}), image.Point{}, draw.Src)

	r.render(canvas, now)

	if len(r.presentRegions) != 1 || r.presentRegions[0] != canvas.Rect {
		t.Fatalf("first frame after Sat Chat presented %v, want the complete home screen", r.presentRegions)
	}
	if got := canvas.RGBAAt(400, 240); got != (color.RGBA{R: 12, G: 24, B: 36, A: 255}) {
		t.Fatalf("old Sat Chat pixel survived home restore: %v", got)
	}
}

func TestSatChatVideoUsesTargetSpecificUncroppedArea(t *testing.T) {
	tests := []struct {
		name        string
		renderer    renderer
		wantContent image.Rectangle
		wantLargest bool
	}{
		{
			name:        "crown",
			renderer:    renderer{target: "crown", screenWidth: 1280, screenHeight: 800},
			wantContent: image.Rect(192, 148, 1088, 652),
		},
		{
			name:        "checkers",
			renderer:    renderer{target: "checkers", screenWidth: 960, screenHeight: 480},
			wantContent: image.Rect(56, 1, 904, 478),
			wantLargest: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := test.renderer.satChatVideoBounds()
			if got != test.wantContent {
				t.Fatalf("video content = %v, want %v", got, test.wantContent)
			}
			if got.Dx()*9 != got.Dy()*16 {
				t.Fatalf("video content %v is not exactly 16:9", got)
			}
			width, height := test.renderer.showSize()
			if test.wantLargest && got.Dx()+16 <= width && got.Dy()+9 <= height {
				t.Fatalf("video content %v does not use the largest 16:9 area in %dx%d", got, width, height)
			}
			if test.renderer.isCrown() {
				formerArea := 1280 * 720
				if got.Dx()*got.Dy()*100/formerArea != 49 {
					t.Fatalf("Crown video area = %d%% of former viewport, want 49%%", got.Dx()*got.Dy()*100/formerArea)
				}
			}
		})
	}
}

func TestCameraDirectoryUsesTargetSpecificPageSizes(t *testing.T) {
	for _, test := range []struct {
		name          string
		width, height int
		spot          bool
		want          int
	}{
		{name: "checkers", width: 960, height: 480, want: 5},
		{name: "crown", width: 1280, height: 800, want: 5},
		{name: "rook", width: 480, height: 480, spot: true, want: 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := cameraPageSize(test.width, test.height, test.spot); got != test.want {
				t.Fatalf("page size = %d, want %d", got, test.want)
			}
		})
	}
}

func TestCameraDirectoryPagesAndSelectsVisibleFeed(t *testing.T) {
	cameras := make([]show.Camera, 8)
	for index := range cameras {
		cameras[index] = show.Camera{ID: fmt.Sprintf("camera-%d", index), Name: fmt.Sprintf("Camera %d", index+1), Available: true}
	}
	var commands bytes.Buffer
	client := &socketClient{writer: bufio.NewWriter(&commands)}
	r := &renderer{
		target: "checkers", screenWidth: 960, screenHeight: 480, cameraOpen: true,
		state: show.Snapshot{Connected: true, Cameras: &show.Cameras{Available: true, Items: cameras}},
	}
	next := cameraNextBounds(960, 480, false)
	r.handleCameraTouch(client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: (next.Min.X + next.Max.X) / 2, Y: (next.Min.Y + next.Max.Y) / 2}, 960, 480, false)
	if r.cameraPage != 1 {
		t.Fatalf("next camera page = %d, want 1", r.cameraPage)
	}
	first := cameraCardBounds(960, 480, 0, false)
	r.handleCameraTouch(client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: (first.Min.X + first.Max.X) / 2, Y: (first.Min.Y + first.Max.Y) / 2}, 960, 480, false)
	if !strings.Contains(commands.String(), `"target":"camera-5"`) {
		t.Fatalf("first camera on page 2 = %q, want camera-5", commands.String())
	}
	r.handleCameraTouch(client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: (next.Min.X + next.Max.X) / 2, Y: (next.Min.Y + next.Max.Y) / 2}, 960, 480, false)
	if r.cameraPage != 1 {
		t.Fatalf("disabled next wrapped to page %d", r.cameraPage)
	}
}

func TestCameraNextPageRendersOnEveryScreen(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	cameras := make([]show.Camera, 8)
	for index := range cameras {
		cameras[index] = show.Camera{
			ID: fmt.Sprintf("camera-%d", index), Name: fmt.Sprintf("Camera %d", index+1), Available: true,
		}
	}
	now := time.Unix(1_800_000_000, 0)
	for _, test := range []struct {
		name          string
		width, height int
		spot          bool
	}{
		{name: "checkers", width: 960, height: 480},
		{name: "crown", width: 1280, height: 800},
		{name: "athena", width: 1280, height: 800},
		{name: "rook", width: 480, height: 480, spot: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var commands bytes.Buffer
			client := &socketClient{writer: bufio.NewWriter(&commands)}
			r := &renderer{
				faces: faces, target: test.name, spotScreen: test.spot,
				screenWidth: test.width, screenHeight: test.height, cameraOpen: true,
				state: show.Snapshot{Connected: true, Phase: "idle", Cameras: &show.Cameras{Available: true, Items: cameras}},
			}
			first := image.NewRGBA(image.Rect(0, 0, test.width, test.height))
			r.render(first, now)

			next := cameraNextBounds(test.width, test.height, test.spot)
			x, y := (next.Min.X+next.Max.X)/2, (next.Min.Y+next.Max.Y)/2
			handleQueuedTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: x, Y: y})
			handleQueuedTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: x, Y: y})
			if r.cameraPage != 1 {
				t.Fatalf("next camera page = %d, want 1", r.cameraPage)
			}

			second := image.NewRGBA(first.Rect)
			r.render(second, now)
			if sha256.Sum256(first.Pix) == sha256.Sum256(second.Pix) {
				t.Fatal("next changed the camera page but the display reused the first-page frame")
			}
		})
	}
}

func TestCrownCameraViewerCloseFitsAboveVideo(t *testing.T) {
	r := renderer{target: "crown", screenWidth: 1280, screenHeight: 800}
	closeBounds := r.cameraCloseBounds(1280, 800, false, true)
	if got, want := closeBounds, image.Rect(1172, 42, 1234, 104); got != want {
		t.Fatalf("Crown close bounds = %v, want %v", got, want)
	}
	if got, want := closeBounds.Dx(), 62; got != want {
		t.Fatalf("Crown close width = %d, want %d", got, want)
	}
	if got, want := closeBounds.Dy(), 62; got != want {
		t.Fatalf("Crown close height = %d, want %d", got, want)
	}
	videoBounds := r.cameraVideoBounds()
	if closeBounds.Max.Y > videoBounds.Min.Y {
		t.Fatalf("Crown close bounds %v overlap video bounds %v", closeBounds, videoBounds)
	}
	panel := cameraDirectoryLayoutFor(1280, 800, false).panel
	if !closeBounds.In(panel) {
		t.Fatalf("Crown close bounds %v extend beyond popup panel %v", closeBounds, panel)
	}
	talkBounds := r.cameraTalkBounds(1280, 800, false)
	if !talkBounds.In(panel) {
		t.Fatalf("Crown talk bounds %v extend beyond popup panel %v", talkBounds, panel)
	}
	if talkBounds.Min.Y <= videoBounds.Max.Y {
		t.Fatalf("Crown talk bounds %v overlap video bounds %v", talkBounds, videoBounds)
	}

	checkers := renderer{target: "checkers", screenWidth: 960, screenHeight: 480}
	if got, want := checkers.cameraCloseBounds(960, 480, false, true), image.Rect(878, 32, 924, 78); got != want {
		t.Fatalf("Checkers close bounds changed: got %v, want %v", got, want)
	}
	if got, want := checkers.cameraTalkBounds(960, 480, false), cameraTalkBounds(960, 480, false); got != want {
		t.Fatalf("Checkers talk bounds changed: got %v, want %v", got, want)
	}
}

func TestSatChatVideoBackdropHidesTheHomeScreen(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		renderer renderer
		bar      image.Point
	}{
		{
			name:     "crown top bar",
			renderer: renderer{faces: faces, target: "crown", screenWidth: 1280, screenHeight: 800},
			bar:      image.Pt(640, 10),
		},
		{
			name:     "checkers side bar",
			renderer: renderer{faces: faces, target: "checkers", screenWidth: 960, screenHeight: 480},
			bar:      image.Pt(10, 240),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := test.renderer
			r.state = show.Snapshot{Connected: true, SatChat: &show.SatChat{
				Call: &show.SatChatCall{ID: "call-1", State: "active", Mode: "video"},
			}}
			canvas := image.NewRGBA(image.Rect(0, 0, r.screenWidth, r.screenHeight))
			draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.RGBA{R: 220, G: 40, B: 30, A: 255}), image.Point{}, draw.Src)
			r.drawSatChat(canvas, time.Unix(1_800_000_000, 0), r.screenWidth, r.screenHeight, color.RGBA{255, 132, 48, 255})
			if got := canvas.RGBAAt(test.bar.X, test.bar.Y); got == (color.RGBA{R: 220, G: 40, B: 30, A: 255}) || got.A != 255 {
				t.Fatalf("backdrop pixel = %v, want an opaque replacement for the old screen", got)
			}
		})
	}
}

func TestCheckersThemeChangesReplyGlow(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	r := &renderer{faces: faces, animationStart: now, state: show.Snapshot{Phase: "speaking", Connected: true, DisplayEdgeGlow: true, AudioLevel: .8}}
	tater := image.NewRGBA(image.Rect(0, 0, 960, 480))
	r.state.DisplayTheme = "tater"
	r.render(tater, now)
	ocean := image.NewRGBA(tater.Rect)
	r.state.DisplayTheme = "ocean"
	r.render(ocean, now)
	if bytes.Equal(tater.Pix, ocean.Pix) {
		t.Fatal("changing display_theme did not change the Checkers reply glow")
	}
}

func TestIntercomIsLowerLeftQuarterCircle(t *testing.T) {
	if intercomCenterX > -20 || intercomCenterY < 500 || micIconLeft+micIconWidth/2 >= 34 || micIconTop+micIconHeight/2 <= 432 {
		t.Fatal("intercom control and icon did not move down and left")
	}
	if !inCircle(24, 452, intercomCenterX, intercomCenterY, intercomRadius) {
		t.Fatal("visible microphone must be inside the touch target")
	}
	if !inCircle(80, 479, intercomCenterX, intercomCenterY, intercomRadius) ||
		!inCircle(0, 398, intercomCenterX, intercomCenterY, intercomRadius) {
		t.Fatal("corner circle is too small after moving off-screen")
	}
	if !inCircle(micIconLeft+micIconWidth, micIconTop, intercomCenterX, intercomCenterY, intercomRadius) {
		t.Fatal("microphone icon no longer fits inside the circle")
	}
	if inCircle(200, 440, intercomCenterX, intercomCenterY, intercomRadius) ||
		inCircle(24, 400, intercomCenterX, intercomCenterY, intercomRadius) {
		t.Fatal("intercom touch target extends past its visible corner")
	}
}

func TestIntercomGlyphIsWhiteAndVisible(t *testing.T) {
	icon := newMicrophoneIcon()
	for _, point := range []image.Point{{20, 10}, {20, 21}, {20, 45}} {
		pixel := icon.RGBAAt(point.X, point.Y)
		if pixel.A < 200 || pixel.R != pixel.G || pixel.G != pixel.B {
			t.Fatalf("microphone glyph at %v is not solid white: %v", point, pixel)
		}
	}
	canvas := image.NewRGBA(image.Rect(0, 0, 960, 480))
	r := &renderer{state: show.Snapshot{Connected: true}}
	r.drawIntercom(canvas, color.RGBA{76, 157, 255, 255})
	if pixel := canvas.RGBAAt(micIconLeft+20, micIconTop+10); pixel.R < 200 || pixel.G < 200 || pixel.B < 200 {
		t.Fatalf("visible microphone is not white: %v", pixel)
	}
}

func TestCrownMicGlyphMovesLeftWithoutMovingCircle(t *testing.T) {
	checkersX, _, checkersRadius, checkersIconX, _ := intercomGeometry(960, 480)
	crownX, _, crownRadius, crownIconX, _ := intercomGeometry(1280, 800)
	if checkersIconX != micIconLeft || crownIconX != micIconLeft-4 {
		t.Fatalf("Checkers/Crown mic x = %d/%d, want %d/%d", checkersIconX, crownIconX, micIconLeft, micIconLeft-4)
	}
	if checkersX != -24 || crownX != -32 || checkersRadius != intercomRadius || crownRadius != intercomRadius {
		t.Fatalf("moving Crown mic changed circle geometry: Checkers x/r=%d/%d Crown x/r=%d/%d", checkersX, checkersRadius, crownX, crownRadius)
	}
}

func TestAwarenessDescriptionUsesReadableTypeWithoutCoveringControls(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	width := rightPane(960, 480).Dx()
	shortSize, shortBaseline, shortLineHeight, shortLines := notificationDescriptionLayout(faces, "A person was detected at the front door", width)
	if shortSize != 36 || len(shortLines) == 0 || len(shortLines) > 3 ||
		shortBaseline+(len(shortLines)-1)*shortLineHeight+faces.regular[shortSize].Metrics().Descent.Ceil() >= 396 {
		t.Fatalf("short awareness description size=%d lines=%d, want 36px in up to 3 lines", shortSize, len(shortLines))
	}
	long := strings.Repeat("The front door camera detected motion nearby. ", 5)
	size, baseline, lineHeight, lines := notificationDescriptionLayout(faces, long, width)
	if size != 30 || len(lines) != 4 || baseline+(len(lines)-1)*lineHeight+faces.regular[size].Metrics().Descent.Ceil() >= 396 {
		t.Fatalf("long awareness description size=%d lines=%d last baseline=%d", size, len(lines), baseline+(len(lines)-1)*lineHeight)
	}
	if !strings.HasSuffix(lines[len(lines)-1], "…") {
		t.Fatal("truncated awareness description has no ellipsis")
	}
}

func TestNotificationCategorySupportsFutureDisplayKinds(t *testing.T) {
	generated := &show.Notification{Kind: "image", Label: "Generated Image", ImageFit: "fit"}
	if got := notificationCategory(generated); got != "GENERATED IMAGE" {
		t.Fatalf("generated image category = %q", got)
	}
	if got := notificationFallbackTitle(generated); got != "Generated Image" {
		t.Fatalf("generated image fallback title = %q", got)
	}
	if got := notificationImageFit(generated, "cover"); got != "contain" {
		t.Fatalf("generated image fit = %q", got)
	}
	future := &show.Notification{Kind: "delivery_update"}
	if got := notificationCategory(future); got != "DELIVERY UPDATE" {
		t.Fatalf("future notification category = %q", got)
	}
	if got := notificationFallbackTitle(future); got != "Delivery Update" {
		t.Fatalf("future notification fallback title = %q", got)
	}
	awareness := &show.Notification{Kind: "camera"}
	if got := notificationCategory(awareness); got != "AWARENESS" {
		t.Fatalf("awareness category = %q", got)
	}
	if got := notificationFallbackTitle(awareness); got != "Recent activity" {
		t.Fatalf("awareness fallback title = %q", got)
	}
}

func TestScreenTapOpensActionMenuAndHoldStartsIntercom(t *testing.T) {
	var commands bytes.Buffer
	client := &socketClient{writer: bufio.NewWriter(&commands)}
	r := &renderer{state: show.Snapshot{Connected: true}}
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: 24, Y: 452})
	if r.intercomPressed || r.menuOpen || !r.satChatTouchDown || commands.Len() != 0 {
		t.Fatal("mic Down did not begin a pending tap/hold gesture")
	}
	// Accept the lift anywhere: the photo home screen is the touch target.
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: 140, Y: 390})
	if r.intercomPressed || !r.menuOpen || r.satChatTouchDown || commands.Len() != 0 {
		t.Fatalf("short tap did not open the action menu: %s", commands.String())
	}

	r.menuOpen = false
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: 24, Y: 452})
	r.updateIntercomHold(client, r.satChatTouchAt.Add(spotHoldDelay))
	if !r.intercomPressed || !strings.Contains(commands.String(), `"action":"intercom.start"`) {
		t.Fatalf("mic hold did not start intercom: %s", commands.String())
	}
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: 140, Y: 390})
	if r.intercomPressed || r.menuOpen || !strings.Contains(commands.String(), `"action":"intercom.stop"`) {
		t.Fatalf("intercom hold did not stop cleanly: %s", commands.String())
	}
	commands.Reset()
	r.menuOpen = false
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: 200, Y: 452})
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: 200, Y: 452})
	if r.intercomPressed || !r.menuOpen || commands.Len() != 0 {
		t.Fatal("tap elsewhere on the screen did not open the action menu")
	}
	r.menuOpen = false
	r.state.Muted = true
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: 24, Y: 452})
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: 24, Y: 452})
	if r.intercomPressed || !r.menuOpen || commands.Len() != 0 {
		t.Fatal("muted screen tap did not open the action menu cleanly")
	}
}

func TestFullscreenVideoTapPausesThenPlayAndStopUseOverlayButtons(t *testing.T) {
	var commands bytes.Buffer
	client := &socketClient{writer: bufio.NewWriter(&commands)}
	r := &renderer{
		screenWidth: 960, screenHeight: 480,
		state: show.Snapshot{Video: &show.Video{
			Active: true, SessionID: "video-1", State: "playing", PositionMS: 1200,
		}},
		displayVideoPositionMS: 2345,
	}
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: 100, Y: 100})
	if got := commands.String(); !strings.Contains(got, `"action":"video.pause"`) ||
		!strings.Contains(got, `"session_id":"video-1"`) || !strings.Contains(got, `"position_ms":2345`) {
		t.Fatalf("fullscreen tap did not pause at the rendered position: %s", got)
	}

	commands.Reset()
	r.state.Video.State = "paused"
	play, stop := displayVideoControlBounds(960, 480, false)
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: play.Min.X + 10, Y: play.Min.Y + 10})
	if !strings.Contains(commands.String(), `"action":"video.resume"`) {
		t.Fatalf("Play button did not resume: %s", commands.String())
	}

	commands.Reset()
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: stop.Min.X + 10, Y: stop.Min.Y + 10})
	if !strings.Contains(commands.String(), `"action":"video.stop"`) {
		t.Fatalf("Stop button did not close playback: %s", commands.String())
	}
}

func TestDisplayVideoServerSeekURLPreservesGrantAndMovesStart(t *testing.T) {
	raw := "https://tube.invalid/api/tater/local/stream?core_stream_token=secret&profile=echo_show_720p"
	seeked := displayVideoServerSeekURL(raw, 90_500)
	for _, expected := range []string{"core_stream_token=secret", "profile=echo_show_720p", "start=90.500"} {
		if !strings.Contains(seeked, expected) {
			t.Fatalf("seek URL %q missing %q", seeked, expected)
		}
	}
	restarted := displayVideoServerSeekURL(seeked, 0)
	if strings.Contains(restarted, "start=") || !strings.Contains(restarted, "core_stream_token=secret") {
		t.Fatalf("loop restart URL = %q", restarted)
	}
}

func TestRookVideoControlsRemainInsideCircularSafeArea(t *testing.T) {
	play, stop := displayVideoControlBounds(480, 480, true)
	for _, point := range []image.Point{
		play.Min, image.Pt(play.Max.X-1, play.Max.Y-1), stop.Min, image.Pt(stop.Max.X-1, stop.Max.Y-1),
	} {
		dx, dy := point.X-240, point.Y-240
		if dx*dx+dy*dy > 232*232 {
			t.Fatalf("video control point %v falls outside Rook's circular safe area", point)
		}
	}
}

func TestActionMenuOpensClimateAndControlsThermostat(t *testing.T) {
	var commands bytes.Buffer
	client := &socketClient{writer: bufio.NewWriter(&commands)}
	target, current := 72.0, 71.0
	r := &renderer{
		screenWidth: 960, screenHeight: 480,
		state: show.Snapshot{Connected: true, Climate: &show.Climate{
			Available: true, RoomID: "living_room", RoomName: "Living Room",
			CurrentTemperature: &current, TargetTemperature: &target, TemperatureUnit: "F",
			CanSetTemperature: true, CanSetMode: true, HVACMode: "heat",
			AvailableHVACModes: []string{"heat", "cool", "auto", "off"},
			MinimumTemperature: 45, MaximumTemperature: 90, TemperatureStep: 1,
		}},
	}
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: 800, Y: 400})
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: 800, Y: 400})
	if !r.menuOpen || r.climateOpen {
		t.Fatalf("home tap menu=%t climate=%t", r.menuOpen, r.climateOpen)
	}
	climateCard := actionMenuBounds(960, 480, 2, false)
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: (climateCard.Min.X + climateCard.Max.X) / 2, Y: (climateCard.Min.Y + climateCard.Max.Y) / 2})
	if !r.climateOpen || r.menuOpen || r.satChatOpen {
		t.Fatalf("climate card menu=%t climate=%t sat-chat=%t", r.menuOpen, r.climateOpen, r.satChatOpen)
	}
	plus := climateSetpointBounds(960, 480, true, false)
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: (plus.Min.X + plus.Max.X) / 2, Y: (plus.Min.Y + plus.Max.Y) / 2})
	if !strings.Contains(commands.String(), `"action":"climate.set_temperature"`) || !strings.Contains(commands.String(), `"value":73`) {
		t.Fatalf("setpoint command = %q", commands.String())
	}
	auto := climateModeBounds(960, 480, 2, false)
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: (auto.Min.X + auto.Max.X) / 2, Y: (auto.Min.Y + auto.Max.Y) / 2})
	if !strings.Contains(commands.String(), `"action":"climate.set_mode"`) || !strings.Contains(commands.String(), `"mode":"auto"`) {
		t.Fatalf("mode command = %q", commands.String())
	}
	closeBounds := climateCloseBounds(960, 480, false)
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: (closeBounds.Min.X + closeBounds.Max.X) / 2, Y: (closeBounds.Min.Y + closeBounds.Max.Y) / 2})
	if r.climateOpen {
		t.Fatal("close left climate screen open")
	}
}

func TestActionMenuLayoutFitsEveryScreen(t *testing.T) {
	targets := []struct {
		name          string
		width, height int
		spot          bool
	}{
		{name: "checkers", width: 960, height: 480},
		{name: "crown", width: 1280, height: 800},
		{name: "athena", width: 1280, height: 800},
		{name: "rook", width: 480, height: 480, spot: true},
	}
	items := []actionMenuItem{actionMenuSatChat, actionMenuCameras, actionMenuClimate}
	for _, target := range targets {
		t.Run(target.name, func(t *testing.T) {
			screen := image.Rect(0, 0, target.width, target.height)
			panel := actionMenuPanelBounds(target.width, target.height, target.spot)
			if !panel.In(screen) {
				t.Fatalf("panel %v outside screen %v", panel, screen)
			}
			previous := image.Rectangle{}
			for index, item := range items {
				bounds := actionMenuBounds(target.width, target.height, index, target.spot)
				if !bounds.In(screen) || !bounds.In(panel) {
					t.Fatalf("item %d bounds %v outside panel %v", index, bounds, panel)
				}
				if index > 0 && bounds.Min.Y <= previous.Max.Y {
					t.Fatalf("item %d overlaps previous row: %v and %v", index, previous, bounds)
				}
				center := image.Pt((bounds.Min.X+bounds.Max.X)/2, (bounds.Min.Y+bounds.Max.Y)/2)
				if got := actionMenuItemAt(center, target.width, target.height, target.spot); got != item {
					t.Fatalf("item %d center selected %v, want %v", index, got, item)
				}
				previous = bounds
			}
			broadcast := actionMenuBroadcastBounds(target.width, target.height, target.spot)
			if !broadcast.In(screen) || !broadcast.In(panel) || broadcast.Min.Y <= previous.Max.Y {
				t.Fatalf("broadcast bounds %v do not follow rows inside panel %v", broadcast, panel)
			}
			if target.spot {
				panelMask := menuSuperellipseMask(panel.Size())
				for y := 0; y < panelMask.Rect.Dy(); y++ {
					for x := 0; x < panelMask.Rect.Dx(); x++ {
						if panelMask.AlphaAt(x, y).A == 0 {
							continue
						}
						dx := panel.Min.X + x - target.width/2
						dy := panel.Min.Y + y - target.height/2
						if dx*dx+dy*dy > 236*236 {
							t.Fatalf("Rook oval paints outside the visible circle at (%d,%d)", panel.Min.X+x, panel.Min.Y+y)
						}
					}
				}
				assertPillInsideOval := func(label string, bounds image.Rectangle) {
					radius := bounds.Dy() / 2
					centerY := bounds.Min.Y + radius
					// A pill's rectangular corners are transparent. Check the six
					// cardinal/flat-edge points that its outline actually paints.
					for _, point := range []image.Point{
						{X: bounds.Min.X, Y: centerY},
						{X: bounds.Max.X - 1, Y: centerY},
						{X: bounds.Min.X + radius, Y: bounds.Min.Y},
						{X: bounds.Max.X - radius - 1, Y: bounds.Min.Y},
						{X: bounds.Min.X + radius, Y: bounds.Max.Y - 1},
						{X: bounds.Max.X - radius - 1, Y: bounds.Max.Y - 1},
					} {
						dx, dy := point.X-target.width/2, point.Y-target.height/2
						if dx*dx+dy*dy > 236*236 {
							t.Fatalf("%s edge %v falls outside Rook's visible circle", label, point)
						}
						local := point.Sub(panel.Min)
						if !local.In(panelMask.Bounds()) || panelMask.AlphaAt(local.X, local.Y).A == 0 {
							t.Fatalf("%s edge %v falls outside Rook's oval menu surface", label, point)
						}
					}
				}
				for index := range items {
					assertPillInsideOval(fmt.Sprintf("item %d", index), actionMenuBounds(target.width, target.height, index, true))
				}
				assertPillInsideOval("broadcast", broadcast)
			}
		})
	}
}

func TestMenuRoundedMaskHasAntialiasedEdges(t *testing.T) {
	mask := menuRoundedMask(image.Pt(80, 48), 16)
	partial := 0
	for _, alpha := range mask.Pix {
		if alpha > 0 && alpha < 255 {
			partial++
		}
	}
	if partial == 0 {
		t.Fatal("rounded menu mask has no antialiased edge pixels")
	}
}

func TestUnavailableClimateIsVisibleButDoesNotSendControls(t *testing.T) {
	var commands bytes.Buffer
	client := &socketClient{writer: bufio.NewWriter(&commands)}
	r := &renderer{screenWidth: 960, screenHeight: 480, climateOpen: true, state: show.Snapshot{Connected: true, Climate: &show.Climate{Available: false}}}
	plus := climateSetpointBounds(960, 480, true, false)
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: (plus.Min.X + plus.Max.X) / 2, Y: (plus.Min.Y + plus.Max.Y) / 2})
	if commands.Len() != 0 || !r.climateOpen {
		t.Fatalf("unavailable thermostat sent command=%q open=%t", commands.String(), r.climateOpen)
	}
}

func TestCrownSatChatTapHoldAndControls(t *testing.T) {
	var commands bytes.Buffer
	client := &socketClient{writer: bufio.NewWriter(&commands)}
	r := &renderer{target: "crown", screenWidth: 1280, screenHeight: 800, state: show.Snapshot{Connected: true}}

	// The microphone still classifies the gesture from release versus the hold
	// deadline; Down by itself must never declare a short tap.
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: 24, Y: 772})
	if r.menuOpen || !r.satChatTouchDown || r.intercomPressed {
		t.Fatalf("Crown mic Down was prematurely classified: open=%t pending=%t intercom=%t",
			r.menuOpen, r.satChatTouchDown, r.intercomPressed)
	}
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: 24, Y: 772})
	if !r.menuOpen || r.satChatTouchDown || r.intercomPressed || commands.Len() != 0 {
		t.Fatalf("Crown release did not open the action menu: %q", commands.String())
	}

	r.menuOpen = false
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: 24, Y: 772})
	r.updateIntercomHold(client, r.satChatTouchAt.Add(spotHoldDelay))
	if r.menuOpen || !r.intercomPressed || !strings.Contains(commands.String(), `"action":"intercom.start"`) {
		t.Fatalf("Crown hold did not start intercom: %q", commands.String())
	}

	commands.Reset()
	r.intercomPressed = false
	r.satChatTouchDown = false
	r.satChatOpen = true
	r.state.SatChat = &show.SatChat{Participants: []show.SatChatParticipant{{ID: "native:kitchen", Name: "Kitchen", Audio: true}}}
	firstParticipant := satChatDirectoryLayoutFor(1280, 800).participantBounds(0)
	participantX, participantY := (firstParticipant.Min.X+firstParticipant.Max.X)/2, (firstParticipant.Min.Y+firstParticipant.Max.Y)/2
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: participantX, Y: participantY})
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: participantX, Y: participantY})
	if count := strings.Count(commands.String(), `"action":"sat_chat.start"`); count != 1 {
		t.Fatalf("Crown participant press sent %d start commands, want one: %q", count, commands.String())
	}

	commands.Reset()
	r.satChatOpen = true
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: 1200, Y: 60})
	if r.satChatOpen {
		t.Fatal("Crown close control waited for touch release")
	}
}

func TestRectangularSatChatDirectoryPagesAndSelectsVisibleTarget(t *testing.T) {
	participants := make([]show.SatChatParticipant, 20)
	for index := range participants {
		participants[index] = show.SatChatParticipant{
			ID: fmt.Sprintf("native:sat-%02d", index), Name: fmt.Sprintf("Satellite %02d", index+1), Audio: true, Video: index%2 == 0,
		}
	}
	for _, test := range []struct {
		name          string
		target        string
		width, height int
		pageSize      int
	}{
		{name: "checkers", target: "checkers", width: 960, height: 480, pageSize: 8},
		{name: "crown", target: "crown", width: 1280, height: 800, pageSize: 8},
	} {
		t.Run(test.name, func(t *testing.T) {
			var commands bytes.Buffer
			client := &socketClient{writer: bufio.NewWriter(&commands)}
			r := &renderer{
				target: test.target, screenWidth: test.width, screenHeight: test.height, satChatOpen: true,
				state: show.Snapshot{Connected: true, SatChat: &show.SatChat{Participants: participants}},
			}
			if got := r.satChatPageSize(test.width, test.height); got != test.pageSize {
				t.Fatalf("page size = %d, want %d", got, test.pageSize)
			}
			next := satChatNextBounds(test.width)
			r.handleSatChatTouch(client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: (next.Min.X + next.Max.X) / 2, Y: (next.Min.Y + next.Max.Y) / 2}, test.width, test.height)
			if r.satChatPage != 1 {
				t.Fatalf("next page = %d, want 1", r.satChatPage)
			}
			firstCard := satChatDirectoryLayoutFor(test.width, test.height).participantBounds(0)
			r.handleSatChatTouch(client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: (firstCard.Min.X + firstCard.Max.X) / 2, Y: (firstCard.Min.Y + firstCard.Max.Y) / 2}, test.width, test.height)
			wantTarget := participants[test.pageSize].ID
			if !strings.Contains(commands.String(), `"target":"`+wantTarget+`"`) {
				t.Fatalf("first target on page 2 = %q, want %q", commands.String(), wantTarget)
			}
			r.satChatPage = 8
			if got := r.satChatPageFor(2, test.pageSize); got != 0 {
				t.Fatalf("shrunk directory page = %d, want 0", got)
			}
		})
	}
}

func TestSlowScreenImageLoadDoesNotBlockCrownTouch(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		<-release
		writer.Header().Set("Content-Type", "image/png")
		_, _ = writer.Write(encoded.Bytes())
	}))
	defer server.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan screenImageResult, 1)
	r := &renderer{
		target: "crown", screenWidth: 1280, screenHeight: 800,
		state: show.Snapshot{Connected: true, LastEvent: &show.Notification{
			ID: "slow-camera", ImageURL: server.URL,
		}},
	}
	queued := make(chan struct{})
	go func() {
		r.queueLastEventImage(ctx, results)
		close(queued)
	}()
	select {
	case <-queued:
	case <-time.After(250 * time.Millisecond):
		close(release)
		t.Fatal("queueLastEventImage blocked the display loop on image I/O")
	}

	var commands bytes.Buffer
	client := &socketClient{writer: bufio.NewWriter(&commands)}
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: 24, Y: 772})
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: 24, Y: 772})
	if !r.menuOpen || r.satChatTouchDown || commands.Len() != 0 {
		t.Fatalf("slow camera image blocked Crown touch: %q", commands.String())
	}

	close(release)
	result := <-results
	r.applyScreenImage(result)
	if r.lastEventImage == nil {
		t.Fatal("completed asynchronous camera image was not applied")
	}
}

func TestScreenImageLoadRetriesTransientLoopbackFailure(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts < 3 {
			http.Error(writer, "not ready", http.StatusServiceUnavailable)
			return
		}
		writer.Header().Set("Content-Type", "image/png")
		_, _ = writer.Write(encoded.Bytes())
	}))
	defer server.Close()

	results := make(chan screenImageResult, 1)
	queueScreenImage(context.Background(), results, notificationScreenImage, "door|"+server.URL, server.URL)
	select {
	case result := <-results:
		if result.image == nil || attempts != 3 {
			t.Fatalf("retried image result=%v attempts=%d, want decoded image after 3 attempts", result.image, attempts)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for retried notification image")
	}
}

func TestNotificationImageCompletionInvalidatesCrownCache(t *testing.T) {
	loaded := image.NewRGBA(image.Rect(0, 0, 2, 2))
	r := &renderer{
		notificationID:   "door|http://127.0.0.1/image?v=2",
		crownBaseKey:     "cached-crown",
		checkersBaseKey:  "cached-checkers",
		spotFrameBaseKey: "cached-rook",
		crownCanvas:      image.NewRGBA(image.Rect(0, 0, 2, 2)),
		checkersCanvas:   image.NewRGBA(image.Rect(0, 0, 2, 2)),
		spotFrameCanvas:  image.NewRGBA(image.Rect(0, 0, 2, 2)),
	}
	r.applyScreenImage(screenImageResult{
		kind: notificationScreenImage, identity: r.notificationID, image: loaded,
	})
	if r.notificationImage != loaded {
		t.Fatal("completed notification image was not accepted")
	}
	if r.crownBaseKey != "" || r.checkersBaseKey != "" || r.spotFrameBaseKey != "" {
		t.Fatal("completed notification image did not invalidate static screen caches")
	}
	if r.crownCanvas != nil || r.checkersCanvas != nil || r.spotFrameCanvas != nil {
		t.Fatal("completed notification image left a stale rendered canvas attached")
	}
}

func TestNotificationImageIdentityIncludesURLVersion(t *testing.T) {
	first := notificationImageIdentity(&show.Notification{ID: "door", ImageURL: "http://127.0.0.1/image?v=1"})
	second := notificationImageIdentity(&show.Notification{ID: "door", ImageURL: "http://127.0.0.1/image?v=2"})
	if first == second {
		t.Fatal("a newer image URL with the same event ID did not request a reload")
	}
}

func TestNotificationProgressUsesActualTTL(t *testing.T) {
	item := &show.Notification{CreatedAtUnixMS: 1_000, ExpiresAtUnixMS: 11_000, TTLMS: 10_000}
	for _, test := range []struct {
		now  int64
		want float64
	}{{1_000, 1}, {6_000, .5}, {10_000, .1}, {11_000, 0}} {
		if got := notificationProgress(item, test.now); math.Abs(got-test.want) > .0001 {
			t.Errorf("progress at %d = %.3f, want %.3f", test.now, got, test.want)
		}
	}
	item.TTLMS = 0
	if got := notificationProgress(item, 6_000); math.Abs(got-.5) > .0001 {
		t.Fatalf("created/expires fallback progress = %.3f, want .5", got)
	}
}

func TestCachedCrownNotificationProgressKeepsMoving(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	r := &renderer{
		faces: faces, target: "crown", screenWidth: 1280, screenHeight: 800,
		received: now, animationStart: now,
		state: show.Snapshot{
			Connected: true, TaterTimeUnixMS: now.UnixMilli(),
			Notification: &show.Notification{
				ID: "door", CameraName: "Front Door", Description: "Person detected",
				CreatedAtUnixMS: now.UnixMilli(), ExpiresAtUnixMS: now.Add(10 * time.Second).UnixMilli(), TTLMS: 10_000,
			},
		},
	}
	canvas := image.NewRGBA(image.Rect(0, 0, 1280, 800))
	r.render(canvas, now)
	base := r.crownBase
	first := append([]byte(nil), canvas.Pix...)
	r.render(canvas, now.Add(5*time.Second))
	if r.crownBase != base {
		t.Fatal("countdown-only update rebuilt the Crown notification composition")
	}
	if bytes.Equal(first, canvas.Pix) {
		t.Fatal("Crown notification countdown bar did not move")
	}
	if len(r.presentRegions) == 0 {
		t.Fatal("Crown notification countdown requested no framebuffer update")
	}
	for _, region := range r.presentRegions {
		if region == canvas.Rect {
			t.Fatal("Crown notification countdown requested an unnecessary full-frame update")
		}
	}
}

func TestCameraNameKeepsPreferredSizeOrShrinksToFit(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	if got := fittingTextSize("Front Door", faces.bold, 30, 12, 300); got != 30 {
		t.Fatalf("short camera name size = %d, want 30", got)
	}
	longName := "Upstairs West Hallway and Landing Camera"
	got := fittingTextSize(longName, faces.bold, 30, 12, 300)
	if got >= 30 {
		t.Fatalf("long camera name size = %d, want a smaller fitting size", got)
	}
	if font.MeasureString(faces.bold[got], longName).Round() > 300 {
		t.Fatalf("long camera name still exceeds its width at size %d", got)
	}
}

func TestSatChatMenuAndCallRenderOnEveryScreenTarget(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{19, 25, 28, 34} {
		if faces.regular[size] == nil || faces.bold[size] == nil {
			t.Fatalf("Sat Chat font size %d was not loaded", size)
		}
	}
	now := time.Unix(1_800_000_000, 0)
	for _, test := range []struct {
		name          string
		target        string
		width, height int
	}{
		{name: "checkers", target: "checkers", width: 960, height: 480},
		{name: "crown", target: "crown", width: 1280, height: 800},
		{name: "rook", target: "rook", width: 480, height: 480},
	} {
		t.Run(test.name, func(t *testing.T) {
			participants := make([]show.SatChatParticipant, 12)
			for index := range participants {
				participants[index] = show.SatChatParticipant{
					ID: fmt.Sprintf("native:other-%02d", index), Name: fmt.Sprintf("Room Satellite %02d", index+1), Room: "Kitchen", Audio: true, Video: index%2 == 0,
				}
			}
			r := &renderer{
				faces: faces, target: test.target, spotScreen: test.width == test.height,
				screenWidth: test.width, screenHeight: test.height, satChatOpen: true, satChatPage: 1,
				state: show.Snapshot{Connected: true, Phase: "idle", SatChat: &show.SatChat{
					Self: "native:self", Participants: participants,
				}},
				animationStart: now,
			}
			canvas := image.NewRGBA(image.Rect(0, 0, test.width, test.height))
			r.render(canvas, now)

			r.satChatOpen = false
			r.state.SatChat.Call = &show.SatChatCall{
				ID: "call-1", State: "active", Mode: "audio",
				Caller: "native:self", CallerName: "Office", Target: participants[0].ID, TargetName: participants[0].Name,
			}
			r.render(canvas, now)
		})
	}
}

func TestCrownFrameRateMatchesCheckersWithRegionalPresent(t *testing.T) {
	if got := displayFrameInterval("crown"); got != time.Second/30 {
		t.Fatalf("Crown frame interval = %s, want %s", got, time.Second/30)
	}
	if got := displayFrameInterval("checkers"); got != time.Second/30 {
		t.Fatalf("Checkers frame interval = %s, want %s", got, time.Second/30)
	}
}

func TestNormalizeDisplayTargetCorrectsCrownFramebuffer(t *testing.T) {
	tests := []struct {
		name          string
		linked        string
		width, height int
		want          string
	}{
		{name: "crown label", linked: "crown", width: 1280, height: 800, want: "crown"},
		{name: "athena remains explicit", linked: "athena", width: 1280, height: 800, want: "athena"},
		{name: "mislabeled crown", linked: "checkers", width: 1280, height: 800, want: "crown"},
		{name: "checkers", linked: "checkers", width: 960, height: 480, want: "checkers"},
		{name: "rook remains explicit", linked: "rook", width: 1280, height: 800, want: "rook"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeDisplayTarget(test.linked, test.width, test.height); got != test.want {
				t.Fatalf("normalizeDisplayTarget(%q, %d, %d) = %q, want %q", test.linked, test.width, test.height, got, test.want)
			}
		})
	}
}

func TestCrownContextPriorityReturnsToMusicThenLastEvent(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	r := &renderer{target: "crown", received: now, state: show.Snapshot{
		TaterTimeUnixMS: now.UnixMilli(),
		LastEvent:       &show.Notification{ID: "last"},
	}}
	if got := r.crownContextKind(now); got != "last_event" {
		t.Fatalf("idle Crown context = %q, want last_event", got)
	}
	r.state.Media = &show.Media{Active: true, Title: "Music"}
	if got := r.crownContextKind(now); got != "media" {
		t.Fatalf("playing Crown context = %q, want media", got)
	}
	r.state.Notification = &show.Notification{ID: "new", ExpiresAtUnixMS: now.Add(8 * time.Second).UnixMilli()}
	if got := r.crownContextKind(now); got != "notification" {
		t.Fatalf("new Awareness context = %q, want notification", got)
	}
	activeKey := r.crownCacheKey(now, accentForTheme("idle", "tater"))
	if got := r.crownContextKind(now.Add(9 * time.Second)); got != "media" {
		t.Fatalf("expired Awareness context = %q, want media", got)
	}
	if expiredKey := r.crownCacheKey(now.Add(9*time.Second), accentForTheme("idle", "tater")); expiredKey == activeKey {
		t.Fatal("Crown cache did not invalidate when Awareness expired")
	}
	r.state.Media.Active = false
	if got := r.crownContextKind(now.Add(9 * time.Second)); got != "last_event" {
		t.Fatalf("stopped music context = %q, want last_event", got)
	}
}

func TestDisplayCacheKeysCompareClimatePointerValuesNotAddresses(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	accent := accentForTheme("idle", "tater")
	climate := func(current, target, humidity float64) *show.Climate {
		return &show.Climate{
			Available: true, RoomID: "kitchen", RoomName: "Kitchen",
			CurrentTemperature: &current, TargetTemperature: &target, CurrentHumidity: &humidity,
			TemperatureUnit: "F", HVACMode: "cool", HVACAction: "cooling",
			RoomTemperatures: []show.RoomTemperature{{RoomID: "office", RoomName: "Office", Temperature: 72, TemperatureUnit: "F"}},
		}
	}

	first := &renderer{target: "crown", state: show.Snapshot{Connected: true, Climate: climate(71, 72, 42)}}
	second := &renderer{target: "crown", state: show.Snapshot{Connected: true, Climate: climate(71, 72, 42)}}
	if first.crownCacheKey(now, accent) != second.crownCacheKey(now, accent) {
		t.Fatal("equal Crown climate values with different pointers produced different cache keys")
	}
	first.target = "checkers"
	second.target = "checkers"
	if first.checkersCacheKey(now, accent) != second.checkersCacheKey(now, accent) {
		t.Fatal("equal Checkers climate values with different pointers produced different cache keys")
	}
	second.state.Climate = climate(70, 72, 42)
	if first.checkersCacheKey(now, accent) == second.checkersCacheKey(now, accent) {
		t.Fatal("a changed climate reading did not invalidate the display cache")
	}
}

func TestCrownWithoutHistoryUsesEmptyLastEventState(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	r := &renderer{target: "crown", state: show.Snapshot{Connected: true}}
	if got := r.crownContextKind(now); got != "empty" {
		t.Fatalf("Crown context without history = %q, want empty", got)
	}
}

func TestCrownVoicePhasesKeepPersistentLowerContext(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	r := &renderer{target: "crown", state: show.Snapshot{
		Connected: true,
		LastEvent: &show.Notification{ID: "last", CameraName: "Front Door"},
	}}
	voicePhases := []string{"listening", "thinking", "speaking", "intercom"}
	for _, phase := range voicePhases {
		r.state.Phase = phase
		if got := r.crownContextKind(now); got != "last_event" {
			t.Fatalf("Crown context during %s = %q, want last_event", phase, got)
		}
	}

	r.state.Media = &show.Media{Active: true, Title: "Music"}
	for _, phase := range voicePhases {
		r.state.Phase = phase
		if got := r.crownContextKind(now); got != "media" {
			t.Fatalf("Crown media context during %s = %q, want media", phase, got)
		}
		if got := r.nowPlayingHeading(r.state.Media); got != "NOW PLAYING" {
			t.Fatalf("Crown media heading during %s = %q, want NOW PLAYING", phase, got)
		}
	}
}

func TestCrownUsesFullScreenContextOverPhotoHome(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	base := show.Snapshot{
		Phase: "idle", Connected: true, Room: "Kitchen",
		TaterTimeUnixMS: now.UnixMilli(),
		Weather: &show.Weather{
			TemperatureText: "72°", Condition: "Partly cloudy", ConditionKind: "partly",
			FeelsLikeText: "Feels like 70°", HumidityText: "48%", WindText: "6 mph", RainText: "10%",
			IndoorTemperatureText: "71°", IndoorHumidityText: "44%",
		},
		LastEvent: &show.Notification{ID: "last", CameraName: "Front Door", Description: "Person detected"},
	}
	render := func(state show.Snapshot) *image.RGBA {
		canvas := image.NewRGBA(image.Rect(0, 0, 1280, 800))
		r := &renderer{
			faces: faces, target: "crown", screenWidth: 1280, screenHeight: 800,
			state: state, received: now, animationStart: now.Add(-2 * time.Second),
		}
		r.render(canvas, now)
		return canvas
	}
	idle := render(base)
	mediaState := base
	mediaState.Media = &show.Media{Active: true, Title: "Garden Song", DurationMS: 60_000}
	media := render(mediaState)
	notificationState := base
	notificationState.Notification = &show.Notification{ID: "new", CameraName: "Driveway", ExpiresAtUnixMS: now.Add(time.Minute).UnixMilli()}
	notification := render(notificationState)

	if bytes.Equal(idle.Pix, media.Pix) || bytes.Equal(idle.Pix, notification.Pix) || bytes.Equal(media.Pix, notification.Pix) {
		t.Fatal("Crown did not switch between photo home, full-screen music, and full-screen Awareness")
	}
}

func TestWeatherDisplayKindUsesNightArtwork(t *testing.T) {
	clear := &show.Weather{ConditionKind: "sun", Daylight: "night"}
	if got := weatherDisplayKind(clear, time.Date(2026, time.July, 1, 12, 0, 0, 0, time.UTC)); got != "moon" {
		t.Fatalf("clear night kind = %q, want moon", got)
	}
	partly := &show.Weather{ConditionKind: "partly", Daylight: "night"}
	if got := weatherDisplayKind(partly, time.Date(2026, time.July, 1, 12, 0, 0, 0, time.UTC)); got != "partly-night" {
		t.Fatalf("partly cloudy night kind = %q, want partly-night", got)
	}
	clear.Daylight = "day"
	if got := weatherDisplayKind(clear, time.Date(2026, time.July, 1, 23, 0, 0, 0, time.UTC)); got != "sun" {
		t.Fatalf("provider daylight kind = %q, want sun", got)
	}
	clear.Daylight = ""
	if got := weatherDisplayKind(clear, time.Date(2026, time.July, 1, 23, 0, 0, 0, time.UTC)); got != "moon" {
		t.Fatalf("legacy nighttime fallback kind = %q, want moon", got)
	}
}

func TestCrownConditionsRenderLightningMetric(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.October, 13, 0, 30, 0, 0, time.FixedZone("CDT", -5*60*60))
	state := show.Snapshot{Phase: "idle", Connected: true, Room: "Kitchen", TaterTimeUnixMS: now.UnixMilli(), Weather: &show.Weather{
		TemperatureText: "72°", Condition: "Clear", ConditionKind: "sun", Daylight: "night",
		HumidityText: "48%", WindText: "6 mph", RainText: "0 in/hr",
		IndoorTemperatureText: "71°", IndoorHumidityText: "44%",
	}}
	render := func(value show.Snapshot) *image.RGBA {
		canvas := image.NewRGBA(image.Rect(0, 0, 1280, 800))
		r := &renderer{faces: faces, target: "crown", screenWidth: 1280, screenHeight: 800, state: value, received: now, animationStart: now}
		r.render(canvas, now)
		return canvas
	}
	without := render(state)
	state.Weather.LightningText = "3 strikes"
	with := render(state)
	if rgbaRegionEqual(without, with, image.Rect(590, 510, 1250, 770)) {
		t.Fatal("lightning reading did not change Crown's photo-home sensor panel")
	}
}

func TestCrownReusesStaticCompositionWithStaticWeatherArt(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	r := &renderer{
		faces: faces, target: "crown", screenWidth: 1280, screenHeight: 800,
		animationStart: now, received: now,
		state: show.Snapshot{
			Phase: "idle", Connected: true, Room: "Kitchen", TaterTimeUnixMS: now.UnixMilli(),
			Weather: &show.Weather{
				TemperatureText: "72°", Condition: "Rain", ConditionKind: "rain",
				IndoorTemperatureText: "71°", IndoorHumidityText: "44%",
			},
			LastEvent: &show.Notification{ID: "last", Description: "Motion detected"},
		},
	}
	first := image.NewRGBA(image.Rect(0, 0, 1280, 800))
	r.render(first, now)
	base := r.crownBase
	key := r.crownBaseKey
	if base == nil || key == "" {
		t.Fatal("Crown did not build its static composition cache")
	}
	before := append([]byte(nil), first.Pix...)
	r.render(first, now.Add(100*time.Millisecond))
	if r.crownBase != base || r.crownBaseKey != key {
		t.Fatal("Crown rebuilt its static composition for an animation-only frame")
	}
	if !bytes.Equal(before, first.Pix) {
		t.Fatal("static Crown weather art changed between frames")
	}
	if len(r.presentRegions) != 0 {
		t.Fatalf("static Crown home requested motion regions: %v", r.presentRegions)
	}
}

func TestCrownVoicePhasesReuseStaticComposition(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	for _, phase := range []string{"listening", "thinking", "speaking", "intercom"} {
		r := &renderer{
			faces: faces, target: "crown", screenWidth: 1280, screenHeight: 800,
			animationStart: now, received: now,
			state: show.Snapshot{
				Phase: phase, Connected: true, AudioLevel: .2, TaterTimeUnixMS: now.UnixMilli(),
				Weather:   &show.Weather{TemperatureText: "72°", Condition: "Clear", ConditionKind: "sun"},
				LastEvent: &show.Notification{ID: "last", Description: "Motion detected"},
			},
		}
		canvas := image.NewRGBA(image.Rect(0, 0, 1280, 800))
		r.render(canvas, now)
		base, key := r.crownBase, r.crownBaseKey
		if base == nil || key == "" {
			t.Fatalf("Crown did not cache its %s composition", phase)
		}
		r.state.AudioLevel = .9
		r.render(canvas, now.Add(time.Second/30))
		if r.crownBase != base || r.crownBaseKey != key {
			t.Fatalf("Crown rebuilt its %s composition for an audio-level-only frame", phase)
		}
		for _, region := range r.presentRegions {
			if region == canvas.Rect {
				t.Fatalf("Crown requested a full-frame present during %s", phase)
			}
		}
	}
}

func TestCrownReplyRimFollowsAudioWithoutRebuildingStaticComposition(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	r := &renderer{
		faces: faces, target: "crown", screenWidth: 1280, screenHeight: 800,
		animationStart: now, received: now,
		state: show.Snapshot{
			Phase: "speaking", Connected: true, DisplayEdgeGlow: true, TaterTimeUnixMS: now.UnixMilli(),
			WallpaperIntervalMinutes: 1440,
			Weather:                  &show.Weather{TemperatureText: "72°", Condition: "Clear", ConditionKind: "sun"},
			LastEvent:                &show.Notification{ID: "last", Description: "Motion detected"},
		},
	}
	quiet := image.NewRGBA(image.Rect(0, 0, 1280, 800))
	r.render(quiet, now)
	base, key := r.crownBase, r.crownBaseKey
	r.state.AudioLevel = .7
	loud := image.NewRGBA(quiet.Rect)
	for frame := 1; frame <= 5; frame++ {
		r.render(loud, now.Add(time.Duration(frame)*33*time.Millisecond))
	}
	if r.crownBase != base || r.crownBaseKey != key {
		t.Fatal("Crown rebuilt its static composition while the reply rim followed audio")
	}
	for _, point := range []image.Point{{X: 640, Y: 3}, {X: 3, Y: 400}} {
		low, high := quiet.RGBAAt(point.X, point.Y), loud.RGBAAt(point.X, point.Y)
		if high.R <= low.R+60 || high.G <= low.G+30 {
			t.Errorf("Crown reply rim did not brighten with audio at %v: quiet=%v loud=%v", point, low, high)
		}
	}
	for _, region := range r.presentRegions {
		if region == loud.Rect {
			t.Fatal("Crown reply rim requested a full-frame present")
		}
	}
}

func TestCachedCrownSpeakingFrameMatchesFullComposition(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	state := show.Snapshot{
		Phase: "speaking", Connected: true, AudioLevel: .65, Room: "Kitchen",
		Weather: &show.Weather{
			TemperatureText: "72°", Condition: "Partly cloudy", ConditionKind: "partly",
			HumidityText: "48%", IndoorTemperatureText: "71°", IndoorHumidityText: "44%",
		},
		LastEvent: &show.Notification{ID: "last", Description: "Motion detected"},
	}
	renderAt := now.Add(1250 * time.Millisecond)
	cachedRenderer := &renderer{
		faces: faces, target: "crown", screenWidth: 1280, screenHeight: 800,
		animationStart: now, state: state,
	}
	cached := image.NewRGBA(image.Rect(0, 0, 1280, 800))
	cachedRenderer.render(cached, renderAt)

	fullRenderer := &renderer{
		faces: faces, target: "crown", screenWidth: 1280, screenHeight: 800,
		animationStart: now, state: state,
	}
	full := image.NewRGBA(cached.Rect)
	seconds := renderAt.Sub(now).Seconds()
	fullRenderer.renderCheckers(full, renderAt, 1280, 800, seconds, accentForTheme(state.Phase, state.DisplayTheme), true)
	if !bytes.Equal(cached.Pix, full.Pix) {
		t.Fatal("cached Crown speaking composition differs from a full render")
	}
}

func TestCrownCachedAnimationKeepsEveryFramebufferPageCurrent(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	r := &renderer{
		faces: faces, target: "crown", screenWidth: 1280, screenHeight: 800,
		animationStart: now, received: now,
		state: show.Snapshot{
			Phase: "idle", Connected: true, TaterTimeUnixMS: now.UnixMilli(),
			Weather: &show.Weather{TemperatureText: "72°", Condition: "Clear", ConditionKind: "sun"},
			Media: &show.Media{
				Active: true, PlaybackState: "playing", Title: "Garden Song", Spectrum: []float64{.2, .8},
				ProgressMS: 10_000, DurationMS: 180_000, PlaybackSpeed: 1000,
				ProgressUpdatedAtUnixMS: now.UnixMilli(),
			},
		},
	}
	canvas := image.NewRGBA(image.Rect(0, 0, 1280, 800))
	r.render(canvas, now)
	r.render(canvas, now.Add(time.Second/30))
	if len(r.presentRegions) != 3 {
		t.Fatalf("Crown motion regions = %v, want fallback bars, progress, and spectrum on every frame", r.presentRegions)
	}
	area := 0
	for _, region := range r.presentRegions {
		if region == canvas.Rect {
			t.Fatal("cached Crown animation requested a full-frame present")
		}
		area += region.Dx() * region.Dy()
	}
	if area >= canvas.Rect.Dx()*canvas.Rect.Dy()/5 {
		t.Fatalf("Crown motion area = %d pixels, want less than one fifth of a frame", area)
	}
	r.render(canvas, now.Add(1100*time.Millisecond))
	if len(r.presentRegions) != 3 {
		t.Fatalf("Crown later motion regions = %v, want fallback bars, progress, and spectrum", r.presentRegions)
	}
}

func TestCrownMusicProgressDoesNotRebuildFullFrame(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	r := &renderer{
		faces: faces, target: "crown", screenWidth: 1280, screenHeight: 800,
		animationStart: now, received: now,
		state: show.Snapshot{
			Phase: "idle", Connected: true, TaterTimeUnixMS: now.UnixMilli(),
			Weather: &show.Weather{TemperatureText: "72°", Condition: "Clear", ConditionKind: "sun"},
			Media: &show.Media{
				Active: true, PlaybackState: "playing", Title: "Garden Song",
				ProgressMS: 10_000, DurationMS: 180_000, PlaybackSpeed: 1000,
				ProgressUpdatedAtUnixMS: now.UnixMilli(), Spectrum: []float64{.2, .8},
			},
		},
	}
	canvas := image.NewRGBA(image.Rect(0, 0, 1280, 800))
	r.render(canvas, now)
	base, key := r.crownBase, r.crownBaseKey
	progressBounds := image.Rect(crownPane(1280, crownContextHeight).Min.X, 298, crownPane(1280, crownContextHeight).Max.X, 354).
		Add(image.Pt(0, 800-crownContextHeight))
	before := image.NewRGBA(progressBounds)
	draw.Draw(before, before.Rect, canvas, progressBounds.Min, draw.Src)

	r.render(canvas, now.Add(1100*time.Millisecond))
	if r.crownBase != base || r.crownBaseKey != key {
		t.Fatal("Crown rebuilt its static frame solely because music progress advanced")
	}
	for _, region := range r.presentRegions {
		if region == canvas.Rect {
			t.Fatal("advancing music progress requested a full-frame present")
		}
	}
	after := image.NewRGBA(progressBounds)
	draw.Draw(after, after.Rect, canvas, progressBounds.Min, draw.Src)
	if bytes.Equal(before.Pix, after.Pix) {
		t.Fatal("music progress did not animate inside the regional update")
	}
}

func TestCheckersIgnoresCrownLastEventCard(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	state := show.Snapshot{Phase: "idle", Connected: true, Weather: &show.Weather{TemperatureText: "70°", Condition: "Clear", ConditionKind: "sun"}}
	render := func(value show.Snapshot) *image.RGBA {
		canvas := image.NewRGBA(image.Rect(0, 0, 960, 480))
		(&renderer{faces: faces, target: "checkers", state: value, animationStart: now}).render(canvas, now)
		return canvas
	}
	without := render(state)
	state.LastEvent = &show.Notification{ID: "event", Description: "Should remain Crown-only"}
	with := render(state)
	if !bytes.Equal(without.Pix, with.Pix) {
		t.Fatal("adding Crown Last Event data changed the Checkers UI")
	}
}

func TestLastEventTimestampUsesTaterLocalDay(t *testing.T) {
	now := time.Date(2026, time.October, 12, 18, 48, 0, 0, time.UTC)
	r := &renderer{state: show.Snapshot{TaterTimeUnixMS: now.UnixMilli()}}
	if got := r.lastEventTimestamp(now.Add(-5*time.Minute).UnixMilli(), now); got != "TODAY  ·  6:43 PM" {
		t.Fatalf("same-day timestamp = %q", got)
	}
	if got := r.lastEventTimestamp(now.Add(-24*time.Hour).UnixMilli(), now); got != "YESTERDAY  ·  6:48 PM" {
		t.Fatalf("prior-day timestamp = %q", got)
	}
}

func TestClockAnchorIgnoresRepeatedSnapshotTimestamp(t *testing.T) {
	received := time.Date(2026, time.October, 14, 18, 49, 58, 0, time.UTC)
	state := show.Snapshot{TaterTimeUnixMS: received.UnixMilli(), TaterUTCOffset: -5 * 60 * 60}
	r := &renderer{}
	r.updateClockAnchor(state, received)
	r.state = state
	r.received = received

	// An unrelated state change 32 seconds later still carries the clock
	// sampled at 6:49:58. It must not reset the clock back into minute 49.
	staleReceived := received.Add(32 * time.Second)
	r.updateClockAnchor(state, staleReceived)
	r.state = state
	r.received = staleReceived
	if got := r.localTime(staleReceived); got.Hour() != 13 || got.Minute() != 50 || got.Second() != 30 {
		t.Fatalf("clock after repeated snapshot = %s, want 13:50:30", got.Format("15:04:05"))
	}

	// The next authoritative refresh should retain the continuous clock.
	freshReceived := received.Add(time.Minute)
	fresh := state
	fresh.TaterTimeUnixMS = received.Add(time.Minute).UnixMilli()
	r.updateClockAnchor(fresh, freshReceived)
	r.state = fresh
	r.received = freshReceived
	if got := r.localTime(freshReceived); got.Hour() != 13 || got.Minute() != 50 || got.Second() != 58 {
		t.Fatalf("clock after refreshed snapshot = %s, want 13:50:58", got.Format("15:04:05"))
	}
}

func TestDrawContainPreservesWideCameraAspect(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 160, 90))
	draw.Draw(source, source.Rect, image.NewUniform(color.RGBA{230, 30, 40, 255}), image.Point{}, draw.Src)
	destination := image.NewRGBA(image.Rect(0, 0, 100, 100))
	draw.Draw(destination, destination.Rect, image.NewUniform(color.RGBA{8, 12, 18, 255}), image.Point{}, draw.Src)
	drawContain(destination, destination.Rect, source)
	if center := destination.RGBAAt(50, 50); center.R < 200 || center.G > 50 {
		t.Fatalf("contained image center = %v, want source image", center)
	}
	if top := destination.RGBAAt(50, 5); top.R != 8 || top.G != 12 || top.B != 18 {
		t.Fatalf("wide image was stretched instead of letterboxed: top=%v", top)
	}
}

func rgbaRegionEqual(first, second *image.RGBA, region image.Rectangle) bool {
	if !region.In(first.Rect) || !region.In(second.Rect) {
		return false
	}
	for y := region.Min.Y; y < region.Max.Y; y++ {
		startFirst := first.PixOffset(region.Min.X, y)
		startSecond := second.PixOffset(region.Min.X, y)
		length := region.Dx() * 4
		if !bytes.Equal(first.Pix[startFirst:startFirst+length], second.Pix[startSecond:startSecond+length]) {
			return false
		}
	}
	return true
}

func TestPendingCrownTouchesDrainBeforeNextFrame(t *testing.T) {
	var commands bytes.Buffer
	client := &socketClient{writer: bufio.NewWriter(&commands)}
	r := &renderer{
		target: "crown", screenWidth: 1280, screenHeight: 800,
		state: show.Snapshot{Connected: true},
	}
	events := make(chan linuxinput.TouchEvent, 128)
	point := image.Pt(28, 766)
	events <- linuxinput.TouchEvent{Kind: linuxinput.Down, X: point.X, Y: point.Y}
	for index := 0; index < 40; index++ {
		events <- linuxinput.TouchEvent{Kind: linuxinput.Move, X: point.X, Y: point.Y}
	}
	events <- linuxinput.TouchEvent{Kind: linuxinput.Up, X: point.X, Y: point.Y}

	if got := drainTouchEvents(r, client, events); got != 1 {
		t.Fatalf("visible drained events = %d, want the Sat Chat-opening release", got)
	}
	if len(events) != 0 {
		t.Fatalf("touch queue still has %d events", len(events))
	}
	if r.intercomPressed || !r.menuOpen || commands.Len() != 0 {
		t.Fatalf("queued Crown tap did not open the action menu: %s", commands.String())
	}
	if r.touchDowns != 1 || r.touchMoves != 40 || r.touchUps != 1 {
		t.Fatalf("touch diagnostics down/move/up = %d/%d/%d", r.touchDowns, r.touchMoves, r.touchUps)
	}
}

func TestTimerRemainingAndProgress(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	timer := &show.Timer{
		Active:             true,
		OriginalDurationMS: 120_000,
		DeadlineUnixMS:     now.Add(30 * time.Second).UnixMilli(),
	}
	if got := timerRemaining(timer, now); got != 30*time.Second {
		t.Fatalf("timerRemaining() = %s, want 30s", got)
	}
	if got := timerProgress(timer, now); got != .25 {
		t.Fatalf("timerProgress() = %v, want .25", got)
	}
	timer.Ringing = true
	if got := timerProgress(timer, now); got != 0 {
		t.Fatalf("ringing timerProgress() = %v, want 0", got)
	}
}

func TestTimerTouchMatchesTheFullScreenButton(t *testing.T) {
	button := timerStopBounds(960, 480)
	if button.Min.X != 330 || button.Max.X != 630 || button.Min.Y != 392 || button.Max.Y != 452 {
		t.Fatalf("timer touch bounds=%v, want the visible bottom-center cancel button", button)
	}
	var commands bytes.Buffer
	client := &socketClient{writer: bufio.NewWriter(&commands)}
	r := &renderer{state: show.Snapshot{Connected: true, Timer: &show.Timer{Active: true}}}
	point := image.Pt((button.Min.X+button.Max.X)/2, (button.Min.Y+button.Max.Y)/2)
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: point.X, Y: point.Y})
	handleTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: point.X, Y: point.Y})
	if !strings.Contains(commands.String(), `"action":"timer.stop"`) {
		t.Fatalf("expanded timer button did not stop timer: %s", commands.String())
	}
}

func TestTranslucentRoundedRectKeepsItsColor(t *testing.T) {
	canvas := image.NewRGBA(image.Rect(0, 0, 60, 40))
	for index := 0; index < len(canvas.Pix); index += 4 {
		canvas.Pix[index], canvas.Pix[index+1], canvas.Pix[index+2], canvas.Pix[index+3] = 10, 10, 10, 255
	}
	roundedRect(canvas, image.Rect(5, 5, 55, 35), 12, color.RGBA{255, 147, 66, 48})
	center := canvas.RGBAAt(30, 20)
	if center.R < 50 || center.G < 30 || center.G > center.R || center.B >= center.G || center.A != 255 {
		t.Fatalf("translucent orange fill has incorrect color: %v", center)
	}
	if corner := canvas.RGBAAt(5, 5); corner.R != 10 || corner.G != 10 || corner.B != 10 {
		t.Fatalf("rounded corner was filled: %v", corner)
	}
}

func TestCheckersVoiceStagesOnlyChangeTheHomeStatusWhenGlowIsDisabled(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	r := &renderer{faces: faces, animationStart: now, state: show.Snapshot{
		Connected: true, DisplayEdgeGlow: false,
		Weather: &show.Weather{TemperatureText: "72°", Condition: "Sunny", ConditionKind: "sunny"},
	}}
	r.state.Phase = "idle"
	home := image.NewRGBA(image.Rect(0, 0, 960, 480))
	r.render(home, now)
	for _, phase := range []string{"listening", "thinking", "tool_call", "speaking", "intercom"} {
		r.state.Phase = phase
		canvas := image.NewRGBA(image.Rect(0, 0, 960, 480))
		r.render(canvas, now)
		if bytes.Equal(home.Pix, canvas.Pix) {
			t.Errorf("%s did not update the assistant status", phase)
		}
		if !imagesEqualOutside(home, canvas, image.Rect(0, 412, 960, 447)) {
			t.Errorf("%s changed the photo home outside the room/status row", phase)
		}
	}
}

func TestCheckersListeningGlowIsStaticAndThemeAware(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	state := show.Snapshot{
		Phase: "listening", Connected: true, DisplayEdgeGlow: true, DisplayTheme: "ocean",
		Room: "Kitchen", AssistantName: "Spud", WallpaperIntervalMinutes: 1440,
	}
	r := &renderer{faces: faces, animationStart: now, state: state}
	quiet := image.NewRGBA(image.Rect(0, 0, 960, 480))
	r.render(quiet, now)
	r.state.AudioLevel = .95
	loud := image.NewRGBA(quiet.Rect)
	r.render(loud, now.Add(time.Second/30))
	if !bytes.Equal(quiet.Pix, loud.Pix) {
		t.Fatal("listening glow reacted to speaker audio; only reply glow should be reactive")
	}

	withoutGlow := &renderer{faces: faces, animationStart: now, state: state}
	withoutGlow.state.DisplayEdgeGlow = false
	plain := image.NewRGBA(quiet.Rect)
	withoutGlow.render(plain, now)
	if quiet.RGBAAt(480, 3) == plain.RGBAAt(480, 3) {
		t.Fatal("listening state did not add its themed edge glow")
	}
}

func TestCheckersReplyRimFollowsAudioWithoutCoveringWeather(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	r := &renderer{faces: faces, animationStart: now, state: show.Snapshot{
		Phase: "speaking", Connected: true, DisplayEdgeGlow: true,
		WallpaperIntervalMinutes: 1440,
		Weather:                  &show.Weather{TemperatureText: "72°", Condition: "Sunny", ConditionKind: "sunny"},
	}}
	quiet := image.NewRGBA(image.Rect(0, 0, 960, 480))
	r.render(quiet, now)
	r.state.AudioLevel = .7
	loud := image.NewRGBA(quiet.Rect)
	for frame := 1; frame <= 5; frame++ {
		r.render(loud, now.Add(time.Duration(frame)*33*time.Millisecond))
	}
	for _, point := range []image.Point{{X: 480, Y: 3}, {X: 3, Y: 240}} {
		low, high := quiet.RGBAAt(point.X, point.Y), loud.RGBAAt(point.X, point.Y)
		if high.R <= low.R+60 || high.G <= low.G+30 {
			t.Errorf("reply rim did not brighten with audio at %v: quiet=%v loud=%v", point, low, high)
		}
	}
	if quiet.RGBAAt(480, 434) != loud.RGBAAt(480, 434) {
		t.Fatal("reply audio changed the former compact-orb area")
	}
}

func TestMusicSpectrumUsesControlledAttackAndSmoothRelease(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	media := &show.Media{Active: true, Title: "Garden Song", Spectrum: []float64{.1}}
	r := &renderer{state: show.Snapshot{Media: media}}
	r.updateMusicLevels(now, 0)
	if got := r.musicBinLevel(0, 0); math.Abs(got-.1) > .001 {
		t.Fatalf("initial music level = %.3f, want .1", got)
	}

	media.Spectrum[0] = .9
	r.updateMusicLevels(now.Add(33*time.Millisecond), .033)
	risen := r.musicBinLevel(0, .033)
	if risen <= .4 || risen >= .65 {
		t.Fatalf("attack level = %.3f, want a controlled smoothed rise between .4 and .65", risen)
	}

	media.Spectrum[0] = .1
	r.updateMusicLevels(now.Add(66*time.Millisecond), .066)
	released := r.musicBinLevel(0, .066)
	if released <= .1 || released >= risen {
		t.Fatalf("release level = %.3f, want a smoothed fall between .1 and %.3f", released, risen)
	}
}

func TestCheckersCachesStaticFrameButKeepsSpectrumLiveAcrossVoice(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	media := &show.Media{
		Active: true, PlaybackState: "playing", Title: "Garden Song", Artist: "The Taters",
		ProgressMS: 10_000, DurationMS: 180_000, ProgressUpdatedAtUnixMS: now.UnixMilli(),
		PlaybackSpeed: 1000, Spectrum: []float64{.1, .2, .3, .4},
	}
	r := &renderer{faces: faces, state: show.Snapshot{Phase: "idle", Connected: true, Media: media}}
	first := image.NewRGBA(image.Rect(0, 0, 960, 480))
	r.render(first, now)
	base := r.checkersBase
	if base == nil {
		t.Fatal("Checkers music did not cache its static frame")
	}

	media.Spectrum = []float64{.9, .8, .7, .6}
	second := image.NewRGBA(first.Rect)
	r.render(second, now.Add(33*time.Millisecond))
	if r.checkersBase != base {
		t.Fatal("spectrum-only frame rebuilt the static Checkers music UI")
	}
	if bytes.Equal(first.Pix, second.Pix) {
		t.Fatal("cached Checkers music frame stopped updating the live spectrum")
	}

	r.render(image.NewRGBA(first.Rect), now.Add(time.Second))
	if r.checkersBase != base {
		t.Fatal("progress tick rebuilt the static Checkers music UI")
	}
	r.state.Phase = "listening"
	voice := image.NewRGBA(first.Rect)
	r.render(voice, now.Add(1100*time.Millisecond))
	voiceBase := r.checkersBase
	if voiceBase == nil || r.checkersBaseKey == "" {
		t.Fatal("voice state did not retain the static Checkers cache")
	}
	media.Spectrum = []float64{.2, .9, .2, .9}
	r.render(voice, now.Add(1133*time.Millisecond))
	if r.checkersBase != voiceBase {
		t.Fatal("spectrum-only voice frame rebuilt the static Checkers UI")
	}
}

func TestCheckersWeatherAndReplyUseOnlyMotionRegions(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	r := &renderer{faces: faces, state: show.Snapshot{
		Phase: "speaking", Connected: true, DisplayEdgeGlow: true, AudioLevel: .1, Room: "Kitchen",
		Weather: &show.Weather{TemperatureText: "72°", Condition: "Clear", ConditionKind: "sun"},
	}}
	canvas := image.NewRGBA(image.Rect(0, 0, 960, 480))
	r.render(canvas, now)
	base := r.checkersBase
	if base == nil {
		t.Fatal("Checkers speaking/weather frame was not cached")
	}
	r.state.AudioLevel = .8
	r.render(canvas, now.Add(time.Second/30))
	if r.checkersBase != base {
		t.Fatal("audio-level-only frame rebuilt the static Checkers UI")
	}
	if len(r.presentRegions) == 0 {
		t.Fatal("animated Checkers frame reported no motion regions")
	}
	for _, region := range r.presentRegions {
		if region == canvas.Rect {
			t.Fatal("animated Checkers frame requested a full-frame present")
		}
	}
}

func TestCachedCheckersFrameMatchesFullComposition(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	state := show.Snapshot{
		Phase: "speaking", Connected: true, AudioLevel: .65, Room: "Kitchen",
		Weather: &show.Weather{
			TemperatureText: "72°", Condition: "Partly cloudy", ConditionKind: "partly",
			HumidityText: "48%", IndoorTemperatureText: "71°", IndoorHumidityText: "44%",
		},
	}
	cachedRenderer := &renderer{faces: faces, animationStart: now, state: state}
	cached := image.NewRGBA(image.Rect(0, 0, 960, 480))
	cachedRenderer.render(cached, now.Add(1250*time.Millisecond))

	fullRenderer := &renderer{faces: faces, animationStart: now, state: state}
	full := image.NewRGBA(cached.Rect)
	seconds := now.Add(1250 * time.Millisecond).Sub(now).Seconds()
	fullRenderer.renderCheckers(full, now.Add(1250*time.Millisecond), 960, 480, seconds, accentForTheme(state.Phase, state.DisplayTheme), true)
	if !bytes.Equal(cached.Pix, full.Pix) {
		t.Fatal("cached Checkers composition differs from a full render")
	}
}

func TestStaticCheckersVoiceFrameSkipsUnchangedPresent(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	r := &renderer{faces: faces, state: show.Snapshot{Phase: "listening", Connected: true}}
	canvas := image.NewRGBA(image.Rect(0, 0, 960, 480))
	r.render(canvas, now)
	r.render(canvas, now.Add(time.Second/30))
	if len(r.presentRegions) != 0 {
		t.Fatalf("unchanged static Checkers frame regions = %v, want none", r.presentRegions)
	}
}

func TestToolCallKeepsWeatherCardAndHasNoSpinningFallback(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	weather := &show.Weather{TemperatureText: "72°", Condition: "Partly cloudy", ConditionKind: "partly"}
	r := &renderer{faces: faces, state: show.Snapshot{Phase: "idle", Weather: weather}}
	idle := image.NewRGBA(image.Rect(0, 0, 960, 480))
	r.drawRight(idle, now, 960, 480, 2, color.RGBA{255, 147, 66, 255})
	r.state.Phase = "tool_call"
	r.state.ToolName = "home_assistant"
	working := image.NewRGBA(idle.Rect)
	r.drawRight(working, now, 960, 480, 2, color.RGBA{255, 147, 66, 255})
	if bytes.Equal(idle.Pix, working.Pix) {
		t.Fatal("tool status did not update the weather heading")
	}
	if !bytes.Equal(idle.Pix[65*idle.Stride:], working.Pix[65*working.Stride:]) {
		t.Fatal("tool call replaced more than the weather heading")
	}

	r.state.Weather = nil
	first := image.NewRGBA(idle.Rect)
	second := image.NewRGBA(idle.Rect)
	r.drawRight(first, now, 960, 480, 2, color.RGBA{255, 147, 66, 255})
	r.drawRight(second, now, 960, 480, 9, color.RGBA{255, 147, 66, 255})
	if !bytes.Equal(first.Pix, second.Pix) {
		t.Fatal("tool status fallback is still animated")
	}
}

func TestCheckersNowPlayingReplacesWeatherButNotAwareness(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	media := &show.Media{
		Active: true, PlaybackState: "playing", GroupName: "Downstairs",
		Title: "Garden Song", Artist: "The Taters", Album: "Freshly Planted",
		PrimaryColor: [3]uint8{26, 112, 170}, AccentColor: [3]uint8{255, 124, 44},
		ProgressMS: 75_000, DurationMS: 240_000, PlaybackSpeed: 1000,
		ProgressUpdatedAtUnixMS: now.UnixMilli(), Spectrum: []float64{.1, .3, .8, .4, .6, .2},
	}
	r := &renderer{faces: faces, state: show.Snapshot{
		Phase: "idle", Media: media,
		Weather: &show.Weather{TemperatureText: "72°", Condition: "Sunny", ConditionKind: "sun"},
	}}
	nowPlaying := image.NewRGBA(image.Rect(0, 0, 960, 480))
	r.drawRight(nowPlaying, now, 960, 480, 2, color.RGBA{255, 132, 48, 255})
	r.state.Media = nil
	weather := image.NewRGBA(nowPlaying.Rect)
	r.drawRight(weather, now, 960, 480, 2, color.RGBA{255, 132, 48, 255})
	if bytes.Equal(nowPlaying.Pix, weather.Pix) {
		t.Fatal("active Sendspin media did not replace the weather pane")
	}

	r.state.Media = media
	r.state.Notification = &show.Notification{ID: "door", CameraName: "Front Door", Description: "A person is at the door", ExpiresAtUnixMS: now.Add(time.Minute).UnixMilli()}
	withMedia := image.NewRGBA(nowPlaying.Rect)
	r.drawRight(withMedia, now, 960, 480, 2, color.RGBA{255, 132, 48, 255})
	r.state.Media = nil
	withoutMedia := image.NewRGBA(nowPlaying.Rect)
	r.drawRight(withoutMedia, now, 960, 480, 2, color.RGBA{255, 132, 48, 255})
	if !bytes.Equal(withMedia.Pix, withoutMedia.Pix) {
		t.Fatal("now playing overrode an awareness notification")
	}
}

func TestNowPlayingHeadingPreservesVoiceAndToolFeedback(t *testing.T) {
	media := &show.Media{Active: true, GroupName: "Downstairs"}
	if got := nowPlayingHeading(show.Snapshot{Phase: "idle"}, media); got != "NOW PLAYING  ·  DOWNSTAIRS" {
		t.Fatalf("idle now-playing heading = %q", got)
	}
	if got := nowPlayingHeading(show.Snapshot{Phase: "listening"}, media); got != "LISTENING" {
		t.Fatalf("listening heading = %q", got)
	}
	if got := nowPlayingHeading(show.Snapshot{Phase: "tool_call", ToolName: "room_vision"}, media); got != "USING ROOM VISION" {
		t.Fatalf("tool heading = %q", got)
	}
}

func TestConnectedStatusUsesAssistantFirstName(t *testing.T) {
	if got := connectedStatus("Jarvis Totterson"); got != "Jarvis Online" {
		t.Fatalf("connected status = %q", got)
	}
	if got := connectedStatus("  "); got != "Tater Online" {
		t.Fatalf("empty assistant name fallback = %q", got)
	}
}

func TestAssistantStatusTracksVoiceModeAndTheme(t *testing.T) {
	base := show.Snapshot{Connected: true, AssistantName: "Spud Totterson", DisplayTheme: "violet"}
	cases := []struct {
		phase string
		want  string
	}{
		{"idle", "Spud Online"},
		{"listening", "Listening"},
		{"thinking", "Thinking"},
		{"tool_call", "Working"},
		{"speaking", "Replying"},
		{"intercom", "Broadcasting"},
	}
	for _, test := range cases {
		state := base
		state.Phase = test.phase
		got, ink := assistantStatus(state)
		if got != test.want {
			t.Errorf("%s status = %q, want %q", test.phase, got, test.want)
		}
		if want := accentForTheme(test.phase, state.DisplayTheme); ink != want {
			t.Errorf("%s status color = %v, want theme color %v", test.phase, ink, want)
		}
	}
}

func imagesEqualOutside(left, right *image.RGBA, ignored image.Rectangle) bool {
	if left == nil || right == nil || left.Rect != right.Rect {
		return false
	}
	for y := left.Rect.Min.Y; y < left.Rect.Max.Y; y++ {
		for x := left.Rect.Min.X; x < left.Rect.Max.X; x++ {
			if image.Pt(x, y).In(ignored) {
				continue
			}
			if left.RGBAAt(x, y) != right.RGBAAt(x, y) {
				return false
			}
		}
	}
	return true
}

func TestWeatherIconMovesAcrossFrames(t *testing.T) {
	for _, kind := range []string{"sun", "partly", "cloud", "rain", "storm", "snow", "fog", "wind"} {
		first := image.NewRGBA(image.Rect(0, 0, 120, 120))
		second := image.NewRGBA(image.Rect(0, 0, 120, 120))
		r := &renderer{}
		r.drawWeatherIcon(first, 60, 48, 35, kind, 0, weatherColor(kind))
		r.drawWeatherIcon(second, 60, 48, 35, kind, 1.3, weatherColor(kind))
		if sha256.Sum256(first.Pix) == sha256.Sum256(second.Pix) {
			t.Errorf("%s icon did not animate", kind)
		}
	}
}

func TestRepresentativeStatesRenderDistinctFrames(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	direction := 55.0
	base := show.Snapshot{
		Phase:           "idle",
		Connected:       true,
		DeviceName:      "Tater Checkers",
		Room:            "Family Room",
		Message:         "Ready when you are",
		VolumePercent:   45,
		TaterTimeUnixMS: now.UnixMilli(),
	}
	cases := map[string]show.Snapshot{
		"idle": base,
		"voice": func() show.Snapshot {
			state := base
			state.Phase = "listening"
			state.AudioLevel = .72
			state.DirectionDegrees = &direction
			return state
		}(),
		"tool": func() show.Snapshot {
			state := base
			state.Phase = "tool_call"
			state.ToolName = "home_assistant"
			state.ToolMessage = "Checking the lights"
			return state
		}(),
		"weather": func() show.Snapshot {
			state := base
			state.Weather = &show.Weather{TemperatureText: "72°", TemperatureUnit: "F", Condition: "Partly cloudy", ConditionKind: "cloudy", FeelsLikeText: "Feels like 74°", FeelsLikeRelation: "warmer", HumidityText: "44%", WindText: "8 mph", RainText: "10%", IndoorTemperatureText: "71°", IndoorHumidityText: "42%"}
			return state
		}(),
		"weather-voice": func() show.Snapshot {
			state := base
			state.Phase = "speaking"
			state.AudioLevel = .68
			state.Weather = &show.Weather{TemperatureText: "72°", ConditionKind: "rain", Condition: "Rain showers"}
			return state
		}(),
		"timer": func() show.Snapshot {
			state := base
			state.TimerActive = true
			state.Timer = &show.Timer{Active: true, ID: "dinner", Label: "Dinner", OriginalDurationMS: 600_000, DeadlineUnixMS: now.Add(4 * time.Minute).UnixMilli()}
			return state
		}(),
		"notification": func() show.Snapshot {
			state := base
			state.Notification = &show.Notification{ID: "door", CameraName: "Front Door", Description: "A person was detected at the front door", Priority: "critical", ExpiresAtUnixMS: now.Add(time.Minute).UnixMilli()}
			return state
		}(),
		"setup": func() show.Snapshot {
			state := base
			state.Phase = "setup"
			state.Connected = false
			return state
		}(),
		"setup-hotspot": {Phase: "setup", Connected: false, DeviceName: "Tater Checkers", Room: "Tater-Setup-D4B7", Message: "192.168.4.1"},
		"connecting":    {Phase: "offline", Message: "Connecting to Tater", DeviceName: "Tater Checkers"},
	}
	seen := map[string]string{}
	groups := map[string]string{"idle": "home", "voice": "home", "tool": "home"}
	for name, state := range cases {
		canvas := image.NewRGBA(image.Rect(0, 0, 960, 480))
		renderer := &renderer{faces: faces, state: state, received: now, animationStart: now.Add(-2 * time.Second)}
		renderer.render(canvas, now)
		digest := fmt.Sprintf("%x", sha256.Sum256(canvas.Pix))
		group := groups[name]
		if group == "" {
			group = name
		}
		if previous, duplicate := seen[digest]; duplicate && previous != group {
			t.Errorf("%s and %s unexpectedly produced the same frame", group, previous)
		}
		seen[digest] = group
		if canvas.RGBAAt(0, 0).A != 255 || canvas.RGBAAt(959, 479).A != 255 {
			t.Errorf("%s did not paint an opaque full-screen frame", name)
		}
	}
}

func TestConnectingScreenUsesPrimaryTaterLogo(t *testing.T) {
	const primaryLogoSHA256 = "c825e118c81171a4175a9870877131f09a148024d56aa1d45ad9a315bdcddb04"
	if got := fmt.Sprintf("%x", sha256.Sum256(taterPrimaryLogoPNG)); got != primaryLogoSHA256 {
		t.Fatalf("embedded logo is not the main Tater app's primary transparent asset: %s", got)
	}
	if _, _, _, alpha := taterPrimaryLogo.At(0, 0).RGBA(); alpha != 0 {
		t.Fatal("Tater logo lost its transparent background")
	}
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	first := image.NewRGBA(image.Rect(0, 0, 960, 480))
	second := image.NewRGBA(first.Bounds())
	now := time.Unix(1_800_000_000, 0)
	r := &renderer{faces: faces, state: show.Snapshot{Phase: "connecting"}, animationStart: now}
	r.render(first, now)
	background := r.connectingBase
	r.render(second, now.Add(600*time.Millisecond))
	if r.connectingBase != background {
		t.Fatal("connecting logo and glow were rebuilt every animation frame")
	}
	if sha256.Sum256(first.Pix) == sha256.Sum256(second.Pix) {
		t.Fatal("connection indicator stopped animating")
	}
	if corner := first.RGBAAt(0, 0); corner.R > 35 || corner.G > 20 || corner.B > 20 || corner.A != 255 {
		t.Fatalf("connecting screen is not an opaque near-black background: %v", corner)
	}
	bright := 0
	for y := 40; y < 400; y += 8 {
		for x := 110; x < 850; x += 8 {
			pixel := first.RGBAAt(x, y)
			if pixel.R > 190 && pixel.G > 170 && pixel.B > 140 {
				bright++
			}
		}
	}
	if bright < 100 {
		t.Fatalf("main Tater logo is missing or too small on the boot-style screen (%d bright samples)", bright)
	}
}

// Set TATER_SHOW_PREVIEW to inspect one representative composed screen without
// opening the hardware framebuffer. Ordinary test runs skip this artifact.
func TestRenderPreview(t *testing.T) {
	path := os.Getenv("TATER_SHOW_PREVIEW")
	if path == "" {
		t.Skip("set TATER_SHOW_PREVIEW to render a screenshot")
	}
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	previewTargetValue := os.Getenv("TATER_SHOW_PREVIEW_TARGET")
	previewCrown := previewTargetValue == "crown"
	previewRook := previewTargetValue == "rook"
	state := show.Snapshot{
		Phase:           "speaking",
		Connected:       true,
		DeviceName:      "Tater Checkers",
		AssistantName:   "Spud Assistant",
		Room:            "Family Room",
		Message:         "Here is the forecast",
		AudioLevel:      .68,
		TaterTimeUnixMS: now.UnixMilli(),
		Weather: &show.Weather{
			TemperatureText: "72°", TemperatureUnit: "F", Condition: "Rain showers",
			ConditionKind: "rain", FeelsLikeText: "Feels like 69°", FeelsLikeRelation: "cooler",
			HumidityText: "68%", WindText: "8 mph", RainText: "65%", LightningText: "3 strikes",
			IndoorTemperatureText: "71°", IndoorHumidityText: "42%",
		},
		LastEvent: &show.Notification{
			ID: "last-door", Kind: "awareness", Source: "awareness", CameraName: "Front Door",
			Description: "A person was detected at the front door.", CreatedAtUnixMS: now.Add(-5 * time.Minute).UnixMilli(),
		},
	}
	weather := state.Weather
	switch os.Getenv("TATER_SHOW_PREVIEW_STATE") {
	case "menu":
		state.Phase = "idle"
		current, target := 71.0, 72.0
		state.Climate = &show.Climate{
			Available: true, RoomID: "living_room", RoomName: "Living Room",
			CurrentTemperature: &current, TargetTemperature: &target, TemperatureUnit: "F",
		}
	case "cameras":
		state.Phase = "idle"
		state.Cameras = &show.Cameras{Available: true, Items: []show.Camera{
			{ID: "front", Name: "Front Porch Doorbell", Room: "Entry", Doorbell: true, Talkback: true, Available: true},
			{ID: "driveway", Name: "North Driveway Camera", Room: "Outside", Available: true},
			{ID: "backyard", Name: "Backyard Patio Camera", Room: "Backyard", Available: true},
			{ID: "nursery", Name: "Garage Interior Camera", Room: "Garage", Talkback: true, Available: true},
			{ID: "garage", Name: "Garage", Room: "Garage", Talkback: true, Available: true},
			{ID: "patio", Name: "Patio", Room: "Outside", Available: true},
			{ID: "office", Name: "Office", Room: "Office", Available: true},
			{ID: "workshop", Name: "Workshop", Room: "Workshop", Talkback: true, Available: true},
		}}
	case "camera-view":
		state.Phase = "idle"
		state.Cameras = &show.Cameras{Available: true, SelectedID: "front", SelectedName: "Front Door", SelectedRoom: "Entry", SelectedTalkback: true}
	case "climate":
		state.Phase = "idle"
		current, target, humidity := 71.4, 72.6, 42.0
		state.Climate = &show.Climate{
			Available: true, RoomID: "living_room", RoomName: "Living Room",
			CurrentTemperature: &current, TargetTemperature: &target, CurrentHumidity: &humidity,
			TemperatureUnit: "F", HVACMode: "heat", HVACAction: "heating",
			AvailableHVACModes: []string{"heat", "cool", "auto", "off"},
			CanSetTemperature:  true, CanSetMode: true, MinimumTemperature: 45, MaximumTemperature: 90, TemperatureStep: 1,
			RoomTemperatures: []show.RoomTemperature{
				{RoomName: "Living Room", Temperature: 71.4, TemperatureUnit: "F"},
				{RoomName: "Kitchen", Temperature: 72.6, TemperatureUnit: "F"},
				{RoomName: "Office", Temperature: 71.5, TemperatureUnit: "F"},
				{RoomName: "Bedroom", Temperature: 68.2, TemperatureUnit: "F"},
				{RoomName: "Nursery", Temperature: 69.8, TemperatureUnit: "F"},
				{RoomName: "Garage", Temperature: 64.4, TemperatureUnit: "F"},
			},
		}
	case "sat-chat":
		state.Phase = "idle"
		state.SatChat = &show.SatChat{Self: "native:family-room", Participants: []show.SatChatParticipant{
			{ID: "native:kitchen", Name: "Tater Crown Kitchen", Room: "Kitchen", Audio: true, Video: true},
			{ID: "native:living-room", Name: "Tater Athena Family Room", Room: "Family Room", Audio: true, Video: true},
			{ID: "native:office", Name: "Tater Checkers Office", Room: "Office", Audio: true},
			{ID: "native:bedroom", Name: "Tater Rook Bedroom", Room: "Bedroom", Audio: true},
			{ID: "native:workshop", Name: "Workshop", Room: "Workshop", Audio: true, Video: true},
			{ID: "native:patio", Name: "Patio", Room: "Patio", Audio: true},
			{ID: "native:garage", Name: "Garage", Room: "Garage", Audio: true, Busy: true},
			{ID: "native:nursery", Name: "Nursery", Room: "Nursery", Audio: true, Video: true},
			{ID: "native:studio", Name: "Studio", Room: "Studio", Audio: true, Video: true},
			{ID: "native:guest-room", Name: "Guest Room", Room: "Guest Room", Audio: true},
			{ID: "native:basement", Name: "Basement", Room: "Basement", Audio: true},
			{ID: "native:porch", Name: "Front Porch", Room: "Front Porch", Audio: true, Video: true},
		}}
	case "sat-chat-video":
		state.Phase = "idle"
		state.SatChat = &show.SatChat{
			Self: "native:family-room",
			Call: &show.SatChatCall{
				ID: "preview-call", State: "active", Mode: "video",
				Caller: "native:family-room", CallerName: "Family Room",
				Target: "native:kitchen", TargetName: "Kitchen",
			},
		}
	case "two-sensors":
		state.Phase = "idle"
		state.Weather.HumidityText = ""
		state.Weather.WindText = ""
		state.Weather.RainText = ""
		state.Weather.LightningText = ""
	case "volume":
		state.Phase = "idle"
		state.Message = "Ready when you are"
		state.VolumePercent = 64
	case "timer":
		state.Weather = nil
		state.TimerActive = true
		state.Timer = &show.Timer{Active: true, ID: "dinner", Label: "Dinner", OriginalDurationMS: 600_000, DeadlineUnixMS: now.Add(4 * time.Minute).UnixMilli()}
		state.Message = "Your dinner timer is running"
	case "notification":
		state.Weather = nil
		state.Notification = &show.Notification{ID: "door", Kind: "awareness", Source: "awareness", CameraName: "Front Door", Description: "A person was detected at the front door. Check the camera or ask Tater what happened.", Priority: "critical", CreatedAtUnixMS: now.UnixMilli(), ExpiresAtUnixMS: now.Add(time.Minute).UnixMilli()}
		state.Message = "Someone is at the door"
	case "media":
		state.Phase = "idle"
		state.Media = &show.Media{
			Active: true, PlaybackState: "playing", GroupName: "Downstairs",
			Title: "Garden Song", Artist: "The Taters", Album: "Freshly Planted",
			PrimaryColor: [3]uint8{30, 108, 165}, AccentColor: [3]uint8{255, 124, 44},
			ProgressMS: 75_000, DurationMS: 240_000, PlaybackSpeed: 1000,
			ProgressUpdatedAtUnixMS: now.UnixMilli(),
			Spectrum:                []float64{.12, .28, .72, .55, .9, .38, .61, .22, .48, .77, .34, .18},
		}
	case "last-event":
		state.Phase = "idle"
		state.Message = "Ready when you are"
	case "empty":
		state.Phase = "idle"
		state.Message = "Ready when you are"
		state.LastEvent = nil
	case "night":
		state.Phase = "idle"
		state.Message = "Good night"
		state.Weather.Condition = "Clear"
		state.Weather.ConditionKind = "sun"
		state.Weather.Daylight = "night"
		state.Weather.RainText = "0 in/hr"
	case "setup-hotspot":
		state = show.Snapshot{Phase: "setup", Connected: false, DeviceName: "Tater Checkers", Room: "Tater-Setup-D4B7", Message: "192.168.4.1"}
	case "connecting":
		state = show.Snapshot{Phase: "connecting", Connected: false, DeviceName: "Tater Checkers", Message: "Connecting to Tater"}
	}
	if theme := strings.TrimSpace(os.Getenv("TATER_SHOW_PREVIEW_THEME")); theme != "" {
		state.DisplayTheme = theme
	}
	width, height, previewTarget := 960, 480, "checkers"
	if previewCrown {
		width, height, previewTarget = 1280, 800, "crown"
		state.DeviceName = "Tater Crown"
		if state.Connected && state.Weather == nil {
			state.Weather = weather
		}
	} else if previewRook {
		width, height, previewTarget = 480, 480, "rook"
		state.DeviceName = "Tater Rook"
		if state.Connected && state.Weather == nil {
			state.Weather = weather
		}
	}
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	r := &renderer{
		faces: faces, target: previewTarget, spotScreen: previewRook, screenWidth: width, screenHeight: height,
		state: state, received: now, animationStart: now.Add(-2 * time.Second), satChatOpen: state.SatChat != nil,
		climateOpen: os.Getenv("TATER_SHOW_PREVIEW_STATE") == "climate",
		menuOpen:    os.Getenv("TATER_SHOW_PREVIEW_STATE") == "menu",
		cameraOpen:  strings.HasPrefix(os.Getenv("TATER_SHOW_PREVIEW_STATE"), "camera"),
	}
	if os.Getenv("TATER_SHOW_PREVIEW_STATE") == "camera-view" {
		frame := image.NewRGBA(r.cameraVideoBounds())
		fillGradient(frame, color.RGBA{14, 42, 68, 255}, color.RGBA{105, 64, 39, 255})
		r.cameraFrameImage = prepareContainImage(frame, r.cameraVideoBounds().Size())
	}
	if os.Getenv("TATER_SHOW_PREVIEW_STATE") == "sat-chat-video" {
		if wallpaper := r.activeWallpaper(now); wallpaper != nil {
			r.satChatVideoImage = prepareCoverImage(wallpaper, r.satChatVideoBounds().Size())
		}
	}
	if os.Getenv("TATER_SHOW_PREVIEW_STATE") == "volume" {
		r.beginVolumeOverlay(state.VolumePercent, now)
	}
	r.render(canvas, now)
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, canvas); err != nil {
		t.Fatal(err)
	}
}
