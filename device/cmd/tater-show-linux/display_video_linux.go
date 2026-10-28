//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/linuxinput"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/videostream"
)

type displayVideoResult struct {
	identity   string
	sessionID  string
	positionMS int64
	sequence   uint64
	image      *image.RGBA
	ended      bool
	err        error
}

type displayVideoController struct {
	identity   string
	sessionID  string
	cancel     context.CancelFunc
	frames     chan displayVideoResult
	recycle    chan *image.RGBA
	generation atomic.Uint64
}

func newDisplayVideoController() *displayVideoController {
	return &displayVideoController{
		frames: make(chan displayVideoResult, 1), recycle: make(chan *image.RGBA, 3),
	}
}

func displayVideoIdentity(video *show.Video, size image.Point) string {
	if video == nil || !video.Active || video.SessionID == "" || video.URL == "" ||
		!strings.EqualFold(video.State, "playing") || size.X <= 0 || size.Y <= 0 {
		return ""
	}
	fps := video.FPS
	if fps != 24 && fps != 30 {
		fps = 30
	}
	return fmt.Sprintf("%s\x00%s\x00%d\x00%d\x00%dx%d", video.SessionID, video.URL, video.PositionMS, fps, size.X&^1, size.Y&^1)
}

func displayVideoServerSeekURL(raw string, positionMS int64) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	query := parsed.Query()
	if positionMS > 0 {
		query.Set("start", strconv.FormatFloat(float64(positionMS)/1000, 'f', 3, 64))
	} else {
		query.Del("start")
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

// Update starts, pauses, replaces, or stops playback. Pausing intentionally
// retains the most recent frame so the controls appear over the exact moment
// the user touched.
func (controller *displayVideoController) Update(parent context.Context, video *show.Video, size image.Point) (reset bool) {
	identity := displayVideoIdentity(video, size)
	sessionID := ""
	if video != nil && video.Active {
		sessionID = video.SessionID
	}
	if identity == controller.identity && sessionID == controller.sessionID {
		return false
	}
	oldSession := controller.sessionID
	if controller.cancel != nil {
		controller.cancel()
		controller.cancel = nil
	}
	controller.identity = identity
	controller.sessionID = sessionID
	reset = oldSession != sessionID
	if identity == "" {
		return reset
	}
	width, height := size.X&^1, size.Y&^1
	ctx, cancel := context.WithCancel(parent)
	controller.cancel = cancel
	request := videostream.MediaRequest{
		URL: video.URL, Width: width, Height: height, FPS: video.FPS,
		StartPositionMS: video.PositionMS,
	}
	positionBaseMS := video.PositionMS
	if video.ServerSeek && !video.Live {
		request.URL = displayVideoServerSeekURL(request.URL, video.PositionMS)
		request.StartPositionMS = 0
	}
	if video.Live {
		// Live sources resume at their current edge; seeking to the elapsed UI
		// position is meaningful only for VOD and breaks many RTSP/HLS inputs.
		request.StartPositionMS = 0
	}
	streamID := fmt.Sprintf("%s-%d", video.SessionID, controller.generation.Add(1))
	go controller.run(ctx, identity, video.SessionID, streamID, request, positionBaseMS, video.Loop, video.ServerSeek)
	return reset
}

func (controller *displayVideoController) Active() bool {
	return controller != nil && controller.identity != ""
}

func (controller *displayVideoController) run(ctx context.Context, identity, sessionID, streamID string, request videostream.MediaRequest, positionBaseMS int64, loop, serverSeek bool) {
	parsed, _ := videostream.CheckURL(request.URL)
	log.Printf("[display-video] opening %s with %s at %dx%d/%dfps", videostream.RedactedURL(parsed), videostream.DecoderPath(), request.Width, request.Height, request.FPS)
	for ctx.Err() == nil {
		audioReader, audioWriter := io.Pipe()
		audioDone := make(chan error, 1)
		go func() { audioDone <- postDisplayVideoAudio(ctx, sessionID, streamID, audioReader) }()
		var sequence uint64
		lastPositionMS := positionBaseMS
		err := videostream.ReadMedia(ctx, request, audioWriter, func(source *image.RGBA) error {
			sequence++
			destination := controller.acquire(source.Rect)
			copy(destination.Pix, source.Pix)
			position := positionBaseMS + int64(sequence)*1000/int64(max(request.FPS, 1))
			lastPositionMS = position
			controller.publish(displayVideoResult{
				identity: identity, sessionID: sessionID, positionMS: position,
				sequence: sequence, image: destination,
			})
			return nil
		})
		_ = audioWriter.Close()
		audioErr := <-audioDone
		if ctx.Err() != nil {
			return
		}
		if err != nil && sequence == 0 && !request.VideoOnly {
			// FFmpeg cannot create an optional audio-only output when the input
			// is genuinely silent. Retry once without fd 3; ordinary decoder or
			// network failures simply fail the second bounded attempt normally.
			request.VideoOnly = true
			streamID = fmt.Sprintf("%s-%d", sessionID, controller.generation.Add(1))
			continue
		}
		if err == nil || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			if loop {
				request.StartPositionMS = 0
				positionBaseMS = 0
				if serverSeek {
					request.URL = displayVideoServerSeekURL(request.URL, 0)
				}
				streamID = fmt.Sprintf("%s-%d", sessionID, controller.generation.Add(1))
				continue
			}
			controller.publish(displayVideoResult{identity: identity, sessionID: sessionID, positionMS: lastPositionMS, ended: true})
			return
		}
		if audioErr != nil && !errors.Is(audioErr, context.Canceled) {
			log.Printf("[display-video] audio path stopped: %v", audioErr)
		}
		log.Printf("[display-video] playback stopped: %v", err)
		controller.publish(displayVideoResult{identity: identity, sessionID: sessionID, err: errors.New("video playback stopped")})
		return
	}
}

