package beamformer

import (
	"strings"
)

// FrontEnd is the target-specific conversion from a raw ALSA capture period
// to Tater's 16 kHz mono S16 pipeline. Biscuit and Rook have different measured
// array geometries and in-band echo-reference layouts; Checkers must not be
// decoded with either merely because all three ADC paths use packed 24-bit
// samples.
type FrontEnd interface {
	Lock(enabled bool)
	PrepareSpeechLock()
	LockCurrent(enabled bool)
	Unlock()
	Process(raw []byte, steerAngle float64, gain float64) (mono []byte, angle float64)
	EchoRefInto(raw, dst []byte) []byte
	ClippedSamples() uint64
	ClippedByChannel() [7]uint64
	OutputChannel() int
	Diagnostics() Diagnostics
}

// WakeBeam is one independently scored wake-word view of the same raw array
// capture. Direction is a stable acoustic-path identity (0..5 on Biscuit,
// 0..3 on Rook, or that target's omni fallback path), not merely a display
// angle. Checkers uses 0/1 for its measured left/right acoustic paths and 2
// for its non-directional fallback.
type WakeBeam struct {
	Direction int
	Angle     float64
	PCM       []byte
}

// WakeArray is implemented only by front ends that can form multiple,
// independently useful wake-word beams from one capture. DataClient discovers
// it at runtime, so single-stream targets keep their existing scorer and audio
// path while Checkers, Rook, and Biscuit can use their measured arrays.
type WakeArray interface {
	WakeBeamCount() int
	WakeBeams(raw []byte, gain float64) []WakeBeam
	WakeBeam(raw []byte, direction int, gain float64) WakeBeam
	LockWakeDirection(direction int, enabled bool)
}

// NewForTarget returns the capture front end for a release target.
func NewForTarget(target string) FrontEnd {
	if strings.EqualFold(strings.TrimSpace(target), "checkers") {
		return NewCheckers()
	}
	if strings.EqualFold(strings.TrimSpace(target), "rook") {
		return NewRook()
	}
	return New()
}

func decodePackedS24(sample []byte) int32 {
	value := int32(sample[0]) | int32(sample[1])<<8 | int32(sample[2])<<16
	if value&0x800000 != 0 {
		value |= ^int32(0xFFFFFF)
	}
	return value
}

var _ FrontEnd = (*Beamformer)(nil)
var _ WakeArray = (*Beamformer)(nil)
var _ FrontEnd = (*CheckersFrontEnd)(nil)
var _ WakeArray = (*CheckersFrontEnd)(nil)
var _ FrontEnd = (*RookFrontEnd)(nil)
var _ WakeArray = (*RookFrontEnd)(nil)
