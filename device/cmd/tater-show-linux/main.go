//go:build linux

// tater-show-linux is the native Checkers, Crown, Athena, and Rook renderer. The Tater daemon remains
// the source of truth; this process handles screen, touch, and loopback commands.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/linuxinput"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/linuxscreen"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

var (
	version = "dev"
	target  = "checkers"
)

type socketClient struct {
	mu     sync.Mutex
	writer *bufio.Writer
	states chan show.Snapshot
}

func newSocketClient() *socketClient {
	return &socketClient{states: make(chan show.Snapshot, 4)}
}

func (client *socketClient) run(ctx context.Context) {
	for ctx.Err() == nil {
		connection, err := (&net.Dialer{Timeout: 1200 * time.Millisecond}).DialContext(ctx, "tcp", show.DefaultAddress)
		if err != nil {
			wait(ctx, 1200*time.Millisecond)
			continue
		}
		client.mu.Lock()
		client.writer = bufio.NewWriter(connection)
		client.mu.Unlock()
		client.send("screen.ready")
		scanner := bufio.NewScanner(connection)
		scanner.Buffer(make([]byte, 64*1024), 256*1024)
		for scanner.Scan() {
			var snapshot show.Snapshot
			if err := json.Unmarshal(scanner.Bytes(), &snapshot); err != nil || snapshot.Protocol != show.ProtocolVersion || snapshot.Type != "snapshot" {
				continue
			}
			select {
			case client.states <- snapshot:
			default:
				select {
				case <-client.states:
				default:
				}
				client.states <- snapshot
			}
		}
		client.mu.Lock()
		client.writer = nil
		client.mu.Unlock()
		_ = connection.Close()
		wait(ctx, 1200*time.Millisecond)
	}
}

func (client *socketClient) send(action string) {
	client.sendCommand(show.Command{Protocol: show.ProtocolVersion, Type: "command", Action: action})
}

func (client *socketClient) sendSatChat(action, target, mode, callID string) {
	client.sendCommand(show.Command{
		Protocol: show.ProtocolVersion, Type: "command", Action: action,
		Target: target, Mode: mode, CallID: callID,
	})
}

func (client *socketClient) sendClimateTemperature(roomID string, value float64, unit string) {
	client.sendCommand(show.Command{
		Protocol: show.ProtocolVersion, Type: "command", Action: "climate.set_temperature",
		Target: roomID, Value: &value, Unit: unit,
	})
}

func (client *socketClient) sendClimateMode(roomID, mode string) {
	client.sendCommand(show.Command{
		Protocol: show.ProtocolVersion, Type: "command", Action: "climate.set_mode",
		Target: roomID, Mode: mode,
	})
}

func (client *socketClient) sendCamera(action, target string) {
	client.sendCommand(show.Command{
		Protocol: show.ProtocolVersion, Type: "command", Action: action, Target: target,
	})
}

func (client *socketClient) sendDiagnostics(diagnostics show.ScreenDiagnostics) {
	client.sendCommand(show.Command{
		Protocol: show.ProtocolVersion, Type: "command", Action: "screen.diagnostics",
		Diagnostics: &diagnostics,
	})
}

func (client *socketClient) sendCommand(command show.Command) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.writer == nil {
		return
	}
	if err := json.NewEncoder(client.writer).Encode(command); err == nil {
		_ = client.writer.Flush()
	}
}

func wait(ctx context.Context, duration time.Duration) {
	select {
	case <-time.After(duration):
	case <-ctx.Done():
	}
}

type faceSet struct {
	regular map[int]font.Face
	bold    map[int]font.Face
}

func newFaceSet() (*faceSet, error) {
	regular, err := opentype.Parse(goregular.TTF)
	if err != nil {
		return nil, err
	}
	bold, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return nil, err
	}
	result := &faceSet{regular: map[int]font.Face{}, bold: map[int]font.Face{}}
	for _, size := range []int{8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 24, 25, 26, 28, 30, 34, 36, 42, 48, 64, 74, 82, 96} {
		result.regular[size], err = opentype.NewFace(regular, &opentype.FaceOptions{Size: float64(size), DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			return nil, err
		}
		result.bold[size], err = opentype.NewFace(bold, &opentype.FaceOptions{Size: float64(size), DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

type renderer struct {
	faces                   *faceSet
	spotScreen              bool
	target                  string
	screenWidth             int
	screenHeight            int
	state                   show.Snapshot
	received                time.Time
	clockUnixMS             int64
	clockSourceUnixMS       int64
	clockReceived           time.Time
	animationStart          time.Time
	notificationID          string
	notificationImage       image.Image
	notificationDismissedID string
	lastEventID             string
	lastEventImage          image.Image
	mediaID                 string
	mediaImage              image.Image
	wallpaperID             string
	wallpaperImage          image.Image
	defaultWallpaperID      string
	defaultWallpaperImage   image.Image
	satChatVideoID          string
	satChatVideoShown       string
	satChatVideoDrawn       string
	satChatVideoImage       image.Image
	satChatFrameKey         string
	satChatFrameCanvas      *image.RGBA
	intercomPressed         bool
	menuOpen                bool
	cameraOpen              bool
	cameraPage              int
	cameraTalkPressed       bool
	cameraFrameID           string
	cameraFrameShown        string
	cameraFrameImage        image.Image
	cameraFrameDrawn        string
	cameraFrameCanvas       *image.RGBA
	cameraFrameKey          string
	cameraStreamImage       *image.RGBA
	cameraStreamShown       bool
	satChatOpen             bool
	climateOpen             bool
	climatePage             int
	satChatPage             int
	satChatTouchDown        bool
	satChatTouchAt          time.Time
	timerPressed            bool
	intercomIcon            *image.RGBA
	connectingBase          *image.RGBA
	spotBase                *image.RGBA
	spotBaseAccent          color.RGBA
	spotTouchDown           bool
	spotTouchAt             time.Time
	spotTimerDown           bool
	spotReplyGlow           float64
	checkersReplyGlow       float64
	musicLevels             [12]float64
	musicLevelsAt           time.Time
	musicLevelsMedia        string
	musicLevelsReady        bool
	musicProgressDrawn      string
	checkersBase            *image.RGBA
	checkersBaseKey         string
	checkersCanvas          *image.RGBA
	spotFrameBase           *image.RGBA
	spotFrameBaseKey        string
	spotFrameCanvas         *image.RGBA
	crownContext            *image.RGBA
	crownBase               *image.RGBA
	crownBaseKey            string
	crownCanvas             *image.RGBA
	volumeOverlayUntil      time.Time
	volumeOverlayPercent    int
	volumeOverlayBase       *image.RGBA
	volumeOverlayCanvas     *image.RGBA
	volumeOverlayPainted    bool
	presentRegions          []image.Rectangle
	transientPresent        bool
	touchDowns              uint64
	touchMoves              uint64
	touchUps                uint64
	lastTouchX              int
	lastTouchY              int
	lastTouchAtMS           int64
	nightModeActive         bool
	nightClockKey           string
	nightClockCanvas        *image.RGBA
}

type screenImageKind uint8

const (
	notificationScreenImage screenImageKind = iota
	lastEventScreenImage
	mediaScreenImage
	wallpaperScreenImage
	satChatVideoScreenImage
	cameraVideoScreenImage
)

type screenImageResult struct {
	kind          screenImageKind
	identity      string
	session       string
	image         image.Image
	loadDuration  time.Duration
	scaleDuration time.Duration
}

type screenImageRequest struct {
	kind     screenImageKind
	identity string
	session  string
	rawURL   string
	size     image.Point
}

const (
	// Keep only a small quadrant visible, tucked into the lower-left edge.
	intercomCenterX       = -24
	intercomCenterY       = 506
	intercomRadius        = 114
	micIconLeft           = 8
	micIconTop            = 422
	micIconWidth          = 40
	micIconHeight         = 48
	volumeOverlayDuration = 2 * time.Second
)

// The right-hand content can use the lower half of the display while leaving
// a narrow gutter beside the clock and connection status.
func rightPane(width, height int) image.Rectangle {
	return image.Rect(width*45/100, 24, width-36, height-20)
}

// Crown has enough horizontal room to bring its permanent conditions and
// rotating context panes closer to the clock. Checkers keeps its original
// 45-percent split exactly as shipped.
func crownPane(width, height int) image.Rectangle {
	return image.Rect(width*35/100, 24, width-36, height-20)
}

func musicBarsBounds(pane image.Rectangle) image.Rectangle {
	return image.Rect(pane.Min.X, 348, pane.Max.X, 430)
}

func timerStopBounds(width, height int) image.Rectangle {
	return image.Rect(width/2-150, height-88, width/2+150, height-28)
}

func timerStopBoundsIn(pane image.Rectangle) image.Rectangle {
	cx := (pane.Min.X + pane.Max.X) / 2
	return image.Rect(cx-145, pane.Max.Y-64, cx+145, pane.Max.Y)
}

func intercomGeometry(width, height int) (centerX, centerY, radius, iconLeft, iconTop int) {
	if width <= 0 {
		width = 960
	}
	if height <= 0 {
		height = 480
	}
	iconLeft = micIconLeft
	if width >= 1200 {
		iconLeft -= 4
	}
	return -max(24, width/40), height + 26, intercomRadius, iconLeft, height - 58
}

func checkersMusicBarsBounds(width, height int) image.Rectangle {
	return musicBarsBounds(rightPane(width, height))
}

func (r *renderer) beginVolumeOverlay(percent int, now time.Time) {
	percent = max(0, min(100, percent))
	if !r.volumeOverlayUntil.IsZero() && !now.Before(r.volumeOverlayUntil) {
		r.clearVolumeOverlay()
	}
	r.volumeOverlayPercent = percent
	r.volumeOverlayUntil = now.Add(volumeOverlayDuration)
	r.volumeOverlayPainted = false
}

func (r *renderer) clearVolumeOverlay() {
	r.volumeOverlayUntil = time.Time{}
	r.volumeOverlayBase = nil
	r.volumeOverlayCanvas = nil
	r.volumeOverlayPainted = false
	r.invalidateFrameCanvases()
}

func (r *renderer) invalidateFrameCanvases() {
	r.invalidateStaticScreenCaches()
	r.nightClockCanvas = nil
	r.nightClockKey = ""
	r.cameraFrameCanvas = nil
	r.cameraFrameKey = ""
	r.satChatFrameCanvas = nil
	r.satChatFrameKey = ""
}

func (r *renderer) render(canvas *image.RGBA, now time.Time) {
	if now.Before(r.volumeOverlayUntil) {
		if r.volumeOverlayBase == nil || r.volumeOverlayBase.Rect != canvas.Rect || r.volumeOverlayCanvas != canvas {
			r.invalidateFrameCanvases()
			r.renderBase(canvas, now)
			r.volumeOverlayBase = image.NewRGBA(canvas.Rect)
			draw.Draw(r.volumeOverlayBase, canvas.Rect, canvas, canvas.Rect.Min, draw.Src)
			r.volumeOverlayCanvas = canvas
			r.volumeOverlayPainted = false
		}
		if r.volumeOverlayPainted {
			r.presentRegions = nil
			return
		}
		draw.Draw(canvas, canvas.Rect, r.volumeOverlayBase, canvas.Rect.Min, draw.Src)
		r.drawVolumeOverlay(canvas, r.volumeOverlayPercent)
		r.presentRegions = []image.Rectangle{canvas.Rect}
		r.transientPresent = false
		r.volumeOverlayPainted = true
		return
	}
	if r.volumeOverlayBase != nil || r.volumeOverlayCanvas != nil {
		r.clearVolumeOverlay()
	}
	r.renderBase(canvas, now)
}

func (r *renderer) renderBase(canvas *image.RGBA, now time.Time) {
	r.presentRegions = []image.Rectangle{canvas.Rect}
	r.transientPresent = false
	if r.nightClockVisible(now) {
		r.renderNightClock(canvas, now)
		return
	}
	if r.nightClockCanvas != nil {
		r.nightClockCanvas, r.nightClockKey = nil, ""
		r.invalidateFrameCanvases()
	}
	width, height := canvas.Rect.Dx(), canvas.Rect.Dy()
	accent := accentForTheme(r.state.Phase, r.state.DisplayTheme)
	seconds := now.Sub(r.animationStart).Seconds()
	if r.animationStart.IsZero() {
		seconds = 0
	}
	r.updateMusicLevels(now, seconds)
	if r.cameraVideoActive() {
		key := r.cameraVideoRenderKey(accent)
		if r.cameraFrameCanvas == canvas && r.cameraFrameKey == key {
			if r.cameraFrameShown == r.cameraFrameDrawn {
				r.presentRegions = nil
				return
			}
			if r.cameraFrameShown != "" && r.cameraFrameImage != nil {
				bounds := r.drawCameraVideoFrame(canvas)
				r.presentRegions = []image.Rectangle{bounds}
				r.transientPresent = true
				r.cameraFrameDrawn = r.cameraFrameShown
				return
			}
		}
		if width == height {
			r.renderSpot(canvas, now, seconds, accent)
		} else {
			r.renderCheckers(canvas, now, width, height, seconds, accent, true)
		}
		r.cameraFrameCanvas = canvas
		r.cameraFrameKey = key
		r.cameraFrameDrawn = r.cameraFrameShown
		return
	}
	if r.cameraFrameCanvas != nil {
		r.cameraFrameCanvas = nil
		r.cameraFrameKey = ""
		r.spotFrameCanvas, r.crownCanvas, r.checkersCanvas = nil, nil, nil
	}
	// Sat Chat has no continuous UI animation of its own. Redrawing it at the
	// normal 30 Hz Crown cadence used to rescale and repaint the same camera
	// frame over and over. Render only when call state or the displayed frame
	// changes; otherwise leave the framebuffer completely untouched.
	if r.satChatVisible() {
		key := r.satChatRenderKey(now, accent)
		if r.satChatFrameCanvas == canvas && r.satChatFrameKey == key {
			if r.satChatVideoShown == r.satChatVideoDrawn {
				r.presentRegions = nil
				return
			}
			if r.satChatVideoShown != "" && r.satChatVideoImage != nil && r.satChatVideoActive() {
				if width == height {
					r.presentRegions = []image.Rectangle{r.drawSpotSatChatVideoFrame(canvas)}
				} else {
					r.presentRegions = []image.Rectangle{r.drawSatChatVideoFrame(canvas, width, height)}
				}
				r.transientPresent = true
				r.satChatVideoDrawn = r.satChatVideoShown
				return
			}
		}
		if width == height {
			r.renderSpot(canvas, now, seconds, accent)
		} else {
			r.renderCheckers(canvas, now, width, height, seconds, accent, true)
		}
		r.satChatFrameCanvas = canvas
		r.satChatFrameKey = key
		r.satChatVideoDrawn = r.satChatVideoShown
		return
	}
	// Sat Chat draws directly over the live framebuffer while the cached home
	// bases remain unchanged. When the call or menu closes, those caches still
	// describe the right pixels but their canvas markers no longer do: the
	// canvas contains the final video frame. Force one complete base restore on
	// the first frame after Sat Chat so Checkers cannot leave video remnants
	// behind until a later minute/weather cache-key change. Crown and Rook use
	// the same transition guard for deterministic teardown.
	leftSatChat := r.satChatFrameCanvas != nil
	r.satChatFrameKey = ""
	r.satChatFrameCanvas = nil
	if leftSatChat {
		r.spotFrameCanvas = nil
		r.crownCanvas = nil
		r.checkersCanvas = nil
	}
	if width == height {
		if r.canCacheSpot(now) {
			key := r.spotFrameCacheKey(now, accent)
			rebuilt := r.spotFrameBase == nil || r.spotFrameBase.Rect != canvas.Rect || key != r.spotFrameBaseKey
			if rebuilt {
				r.spotFrameBase = image.NewRGBA(canvas.Rect)
				r.renderSpotFrame(r.spotFrameBase, now, seconds, accent, false)
				r.spotFrameBaseKey = key
				r.musicProgressDrawn = ""
			}
			if rebuilt || r.spotFrameCanvas != canvas {
				draw.Draw(canvas, canvas.Rect, r.spotFrameBase, canvas.Rect.Min, draw.Src)
				r.presentRegions = []image.Rectangle{canvas.Rect}
			} else {
				r.presentRegions = r.spotMotionRegions(now)
				restoreRegions(canvas, r.spotFrameBase, r.presentRegions)
			}
			r.drawSpotMotion(canvas, now, seconds, accent)
			r.spotFrameCanvas = canvas
			return
		}
		r.spotFrameBaseKey = ""
		r.spotFrameCanvas = nil
		r.renderSpot(canvas, now, seconds, accent)
		return
	}
	if r.canCacheCrown() {
		key := r.crownCacheKey(now, accent)
		rebuilt := r.crownBase == nil || r.crownBase.Rect != canvas.Rect || key != r.crownBaseKey
		if rebuilt {
			r.crownBase = image.NewRGBA(canvas.Rect)
			r.renderCheckers(r.crownBase, now, width, height, seconds, accent, false)
			r.crownBaseKey = key
			r.musicProgressDrawn = ""
		}
		if rebuilt || r.crownCanvas != canvas {
			draw.Draw(canvas, canvas.Rect, r.crownBase, canvas.Rect.Min, draw.Src)
			r.presentRegions = []image.Rectangle{canvas.Rect}
		} else {
			r.presentRegions = r.crownMotionRegions(now, width, height)
			for _, region := range r.presentRegions {
				region = region.Intersect(canvas.Rect)
				if !region.Empty() {
					draw.Draw(canvas, region, r.crownBase, region.Min, draw.Src)
				}
			}
		}
		r.drawCrownMotion(canvas, now, width, height, seconds, accent)
		r.crownCanvas = canvas
		return
	}
	r.crownBaseKey = ""
	r.crownCanvas = nil
	if r.canCacheCheckers(now) {
		key := r.checkersCacheKey(now, accent)
		rebuilt := r.checkersBase == nil || r.checkersBase.Rect != canvas.Rect || key != r.checkersBaseKey
		if rebuilt {
			r.checkersBase = image.NewRGBA(canvas.Rect)
			r.renderCheckers(r.checkersBase, now, width, height, seconds, accent, false)
			r.checkersBaseKey = key
			r.musicProgressDrawn = ""
		}
		if rebuilt || r.checkersCanvas != canvas {
			draw.Draw(canvas, canvas.Rect, r.checkersBase, canvas.Rect.Min, draw.Src)
			r.presentRegions = []image.Rectangle{canvas.Rect}
		} else {
			r.presentRegions = r.checkersMotionRegions(now, width, height)
			restoreRegions(canvas, r.checkersBase, r.presentRegions)
		}
		r.drawCheckersMotion(canvas, now, width, height, seconds, accent)
		r.checkersCanvas = canvas
		return
	}
	r.checkersBaseKey = ""
	r.checkersCanvas = nil
	r.renderCheckers(canvas, now, width, height, seconds, accent, true)
}

func (r *renderer) drawVolumeOverlay(canvas *image.RGBA, percent int) {
	width, height := canvas.Rect.Dx(), canvas.Rect.Dy()
	accent := paletteForTheme(r.state.DisplayTheme).primary
	draw.Draw(canvas, canvas.Rect, image.NewUniform(color.NRGBA{A: 148}), image.Point{}, draw.Over)

	if width == height {
		panelWidth, panelHeight := width*71/100, height*61/100
		panel := image.Rect((width-panelWidth)/2, (height-panelHeight)/2, (width+panelWidth)/2, (height+panelHeight)/2)
		roundedRect(canvas, panel, 30, color.RGBA{8, 14, 21, 232})
		roundedRectOutline(canvas, panel, 30, 2, color.RGBA{255, 255, 255, 32})
		r.drawVolumeSpeaker(canvas, width/2, panel.Min.Y+57, 46, percent, accent)
		r.centeredText(canvas, width/2, panel.Min.Y+158, fmt.Sprintf("%d", percent), 74, false, color.White)
		r.centeredText(canvas, width/2, panel.Min.Y+185, "VOLUME", 14, true, color.RGBA{225, 235, 245, 190})
		r.drawVolumeTrack(canvas, image.Rect(panel.Min.X+42, panel.Max.Y-46, panel.Max.X-42, panel.Max.Y-34), percent, accent)
		return
	}

	crown := r.isCrown()
	panelWidth, panelHeight := width*62/100, height*48/100
	radius, iconSize, numberSize, labelSize := 24, 58, 64, 16
	if crown {
		panelHeight = height * 42 / 100
		radius, iconSize, numberSize, labelSize = 34, 78, 82, 20
	}
	panel := image.Rect((width-panelWidth)/2, (height-panelHeight)/2, (width+panelWidth)/2, (height+panelHeight)/2)
	roundedRect(canvas, panel, radius, color.RGBA{8, 14, 21, 232})
	roundedRectOutline(canvas, panel, radius, 2, color.RGBA{255, 255, 255, 32})
	contentY := panel.Min.Y + panelHeight*42/100
	iconX := panel.Min.X + panelWidth*24/100
	r.drawVolumeSpeaker(canvas, iconX, contentY, iconSize, percent, accent)
	copyLeft := panel.Min.X + panelWidth*39/100
	r.text(canvas, copyLeft, contentY-numberSize/3, "VOLUME", labelSize, true, color.RGBA{225, 235, 245, 190})
	r.text(canvas, copyLeft, contentY+numberSize/2, fmt.Sprintf("%d", percent), numberSize, false, color.White)
	trackInset := panelWidth * 9 / 100
	trackHeight := 12
	if crown {
		trackHeight = 16
	}
	trackTop := panel.Max.Y - panelHeight*25/100
	r.drawVolumeTrack(canvas, image.Rect(panel.Min.X+trackInset, trackTop, panel.Max.X-trackInset, trackTop+trackHeight), percent, accent)
}

func (r *renderer) drawVolumeSpeaker(canvas *image.RGBA, centerX, centerY, size, percent int, accent color.RGBA) {
	icon := image.NewRGBA(image.Rect(0, 0, size, size))
	path := vector.NewRasterizer(size, size)
	s := float32(size)
	path.MoveTo(s*.08, s*.37)
	path.LineTo(s*.29, s*.37)
	path.LineTo(s*.53, s*.17)
	path.LineTo(s*.53, s*.83)
	path.LineTo(s*.29, s*.63)
	path.LineTo(s*.08, s*.63)
	path.ClosePath()
	path.Draw(icon, icon.Rect, image.NewUniform(color.White), image.Point{})
	destination := image.Rect(centerX-size/2, centerY-size/2, centerX+size/2, centerY+size/2)
	draw.DrawMask(canvas, destination, image.NewUniform(accent), image.Point{}, icon, image.Point{}, draw.Over)
	if percent == 0 {
		line(canvas, centerX+size*60/100, centerY-size*17/100, centerX+size*88/100, centerY+size*17/100, max(3, size/11), accent)
		line(canvas, centerX+size*88/100, centerY-size*17/100, centerX+size*60/100, centerY+size*17/100, max(3, size/11), accent)
		return
	}
	arc(canvas, centerX+size*46/100, centerY, size*25/100, -55, 110, max(3, size/12), accent)
	if percent >= 50 {
		arc(canvas, centerX+size*46/100, centerY, size*42/100, -55, 110, max(3, size/12), accent)
	}
}

func (r *renderer) drawVolumeTrack(canvas *image.RGBA, bounds image.Rectangle, percent int, accent color.RGBA) {
	percent = max(0, min(100, percent))
	radius := max(2, bounds.Dy()/2)
	roundedRect(canvas, bounds, radius, color.RGBA{255, 255, 255, 40})
	filled := bounds
	filled.Max.X = filled.Min.X + bounds.Dx()*percent/100
	if !filled.Empty() {
		roundedRect(canvas, filled, radius, accent)
	}
}

func restoreRegions(canvas, base *image.RGBA, regions []image.Rectangle) {
	for _, region := range regions {
		region = region.Intersect(canvas.Rect).Intersect(base.Rect)
		if !region.Empty() {
			draw.Draw(canvas, region, base, region.Min, draw.Src)
		}
	}
}

func (r *renderer) renderCheckers(canvas *image.RGBA, now time.Time, width, height int, seconds float64, accent color.RGBA, motion bool) {
	if r.state.Phase != "speaking" {
		r.checkersReplyGlow = 0
	}
	if r.state.Phase == "setup" {
		r.drawSetup(canvas, seconds, accent)
		return
	}
	if !r.state.Connected && (r.state.Phase == "offline" || r.state.Phase == "connecting") {
		r.drawConnecting(canvas, seconds)
		return
	}
	switch {
	case r.notificationActive(now):
		r.drawFullscreenNotification(canvas, now, seconds, accent, motion)
	case r.state.Timer != nil && r.state.Timer.Active:
		r.drawFullscreenTimer(canvas, now, seconds, accent)
	case r.climateOpen:
		r.drawClimate(canvas, now, accent)
	case r.cameraOpen:
		r.drawCameraScreen(canvas, now, accent)
	case r.menuOpen:
		r.drawActionMenu(canvas, now, accent)
	case r.state.Media != nil && r.state.Media.Active:
		r.drawFullscreenMusic(canvas, now, seconds, accent, motion)
	default:
		r.drawPhotoHome(canvas, now, accent)
	}
	if r.satChatVisible() {
		r.drawSatChat(canvas, now, width, height, accent)
		return
	}
	if voiceEdgeActive(r.state.Phase) && r.state.DisplayEdgeGlow {
		if r.state.Phase == "speaking" {
			if motion {
				r.drawCheckersReplyRim(canvas, accent)
			}
		} else {
			r.drawCheckersStateRim(canvas, accent)
		}
	}
	if motion || r.state.Phase != "speaking" {
		r.drawMutedBadge(canvas, width)
	}
}

func (r *renderer) activeWallpaper(now time.Time) image.Image {
	if len(r.state.WallpaperURLs) > 0 && r.wallpaperImage != nil {
		return r.wallpaperImage
	}
	index := wallpaperRotationIndex(
		r.localTime(now),
		defaultWallpaperCount(),
		r.state.WallpaperIntervalMinutes,
	)
	identity := fmt.Sprintf("%s:%d", r.target, index)
	if r.defaultWallpaperID != identity || r.defaultWallpaperImage == nil {
		loaded, _, err := readDefaultWallpaper(r.target, index)
		if err != nil {
			log.Printf("wallpaper %s: %v", identity, err)
			return nil
		}
		if r.spotScreen {
			loaded = prepareCoverImage(loaded, image.Pt(r.screenWidth, r.screenHeight))
		}
		r.defaultWallpaperID, r.defaultWallpaperImage = identity, loaded
	}
	return r.defaultWallpaperImage
}

func (r *renderer) drawPhotoHome(canvas *image.RGBA, now time.Time, accent color.RGBA) {
	if wallpaper := r.activeWallpaper(now); wallpaper != nil {
		drawPreparedCover(canvas, canvas.Bounds(), wallpaper)
	} else {
		fillGradient(canvas, color.RGBA{6, 11, 19, 255}, mix(color.RGBA{6, 11, 19, 255}, accent, .13))
	}
	width, height := canvas.Rect.Dx(), canvas.Rect.Dy()
	margin := 20
	groupWidth := min(480, width-2*margin)
	sensorWidth := min(400, groupWidth)
	clockSize, weatherSize, detailSize, roomSize := 36, 30, 15, 14
	mainBaseline, detailGap, roomGap := height-91, 28, 17
	iconSize, sensorIconSize, sensorLabelSize, sensorValueSize := 46, 8, 8, 12
	if r.isCrown() {
		margin = 30
		groupWidth = min(620, width-2*margin)
		sensorWidth = min(540, groupWidth)
		clockSize, weatherSize, detailSize, roomSize = 48, 36, 20, 18
		mainBaseline, detailGap, roomGap = height-142, 36, 22
		iconSize, sensorIconSize, sensorLabelSize, sensorValueSize = 60, 10, 9, 15
	}
	drawPhotoHomeShade(canvas, image.Rect(width-groupWidth-margin-80, height*55/100, width, height))

	local := r.localTime(now)
	left, right := width-margin-groupWidth, width-margin
	timeText := local.Format("3:04")
	timeWidth := font.MeasureString(r.faces.regular[clockSize], timeText).Round()
	weather := r.state.Weather
	temperature, temperatureWidth := "", 0
	if weather != nil && strings.TrimSpace(weather.TemperatureText) != "" {
		temperature = strings.TrimSpace(weather.TemperatureText)
		if weather.TemperatureUnit != "" && !strings.Contains(temperature, weather.TemperatureUnit) && !strings.Contains(temperature, "°") {
			temperature += weather.TemperatureUnit
		}
		temperatureWidth = font.MeasureString(r.faces.regular[weatherSize], temperature).Round()
	}
	mainWidth := timeWidth
	if temperature != "" {
		mainWidth += 20 + 1 + 20 + iconSize + 8 + temperatureWidth
	}
	mainLeft := max(left, right-mainWidth)
	r.text(canvas, mainLeft, mainBaseline, timeText, clockSize, false, color.White)
	if temperature != "" {
		separatorX := mainLeft + timeWidth + 20
		separatorTop := mainBaseline - clockSize + 5
		draw.Draw(canvas, image.Rect(separatorX, separatorTop, separatorX+1, mainBaseline+5), image.NewUniform(color.NRGBA{R: 255, G: 255, B: 255, A: 205}), image.Point{}, draw.Over)
		iconLeft := separatorX + 21
		kind := weatherDisplayKind(weather, local)
		if art := weatherArt(kind); art != nil {
			drawContain(canvas, image.Rect(iconLeft, mainBaseline-iconSize+10, iconLeft+iconSize, mainBaseline+10), art)
		} else {
			r.drawWeatherIcon(canvas, iconLeft+iconSize/2, mainBaseline-iconSize/2+5, iconSize/3, kind, 0, color.RGBA{255, 255, 255, 255})
		}
		r.text(canvas, iconLeft+iconSize+8, mainBaseline-2, temperature, weatherSize, false, color.White)
	}

	detail := local.Format("Monday, January 2")
	if weather != nil && strings.TrimSpace(weather.Condition) != "" {
		detail += "  •  " + strings.TrimSpace(weather.Condition)
	}
	detail = fitTextToWidth(detail, r.faces.regular[detailSize], groupWidth)
	detailWidth := font.MeasureString(r.faces.regular[detailSize], detail).Round()
	detailBaseline := mainBaseline + detailGap
	r.text(canvas, right-detailWidth, detailBaseline, detail, detailSize, false, color.RGBA{245, 248, 252, 238})

	room := strings.TrimSpace(r.state.Room)
	if room == "" {
		room = r.showTarget()
	}
	roomBaseline := detailBaseline + roomGap
	r.drawHomeRoomStatus(canvas, room, right, roomBaseline, groupWidth, roomSize, false)

	type metric struct {
		kind, label, value string
		ink                color.RGBA
	}
	metrics := make([]metric, 0, 6)
	if weather != nil {
		if weather.HumidityText != "" {
			metrics = append(metrics, metric{"humidity", "HUMIDITY", weather.HumidityText, color.RGBA{83, 178, 255, 255}})
		}
		if weather.WindText != "" {
			metrics = append(metrics, metric{"wind", "WIND", weather.WindText, color.RGBA{143, 211, 222, 255}})
		}
		if weather.RainText != "" {
			metrics = append(metrics, metric{"rain", "RAIN", weather.RainText, color.RGBA{108, 162, 255, 255}})
		}
		if weather.LightningText != "" {
			metrics = append(metrics, metric{"lightning", "LIGHTNING", weather.LightningText, color.RGBA{255, 208, 92, 255}})
		}
		if weather.IndoorTemperatureText != "" {
			metrics = append(metrics, metric{"temperature", "ROOM", weather.IndoorTemperatureText, color.RGBA{255, 147, 66, 255}})
		}
		if weather.IndoorHumidityText != "" {
			metrics = append(metrics, metric{"humidity", "ROOM RH", weather.IndoorHumidityText, color.RGBA{83, 178, 255, 255}})
		}
	}
	if len(metrics) == 0 {
		return
	}
	metricTop := height - 33
	if r.isCrown() {
		metricTop = height - margin - 35
	}
	sensorLeft := right - sensorWidth
	for index, item := range metrics {
		cell := rightAlignedMetricCell(sensorLeft, sensorWidth, 6, len(metrics), index)
		cellLeft, cellWidth := cell.Min.X, cell.Dx()
		iconX := cellLeft + sensorIconSize
		textLeft := iconX + sensorIconSize + 3
		textWidth := max(12, cellWidth-(textLeft-cellLeft)-2)
		r.drawWeatherMetricGlyph(canvas, iconX, metricTop+10, sensorIconSize, item.kind, item.ink)
		r.text(canvas, textLeft, metricTop+5, fitTextToWidth(item.label, r.faces.bold[sensorLabelSize], textWidth), sensorLabelSize, true, color.RGBA{224, 230, 238, 220})
		r.text(canvas, textLeft, metricTop+22, fitTextToWidth(item.value, r.faces.regular[sensorValueSize], textWidth), sensorValueSize, false, color.White)
	}
}

func rightAlignedMetricCell(left, width, totalSlots, visibleCount, visibleIndex int) image.Rectangle {
	if totalSlots <= 0 || width <= 0 || visibleCount <= 0 || visibleIndex < 0 {
		return image.Rectangle{}
	}
	visibleCount = min(visibleCount, totalSlots)
	if visibleIndex >= visibleCount {
		return image.Rectangle{}
	}
	firstSlot := totalSlots - visibleCount
	slot := firstSlot + visibleIndex
	return image.Rect(left+width*slot/totalSlots, 0, left+width*(slot+1)/totalSlots, 1)
}

func centeredMetricCell(left, width, totalSlots, visibleCount, visibleIndex int) image.Rectangle {
	if totalSlots <= 0 || width <= 0 || visibleCount <= 0 || visibleIndex < 0 {
		return image.Rectangle{}
	}
	visibleCount = min(visibleCount, totalSlots)
	if visibleIndex >= visibleCount {
		return image.Rectangle{}
	}
	groupWidth := width * visibleCount / totalSlots
	groupLeft := left + (width-groupWidth)/2
	return image.Rect(groupLeft+groupWidth*visibleIndex/visibleCount, 0,
		groupLeft+groupWidth*(visibleIndex+1)/visibleCount, 1)
}

func drawPhotoHomeShade(canvas *image.RGBA, bounds image.Rectangle) {
	bounds = bounds.Intersect(canvas.Bounds())
	if bounds.Empty() {
		return
	}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		vertical := float64(y-bounds.Min.Y) / float64(max(1, bounds.Dy()-1))
		vertical = vertical * vertical * (3 - 2*vertical)
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			horizontal := float64(x-bounds.Min.X) / float64(max(1, bounds.Dx()-1))
			horizontal = horizontal * horizontal * (3 - 2*horizontal)
			alpha := uint8(150 * vertical * (.16 + .84*horizontal))
			blendPixel(canvas, x, y, color.RGBA{0, 0, 0, alpha})
		}
	}
}

func (r *renderer) drawFullscreenBackdrop(canvas *image.RGBA, now time.Time, accent color.RGBA, opacity uint8) {
	if wallpaper := r.activeWallpaper(now); wallpaper != nil {
		drawPreparedCover(canvas, canvas.Bounds(), wallpaper)
	} else {
		fillGradient(canvas, color.RGBA{6, 11, 19, 255}, mix(color.RGBA{6, 11, 19, 255}, accent, .13))
	}
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.NRGBA{R: 3, G: 7, B: 12, A: opacity}), image.Point{}, draw.Over)
}

