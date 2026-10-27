//go:build linux

package main

import (
	"context"
	"fmt"
	"image"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/videostream"
)

const (
	idleCoreFloor  = 2
	videoCoreFloor = 4
)

type cameraStreamResult struct {
	identity string
	sequence uint64
	image    *image.RGBA
	err      error
}

// cameraStreamController owns at most one decoder. Frames cross into the
// renderer through a one-deep latest-frame queue; old buffers are recycled so
// 1280x800 video does not create display-sized garbage every frame.
type cameraStreamController struct {
	identity string
	cancel   context.CancelFunc
	frames   chan cameraStreamResult
	recycle  chan *image.RGBA
}

func newCameraStreamController() *cameraStreamController {
	// Also repairs the floor after an unclean renderer exit; BusyBox init
	// respawns this process, while procfs retains the previous value.
	setVideoCoreFloor(false)
	return &cameraStreamController{
		frames:  make(chan cameraStreamResult, 1),
		recycle: make(chan *image.RGBA, 3),
	}
}

func cameraStreamIdentity(cameras *show.Cameras, size image.Point) string {
	if cameras == nil || cameras.SelectedID == "" || cameras.StreamURL == "" || size.X <= 0 || size.Y <= 0 {
		return ""
	}
	width, height := size.X&^1, size.Y&^1
	if width <= 0 || height <= 0 {
		return ""
	}
	return fmt.Sprintf("%s\x00%s\x00%dx%d", cameras.SelectedID, cameras.StreamURL, width, height)
}

// Update starts or replaces the decoder when the selected stream changes. It
// returns true when the renderer must discard a frame from the old stream.
func (controller *cameraStreamController) Update(parent context.Context, cameras *show.Cameras, size image.Point) bool {
	identity := cameraStreamIdentity(cameras, size)
	if identity == controller.identity {
		return false
	}
	if controller.cancel != nil {
		controller.cancel()
		controller.cancel = nil
	}
	controller.identity = identity
	if identity == "" {
		return true
	}
	width, height := size.X&^1, size.Y&^1
	ctx, cancel := context.WithCancel(parent)
	controller.cancel = cancel
	go controller.run(ctx, identity, videostream.Request{URL: cameras.StreamURL, Width: width, Height: height})
	return true
}

func (controller *cameraStreamController) Active() bool {
	return controller != nil && controller.identity != ""
}

func (controller *cameraStreamController) run(ctx context.Context, identity string, request videostream.Request) {
	parsed, _ := videostream.CheckURL(request.URL)
	log.Printf("[camera-video] opening %s with %s at %dx%d", videostream.RedactedURL(parsed), videostream.DecoderPath(), request.Width, request.Height)
	for attempt := 1; ctx.Err() == nil; attempt++ {
		var sequence uint64
		err := videostream.Read(ctx, request, func(source *image.RGBA) error {
			sequence++
			destination := controller.acquire(source.Rect)
			copy(destination.Pix, source.Pix)
			controller.publish(cameraStreamResult{identity: identity, sequence: sequence, image: destination})
			return nil
		})
		if ctx.Err() != nil {
			return
		}
		controller.publish(cameraStreamResult{identity: identity, err: err})
		log.Printf("[camera-video] stream stopped (attempt %d): %v", attempt, err)
		select {
		case <-time.After(min(10*time.Second, time.Duration(attempt)*1500*time.Millisecond)):
		case <-ctx.Done():
			return
		}
	}
}

func (controller *cameraStreamController) acquire(bounds image.Rectangle) *image.RGBA {
	for {
		select {
		case frame := <-controller.recycle:
			if frame != nil && frame.Rect == bounds {
				return frame
			}
		default:
			return image.NewRGBA(bounds)
		}
	}
}

func (controller *cameraStreamController) publish(result cameraStreamResult) {
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

func (controller *cameraStreamController) Release(frame *image.RGBA) {
	if frame == nil {
		return
	}
	select {
	case controller.recycle <- frame:
	default:
	}
}

func (controller *cameraStreamController) Close() {
	if controller.cancel != nil {
		controller.cancel()
		controller.cancel = nil
	}
	controller.identity = ""
	setVideoCoreFloor(false)
}

func satChatVideoActive(state show.Snapshot) bool {
	return state.SatChat != nil && state.SatChat.Call != nil &&
		strings.EqualFold(state.SatChat.Call.State, "active") &&
		strings.EqualFold(state.SatChat.Call.Mode, "video")
}

func cameraVideoActive(state show.Snapshot) bool {
	return state.Cameras != nil && state.Cameras.SelectedID != "" && state.Cameras.StreamActive
}

func (renderer *renderer) resetCameraStream(controller *cameraStreamController) {
	controller.Release(renderer.cameraStreamImage)
	renderer.cameraStreamImage = nil
	renderer.cameraStreamShown = false
	renderer.cameraFrameImage = nil
	renderer.cameraFrameShown = ""
}

func (renderer *renderer) applyCameraStream(controller *cameraStreamController, result cameraStreamResult) {
	if result.identity == "" || result.identity != controller.identity || renderer.state.Cameras == nil ||
		cameraStreamIdentity(renderer.state.Cameras, renderer.cameraVideoBounds().Size()) != result.identity {
		controller.Release(result.image)
		return
	}
	if result.err != nil {
		// A decoder retry is not a camera selection change. Keep the most
		// recent live frame, or the snapshot fallback when no live frame has
		// arrived, visible while the decoder reconnects. Clearing both here
		// made the Cameras screen flash at every retry.
		controller.Release(result.image)
		return
	}
	if result.image == nil {
		return
	}
	controller.Release(renderer.cameraStreamImage)
	renderer.cameraStreamImage = result.image
	renderer.cameraStreamShown = true
	renderer.cameraFrameImage = result.image
	renderer.cameraFrameShown = fmt.Sprintf("stream:%s:%d", result.identity, result.sequence)
}

// setVideoCoreFloor leaves the normal two-core latency floor in place at idle
// and asks MediaTek HPS for all four A53s while either direct camera playback
// or Sat Chat video is active. HPS is absent on platforms that do not need
// this, where the operation is a no-op.
func setVideoCoreFloor(active bool) {
	const path = "/proc/hps/num_base_perf_serv"
	wanted := idleCoreFloor
	if active {
		wanted = videoCoreFloor
	}
	before, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(before)) == strconv.Itoa(wanted) {
		return
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(wanted)), 0o644); err != nil {
		log.Printf("[camera-video] could not set CPU core floor to %d: %v", wanted, err)
		return
	}
	log.Printf("[camera-video] CPU core floor %s -> %d", strings.TrimSpace(string(before)), wanted)
}
