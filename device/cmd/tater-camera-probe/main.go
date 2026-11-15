//go:build linux

// A bounded, operator-invoked Checkers camera/codec qualification. Scene pixels
// stay in memory; only counts, timing and geometry are printed. No microphone,
// framebuffer, sensor registers or persistent settings are opened here.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg"
	"io"
	"log"
	"os"
	"sync/atomic"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/liveh264"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/videostream"
)

func main() {
	address := flag.String("address", "127.0.0.1:43824", "loopback camera helper")
	target := flag.String("target", "checkers", "qualified capture target: checkers, cronos or experimental athena")
	profile := flag.String("profile", "", "explicit balanced live-video profile; default is full resolution")
	duration := flag.Duration("duration", 12*time.Second, "bounded test duration (1-30s)")
	soakMode := flag.Bool("soak", false, "explicitly allow a sustained in-memory test up to 10 minutes")
	acknowledge := flag.Bool("acknowledge-camera-in-memory", false, "capture without saving scene pixels")
	dropEvery := flag.Uint("drop-every", 0, "omit every Nth predictive packet to test keyframe recovery")
	captureOnly := flag.Bool("capture-only", false, "measure capture/encoding without the decoder")
	relayMode := flag.Bool("relay", false, "include the production loopback Sat Chat relay")
	snapshotMode := flag.Bool("snapshot", false, "validate a native-resolution still, retaining pixels only in memory")
	flag.Parse()
	videoWidth, videoHeight := 1280, 720
	stillWidth, stillHeight := 1280, 720
	switch *target {
	case "checkers":
	case "cronos":
		videoHeight = 960
		stillWidth, stillHeight = 1600, 1200
	case "athena":
		videoWidth, videoHeight = 640, 480
		stillWidth, stillHeight = 2112, 1568
	default:
		fmt.Fprintln(os.Stderr, "unqualified camera target")
		os.Exit(2)
	}
	if *profile != "" && *profile != "balanced" {
		fmt.Fprintln(os.Stderr, "unqualified capture profile")
		os.Exit(2)
	}
	if *profile == "balanced" {
		videoWidth, videoHeight = 768, 432
		if *target == "cronos" || *target == "athena" {
			videoWidth, videoHeight = 640, 480
		}
	}
	if !*acknowledge || !validProbeDuration(*duration, *soakMode) {
		fmt.Fprintln(os.Stderr, "explicit in-memory camera gate and 1-30s duration required (up to 10m with --soak)")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()
	if *snapshotMode {
		still, err := show.CaptureCameraSnapshot(ctx, *address)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		config, format, err := image.DecodeConfig(bytes.NewReader(still.Image))
		if err != nil || format != "jpeg" || config.Width != stillWidth || config.Height != stillHeight {
			fmt.Fprintln(os.Stderr, "still is not the target's native-resolution JPEG")
			os.Exit(1)
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"snapshot_width": config.Width, "snapshot_height": config.Height, "jpeg_bytes": len(still.Image), "scene_pixels_saved": false})
		return
	}
	input, output := io.Pipe()
	var captured, decoded atomic.Uint64
	var keyframes atomic.Uint64
	var dropped atomic.Uint64
	var ptsRegressions atomic.Uint64
	var previousPTS int64
	var firstFrame atomic.Int64
	var lastFrame, maxGap atomic.Int64
	started := time.Now()
	var relay *show.Server
	if *relayMode {
		relay = show.New("127.0.0.1:0", show.Snapshot{SatChat: &show.SatChat{Call: &show.SatChatCall{ID: "camera-probe", State: "active", Mode: "video"}}}, nil)
		defer relay.Close()
		go func() {
			if err := relay.Run(ctx); err != nil && ctx.Err() == nil {
				fmt.Fprintln(os.Stderr, err)
				cancel()
			}
		}()
	}
	captureDone := make(chan error, 1)
	go func() {
		maxFPS := 30
		if *profile == "balanced" {
			maxFPS = 15
		}
		err := show.StreamCameraH264Profile(ctx, *address, maxFPS, *profile, func(frame liveh264.Frame) {
			count := captured.Add(1)
			if count > 1 && frame.PTSUS <= previousPTS {
				ptsRegressions.Add(1)
			}
			previousPTS = frame.PTSUS
			if frame.Keyframe {
				keyframes.Add(1)
			}
			if *captureOnly {
				return
			}
			if *dropEvery > 0 && !frame.Keyframe && frame.Sequence%uint32(*dropEvery) == 0 {
				dropped.Add(1)
				return
			}
			if relay != nil {
				relay.SetSatChatH264(liveh264.Encode(frame))
				if os.Getenv("TATER_VDEC_TRACE") == "1" && frame.Sequence%300 == 0 {
					state := relay.Snapshot().SatChat
					log.Printf("[camera-probe] relay sequence=%d stream=%d url=%q call=%+v", frame.Sequence, frame.StreamID, state.VideoStreamURL, state.Call)
				}
				return
			}
			if err := liveh264.Write(output, liveh264.Encode(frame)); err != nil {
				cancel()
				return
			}
		})
		output.CloseWithError(err)
		captureDone <- err
		if ctx.Err() == nil {
			cancel()
		}
	}()
	receive := func(frame *image.RGBA) error {
		if frame == nil || frame.Rect.Size() != image.Pt(videoWidth, videoHeight) {
			return fmt.Errorf("unexpected decoded geometry")
		}
		decoded.Add(1)
		now := time.Since(started).Microseconds()
		firstFrame.CompareAndSwap(0, now)
		if previous := lastFrame.Swap(now); previous > 0 {
			gap := now - previous
			for old := maxGap.Load(); gap > old && !maxGap.CompareAndSwap(old, gap); old = maxGap.Load() {
			}
		}
		return nil
	}
	var decodeErr error
	relayURLReady := false
	if *captureOnly {
		<-ctx.Done()
	} else if relay != nil {
		// Wait for the first packet to establish the production stream URL.
		for ctx.Err() == nil {
			if url := relay.Snapshot().SatChat.VideoStreamURL; url != "" {
				relayURLReady = true
				decodeErr = videostream.ReadSatChatH264(ctx, url, receive)
				break
			}
			if time.Since(started) >= 10*time.Second {
				decodeErr = errors.New("camera relay did not publish a stream within 10 seconds")
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	} else {
		decodeErr = videostream.ReadH264Packets(ctx, input, receive)
	}
	expectedEnd := errors.Is(ctx.Err(), context.DeadlineExceeded)
	cancel()
	input.Close()
	captureErr := <-captureDone
	if decodeErr == nil && captureErr != nil && !expectedEnd {
		decodeErr = captureErr
	}
	elapsed := time.Since(started)
	report := map[string]any{"capture_frames": captured.Load(), "hardware_decoded_frames": decoded.Load(), "elapsed_seconds": elapsed.Seconds(), "decoded_fps": float64(decoded.Load()) / elapsed.Seconds(), "first_picture_ms": float64(firstFrame.Load()) / 1000, "width": videoWidth, "height": videoHeight, "scene_pixels_saved": false}
	report["deliberately_dropped_packets"] = dropped.Load()
	report["capture_fps"] = float64(captured.Load()) / elapsed.Seconds()
	report["production_relay"] = *relayMode
	report["relay_url_ready"] = relayURLReady
	report["capture_only"] = *captureOnly
	report["capture_profile"] = *profile
	report["last_picture_ms"] = float64(lastFrame.Load()) / 1000
	report["max_picture_gap_ms"] = float64(maxGap.Load()) / 1000
	report["capture_keyframes"] = keyframes.Load()
	report["capture_pts_regressions"] = ptsRegressions.Load()
	report["soak_mode"] = *soakMode
	if decodeErr != nil && !expectedEnd {
		report["error"] = decodeErr.Error()
	}
	_ = json.NewEncoder(os.Stdout).Encode(report)
	if captured.Load() < 2 || ptsRegressions.Load() != 0 || !*captureOnly && (decoded.Load() < 2 || lastFrame.Load() < elapsed.Microseconds()-2_000_000) || decodeErr != nil && !expectedEnd {
		os.Exit(1)
	}
}

func validProbeDuration(duration time.Duration, soak bool) bool {
	maximum := 30 * time.Second
	if soak {
		maximum = 10 * time.Minute
	}
	return duration >= time.Second && duration <= maximum
}