func (r *renderer) drawFullscreenNotification(canvas *image.RGBA, now time.Time, seconds float64, accent color.RGBA, motion bool) {
	item := r.state.Notification
	if item == nil {
		return
	}
	r.drawFullscreenBackdrop(canvas, now, accent, 184)
	width, height := canvas.Rect.Dx(), canvas.Rect.Dy()
	margin, gap, headingSize, titleSize, bodySize := 24, 24, 16, 30, 22
	imageWidth := width * 59 / 100
	if r.isCrown() {
		margin, gap, headingSize, titleSize, bodySize = 38, 34, 20, 48, 28
		imageWidth = width * 62 / 100
	}
	noticeColor := accent
	if item.Priority == "critical" {
		noticeColor = color.RGBA{255, 92, 92, 255}
	}
	imageFrame := image.Rect(margin, margin+44, imageWidth, height-margin)
	roundedRect(canvas, imageFrame, 24, color.RGBA{7, 12, 19, 245})
	if r.notificationImage != nil {
		drawNotificationImage(canvas, imageFrame.Inset(8), r.notificationImage, notificationImageFit(item, "contain"))
	} else {
		pulse := .35
		if motion {
			pulse = .5 + .5*math.Sin(seconds*2.2)
		}
		cx, cy := (imageFrame.Min.X+imageFrame.Max.X)/2, (imageFrame.Min.Y+imageFrame.Max.Y)/2
		circleOutline(canvas, cx, cy, int(30+pulse*10), 4, color.RGBA{noticeColor.R, noticeColor.G, noticeColor.B, uint8(100 + pulse*80)})
		circle(canvas, cx, cy, 10, noticeColor)
	}
	category := notificationCategory(item)
	categorySize := fittingTextSize(category, r.faces.bold, headingSize, 10, imageFrame.Dx())
	r.text(canvas, margin, margin+22, fitTextToWidth(category, r.faces.bold[categorySize], imageFrame.Dx()), categorySize, true, noticeColor)

	textLeft, textRight := imageFrame.Max.X+gap, width-margin
	name := firstNonEmpty(item.CameraName, item.Title, notificationFallbackTitle(item))
	nameSize := fittingTextSize(name, r.faces.bold, titleSize, 12, textRight-textLeft)
	r.text(canvas, textLeft, margin+92, name, nameSize, true, color.White)
	meta := r.lastEventTimestamp(item.CreatedAtUnixMS, now)
	if meta == "" {
		meta = "NOW"
	}
	r.text(canvas, textLeft, margin+126+(titleSize-30), meta, headingSize, true, noticeColor)
	description := strings.TrimSpace(item.Description)
	if description == "" {
		description = name
	}
	lines := wrapTextToWidth(description, r.faces.regular[bodySize], textRight-textLeft)
	lineHeight := bodySize + 9
	startY := margin + 178 + (titleSize - 30)
	maxLines := max(1, (height-margin-62-startY)/lineHeight)
	for index, value := range lines {
		if index >= maxLines {
			break
		}
		if index == maxLines-1 && len(lines) > maxLines {
			value = fitTextToWidth(value+"…", r.faces.regular[bodySize], textRight-textLeft)
		}
		r.text(canvas, textLeft, startY+index*lineHeight, value, bodySize, false, color.RGBA{229, 236, 245, 255})
	}
	r.text(canvas, textLeft, height-margin-18, "TAP TO RETURN HOME", 14, true, color.RGBA{171, 184, 202, 255})
	if motion {
		r.drawFullscreenNotificationProgress(canvas, now, noticeColor)
	}
}

func (r *renderer) drawFullscreenNotificationProgress(canvas *image.RGBA, now time.Time, noticeColor color.RGBA) {
	item := r.state.Notification
	if item == nil || item.ExpiresAtUnixMS <= 0 {
		return
	}
	margin := 24
	if r.isCrown() {
		margin = 38
	}
	drawNotificationProgressBar(canvas, margin, canvas.Rect.Dx()-margin, canvas.Rect.Dy()-10, 4,
		notificationProgress(item, r.currentTaterUnixMS(now)), noticeColor)
}

func (r *renderer) fullscreenMusicLayout(width, height int) (image.Rectangle, image.Rectangle, image.Rectangle) {
	margin, artSize := 36, min(300, height-128)
	if r.isCrown() {
		margin, artSize = 54, min(520, height-190)
	}
	artTop := (height - artSize) / 2
	art := image.Rect(margin, artTop, margin+artSize, artTop+artSize)
	content := image.Rect(art.Max.X+max(32, width/35), margin+34, width-margin, height-margin)
	bars := image.Rect(content.Min.X, height-margin-max(92, height/7), content.Max.X, height-margin)
	return art, content, bars
}

func (r *renderer) drawFullscreenMusic(canvas *image.RGBA, now time.Time, seconds float64, accent color.RGBA, motion bool) {
	media := r.state.Media
	if media == nil {
		return
	}
	musicColor := mediaDisplayColor(media, accent)
	fillGradient(canvas, mix(color.RGBA{4, 8, 14, 255}, musicColor, .12), color.RGBA{3, 7, 12, 255})
	art, content, bars := r.fullscreenMusicLayout(canvas.Rect.Dx(), canvas.Rect.Dy())
	roundedRect(canvas, art, 28, mix(color.RGBA{12, 18, 28, 255}, musicColor, .14))
	if r.mediaImage != nil {
		drawCover(canvas, art, r.mediaImage)
	} else {
		r.drawMusicVisualizerBackdrop(canvas, art.Inset(max(22, art.Dx()/9)), musicColor)
	}
	headingSize, titleSize, artistSize := 16, 34, 20
	if r.isCrown() {
		headingSize, titleSize, artistSize = 20, 48, 26
	}
	r.text(canvas, content.Min.X, content.Min.Y, r.nowPlayingHeading(media), headingSize, true, musicColor)
	titleY := content.Min.Y + titleSize + 35
	titleLines := wrapTextToWidth(firstNonEmpty(media.Title, "Music"), r.faces.bold[titleSize], content.Dx())
	for index, value := range titleLines {
		if index >= 3 {
			break
		}
		r.text(canvas, content.Min.X, titleY+index*(titleSize+6), fitTextToWidth(value, r.faces.bold[titleSize], content.Dx()), titleSize, true, color.White)
	}
	artistY := titleY + min(3, len(titleLines))*(titleSize+6) + 12
	r.text(canvas, content.Min.X, artistY, fitTextToWidth(firstNonEmpty(media.Artist, media.AlbumArtist, "Tater Music"), r.faces.bold[artistSize], content.Dx()), artistSize, true, color.RGBA{218, 228, 241, 255})
	if album := strings.TrimSpace(media.Album); album != "" {
		r.text(canvas, content.Min.X, artistY+artistSize+12, fitTextToWidth(album, r.faces.regular[artistSize-4], content.Dx()), artistSize-4, false, color.RGBA{153, 169, 190, 255})
	}
	if motion {
		r.drawFullscreenMusicMotion(canvas, now, seconds, musicColor, art, content, bars)
	}
}

func (r *renderer) drawFullscreenMusicMotion(canvas *image.RGBA, now time.Time, seconds float64, musicColor color.RGBA, art, content, bars image.Rectangle) {
	if r.mediaImage == nil {
		r.drawMusicVisualizerBars(canvas, art.Inset(max(22, art.Dx()/9)), seconds, musicColor)
	}
	if r.musicProgressNeedsRedraw(now) {
		progress, duration := mediaProgress(r.state.Media, now)
		barY := bars.Min.Y - 34
		line(canvas, content.Min.X, barY, content.Max.X, barY, 4, color.RGBA{91, 103, 122, 120})
		filled := int(float64(content.Dx()) * math.Min(1, float64(progress)/float64(duration)))
		line(canvas, content.Min.X, barY, content.Min.X+filled, barY, 4, musicColor)
		r.text(canvas, content.Min.X, barY+25, formatMediaTime(progress), 14, true, color.RGBA{210, 220, 234, 255})
		end := formatMediaTime(duration)
		endWidth := font.MeasureString(r.faces.bold[14], end).Round()
		r.text(canvas, content.Max.X-endWidth, barY+25, end, 14, true, color.RGBA{153, 169, 190, 255})
		r.musicProgressDrawn = musicProgressVisualKey(r.state.Media, now)
	}
	r.drawMusicBars(canvas, bars, seconds, musicColor)
}

func (r *renderer) drawFullscreenTimer(canvas *image.RGBA, now time.Time, seconds float64, accent color.RGBA) {
	r.drawFullscreenBackdrop(canvas, now, accent, 194)
	timer := r.state.Timer
	if timer == nil {
		return
	}
	timerNow := time.UnixMilli(r.currentTaterUnixMS(now))
	remaining := timerRemaining(timer, timerNow)
	width, height := canvas.Rect.Dx(), canvas.Rect.Dy()
	cx, cy := width/2, height/2-20
	radius := min(width, height) / 4
	timerAccent := accent
	if timer.Ringing {
		timerAccent = color.RGBA{255, 105, 76, 255}
	}
	circleOutline(canvas, cx, cy, radius, 7, color.RGBA{timerAccent.R, timerAccent.G, timerAccent.B, 75})
	if timer.Ringing {
		pulse := .5 + .5*math.Sin(seconds*4.8)
		arc(canvas, cx, cy, radius, -90+seconds*70, 82+pulse*42, 7, timerAccent)
		r.centeredText(canvas, cx, cy+12, "TIME’S UP", 48, true, color.White)
	} else {
		arc(canvas, cx, cy, radius, -90, 360*timerProgress(timer, timerNow), 7, timerAccent)
		countdown := formatDuration(remaining)
		size := 64
		if r.isCrown() {
			size = 82
		}
		r.centeredText(canvas, cx, cy+size/3, countdown, size, true, color.White)
	}
	button := image.Rect(cx-150, height-88, cx+150, height-28)
	roundedRect(canvas, button, 26, color.RGBA{timerAccent.R, timerAccent.G, timerAccent.B, 145})
	label := "CANCEL TIMER"
	if timer.Ringing {
		label = "STOP TIMER"
	}
	r.centeredText(canvas, cx, button.Min.Y+39, label, 18, true, color.White)
}

func (r *renderer) drawMutedBadge(canvas *image.RGBA, width int) {
	if !r.state.Muted {
		return
	}
	roundedRect(canvas, image.Rect(width-174, 22, width-26, 58), 18, color.RGBA{101, 27, 38, 225})
	r.text(canvas, width-153, 48, "MIC MUTED", 16, true, color.RGBA{255, 191, 197, 255})
}

func (r *renderer) canCacheCheckers(now time.Time) bool {
	if r.isCrown() || r.spotScreen || !r.state.Connected || r.intercomPressed || r.timerPressed || r.satChatVisible() {
		return false
	}
	if r.state.Timer != nil && r.state.Timer.Active {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(r.state.Phase)) {
	case "", "idle", "playing", "music", "listening", "thinking", "speaking", "intercom", "tool_call":
		return true
	default:
		return false
	}
}

func (r *renderer) canCacheCrown() bool {
	if !r.isCrown() || !r.state.Connected || r.intercomPressed || r.timerPressed || r.satChatVisible() {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(r.state.Phase)) {
	case "", "idle", "playing", "music", "listening", "thinking", "speaking", "intercom", "tool_call":
		return true
	default:
		return false
	}
}

func (r *renderer) crownCacheKey(now time.Time, accent color.RGBA) string {
	timerSecond := int64(0)
	if r.state.Timer != nil && r.state.Timer.Active {
		timerSecond = timerRemaining(r.state.Timer, time.UnixMilli(r.currentTaterUnixMS(now))).Milliseconds() / 1000
	}
	return strings.Join([]string{
		r.crownContextKind(now), r.state.Phase, r.state.Message, r.state.DeviceName, r.state.Room,
		r.state.AssistantName,
		fmt.Sprint(r.state.Connected), fmt.Sprint(r.state.Muted), fmt.Sprint(r.state.DisplayEdgeGlow), fmt.Sprint(accent),
		stableCacheValue(r.state.Weather), stableCacheValue(r.state.Climate), stableCacheValue(r.state.Notification),
		stableCacheValue(r.state.LastEvent), stableCacheValue(r.state.Timer),
		mediaIdentity(r.state.Media), mediaPlaybackState(r.state.Media), mediaGroupName(r.state.Media),
		fmt.Sprint(mediaPrimaryColor(r.state.Media)), fmt.Sprint(mediaAccentColor(r.state.Media)),
		fmt.Sprint(timerSecond),
		r.notificationID, r.lastEventID, fmt.Sprint(r.notificationImage != nil), fmt.Sprint(r.lastEventImage != nil),
		r.localTime(now).Format("200601021504"), fmt.Sprint(r.mediaImage != nil), r.wallpaperID,
		stableCacheValue(r.state.WallpaperURLs), fmt.Sprint(r.climateOpen), fmt.Sprint(r.cameraOpen), fmt.Sprint(r.menuOpen),
	}, "|")
}

