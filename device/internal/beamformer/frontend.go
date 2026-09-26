package beamformer

import (
	"strings"
)

// FrontEnd is the target-specific conversion from a raw ALSA capture period
// to Tater's 16 kHz mono S16 pipeline. Biscuit implements seven-mic direction
// selection and an in-band echo reference; Checkers must not be decoded with
// that layout merely because both ADCs use packed 24-bit samples.
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

// NewForTarget returns the capture front end for a release target.
func NewForTarget(target string) FrontEnd {
	if strings.EqualFold(strings.TrimSpace(target), "checkers") {
		return NewCheckers()
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
var _ FrontEnd = (*CheckersFrontEnd)(nil)
