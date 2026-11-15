//go:build linux

// tater-frame-probe presents a bounded raw RGBA frame through the same
// framebuffer path as tater-show. It is an operator-present diagnostic, not a
// background renderer, and must only run while tater-show is stopped.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/linuxscreen"
)

const acknowledgement = "single-owner-transient-display"

type result struct {
	SourceWidth       int   `json:"source_width"`
	SourceHeight      int   `json:"source_height"`
	SourceFrames      int   `json:"source_frames"`
	CanvasWidth       int   `json:"canvas_width"`
	CanvasHeight      int   `json:"canvas_height"`
	FramesPresented   int   `json:"frames_presented"`
	MaximumConvertUS  int64 `json:"maximum_convert_us"`
	MaximumPanUS      int64 `json:"maximum_pan_us"`
	PresentationRunMS int64 `json:"presentation_run_ms"`
}

func parseDimensions(value string) (int, int, error) {
	parts := strings.Split(value, "x")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("dimensions must be WIDTHxHEIGHT")
	}
	width, err := strconv.Atoi(parts[0])
	if err != nil || width <= 0 || width > 4096 {
		return 0, 0, fmt.Errorf("invalid source width")
	}
	height, err := strconv.Atoi(parts[1])
	if err != nil || height <= 0 || height > 4096 {
		return 0, 0, fmt.Errorf("invalid source height")
	}
	return width, height, nil
}

func fill(canvas *image.RGBA, rectangle image.Rectangle, value color.RGBA) {
	rectangle = rectangle.Intersect(canvas.Bounds())
	for y := rectangle.Min.Y; y < rectangle.Max.Y; y++ {
		for x := rectangle.Min.X; x < rectangle.Max.X; x++ {
			canvas.SetRGBA(x, y, value)
		}
	}
}

func scaleNearest(canvas *image.RGBA, source []byte, sourceWidth, sourceHeight int, destination image.Rectangle) {
	for y := destination.Min.Y; y < destination.Max.Y; y++ {
		sourceY := (y - destination.Min.Y) * sourceHeight / destination.Dy()
		for x := destination.Min.X; x < destination.Max.X; x++ {
			sourceX := (x - destination.Min.X) * sourceWidth / destination.Dx()
			offset := (sourceY*sourceWidth + sourceX) * 4
			canvas.SetRGBA(x, y, color.RGBA{
				R: source[offset], G: source[offset+1], B: source[offset+2], A: source[offset+3],
			})
		}
	}
}