// stableCacheValue compares state by its contents. Formatting structs with
// %#v includes addresses for pointer-valued fields (for example Climate's
// temperatures), which made an unchanged decoded snapshot invalidate the
// framebuffer cache on every update.
func stableCacheValue(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

func mediaPlaybackState(media *show.Media) string {
	if media == nil {
		return ""
	}
	return media.PlaybackState
}

func mediaGroupName(media *show.Media) string {
	if media == nil {
		return ""
	}
	return media.GroupName
}

func mediaPrimaryColor(media *show.Media) [3]uint8 {
	if media == nil {
		return [3]uint8{}
	}
	return media.PrimaryColor
}

func mediaAccentColor(media *show.Media) [3]uint8 {
	if media == nil {
		return [3]uint8{}
	}
	return media.AccentColor
}

func mediaCacheIdentity(media *show.Media) string {
	if media == nil {
		return ""
	}
	return fmt.Sprintf("%s|%s|%s|%s|%v|%v", mediaIdentity(media), media.PlaybackState,
		media.GroupName, media.AlbumArtist, media.PrimaryColor, media.AccentColor)
}

func (r *renderer) checkersCacheKey(now time.Time, accent color.RGBA) string {
	media := r.state.Media
	local := r.localTime(now)
	return strings.Join([]string{
		r.state.Phase,
		r.state.ToolName,
		r.state.ToolMessage,
		mediaCacheIdentity(media),
		r.state.Room,
		r.state.DisplayTheme,
		fmt.Sprint(r.state.DisplayEdgeGlow),
		r.state.Message,
		r.state.DeviceName,
		r.state.AssistantName,
		fmt.Sprint(r.state.Connected, "|", r.state.Muted, "|", r.state.TimerActive),
		fmt.Sprint(accent),
		stableCacheValue(r.state.Weather),
		stableCacheValue(r.state.Climate) + "|" + fmt.Sprint(r.climateOpen, "|", r.climatePage) + "|" + fmt.Sprint(r.cameraOpen, "|", r.cameraPage) + "|" + fmt.Sprint(r.menuOpen),
		stableCacheValue(r.state.Timer),
		stableCacheValue(r.state.Notification),
		local.Format("200601021504"),
		fmt.Sprint(r.mediaImage != nil),
		fmt.Sprint(r.notificationImage != nil),
		r.notificationID,
		r.wallpaperID,
		stableCacheValue(r.state.WallpaperURLs),
	}, "|")
}

func (r *renderer) drawCheckersMotion(canvas *image.RGBA, now time.Time, width, height int, seconds float64, accent color.RGBA) {
	speaking := r.state.Phase == "speaking"
	if speaking && r.state.DisplayEdgeGlow {
		r.drawCheckersReplyRim(canvas, accent)
	}
	if r.notificationActive(now) {
		noticeColor := accent
		if r.state.Notification.Priority == "critical" {
			noticeColor = color.RGBA{255, 92, 92, 255}
		}
		r.drawFullscreenNotificationProgress(canvas, now, noticeColor)
		return
	}
	if r.climateOpen || r.cameraOpen || r.menuOpen {
		if speaking {
			r.drawMutedBadge(canvas, width)
		}
		return
	}
	if media := r.state.Media; media != nil && media.Active {
		musicColor := mediaDisplayColor(media, accent)
		art, content, bars := r.fullscreenMusicLayout(width, height)
		r.drawFullscreenMusicMotion(canvas, now, seconds, musicColor, art, content, bars)
		return
	}
	if speaking {
		r.drawMutedBadge(canvas, width)
	}
}

func (r *renderer) checkersMotionRegions(now time.Time, width, height int) []image.Rectangle {
	regions := make([]image.Rectangle, 0, 5)
	if r.state.Phase == "speaking" && r.state.DisplayEdgeGlow {
		regions = append(regions, image.Rect(0, 0, width, 24), image.Rect(0, height-24, width, height),
			image.Rect(0, 24, 24, height-24), image.Rect(width-24, 24, width, height-24))
	}
	if r.notificationActive(now) {
		regions = append(regions, image.Rect(0, height-16, width, height))
		return regions
	}
	if r.climateOpen || r.cameraOpen || r.menuOpen {
		return regions
	}
	if media := r.state.Media; media != nil && media.Active {
		art, content, bars := r.fullscreenMusicLayout(width, height)
		if r.mediaImage == nil {
			regions = append(regions, musicVisualizerMotionBounds(art.Inset(max(22, art.Dx()/9))))
		}
		if r.musicProgressNeedsRedraw(now) {
			regions = append(regions, image.Rect(content.Min.X, bars.Min.Y-42, content.Max.X, bars.Min.Y+30))
		}
		regions = append(regions, bars)
		return regions
	}
	return regions
}

func (r *renderer) drawSetup(canvas *image.RGBA, seconds float64, accent color.RGBA) {
	fillGradient(canvas, color.RGBA{17, 9, 5, 255}, color.RGBA{8, 13, 18, 255})
	r.text(canvas, 38, 55, "TATER "+strings.ToUpper(r.showTarget())+" SETUP", 18, true, color.RGBA{255, 194, 132, 255})
	r.text(canvas, 38, 105, "Connect this Show to Tater", 36, true, color.White)
	drawSetupCard := func(rect image.Rectangle, number, label, value string) {
		roundedRect(canvas, rect, 14, color.RGBA{25, 28, 34, 230})
		circleOutline(canvas, rect.Min.X+38, (rect.Min.Y+rect.Max.Y)/2, 23, 2, color.RGBA{accent.R, accent.G, accent.B, 180})
		r.centeredText(canvas, rect.Min.X+38, (rect.Min.Y+rect.Max.Y)/2+7, number, 20, true, accent)
		r.text(canvas, rect.Min.X+76, rect.Min.Y+31, label, 14, true, color.RGBA{160, 172, 190, 255})
		r.text(canvas, rect.Min.X+76, rect.Min.Y+62, ellipsize(value, 28), 20, true, color.White)
	}
	if strings.HasPrefix(r.state.Room, "Tater-Setup-") {
		drawSetupCard(image.Rect(38, 136, 465, 222), "1", "JOIN WI-FI", r.state.Room)
		drawSetupCard(image.Rect(495, 136, 922, 222), "2", "OPEN IN BROWSER", "192.168.4.1")
		r.text(canvas, 38, 282, "Join the open Tater setup network on your phone.", 24, true, color.White)
		r.text(canvas, 38, 321, "In Tater, use Satellites  ›  Add Satellite for a pairing code.", 18, false, color.RGBA{180, 188, 199, 255})
	} else {
		drawSetupCard(image.Rect(38, 136, 465, 222), "1", "CONNECT USB", "Open the Tater desktop app")
		drawSetupCard(image.Rect(495, 136, 922, 222), "2", "PROVISION", "Satellites  ›  Add Satellite")
		r.text(canvas, 38, 282, "Keep USB connected while Wi-Fi and pairing are saved.", 24, true, color.White)
		r.text(canvas, 38, 321, "The screen will continue automatically after provisioning.", 20, false, color.RGBA{180, 188, 199, 255})
	}
	for index := 0; index < 3; index++ {
		alpha := uint8(70)
		if int(seconds*2)%3 == index {
			alpha = 255
		}
		circle(canvas, 48+index*24, 374, 6, color.RGBA{accent.R, accent.G, accent.B, alpha})
	}
	if strings.HasPrefix(r.state.Room, "Tater-Setup-") {
		r.text(canvas, 126, 381, "Waiting for Wi-Fi and Tater pairing", 18, true, color.RGBA{200, 209, 222, 255})
		r.text(canvas, 38, 449, "USB provisioning is also available if needed.", 16, false, color.RGBA{126, 136, 150, 255})
	} else {
		r.text(canvas, 126, 381, "Waiting for secure USB provisioning", 18, true, color.RGBA{200, 209, 222, 255})
		r.text(canvas, 38, 449, "The setup hotspot could not start. USB remains available.", 16, false, color.RGBA{126, 136, 150, 255})
	}
}

func (r *renderer) localTime(now time.Time) time.Time {
	if r.state.TaterTimeUnixMS <= 0 && r.clockUnixMS <= 0 {
		return now
	}
	base := time.UnixMilli(r.currentTaterUnixMS(now))
	return base.UTC().Add(time.Duration(r.state.TaterUTCOffset) * time.Second)
}

func (r *renderer) showTarget() string {
	if strings.EqualFold(strings.TrimSpace(r.target), "athena") {
		return "Athena"
	}
	if r.isCrown() {
		return "Crown"
	}
	return "Checkers"
}

func (r *renderer) isCrown() bool {
	target := strings.ToLower(strings.TrimSpace(r.target))
	return target == "crown" || target == "athena"
}

func (r *renderer) contentPane(width, height int) image.Rectangle {
	if r.isCrown() {
		return crownPane(width, height)
	}
	return rightPane(width, height)
}

func (r *renderer) showSize() (int, int) {
	width, height := r.screenWidth, r.screenHeight
	if width <= 0 {
		width = 960
	}
	if height <= 0 {
		height = 480
	}
	return width, height
}

func (r *renderer) status(local time.Time) (string, string) {
	if r.state.Phase == "tool_call" {
		tool := titleWords(r.state.ToolName)
		if tool == "" {
			tool = "Tater"
		}
		return "Using " + tool, firstNonEmpty(r.state.ToolMessage, r.state.Message, "Working on it")
	}
	message := firstNonEmpty(r.state.Message, "Ready when you are")
	if r.state.Connected && r.state.Phase == "idle" && message == "Ready when you are" {
		hour := local.Hour()
		switch {
		case hour >= 5 && hour < 12:
			message = "Good morning"
		case hour >= 12 && hour < 17:
			message = "Good afternoon"
		case hour >= 17 && hour < 21:
			message = "Good evening"
		default:
			message = "Good night"
		}
	}
	return message, firstNonEmpty(r.state.DeviceName, "Tater Checkers")
}

func (r *renderer) drawRight(canvas *image.RGBA, now time.Time, width, height int, seconds float64, accent color.RGBA) {
	r.drawRightWithMotion(canvas, now, width, height, seconds, accent, true)
}

func (r *renderer) drawRightWithMotion(canvas *image.RGBA, now time.Time, width, height int, seconds float64, accent color.RGBA, motion bool) {
	if r.notificationActive(now) {
		r.drawNotification(canvas, now, width, seconds, accent)
		return
	}
	if r.state.Timer != nil && r.state.Timer.Active {
		r.drawTimer(canvas, now, width, seconds, accent)
		return
	}
	if r.state.Media != nil && r.state.Media.Active {
		r.drawNowPlaying(canvas, now, width, seconds, accent, motion)
		return
	}
	if r.state.Weather != nil {
		r.drawWeather(canvas, now, width, seconds, accent, motion)
		return
	}
	if r.state.Phase == "tool_call" {
		pane := rightPane(width, height)
		cx := (pane.Min.X + pane.Max.X) / 2
		r.centeredText(canvas, cx, 220, "WORKING", 18, true, color.White)
		r.centeredText(canvas, cx, 257, ellipsize(titleWords(r.state.ToolName), 23), 20, false, color.RGBA{180, 190, 207, 255})
		return
	}
	pane := rightPane(width, height)
	if label := checkersVoiceStageLabel(r.state.Phase); label != "" {
		r.centeredText(canvas, (pane.Min.X+pane.Max.X)/2, 228, label, 20, true, accent)
		return
	}
	if motion {
		r.drawOrb(canvas, (pane.Min.X+pane.Max.X)/2, 220, 120, seconds, accent)
	}
}

const crownContextHeight = 480

func (r *renderer) drawCrownRight(canvas *image.RGBA, now time.Time, width, height int, seconds float64, accent color.RGBA, motion bool) {
	r.drawCrownConditions(canvas, now, width, seconds, accent, motion)

	// Reuse the original 480-pixel-tall Checkers content geometry below the
	// permanent conditions region. This keeps the mature music, timer, and
	// Awareness layouts intact while using Crown's extra vertical space.
	contextBounds := image.Rect(0, 0, width, crownContextHeight)
	if r.crownContext == nil || r.crownContext.Rect != contextBounds {
		r.crownContext = image.NewRGBA(contextBounds)
	} else {
		draw.Draw(r.crownContext, contextBounds, image.Transparent, image.Point{}, draw.Src)
	}
	r.drawCrownContext(r.crownContext, now, width, seconds, accent, motion)
	offsetY := max(0, height-crownContextHeight)
	source := crownPane(width, crownContextHeight)
	destination := source.Add(image.Pt(0, offsetY))
	draw.Draw(canvas, destination, r.crownContext, source.Min, draw.Over)
}

func (r *renderer) drawCrownConditions(canvas *image.RGBA, now time.Time, width int, seconds float64, accent color.RGBA, motion bool) {
	pane := crownPane(width, canvas.Rect.Dy())
	left, right := pane.Min.X, pane.Max.X
	weather := r.state.Weather
	weatherAccent := accent
	conditionKind := ""
	if weather != nil {
		conditionKind = weatherDisplayKind(weather, r.localTime(now))
		weatherAccent = weatherColor(conditionKind)
	}
	r.text(canvas, left, 52, "OUTSIDE CONDITIONS", 16, true, accent)
	if weather != nil && weather.Stale {
		staleWidth := font.MeasureString(r.faces.bold[14], "STALE").Round()
		r.text(canvas, right-staleWidth, 52, "STALE", 14, true, color.RGBA{255, 170, 92, 255})
	}

	temperature, condition, feels := "--°", "Weather unavailable", ""
	if weather != nil {
		temperature = firstNonEmpty(weather.TemperatureText, "--°")
		condition = firstNonEmpty(weather.Condition, "Conditions unavailable")
		feels = weather.FeelsLikeText
	}
	if motion {
		r.drawWeatherIcon(canvas, right-68, 112, 56, conditionKind, seconds, weatherAccent)
	}
	temperature = ellipsize(temperature, 7)
	r.text(canvas, left, 148, temperature, 74, true, color.White)
	temperatureWidth := font.MeasureString(r.faces.bold[74], temperature).Round()
	if weather != nil && weather.TemperatureUnit != "" {
		r.text(canvas, left+temperatureWidth+8, 116, weather.TemperatureUnit, 18, true, color.RGBA{174, 189, 209, 255})
	}
	if feels != "" {
		feelsInk := color.RGBA{174, 189, 209, 255}
		if weather.FeelsLikeRelation == "cooler" {
			feelsInk = color.RGBA{83, 178, 255, 255}
		} else if weather.FeelsLikeRelation == "warmer" {
			feelsInk = color.RGBA{255, 147, 66, 255}
		}
		feelsLeft := min(left+max(temperatureWidth+28, 190), right-250)
		r.text(canvas, feelsLeft, 108, "FEELS LIKE", 14, true, feelsInk)
		r.text(canvas, feelsLeft, 139, ellipsize(trimFeelsLike(feels), 10), 24, true, feelsInk)
	}
	r.text(canvas, left, 194, fitTextToWidth(condition, r.faces.bold[24], pane.Dx()-90), 24, true, color.RGBA{235, 240, 248, 255})

	type metric struct {
		kind, label, value string
		ink                color.RGBA
	}
	metrics := []metric{}
	if weather != nil {
		if weather.HumidityText != "" {
			metrics = append(metrics, metric{"humidity", "HUMIDITY", weather.HumidityText, color.RGBA{83, 178, 255, 255}})
		}
		if weather.WindText != "" {
			metrics = append(metrics, metric{"wind", "WIND", weather.WindText, color.RGBA{143, 211, 222, 255}})
		}
		if weather.RainText != "" {
			metrics = append(metrics, metric{"rain", "RAIN", weather.RainText, color.RGBA{108, 162, 255, 255}})
		}
		if weather.LightningText != "" {
			metrics = append(metrics, metric{"lightning", "LIGHTNING", weather.LightningText, color.RGBA{255, 208, 92, 255}})
		}
		if weather.IndoorTemperatureText != "" {
			metrics = append(metrics, metric{"temperature", "ROOM TEMP", weather.IndoorTemperatureText, color.RGBA{255, 147, 66, 255}})
		}
		if weather.IndoorHumidityText != "" {
			metrics = append(metrics, metric{"humidity", "ROOM HUMIDITY", weather.IndoorHumidityText, color.RGBA{83, 178, 255, 255}})
		}
	}
	shown := metrics[:min(6, len(metrics))]
	for index, item := range shown {
		x := left + pane.Dx()*(index*2+1)/(max(1, len(shown))*2)
		r.drawWeatherMetricGlyph(canvas, x, 240, 11, item.kind, item.ink)
		r.centeredText(canvas, x, 273, fitTextToWidth(item.value, r.faces.bold[18], max(80, pane.Dx()/max(1, len(shown))-8)), 18, true, color.RGBA{238, 243, 250, 255})
		r.centeredText(canvas, x, 296, item.label, 12, true, item.ink)
	}
	line(canvas, left, 310, right, 310, 1, color.RGBA{accent.R, accent.G, accent.B, 50})
}
func (r *renderer) crownContextKind(now time.Time) string {
	if r.notificationActive(now) {
		return "notification"
	}
	if r.state.Timer != nil && r.state.Timer.Active {
		return "timer"
	}
	if r.state.Media != nil && r.state.Media.Active {
		return "media"
	}
	if r.state.Phase == "tool_call" {
		return "tool"
	}
	if r.state.LastEvent != nil {
		return "last_event"
	}
	return "empty"
}

func (r *renderer) drawCrownContext(canvas *image.RGBA, now time.Time, width int, seconds float64, accent color.RGBA, motion bool) {
	pane := crownPane(width, crownContextHeight)
	switch r.crownContextKind(now) {
	case "notification":
		r.drawCrownEventCard(canvas, now, width, seconds, accent, r.state.Notification, r.notificationImage, true)
	case "timer":
		r.drawTimer(canvas, now, width, seconds, accent)
	case "media":
		r.drawNowPlaying(canvas, now, width, seconds, accent, motion)
	case "tool":
		cx := (pane.Min.X + pane.Max.X) / 2
		r.centeredText(canvas, cx, 220, "WORKING", 18, true, color.White)
		r.centeredText(canvas, cx, 257, ellipsize(titleWords(r.state.ToolName), 23), 20, false, color.RGBA{180, 190, 207, 255})
	case "last_event":
		r.drawLastEvent(canvas, now, width, seconds, accent)
	case "empty":
		r.text(canvas, pane.Min.X, 52, "LAST EVENT", 16, true, accent)
		r.centeredText(canvas, (pane.Min.X+pane.Max.X)/2, 228, "NO LAST EVENT", 20, true, color.RGBA{151, 164, 184, 255})
	default:
		r.drawOrb(canvas, (pane.Min.X+pane.Max.X)/2, 220, 120, seconds, accent)
	}
}

func (r *renderer) drawCrownMotion(canvas *image.RGBA, now time.Time, width, height int, seconds float64, accent color.RGBA) {
	r.drawCheckersMotion(canvas, now, width, height, seconds, accent)
}

func (r *renderer) crownMotionRegions(now time.Time, width, height int) []image.Rectangle {
	return r.checkersMotionRegions(now, width, height)
}

func (r *renderer) drawNowPlaying(canvas *image.RGBA, now time.Time, width int, seconds float64, accent color.RGBA, musicMotion bool) {
	media := r.state.Media
	pane := r.contentPane(width, canvas.Rect.Dy())
	left, right := pane.Min.X, pane.Max.X
	musicColor := mediaDisplayColor(media, accent)
	heading := r.nowPlayingHeading(media)
	r.text(canvas, left, 52, fitTextToWidth(heading, r.faces.bold[16], pane.Dx()), 16, true, musicColor)

	art := image.Rect(left, 75, left+202, 277)
	roundedRect(canvas, art, 20, mix(color.RGBA{16, 23, 35, 255}, musicColor, .13))
	if r.mediaImage != nil {
		drawCover(canvas, art, r.mediaImage)
	} else if musicMotion {
		r.drawMusicVisualizer(canvas, art, seconds, musicColor)
	}

	textLeft := art.Max.X + 26
	textWidth := max(1, right-textLeft)
	titleLines := wrapTextToWidth(firstNonEmpty(media.Title, "Music"), r.faces.bold[30], textWidth)
	for index, value := range titleLines {
		if index >= 3 {
			break
		}
		r.text(canvas, textLeft, 116+index*35, fitTextToWidth(value, r.faces.bold[30], textWidth), 30, true, color.White)
	}
	artistBaseline := 229
	if len(titleLines) < 3 {
		artistBaseline = 123 + len(titleLines)*35
	}
	r.text(canvas, textLeft, artistBaseline, fitTextToWidth(firstNonEmpty(media.Artist, media.AlbumArtist, "Tater Music"), r.faces.bold[18], textWidth), 18, true, color.RGBA{216, 225, 238, 255})
	if album := strings.TrimSpace(media.Album); album != "" {
		r.text(canvas, textLeft, artistBaseline+27, fitTextToWidth(album, r.faces.regular[16], textWidth), 16, false, color.RGBA{146, 162, 185, 255})
	}

	if musicMotion {
		r.drawMediaProgress(canvas, pane, media, now, musicColor)
	}
	if musicMotion {
		r.drawMusicBars(canvas, musicBarsBounds(pane), seconds, musicColor)
	}
}

func (r *renderer) drawMediaProgress(canvas *image.RGBA, pane image.Rectangle, media *show.Media, now time.Time, musicColor color.RGBA) {
	progress, duration := mediaProgress(media, now)
	if duration <= 0 {
		return
	}
	left, right := pane.Min.X, pane.Max.X
	barY := pane.Min.Y + 292
	line(canvas, left, barY, right, barY, 4, color.RGBA{91, 103, 122, 95})
	filled := int(float64(pane.Dx()) * math.Min(1, float64(progress)/float64(duration)))
	line(canvas, left, barY, left+filled, barY, 4, musicColor)
	r.text(canvas, left, barY+28, formatMediaTime(progress), 14, true, color.RGBA{192, 205, 222, 255})
	end := formatMediaTime(duration)
	endWidth := font.MeasureString(r.faces.bold[14], end).Round()
	r.text(canvas, right-endWidth, barY+28, end, 14, true, color.RGBA{141, 156, 178, 255})
}

func nowPlayingHeading(state show.Snapshot, media *show.Media) string {
	switch state.Phase {
	case "listening":
		return "LISTENING"
	case "thinking":
		return "THINKING"
	case "speaking":
		return "SPEAKING"
	case "intercom":
		return "INTERCOM"
	case "tool_call":
		if tool := titleWords(state.ToolName); tool != "" {
			return "USING " + strings.ToUpper(tool)
		}
		return "WORKING"
	}
	heading := "NOW PLAYING"
	if media != nil && strings.EqualFold(media.PlaybackState, "paused") {
		heading = "PAUSED"
	}
	if media != nil {
		if group := strings.TrimSpace(media.GroupName); group != "" {
			heading += "  ·  " + strings.ToUpper(group)
		}
	}
	return heading
}

func (r *renderer) nowPlayingHeading(media *show.Media) string {
	state := r.state
	if r.isCrown() && checkersVoiceStageLabel(state.Phase) != "" {
		// Crown already reports the voice stage in its permanent left column.
		// Keep the lower context card focused on the active media instead of
		// duplicating LISTENING/THINKING/SPEAKING/INTERCOM over it.
		state.Phase = "idle"
	}
	return nowPlayingHeading(state, media)
}

func (r *renderer) drawMusicVisualizerBackdrop(canvas *image.RGBA, bounds image.Rectangle, ink color.RGBA) {
	cx, cy := (bounds.Min.X+bounds.Max.X)/2, (bounds.Min.Y+bounds.Max.Y)/2
	circle(canvas, cx, cy, min(bounds.Dx(), bounds.Dy())/3, color.RGBA{ink.R, ink.G, ink.B, 30})
}

func (r *renderer) drawMusicVisualizerBars(canvas *image.RGBA, bounds image.Rectangle, seconds float64, ink color.RGBA) {
	cx, cy := (bounds.Min.X+bounds.Max.X)/2, (bounds.Min.Y+bounds.Max.Y)/2
	for index := 0; index < 5; index++ {
		height := int((.18 + .52*r.musicBinLevel(index, seconds)) * float64(bounds.Dy()))
		x := cx - 42 + index*21
		roundedRect(canvas, image.Rect(x-5, cy-height/2, x+6, cy+height/2), 5, color.RGBA{ink.R, ink.G, ink.B, 210})
	}
}

func (r *renderer) drawMusicVisualizer(canvas *image.RGBA, bounds image.Rectangle, seconds float64, ink color.RGBA) {
	r.drawMusicVisualizerBackdrop(canvas, bounds, ink)
	r.drawMusicVisualizerBars(canvas, bounds, seconds, ink)
}

func musicVisualizerMotionBounds(bounds image.Rectangle) image.Rectangle {
	cx, cy := (bounds.Min.X+bounds.Max.X)/2, (bounds.Min.Y+bounds.Max.Y)/2
	halfHeight := int(math.Ceil(.36 * float64(bounds.Dy())))
	return image.Rect(cx-49, cy-halfHeight, cx+49, cy+halfHeight).Intersect(bounds)
}

func (r *renderer) drawMusicBars(canvas *image.RGBA, bounds image.Rectangle, seconds float64, ink color.RGBA) {
	const count = 12
	gap := 7
	barWidth := max(3, (bounds.Dx()-gap*(count-1))/count)
	for index := 0; index < count; index++ {
		level := r.musicBinLevel(index, seconds)
		// Sendspin spectrum bins intentionally retain headroom. Expand that
		// useful range for the display so normal music travels farther without
		// clipping quiet passages into a permanently tall baseline.
		level = math.Min(1, .04+(level-.04)*1.30)
		height := max(3, int(level*float64(bounds.Dy())))
		x := bounds.Min.X + index*(barWidth+gap)
		roundedRect(canvas, image.Rect(x, bounds.Max.Y-height, x+barWidth, bounds.Max.Y), barWidth/2,
			color.RGBA{ink.R, ink.G, ink.B, uint8(85 + 170*level)})
	}
}

func (r *renderer) updateMusicLevels(now time.Time, seconds float64) {
	media := r.state.Media
	if media == nil || !media.Active || len(media.Spectrum) == 0 {
		r.musicLevelsAt = time.Time{}
		r.musicLevelsMedia = ""
		r.musicLevelsReady = false
		return
	}

	identity := mediaIdentity(media)
	if !r.musicLevelsReady || r.musicLevelsAt.IsZero() || identity != r.musicLevelsMedia || now.Before(r.musicLevelsAt) {
		for index := range r.musicLevels {
			r.musicLevels[index] = musicBinLevel(media, index, seconds, r.state.AudioLevel)
		}
		r.musicLevelsAt = now
		r.musicLevelsMedia = identity
		r.musicLevelsReady = true
		return
	}

	delta := now.Sub(r.musicLevelsAt)
	if delta <= 0 {
		return
	}
	// A fast attack keeps beats feeling immediate; the gentler release removes
	// the jagged stepping between incoming Sendspin spectrum frames.
	for index := range r.musicLevels {
		target := musicBinLevel(media, index, seconds, r.state.AudioLevel)
		tau := 70 * time.Millisecond
		if target > r.musicLevels[index] {
			tau = 22 * time.Millisecond
		}
		alpha := 1 - math.Exp(-float64(delta)/float64(tau))
		r.musicLevels[index] += (target - r.musicLevels[index]) * alpha
	}
	r.musicLevelsAt = now
}

func (r *renderer) musicBinLevel(index int, seconds float64) float64 {
	if r.musicLevelsReady && r.state.Media != nil && len(r.state.Media.Spectrum) > 0 {
		return r.musicLevels[index%len(r.musicLevels)]
	}
	return musicBinLevel(r.state.Media, index, seconds, r.state.AudioLevel)
}

func musicBinLevel(media *show.Media, index int, seconds, fallback float64) float64 {
	if media != nil && len(media.Spectrum) > 0 {
		value := media.Spectrum[index%len(media.Spectrum)]
		return math.Max(.04, math.Min(1, value))
	}
	level := math.Min(1, math.Max(.03, fallback*5))
	wave := .48 + .52*math.Sin(seconds*3.4+float64(index)*.82)
	return math.Max(.04, level*(.32+.68*wave))
}

func mediaDisplayColor(media *show.Media, fallback color.RGBA) color.RGBA {
	if media == nil {
		return fallback
	}
	selected := media.PrimaryColor
	if brighterRGB(media.AccentColor, selected) {
		selected = media.AccentColor
	}
	if selected == ([3]uint8{}) {
		return fallback
	}
	peak := max(selected[0], max(selected[1], selected[2]))
	if peak < 150 {
		scale := 150 / float64(max(peak, 1))
		for index := range selected {
			selected[index] = uint8(math.Min(255, math.Round(float64(selected[index])*scale)))
		}
	}
	return color.RGBA{selected[0], selected[1], selected[2], 255}
}

func brighterRGB(a, b [3]uint8) bool {
	brightness := func(value [3]uint8) int { return int(value[0])*299 + int(value[1])*587 + int(value[2])*114 }
	return brightness(a) > brightness(b)
}

func mediaProgress(media *show.Media, now time.Time) (int64, int64) {
	if media == nil {
		return 0, 0
	}
	progress := media.ProgressMS
	if media.PlaybackSpeed > 0 && media.ProgressUpdatedAtUnixMS > 0 {
		elapsed := max(int64(0), now.UnixMilli()-media.ProgressUpdatedAtUnixMS)
		progress += elapsed * int64(media.PlaybackSpeed) / 1000
	}
	if media.DurationMS > 0 {
		progress = min(progress, media.DurationMS)
	}
	return max(int64(0), progress), max(int64(0), media.DurationMS)
}

func musicProgressVisualKey(media *show.Media, now time.Time) string {
	progress, duration := mediaProgress(media, now)
	if duration <= 0 {
		return ""
	}
	return fmt.Sprintf("%s|%d|%d", mediaIdentity(media), progress/1000, duration)
}

func (r *renderer) musicProgressNeedsRedraw(now time.Time) bool {
	key := musicProgressVisualKey(r.state.Media, now)
	return key != "" && key != r.musicProgressDrawn
}

func formatMediaTime(milliseconds int64) string {
	seconds := max(int64(0), milliseconds/1000)
	if seconds >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
	}
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}

