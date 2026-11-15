//go:build !dot && !spot && !panel

package camera

import (
	"testing"
)

func TestAthenaNV12MatchesIndependentNativeRGBSamples(t *testing.T) {
	useSensor(t, athenaSensor)
	raw := make([]byte, frameBytes)
	for i := range raw {
		raw[i] = byte(i*37 + i/11)
	}
	level := tone{gainR: 1.17, gainB: 0.83, black: 137, white: 1837, gamma: 0.48}
	got := renderTaterAthenaNV12(raw, level.tables(), nil)
	if len(got) != 640*480*3/2 {
		t.Fatal("invalid NV12 size")
	}
	reference := render(raw, level)
	for y := 0; y < 480; y += 2 {
		for x := 0; x < 640; x += 2 {
			sums := [3]int{}
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					p := reference.RGBAAt(8+(x+dx)*1040/640, 2+(y+dy)*780/480)
					r, g, b := int(p.R), int(p.G), int(p.B)
					wantY := byte(16 + ((66*r + 129*g + 25*b + 128) >> 8))
					if got[(y+dy)*640+x+dx] != wantY {
						t.Fatalf("Y differs at %d,%d", x+dx, y+dy)
					}
					sums[0], sums[1], sums[2] = sums[0]+r, sums[1]+g, sums[2]+b
				}
			}
			u := byte(128 + ((-38*sums[0] - 74*sums[1] + 112*sums[2] + 512) >> 10))
			v := byte(128 + ((112*sums[0] - 94*sums[1] - 18*sums[2] + 512) >> 10))
			uv := 640*480 + y/2*640 + x
			if got[uv] != u || got[uv+1] != v {
				t.Fatalf("UV differs at %d,%d", x, y)
			}
		}
	}
	if again := renderTaterAthenaNV12(raw, level.tables(), got); &again[0] != &got[0] {
		t.Fatal("NV12 output was not reused")
	}
	for _, invalid := range [][]byte{nil, raw[:len(raw)-1], append(raw, 0)} {
		if renderTaterAthenaNV12(invalid, level.tables(), nil) != nil {
			t.Fatal("accepted wrong raw size")
		}
	}
	if renderTaterAthenaNV12(raw, nil, nil) != nil {
		t.Fatal("accepted absent tone tables")
	}
}

func TestAthenaNV12KnownColorsAndChromaOrder(t *testing.T) {
	// Constant LUTs isolate YUV packing from Bayer/tone sampling. The values
	// below are independent BT.601 reference triplets, including red/blue.
	for _, color := range []struct {
		rgb     [3]uint8
		y, u, v byte
	}{
		{[3]uint8{0, 0, 0}, 16, 128, 128},
		{[3]uint8{255, 255, 255}, 235, 128, 128},
		{[3]uint8{255, 0, 0}, 82, 90, 240},
		{[3]uint8{0, 255, 0}, 144, 54, 34},
		{[3]uint8{0, 0, 255}, 41, 240, 110},
	} {
		var tables [3][2048]uint8
		for ch := range tables {
			for i := range tables[ch] {
				tables[ch][i] = color.rgb[ch]
			}
		}
		out := renderTaterAthenaNV12(make([]byte, 2112*1568*5/4), &tables, nil)
		for i, value := range out {
			want := color.y
			if i >= 640*480 {
				if i%2 == 0 {
					want = color.u
				} else {
					want = color.v
				}
			}
			if value != want {
				t.Fatalf("RGB %v byte %d = %d, want %d", color.rgb, i, value, want)
			}
		}
	}
}

func BenchmarkAthenaNV12(b *testing.B) {
	raw := make([]byte, 2112*1568*5/4)
	tables := (&tone{gainR: 1, gainB: 1, white: 2047, gamma: 1}).tables()
	out := make([]byte, 640*480*3/2)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out = renderTaterAthenaNV12(raw, tables, out)
	}
}
