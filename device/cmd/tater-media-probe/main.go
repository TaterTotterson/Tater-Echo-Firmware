//go:build linux

// tater-media-probe exercises Tater's bounded media decoder without opening
// the framebuffer or audio hardware. It is an operator-present diagnostic,
// not a background player.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash"
	"image"
	"io"
	"os"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/videostream"
)

const acknowledgement = "bounded-hardware-media-decode"

type byteCounter struct {
	bytes int64
	hash  hash.Hash
}

func (counter *byteCounter) Write(value []byte) (int, error) {
	if counter.hash == nil {
		counter.hash = sha256.New()
	}
	written, err := counter.hash.Write(value)
	if err != nil {
		return written, err
	}
	counter.bytes += int64(written)
	return written, nil
}

func (counter *byteCounter) finish() string {
	if counter.hash == nil {
		counter.hash = sha256.New()
	}
	return hex.EncodeToString(counter.hash.Sum(nil))
}

type result struct {
	Frames       int       `json:"frames"`
	FrameBytes   int64     `json:"frame_bytes"`
	FrameSHA256  string    `json:"frame_sha256"`
	AudioBytes   int64     `json:"audio_bytes"`
	AudioSHA256  string    `json:"audio_sha256"`
	ElapsedMS    int64     `json:"elapsed_ms"`
	Decoder      string    `json:"decoder"`
	HardwareOnly bool      `json:"hardware_only"`
}

func main() {
	var rawURL, acknowledge string
	var width, height, fps, maximumFrames int
	var timeout time.Duration
	var requireHardware bool
	flag.StringVar(&rawURL, "url", "", "HTTP(S) media URL")
	flag.IntVar(&width, "width", 960, "expected decoded width")
	flag.IntVar(&height, "height", 480, "expected decoded height")
	flag.IntVar(&fps, "fps", 30, "expected frame rate (24 or 30)")
	flag.IntVar(&maximumFrames, "frames", 30, "frames to decode (1-18000)")
	flag.DurationVar(&timeout, "timeout", 20*time.Second, "overall timeout")
	flag.BoolVar(&requireHardware, "require-hardware", true, "reject software fallback")
	flag.StringVar(&acknowledge, "acknowledge", "", "required bounded diagnostic acknowledgement")
	flag.Parse()

	if acknowledge != acknowledgement || width < 16 || height < 16 || width > 1920 || height > 1088 || width&1 != 0 || height&1 != 0 ||
		(fps != 24 && fps != 30) || maximumFrames < 1 || maximumFrames > 18_000 || timeout <= 0 || timeout > 15*time.Minute {
		fmt.Fprintln(os.Stderr, "refusing: invalid bounds or missing --acknowledge "+acknowledgement)
		os.Exit(2)
	}
	if _, err := videostream.CheckURL(rawURL); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if requireHardware {
		_ = os.Setenv("TATER_VDEC_ENABLE", "1")
		_ = os.Setenv("TATER_VDEC_REQUIRE", "1")
		_ = os.Setenv("TATER_FIRMWARE_TARGET", "checkers")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	started := time.Now()
	var frames int
	frameHash := sha256.New()
	var frameBytes int64
	var audio byteCounter
	err := videostream.ReadMedia(ctx, videostream.MediaRequest{
		URL: rawURL, Width: width, Height: height, FPS: fps,
	}, &audio, func(frame *image.RGBA) error {
		frames++
		written, writeErr := frameHash.Write(frame.Pix)
		frameBytes += int64(written)
		if writeErr != nil {
			return writeErr
		}
		if frames == maximumFrames {
			cancel()
		}
		return nil
	})
	if err != nil && !(frames == maximumFrames && errors.Is(err, context.Canceled)) && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		fmt.Fprintf(os.Stderr, "media probe failed after %d frame(s): %v\n", frames, err)
		os.Exit(1)
	}
	if frames != maximumFrames {
		fmt.Fprintf(os.Stderr, "media probe decoded %d frame(s), expected %d\n", frames, maximumFrames)
		os.Exit(1)
	}
	answer := result{
		Frames: frames, FrameBytes: frameBytes,
		FrameSHA256: hex.EncodeToString(frameHash.Sum(nil)),
		AudioBytes:  audio.bytes, AudioSHA256: audio.finish(),
		ElapsedMS: time.Since(started).Milliseconds(), Decoder: videostream.DecoderPath(),
		HardwareOnly: requireHardware,
	}
	if err := json.NewEncoder(os.Stdout).Encode(answer); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
