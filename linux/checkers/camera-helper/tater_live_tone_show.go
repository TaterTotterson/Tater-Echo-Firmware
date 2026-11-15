//go:build !dot && !spot && !panel

package camera

import (
	"math"
	"sync"
)

var taterToneCache struct {
	sync.Mutex
	gamma  float64
	powers *[2048]float64
	tone   tone
	tables *[3][2048]uint8
}

// The live encoder keeps the platform's exact black/white levels, white
// balance and gamma. Factor pow(k*scale,gamma) into pow(k,gamma)*pow(scale,gamma)
// so ordinary exposure/white-balance changes need three powers, not 6144.
// A gamma change rebuilds one 2048-entry table. No gamma quantization is used;
// the independently checked reassociation differs by at most one output LSB.
func (t tone) taterLiveTables() *[3][2048]uint8 {
	taterToneCache.Lock()
	defer taterToneCache.Unlock()
	if taterToneCache.tables != nil && taterToneCache.tone == t {
		return taterToneCache.tables
	}
	if taterToneCache.powers == nil || taterToneCache.gamma != t.gamma {
		powers := new([2048]float64)
		for n := range powers {
			powers[n] = math.Pow(float64(n), t.gamma)
		}
		taterToneCache.powers, taterToneCache.gamma = powers, t.gamma
	}
	result := new([3][2048]uint8)
	for ch, gain := range []float64{t.gainR, 1, t.gainB} {
		scale := math.Pow(gain/float64(t.white-t.black), t.gamma) * 255
		for value := 0; value < 2048; value++ {
			if value <= t.black {
				continue
			}
			adjusted := taterToneCache.powers[value-t.black] * scale
			if adjusted >= 255 {
				result[ch][value] = 255
			} else {
				result[ch][value] = uint8(adjusted + 0.5)
			}
		}
	}
	taterToneCache.tone, taterToneCache.tables = t, result
	return result
}
