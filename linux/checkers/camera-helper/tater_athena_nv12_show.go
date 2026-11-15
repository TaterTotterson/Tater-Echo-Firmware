//go:build !dot && !spot && !panel

package camera

import (
	"sync"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

const taterAthenaVideoWidth, taterAthenaVideoHeight = 640, 480

// TaterAthenaVideo belongs to one stream's producer. It retains the camera's
// ordinary tone/white-balance tables between metering ticks, not camera frames.
// Sensor capture and still photographs retain their native resolution.
type TaterAthenaVideo struct{ tables *[3][2048]uint8 }

func (c *TaterAthenaVideo) NV12(f *Frame, reuse []byte, refreshTone bool) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	if layout.Board != "athena" || sensor != athenaSensor || len(f.raw) != frameBytes {
		return nil
	}
	if c.tables == nil || refreshTone {
		c.tables = f.level().taterLiveTables()
	}
	return renderTaterAthenaNV12(f.raw, c.tables, reuse)
}

// Sample one RGB value per BGGR cell before BT.601 limited-range conversion.
// The 1056x784 cell image is centered/cropped to 1040x780 (exact 4:3); it is
// never stretched, developed at full size, or read after its DMA buffer returns
// to the driver. Frame.raw is the platform's immutable copy made before QBUF.
func renderTaterAthenaNV12(raw []byte, tables *[3][2048]uint8, reuse []byte) []byte {
	const sensorWidth, sensorHeight = 2112, 1568
	const stride = sensorWidth * 5 / 4
	const width, height = taterAthenaVideoWidth, taterAthenaVideoHeight
	const cropWidth, cropHeight, cropX, cropY = 1040, 780, 8, 2
	const pixels = width * height
	if len(raw) != stride*sensorHeight || tables == nil {
		return nil
	}
	if cap(reuse) < pixels*3/2 {
		reuse = make([]byte, pixels*3/2)
	} else {
		reuse = reuse[:pixels*3/2]
	}
	var columns [width]int
	for x := range columns {
		columns[x] = 2 * (cropX + x*cropWidth/width)
	}
	// Four independent row bands avoid putting the entire conversion on one
	// core. Every worker owns complete 2x2 chroma cells and disjoint output.
	// No full-size RGB intermediate or unbounded frame queue is introduced.
	var workers sync.WaitGroup
	for band := 0; band < 4; band++ {
		y0, y1 := band*height/4, (band+1)*height/4
		if band == 3 {
			renderTaterAthenaNV12Band(raw, tables, reuse, &columns, y0, y1)
		} else {
			workers.Add(1)
			go func() {
				defer workers.Done()
				renderTaterAthenaNV12Band(raw, tables, reuse, &columns, y0, y1)
			}()
		}
	}
	workers.Wait()
	return reuse
}

func renderTaterAthenaNV12Band(raw []byte, tables *[3][2048]uint8, out []byte, columns *[640]int, y0, y1 int) {
	const width, height = taterAthenaVideoWidth, taterAthenaVideoHeight
	const stride, cropHeight, cropY = 2112 * 5 / 4, 780, 2
	const pixels = width * height
	for y := y0; y < y1; y += 2 {
		for x := 0; x < width; x += 2 {
			sumR, sumG, sumB := 0, 0, 0
			for dy := 0; dy < 2; dy++ {
				sy := 2 * (cropY + (y+dy)*cropHeight/height)
				row0, row1 := raw[sy*stride:(sy+1)*stride], raw[(sy+1)*stride:(sy+2)*stride]
				for dx := 0; dx < 2; dx++ {
					sx := columns[x+dx]
					b := packed10Sample(row0, sx)
					g := packed10Sample(row0, sx+1) + packed10Sample(row1, sx)
					r := packed10Sample(row1, sx+1)
					r8, g8, b8 := int(tables[0][r*2]), int(tables[1][g]), int(tables[2][b*2])
					out[(y+dy)*width+x+dx] = byte(16 + ((66*r8 + 129*g8 + 25*b8 + 128) >> 8))
					sumR, sumG, sumB = sumR+r8, sumG+g8, sumB+b8
				}
			}
			uv := pixels + y/2*width + x
			out[uv] = byte(128 + ((-38*sumR - 74*sumG + 112*sumB + 512) >> 10))
			out[uv+1] = byte(128 + ((112*sumR - 94*sumG - 18*sumB + 512) >> 10))
		}
	}
}
