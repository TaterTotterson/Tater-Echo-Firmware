//go:build spot

package camera

// The encoder lease uses the existing GC0312 driver and privacy/power lifecycle,
// with motion-priority exposure while streaming. No second owner or ISP job is created.
func (c *Camera) AcquireTaterH264() (func(), error) { return c.AcquireTaterLiveVideo() }

type TaterEncoderInput struct{}

func (p *TaterEncoderInput) Prepare(f *Frame, reuse []byte, refreshTone bool) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.raw) != sensorW*sensorH {
		return nil
	}
	const tableBytes = 3072
	size := tableBytes + len(f.raw)
	if cap(reuse) < size {
		reuse, refreshTone = make([]byte, size), true
	} else {
		reuse = reuse[:size]
	}
	if refreshTone {
		t := f.level()
		for channel, gain := range []float64{t.gainR, 1, t.gainB} {
			table := taterVideoLUT(t, gain)
			copy(reuse[channel*1024:], table[:])
		}
	}
	copy(reuse[tableBytes:], f.raw)
	return reuse
}
