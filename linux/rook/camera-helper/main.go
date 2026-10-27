// This adapter is copied into the pinned TECHO5 module and built with the
// spot tag. TECHO5's Rook GC0312 driver owns the hardware-specific capture.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
)

var rookPrivacyStatePatterns = []string{
	"/sys/devices/soc/10010000.keypad/privacy_state",
	"/sys/devices/soc/10010000.keypad/amz_privacy/privacy_state",
	"/sys/devices/soc/*/privacy_state",
	"/sys/devices/soc/*/amz_privacy/privacy_state",
}

const (
	defaultAddress      = "127.0.0.1:43823"
	muteStatePath       = "/data/local/etc/tater/state.json"
	streamBoundary      = "taterframe"
	streamFramePeriod   = time.Second / 15
	echoStreamPeriod    = time.Second / 20
	streamJPEGQuality   = 60
	echoJPEGQuality     = 55
	snapshotJPEGQuality = 88
	echoVideoMaxWidth   = 400
)

func main() {
	log.SetOutput(os.Stdout)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	address := strings.TrimSpace(os.Getenv("TATER_CAMERA_ADDRESS"))
	if address == "" {
		address = defaultAddress
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		log.Fatalf("camera listen: %v", err)
	}
	server := &http.Server{ReadHeaderTimeout: 2 * time.Second}
	http.HandleFunc("/health", health)
	http.HandleFunc("/snapshot", snapshot)
	http.HandleFunc("/stream", stream)
	go func() {
		<-ctx.Done()
		shutdown, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("Tater Rook camera ready on %s", address)
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func health(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func snapshot(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	muted, err := currentMuteState()
	if err != nil {
		http.Error(response, "camera mute state unavailable", http.StatusServiceUnavailable)
		return
	}
	if muted {
		http.Error(response, "camera unavailable while muted", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 8*time.Second)
	defer cancel()
	frame, err := camera.Get().Snapshot(ctx)
	if err != nil {
		http.Error(response, err.Error(), http.StatusServiceUnavailable)
		return
	}
	// Rook has no hardware camera shutter. A mute pressed during capture must
	// still prevent the completed frame from leaving this process.
	muted, err = currentMuteState()
	if err != nil {
		http.Error(response, "camera mute state unavailable", http.StatusServiceUnavailable)
		return
	}
	if muted {
		http.Error(response, "camera unavailable while muted", http.StatusForbidden)
		return
	}
	response.Header().Set("Content-Type", "image/jpeg")
	response.Header().Set("Cache-Control", "no-store")
	var cameraImage image.Image
	quality := snapshotJPEGQuality
	profile := cameraVideoProfile(request.URL.Query().Get("profile"))
	if profile == "video" {
		cameraImage = frame.Image()
		quality = streamJPEGQuality
	} else if profile == "echo-low" || profile == "echo-balanced" {
		cameraImage = frame.TaterEchoVideo(nil)
		quality = echoJPEGQuality
	} else {
		cameraImage = frame.Full()
	}
	if err := jpeg.Encode(response, cameraImage, &jpeg.Options{Quality: quality}); err != nil {
		log.Printf("camera JPEG: %v", err)
	}
}

func stream(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := response.(http.Flusher)
	if !ok {
		http.Error(response, "streaming unavailable", http.StatusInternalServerError)
		return
	}
	muted, err := currentMuteState()
	if err != nil {
		http.Error(response, "camera mute state unavailable", http.StatusServiceUnavailable)
		return
	}
	if muted {
		http.Error(response, "camera unavailable while muted", http.StatusForbidden)
		return
	}
	device := camera.Get()
	release, err := device.Acquire()
	if err != nil {
		http.Error(response, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer release()
	frames := make(chan *camera.Frame, 1)
	cancel := device.Frames.Listen(func(frame *camera.Frame) {
		select {
		case frames <- frame:
			return
		default:
		}
		select {
		case <-frames:
		default:
		}
		select {
		case frames <- frame:
		default:
		}
	})
	defer cancel()
	response.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary="+streamBoundary)
	response.Header().Set("Cache-Control", "no-store, max-age=0")
	response.WriteHeader(http.StatusOK)
	flusher.Flush()
	nextFrame := time.Time{}
	var encoded bytes.Buffer
	encoded.Grow(128 * 1024)
	var echoFrame *image.RGBA
	profile := cameraVideoProfile(request.URL.Query().Get("profile"))
	statsStarted := time.Now()
	statsFrames := 0
	statsBytes := 0
	var statsEncode time.Duration
	for {
		select {
		case <-request.Context().Done():
			return
		case frame := <-frames:
			muted, err := currentMuteState()
			if err != nil || muted {
				return
			}
			now := time.Now()
			if now.Before(nextFrame) {
				continue
			}
			nextFrame = now.Add(cameraStreamPeriod(profile))
			encoded.Reset()
			var cameraImage image.Image
			quality := streamJPEGQuality
			if profile == "echo-low" || profile == "echo-balanced" {
				echoFrame = frame.TaterEchoVideo(echoFrame)
				cameraImage = echoFrame
				quality = echoJPEGQuality
			} else {
				cameraImage = frame.Image()
			}
			encodeStarted := time.Now()
			if err := jpeg.Encode(&encoded, cameraImage, &jpeg.Options{Quality: quality}); err != nil {
				log.Printf("camera stream JPEG: %v", err)
				continue
			}
			statsEncode += time.Since(encodeStarted)
			statsFrames++
			statsBytes += encoded.Len()
			if elapsed := time.Since(statsStarted); elapsed >= 10*time.Second {
				log.Printf("[camera-video] profile=%s %.1f fps, JPEG avg=%s, bytes avg=%d",
					profile, float64(statsFrames)/elapsed.Seconds(),
					averageDuration(statsEncode, statsFrames).Round(time.Millisecond), statsBytes/max(statsFrames, 1))
				statsStarted, statsFrames, statsBytes, statsEncode = time.Now(), 0, 0, 0
			}
			if _, err := fmt.Fprintf(response, "--%s\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", streamBoundary, encoded.Len()); err != nil {
				return
			}
			if _, err := response.Write(encoded.Bytes()); err != nil {
				return
			}
			if _, err := response.Write([]byte("\r\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func averageDuration(total time.Duration, count int) time.Duration {
	if count <= 0 {
		return 0
	}
	return total / time.Duration(count)
}

func cameraStreamPeriod(profile string) time.Duration {
	if cameraVideoProfile(profile) != "video" {
		return echoStreamPeriod
	}
	return streamFramePeriod
}

func cameraVideoProfile(profile string) string {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "echo-low", "echo-balanced":
		return strings.ToLower(strings.TrimSpace(profile))
	default:
		return "video"
	}
}

func echoVideoImage(source *image.RGBA, reuse *image.RGBA) *image.RGBA {
	if source == nil || source.Bounds().Dx() < 16 || source.Bounds().Dy() < 9 {
		return source
	}
	bounds := source.Bounds()
	cropWidth := bounds.Dx()
	cropHeight := cropWidth * 9 / 16
	if cropHeight > bounds.Dy() {
		cropHeight = bounds.Dy()
		cropWidth = cropHeight * 16 / 9
	}
	outputWidth := cropWidth
	if outputWidth > echoVideoMaxWidth {
		outputWidth = echoVideoMaxWidth
	}
	outputWidth -= outputWidth % 16
	outputHeight := outputWidth * 9 / 16
	if outputWidth < 16 || outputHeight < 9 {
		return source
	}
	if reuse == nil || reuse.Bounds().Dx() != outputWidth || reuse.Bounds().Dy() != outputHeight {
		reuse = image.NewRGBA(image.Rect(0, 0, outputWidth, outputHeight))
	}
	cropX := bounds.Min.X + (bounds.Dx()-cropWidth)/2
	cropY := bounds.Min.Y + (bounds.Dy()-cropHeight)/2
	for y := 0; y < outputHeight; y++ {
		sourceY := cropY + y*cropHeight/outputHeight
		sourceRow := source.PixOffset(cropX, sourceY)
		destinationRow := reuse.PixOffset(0, y)
		for x := 0; x < outputWidth; x++ {
			sourcePixel := sourceRow + (x*cropWidth/outputWidth)*4
			destinationPixel := destinationRow + x*4
			reuse.Pix[destinationPixel] = source.Pix[sourcePixel]
			reuse.Pix[destinationPixel+1] = source.Pix[sourcePixel+1]
			reuse.Pix[destinationPixel+2] = source.Pix[sourcePixel+2]
			reuse.Pix[destinationPixel+3] = source.Pix[sourcePixel+3]
		}
	}
	return reuse
}

func currentMuteState() (bool, error) {
	return currentMuteStateFromPaths(muteStatePath, discoverPrivacyStatePaths())
}

func currentMuteStateFromPaths(logicalPath string, privacyPaths []string) (bool, error) {
	stateBytes, err := os.ReadFile(logicalPath)
	if err != nil {
		return false, err
	}
	logicalMuted, err := parseMuteState(stateBytes)
	if err != nil || logicalMuted {
		return logicalMuted, err
	}
	for _, path := range privacyPaths {
		value, readErr := os.ReadFile(path)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return false, fmt.Errorf("read camera privacy state %s: %w", path, readErr)
		}
		muted, parseErr := parsePrivacyState(value)
		if parseErr != nil {
			return false, fmt.Errorf("read camera privacy state %s: %w", path, parseErr)
		}
		if muted {
			return true, nil
		}
	}
	return false, nil
}

func discoverPrivacyStatePaths() []string {
	paths := make([]string, 0, len(rookPrivacyStatePatterns))
	seen := map[string]bool{}
	for _, pattern := range rookPrivacyStatePatterns {
		matches := []string{pattern}
		if strings.ContainsAny(pattern, "*?[") {
			matches, _ = filepath.Glob(pattern)
		}
		for _, path := range matches {
			if path != "" && !seen[path] {
				seen[path] = true
				paths = append(paths, path)
			}
		}
	}
	return paths
}

func parsePrivacyState(data []byte) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(string(data))) {
	case "1", "on", "true", "muted":
		return true, nil
	case "0", "off", "false", "unmuted":
		return false, nil
	default:
		return false, fmt.Errorf("invalid privacy state %q", strings.TrimSpace(string(data)))
	}
}

func parseMuteState(data []byte) (bool, error) {
	var state struct {
		Muted *bool `json:"muted"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return false, err
	}
	if state.Muted == nil {
		return false, errors.New("mute state missing")
	}
	return *state.Muted, nil
}
