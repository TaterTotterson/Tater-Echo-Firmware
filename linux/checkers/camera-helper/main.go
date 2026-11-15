// This file is copied into the pinned TECHO5 module and built there. Keeping
// the thin HTTP adapter here lets Tater reuse TECHO5's hardware-reviewed
// Checkers camera driver without forking its MediaTek ISP implementation.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
)

const (
	defaultAddress       = "127.0.0.1:43823"
	streamBoundary       = "taterframe"
	streamFramePeriod    = time.Second / 15
	echoStreamPeriod     = time.Second / 20
	balancedStreamPeriod = time.Second / 15
	smoothStreamPeriod   = time.Second / 20
	streamJPEGQuality    = 60
	echoJPEGQuality      = 55
	balancedJPEGQuality  = 62
	snapshotJPEGQuality  = 88
	balancedVideoWidth   = 640
)

func main() {
	log.SetOutput(os.Stdout)
	if os.Getenv("TATER_CAMERA_DEBUG") == "1" {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	}
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
	server := &http.Server{ReadHeaderTimeout: 2 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	http.HandleFunc("/health", health)
	http.HandleFunc("/capabilities", cameraCapabilities)
	http.HandleFunc("/snapshot", snapshot)
	http.HandleFunc("/stream", stream)
	http.HandleFunc("/stream.h264", streamH264)
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdown, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_ = server.Shutdown(shutdown)
		_ = server.Close()
		if err := camera.Get().TaterStop(shutdown); err != nil {
			log.Printf("camera shutdown: %v", err)
		}
	}()
	log.Printf("Tater Checkers camera ready on %s", address)
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	if ctx.Err() != nil {
		<-shutdownDone
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
	ctx, cancel := context.WithTimeout(request.Context(), 8*time.Second)
	defer cancel()
	frame, err := camera.Get().Snapshot(ctx)
	if err != nil {
		http.Error(response, err.Error(), http.StatusServiceUnavailable)
		return
	}
	response.Header().Set("Content-Type", "image/jpeg")
	response.Header().Set("Cache-Control", "no-store")
	image, quality := snapshotImage(frame, request.URL.Query().Get("profile"))
	if err := jpeg.Encode(response, image, &jpeg.Options{Quality: quality}); err != nil {
		log.Printf("camera JPEG: %v", err)
	}
}

type snapshotFrame interface {
	Full() *image.RGBA
	Image() *image.RGBA
	TaterEchoVideo(*image.RGBA) *image.RGBA
	TaterSmoothVideo(*image.RGBA) *image.RGBA
}

func snapshotImage(frame snapshotFrame, profile string) (*image.RGBA, int) {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "video", "echo-low", "echo-balanced":
		return liveVideoImage(frame, cameraVideoProfile(profile), nil)
	default:
		return frame.Full(), snapshotJPEGQuality
	}
}

// Only the two connected development targets use the lower-cost live profile.
// Still photos and other models retain their existing resolution and cadence.
func smoothLiveCapture() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TATER_FIRMWARE_TARGET"))) {
	case "checkers", "cronos":
		return true
	default:
		return false
	}
}

func liveVideoImage(frame snapshotFrame, profile string, reuse *image.RGBA) (*image.RGBA, int) {
	if profile == "echo-low" {
		return frame.TaterEchoVideo(reuse), echoJPEGQuality
	}
	quality := streamJPEGQuality
	if profile == "echo-balanced" {
		quality = balancedJPEGQuality
	}
	if smoothLiveCapture() {
		// Develop only the 480x270 Bayer samples the stream needs, rather than
		// developing a larger image and throwing most of its pixels away.
		return frame.TaterSmoothVideo(reuse), quality
	}
	if profile == "echo-balanced" {
		return echoBalancedVideoImage(frame.Image(), reuse), quality
	}
	return frame.Image(), quality
}

type liveCameraAcquirer interface {
	Acquire() (func(), error)
	AcquireTaterLiveVideo() (func(), error)
}

func acquireLiveCamera(device liveCameraAcquirer) (func(), error) {
	if smoothLiveCapture() {
		return device.AcquireTaterLiveVideo()
	}
	return device.Acquire()
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
	device := camera.Get()
	release, err := acquireLiveCamera(device)
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
	var videoFrame *image.RGBA
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
			now := time.Now()
			if now.Before(nextFrame) {
				continue
			}
			nextFrame = now.Add(cameraStreamPeriod(profile))
			encoded.Reset()
			cameraImage, quality := liveVideoImage(frame, profile, videoFrame)
			if cameraImage == nil {
				continue
			}
			videoFrame = cameraImage
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
	if smoothLiveCapture() && cameraVideoProfile(profile) != "echo-low" {
		return smoothStreamPeriod
	}
	switch cameraVideoProfile(profile) {
	case "echo-low":
		return echoStreamPeriod
	case "echo-balanced":
		return balancedStreamPeriod
	default:
		return streamFramePeriod
	}
}

func cameraVideoProfile(profile string) string {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "echo-low", "echo-balanced":
		return strings.ToLower(strings.TrimSpace(profile))
	default:
		return "video"
	}
}

// echoBalancedVideoImage prepares the 640x360 profile used by the larger Echo
// displays. Developing the native image preserves the extra Bayer detail;
// Athena's 4:3 camera is center-cropped before scaling so every target sees an
// uncropped 16:9 call frame.
func echoBalancedVideoImage(source *image.RGBA, reuse *image.RGBA) *image.RGBA {
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
	outputWidth := min(cropWidth, balancedVideoWidth)
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
			copy(reuse.Pix[destinationPixel:destinationPixel+4], source.Pix[sourcePixel:sourcePixel+4])
		}
	}
	return reuse
}
