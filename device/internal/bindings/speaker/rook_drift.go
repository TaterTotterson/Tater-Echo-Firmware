package speaker

import "encoding/binary"

// The affected MediaTek I2S0 DL1 paths show a repeatable 16-stereo-frame
// playback drift for each 2048-frame mixer block. Expand each mixed period by
// those 16 frames. Linear interpolation distributes the adjustment instead of
// inserting a repeated 0.33ms block at each boundary.
const (
	driftInputFrames  = 2048
	driftOutputFrames = driftInputFrames + driftInputFrames/128
)

type playbackDriftCorrection struct {
	buf     [driftOutputFrames * 4]byte
	last    [2]int16
	started bool
}

// compressPlaybackDrift maps one hardware-accounting period back onto the
// mixer's ordinary 2048-frame period. Timestamped sources use it before the
// mixer, then expand applies the inverse mapping at the ALSA write. That lets
// Sendspin consume the full 2064 frames represented by the DAC clock without
// dropping 16 adjacent source samples at every period boundary.
func compressPlaybackDrift(out, in []byte) []byte {
	if len(in) != driftOutputFrames*4 || len(out) < driftInputFrames*4 {
		return nil
	}
	out = out[:driftInputFrames*4]
	for outFrame := 0; outFrame < driftInputFrames; outFrame++ {
		// This is the inverse of expand's 128/129 mapping. Offset by one
		// numerator unit so both mappings agree at the final frame.
		pos := outFrame*129 + 1
		frame := pos / 128
		fraction := pos % 128
		for channel := 0; channel < 2; channel++ {
			a := int32(int16(binary.LittleEndian.Uint16(in[frame*4+channel*2:])))
			b := a
			if frame+1 < driftOutputFrames {
				b = int32(int16(binary.LittleEndian.Uint16(in[(frame+1)*4+channel*2:])))
			}
			value := (a*int32(128-fraction) + b*int32(fraction)) / 128
			binary.LittleEndian.PutUint16(out[outFrame*4+channel*2:], uint16(int16(value)))
		}
	}
	return out
}

func (r *playbackDriftCorrection) expand(in []byte) []byte {
	if len(in) != driftInputFrames*4 {
		return in
	}
	if !r.started {
		r.last[0] = int16(binary.LittleEndian.Uint16(in[0:2]))
		r.last[1] = int16(binary.LittleEndian.Uint16(in[2:4]))
		r.started = true
	}
	for outFrame := 0; outFrame < driftOutputFrames; outFrame++ {
		// 2048 / 2064 = 128 / 129. Offset by one output sample so
		// the only cross-period interpolation uses the saved last frame.
		pos := (outFrame+1)*128 - 129
		for channel := 0; channel < 2; channel++ {
			var a, b int32
			var fraction int
			if pos < 0 {
				a = int32(r.last[channel])
				b = int32(int16(binary.LittleEndian.Uint16(in[channel*2:])))
				fraction = 128
			} else {
				frame := pos / 129
				fraction = pos % 129
				a = int32(int16(binary.LittleEndian.Uint16(in[frame*4+channel*2:])))
				if frame+1 < driftInputFrames {
					b = int32(int16(binary.LittleEndian.Uint16(in[(frame+1)*4+channel*2:])))
				} else {
					b = a
				}
			}
			value := (a*int32(129-fraction) + b*int32(fraction)) / 129
			binary.LittleEndian.PutUint16(r.buf[outFrame*4+channel*2:], uint16(int16(value)))
		}
	}
	for channel := 0; channel < 2; channel++ {
		r.last[channel] = int16(binary.LittleEndian.Uint16(in[(driftInputFrames-1)*4+channel*2:]))
	}
	return r.buf[:]
}
