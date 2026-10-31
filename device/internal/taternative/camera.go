package taternative

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

const maxCameraSnapshotBytes = 4 * 1024 * 1024

func (c *Client) captureCameraSnapshot(replyTo string) {
	result := map[string]any{"reply_to": replyTo, "ok": false}
	hook := c.hooks.CameraSnapshot
	if hook == nil {
		result["error"] = "camera snapshots are unavailable"
		c.sendJSON("camera.snapshot.result", "", result)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Second)
	defer cancel()
	snapshot, err := hook(ctx)
	if err != nil {
		result["error"] = err.Error()
		c.sendJSON("camera.snapshot.result", "", result)
		return
	}
	if len(snapshot.Image) == 0 || len(snapshot.Image) > maxCameraSnapshotBytes {
		result["error"] = fmt.Sprintf("camera returned an invalid image size: %d", len(snapshot.Image))
		c.sendJSON("camera.snapshot.result", "", result)
		return
	}
	contentType := strings.TrimSpace(snapshot.ContentType)
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		contentType = "image/jpeg"
	}
	result["ok"] = true
	result["content_type"] = contentType
	result["image_base64"] = base64.StdEncoding.EncodeToString(snapshot.Image)
	c.sendJSON("camera.snapshot.result", "", result)
}

func encodeCameraFrame(jpeg []byte) []byte {
	if len(jpeg) < 4 || len(jpeg) > maxCameraSnapshotBytes || jpeg[0] != 0xff || jpeg[1] != 0xd8 {
		return nil
	}
	frame := make([]byte, len(cameraFramePrefix)+len(jpeg))
	copy(frame, cameraFramePrefix)
	copy(frame[len(cameraFramePrefix):], jpeg)
	return frame
}

// startCameraStream publishes the camera helper's continuous MJPEG frames on
// the best-effort video queue. It deliberately shares the TCAM1 envelope used
// for integration-camera playback, so Tater can relay a native camera without
// inventing a second image transport.
func (c *Client) startCameraStream(replyTo, profile string) {
	result := map[string]any{"reply_to": replyTo, "ok": false}
	if c.satChatActive.Load() {
		result["error"] = "camera stream is already in use by Sat Chat"
		c.sendJSON("camera.stream.start.result", "", result)
		return
	}
	hook := c.hooks.CameraStream
	if hook == nil {
		result["error"] = "camera streaming is unavailable"
		c.sendJSON("camera.stream.start.result", "", result)
		return
	}
	c.stopCameraStream()
	ctx, cancel := context.WithCancel(c.ctx)
	c.cameraStreamMu.Lock()
	c.cameraStreamID++
	streamID := c.cameraStreamID
	c.cameraStreamCancel = cancel
	c.cameraStreamMu.Unlock()
	result["ok"] = true
	result["format"] = "mjpeg"
	result["profile"] = strings.TrimSpace(profile)
	c.sendJSON("camera.stream.start.result", "", result)
	go func() {
		err := hook(ctx, strings.TrimSpace(profile), func(jpeg []byte) {
			if frame := encodeCameraFrame(jpeg); len(frame) > 0 {
				c.sendBestEffortBinary(frame)
			}
		})
		c.cameraStreamMu.Lock()
		current := c.cameraStreamID == streamID
		if current {
			c.cameraStreamCancel = nil
		}
		c.cameraStreamMu.Unlock()
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			return
		}
		if !current {
			return
		}
		payload := map[string]any{"ok": err == nil}
		if err != nil {
			payload["error"] = err.Error()
		}
		c.sendJSON("camera.stream.ended", "", payload)
	}()
}

func (c *Client) stopCameraStream() bool {
	c.cameraStreamMu.Lock()
	cancel := c.cameraStreamCancel
	c.cameraStreamCancel = nil
	c.cameraStreamID++
	c.cameraStreamMu.Unlock()
	if cancel != nil {
		cancel()
		return true
	}
	return false
}
