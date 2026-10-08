package sendspin

import "strings"

const (
	outputChannelStereo int32 = iota
	outputChannelLeft
	outputChannelRight
	outputChannelMono
)

var outputChannelNames = [...]string{"stereo", "left", "right", "mono"}

// SetOutputChannel selects the side rendered by this physical Echo. Stereo
// and mono both fold the stream down; left/right preserve Tater stereo-pair
// assignments while every member receives the same synchronized timeline.
func (c *Client) SetOutputChannel(mode string) bool {
	value, normalized, ok := parseOutputChannel(mode)
	if !ok {
		return false
	}
	c.outputChannel.Store(value)
	settings := c.store.settings()
	if settings.OutputChannel != normalized {
		settings.OutputChannel = normalized
		c.store.setSettings(settings)
	}
	c.changed()
	return true
}

func (c *Client) OutputChannel() string {
	value := c.outputChannel.Load()
	if value < 0 || int(value) >= len(outputChannelNames) {
		return outputChannelNames[outputChannelStereo]
	}
	return outputChannelNames[value]
}

func parseOutputChannel(mode string) (int32, string, bool) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	for value, name := range outputChannelNames {
		if mode == name {
			return int32(value), name, true
		}
	}
	return outputChannelStereo, "", false
}
