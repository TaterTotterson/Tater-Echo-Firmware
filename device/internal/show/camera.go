package show

import (
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

const (
	DefaultCameraAddress   = "127.0.0.1:43823"
	maxCameraSnapshotBytes = 4 * 1024 * 1024
)

// CameraSnapshot is a fresh still frame captured by the loopback-only Linux
// camera helper. It is kept in memory and is never written to storage.
type CameraSnapshot struct {
	Image       []byte
	ContentType string
}

// CaptureCameraSnapshot requests one frame from the Linux camera helper. The
// native daemon relays an explicit, authenticated Tater request over loopback.
func CaptureCameraSnapshot(ctx context.Context, address string) (CameraSnapshot, error) {
	return captureCamera(ctx, address, "/snapshot")
}

// CaptureCameraVideoFrame requests the helper's lower-cost live-view profile.
// Older helpers ignore the query parameter and return a full still, so an app
// OTA remains compatible with the immutable root filesystem already installed
// on a device.
func CaptureCameraVideoFrame(ctx context.Context, address string) (CameraSnapshot, error) {
	return CaptureCameraVideoFrameProfile(ctx, address, "")
}

// CaptureCameraVideoFrameProfile requests the Echo-to-Echo profile selected
// for this endpoint. Unknown or empty profiles deliberately retain the normal
// video frame for mobile peers.
func CaptureCameraVideoFrameProfile(ctx context.Context, address, profile string) (CameraSnapshot, error) {
	endpoint := "/snapshot?profile=video"
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "echo-low":
		endpoint = "/snapshot?profile=echo-low"
	case "echo-balanced":
		endpoint = "/snapshot?profile=echo-balanced"
	}
	return captureCamera(ctx, address, endpoint)
}

// StreamCameraVideo consumes the camera helper's continuous MJPEG feed. The
// helper keeps the sensor acquired and drops frames before encoding whenever
// the consumer cannot keep up, so callback always receives current pictures
// rather than a growing snapshot backlog.
func StreamCameraVideo(ctx context.Context, address string, callback func([]byte)) error {
	return StreamCameraVideoProfile(ctx, address, "", callback)
}

// StreamCameraVideoProfile consumes the selected helper profile. Echo-to-Echo
// calls use a frame sized for the receiving display; mobile peers retain the
// helper's normal stream.
func StreamCameraVideoProfile(ctx context.Context, address, profile string, callback func([]byte)) error {
	address = strings.TrimSpace(address)
	if address == "" {
		address = DefaultCameraAddress
	}
	endpoint := "/stream"
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "echo-low":
		endpoint += "?profile=echo-low"
	case "echo-balanced":
		endpoint += "?profile=echo-balanced"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+endpoint, nil)
	if err != nil {
		return fmt.Errorf("create camera stream request: %w", err)
	}
	response, err := (&http.Client{Timeout: 0}).Do(request)
	if err != nil {
		return fmt.Errorf("request Show camera stream: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return fmt.Errorf("Show camera stream returned %s: %s",
			response.Status, strings.TrimSpace(string(detail)))
	}
	mediaType, parameters, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "multipart/x-mixed-replace") || strings.TrimSpace(parameters["boundary"]) == "" {
		return fmt.Errorf("Show camera stream returned unsupported content type %q", response.Header.Get("Content-Type"))
	}
	reader := multipart.NewReader(response.Body, parameters["boundary"])
	for {
		part, err := reader.NextPart()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("read Show camera stream: %w", err)
		}
		image, readErr := io.ReadAll(io.LimitReader(part, maxCameraSnapshotBytes+1))
		_ = part.Close()
		if readErr != nil {
			return fmt.Errorf("read Show camera frame: %w", readErr)
		}
		if len(image) == 0 || len(image) > maxCameraSnapshotBytes || len(image) < 2 || image[0] != 0xff || image[1] != 0xd8 {
			continue
		}
		if callback != nil {
			callback(image)
		}
	}
}

func captureCamera(ctx context.Context, address, endpoint string) (CameraSnapshot, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		address = DefaultCameraAddress
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+endpoint, nil)
	if err != nil {
		return CameraSnapshot{}, fmt.Errorf("create camera request: %w", err)
	}
	client := &http.Client{Timeout: 8 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return CameraSnapshot{}, fmt.Errorf("request Show camera: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return CameraSnapshot{}, fmt.Errorf("Show camera returned %s: %s",
			response.Status, strings.TrimSpace(string(detail)))
	}
	image, err := io.ReadAll(io.LimitReader(response.Body, maxCameraSnapshotBytes+1))
	if err != nil {
		return CameraSnapshot{}, fmt.Errorf("read Show camera JPEG: %w", err)
	}
	if len(image) == 0 {
		return CameraSnapshot{}, fmt.Errorf("Show camera returned an empty image")
	}
	if len(image) > maxCameraSnapshotBytes {
		return CameraSnapshot{}, fmt.Errorf("Show camera image exceeded %d bytes", maxCameraSnapshotBytes)
	}
	contentType := strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0])
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		return CameraSnapshot{}, fmt.Errorf("Show camera returned unsupported content type %q", contentType)
	}
	return CameraSnapshot{Image: image, ContentType: contentType}, nil
}
