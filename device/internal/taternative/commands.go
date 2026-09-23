package taternative

import (
	"context"
	"log"
	"strings"
)

func (c *Client) handle(message Envelope) {
	payload := message.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	if c.timers.Handle(message.Type, message.ID, payload) {
		return
	}
	switch message.Type {
	case "settings":
		applied := payload
		result := map[string]any{"ok": true}
		if hook := c.hooks.Settings; hook != nil {
			var err error
			applied, err = hook(payload)
			if err != nil {
				result["ok"] = false
				result["error"] = err.Error()
			}
		}
		c.stateMu.Lock()
		c.settings = copyMap(applied)
		c.stateMu.Unlock()
		result["settings"] = applied
		c.sendJSON("settings.changed", message.ID, result)
	case "state":
		c.setState(stringValue(payload["state"]), payload)
	case "voice.start.ack":
		c.handleVoiceStartAck(payload)
	case "wake.verify.result":
		c.handleWakeVerificationResult(payload)
	case "voice.event":
		event := strings.ToUpper(stringValue(payload["event"]))
		switch event {
		case "STT_VAD_END", "STT_END":
			c.stopVoiceCapture()
			c.setState("thinking", payload)
		case "INTENT_START":
			c.setState("thinking", payload)
		case "INTENT_END":
			data, _ := payload["data"].(map[string]any)
			c.setPendingReopen(
				boolValue(data["continue_conversation"]),
				stringValue(data["conversation_id"]),
			)
			c.setState("thinking", payload)
		case "TOOL_CALL_START":
			c.setState("tool_call", payload)
		case "TTS_START", "TTS_END":
			c.setState("speaking", payload)
		case "RUN_END":
			// playback.finished completes the old run just before a continued
			// voice.start opens the next one. A late RUN_END from that old run
			// must not close the newly pending/active microphone or clear its DOA
			// listening animation.
			if c.voiceCaptureInProgress() {
				break
			}
			c.stopVoiceCapture()
			if !c.playbackTurnInProgress() {
				c.setPendingReopen(false, "")
				c.setState("idle", payload)
			}
		case "ERROR":
			c.stopVoiceCapture()
			c.setPendingReopen(false, "")
			c.setState("error", payload)
		}
	case "play.url":
		req := PlayRequest{
			URL: stringValue(payload["url"]), TTSKind: stringValue(payload["tts_kind"]),
			StateAfter:           stringValue(payload["state_after"]),
			ContinueConversation: boolValue(payload["continue_conversation"]),
			ConversationID:       stringValue(payload["conversation_id"]),
		}
		if pending, conversationID := c.pendingReopenSnapshot(); pending {
			req.ContinueConversation = true
			if req.ConversationID == "" {
				req.ConversationID = conversationID
			}
		}
		if body, ok := payload["ducking"].(map[string]any); ok {
			req.Ducking = copyMap(body)
		}
		if req.URL == "" {
			c.sendJSON("playback.finished", message.ID, map[string]any{"ok": false, "error": "url is required"})
			return
		}
		if !c.queueVoice(req) {
			c.sendJSON("playback.finished", message.ID, map[string]any{"ok": false, "error": "voice playback queue full"})
		}
	case "play.stop":
		c.stopVoice()
		c.stopVoiceCapture()
		c.setState("idle", payload)
	case "play.tone":
		// Tone URLs use the same player when provided; otherwise acknowledge
		// without inventing a built-in asset that the Echo image may not have.
		if url := stringValue(payload["url"]); url != "" {
			if !c.queueVoice(PlayRequest{URL: url, TTSKind: "tone"}) {
				c.sendJSON("playback.finished", message.ID, map[string]any{"ok": false, "error": "voice playback queue full"})
			}
		} else {
			c.sendJSON("playback.finished", message.ID, map[string]any{"ok": true})
		}
	case "media.session.start":
		media, _ := payload["media"].(map[string]any)
		req := MediaRequest{
			SessionID: stringValue(payload["session_id"]), GroupID: stringValue(payload["group_id"]),
			URL: stringValue(media["url"]), VolumePercent: intValue(media["volume_percent"], 100),
			StartPositionMS: intValue(media["start_position_ms"], 0), Loop: boolValue(media["loop"]),
			ContentType: stringValue(media["content_type"]), Title: stringValue(media["title"]),
			Artist: stringValue(media["artist"]), Album: stringValue(media["album"]),
		}
		if req.SessionID == "" || req.URL == "" {
			c.sendJSON("media.session.finished", message.ID, map[string]any{
				"session_id": req.SessionID, "group_id": req.GroupID, "ok": false,
				"reason": "session_id and media.url are required",
			})
			return
		}
		go c.startMedia(req)
	case "media.session.stop":
		c.stopMedia(stringValue(payload["session_id"]))
	case "media.session.pause":
		if hook := c.hooks.PauseMedia; hook != nil {
			hook(stringValue(payload["session_id"]))
		}
	case "media.session.resume":
		if hook := c.hooks.ResumeMedia; hook != nil {
			hook(stringValue(payload["session_id"]))
		}
	case "media.session.volume":
		if hook := c.hooks.VolumeMedia; hook != nil {
			hook(stringValue(payload["session_id"]), clamp(intValue(payload["volume_percent"], 100), 0, 100))
		}
	case "ota.url":
		req := OTARequest{URL: stringValue(payload["url"]), SHA256: strings.ToLower(stringValue(payload["sha256"])), SizeBytes: int64(intValue(payload["size_bytes"], 0))}
		hook := c.hooks.OTA
		if hook == nil {
			c.sendJSON("ota.status", message.ID, map[string]any{"status": "error", "message": "OTA unavailable"})
			return
		}
		go func() {
			report := func(status string, progress int, detail string) {
				body := map[string]any{"status": status, "progress": clamp(progress, 0, 100)}
				if detail != "" {
					body["message"] = detail
				}
				c.sendJSON("ota.status", message.ID, body)
			}
			report("downloading", 0, "")
			if err := hook(context.Background(), req, report); err != nil {
				report("error", 0, err.Error())
				return
			}
			report("ready", 100, "Firmware installed; restarting")
		}()
	case "ping":
		c.sendJSON("pong", message.ID, map[string]any{"ok": true})
	case "error":
		log.Printf("[tater-native] server error: %s", stringValue(payload["error"]))
	}
}

func copyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func clamp(value, minimum, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}
