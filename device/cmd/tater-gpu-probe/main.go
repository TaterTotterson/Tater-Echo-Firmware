//go:build linux

// tater-gpu-probe validates and benchmarks the owner-local Checkers GPU
// compositor without opening or modifying the framebuffer.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"os"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/gpucompositor"
)

func main() {
	if len(os.Args) != 2 || (os.Args[1] != "--benchmark-offscreen" && os.Args[1] != "--benchmark-scenes-offscreen") {
		fmt.Fprintln(os.Stderr, "usage: tater-gpu-probe --benchmark-offscreen | --benchmark-scenes-offscreen")
		os.Exit(2)
	}
	compositor, err := gpucompositor.Open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "GPU compositor unavailable: %v\n", err)
		os.Exit(1)
	}
	if compositor == nil {
		fmt.Fprintln(os.Stderr, "GPU compositor is not enabled")
		os.Exit(1)
	}
	defer compositor.Close()
	if os.Args[1] == "--benchmark-scenes-offscreen" {
		if err := benchmarkScenes(compositor); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	source := make([]byte, gpucompositor.SourceBytes)
	destination := make([]byte, gpucompositor.PanelBytes)
	for index := 0; index < len(source); index += 4 {
		source[index+0] = byte(index)
		source[index+1] = byte(index >> 8)
		source[index+2] = byte(index >> 16)
		source[index+3] = 255
	}
	const frames = 60
	var total, maximum time.Duration
	for frame := 0; frame < frames+3; frame++ {
		source[(frame*7919%(gpucompositor.SourceWidth*gpucompositor.SourceHeight))*4] ^= byte(frame + 1)
		started := time.Now()
		if err := compositor.Composite(source, destination); err != nil {
			fmt.Fprintf(os.Stderr, "GPU compositor frame failed: %v\n", err)
			os.Exit(1)
		}
		elapsed := time.Since(started)
		if frame < 3 {
			continue
		}
		total += elapsed
		if elapsed > maximum {
			maximum = elapsed
		}
	}
	result := map[string]any{
		"adapter": "tater-gpu-rotate", "benchmark": "go-pipe-roundtrip",
		"frames": frames, "average_us": (total / frames).Microseconds(),
		"maximum_us": maximum.Microseconds(), "display_touched": false,
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func benchmarkScenes(compositor *gpucompositor.Compositor) error {
	if !compositor.SupportsScenes() {
		return fmt.Errorf("helper does not support retained scenes")
	}
	source := make([]byte, gpucompositor.SourceBytes)
	destination := make([]byte, gpucompositor.PanelBytes)
	paint := func(red, green, blue byte) {
		for index := 0; index < len(source); index += 4 {
			source[index], source[index+1], source[index+2], source[index+3] = red, green, blue, 255
		}
	}
	for y := 0; y < gpucompositor.SourceHeight; y++ {
		for x := 0; x < gpucompositor.SourceWidth; x++ {
			index := (y*gpucompositor.SourceWidth + x) * 4
			source[index], source[index+1], source[index+2], source[index+3] = byte(x), byte(y), byte(x^y), 255
		}
	}
	if err := compositor.Composite(source, destination); err != nil {
		return err
	}
	previous := append([]byte(nil), destination...)
	paint(0, 200, 0)
	if err := compositor.CompositeScene(source, destination, gpucompositor.Effects{Blend: 0}); err != nil {
		return err
	}
	if !bytes.Equal(previous, destination) {
		return fmt.Errorf("fade start changed the previously displayed pixels")
	}
	if err := compositor.CompositeScene(nil, destination, gpucompositor.Effects{Blend: .5}); err != nil {
		return err
	}
	for index := 0; index < len(destination); index += 4 {
		for channel, target := range []int{0, 200, 0, 255} {
			expected := (int(previous[index+channel]) + target) / 2
			if actual := int(destination[index+channel]); actual < expected-1 || actual > expected+1 {
				return fmt.Errorf("crossfade pixel %d = %v", index/4, destination[index:index+4])
			}
		}
	}
	copy(previous, destination)
	paint(0, 0, 200)
	if err := compositor.CompositeScene(source, destination, gpucompositor.Effects{Blend: 0}); err != nil {
		return err
	}
	if !bytes.Equal(previous, destination) {
		return fmt.Errorf("interrupted fade did not retain the last displayed image")
	}
	if err := compositor.CompositeScene(nil, destination, gpucompositor.Effects{Blend: 1}); err != nil {
		return err
	}
	for index := 0; index < len(destination); index += 4 {
		if !bytes.Equal(destination[index:index+4], []byte{200, 0, 0, 255}) {
			return fmt.Errorf("fade end pixel %d = %v", index/4, destination[index:index+4])
		}
	}
	const frames = 120
	var total, maximum time.Duration
	for frame := 0; frame < frames+3; frame++ {
		started := time.Now()
		err := compositor.CompositeScene(nil, destination, gpucompositor.Effects{
			Blend: 1, Seconds: float32(frame) / 30, Level: .7,
			Red: 1, Green: .35, Blue: .1, Rim: true, Ambient: true, Dots: true,
		})
		if err != nil {
			return err
		}
		elapsed := time.Since(started)
		if frame >= 3 {
			total += elapsed
			if elapsed > maximum {
				maximum = elapsed
			}
		}
	}
	// Rotation maps source (x,y) to panel (479-y,x).
	center := (480*480 + 239) * 4
	edge := (480*480 + 479) * 4
	if destination[edge+2] <= destination[center+2] {
		return fmt.Errorf("GPU glow did not illuminate the edge")
	}
	if compositor.SupportsAnimatedText() {
		paint(0, 0, 0)
		atlas := make([]byte, 8*8*4)
		for pixel := 0; pixel < len(atlas); pixel += 4 {
			atlas[pixel], atlas[pixel+1], atlas[pixel+2], atlas[pixel+3] = 200, 20, 10, 255
		}
		text := &gpucompositor.TextFrame{Pixels: atlas, Width: 8, Height: 8,
			Quads: []gpucompositor.TextQuad{{Atlas: image.Rect(0, 0, 8, 8), X: 100, Y: 100, Opacity: .5}}}
		if err := compositor.CompositeScene(source, destination, gpucompositor.Effects{Blend: 1, Text: text}); err != nil {
			return err
		}
		at := func(x, y int) []byte { offset := (x*480 + 479 - y) * 4; return destination[offset : offset+4] }
		pixel := at(104, 104)
		for channel, expected := range []int{5, 10, 100, 255} {
			if actual := int(pixel[channel]); actual < expected-1 || actual > expected+1 {
				return fmt.Errorf("text opacity/swizzle pixel = %v", pixel)
			}
		}
		text.Pixels = nil
		text.Quads[0].Y, text.Quads[0].Opacity = 108, 1
		if err := compositor.CompositeScene(nil, destination, gpucompositor.Effects{Blend: 1, Text: text}); err != nil {
			return err
		}
		if !bytes.Equal(at(104, 104), []byte{0, 0, 0, 255}) || !bytes.Equal(at(104, 112), []byte{10, 20, 200, 255}) {
			return fmt.Errorf("retained glyph did not move independently")
		}
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"adapter": "tater-gpu-rotate", "benchmark": "retained-ui-scene", "frames": frames,
		"average_us": (total / frames).Microseconds(), "maximum_us": maximum.Microseconds(),
		"fade_start_exact": true, "interrupted_fade_exact": true, "fade_end_exact": true,
		"command_bytes_per_frame": 40, "display_touched": false,
		"word_animation_verified": compositor.SupportsAnimatedText(),
	})
}