func checkersVoiceStageLabel(phase string) string {
	switch phase {
	case "listening":
		return "LISTENING"
	case "thinking":
		return "THINKING"
	case "speaking":
		return "SPEAKING"
	case "intercom":
		return "INTERCOM"
	default:
		return ""
	}
}

// Match the Spot's audio-following rim on Checkers' rectangular display.
// Painting only the outer 24 pixels keeps the content readable and the per-
// frame work bounded on the Show 5's small CPU.
func (r *renderer) drawCheckersReplyRim(canvas *image.RGBA, replyColor color.RGBA) {
	r.drawCheckersVoiceRim(canvas, replyColor, true)
}

func (r *renderer) drawCheckersStateRim(canvas *image.RGBA, stateColor color.RGBA) {
	r.drawCheckersVoiceRim(canvas, stateColor, false)
}

func (r *renderer) drawCheckersVoiceRim(canvas *image.RGBA, stateColor color.RGBA, reactive bool) {
	level := .34
	if reactive {
		target := math.Pow(math.Min(1, math.Max(0, r.state.AudioLevel)/.22), .65)
		response := .30
		if target > r.checkersReplyGlow {
			response = .60
		}
		r.checkersReplyGlow += (target - r.checkersReplyGlow) * response
		level = r.checkersReplyGlow
	}
	width, height := canvas.Rect.Dx(), canvas.Rect.Dy()
	ink := mix(stateColor, color.RGBA{255, 255, 255, 255}, .04+.10*level)
	for inset := 0; inset < 24 && inset*2 < width && inset*2 < height; inset++ {
		fade := 1 - float64(inset)/24
		ink.A = uint8((12 + 220*level) * fade * fade)
		if ink.A == 0 {
			continue
		}
		left, right := inset, width-inset-1
		top, bottom := inset, height-inset-1
		for x := left; x <= right; x++ {
			blendPixel(canvas, x, top, ink)
			blendPixel(canvas, x, bottom, ink)
		}
		for y := top + 1; y < bottom; y++ {
			blendPixel(canvas, left, y, ink)
			blendPixel(canvas, right, y, ink)
		}
	}
}

func connectedStatus(name string) string {
	return assistantFirstName(name) + " Online"
}

func assistantFirstName(name string) string {
	if fields := strings.Fields(strings.TrimSpace(name)); len(fields) > 0 {
		return fields[0]
	}
	return "Tater"
}

func assistantStatus(state show.Snapshot) (string, color.RGBA) {
	phase := strings.ToLower(strings.TrimSpace(state.Phase))
	if !state.Connected || phase == "offline" {
		return assistantFirstName(state.AssistantName) + " Offline", color.RGBA{255, 132, 102, 255}
	}
	if state.Muted {
		return "Mic Muted", color.RGBA{255, 112, 126, 255}
	}
	ink := accentForTheme(phase, state.DisplayTheme)
	switch phase {
	case "listening":
		return "Listening", ink
	case "thinking":
		return "Thinking", ink
	case "tool_call":
		return "Working", ink
	case "speaking":
		return "Replying", ink
	case "intercom":
		return "Broadcasting", ink
	case "connecting":
		return "Connecting", ink
	case "error":
		return "Needs Attention", ink
	default:
		return connectedStatus(state.AssistantName), paletteForTheme(state.DisplayTheme).primary
	}
}

func voiceEdgeActive(phase string) bool {
	switch strings.ToLower(strings.TrimSpace(phase)) {
	case "listening", "thinking", "tool_call", "speaking", "intercom":
		return true
	default:
		return false
	}
}

func (r *renderer) drawHomeRoomStatus(canvas *image.RGBA, room string, anchorX, baseline, maxWidth, size int, centered bool) {
	status, statusColor := assistantStatus(r.state)
	face := r.faces.regular[size]
	dotDiameter := max(6, size/2)
	gap := max(5, size/3)
	status = fitTextToWidth(status, face, max(28, maxWidth*3/5))
	statusWidth := font.MeasureString(face, status).Round()
	roomMax := max(24, maxWidth-statusWidth-dotDiameter-gap*2)
	room = fitTextToWidth(strings.TrimSpace(room), face, roomMax)
	roomWidth := font.MeasureString(face, room).Round()
	totalWidth := roomWidth + gap + dotDiameter + gap + statusWidth
	left := anchorX - totalWidth
	if centered {
		left = anchorX - totalWidth/2
	}
	roomColor := color.RGBA{245, 248, 252, 230}
	r.text(canvas, left, baseline, room, size, false, roomColor)
	dotX := left + roomWidth + gap + dotDiameter/2
	dotY := baseline - max(3, size/3)
	circle(canvas, dotX, dotY, max(3, dotDiameter/2), statusColor)
	statusX := left + roomWidth + gap + dotDiameter + gap
	r.text(canvas, statusX, baseline, status, size, false, mix(statusColor, color.RGBA{255, 255, 255, 255}, .18))
}

func (r *renderer) drawOrb(canvas *image.RGBA, cx, cy, radius int, seconds float64, accent color.RGBA) {
	audio := math.Min(1, math.Max(0, r.state.AudioLevel*1.8))
	breath := 1 + .035*math.Sin(seconds*math.Pi*1.25)
	activeRadius := int(float64(radius) * breath * (1 + .10*audio))
	circle(canvas, cx, cy, int(float64(activeRadius)*1.30), color.RGBA{accent.R, accent.G, accent.B, 24})
	circle(canvas, cx, cy, int(float64(activeRadius)*1.12), color.RGBA{accent.R, accent.G, accent.B, 50})
	circle(canvas, cx, cy, int(float64(activeRadius)*.76), mix(accent, color.RGBA{15, 20, 31, 255}, .34))
	label := phaseLabel(r.state.Phase)
	if r.state.Muted {
		label = "MUTED"
	}
	r.centeredText(canvas, cx, cy+7, label, 18, true, color.White)
	if r.state.TimerActive {
		circle(canvas, cx+radius*3/4, cy-radius*7/10, 11, color.RGBA{255, 208, 92, 255})
	}
}

func (r *renderer) drawWeather(canvas *image.RGBA, now time.Time, width int, seconds float64, accent color.RGBA, motion bool) {
	weather := r.state.Weather
	pane := r.contentPane(width, canvas.Rect.Dy())
	left, right := pane.Min.X, pane.Max.X
	displayKind := weatherDisplayKind(weather, r.localTime(now))
	weatherAccent := weatherColor(displayKind)
	heading := "OUTSIDE CONDITIONS"
	if r.state.Phase == "tool_call" {
		heading = "WORKING"
		if tool := titleWords(r.state.ToolName); tool != "" {
			heading = "USING " + strings.ToUpper(tool)
		}
	}
	r.text(canvas, left, 52, fitTextToWidth(heading, r.faces.bold[16], right-left-64), 16, true, accent)
	if motion {
		r.drawWeatherIcon(canvas, right-68, 98, 62, displayKind, seconds, weatherAccent)
	}
	temperature := ellipsize(firstNonEmpty(weather.TemperatureText, "--°"), 6)
	r.text(canvas, left, 154, temperature, 82, true, color.White)
	temperatureWidth := font.MeasureString(r.faces.bold[82], temperature).Round()
	if weather.TemperatureUnit != "" {
		r.text(canvas, left+temperatureWidth+8, 123, weather.TemperatureUnit, 18, true, color.RGBA{174, 189, 209, 255})
	}
	if weather.FeelsLikeText != "" {
		feelsColor := color.RGBA{255, 255, 255, 255}
		if weather.FeelsLikeRelation == "cooler" {
			feelsColor = color.RGBA{83, 178, 255, 255}
		} else if weather.FeelsLikeRelation == "warmer" {
			feelsColor = color.RGBA{255, 147, 66, 255}
		}
		feelsLeft := min(left+max(temperatureWidth+28, 200), right-250)
		r.text(canvas, feelsLeft, 111, "FEELS LIKE", 14, true, feelsColor)
		r.text(canvas, feelsLeft, 145, ellipsize(trimFeelsLike(weather.FeelsLikeText), 10), 24, true, feelsColor)
	}
	r.text(canvas, left, 207, fitTextToWidth(weather.Condition, r.faces.bold[24], right-left-70), 24, true, color.RGBA{235, 240, 248, 255})
	type metric struct {
		kind, label, value string
		ink                color.RGBA
	}
	metrics := []metric{}
	if weather.HumidityText != "" {
		metrics = append(metrics, metric{"humidity", "HUMIDITY", weather.HumidityText, color.RGBA{83, 178, 255, 255}})
	}
	if weather.WindText != "" {
		metrics = append(metrics, metric{"wind", "WIND", weather.WindText, color.RGBA{143, 211, 222, 255}})
	}
	if weather.RainText != "" {
		metrics = append(metrics, metric{"rain", "RAIN", weather.RainText, color.RGBA{108, 162, 255, 255}})
	}
	if weather.LightningText != "" {
		metrics = append(metrics, metric{"lightning", "LIGHTNING", weather.LightningText, color.RGBA{255, 208, 92, 255}})
	}
	for index, item := range metrics {
		x := left + (right-left)*(index*2+1)/(max(1, len(metrics))*2)
		r.drawWeatherMetricGlyph(canvas, x, 272, 13, item.kind, item.ink)
		r.centeredText(canvas, x, 312, ellipsize(item.value, 11), 20, true, color.RGBA{238, 243, 250, 255})
		r.centeredText(canvas, x, 334, item.label, 14, true, item.ink)
	}
	if weather.IndoorTemperatureText != "" || weather.IndoorHumidityText != "" {
		r.drawIndoorClimateStrip(canvas, pane, accent, weather.IndoorTemperatureText, weather.IndoorHumidityText)
	}
	if weather.Stale {
		r.text(canvas, right-48, 52, "STALE", 14, true, color.RGBA{255, 170, 92, 255})
	}
}

func (r *renderer) drawIndoorClimateStrip(canvas *image.RGBA, pane image.Rectangle, accent color.RGBA, temperature, humidity string) {
	left, right := pane.Min.X, pane.Max.X
	titleY, metricY := pane.Max.Y-88, pane.Max.Y-39
	room := strings.TrimSpace(r.state.Room)
	if room == "" || strings.EqualFold(room, "unassigned room") || strings.EqualFold(room, "echo show 5") {
		room = "INDOOR"
	}
	title := ellipsize(strings.ToUpper(room)+" CLIMATE", 24)
	circle(canvas, left+11, titleY-5, 13, color.RGBA{accent.R, accent.G, accent.B, 35})
	line(canvas, left+4, titleY-5, left+11, titleY-11, 2, accent)
	line(canvas, left+11, titleY-11, left+18, titleY-5, 2, accent)
	line(canvas, left+6, titleY-6, left+6, titleY+2, 2, accent)
	line(canvas, left+6, titleY+2, left+16, titleY+2, 2, accent)
	line(canvas, left+16, titleY+2, left+16, titleY-6, 2, accent)
	r.text(canvas, left+28, titleY, title, 16, true, accent)
	lineStart := left + 38 + font.MeasureString(r.faces.bold[16], title).Round()
	if lineStart < right {
		line(canvas, lineStart, titleY-5, right, titleY-5, 1, color.RGBA{accent.R, accent.G, accent.B, 45})
	}
	if temperature != "" && humidity != "" {
		r.drawRoomMetricInline(canvas, left+125, metricY, "temperature", "TEMPERATURE", temperature, color.RGBA{255, 147, 66, 255})
		r.drawRoomMetricInline(canvas, left+330, metricY, "humidity", "HUMIDITY", humidity, color.RGBA{83, 178, 255, 255})
	} else if temperature != "" {
		r.drawRoomMetricInline(canvas, left+pane.Dx()/2, metricY, "temperature", "TEMPERATURE", temperature, color.RGBA{255, 147, 66, 255})
	} else {
		r.drawRoomMetricInline(canvas, left+pane.Dx()/2, metricY, "humidity", "HUMIDITY", humidity, color.RGBA{83, 178, 255, 255})
	}
}

func (r *renderer) drawRoomMetricInline(canvas *image.RGBA, x, y int, kind, label, value string, ink color.RGBA) {
	r.drawWeatherMetricGlyph(canvas, x, y, 13, kind, ink)
	r.text(canvas, x+23, y-4, label, 14, true, ink)
	r.text(canvas, x+23, y+18, ellipsize(value, 10), 20, true, color.RGBA{238, 243, 250, 255})
}

func (r *renderer) drawWeatherMetricGlyph(canvas *image.RGBA, x, y, radius int, kind string, ink color.RGBA) {
	circle(canvas, x, y, radius+7, color.RGBA{ink.R, ink.G, ink.B, 28})
	switch kind {
	case "humidity":
		for row := -8; row <= 8; row++ {
			span := int(float64(radius) * .72 * (1 - math.Abs(float64(row))/15))
			if span > 0 {
				line(canvas, x-span, y+row, x+span, y+row, 1, ink)
			}
		}
	case "wind":
		line(canvas, x-10, y-4, x+7, y-4, 2, ink)
		arc(canvas, x+7, y-7, 3, 90, 240, 2, ink)
		line(canvas, x-10, y+4, x+3, y+4, 2, ink)
		arc(canvas, x+3, y+7, 3, 270, 240, 2, ink)
	case "rain":
		for offset := -1; offset <= 1; offset++ {
			line(canvas, x+offset*6+2, y-7, x+offset*6-2, y+7, 2, ink)
		}
	case "temperature":
		roundedRectOutline(canvas, image.Rect(x-3, y-11, x+4, y+7), 3, 2, ink)
		line(canvas, x, y-6, x, y+7, 2, ink)
		circle(canvas, x, y+8, 4, ink)
	default:
		line(canvas, x+2, y-10, x-5, y+1, 2, ink)
		line(canvas, x-5, y+1, x+1, y, 2, ink)
		line(canvas, x+1, y, x-2, y+10, 2, ink)
		line(canvas, x-2, y+10, x+7, y-2, 2, ink)
	}
}

func (r *renderer) drawTimer(canvas *image.RGBA, now time.Time, width int, seconds float64, accent color.RGBA) {
	timer := r.state.Timer
	timerNow := time.UnixMilli(r.currentTaterUnixMS(now))
	remaining := timerRemaining(timer, timerNow)
	pane := r.contentPane(width, canvas.Rect.Dy())
	left, right := pane.Min.X, pane.Max.X
	cx, cy, radius := (left+right)/2, 202, 112
	timerAccent := accent
	if timer.Ringing {
		timerAccent = color.RGBA{255, 105, 76, 255}
	}
	heading := "TATER TIMER"
	if timer.Count > 1 {
		heading = fmt.Sprintf("TATER TIMERS  ·  %d", timer.Count)
	}
	r.text(canvas, left, 50, heading, 16, true, timerAccent)
	circleOutline(canvas, cx, cy, radius, 7, color.RGBA{timerAccent.R, timerAccent.G, timerAccent.B, 45})
	if timer.Ringing {
		pulse := .5 + .5*math.Sin(seconds*4.8)
		arc(canvas, cx, cy, radius, -90+seconds*70, 72+pulse*38, 7, timerAccent)
		arc(canvas, cx, cy, radius, 90+seconds*70, 72+pulse*38, 7, timerAccent)
		r.centeredText(canvas, cx, cy+10, "TIME’S UP", 36, true, color.White)
		r.centeredText(canvas, cx, cy+47, "SAY STOP OR TAP BELOW", 14, true, timerAccent)
	} else {
		arc(canvas, cx, cy, radius, -90, 360*timerProgress(timer, timerNow), 7, timerAccent)
		countdown := formatDuration(remaining)
		size := 64
		if len(countdown) > 5 {
			size = 48
		}
		r.centeredText(canvas, cx, cy+21, countdown, size, true, color.White)
	}
	label := firstNonEmpty(timer.Label, timer.Name)
	if label != "" {
		r.centeredText(canvas, cx, cy+radius+35, ellipsize(label, 26), 18, false, color.RGBA{205, 214, 226, 255})
	}
	button := timerStopBoundsIn(pane)
	buttonColor := color.RGBA{timerAccent.R, timerAccent.G, timerAccent.B, 48}
	if r.timerPressed {
		buttonColor = color.RGBA{timerAccent.R, timerAccent.G, timerAccent.B, 110}
	}
	roundedRect(canvas, button, 28, buttonColor)
	roundedRectOutline(canvas, button, 28, 2, color.RGBA{timerAccent.R, timerAccent.G, timerAccent.B, 190})
	buttonLabel := "CANCEL TIMER"
	if timer.Ringing {
		buttonLabel = "STOP TIMER"
	}
	r.centeredText(canvas, (button.Min.X+button.Max.X)/2, button.Min.Y+43, buttonLabel, 18, true, color.White)
}

