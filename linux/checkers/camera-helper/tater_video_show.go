//go:build !spot && !dot

package camera

import (
	"context"
	"image"
	"os"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// TaterStop waits for the driver's reverse teardown on service shutdown,
// rather than exiting during the normal inter-snapshot sensor linger period.
func (c *Camera) TaterStop(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		c.idleStop()
		c.mu.Lock()
		stopping := c.stopping
		c.mu.Unlock()
		if stopping != nil {
			<-stopping
		}
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

const taterEchoVideoWidth, taterEchoVideoHeight = 320, 180
const taterSmoothVideoWidth, taterSmoothVideoHeight = 480, 270

// TaterBayer10 copies one immutable qualified sensor frame and its existing
// tone tables to the narrow encoder process. It does not develop an RGBA
// image, alter exposure, or acquire/power the sensor. Other targets are not
// qualified for this contract.
func (f *Frame) TaterBayer10(reuse []byte) []byte {
	return f.TaterLiveBayer10(reuse, true)
}

// TaterLiveBayer10 can retain the prior color tables between metering ticks.
// Camera exposure is still measured by the sensor driver on every normal AE
// cycle; only display white-balance/tone analysis is rate limited by the caller.
func (f *Frame) TaterLiveBayer10(reuse []byte, refreshTone bool) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	checkers := (layout.Checkers() || layout.Crown()) && sensorW == 1280 && sensorH == 720 && sensor.blueFirst
	cronos := layout.Board == "cronos" && sensorW == 1600 && sensorH == 1200 && !sensor.blueFirst
	if (!checkers && !cronos) || len(f.raw) != frameBytes {
		return nil
	}
	if os.Getenv("TATER_CAMERA_DEBUG") == "1" {
		taterClockOnce.Do(taterCameraTiming)
	}
	size := 6144 + len(f.raw)
	if cap(reuse) < size {
		reuse = make([]byte, size)
		refreshTone = true
	} else {
		reuse = reuse[:size]
	}
	if refreshTone {
		tables := f.level().taterLiveTables()
		for channel := 0; channel < 3; channel++ {
			copy(reuse[channel*2048:], tables[channel][:])
		}
	}
	copy(reuse[6144:], f.raw)
	return reuse
}

// TaterEchoVideo develops only the pixels needed by a Sat Chat frame. The
// normal live image expands every Bayer cell to 640x360 (or 800x600) before
// the helper downsizes it. Reading the required packed samples directly cuts
// both conversion work and allocation pressure at the camera source.
func (f *Frame) TaterEchoVideo(reuse *image.RGBA) *image.RGBA {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.raw == nil {
		return nil
	}
	return renderTaterEchoVideo(f.raw, f.level(), reuse)
}

func renderTaterEchoVideo(raw []byte, level tone, reuse *image.RGBA) *image.RGBA {
	return renderTaterVideo(raw, level, reuse, taterEchoVideoWidth, taterEchoVideoHeight)
}

// TaterSmoothVideo keeps the existing 16:9 call composition while developing
// only 480x270 pixels. Native stills continue to use Full without this path.
func (f *Frame) TaterSmoothVideo(reuse *image.RGBA) *image.RGBA {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.raw == nil {
		return nil
	}
	return renderTaterVideo(f.raw, f.level(), reuse, taterSmoothVideoWidth, taterSmoothVideoHeight)
}

func renderTaterVideo(raw []byte, level tone, reuse *image.RGBA, width, height int) *image.RGBA {
	if len(raw) < frameBytes || (width != taterEchoVideoWidth || height != taterEchoVideoHeight) &&
		(width != taterSmoothVideoWidth || height != taterSmoothVideoHeight) {
		return nil
	}
	if reuse == nil || reuse.Bounds() != image.Rect(0, 0, width, height) {
		reuse = image.NewRGBA(image.Rect(0, 0, width, height))
	}

	// Width/Height are measured in Bayer cells. Crop the source cells to 16:9
	// before sampling so cronos retains the same uncropped call composition as
	// the former post-render scaler; OV9734 Checkers/Crown are already 16:9.
	cropWidth := Width
	cropHeight := cropWidth * 9 / 16
	if cropHeight > Height {
		cropHeight = Height
		cropWidth = cropHeight * 16 / 9
	}
	cropX := (Width - cropWidth) / 2
	cropY := (Height - cropHeight) / 2
	luts := level.tables()
	for y := 0; y < height; y++ {
		cellY := cropY + y*cropHeight/height
		sensorY := cellY * 2
		row0 := raw[sensorY*bytesPerLine : (sensorY+1)*bytesPerLine]
		row1 := raw[(sensorY+1)*bytesPerLine : (sensorY+2)*bytesPerLine]
		destination := y * reuse.Stride
		for x := 0; x < width; x++ {
			cellX := cropX + x*cropWidth/width
			sensorX := cellX * 2
			r := packed10Sample(row0, sensorX)
			g := packed10Sample(row0, sensorX+1) + packed10Sample(row1, sensorX)
			b := packed10Sample(row1, sensorX+1)
			if sensor.blueFirst {
				r, b = b, r
			}
			pixel := destination + x*4
			reuse.Pix[pixel+0] = luts[0][r<<1]
			reuse.Pix[pixel+1] = luts[1][g]
			reuse.Pix[pixel+2] = luts[2][b<<1]
			reuse.Pix[pixel+3] = 255
		}
	}
	return reuse
}

func packed10Sample(line []byte, pixel int) uint16 {
	group := pixel / 4 * 5
	b0, b1 := uint16(line[group]), uint16(line[group+1])
	switch pixel & 3 {
	case 0:
		return b0 | (b1&3)<<8
	case 1:
		return b1>>2 | (uint16(line[group+2])&0x0f)<<6
	case 2:
		b2, b3 := uint16(line[group+2]), uint16(line[group+3])
		return b2>>4 | (b3&0x3f)<<4
	default:
		return uint16(line[group+3])>>6 | uint16(line[group+4])<<2
	}
}
