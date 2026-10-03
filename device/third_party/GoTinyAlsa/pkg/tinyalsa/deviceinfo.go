package tinyalsa

import "github.com/Binozo/GoTinyAlsa/pkg/pcm"

type DeviceInfo struct {
	// Output info (e.g. speaker)
	Out pcm.Info
	// Input info (e.g. microphone)
	In pcm.Info
}

// HasOutput returns true, if the given device has an output (e.g. speaker)
func (i *DeviceInfo) HasOutput() bool {
	return i.Out.Access != 0 && i.Out.RateMin != 0
}

// HasInput returns true, if the given device has an input (e.g. microphone)
func (i *DeviceInfo) HasInput() bool {
	return i.In.Access != 0 && i.In.RateMin != 0
}