func (r *renderer) drawNotification(canvas *image.RGBA, now time.Time, width int, seconds float64, accent color.RGBA) {
	item := r.state.Notification
	pane := rightPane(width, canvas.Rect.Dy())
	left, right := pane.Min.X, pane.Max.X
	noticeColor := accent
	if item.Priority == "critical" {
		noticeColor = color.RGBA{255, 92, 92, 255}
	}
	heading := notificationCategory(item) + "  ·  " + strings.ToUpper(firstNonEmpty(item.CameraName, item.Title, notificationFallbackTitle(item)))
	headingSize := fittingTextSize(heading, r.faces.bold, 16, 8, pane.Dx())
	r.text(canvas, left, 52, heading, headingSize, true, noticeColor)
	panel := image.Rect(left, 72, right, 260)
	roundedRect(canvas, panel, 18, color.RGBA{20, 27, 39, 245})
	if r.notificationImage != nil {
		drawNotificationImage(canvas, panel, r.notificationImage, notificationImageFit(item, "cover"))
	} else {
		pulse := .5 + .5*math.Sin(seconds*2.2)
		circleOutline(canvas, (left+right)/2, 181, int(24+pulse*9), 4, color.RGBA{noticeColor.R, noticeColor.G, noticeColor.B, uint8(95 + pulse*80)})
		circle(canvas, (left+right)/2, 181, 9, noticeColor)
	}
	size, baseline, lineHeight, lines := notificationDescriptionLayout(r.faces, item.Description, pane.Dx())
	for index, value := range lines {
		r.text(canvas, left, baseline+index*lineHeight, fitTextToWidth(value, r.faces.regular[size], pane.Dx()), size, false, color.RGBA{215, 224, 236, 255})
	}
	if item.ExpiresAtUnixMS > 0 {
		drawNotificationProgressBar(canvas, left, right, pane.Max.Y-7, 3,
			notificationProgress(item, r.currentTaterUnixMS(now)), noticeColor)
	}
}

func (r *renderer) drawLastEvent(canvas *image.RGBA, now time.Time, width int, seconds float64, accent color.RGBA) {
	r.drawCrownEventCard(canvas, now, width, seconds, accent, r.state.LastEvent, r.lastEventImage, false)
}

func (r *renderer) drawCrownEventCard(
	canvas *image.RGBA,
	now time.Time,
	width int,
	seconds float64,
	accent color.RGBA,
	item *show.Notification,
	eventImage image.Image,
	transient bool,
) {
	if item == nil {
		return
	}
	pane := crownPane(width, crownContextHeight)
	left, right := pane.Min.X, pane.Max.X
	eventColor := accent
	if item.Priority == "critical" {
		eventColor = color.RGBA{255, 92, 92, 255}
	}
	name := firstNonEmpty(item.CameraName, item.Title, notificationFallbackTitle(item))
	prefix := "LAST EVENT"
	if transient {
		prefix = notificationCategory(item)
	}
	heading := prefix + "  ·  " + strings.ToUpper(name)
	headingSize := fittingTextSize(heading, r.faces.bold, 18, 8, pane.Dx())
	r.text(canvas, left, 54, heading, headingSize, true, eventColor)

	imageWidth := min(430, max(366, pane.Dx()-334))
	imageFrame := image.Rect(left, 78, left+imageWidth, 353)
	roundedRect(canvas, imageFrame, 18, color.RGBA{20, 27, 39, 245})
	if eventImage != nil {
		drawNotificationImage(canvas, imageFrame.Inset(6), eventImage, notificationImageFit(item, "contain"))
	} else {
		pulse := .5 + .5*math.Sin(seconds*1.8)
		cx, cy := (imageFrame.Min.X+imageFrame.Max.X)/2, (imageFrame.Min.Y+imageFrame.Max.Y)/2
		circle(canvas, cx, cy, 44, color.RGBA{eventColor.R, eventColor.G, eventColor.B, 22})
		circleOutline(canvas, cx, cy, int(25+pulse*6), 4, color.RGBA{eventColor.R, eventColor.G, eventColor.B, uint8(110 + pulse*70)})
		circle(canvas, cx, cy, 9, eventColor)
	}

	separatorX := imageFrame.Max.X + 18
	line(canvas, separatorX, imageFrame.Min.Y, separatorX, 424, 1, color.RGBA{eventColor.R, eventColor.G, eventColor.B, 55})
	textLeft := separatorX + 22
	textWidth := max(1, right-textLeft)
	r.text(canvas, textLeft, 98, strings.ToUpper(firstNonEmpty(titleWords(item.Source), titleWords(item.Kind), "Awareness")), 14, true, eventColor)
	titleLines := wrapTextToWidth(name, r.faces.bold[26], textWidth)
	titleLineCount := min(2, len(titleLines))
	for index, value := range titleLines {
		if index >= titleLineCount {
			break
		}
		r.text(canvas, textLeft, 139+index*32, fitTextToWidth(value, r.faces.bold[26], textWidth), 26, true, color.White)
	}
	titleBottom := 139 + max(0, titleLineCount-1)*32
	meta := r.lastEventTimestamp(item.CreatedAtUnixMS, now)
	if meta == "" && transient {
		meta = "NOW"
	}
	if meta != "" {
		r.text(canvas, textLeft, titleBottom+29, fitTextToWidth(meta, r.faces.bold[16], textWidth), 16, true, color.RGBA{151, 164, 184, 255})
	}
	description := strings.TrimSpace(item.Description)
	if description == "" {
		description = name
	}
	descriptionY := titleBottom + 38
	if meta != "" {
		descriptionY = titleBottom + 64
	}
	descriptionLines := wrapTextToWidth(description, r.faces.regular[20], textWidth)
	maxLines := max(1, (416-descriptionY)/27)
	for index, value := range descriptionLines {
		if index >= maxLines {
			break
		}
		if index == maxLines-1 && len(descriptionLines) > maxLines {
			value = fitTextToWidth(value+"…", r.faces.regular[20], textWidth)
		}
		r.text(canvas, textLeft, descriptionY+index*27, value, 20, false, color.RGBA{215, 224, 236, 255})
	}
	if transient && item.ExpiresAtUnixMS > 0 {
		drawNotificationProgressBar(canvas, left, right, pane.Max.Y-7, 3,
			notificationProgress(item, r.currentTaterUnixMS(now)), eventColor)
	}
}

func notificationCategory(item *show.Notification) string {
	if item == nil {
		return "NOTIFICATION"
	}
	if label := strings.TrimSpace(item.Label); label != "" {
		return strings.ToUpper(strings.Join(strings.Fields(label), " "))
	}
	kind := strings.ToLower(strings.TrimSpace(item.Kind))
	source := strings.ToLower(strings.TrimSpace(item.Source))
	if kind == "awareness" || kind == "camera" || kind == "doorbell" || strings.Contains(source, "awareness") {
		return "AWARENESS"
	}
	if kind != "" && kind != "notification" {
		return strings.ToUpper(titleWords(kind))
	}
	if source != "" && source != "api" && source != "notify" {
		return strings.ToUpper(titleWords(source))
	}
	return "NOTIFICATION"
}

func notificationFallbackTitle(item *show.Notification) string {
	if notificationCategory(item) == "AWARENESS" {
		return "Recent activity"
	}
	if item != nil {
		if label := titleWords(item.Label); label != "" {
			return label
		}
		if kind := titleWords(item.Kind); kind != "" && !strings.EqualFold(kind, "notification") {
			return kind
		}
	}
	return "Notification"
}

func notificationImageFit(item *show.Notification, fallback string) string {
	if item != nil {
		switch strings.ToLower(strings.TrimSpace(item.ImageFit)) {
		case "contain", "fit":
			return "contain"
		case "cover", "fill":
			return "cover"
		}
	}
	return fallback
}

func drawNotificationImage(canvas *image.RGBA, bounds image.Rectangle, source image.Image, fit string) {
	if fit == "cover" {
		drawCover(canvas, bounds, source)
		return
	}
	drawContain(canvas, bounds, source)
}

func notificationProgress(item *show.Notification, nowUnixMS int64) float64 {
	if item == nil || item.ExpiresAtUnixMS <= 0 || nowUnixMS >= item.ExpiresAtUnixMS {
		return 0
	}
	duration := item.TTLMS
	if duration <= 0 && item.CreatedAtUnixMS > 0 && item.CreatedAtUnixMS < item.ExpiresAtUnixMS {
		duration = item.ExpiresAtUnixMS - item.CreatedAtUnixMS
	}
	if duration <= 0 {
		duration = 90 * time.Second.Milliseconds()
	}
	remaining := item.ExpiresAtUnixMS - nowUnixMS
	return math.Max(0, math.Min(1, float64(remaining)/float64(duration)))
}

func drawNotificationProgressBar(canvas *image.RGBA, left, right, y, thickness int, progress float64, ink color.RGBA) {
	if right <= left {
		return
	}
	line(canvas, left, y, right, y, thickness, color.RGBA{ink.R, ink.G, ink.B, 48})
	filled := left + int(float64(right-left)*math.Max(0, math.Min(1, progress)))
	if filled > left {
		line(canvas, left, y, filled, y, thickness, color.RGBA{ink.R, ink.G, ink.B, 210})
	}
}
func (r *renderer) lastEventTimestamp(createdAtUnixMS int64, now time.Time) string {
	if createdAtUnixMS <= 0 {
		return ""
	}
	offset := time.Duration(r.state.TaterUTCOffset) * time.Second
	created := time.UnixMilli(createdAtUnixMS).UTC().Add(offset)
	localNow := r.localTime(now)
	day := func(value time.Time) time.Time {
		return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
	}
	label := created.Format("JAN 2")
	switch delta := int(day(localNow).Sub(day(created)).Hours() / 24); delta {
	case 0:
		label = "TODAY"
	case 1:
		label = "YESTERDAY"
	}
	return label + "  ·  " + created.Format("3:04 PM")
}

func (r *renderer) drawIntercom(canvas *image.RGBA, accent color.RGBA) {
	centerX, centerY, radius, iconLeft, iconTop := intercomGeometry(canvas.Rect.Dx(), canvas.Rect.Dy())
	active := r.intercomPressed || r.state.Phase == "intercom"
	enabled := r.state.Connected && !r.state.Muted
	buttonColor := accent
	if active {
		buttonColor = color.RGBA{255, 105, 140, 255}
		circle(canvas, centerX, centerY, radius+7, color.RGBA{buttonColor.R, buttonColor.G, buttonColor.B, 32})
	}
	strength := uint8(74)
	if !enabled {
		strength = 32
	} else if active {
		strength = 145
	}
	circle(canvas, centerX, centerY, radius, color.RGBA{buttonColor.R, buttonColor.G, buttonColor.B, strength})
	circleOutline(canvas, centerX, centerY, radius-2, 3,
		color.RGBA{buttonColor.R, buttonColor.G, buttonColor.B, 200})
	if r.intercomIcon == nil {
		r.intercomIcon = newMicrophoneIcon()
	}
	iconAlpha := uint8(245)
	if !enabled {
		iconAlpha = 120
	}
	draw.DrawMask(canvas, image.Rect(iconLeft, iconTop, iconLeft+micIconWidth, iconTop+micIconHeight),
		image.NewUniform(color.NRGBA{R: 255, G: 255, B: 255, A: iconAlpha}), image.Point{},
		r.intercomIcon, image.Point{}, draw.Over)
}

// A small antialiased, filled microphone glyph reads more clearly than the
// previous framebuffer-drawn outline at Checkers' native pixel density.
func newMicrophoneIcon() *image.RGBA {
	icon := image.NewRGBA(image.Rect(0, 0, micIconWidth, micIconHeight))
	path := vector.NewRasterizer(micIconWidth, micIconHeight)
	// Capsule.
	path.MoveTo(20, 2)
	path.CubeTo(16.5, 2, 14, 4.8, 14, 8.5)
	path.LineTo(14, 20.5)
	path.CubeTo(14, 24.2, 16.5, 27, 20, 27)
	path.CubeTo(23.5, 27, 26, 24.2, 26, 20.5)
	path.LineTo(26, 8.5)
	path.CubeTo(26, 4.8, 23.5, 2, 20, 2)
	path.ClosePath()
	// One filled U shape forms the cradle without rough overlapping strokes.
	path.MoveTo(8, 20)
	path.LineTo(12, 20)
	path.LineTo(12, 26)
	path.CubeTo(12, 32.5, 15, 36, 20, 36)
	path.CubeTo(25, 36, 28, 32.5, 28, 26)
	path.LineTo(28, 20)
	path.LineTo(32, 20)
	path.LineTo(32, 26)
	path.CubeTo(32, 35, 27.5, 40, 20, 40)
	path.CubeTo(12.5, 40, 8, 35, 8, 26)
	path.ClosePath()
	// Stem and rounded base.
	path.MoveTo(18, 39)
	path.LineTo(22, 39)
	path.LineTo(22, 44)
	path.LineTo(18, 44)
	path.ClosePath()
	path.MoveTo(12, 43)
	path.LineTo(28, 43)
	path.CubeTo(29.5, 43, 30.5, 44, 30.5, 45.5)
	path.CubeTo(30.5, 47, 29.5, 47.5, 28, 47.5)
	path.LineTo(12, 47.5)
	path.CubeTo(10.5, 47.5, 9.5, 47, 9.5, 45.5)
	path.CubeTo(9.5, 44, 10.5, 43, 12, 43)
	path.ClosePath()
	path.Draw(icon, icon.Rect, image.NewUniform(color.White), image.Point{})
	return icon
}

func (r *renderer) text(canvas *image.RGBA, x, baseline int, value string, size int, bold bool, ink color.Color) {
	face := r.faces.regular[size]
	if bold {
		face = r.faces.bold[size]
	}
	drawer := font.Drawer{Dst: canvas, Src: image.NewUniform(ink), Face: face, Dot: fixed.P(x, baseline)}
	drawer.DrawString(value)
}

func (r *renderer) centeredText(canvas *image.RGBA, centerX, baseline int, value string, size int, bold bool, ink color.Color) {
	face := r.faces.regular[size]
	if bold {
		face = r.faces.bold[size]
	}
	width := font.MeasureString(face, value).Round()
	r.text(canvas, centerX-width/2, baseline, value, size, bold, ink)
}

func wrapTextToWidth(value string, face font.Face, maxWidth int) []string {
	words := strings.Fields(value)
	lines := make([]string, 0, 4)
	lineValue := ""
	for _, word := range words {
		candidate := strings.TrimSpace(lineValue + " " + word)
		if lineValue != "" && font.MeasureString(face, candidate).Round() > maxWidth {
			lines = append(lines, lineValue)
			lineValue = word
		} else {
			lineValue = candidate
		}
	}
	if lineValue != "" {
		lines = append(lines, lineValue)
	}
	return lines
}

func notificationDescriptionLayout(faces *faceSet, description string, maxWidth int) (size, baseline, lineHeight int, lines []string) {
	// Use the larger type for brief alerts; longer descriptions still fit below
	// the image without covering the expiry indicator.
	for _, option := range []struct{ size, baseline, lineHeight, maxLines int }{
		{36, 296, 40, 3},
		{30, 290, 32, 4},
	} {
		wrapped := wrapTextToWidth(description, faces.regular[option.size], maxWidth)
		if len(wrapped) <= option.maxLines {
			return option.size, option.baseline, option.lineHeight, wrapped
		}
		if option.size == 30 {
			wrapped = wrapped[:option.maxLines]
			wrapped[len(wrapped)-1] = fitTextToWidth(wrapped[len(wrapped)-1]+"…", faces.regular[option.size], maxWidth)
			return option.size, option.baseline, option.lineHeight, wrapped
		}
	}
	return 30, 290, 32, nil
}

func (r *renderer) currentTaterUnixMS(now time.Time) int64 {
	if r.clockUnixMS > 0 {
		if r.clockReceived.IsZero() {
			return r.clockUnixMS
		}
		return r.clockUnixMS + now.Sub(r.clockReceived).Milliseconds()
	}
	if r.state.TaterTimeUnixMS <= 0 {
		return now.UnixMilli()
	}
	if r.received.IsZero() {
		return r.state.TaterTimeUnixMS
	}
	return r.state.TaterTimeUnixMS + now.Sub(r.received).Milliseconds()
}

const clockCorrectionThreshold = 5 * time.Second

// updateClockAnchor keeps the Tater clock independent from ordinary display
// snapshots. Tater refreshes its clock value once a minute, while audio,
// volume, and status changes can rebroadcast the same value many times. If
// those repeated snapshots reset the receive time, the displayed clock can
// move backwards across a minute boundary.
func (r *renderer) updateClockAnchor(state show.Snapshot, received time.Time) {
	source := state.TaterTimeUnixMS
	if source <= 0 || source == r.clockSourceUnixMS {
		return
	}
	if r.clockUnixMS <= 0 || r.clockReceived.IsZero() {
		r.clockUnixMS = source
		r.clockSourceUnixMS = source
		r.clockReceived = received
		return
	}

	projected := r.clockUnixMS + received.Sub(r.clockReceived).Milliseconds()
	difference := time.Duration(source-projected) * time.Millisecond
	if difference > clockCorrectionThreshold || difference < -clockCorrectionThreshold {
		// A material change is an intentional server-side clock correction.
		r.clockUnixMS = source
	} else if source > projected {
		// Accept small forward corrections, but never jitter backwards merely
		// because the refreshed timestamp spent time in transit.
		r.clockUnixMS = source
	} else {
		r.clockUnixMS = projected
	}
	r.clockSourceUnixMS = source
	r.clockReceived = received
}

func (r *renderer) notificationActive(now time.Time) bool {
	return r.state.Notification != nil && notificationIdentity(r.state.Notification) != r.notificationDismissedID &&
		(r.state.Notification.ExpiresAtUnixMS <= 0 || r.currentTaterUnixMS(now) < r.state.Notification.ExpiresAtUnixMS)
}

func weatherDisplayKind(weather *show.Weather, local time.Time) string {
	if weather == nil {
		return ""
	}
	kind := strings.ToLower(strings.TrimSpace(weather.ConditionKind))
	daylight := strings.ToLower(strings.TrimSpace(weather.Daylight))
	isNight := daylight == "night"
	if daylight != "day" && daylight != "night" {
		// Older Tater versions do not send daylight yet. Keep those devices
		// sensible using their already-synchronized local display clock.
		isNight = local.Hour() < 6 || local.Hour() >= 18
	}
	if !isNight {
		return kind
	}
	switch {
	case kind == "partly", strings.Contains(kind, "partly"):
		return "partly-night"
	case kind == "sun", kind == "sunny", strings.Contains(kind, "clear"):
		return "moon"
	default:
		return kind
	}
}

func (r *renderer) drawWeatherIcon(canvas *image.RGBA, cx, cy, radius int, kind string, seconds float64, accent color.RGBA) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	circle(canvas, cx, cy, radius+36, color.RGBA{accent.R, accent.G, accent.B, 9})
	circle(canvas, cx, cy, radius+18, color.RGBA{accent.R, accent.G, accent.B, 9})
	sun := kind == "sun" || kind == "partly" || strings.Contains(kind, "clear")
	moon := kind == "moon" || kind == "partly-night"
	cloud := (!sun && !moon) || kind == "partly" || kind == "partly-night"
	if kind == "fog" || kind == "wind" {
		cloud = false
	}
	drift := math.Sin(seconds*.42) * float64(radius) * .07
	if sun {
		sunX, sunY := cx, cy
		if kind == "partly" {
			sunX -= int(float64(radius) * .34)
			sunY -= int(float64(radius) * .23)
		}
		breath := 1 + .035*math.Sin(seconds*1.7)
		circle(canvas, sunX, sunY, int(float64(radius)*.72*breath), color.RGBA{255, 190, 75, 25})
		circle(canvas, sunX, sunY, int(float64(radius)*.38*breath), color.RGBA{255, 194, 79, 255})
		for index := 0; index < 8; index++ {
			angle := math.Pi*float64(index)/4 + seconds*.16
			line(canvas,
				sunX+int(math.Cos(angle)*float64(radius)*.48), sunY+int(math.Sin(angle)*float64(radius)*.48),
				sunX+int(math.Cos(angle)*float64(radius)*.64), sunY+int(math.Sin(angle)*float64(radius)*.64),
				3, color.RGBA{255, 191, 81, 255})
		}
	}
	if moon {
		moonX, moonY := cx, cy
		if kind == "partly-night" {
			moonX -= int(float64(radius) * .34)
			moonY -= int(float64(radius) * .23)
		}
		breath := 1 + .025*math.Sin(seconds*1.25)
		moonRadius := int(float64(radius) * .43 * breath)
		circle(canvas, moonX, moonY, int(float64(radius)*.78*breath), color.RGBA{150, 190, 255, 18})
		drawCrescent(canvas, moonX, moonY, moonRadius, color.RGBA{218, 231, 255, 255})
		for index := 0; index < 4; index++ {
			angle := float64(index)*1.73 + .5
			distance := float64(radius) * (.58 + .10*float64(index%2))
			alpha := uint8(125 + 80*(.5+.5*math.Sin(seconds*1.8+float64(index))))
			circle(canvas, moonX+int(math.Cos(angle)*distance), moonY+int(math.Sin(angle)*distance), 1+index%2,
				color.RGBA{198, 220, 255, alpha})
		}
	}
	if cloud {
		if kind == "cloud" || kind == "rain" || kind == "storm" {
			drawCloud(canvas, cx-int(float64(radius)*.42+drift*.45), cy-int(float64(radius)*.18),
				int(float64(radius)*.76), color.RGBA{122, 143, 169, 105})
			drawCloud(canvas, cx+int(float64(radius)*.40+drift*.30), cy-int(float64(radius)*.03),
				int(float64(radius)*.62), color.RGBA{144, 162, 185, 115})
		}
		cloudColor := color.RGBA{215, 226, 239, 255}
		if kind == "storm" {
			cloudColor = color.RGBA{139, 151, 175, 255}
		}
		drawCloud(canvas, cx+int(drift), cy+int(float64(radius)*.12), radius, cloudColor)
	}
	switch kind {
	case "rain", "storm":
		for index := 0; index < 6; index++ {
			progress := math.Mod(seconds*.72+float64(index)*.19, 1)
			x := cx + int(float64(radius)*(-.62+float64(index%4)*.40)+drift)
			y := cy + int(float64(radius)*(.50+progress*.76))
			line(canvas, x, y, x-int(float64(radius)*.10), y+int(float64(radius)*.24), 3,
				color.RGBA{83, 178, 255, uint8(210 * (1 - progress))})
		}
		if kind == "storm" && math.Sin(seconds*5.7) > .72 {
			line(canvas, cx+3, cy+20, cx-4, cy+37, 4, color.RGBA{255, 213, 79, 255})
			line(canvas, cx-4, cy+37, cx+4, cy+34, 4, color.RGBA{255, 213, 79, 255})
			line(canvas, cx+4, cy+34, cx-2, cy+51, 4, color.RGBA{255, 213, 79, 255})
		}
	case "snow":
		for index := 0; index < 7; index++ {
			progress := math.Mod(seconds*.24+float64(index)*.17, 1)
			x := cx + int(float64(radius)*(-.70+float64(index%4)*.45+math.Sin(seconds+float64(index))*.08))
			y := cy + int(float64(radius)*(.50+progress*.83))
			circle(canvas, x, y, 2, color.RGBA{190, 226, 255, 255})
		}
	case "fog", "wind":
		for index := -1; index <= 1; index++ {
			y := cy + int(float64(index*radius)*.27)
			travel := math.Sin(seconds*.50+float64(index)) * float64(radius) * .12
			span := float64(radius) * (.72 - math.Abs(float64(index))*.10)
			line(canvas, cx-int(span)+int(travel), y, cx+int(span)+int(travel), y, 3,
				color.RGBA{191, 208, 225, 220})
		}
	}
}

