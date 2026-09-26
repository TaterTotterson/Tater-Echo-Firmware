package show

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	DefaultCameraAddress   = "127.0.0.1:43823"
	maxCameraSnapshotBytes = 4 * 1024 * 1024
)

// CameraSnapshot is a fresh still frame captured by the loopback-only Show
// APK camera service. It is kept in memory and is never written to storage.
type CameraSnapshot struct {
	Image       []byte
	ContentType string
}

// CaptureCameraSnapshot requests one frame from the Show APK. The APK owns
// Android camera permissions and hardware lifecycle; the native daemon only
// relays an explicit, authenticated Tater request over loopback.
func CaptureCameraSnapshot(ctx context.Context, address string) (CameraSnapshot, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		address = DefaultCameraAddress
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/snapshot", nil)
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
