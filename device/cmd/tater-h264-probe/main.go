//go:build linux

// Operator-invoked generated-fixture test of the actual Sat Chat receiver.
// Never opens camera, microphone, speaker, framebuffer or network endpoints.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"io"
	"os"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/liveh264"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/videostream"
)

const fixtureSHA256 = "e7d04ebc4a32d8c4ecbb0ae7e0adbfe3613ada4f41858b38a5316b09a1d52c49"

func main() {
	input := flag.String("generated-input", "", "exact generated 360-frame TVI2 fixture")
	acknowledge := flag.String("acknowledge", "", "required: generated-h264-no-camera")
	flag.Parse()
	target := os.Getenv("TATER_FIRMWARE_TARGET")
	if *acknowledge != "generated-h264-no-camera" || (target != "athena" && target != "rook") {
		fmt.Fprintln(os.Stderr, "refusing: explicit qualified target and generated-only acknowledgement required")
		os.Exit(2)
	}
	info, err := os.Lstat(*input)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 256*1024 {
		fmt.Fprintln(os.Stderr, "refusing: invalid generated fixture")
		os.Exit(2)
	}
	data, err := os.ReadFile(*input)
	digest := sha256.Sum256(data)
	if err != nil || hex.EncodeToString(digest[:]) != fixtureSHA256 {
		fmt.Fprintln(os.Stderr, "refusing: not the exact generated fixture")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Second)
	defer cancel()
	if err := videostream.CheckHardwareH264(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "generated receiver preflight:", err)
		os.Exit(1)
	}
	reader, writer := io.Pipe()
	feedDone := make(chan error, 1)
	go func() {
		var feedErr error
		defer func() { writer.CloseWithError(feedErr); feedDone <- feedErr }()
		var sequence uint32
		for offset := 0; offset < len(data); {
			if len(data)-offset < 12 {
				feedErr = io.ErrUnexpectedEOF
				return
			}
			size := int(binary.LittleEndian.Uint32(data[offset:]))
			pts := int64(binary.LittleEndian.Uint64(data[offset+4:]))
			offset += 12
			if size == 0 {
				if pts != 0 || offset != len(data) || sequence != 360 {
					feedErr = errors.New("invalid generated EOS")
				}
				return
			}
			if size > liveh264.MaxPayload || size > len(data)-offset {
				feedErr = errors.New("invalid generated AU")
				return
			}
			payload := data[offset : offset+size]
			offset += size
			key := bytes.Contains(payload, []byte{0, 0, 0, 1, 0x65}) || bytes.Contains(payload, []byte{0, 0, 1, 0x65})
			wire := liveh264.Encode(liveh264.Frame{Width: 640, Height: 480, FPS: 30,
				Sequence: sequence, StreamID: 1, PTSUS: pts, Keyframe: key, Data: payload})
			if feedErr = liveh264.Write(writer, wire); feedErr != nil {
				return
			}
			sequence++
			timer := time.NewTimer(50 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				feedErr = ctx.Err()
				return
			case <-timer.C:
			}
		}
		feedErr = io.ErrUnexpectedEOF
	}()
	started := time.Now()
	frameHash := sha256.New()
	frames := 0
	frameBytes := 0
	err = videostream.ReadH264Packets(ctx, reader, func(frame *image.RGBA) error {
		if frame == nil || frame.Rect != image.Rect(0, 0, 640, 480) {
			return errors.New("invalid decoded geometry")
		}
		frames++
		frameBytes += len(frame.Pix)
		_, writeErr := frameHash.Write(frame.Pix)
		return writeErr
	})
	cancel()
	reader.Close()
	feedErr := <-feedDone
	if err != nil || feedErr != nil || frames != 360 {
		fmt.Fprintf(os.Stderr, "generated receiver failed: frames=%d decode=%v feed=%v\n", frames, err, feedErr)
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"generated_only": true, "camera_opened": false,
		"frames": frames, "rgba_bytes": frameBytes, "rgba_sha256": hex.EncodeToString(frameHash.Sum(nil)),
		"elapsed_ms": time.Since(started).Milliseconds()})
}
