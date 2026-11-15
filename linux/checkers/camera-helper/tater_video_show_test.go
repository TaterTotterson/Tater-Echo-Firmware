//go:build !spot && !dot

package camera

import (
	"image"
	"testing"
)

func TestRenderTaterEchoVideoMatchesSampledNativeImage(t *testing.T) {
	raw := make([]byte, frameBytes)
	for index := range raw {
		raw[index] = byte(index*37 + index/11)
	}
	level := tone{gainR: 1, gainB: 1, white: 2047, gamma: 1}
	got := renderTaterEchoVideo(raw, level, nil)
	if got == nil {
		t.Fatal("direct Sat Chat frame is nil")
	}
	if got.Bounds().Dx() != 320 || got.Bounds().Dy() != 180 {
		t.Fatalf("direct Sat Chat frame = %v, want 320x180", got.Bounds())
	}
	reference := render(raw, level)
	cropWidth := Width
	cropHeight := cropWidth * 9 / 16
	if cropHeight > Height {
		cropHeight = Height
		cropWidth = cropHeight * 16 / 9
	}
	cropX := (Width - cropWidth) / 2
	cropY := (Height - cropHeight) / 2
	for _, point := range [][2]int{{0, 0}, {1, 1}, {79, 44}, {160, 90}, {319, 179}} {
		x, y := point[0], point[1]
		want := reference.RGBAAt(cropX+x*cropWidth/320, cropY+y*cropHeight/180)
		if actual := got.RGBAAt(x, y); actual != want {
			t.Fatalf("direct pixel (%d,%d) = %v, want native sample %v", x, y, actual, want)
		}
	}
	if reused := renderTaterEchoVideo(raw, level, got); reused != got {
		t.Fatal("direct Sat Chat renderer did not reuse its frame buffer")
	}
}

func TestRenderTaterEchoVideoRejectsShortRawFrame(t *testing.T) {
	if got := renderTaterEchoVideo(make([]byte, frameBytes-1), tone{}, nil); got != nil {
		t.Fatalf("short raw frame produced %v", got.Bounds())
	}
}

func TestSmoothVideoMatchesEverySampleForBothSensors(t *testing.T) {
	for _, checkers := range []bool{true, false} {
		name := "cronos"
		if checkers {
			name = "checkers"
		}
		t.Run(name, func(t *testing.T) {
			useSensor(t, pickSensor(checkers))
			raw := make([]byte, frameBytes)
			for index := range raw {
				raw[index] = byte(index*37 + index/11)
			}
			level := tone{gainR: 1.17, gainB: 0.83, black: 7, white: 1837, gamma: 0.48}
			frame := &Frame{raw: raw, tone: level, toned: true}
			got := frame.TaterSmoothVideo(nil)
			if got == nil || got.Bounds() != image.Rect(0, 0, 480, 270) {
				t.Fatal("invalid smooth geometry")
			}
			reference := render(raw, level)
			cropWidth, cropHeight := Width, Width*9/16
			if cropHeight > Height {
				cropWidth, cropHeight = Height*16/9, Height
			}
			cropX, cropY := (Width-cropWidth)/2, (Height-cropHeight)/2
			for y := 0; y < 270; y++ {
				for x := 0; x < 480; x++ {
					want := reference.RGBAAt(cropX+x*cropWidth/480, cropY+y*cropHeight/270)
					if actual := got.RGBAAt(x, y); actual != want {
						t.Fatalf("pixel %d,%d = %v, want %v", x, y, actual, want)
					}
				}
			}
			if reused := frame.TaterSmoothVideo(got); reused != got {
				t.Fatal("smooth capture did not reuse its buffer")
			}
			if renderTaterVideo(raw[:frameBytes-1], level, nil, 480, 270) != nil {
				t.Fatal("accepted truncated raw capture")
			}
			if renderTaterVideo(raw, level, nil, 0, 0) != nil {
				t.Fatal("accepted unbounded output geometry")
			}
			if frame.Full().Bounds().Size() != image.Pt(sensorW, sensorH) {
				t.Fatal("changed full-resolution stills")
			}
		})
	}
}