func main() {
	var sourcePath string
	var sourceDimensions string
	var target string
	var acknowledge string
	var duration time.Duration
	var rate time.Duration
	var sourceFrames int
	flag.StringVar(&sourcePath, "rgba", "", "tightly packed RGBA8888 input")
	flag.StringVar(&sourceDimensions, "source", "", "input dimensions WIDTHxHEIGHT")
	flag.IntVar(&sourceFrames, "frame-count", 1, "consecutive tightly packed RGBA frames")
	flag.StringVar(&target, "target", "", "hardware target")
	flag.StringVar(&acknowledge, "acknowledge-display-change", "", "required single-owner acknowledgement")
	flag.DurationVar(&duration, "duration", 6*time.Second, "bounded presentation duration")
	flag.DurationVar(&rate, "interval", 100*time.Millisecond, "frame interval")
	flag.Parse()

	if target != "checkers" || acknowledge != acknowledgement {
		fmt.Fprintln(os.Stderr, "refusing: require --target checkers --acknowledge-display-change "+acknowledgement)
		os.Exit(2)
	}
	if duration <= 0 || duration > 15*time.Second || rate < 25*time.Millisecond || rate > time.Second {
		fmt.Fprintln(os.Stderr, "refusing: duration must be (0,15s] and interval must be [25ms,1s]")
		os.Exit(2)
	}
	if sourceFrames < 1 || sourceFrames > 32 {
		fmt.Fprintln(os.Stderr, "refusing: frame count must be between 1 and 32")
		os.Exit(2)
	}
	sourceWidth, sourceHeight, err := parseDimensions(sourceDimensions)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read RGBA frame: %v\n", err)
		os.Exit(1)
	}
	frameBytes := sourceWidth * sourceHeight * 4
	if frameBytes <= 0 || frameBytes > 64*1024*1024/sourceFrames {
		fmt.Fprintln(os.Stderr, "refusing: RGBA sequence exceeds 64 MiB")
		os.Exit(2)
	}
	expected := frameBytes * sourceFrames
	if len(source) != expected {
		fmt.Fprintf(os.Stderr, "RGBA sequence is %d bytes; expected exactly %d\n", len(source), expected)
		os.Exit(1)
	}

	screen, err := linuxscreen.Open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "open screen: %v\n", err)
		os.Exit(1)
	}
	defer screen.Close()
	canvas := screen.Canvas()
	if canvas.Bounds().Dx() != 960 || canvas.Bounds().Dy() != 480 {
		fmt.Fprintf(os.Stderr, "refusing unexpected Checkers canvas %v\n", canvas.Bounds())
		os.Exit(1)
	}

	background := color.RGBA{R: 5, G: 9, B: 17, A: 255}
	fill(canvas, canvas.Bounds(), background)
	scale := min((canvas.Bounds().Dy()-72)/sourceHeight, (canvas.Bounds().Dx()-72)/sourceWidth)
	if scale < 1 {
		scale = 1
	}
	destination := image.Rect(0, 0, sourceWidth*scale, sourceHeight*scale)
	destination = destination.Add(image.Pt(
		(canvas.Bounds().Dx()-destination.Dx())/2,
		(canvas.Bounds().Dy()-destination.Dy())/2,
	))
	fill(canvas, image.Rect(destination.Min.X-8, destination.Min.Y-8, destination.Max.X+8, destination.Min.Y), color.RGBA{R: 255, G: 132, B: 48, A: 255})
	fill(canvas, image.Rect(destination.Min.X-8, destination.Min.Y, destination.Min.X, destination.Max.Y), color.RGBA{R: 42, G: 152, B: 255, A: 255})

	started := time.Now()
	deadline := started.Add(duration)
	frames := 0
	var maximumConvert, maximumPan time.Duration
	for time.Now().Before(deadline) {
		sourceFrame := frames % sourceFrames
		frame := source[sourceFrame*frameBytes : (sourceFrame+1)*frameBytes]
		scaleNearest(canvas, frame, sourceWidth, sourceHeight, destination)
		bar := image.Rect(36, canvas.Bounds().Dy()-28, canvas.Bounds().Dx()-36, canvas.Bounds().Dy()-16)
		fill(canvas, bar, color.RGBA{R: 33, G: 42, B: 56, A: 255})
		progress := (frames * 19) % max(bar.Dx()-24, 1)
		fill(canvas, image.Rect(bar.Min.X+progress, bar.Min.Y, bar.Min.X+progress+24, bar.Max.Y), color.RGBA{R: 255, G: 132, B: 48, A: 255})
		if err := screen.PresentTransientRegions([]image.Rectangle{canvas.Bounds()}); err != nil {
			fmt.Fprintf(os.Stderr, "present transient frame: %v\n", err)
			os.Exit(1)
		}
		converted, panned := screen.LastPresentTimings()
		maximumConvert = max(maximumConvert, converted)
		maximumPan = max(maximumPan, panned)
		frames++
		time.Sleep(rate)
	}

	answer := result{
		SourceWidth: sourceWidth, SourceHeight: sourceHeight,
		SourceFrames: sourceFrames,
		CanvasWidth:  canvas.Bounds().Dx(), CanvasHeight: canvas.Bounds().Dy(),
		FramesPresented: frames, MaximumConvertUS: maximumConvert.Microseconds(),
		MaximumPanUS: maximumPan.Microseconds(), PresentationRunMS: time.Since(started).Milliseconds(),
	}
	if err := json.NewEncoder(os.Stdout).Encode(answer); err != nil {
		fmt.Fprintf(os.Stderr, "encode result: %v\n", err)
		os.Exit(1)
	}
}