func drawCrescent(canvas *image.RGBA, cx, cy, radius int, ink color.RGBA) {
	if radius <= 0 {
		return
	}
	cutX, cutY := cx+radius*2/5, cy-radius/7
	cutRadius := radius * 9 / 10
	for y := cy - radius; y <= cy+radius; y++ {
		for x := cx - radius; x <= cx+radius; x++ {
			dx, dy := x-cx, y-cy
			if dx*dx+dy*dy > radius*radius {
				continue
			}
			cutDX, cutDY := x-cutX, y-cutY
			if cutDX*cutDX+cutDY*cutDY < cutRadius*cutRadius {
				continue
			}
			blendPixel(canvas, x, y, ink)
		}
	}
}

func drawCloud(canvas *image.RGBA, cx, cy, radius int, ink color.RGBA) {
	circle(canvas, cx-int(float64(radius)*.34), cy+int(float64(radius)*.10), int(float64(radius)*.31), ink)
	circle(canvas, cx, cy-int(float64(radius)*.08), int(float64(radius)*.42), ink)
	circle(canvas, cx+int(float64(radius)*.38), cy+int(float64(radius)*.12), int(float64(radius)*.28), ink)
	roundedRect(canvas, image.Rect(cx-int(float64(radius)*.62), cy+int(float64(radius)*.05),
		cx+int(float64(radius)*.63), cy+int(float64(radius)*.39)), max(2, int(float64(radius)*.16)), ink)
}

func main() {
	log.SetOutput(os.Stdout)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	screenDevice, err := linuxscreen.Open()
	if err != nil {
		log.Fatalf("screen: %v", err)
	}
	defer screenDevice.Close()
	faces, err := newFaceSet()
	if err != nil {
		log.Fatalf("fonts: %v", err)
	}
	log.Printf("Tater Show %s using %s", version, screenDevice)
	client := newSocketClient()
	go client.run(ctx)
	// Match the original Checkers event loop: the small queue drops expendable
	// move samples in the evdev reader while Down and Up remain lossless.
	touchEvents := make(chan linuxinput.TouchEvent, 16)
	if touch, touchErr := linuxinput.Open(); touchErr != nil {
		log.Printf("touch unavailable: %v", touchErr)
	} else {
		defer touch.Close()
		log.Printf("touch using %s", touch)
		go func() {
			if err := touch.Run(ctx, touchEvents); err != nil && ctx.Err() == nil {
				log.Printf("touch stopped: %v", err)
			}
		}()
	}
	screenWidth, screenHeight := screenDevice.Canvas().Rect.Dx(), screenDevice.Canvas().Rect.Dy()
	spotScreen := screenWidth == screenHeight
	normalizedTarget := normalizeDisplayTarget(target, screenWidth, screenHeight)
	if linkedTarget := strings.ToLower(strings.TrimSpace(target)); normalizedTarget != linkedTarget {
		log.Printf("display target corrected from %q to %q for %dx%d framebuffer", linkedTarget, normalizedTarget, screenWidth, screenHeight)
	}
	deviceName := "Tater Checkers"
	if normalizedTarget == "athena" {
		deviceName = "Tater Athena"
	} else if normalizedTarget == "crown" {
		deviceName = "Tater Crown"
	} else if spotScreen {
		deviceName = "Tater Spot"
	}
	renderer := &renderer{
		faces: faces, spotScreen: spotScreen, target: normalizedTarget,
		screenWidth: screenWidth, screenHeight: screenHeight,
		state: show.Snapshot{
			Phase: "offline", Message: "Connecting to Tater", DeviceName: deviceName, DisplayEdgeGlow: true,
			ScreenBrightness: 35, ScreenAutoBrightnessMinimum: 12, ScreenNightBrightness: 3,
			ScreenNightStart: "22:00", ScreenNightEnd: "07:00", ScreenNightDisplay: "red_clock",
		},
		animationStart: time.Now(),
	}
	displayPower := newDisplayPowerController(normalizedTarget)
	defer displayPower.Close()
	imageResults := make(chan screenImageResult, 6)
	satChatVideoRequests := make(chan screenImageRequest, 1)
	go runLatestScreenImageLoader(ctx, satChatVideoRequests, imageResults)
	cameraStream := newCameraStreamController()
	defer cameraStream.Close()
	// Drive animation from wall time so dropped ticks slow the frame rate, not
	// the motion itself. Each native layout reports its animated regions; the
	// framebuffer path can therefore keep a steady cadence without repeatedly
	// rotating unchanged composition.
	ticker := time.NewTicker(displayFrameInterval(normalizedTarget))
	defer ticker.Stop()
	frameWindow := time.Now()
	frames := 0
	var renderTotal, presentTotal, convertTotal, panTotal, renderMax, presentMax time.Duration
	videoStatsAt := time.Now()
	videoStatsSession := ""
	videoLastPresented := ""
	videoDecoded, videoPresented := 0, 0
	var videoLoadTotal, videoScaleTotal, videoPresentTotal time.Duration
	for {
		select {
		case <-ctx.Done():
			return
		case state := <-client.states:
			received := time.Now()
			if !renderer.received.IsZero() && state.VolumePercent != renderer.state.VolumePercent {
				renderer.beginVolumeOverlay(state.VolumePercent, received)
			}
			renderer.updateClockAnchor(state, received)
			if notificationIdentity(state.Notification) != notificationIdentity(renderer.state.Notification) {
				renderer.notificationDismissedID = ""
			}
			if state.Phase != renderer.state.Phase || notificationIdentity(state.Notification) != notificationIdentity(renderer.state.Notification) || notificationIdentity(state.LastEvent) != notificationIdentity(renderer.state.LastEvent) || timerIdentity(state.Timer) != timerIdentity(renderer.state.Timer) || mediaIdentity(state.Media) != mediaIdentity(renderer.state.Media) {
				renderer.animationStart = time.Now()
			}
			renderer.state = state
			renderer.received = received
			if cameraStream.Update(ctx, state.Cameras, renderer.cameraVideoBounds().Size()) {
				renderer.resetCameraStream(cameraStream)
			}
			setVideoCoreFloor(cameraStream.Active() || cameraVideoActive(state) || satChatVideoActive(state))
			renderer.queueNotificationImage(ctx, imageResults)
			renderer.queueLastEventImage(ctx, imageResults)
			renderer.queueMediaImage(ctx, imageResults)
			renderer.queueWallpaperImage(ctx, imageResults, time.Now())
			renderer.queueSatChatVideoImage(satChatVideoRequests)
			renderer.queueCameraVideoImage(satChatVideoRequests)
		case result := <-imageResults:
			acceptedVideo := result.kind == satChatVideoScreenImage && result.image != nil &&
				renderer.state.SatChat != nil && renderer.state.SatChat.Call != nil &&
				result.session != "" && result.session == renderer.state.SatChat.Call.ID
			renderer.applyScreenImage(result)
			if acceptedVideo {
				videoDecoded++
				videoLoadTotal += result.loadDuration
				videoScaleTotal += result.scaleDuration
			}
		case result := <-cameraStream.frames:
			renderer.applyCameraStream(cameraStream, result)
		case event := <-touchEvents:
			handleQueuedTouch(renderer, client, event)
			// A single physical contact can enqueue several movement samples
			// before its release. Finish that burst now so Up cannot sit behind
			// animation frames and make taps feel delayed on Crown.
			drainTouchEvents(renderer, client, touchEvents)
		case <-ticker.C:
			started := time.Now()
			displayPower.Update(renderer.state, renderer.localTime(started), started)
			renderer.nightModeActive = displayPower.night
			// A failed loopback fetch clears its image identity. Rechecking here
			// lets slow devices recover during the same notification (and lets a
			// retained Last Event recover later) without waiting for another state
			// snapshot from the daemon.
			renderer.queueNotificationImage(ctx, imageResults)
			renderer.queueLastEventImage(ctx, imageResults)
			renderer.queueWallpaperImage(ctx, imageResults, started)
			renderer.updateIntercomHold(client, started)
			renderer.render(screenDevice.Canvas(), started)
			rendered := time.Since(started)
			var presented, converted, panned time.Duration
			if len(renderer.presentRegions) > 0 {
				presentStarted := time.Now()
				var err error
				if renderer.transientPresent {
					err = screenDevice.PresentTransientRegions(renderer.presentRegions)
				} else {
					err = screenDevice.PresentRegions(renderer.presentRegions)
				}
				if err != nil {
					log.Fatalf("present screen: %v", err)
				}
				presented = time.Since(presentStarted)
				converted, panned = screenDevice.LastPresentTimings()
				if renderer.satChatVideoActive() && renderer.satChatVideoDrawn != "" && renderer.satChatVideoDrawn != videoLastPresented {
					videoPresented++
					videoPresentTotal += presented
					videoLastPresented = renderer.satChatVideoDrawn
				}
			}
			videoSession := ""
			if renderer.satChatVideoActive() {
				videoSession = renderer.state.SatChat.Call.ID
			}
			if videoSession != videoStatsSession {
				videoStatsSession = videoSession
				videoStatsAt = time.Now()
				videoLastPresented = ""
				videoDecoded, videoPresented = 0, 0
				videoLoadTotal, videoScaleTotal, videoPresentTotal = 0, 0, 0
			} else if videoSession != "" && time.Since(videoStatsAt) >= 10*time.Second {
				elapsed := time.Since(videoStatsAt)
				log.Printf("[sat-chat-video] decoded %.1f fps (fetch/decode %s, scale %s), displayed %.1f fps (present %s)",
					float64(videoDecoded)/elapsed.Seconds(), averageFrameDuration(videoLoadTotal, videoDecoded).Round(time.Millisecond),
					averageFrameDuration(videoScaleTotal, videoDecoded).Round(time.Millisecond), float64(videoPresented)/elapsed.Seconds(),
					averageFrameDuration(videoPresentTotal, videoPresented).Round(time.Millisecond))
				videoStatsAt = time.Now()
				videoDecoded, videoPresented = 0, 0
				videoLoadTotal, videoScaleTotal, videoPresentTotal = 0, 0, 0
			}
			frames++
			renderTotal += rendered
			presentTotal += presented
			convertTotal += converted
			panTotal += panned
			if rendered > renderMax {
				renderMax = rendered
			}
			if presented > presentMax {
				presentMax = presented
			}
			if elapsed := time.Since(frameWindow); elapsed >= 15*time.Second {
				renderAverage := averageFrameDuration(renderTotal, frames)
				presentAverage := averageFrameDuration(presentTotal, frames)
				convertAverage := averageFrameDuration(convertTotal, frames)
				panAverage := averageFrameDuration(panTotal, frames)
				diagnostics := show.ScreenDiagnostics{
					LayoutTarget:  normalizedTarget,
					Width:         screenWidth,
					Height:        screenHeight,
					FPS:           float64(frames) / elapsed.Seconds(),
					RenderAvgMS:   float64(renderAverage) / float64(time.Millisecond),
					RenderMaxMS:   float64(renderMax) / float64(time.Millisecond),
					PresentAvgMS:  float64(presentAverage) / float64(time.Millisecond),
					PresentMaxMS:  float64(presentMax) / float64(time.Millisecond),
					RotateAvgMS:   float64(convertAverage) / float64(time.Millisecond),
					PanAvgMS:      float64(panAverage) / float64(time.Millisecond),
					TouchDowns:    renderer.touchDowns,
					TouchMoves:    renderer.touchMoves,
					TouchUps:      renderer.touchUps,
					LastTouchX:    renderer.lastTouchX,
					LastTouchY:    renderer.lastTouchY,
					LastTouchAtMS: renderer.lastTouchAtMS,
					UpdatedAtMS:   time.Now().UnixMilli(),
				}
				displayPower.ApplyDiagnostics(&diagnostics, renderer.state)
				log.Printf("[display] %.1f fps, render avg/max %s/%s, present avg/max %s/%s (rotate %s, pan %s)",
					diagnostics.FPS, renderAverage.Round(time.Millisecond), renderMax.Round(time.Millisecond),
					presentAverage.Round(time.Millisecond), presentMax.Round(time.Millisecond),
					convertAverage.Round(time.Millisecond), panAverage.Round(time.Millisecond))
				client.sendDiagnostics(diagnostics)
				frameWindow, frames, renderTotal, presentTotal, convertTotal, panTotal, renderMax, presentMax = time.Now(), 0, 0, 0, 0, 0, 0, 0
			}
		}
	}
}

func averageFrameDuration(total time.Duration, frames int) time.Duration {
	if frames <= 0 {
		return 0
	}
	return total / time.Duration(frames)
}

// normalizeDisplayTarget keeps the linked target authoritative for every
// platform except the one impossible combination we can identify from the
// hardware itself: Checkers is 960x480, while Crown is 1280x800. A test OTA
// built with the default Checkers linker value must not silently put the old
// compact layout back onto a Crown display. Athena, Rook, and future explicit
// targets remain untouched even when they share Crown's framebuffer dimensions.
func normalizeDisplayTarget(linked string, width, height int) string {
	normalized := strings.ToLower(strings.TrimSpace(linked))
	if normalized == "checkers" && width >= 1200 && height >= 700 {
		return "crown"
	}
	return normalized
}

func displayFrameInterval(target string) time.Duration {
	return time.Second / 30
}

func drainTouchEvents(renderer *renderer, client *socketClient, events <-chan linuxinput.TouchEvent) int {
	handled := 0
	for {
		select {
		case event := <-events:
			if handleQueuedTouch(renderer, client, event) {
				handled++
			}
		default:
			return handled
		}
	}
}

func handleQueuedTouch(renderer *renderer, client *socketClient, event linuxinput.TouchEvent) bool {
	switch event.Kind {
	case linuxinput.Down:
		renderer.touchDowns++
	case linuxinput.Move:
		renderer.touchMoves++
	case linuxinput.Up:
		renderer.touchUps++
	}
	renderer.lastTouchX = event.X
	renderer.lastTouchY = event.Y
	renderer.lastTouchAtMS = time.Now().UnixMilli()
	// The current controls are tap/hold based and use the release coordinates;
	// movement samples cannot change their state. Ignoring them prevents panel
	// jitter from scheduling expensive full-frame Crown redraws.
	if event.Kind == linuxinput.Move {
		return false
	}
	beforeIntercom := renderer.intercomPressed
	beforeTimer := renderer.timerPressed
	beforeMenuOpen := renderer.menuOpen
	beforeCameraOpen := renderer.cameraOpen
	beforeCameraPage := renderer.cameraPage
	beforeSatChatOpen := renderer.satChatOpen
	beforeSatChatPage := renderer.satChatPage
	beforeClimateOpen := renderer.climateOpen
	beforeClimatePage := renderer.climatePage
	handleTouch(renderer, client, event)
	if renderer.isCrown() {
		return beforeIntercom != renderer.intercomPressed || beforeTimer != renderer.timerPressed ||
			beforeMenuOpen != renderer.menuOpen || beforeCameraOpen != renderer.cameraOpen ||
			beforeCameraPage != renderer.cameraPage || beforeSatChatOpen != renderer.satChatOpen ||
			beforeSatChatPage != renderer.satChatPage || beforeClimateOpen != renderer.climateOpen ||
			beforeClimatePage != renderer.climatePage
	}
	return true
}

func handleTouch(renderer *renderer, client *socketClient, event linuxinput.TouchEvent) {
	if renderer.spotScreen {
		renderer.handleSpotTouch(client, event)
		return
	}
	width, height := renderer.showSize()
	if renderer.cameraOpen && !renderer.notificationActive(time.Now()) {
		renderer.handleCameraTouch(client, event, width, height, false)
		return
	}
	if renderer.climateOpen && !renderer.notificationActive(time.Now()) {
		if event.Kind == linuxinput.Up {
			renderer.handleClimateTouch(client, event, width, height)
		}
		return
	}
	if renderer.menuOpen && !renderer.notificationActive(time.Now()) {
		switch event.Kind {
		case linuxinput.Down:
			if renderer.state.Connected {
				renderer.satChatTouchDown = true
				renderer.satChatTouchAt = time.Now()
			}
		case linuxinput.Up:
			if renderer.intercomPressed {
				renderer.intercomPressed = false
				client.send("intercom.stop")
			} else {
				renderer.handleActionMenuTouch(client, event, width, height, false)
			}
			renderer.satChatTouchDown = false
			renderer.satChatTouchAt = time.Time{}
		}
		return
	}
	// Once Sat Chat is already visible, its controls need no hold gesture, so
	// Crown may act on Down. A pending mic tap is excluded: that gesture must
	// still be classified by release versus spotHoldDelay so a short tap cannot
	// be mistaken for intercom.
	if renderer.satChatVisible() && !renderer.satChatTouchDown {
		if (renderer.isCrown() && event.Kind == linuxinput.Down) ||
			(!renderer.isCrown() && event.Kind == linuxinput.Up) {
			renderer.handleSatChatTouch(client, event, width, height)
		}
		return
	}
	timerBounds := timerStopBounds(width, height)
	timer := image.Pt(event.X, event.Y).In(timerBounds)
	switch event.Kind {
	case linuxinput.Down:
		if renderer.state.Timer != nil && renderer.state.Timer.Active && timer {
			renderer.timerPressed = true
			return
		}
		if renderer.state.Connected {
			renderer.satChatTouchDown = true
			renderer.satChatTouchAt = time.Now()
		}
	case linuxinput.Up:
		if renderer.timerPressed {
			renderer.timerPressed = false
			if timer {
				client.send("timer.stop")
			}
		}
		if renderer.intercomPressed {
			renderer.intercomPressed = false
			client.send("intercom.stop")
			// The Down event owns the gesture. Show touch panels can move the
			// reported release point a few pixels (or briefly to an edge) as the
			// finger lifts; requiring Up to remain inside the small corner circle
			// made ordinary taps disappear even though holds still worked.
		} else if renderer.satChatTouchDown {
			if renderer.notificationActive(time.Now()) {
				renderer.notificationDismissedID = notificationIdentity(renderer.state.Notification)
				renderer.menuOpen = false
				if renderer.cameraOpen {
					client.sendCamera("camera.close", "")
				}
				renderer.cameraOpen = false
				renderer.climateOpen = false
				renderer.checkersCanvas = nil
				renderer.crownCanvas = nil
			} else {
				renderer.menuOpen = true
			}
		}
		renderer.satChatTouchDown = false
		renderer.satChatTouchAt = time.Time{}
	}
}

func (r *renderer) updateIntercomHold(client *socketClient, now time.Time) {
	if r.spotScreen {
		r.updateSpotHold(client, now)
		return
	}
	if r.intercomPressed && (!r.state.Connected || r.state.Muted) {
		r.intercomPressed = false
		r.satChatTouchDown = false
		client.send("intercom.stop")
	}
	if !r.satChatTouchDown || r.intercomPressed || !r.state.Connected || r.state.Muted || r.satChatTouchAt.IsZero() {
		return
	}
	if now.Sub(r.satChatTouchAt) >= spotHoldDelay {
		r.intercomPressed = true
		client.send("intercom.start")
	}
}

func (r *renderer) satChatVisible() bool {
	return r.satChatOpen || (r.state.SatChat != nil && r.state.SatChat.Call != nil)
}

func satChatPageCount(total, pageSize int) int {
	if pageSize <= 0 || total <= 0 {
		return 1
	}
	return (total + pageSize - 1) / pageSize
}

func (r *renderer) satChatPageFor(total, pageSize int) int {
	pages := satChatPageCount(total, pageSize)
	if r.satChatPage < 0 {
		r.satChatPage = 0
	} else if r.satChatPage >= pages {
		r.satChatPage = pages - 1
	}
	return r.satChatPage
}

func (r *renderer) satChatPageSize(width, height int) int {
	if r.spotScreen {
		return 5
	}
	return satChatDirectoryLayoutFor(width, height).rows * 2
}

func satChatPreviousBounds(width int) image.Rectangle {
	return image.Rect(width-380, 40, width-326, 90)
}

func satChatNextBounds(width int) image.Rectangle {
	return image.Rect(width-316, 40, width-262, 90)

}

func satChatCloseBounds(width int) image.Rectangle {
	return image.Rect(width-112, 34, width-48, 98)
}

type satChatDirectoryLayout struct {
	panel, content image.Rectangle
	rows, gap      int
}

func satChatDirectoryLayoutFor(width, height int) satChatDirectoryLayout {
	rows, top, margin, gap := 4, 120, 48, 14
	if height >= 700 {
		rows, top, margin, gap = 4, 160, 56, 18
	}
	return satChatDirectoryLayout{
		panel:   image.Rect(22, 18, width-22, height-18),
		content: image.Rect(margin, top, width-margin, height-32),
		rows:    rows,
		gap:     gap,
	}
}

func (layout satChatDirectoryLayout) participantBounds(visibleIndex int) image.Rectangle {
	column, row := visibleIndex%2, visibleIndex/2
	cardWidth := (layout.content.Dx() - layout.gap) / 2
	cardHeight := (layout.content.Dy() - (layout.rows-1)*layout.gap) / layout.rows
	left := layout.content.Min.X + column*(cardWidth+layout.gap)
	top := layout.content.Min.Y + row*(cardHeight+layout.gap)
	return image.Rect(left, top, left+cardWidth, top+cardHeight)
}

func (r *renderer) satChatRenderKey(now time.Time, accent color.RGBA) string {
	chat := r.state.SatChat
	if chat == nil {
		return fmt.Sprintf("%t|%d|%s|%v|%s", r.satChatOpen, r.satChatPage, r.state.Phase, accent, r.localTime(now).Format("200601021504"))
	}
	return fmt.Sprintf("%t|%d|%s|%s|%s|%s|%s|%s|%t|%t|%v|%s",
		r.satChatOpen, r.satChatPage, chat.Self, stableCacheValue(chat.Participants), stableCacheValue(chat.Call),
		r.state.Phase, r.state.Message, r.state.Room, r.state.Connected, r.state.Muted,
		accent, r.localTime(now).Format("200601021504"))
}

func (r *renderer) satChatVideoActive() bool {
	return r.state.SatChat != nil && r.state.SatChat.Call != nil &&
		strings.EqualFold(r.state.SatChat.Call.Mode, "video")
}

func (r *renderer) satChatEndBounds(width, height int) image.Rectangle {
	if r.satChatVideoActive() && !r.spotScreen && (r.isCrown() || width >= 900) {
		videoBounds := r.satChatVideoOuterBounds(width, height)
		return image.Rect(width/2-150, videoBounds.Max.Y-78, width/2+150, videoBounds.Max.Y-14)
	}
	return image.Rect(width/2-150, height-112, width/2+150, height-48)
}

func (r *renderer) satChatVideoBounds() image.Rectangle {
	if r.spotScreen {
		return image.Rect(62, 160, 418, 330)
	}
	width, height := r.showSize()
	return r.satChatVideoContentBounds(width, height)
}

func (r *renderer) satChatVideoContentBounds(width, height int) image.Rectangle {
	if r.isCrown() && width >= 1200 && height >= 760 {
		// Crown's larger framebuffer costs noticeably more to scale and present
		// than Checkers. Keep the native 640x360 detail, but display it in an
		// exact 16:9 panel with about half the pixels of the former full-screen
		// viewport. The smaller fast-path update is both sharper and cheaper.
		videoWidth := max(16, (width*7/10/16)*16)
		videoHeight := videoWidth * 9 / 16
		if videoHeight > height*7/10 {
			videoHeight = max(9, (height*7/10/9)*9)
			videoWidth = videoHeight * 16 / 9
		}
		return image.Rect((width-videoWidth)/2, (height-videoHeight)/2,
			(width+videoWidth)/2, (height+videoHeight)/2)
	}
	if !r.spotScreen && width >= 900 && height >= 460 {
		// Checkers gets the same uncropped full-screen treatment. Integer 16:9
		// dimensions keep every source pixel and leave only the unavoidable thin
		// bars imposed by the panel's 2:1 shape.
		scale := min(width/16, height/9)
		videoWidth, videoHeight := scale*16, scale*9
		return image.Rect((width-videoWidth)/2, (height-videoHeight)/2,
			(width+videoWidth)/2, (height+videoHeight)/2)
	}
	return image.Rect(48, 92, width-48, height-132).Inset(4)
}