func postDisplayVideoAudio(ctx context.Context, sessionID, streamID string, body io.Reader) error {
	endpoint := "http://" + show.DefaultImageAddress + "/video-audio?session=" + url.QueryEscape(sessionID) + "&stream=" + url.QueryEscape(streamID)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "audio/L16;rate=48000;channels=1")
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("loopback audio endpoint returned %s", response.Status)
	}
	return nil
}

func (controller *displayVideoController) acquire(bounds image.Rectangle) *image.RGBA {
	select {
	case frame := <-controller.recycle:
		if frame != nil && frame.Rect == bounds {
			return frame
		}
	default:
	}
	return image.NewRGBA(bounds)
}

func (controller *displayVideoController) publish(result displayVideoResult) {
	select {
	case controller.frames <- result:
		return
	default:
	}
	select {
	case old := <-controller.frames:
		controller.Release(old.image)
	default:
	}
	select {
	case controller.frames <- result:
	default:
		controller.Release(result.image)
	}
}

func (controller *displayVideoController) Release(frame *image.RGBA) {
	if frame == nil {
		return
	}
	select {
	case controller.recycle <- frame:
	default:
	}
}

func (controller *displayVideoController) Close() {
	if controller.cancel != nil {
		controller.cancel()
	}
	controller.identity, controller.sessionID = "", ""
}

func (r *renderer) displayVideoVisible() bool {
	return r.state.Video != nil && r.state.Video.Active && r.state.Video.SessionID != ""
}

func (r *renderer) resetDisplayVideo(controller *displayVideoController) {
	controller.Release(r.displayVideoImage)
	r.displayVideoImage = nil
	r.displayVideoFrame = ""
	r.displayVideoDrawn = ""
	r.displayVideoCanvas = nil
	r.displayVideoPositionMS = 0
}

func (r *renderer) applyDisplayVideo(controller *displayVideoController, result displayVideoResult) {
	if result.identity == "" || result.identity != controller.identity || r.state.Video == nil ||
		result.sessionID != r.state.Video.SessionID {
		controller.Release(result.image)
		return
	}
	if result.image != nil {
		controller.Release(r.displayVideoImage)
		r.displayVideoImage = result.image
		r.displayVideoFrame = result.identity + ":" + strconv.FormatUint(result.sequence, 10)
		r.displayVideoPositionMS = result.positionMS
	}
}

