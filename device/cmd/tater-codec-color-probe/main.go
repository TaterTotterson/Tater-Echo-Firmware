//go:build linux

// Decode a bounded, operator-generated live H.264 fixture without camera,
// microphone, framebuffer or pixel recording. Report only channel means.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"os"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/videostream"
)

func main() {
	input := flag.String("input", "", "generated timestamped H.264 fixture")
	ack := flag.Bool("acknowledge-synthetic-color", false, "fixture contains generated colors, not a camera recording")
	flag.Parse()
	info, err := os.Lstat(*input)
	if !*ack || err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 8*1024*1024 {
		fmt.Fprintln(os.Stderr, "bounded regular synthetic fixture and acknowledgement required")
		os.Exit(2)
	}
	file, err := os.Open(*input)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer file.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var totals [3]uint64
	var pixels uint64
	frames := 0
	err = videostream.ReadH264Packets(ctx, file, func(frame *image.RGBA) error {
		frames++
		if frames > 120 {
			return fmt.Errorf("synthetic frame limit exceeded")
		}
		for y := frame.Rect.Min.Y; y < frame.Rect.Max.Y; y++ {
			for x := frame.Rect.Min.X; x < frame.Rect.Max.X; x++ {
				p := frame.PixOffset(x, y)
				for c := range totals {
					totals[c] += uint64(frame.Pix[p+c])
				}
				pixels++
			}
		}
		return nil
	})
	if err != nil || pixels == 0 {
		fmt.Fprintf(os.Stderr, "synthetic decode: frames=%d error=%v\n", frames, err)
		os.Exit(1)
	}
	rgb := [3]float64{}
	for c := range totals {
		rgb[c] = float64(totals[c]) / float64(pixels)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"frames": frames, "mean_rgb": rgb, "pixels_saved": false})
}