func (r *renderer) satChatVideoOuterBounds(width, height int) image.Rectangle {
	return r.satChatVideoContentBounds(width, height).Inset(-4)
}

func (r *renderer) drawSatChatVideo(canvas *image.RGBA, width, height int) image.Rectangle {
	videoBounds := r.satChatVideoOuterBounds(width, height)
	drawMenuRoundedRect(canvas, videoBounds, 18, color.RGBA{4, 13, 22, 255})
	if r.satChatVideoImage != nil {
		drawPreparedCover(canvas, r.satChatVideoContentBounds(width, height), r.satChatVideoImage)
	} else {
		r.centeredText(canvas, width/2, (videoBounds.Min.Y+videoBounds.Max.Y)/2, "WAITING FOR CAMERA", 18, true, color.RGBA{164, 179, 198, 255})
	}
	drawMenuRoundedOutline(canvas, videoBounds, 18, 3, color.RGBA{menuCyanInk.R, menuCyanInk.G, menuCyanInk.B, 230})
	r.drawSatChatVideoOverlays(canvas, width, height)
	return videoBounds
}

// drawSatChatVideoFrame replaces only the pixels covered by live video and
// its controls. The rounded backdrop and the panel's black letterbox are
// static for the life of the call and do not need to be repainted for every
// JPEG. This also marks a smaller rectangle for the framebuffer fast path.
func (r *renderer) drawSatChatVideoFrame(canvas *image.RGBA, width, height int) image.Rectangle {
	content := r.satChatVideoContentBounds(width, height)
	drawPreparedCover(canvas, content, r.satChatVideoImage)
	r.drawSatChatVideoOverlays(canvas, width, height)
	return content
}

func (r *renderer) drawSatChatVideoOverlays(canvas *image.RGBA, width, height int) {
	videoBounds := r.satChatVideoOuterBounds(width, height)
	call := r.state.SatChat.Call
	name := firstNonEmpty(call.TargetName, call.CallerName, "Satellite")
	if call.Target == r.state.SatChat.Self {
		name = firstNonEmpty(call.CallerName, "Satellite")
	}
	if !r.spotScreen && (r.isCrown() || width >= 900) {
		title := image.Rect(videoBounds.Min.X+16, videoBounds.Min.Y+14, videoBounds.Min.X+222, videoBounds.Min.Y+62)
		drawMenuRoundedRect(canvas, title, title.Dy()/2, color.RGBA{5, 17, 28, 230})
		drawMenuRoundedOutline(canvas, title, title.Dy()/2, 1, menuOutlineInk)
		r.centeredText(canvas, (title.Min.X+title.Max.X)/2, title.Min.Y+32, "SAT CHAT", 20, true, color.White)
		nameBox := image.Rect(videoBounds.Max.X-390, videoBounds.Min.Y+14, videoBounds.Max.X-16, videoBounds.Min.Y+62)
		drawMenuRoundedRect(canvas, nameBox, nameBox.Dy()/2, color.RGBA{5, 17, 28, 230})
		drawMenuRoundedOutline(canvas, nameBox, nameBox.Dy()/2, 1, menuOutlineInk)
		circle(canvas, nameBox.Min.X+24, (nameBox.Min.Y+nameBox.Max.Y)/2, 6, color.RGBA{52, 240, 105, 255})
		r.centeredText(canvas, (nameBox.Min.X+nameBox.Max.X)/2+8, nameBox.Min.Y+32,
			fitTextToWidth(name, r.faces.bold[20], nameBox.Dx()-32), 20, true, color.White)
		end := r.satChatEndBounds(width, height)
		drawMenuRoundedRect(canvas, end, end.Dy()/2, color.RGBA{139, 30, 39, 248})
		drawMenuRoundedOutline(canvas, end, end.Dy()/2, 2, color.RGBA{255, 80, 72, 240})
		r.centeredText(canvas, width/2, videoBounds.Max.Y-36, "END VIDEO CHAT", 18, true, color.White)
	} else {
		nameBoxRight := min(width-62, 62+max(240, width/3))
		roundedRect(canvas, image.Rect(62, 105, nameBoxRight, 157), 18, color.RGBA{5, 10, 17, 218})
		r.text(canvas, 82, 139, fitTextToWidth(name, r.faces.bold[20], max(200, width/3-40)), 20, true, color.White)
	}
}

func (r *renderer) drawSatChat(canvas *image.RGBA, now time.Time, width, height int, accent color.RGBA) {
	chat := r.state.SatChat
	if chat != nil && chat.Call != nil && strings.EqualFold(chat.Call.Mode, "video") {
		// Compose the call backdrop once. Subsequent frames update only the
		// smaller video rectangle, so the wallpaper and dimming add no work to
		// the live path and never expose stale home-screen pixels.
		if wallpaper := r.activeWallpaper(now); wallpaper != nil {
			drawPreparedCover(canvas, canvas.Bounds(), wallpaper)
		} else {
			fillGradient(canvas, color.RGBA{6, 11, 19, 255}, mix(color.RGBA{6, 11, 19, 255}, accent, .13))
		}
		draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.NRGBA{A: 86}), image.Point{}, draw.Over)
		r.drawSatChatVideo(canvas, width, height)
		return
	}
	if wallpaper := r.activeWallpaper(now); wallpaper != nil {
		drawPreparedCover(canvas, canvas.Bounds(), wallpaper)
	} else {
		fillGradient(canvas, color.RGBA{6, 11, 19, 255}, mix(color.RGBA{6, 11, 19, 255}, accent, .13))
	}
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.NRGBA{A: 86}), image.Point{}, draw.Over)
	layout := satChatDirectoryLayoutFor(width, height)
	backdrop := layout.panel
	drawMenuGlassPanel(canvas, backdrop, max(26, height/22))
	headerSize := 28
	if height >= 700 {
		headerSize = 34
	}
	iconSize := 50
	if height >= 700 {
		iconSize = 68
	}
	iconBounds := image.Rect(backdrop.Min.X+22, backdrop.Min.Y+20, backdrop.Min.X+22+iconSize, backdrop.Min.Y+20+iconSize)
	drawMenuIconMedallion(canvas, iconBounds)
	headerIconX, headerIconY := (iconBounds.Min.X+iconBounds.Max.X)/2, (iconBounds.Min.Y+iconBounds.Max.Y)/2
	drawSatChatNetworkGlyph(canvas, headerIconX, headerIconY, iconSize*28/100, menuCyanInk)
	textX := iconBounds.Max.X + 16
	r.text(canvas, textX, backdrop.Min.Y+46, "SAT CHAT", headerSize, true, color.White)
	r.text(canvas, textX, backdrop.Min.Y+72, "Choose a satellite to start a call", 15, false, menuSubtitleInk)
	if chat != nil && chat.Call != nil {
		call := chat.Call
		name := firstNonEmpty(call.TargetName, call.CallerName, "Satellite")
		if call.Target == chat.Self {
			name = firstNonEmpty(call.CallerName, "Satellite")
		}
		if strings.EqualFold(call.Mode, "video") {
			r.drawSatChatVideo(canvas, width, height)
			if r.spotScreen || (!r.isCrown() && width < 900) {
				end := image.Rect(width/2-150, height-112, width/2+150, height-48)
				drawMenuRoundedRect(canvas, end, end.Dy()/2, color.RGBA{139, 30, 39, 248})
				drawMenuRoundedOutline(canvas, end, end.Dy()/2, 2, color.RGBA{255, 80, 72, 240})
				r.centeredText(canvas, width/2, height-70, "END VIDEO CHAT", 18, true, color.White)
			}
			return
		}
		r.centeredText(canvas, width/2, 138, "SAT CHAT ACTIVE", 18, true, color.RGBA{77, 223, 158, 255})
		r.centeredText(canvas, width/2, 206, fitTextToWidth(name, r.faces.bold[34], width-140), 34, true, color.White)
		r.centeredText(canvas, width/2, 244, strings.ToUpper(firstNonEmpty(call.Mode, "audio"))+" CALL", 16, true, color.RGBA{164, 179, 198, 255})
		end := image.Rect(width/2-150, height-112, width/2+150, height-48)
		drawMenuRoundedRect(canvas, end, end.Dy()/2, color.RGBA{139, 30, 39, 248})
		drawMenuRoundedOutline(canvas, end, end.Dy()/2, 2, color.RGBA{255, 80, 72, 240})
		r.centeredText(canvas, width/2, height-70, "END SAT CHAT", 18, true, color.White)
		return
	}
	closeBounds := satChatCloseBounds(width)
	drawMenuCloseButton(canvas, closeBounds)

	participants := []show.SatChatParticipant{}
	if chat != nil {
		participants = chat.Participants
	}
	if len(participants) == 0 {
		r.centeredText(canvas, width/2, height/2, "NO SATELLITES AVAILABLE", 20, true, color.RGBA{164, 179, 198, 255})
		return
	}
	pageSize := r.satChatPageSize(width, height)
	page := r.satChatPageFor(len(participants), pageSize)
	pageCount := satChatPageCount(len(participants), pageSize)
	start := page * pageSize
	end := min(len(participants), start+pageSize)
	if pageCount > 1 {
		previous, next := satChatPreviousBounds(width), satChatNextBounds(width)
		previousInk, nextInk := menuOrangeInk, menuOrangeInk
		if page == 0 {
			previousInk = color.RGBA{83, 93, 108, 150}
		}
		if page == pageCount-1 {
			nextInk = color.RGBA{83, 93, 108, 150}
		}
		drawMenuRoundedRect(canvas, previous, previous.Dy()/2, menuControlInk)
		drawMenuRoundedRect(canvas, next, next.Dy()/2, menuControlInk)
		drawMenuRoundedOutline(canvas, previous, previous.Dy()/2, 2, previousInk)
		drawMenuRoundedOutline(canvas, next, next.Dy()/2, 2, nextInk)
		r.centeredText(canvas, (previous.Min.X+previous.Max.X)/2, 74, "‹", 28, true, previousInk)
		r.centeredText(canvas, (next.Min.X+next.Max.X)/2, 74, "›", 28, true, nextInk)
		pageText := fmt.Sprintf("%d–%d OF %d", start+1, end, len(participants))
		r.centeredText(canvas, width-190, 70, pageText, 12, true, color.RGBA{190, 202, 218, 255})
	} else {
		r.centeredText(canvas, width-190, 70, fmt.Sprintf("%d AVAILABLE", len(participants)), 12, true, color.RGBA{190, 202, 218, 255})
	}
	for visibleIndex, participant := range participants[start:end] {
		row := layout.participantBounds(visibleIndex)
		nameInk := color.RGBA{255, 255, 255, 255}
		statusInk := color.RGBA{49, 239, 116, 255}
		if participant.Busy {
			nameInk = color.RGBA{139, 148, 163, 255}
			statusInk = color.RGBA{255, 83, 67, 255}
		}
		drawMenuCard(canvas, row, max(18, row.Dy()/4), participant.Busy)
		avatarLimit, nameSize, subSize, capabilitySize := 26, 19, 12, 10
		if height >= 700 {
			avatarLimit, nameSize, subSize, capabilitySize = 42, 24, 15, 14
		}
		avatarRadius := min(avatarLimit, row.Dy()/2-9)
		avatarX, centerY := row.Min.X+avatarRadius+18, (row.Min.Y+row.Max.Y)/2
		avatar := image.Rect(avatarX-avatarRadius, centerY-avatarRadius, avatarX+avatarRadius, centerY+avatarRadius)
		drawMenuIconMedallion(canvas, avatar)
		drawSatChatNetworkGlyph(canvas, avatarX, centerY, max(9, avatarRadius/2), menuCyanInk)
		statusX := avatarX + avatarRadius + 20
		circle(canvas, statusX, centerY-8, 9, color.RGBA{statusInk.R, statusInk.G, statusInk.B, 40})
		circle(canvas, statusX, centerY-8, 6, statusInk)
		nameLeft := statusX + 20
		capabilityRight := row.Max.X - 44
		r.text(canvas, nameLeft, centerY-2, fitTextToWidth(firstNonEmpty(participant.Name, participant.Room), r.faces.bold[nameSize], max(100, capabilityRight-nameLeft-112)), nameSize, true, nameInk)
		label := "AUDIO ONLY"
		if participant.Video {
			label = "VIDEO + AUDIO"
		}
		if participant.Busy {
			label = "BUSY"
		}
		r.text(canvas, nameLeft, centerY+subSize+8, label, subSize, true, color.RGBA{166, 190, 219, 255})
		if !participant.Busy {
			iconInk := menuCyanInk
			micX := row.Max.X - max(54, row.Dy()/3)
			drawSatChatMicGlyph(canvas, micX, centerY-4, capabilitySize, iconInk)
			if participant.Video {
				drawSatChatVideoGlyph(canvas, micX-max(42, row.Dy()/3), centerY-4, capabilitySize, iconInk)
			}
			r.centeredText(canvas, row.Max.X-18, centerY+7, "›", 22, false, color.RGBA{220, 230, 243, 255})
		}
	}
}

func drawSatChatNetworkGlyph(canvas *image.RGBA, cx, cy, radius int, ink color.RGBA) {
	leftX, leftY := cx-radius, cy+radius/2
	rightX, rightY := cx+radius, cy+radius/2
	topX, topY := cx+radius/4, cy-radius
	line(canvas, leftX, leftY, topX, topY, max(2, radius/6), ink)
	line(canvas, topX, topY, rightX, rightY, max(2, radius/6), ink)
	line(canvas, rightX, rightY, leftX, leftY, max(2, radius/6), ink)
	nodeRadius := max(3, radius/4)
	circle(canvas, leftX, leftY, nodeRadius, ink)
	circle(canvas, rightX, rightY, nodeRadius, ink)
	circle(canvas, topX, topY, nodeRadius, ink)
}

func drawSatChatVideoGlyph(canvas *image.RGBA, cx, cy, size int, ink color.RGBA) {
	body := image.Rect(cx-size, cy-size*2/3, cx+size/2, cy+size*2/3)
	roundedRectOutline(canvas, body, max(2, size/4), max(1, size/5), ink)
	line(canvas, body.Max.X+2, cy-3, cx+size, cy-size*2/3, max(1, size/5), ink)
	line(canvas, cx+size, cy-size*2/3, cx+size, cy+size*2/3, max(1, size/5), ink)
	line(canvas, cx+size, cy+size*2/3, body.Max.X+2, cy+3, max(1, size/5), ink)
}

func drawSatChatMicGlyph(canvas *image.RGBA, cx, cy, size int, ink color.RGBA) {
	capsule := image.Rect(cx-size/3, cy-size, cx+size/3+1, cy+size/3)
	roundedRectOutline(canvas, capsule, size/3, max(1, size/5), ink)
	arc(canvas, cx, cy, size*2/3, 0, 180, max(1, size/5), ink)
	line(canvas, cx, cy+size*2/3, cx, cy+size, max(1, size/5), ink)
	line(canvas, cx-size/2, cy+size, cx+size/2, cy+size, max(1, size/5), ink)
}

func (r *renderer) handleSatChatTouch(client *socketClient, event linuxinput.TouchEvent, width, height int) {
	chat := r.state.SatChat
	if (chat == nil || chat.Call == nil) && image.Pt(event.X, event.Y).In(satChatCloseBounds(width)) {
		r.satChatOpen = false
		r.satChatPage = 0
		return
	}
	if chat != nil && chat.Call != nil {
		if image.Pt(event.X, event.Y).In(r.satChatEndBounds(width, height)) {
			client.sendSatChat("sat_chat.end", "", "", chat.Call.ID)
		}
		return
	}
	if chat == nil {
		return
	}
	pageSize := r.satChatPageSize(width, height)
	page := r.satChatPageFor(len(chat.Participants), pageSize)
	pages := satChatPageCount(len(chat.Participants), pageSize)
	point := image.Pt(event.X, event.Y)
	if pages > 1 && point.In(satChatPreviousBounds(width)) {
		if page > 0 {
			r.satChatPage--
		}
		return
	}
	if pages > 1 && point.In(satChatNextBounds(width)) {
		if page+1 < pages {
			r.satChatPage++
		}
		return
	}
	layout := satChatDirectoryLayoutFor(width, height)
	for visibleIndex := 0; visibleIndex < pageSize; visibleIndex++ {
		if !point.In(layout.participantBounds(visibleIndex)) {
			continue
		}
		index := page*pageSize + visibleIndex
		if index >= len(chat.Participants) {
			return
		}
		participant := chat.Participants[index]
		if !participant.Busy {
			mode := "audio"
			if participant.Video {
				mode = "video"
			}
			client.sendSatChat("sat_chat.start", participant.ID, mode, "")
		}
		return
	}
}

func (r *renderer) queueNotificationImage(ctx context.Context, results chan<- screenImageResult) {
	if r.state.Notification == nil || r.state.Notification.ImageURL == "" {
		r.notificationID, r.notificationImage = "", nil
		return
	}
	identity := notificationImageIdentity(r.state.Notification)
	if r.notificationID == identity {
		return
	}
	r.notificationID, r.notificationImage = identity, nil
	queueScreenImage(ctx, results, notificationScreenImage, r.notificationID, r.state.Notification.ImageURL)
}

func (r *renderer) queueLastEventImage(ctx context.Context, results chan<- screenImageResult) {
	if r.state.LastEvent == nil || r.state.LastEvent.ImageURL == "" {
		r.lastEventID, r.lastEventImage = "", nil
		return
	}
	identity := notificationImageIdentity(r.state.LastEvent)
	if r.lastEventID == identity {
		return
	}
	r.lastEventID, r.lastEventImage = identity, nil
	queueScreenImage(ctx, results, lastEventScreenImage, r.lastEventID, r.state.LastEvent.ImageURL)
}

func notificationImageIdentity(item *show.Notification) string {
	if item == nil {
		return ""
	}
	return strings.TrimSpace(item.ID) + "|" + strings.TrimSpace(item.ImageURL)
}

func (r *renderer) queueMediaImage(ctx context.Context, results chan<- screenImageResult) {
	if r.state.Media == nil || r.state.Media.ArtworkURL == "" {
		r.mediaID, r.mediaImage = "", nil
		return
	}
	identity := mediaIdentity(r.state.Media)
	if r.mediaID == identity {
		return
	}
	r.mediaID, r.mediaImage = identity, nil
	queueScreenImage(ctx, results, mediaScreenImage, r.mediaID, r.state.Media.ArtworkURL)
}

func wallpaperRotationIndex(local time.Time, count, intervalMinutes int) int {
	if count <= 1 || intervalMinutes == 0 {
		return 0
	}
	switch intervalMinutes {
	case 60, 360, 720, 1440:
	default:
		intervalMinutes = 1440
	}
	// Build the slot from local wall-clock minutes so each preset changes at a
	// predictable local boundary. A 366-day year stride keeps the sequence
	// moving across New Year's Day without requiring persistent renderer state.
	wallMinutes := (int64(local.Year())*366+int64(local.YearDay()-1))*1440 +
		int64(local.Hour()*60+local.Minute())
	return int((wallMinutes / int64(intervalMinutes)) % int64(count))
}

func (r *renderer) queueWallpaperImage(ctx context.Context, results chan<- screenImageResult, now time.Time) {
	urls := r.state.WallpaperURLs
	if len(urls) == 0 {
		r.wallpaperID, r.wallpaperImage = "", nil
		return
	}
	identity := strings.TrimSpace(urls[wallpaperRotationIndex(
		r.localTime(now),
		len(urls),
		r.state.WallpaperIntervalMinutes,
	)])
	if identity == "" || r.wallpaperID == identity {
		return
	}
	r.wallpaperID, r.wallpaperImage = identity, nil
	go func() {
		loaded := loadWallpaperImage(ctx, identity)
		loaded = prepareCoverImage(loaded, image.Pt(r.screenWidth, r.screenHeight))
		result := screenImageResult{kind: wallpaperScreenImage, identity: identity, image: loaded}
		select {
		case results <- result:
		case <-ctx.Done():
		}
	}()
}

func (r *renderer) queueSatChatVideoImage(requests chan screenImageRequest) {
	if r.state.SatChat == nil || r.state.SatChat.VideoFrameURL == "" {
		r.satChatVideoID, r.satChatVideoShown, r.satChatVideoDrawn, r.satChatVideoImage = "", "", "", nil
		return
	}
	identity := r.state.SatChat.VideoFrameURL
	if r.satChatVideoID == identity {
		return
	}
	r.satChatVideoID = identity
	callID := ""
	if r.state.SatChat.Call != nil {
		callID = r.state.SatChat.Call.ID
	}
	queueLatestScreenImageRequest(requests, screenImageRequest{
		kind: satChatVideoScreenImage, identity: identity, session: callID, rawURL: identity,
		size: r.satChatVideoBounds().Size(),
	})
}

func (r *renderer) queueCameraVideoImage(requests chan screenImageRequest) {
	if r.state.Cameras == nil || r.state.Cameras.SelectedID == "" {
		r.cameraFrameID, r.cameraFrameShown, r.cameraFrameImage = "", "", nil
		return
	}
	if r.cameraStreamShown && r.state.Cameras.StreamURL != "" {
		return
	}
	if r.state.Cameras.FrameURL == "" {
		r.cameraFrameID, r.cameraFrameShown, r.cameraFrameImage = "", "", nil
		return
	}
	identity := r.state.Cameras.FrameURL
	if r.cameraFrameID == identity {
		return
	}
	r.cameraFrameID = identity
	queueLatestScreenImageRequest(requests, screenImageRequest{
		kind: cameraVideoScreenImage, identity: identity, session: r.state.Cameras.SelectedID,
		rawURL: identity, size: r.cameraVideoBounds().Size(),
	})
}

// queueLatestScreenImageRequest bounds live-video decoding to one pending
// picture. The old path launched a decoder for every state update and then
// rejected the completed picture whenever a newer URL had arrived first. On
// the slower Show CPUs that could reject an otherwise healthy continuous
// stream for seconds at a time. A single worker now finishes one decode and
// immediately moves to the newest waiting frame.
func queueLatestScreenImageRequest(requests chan screenImageRequest, request screenImageRequest) {
	select {
	case requests <- request:
		return
	default:
	}
	select {
	case <-requests:
	default:
	}
	select {
	case requests <- request:
	default:
	}
}

func runLatestScreenImageLoader(ctx context.Context, requests <-chan screenImageRequest, results chan<- screenImageResult) {
	for {
		select {
		case <-ctx.Done():
			return
		case request := <-requests:
			loadStarted := time.Now()
			loaded := loadLoopbackImage(ctx, request.rawURL)
			loadDuration := time.Since(loadStarted)
			var scaleDuration time.Duration
			if request.kind == satChatVideoScreenImage || request.kind == cameraVideoScreenImage {
				scaleStarted := time.Now()
				if request.kind == satChatVideoScreenImage {
					loaded = prepareCoverImage(loaded, request.size)
				} else {
					loaded = prepareContainImage(loaded, request.size)
				}
				scaleDuration = time.Since(scaleStarted)
			}
			result := screenImageResult{
				kind: request.kind, identity: request.identity, session: request.session,
				image: loaded, loadDuration: loadDuration, scaleDuration: scaleDuration,
			}
			select {
			case results <- result:
			case <-ctx.Done():
				return
			}
		}
	}
}

