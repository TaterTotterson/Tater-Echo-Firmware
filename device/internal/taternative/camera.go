package taternative

import (
	"context"
	"encoding/base64"
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
