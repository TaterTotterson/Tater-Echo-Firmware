package taternative

import "strings"

// DisplayVideo is the bounded, display-ready playback state supplied by
// Tater. Provider credentials remain embedded only in the ephemeral URL and
// are never logged by the firmware.
type DisplayVideo struct {
	Active     bool
	SessionID  string
	URL        string
	Title      string
	Subtitle   string
	State      string
	PositionMS int64
	DurationMS int64
	FPS        int
	Loop       bool
	Live       bool
	ServerSeek bool
	Error      string
}

func decodeDisplayVideo(payload map[string]any) DisplayVideo {
	state := strings.ToLower(strings.TrimSpace(stringValue(payload["state"])))
	if state == "" {
		state = "playing"
	}
	fps := int(numberValue(payload["fps"], 30))
	if fps != 24 && fps != 30 {
		fps = 30
	}
	position := int64(numberValue(payload["position_ms"], 0))
	duration := int64(numberValue(payload["duration_ms"], 0))
	if position < 0 {
		position = 0
	}
	if duration < 0 {
		duration = 0
	}
	return DisplayVideo{
		Active: boolValue(payload["active"]), SessionID: stringValue(payload["session_id"]),
		URL: stringValue(payload["url"]), Title: stringValue(payload["title"]),
		Subtitle: stringValue(payload["subtitle"]), State: state,
		PositionMS: position, DurationMS: duration, FPS: fps,
		Loop: boolValue(payload["loop"]), Live: boolValue(payload["live"]),
		ServerSeek: boolValue(payload["server_seek"]),
		Error:      stringValue(payload["error"]),
	}
}

func (c *Client) VideoAction(action, sessionID string, positionMS int64, detail string) bool {
	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case "pause", "resume", "stop", "progress", "ended", "error":
	default:
		return false
	}
	payload := map[string]any{"session_id": sessionID, "position_ms": max(positionMS, 0)}
	if detail != "" {
		payload["error"] = detail
	}
	return c.sendJSON("display.video."+action, "", payload)
}