func (r *renderer) renderDisplayVideo(canvas *image.RGBA) {
	draw.Draw(canvas, canvas.Rect, image.NewUniform(color.Black), image.Point{}, draw.Src)
	if r.displayVideoImage != nil {
		draw.Draw(canvas, canvas.Rect, r.displayVideoImage, r.displayVideoImage.Rect.Min, draw.Src)
	}
	video := r.state.Video
	if video == nil {
		return
	}
	paused := strings.EqualFold(video.State, "paused")
	loading := strings.EqualFold(video.State, "loading") || r.displayVideoImage == nil
	if paused || loading || video.Error != "" {
		draw.Draw(canvas, canvas.Rect, image.NewUniform(color.NRGBA{A: 104}), image.Point{}, draw.Over)
	}
	if video.Title != "" && (paused || loading || video.Error != "") {
		size := 24
		if r.isCrown() {
			size = 34
		}
		r.centeredText(canvas, canvas.Rect.Dx()/2, max(54, canvas.Rect.Dy()/7), fitTextToWidth(video.Title, r.faces.bold[size], canvas.Rect.Dx()*3/4), size, true, color.White)
	}
	if paused {
		play, stop := displayVideoControlBounds(canvas.Rect.Dx(), canvas.Rect.Dy(), r.spotScreen)
		accent := paletteForTheme(r.state.DisplayTheme).primary
		drawMenuRoundedRect(canvas, play, play.Dy()/2, color.RGBA{8, 20, 31, 238})
		drawMenuRoundedOutline(canvas, play, play.Dy()/2, 3, accent)
		drawVideoPlayGlyph(canvas, play.Min.X+play.Dy()/2, (play.Min.Y+play.Max.Y)/2, play.Dy()/5, accent)
		r.centeredText(canvas, (play.Min.X+play.Max.X)/2+10, play.Min.Y+play.Dy()*62/100, "PLAY", max(16, play.Dy()/4), true, color.White)
		drawMenuRoundedRect(canvas, stop, stop.Dy()/2, color.RGBA{42, 13, 17, 242})
		drawMenuRoundedOutline(canvas, stop, stop.Dy()/2, 3, color.RGBA{255, 92, 82, 255})
		r.centeredText(canvas, (stop.Min.X+stop.Max.X)/2, stop.Min.Y+stop.Dy()*62/100, "STOP", max(16, stop.Dy()/4), true, color.White)
	} else if loading {
		r.centeredText(canvas, canvas.Rect.Dx()/2, canvas.Rect.Dy()/2+10, "LOADING VIDEO", 18, true, color.RGBA{225, 235, 245, 230})
	}
}

func displayVideoControlBounds(width, height int, spot bool) (image.Rectangle, image.Rectangle) {
	if spot {
		return image.Rect(72, 310, 252, 374), image.Rect(266, 310, 408, 374)
	}
	buttonHeight := max(58, height/10)
	buttonWidth := max(180, width/5)
	gap := max(20, width/40)
	centerX, top := width/2, height*67/100
	return image.Rect(centerX-gap/2-buttonWidth, top, centerX-gap/2, top+buttonHeight),
		image.Rect(centerX+gap/2, top, centerX+gap/2+buttonWidth, top+buttonHeight)
}

func drawVideoPlayGlyph(canvas *image.RGBA, cx, cy, radius int, ink color.RGBA) {
	for y := -radius; y <= radius; y++ {
		absoluteY := y
		if absoluteY < 0 {
			absoluteY = -absoluteY
		}
		right := radius - absoluteY*2
		for x := -radius; x <= right; x++ {
			blendPixel(canvas, cx+x+radius/5, cy+y, ink)
		}
	}
}

func (r *renderer) handleDisplayVideoTouch(client *socketClient, event linuxinput.TouchEvent) {
	if event.Kind != linuxinput.Up || r.state.Video == nil {
		return
	}
	video := r.state.Video
	position := max(r.displayVideoPositionMS, video.PositionMS)
	if !strings.EqualFold(video.State, "paused") {
		client.sendVideo("video.pause", video.SessionID, position, "")
		return
	}
	play, stop := displayVideoControlBounds(r.screenWidth, r.screenHeight, r.spotScreen)
	point := image.Pt(event.X, event.Y)
	if point.In(play) {
		client.sendVideo("video.resume", video.SessionID, position, "")
	} else if point.In(stop) {
		client.sendVideo("video.stop", video.SessionID, position, "")
	}
}
