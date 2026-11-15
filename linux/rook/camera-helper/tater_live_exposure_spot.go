//go:build spot

package camera

import (
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
)

var taterLiveVideoUsers atomic.Int32

func taterUseLiveExposurePolicy() bool { return true }

// Motion exposure is a stream lease, not a saved preference or a sensor mode.
// Existing still acquisition and the physical privacy lifecycle stay intact.
func (c *Camera) AcquireTaterLiveVideo() (func(), error) {
	taterLiveVideoUsers.Add(1)
	release, err := c.Acquire()
	if err != nil {
		taterLiveVideoUsers.Add(-1)
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { taterLiveVideoUsers.Add(-1); release() }) }, nil
}

func taterRookExposure(shutter, gain, mean int, live bool) (int, int) {
	midGain, maxShutter := aeMidGain, aeMaxShut
	if live {
		midGain, maxShutter = aeMaxGain, aeFrame
	}
	ratio := math.Min(math.Max(float64(aeTarget)/float64(max(mean, 1)), .5), 2)
	want := float64(shutter*gain) * ratio
	switch {
	case want <= float64(aeFrame*aeMinGain):
		shutter, gain = int(want/aeMinGain), aeMinGain
	case want <= float64(aeFrame*midGain):
		shutter, gain = aeFrame, int(want/aeFrame)
	case want <= float64(maxShutter*midGain):
		shutter, gain = int(want/float64(midGain)), midGain
	default:
		shutter, gain = maxShutter, int(want/float64(maxShutter))
	}
	return min(max(shutter, aeMinShut), maxShutter), min(max(gain, aeMinGain), aeMaxGain)
}

func (d *device) taterAutoExpose(raw []byte) {
	if d.settle > 0 {
		d.settle--
		return
	}
	live := taterLiveVideoUsers.Load() > 0
	changed := d.taterLiveExposure != live
	d.taterLiveExposure = live
	mean := meter(raw)
	if d.hold.held(float64(mean)) && !changed {
		return
	}
	if mean >= aeTarget-aeDeadband && mean <= aeTarget+aeDeadband && !changed {
		return
	}
	shutter, gain := taterRookExposure(d.shutter, d.gain, mean, live)
	if shutter != d.shutter {
		d.setFeature(featShutter, uint64(shutter))
	}
	if gain != d.gain {
		d.setFeature(featGain, uint64(gain))
	}
	if shutter != d.shutter || gain != d.gain {
		slog.Info("[rook-exposure]", "live", live, "mean", mean, "shutter", shutter, "gain", gain)
		d.shutter, d.gain, d.settle = shutter, gain, aeDelay
	}
}
