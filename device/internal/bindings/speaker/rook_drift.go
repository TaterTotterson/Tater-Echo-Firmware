package speaker

import "encoding/binary"

// Rook's I2S0 DL1 path shows an XRUN interval proportional to the square of
// the configured period size: about 2.3s at 768, 9.2s at 1536, and 16.4s at
// 2048. This is consistent with a 64-byte (16 stereo-frame) per-period drift
// in the vendor driver's ring accounting. Expand each mixed period by those
// 16 frames as a device-specific trial. Linear interpolation distributes the
// adjustment instead of inserting a repeated 0.33ms block at each boundary.
const (
	rookInputFrames  = 2048
	rookOutputFrames = rookInputFrames + rookInputFrames/128
)

type rookPlaybackDrift struct {
	buf     [rookOutputFrames * 4]byte
	last    [2]int16
	started bool
}

func (r *rookPlaybackDrift) expand(in []byte) []byte {
	if len(in) != rookInputFrames*4 {
		return in
	}
	if !r.started {
		r.last[0] = int16(binary.LittleEndian.Uint16(in[0:2]))
		r.last[1] = int16(binary.LittleEndian.Uint16(in[2:4]))
		r.started = true
	}
	for outFrame := 0; outFrame < rookOutputFrames; outFrame++ {
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
				if frame+1 < rookInputFrames {
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
		r.last[channel] = int16(binary.LittleEndian.Uint16(in[(rookInputFrames-1)*4+channel*2:]))
	}
	return r.buf[:]
}