func queueScreenImage(ctx context.Context, results chan<- screenImageResult, kind screenImageKind, identity, rawURL string) {
	go func() {
		var loaded image.Image
		for attempt := 0; attempt < 3 && loaded == nil; attempt++ {
			loaded = loadLoopbackImage(ctx, rawURL)
			if loaded != nil || attempt == 2 {
				break
			}
			delay := time.Duration(attempt+1) * 75 * time.Millisecond
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
		}
		result := screenImageResult{kind: kind, identity: identity, image: loaded}
		select {
		case results <- result:
		case <-ctx.Done():
		}
	}()
}

func (r *renderer) applyScreenImage(result screenImageResult) {
	switch result.kind {
	case notificationScreenImage:
		if r.notificationID == result.identity {
			r.notificationImage = result.image
			if result.image == nil {
				r.notificationID = ""
			} else {
				r.invalidateStaticScreenCaches()
			}
		}
	case lastEventScreenImage:
		if r.lastEventID == result.identity {
			r.lastEventImage = result.image
			if result.image == nil {
				r.lastEventID = ""
			} else {
				r.invalidateStaticScreenCaches()
			}
		}
	case mediaScreenImage:
		if r.mediaID == result.identity {
			r.mediaImage = result.image
			if result.image != nil {
				r.invalidateStaticScreenCaches()
			}
		}
	case wallpaperScreenImage:
		if r.wallpaperID == result.identity {
			r.wallpaperImage = result.image
			r.invalidateStaticScreenCaches()
		}
	case satChatVideoScreenImage:
		// The dedicated video loader is sequential, so its completed images
		// cannot arrive out of order. Accept every decode belonging to the
		// current call even if a newer URL was queued while this JPEG was being
		// decoded. Requiring exact URL equality here caused slower devices to
		// discard virtually the entire continuous stream.
		if result.image != nil && r.state.SatChat != nil && r.state.SatChat.Call != nil &&
			result.session != "" && result.session == r.state.SatChat.Call.ID {
			r.satChatVideoImage = result.image
			r.satChatVideoShown = result.identity
		}
	case cameraVideoScreenImage:
		if result.image != nil && !r.cameraStreamShown && r.state.Cameras != nil && r.cameraOpen &&
			result.session != "" && result.session == r.state.Cameras.SelectedID {
			r.cameraFrameImage = result.image
			r.cameraFrameShown = result.identity
		}
	}
}

func (r *renderer) invalidateStaticScreenCaches() {
	r.checkersBaseKey, r.crownBaseKey, r.spotFrameBaseKey = "", "", ""
	r.checkersCanvas, r.crownCanvas, r.spotFrameCanvas = nil, nil, nil
}

func loadLoopbackImage(ctx context.Context, rawURL string) image.Image {
	return loadHTTPImage(ctx, rawURL, false, 4*1024*1024)
}

func loadWallpaperImage(ctx context.Context, rawURL string) image.Image {
	return loadHTTPImage(ctx, rawURL, true, 12*1024*1024)
}

func loadHTTPImage(ctx context.Context, rawURL string, allowRemote bool, maximumBytes int64) image.Image {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil
	}
	if !allowRemote && (parsed.Scheme != "http" || (parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost")) {
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil
	}
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil
	}
	contents, err := io.ReadAll(io.LimitReader(response.Body, maximumBytes+1))
	if err != nil || int64(len(contents)) > maximumBytes {
		return nil
	}
	decoded, _, err := image.Decode(bytes.NewReader(contents))
	if err != nil {
		return nil
	}
	return decoded
}

func fillGradient(destination *image.RGBA, top, bottom color.RGBA) {
	height := max(destination.Rect.Dy()-1, 1)
	for y := 0; y < destination.Rect.Dy(); y++ {
		ratio := float64(y) / float64(height)
		lineColor := mix(top, bottom, ratio)
		row := destination.Pix[y*destination.Stride : y*destination.Stride+destination.Rect.Dx()*4]
		row[0], row[1], row[2], row[3] = lineColor.R, lineColor.G, lineColor.B, 255
		for filled := 4; filled < len(row); {
			filled += copy(row[filled:], row[:filled])
		}
	}
}

func circle(destination *image.RGBA, cx, cy, radius int, value color.RGBA) {
	if radius <= 0 {
		return
	}
	squared := radius * radius
	for y := max(0, cy-radius); y <= min(destination.Rect.Dy()-1, cy+radius); y++ {
		for x := max(0, cx-radius); x <= min(destination.Rect.Dx()-1, cx+radius); x++ {
			if (x-cx)*(x-cx)+(y-cy)*(y-cy) <= squared {
				blendPixel(destination, x, y, value)
			}
		}
	}
}

func circleOutline(destination *image.RGBA, cx, cy, radius, thickness int, value color.RGBA) {
	if radius <= 0 || thickness <= 0 {
		return
	}
	outer, inner := radius*radius, (radius-thickness)*(radius-thickness)
	for y := max(0, cy-radius); y <= min(destination.Rect.Dy()-1, cy+radius); y++ {
		for x := max(0, cx-radius); x <= min(destination.Rect.Dx()-1, cx+radius); x++ {
			distance := (x-cx)*(x-cx) + (y-cy)*(y-cy)
			if distance <= outer && distance >= inner {
				blendPixel(destination, x, y, value)
			}
		}
	}
}

func line(destination *image.RGBA, x0, y0, x1, y1, thickness int, value color.RGBA) {
	dx := math.Abs(float64(x1 - x0))
	dy := -math.Abs(float64(y1 - y0))
	sx, sy := -1, -1
	if x0 < x1 {
		sx = 1
	}
	if y0 < y1 {
		sy = 1
	}
	err := int(dx + dy)
	radius := max(1, thickness/2)
	for {
		circle(destination, x0, y0, radius, value)
		if x0 == x1 && y0 == y1 {
			break
		}
		twice := 2 * err
		if twice >= int(dy) {
			err += int(dy)
			x0 += sx
		}
		if twice <= int(dx) {
			err += int(dx)
			y0 += sy
		}
	}
}

func arc(destination *image.RGBA, cx, cy, radius int, startDegrees, sweepDegrees float64, thickness int, value color.RGBA) {
	if radius <= 0 || sweepDegrees == 0 {
		return
	}
	steps := max(2, int(math.Abs(sweepDegrees)*float64(radius)/90))
	previousX, previousY := 0, 0
	for step := 0; step <= steps; step++ {
		angle := (startDegrees + sweepDegrees*float64(step)/float64(steps)) * math.Pi / 180
		x := cx + int(math.Cos(angle)*float64(radius))
		y := cy + int(math.Sin(angle)*float64(radius))
		if step > 0 {
			line(destination, previousX, previousY, x, y, thickness, value)
		}
		previousX, previousY = x, y
	}
}

func roundedRect(destination *image.RGBA, rectangle image.Rectangle, radius int, value color.RGBA) {
	radius = min(max(radius, 0), min(rectangle.Dx(), rectangle.Dy())/2)
	// Color values throughout this renderer are straight-alpha RGBA. Convert to
	// NRGBA before compositing, and paint each row once so translucent controls
	// do not acquire a darker cross where two fill rectangles overlap.
	ink := image.NewUniform(color.NRGBA{R: value.R, G: value.G, B: value.B, A: value.A})
	for y := max(rectangle.Min.Y, destination.Rect.Min.Y); y < min(rectangle.Max.Y, destination.Rect.Max.Y); y++ {
		inset := 0
		fromEdge := min(y-rectangle.Min.Y, rectangle.Max.Y-1-y)
		if fromEdge < radius {
			vertical := float64(radius-fromEdge) - .5
			inset = int(math.Ceil(float64(radius) - math.Sqrt(float64(radius*radius)-vertical*vertical)))
		}
		row := image.Rect(rectangle.Min.X+inset, y, rectangle.Max.X-inset, y+1)
		draw.Draw(destination, row, ink, image.Point{}, draw.Over)
	}
}

func roundedRectOutline(destination *image.RGBA, rectangle image.Rectangle, radius, thickness int, value color.RGBA) {
	radius = min(radius, min(rectangle.Dx(), rectangle.Dy())/2)
	line(destination, rectangle.Min.X+radius, rectangle.Min.Y, rectangle.Max.X-radius, rectangle.Min.Y, thickness, value)
	line(destination, rectangle.Min.X+radius, rectangle.Max.Y-1, rectangle.Max.X-radius, rectangle.Max.Y-1, thickness, value)
	line(destination, rectangle.Min.X, rectangle.Min.Y+radius, rectangle.Min.X, rectangle.Max.Y-radius, thickness, value)
	line(destination, rectangle.Max.X-1, rectangle.Min.Y+radius, rectangle.Max.X-1, rectangle.Max.Y-radius, thickness, value)
	arc(destination, rectangle.Min.X+radius, rectangle.Min.Y+radius, radius, 180, 90, thickness, value)
	arc(destination, rectangle.Max.X-radius-1, rectangle.Min.Y+radius, radius, 270, 90, thickness, value)
	arc(destination, rectangle.Max.X-radius-1, rectangle.Max.Y-radius-1, radius, 0, 90, thickness, value)
	arc(destination, rectangle.Min.X+radius, rectangle.Max.Y-radius-1, radius, 90, 90, thickness, value)
}

func drawCover(destination *image.RGBA, rectangle image.Rectangle, source image.Image) {
	if source == nil {
		return
	}
	from := source.Bounds()
	scale := math.Max(float64(rectangle.Dx())/float64(from.Dx()), float64(rectangle.Dy())/float64(from.Dy()))
	width, height := int(float64(from.Dx())*scale), int(float64(from.Dy())*scale)
	for y := 0; y < rectangle.Dy(); y++ {
		sy := from.Min.Y + (y+(height-rectangle.Dy())/2)*from.Dy()/height
		for x := 0; x < rectangle.Dx(); x++ {
			sx := from.Min.X + (x+(width-rectangle.Dx())/2)*from.Dx()/width
			destination.Set(rectangle.Min.X+x, rectangle.Min.Y+y, source.At(sx, sy))
		}
	}
}

// prepareCoverImage performs the expensive camera scale/crop exactly once per
// decoded JPEG. The normal renderer can then copy this display-sized RGBA with
// draw.Draw instead of sampling hundreds of thousands of source pixels on
// every UI tick.
func prepareCoverImage(source image.Image, size image.Point) image.Image {
	if source == nil || size.X <= 0 || size.Y <= 0 || source.Bounds().Empty() {
		return source
	}
	from := source.Bounds()
	crop := from
	if from.Dx()*size.Y > from.Dy()*size.X {
		cropWidth := max(1, from.Dy()*size.X/size.Y)
		crop.Min.X = from.Min.X + (from.Dx()-cropWidth)/2
		crop.Max.X = crop.Min.X + cropWidth
	} else if from.Dx()*size.Y < from.Dy()*size.X {
		cropHeight := max(1, from.Dx()*size.Y/size.X)
		crop.Min.Y = from.Min.Y + (from.Dy()-cropHeight)/2
		crop.Max.Y = crop.Min.Y + cropHeight
	}
	// JPEG decoding yields YCbCr. Scaling that image directly makes the generic
	// scaler perform the YCbCr-to-RGB conversion once per destination pixel --
	// more than 900,000 conversions for Crown even though echo-low contains only
	// 90,000 source pixels. Convert the cropped source once, then expand RGBA.
	// When upscaling, repeated source rows become a single contiguous copy.
	var converted *image.RGBA
	if rgba, ok := source.(*image.RGBA); ok {
		converted = rgba.SubImage(crop).(*image.RGBA)
	} else {
		converted = image.NewRGBA(image.Rect(0, 0, crop.Dx(), crop.Dy()))
		draw.Draw(converted, converted.Bounds(), source, crop.Min, draw.Src)
	}
	destination := image.NewRGBA(image.Rect(0, 0, size.X, size.Y))
	scaleNearestRGBA(destination, converted)
	return destination
}

func prepareContainImage(source image.Image, size image.Point) image.Image {
	if source == nil || size.X <= 0 || size.Y <= 0 || source.Bounds().Empty() {
		return source
	}
	from := source.Bounds()
	width, height := size.X, size.Y
	if from.Dx()*size.Y > from.Dy()*size.X {
		height = max(1, from.Dy()*size.X/from.Dx())
	} else {
		width = max(1, from.Dx()*size.Y/from.Dy())
	}
	converted := image.NewRGBA(image.Rect(0, 0, from.Dx(), from.Dy()))
	draw.Draw(converted, converted.Bounds(), source, from.Min, draw.Src)
	scaled := image.NewRGBA(image.Rect(0, 0, width, height))
	scaleNearestRGBA(scaled, converted)
	destination := image.NewRGBA(image.Rect(0, 0, size.X, size.Y))
	draw.Draw(destination, image.Rect((size.X-width)/2, (size.Y-height)/2, (size.X+width)/2, (size.Y+height)/2), scaled, image.Point{}, draw.Src)
	return destination
}

func scaleNearestRGBA(destination, source *image.RGBA) {
	if destination == nil || source == nil || destination.Bounds().Empty() || source.Bounds().Empty() {
		return
	}
	destinationBounds := destination.Bounds()
	sourceBounds := source.Bounds()
	xMap := make([]int, destinationBounds.Dx())
	for x := range xMap {
		xMap[x] = sourceBounds.Min.X + x*sourceBounds.Dx()/destinationBounds.Dx()
	}
	lastSourceY := -1
	lastDestinationStart := 0
	rowBytes := destinationBounds.Dx() * 4
	for y := 0; y < destinationBounds.Dy(); y++ {
		sourceY := sourceBounds.Min.Y + y*sourceBounds.Dy()/destinationBounds.Dy()
		destinationStart := destination.PixOffset(destinationBounds.Min.X, destinationBounds.Min.Y+y)
		if sourceY == lastSourceY {
			copy(destination.Pix[destinationStart:destinationStart+rowBytes], destination.Pix[lastDestinationStart:lastDestinationStart+rowBytes])
			continue
		}
		for x, sourceX := range xMap {
			sourceOffset := source.PixOffset(sourceX, sourceY)
			destinationOffset := destinationStart + x*4
			destination.Pix[destinationOffset+0] = source.Pix[sourceOffset+0]
			destination.Pix[destinationOffset+1] = source.Pix[sourceOffset+1]
			destination.Pix[destinationOffset+2] = source.Pix[sourceOffset+2]
			destination.Pix[destinationOffset+3] = source.Pix[sourceOffset+3]
		}
		lastSourceY = sourceY
		lastDestinationStart = destinationStart
	}
}

func drawPreparedCover(destination *image.RGBA, rectangle image.Rectangle, source image.Image) {
	if source == nil {
		return
	}
	if bounds := source.Bounds(); bounds.Dx() == rectangle.Dx() && bounds.Dy() == rectangle.Dy() {
		draw.Draw(destination, rectangle, source, bounds.Min, draw.Src)
		return
	}
	drawCover(destination, rectangle, source)
}

func drawContain(destination *image.RGBA, rectangle image.Rectangle, source image.Image) {
	if source == nil || rectangle.Empty() {
		return
	}
	from := source.Bounds()
	if from.Empty() {
		return
	}
	scale := math.Min(float64(rectangle.Dx())/float64(from.Dx()), float64(rectangle.Dy())/float64(from.Dy()))
	width := max(1, int(math.Round(float64(from.Dx())*scale)))
	height := max(1, int(math.Round(float64(from.Dy())*scale)))
	left := rectangle.Min.X + (rectangle.Dx()-width)/2
	top := rectangle.Min.Y + (rectangle.Dy()-height)/2
	for y := 0; y < height; y++ {
		sy := from.Min.Y + y*from.Dy()/height
		for x := 0; x < width; x++ {
			sx := from.Min.X + x*from.Dx()/width
			pixel := color.NRGBAModel.Convert(source.At(sx, sy)).(color.NRGBA)
			blendPixel(destination, left+x, top+y, color.RGBA{R: pixel.R, G: pixel.G, B: pixel.B, A: pixel.A})
		}
	}
}

func blendPixel(destination *image.RGBA, x, y int, source color.RGBA) {
	index := y*destination.Stride + x*4
	pixel := destination.Pix[index : index+4]
	if source.A == 255 {
		pixel[0], pixel[1], pixel[2], pixel[3] = source.R, source.G, source.B, 255
		return
	}
	alpha := uint32(source.A)
	inverse := uint32(255 - source.A)
	pixel[0] = uint8((uint32(source.R)*alpha + uint32(pixel[0])*inverse) / 255)
	pixel[1] = uint8((uint32(source.G)*alpha + uint32(pixel[1])*inverse) / 255)
	pixel[2] = uint8((uint32(source.B)*alpha + uint32(pixel[2])*inverse) / 255)
	pixel[3] = 255
}

func mix(left, right color.RGBA, amount float64) color.RGBA {
	amount = math.Max(0, math.Min(1, amount))
	return color.RGBA{uint8(float64(left.R)*(1-amount) + float64(right.R)*amount), uint8(float64(left.G)*(1-amount) + float64(right.G)*amount), uint8(float64(left.B)*(1-amount) + float64(right.B)*amount), 255}
}

func accentFor(phase string) color.RGBA {
	return accentForTheme(phase, "tater")
}

type displayPalette struct {
	primary   color.RGBA
	listening color.RGBA
	thinking  color.RGBA
	intercom  color.RGBA
	music     color.RGBA
}

func paletteForTheme(theme string) displayPalette {
	switch strings.ToLower(strings.TrimSpace(theme)) {
	case "ocean":
		return displayPalette{
			primary: color.RGBA{62, 203, 255, 255}, listening: color.RGBA{73, 230, 194, 255},
			thinking: color.RGBA{102, 134, 255, 255}, intercom: color.RGBA{127, 224, 255, 255}, music: color.RGBA{73, 230, 194, 255},
		}
	case "violet":
		return displayPalette{
			primary: color.RGBA{176, 124, 255, 255}, listening: color.RGBA{239, 141, 255, 255},
			thinking: color.RGBA{117, 140, 255, 255}, intercom: color.RGBA{255, 181, 220, 255}, music: color.RGBA{141, 190, 255, 255},
		}
	case "forest":
		return displayPalette{
			primary: color.RGBA{117, 214, 110, 255}, listening: color.RGBA{85, 230, 183, 255},
			thinking: color.RGBA{212, 185, 95, 255}, intercom: color.RGBA{201, 242, 124, 255}, music: color.RGBA{92, 204, 180, 255},
		}
	case "sunset":
		return displayPalette{
			primary: color.RGBA{255, 111, 97, 255}, listening: color.RGBA{255, 173, 85, 255},
			thinking: color.RGBA{198, 116, 255, 255}, intercom: color.RGBA{255, 208, 106, 255}, music: color.RGBA{255, 145, 159, 255},
		}
	default:
		return displayPalette{
			primary: color.RGBA{255, 132, 48, 255}, listening: color.RGBA{52, 226, 183, 255},
			thinking: color.RGBA{154, 112, 255, 255}, intercom: color.RGBA{255, 105, 140, 255}, music: color.RGBA{86, 206, 255, 255},
		}
	}
}

func accentForTheme(phase, theme string) color.RGBA {
	palette := paletteForTheme(theme)
	switch phase {
	case "listening":
		return palette.listening
	case "thinking":
		return palette.thinking
	case "tool_call", "setup":
		return palette.primary
	case "speaking":
		return palette.primary
	case "intercom":
		return palette.intercom
	case "music":
		return palette.music
	case "error":
		return color.RGBA{255, 94, 94, 255}
	default:
		return palette.primary
	}
}

func phaseLabel(phase string) string {
	switch phase {
	case "thinking":
		return "THINKING"
	case "tool_call":
		return "WORKING"
	case "music":
		return "PLAYING"
	case "error":
		return "CHECK"
	case "offline":
		return "OFFLINE"
	default:
		return "TATER"
	}
}

func weatherColor(kind string) color.RGBA {
	kind = strings.ToLower(kind)
	switch {
	case strings.Contains(kind, "moon"), strings.Contains(kind, "night"):
		return color.RGBA{144, 183, 255, 255}
	case strings.Contains(kind, "storm"), strings.Contains(kind, "lightning"):
		return color.RGBA{165, 126, 255, 255}
	case strings.Contains(kind, "rain"), strings.Contains(kind, "drizzle"):
		return color.RGBA{83, 156, 255, 255}
	case strings.Contains(kind, "snow"):
		return color.RGBA{194, 229, 255, 255}
	case strings.Contains(kind, "cloud"), strings.Contains(kind, "overcast"):
		return color.RGBA{166, 184, 207, 255}
	default:
		return color.RGBA{255, 194, 76, 255}
	}
}

func titleWords(value string) string {
	words := strings.Fields(strings.NewReplacer("_", " ", "-", " ").Replace(strings.TrimSpace(value)))
	for index := range words {
		if words[index] != "" {
			words[index] = strings.ToUpper(words[index][:1]) + words[index][1:]
		}
	}
	return strings.Join(words, " ")
}

func ellipsize(value string, limit int) string {
	value = strings.TrimSpace(value)
	characters := []rune(value)
	if len(characters) <= limit {
		return value
	}
	return strings.TrimSpace(string(characters[:limit-1])) + "…"
}

func fitTextToWidth(value string, face font.Face, width int) string {
	value = strings.TrimSpace(value)
	if font.MeasureString(face, value).Round() <= width {
		return value
	}
	characters := []rune(value)
	low, high := 0, len(characters)
	for low < high {
		middle := (low + high + 1) / 2
		candidate := strings.TrimSpace(string(characters[:middle])) + "…"
		if font.MeasureString(face, candidate).Round() <= width {
			low = middle
		} else {
			high = middle - 1
		}
	}
	if low == 0 {
		return "…"
	}
	return strings.TrimSpace(string(characters[:low])) + "…"
}

func fittingTextSize(value string, faces map[int]font.Face, preferred, minimum, width int) int {
	best := minimum
	for size, face := range faces {
		if size < minimum || size > preferred || face == nil {
			continue
		}
		if font.MeasureString(face, strings.TrimSpace(value)).Round() <= width && size > best {
			best = size
		}
	}
	return best
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func trimFeelsLike(value string) string {
	value = strings.TrimSpace(value)
	const prefix = "feels like "
	if len(value) >= len(prefix) && strings.EqualFold(value[:len(prefix)], prefix) {
		return strings.TrimSpace(value[len(prefix):])
	}
	return value
}

func formatDuration(duration time.Duration) string {
	total := int64(math.Ceil(duration.Seconds()))
	if total < 0 {
		total = 0
	}
	if total >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", total/3600, total%3600/60, total%60)
	}
	return fmt.Sprintf("%02d:%02d", total/60, total%60)
}

func timerRemaining(timer *show.Timer, now time.Time) time.Duration {
	if timer == nil {
		return 0
	}
	remaining := time.Duration(timer.RemainingMS) * time.Millisecond
	if timer.DeadlineUnixMS > 0 && !timer.Ringing {
		remaining = time.UnixMilli(timer.DeadlineUnixMS).Sub(now)
	}
	if remaining < 0 {
		return 0
	}
	return remaining
}

func timerProgress(timer *show.Timer, now time.Time) float64 {
	if timer == nil || timer.Ringing || timer.OriginalDurationMS <= 0 {
		return 0
	}
	progress := float64(timerRemaining(timer, now).Milliseconds()) / float64(timer.OriginalDurationMS)
	return math.Max(0, math.Min(1, progress))
}

func notificationIdentity(notification *show.Notification) string {
	if notification == nil {
		return ""
	}
	return notification.ID
}

func timerIdentity(timer *show.Timer) string {
	if timer == nil {
		return ""
	}
	return fmt.Sprintf("%s:%t", timer.ID, timer.Ringing)
}

func mediaIdentity(media *show.Media) string {
	if media == nil || !media.Active {
		return ""
	}
	return strings.Join([]string{media.ArtworkURL, media.Title, media.Artist, media.Album}, "\x00")
}

func inCircle(x, y, centerX, centerY, radius int) bool {
	return (x-centerX)*(x-centerX)+(y-centerY)*(y-centerY) <= radius*radius
}
