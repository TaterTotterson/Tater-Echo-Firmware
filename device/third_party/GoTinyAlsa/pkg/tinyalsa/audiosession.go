package tinyalsa

import "github.com/Binozo/GoTinyAlsa/internal/tinyapi"

type AudioSession struct {
	pcmDevice tinyapi.PcmDevice
}

// NewAudioSession creates a new Audio session to stream audio data.
// Do not forget to call `Close()`
func (d *AlsaDevice) NewAudioSession() (AudioSession, error) {
	pcmDevice, err := tinyapi.PcmOpen(d.Card, d.Device, PCM_OUT, d.DeviceConfig)
	if err != nil {
		return AudioSession{}, err
	}

	return AudioSession{
		pcmDevice: pcmDevice,
	}, nil
}

// Close the open pcm device and free it for other use
func (a *AudioSession) Close() {
	a.pcmDevice.Close()
}

// BufferSize returns the buffer size expected by the device
func (a *AudioSession) BufferSize() int {
	return a.pcmDevice.FrameBytesSize()
}

// Pump the audio data into the device.
func (a *AudioSession) Pump(data []byte) error {
	return a.pcmDevice.WriteFrames(data, len(data))
}
