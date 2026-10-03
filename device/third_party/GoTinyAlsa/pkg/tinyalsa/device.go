package tinyalsa

import (
	"fmt"
	"github.com/Binozo/GoTinyAlsa/internal/tinyapi"
	"github.com/Binozo/GoTinyAlsa/internal/tinypcm"
	"github.com/Binozo/GoTinyAlsa/pkg/pcm"
	"time"
)

type AlsaDevice struct {
	Card         int
	Device       int
	DeviceConfig pcm.Config
}

// NewDevice defines a new device you want to interact with
func NewDevice(cardNr int, deviceNr int, deviceConfig pcm.Config) AlsaDevice {
	return AlsaDevice{
		Card:         cardNr,
		Device:       deviceNr,
		DeviceConfig: deviceConfig,
	}
}

// BestDeviceConfig return you the best device config to use
func BestDeviceConfig(cardNr int, deviceNr int, format int) pcm.Config {
	device := AlsaDevice{
		Card:         cardNr,
		Device:       deviceNr,
		DeviceConfig: pcm.Config{},
	}
	info := device.GetInfo()
	infoData := info.In
	if format == PCM_IN {
		infoData = info.In
	} else {
		infoData = info.Out
	}
	return pcm.Config{
		Channels:    int(infoData.ChannelsMax),
		SampleRate:  int(infoData.RateMax),
		PeriodSize:  int(infoData.PeriodSizeMax),
		PeriodCount: int(infoData.PeriodCountMax),
		Format:      tinypcm.Format(format),
	}
}

// GetInfo returns information data about the given device's input & output
func (d *AlsaDevice) GetInfo() DeviceInfo {
	inInfo, outInfo := tinyapi.GetParams(d.Card, d.Device)
	return DeviceInfo{
		In:  inInfo,
		Out: outInfo,
	}
}

// IsReady returns if the device is ready
func (d *AlsaDevice) IsReady(format int) (bool, error) {
	pcmDevice, err := tinyapi.PcmOpen(d.Card, d.Device, format, d.DeviceConfig)
	if err != nil {
		return false, err
	}
	defer pcmDevice.Close()
	return pcmDevice.IsReady(), nil
}

// WaitUntilReady waits until the device is ready or the timeout expired
// If successful, nil will be returned. Otherwise, an error
func (d *AlsaDevice) WaitUntilReady(format int, timeout time.Duration) error {
	pcmDevice, err := tinyapi.PcmOpen(d.Card, d.Device, format, d.DeviceConfig)
	if err != nil {
		return err
	}
	defer pcmDevice.Close()
	return pcmDevice.WaitUntilReady(timeout)
}

// GetLocation returns the device's linux /dev location (e.g. /dev/snd/pcmC0D0p)
func (d *AlsaDevice) GetLocation(format int) string {
	audioType := "c" // capture
	if format == PCM_OUT {
		audioType = "p"
	}
	return fmt.Sprintf("/dev/snd/pcmC%dD%d%s", d.Card, d.Device, audioType)
}
