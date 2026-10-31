package taternative

import (
	"strings"
	"time"
)

const dndMonitorInterval = time.Second

// DNDActive reports the effective per-satellite do-not-disturb state. A manual
// enable and the optional daily local-time window are additive: either one can
// keep DND active. Equal start/end times intentionally mean all day.
func (c *Client) DNDActive() bool {
	if c == nil {
		return false
	}
	c.stateMu.RLock()
	settings := copyMap(c.settings)
	c.stateMu.RUnlock()
	return dndActiveAt(settings, time.Now())
}

func dndActiveAt(settings map[string]any, now time.Time) bool {
	if boolValue(settings["dnd_enabled"]) {
		return true
	}
	if !boolValue(settings["dnd_schedule_enabled"]) {
		return false
	}
	start, startOK := dndClockMinute(settings["dnd_start"])
	end, endOK := dndClockMinute(settings["dnd_end"])
	if !startOK || !endOK {
		return false
	}
	local := now
	if rawOffset, ok := settings["utc_offset_seconds"]; ok {
		offset := clamp(intValue(rawOffset, 0), -18*60*60, 18*60*60)
		local = now.UTC().Add(time.Duration(offset) * time.Second)
	}
	minute := local.Hour()*60 + local.Minute()
	if start == end {
		return true
	}
	if start < end {
		return minute >= start && minute < end
	}
	return minute >= start || minute < end
}

func dndClockMinute(value any) (int, bool) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(stringValue(value)))
	if err != nil {
		return 0, false
	}
	return parsed.Hour()*60 + parsed.Minute(), true
}

func (c *Client) syncDNDState() bool {
	active := c.DNDActive()
	previous := c.dndState.Swap(active)
	if previous == active {
		return active
	}
	if active {
		c.stopOverlay(true)
		c.stopScene(true)
		c.stopVoice()
		c.setState("idle", map[string]any{"dnd_active": true})
	}
	if hook := c.hooks.DNDChanged; hook != nil {
		hook(active)
	}
	return active
}

func (c *Client) dndMonitor() {
	ticker := time.NewTicker(dndMonitorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.syncDNDState()
		}
	}
}

func (c *Client) rejectDNDPlayback(messageType, replyTo string, payload map[string]any) bool {
	if !c.syncDNDState() {
		return false
	}
	result := copyMap(payload)
	result["ok"] = false
	result["error"] = "do not disturb is active"
	result["dnd_active"] = true
	c.sendJSON(messageType, replyTo, result)
	return true
}
