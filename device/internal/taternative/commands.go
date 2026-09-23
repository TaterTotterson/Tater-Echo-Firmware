package taternative

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
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
		c.stopOverlay(true)
		c.stopScene(true)
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
	case "audio.clock.sync":
		receivedUS := monotonicMicros()
		c.sendJSON("audio.clock.sync.result", "", map[string]any{
			"reply_to": message.ID, "ok": true,
			"server_send_us":       int64Value(payload["server_send_us"], 0),
			"satellite_receive_us": receivedUS,
			"satellite_send_us":    monotonicMicros(),
		})
	case "audio.overlay.start":
		foreground, _ := payload["foreground"].(map[string]any)
		ducking, _ := payload["ducking"].(map[string]any)
		finish, _ := payload["finish"].(map[string]any)
		req := OverlayRequest{
			OverlayID:             stringValue(payload["overlay_id"]),
			GroupID:               stringValue(payload["group_id"]),
			URL:                   stringValue(foreground["url"]),
			Kind:                  stringValue(foreground["kind"]),
			VolumePercent:         clamp(intValue(foreground["volume_percent"], 100), 0, 100),
			DuckingTargetPercent:  clamp(intValue(ducking["target_percent"], 20), 0, 100),
			DuckingAttack:         time.Duration(clamp(intValue(ducking["attack_ms"], 150), 0, 10000)) * time.Millisecond,
			DuckingRelease:        time.Duration(clamp(intValue(ducking["release_ms"], 350), 0, 10000)) * time.Millisecond,
			StopMediaWhenFinished: boolValue(finish["stop_media"]),
			BackgroundFadeOut:     time.Duration(clamp(intValue(finish["fade_ms"], 0), 0, 10000)) * time.Millisecond,
			StartAtUS:             int64Value(payload["start_at_us"], 0),
			ContinueConversation:  boolValue(payload["continue_conversation"]),
			ConversationID:        stringValue(payload["conversation_id"]),
		}
		if req.OverlayID == "" {
			req.OverlayID = message.ID
		}
		if req.Kind == "" {
			req.Kind = "tts"
		}
		if req.URL == "" {
			c.sendJSON("audio.overlay.finished", "", map[string]any{
				"overlay_id": req.OverlayID, "group_id": req.GroupID,
				"ok": false, "error": "foreground.url is required",
			})
			return
		}
		c.startOverlay(req)
	case "audio.scene.start":
		foreground, _ := payload["foreground"].(map[string]any)
		background, _ := payload["background"].(map[string]any)
		ducking, _ := payload["ducking"].(map[string]any)
		finish, _ := payload["finish"].(map[string]any)
		req := SceneRequest{
			SceneID:                 stringValue(payload["scene_id"]),
			ForegroundURL:           stringValue(foreground["url"]),
			ForegroundKind:          stringValue(foreground["kind"]),
			ForegroundVolumePercent: clamp(intValue(foreground["volume_percent"], 100), 0, 100),
			BackgroundURL:           stringValue(background["url"]),
			BackgroundVolumePercent: clamp(intValue(background["volume_percent"], 100), 0, 100),
			BackgroundLoop:          boolValueDefault(background["loop"], true),
			DuckingTargetPercent:    clamp(intValue(ducking["target_percent"], 20), 0, 100),
			DuckingAttack:           time.Duration(clamp(intValue(ducking["attack_ms"], 150), 0, 10000)) * time.Millisecond,
			DuckingRelease:          time.Duration(clamp(intValue(ducking["release_ms"], 350), 0, 10000)) * time.Millisecond,
			BackgroundFadeOut:       time.Duration(clamp(intValue(finish["fade_ms"], 350), 0, 10000)) * time.Millisecond,
		}
		if req.SceneID == "" {
			req.SceneID = message.ID
		}
		if req.ForegroundKind == "" {
			req.ForegroundKind = "tts"
		}
		if req.ForegroundURL == "" {
			c.sendJSON("audio.scene.finished", "", map[string]any{
				"scene_id": req.SceneID, "ok": false, "error": "foreground.url is required",
			})
			return
		}
		c.startScene(req)
	case "audio.scene.stop":
		c.stopScene(true)
	case "media.session.start", "media.session.prepare":
		media, _ := payload["media"].(map[string]any)
		routing, _ := payload["routing"].(map[string]any)
		req := MediaRequest{
			SessionID: stringValue(payload["session_id"]), GroupID: stringValue(payload["group_id"]),
			URL: stringValue(media["url"]), VolumePercent: intValue(media["volume_percent"], 100),
			StartPositionMS: intValue(media["start_position_ms"], 0), Loop: boolValue(media["loop"]),
			ContentType: stringValue(media["content_type"]), Title: stringValue(media["title"]),
			Artist: stringValue(media["artist"]), Album: stringValue(media["album"]),
			Channel: normalizedMediaChannel(stringValue(routing["channel"])),
		}
		if req.SessionID == "" || req.URL == "" {
			c.sendJSON("media.session.finished", message.ID, map[string]any{
				"session_id": req.SessionID, "group_id": req.GroupID, "ok": false,
				"reason": "session_id and media.url are required",
			})
			return
		}
		if message.Type == "media.session.prepare" {
			go c.prepareMedia(message.ID, req)
		} else {
			go c.startMedia(req)
		}
	case "media.session.commit":
		c.commitMedia(message.ID, stringValue(payload["session_id"]), int64Value(payload["start_at_us"], 0))
	case "media.session.adjust":
		err := error(nil)
		if hook := c.hooks.AdjustMedia; hook != nil {
			err = hook(
				stringValue(payload["session_id"]),
				clamp(intValue(payload["correction_frames"], 0), -480000, 480000),
				stringValue(payload["mode"]),
				time.Duration(clamp(intValue(payload["settle_ms"], 0), 0, 10000))*time.Millisecond,
			)
		} else {
			err = fmt.Errorf("synchronized media adjustment unavailable")
		}
		result := map[string]any{"reply_to": message.ID, "ok": err == nil}
		if err != nil {
			result["error"] = err.Error()
		}
		c.sendJSON("media.session.adjust.result", "", result)
	case "media.session.stop":
		c.stopMedia(stringValue(payload["session_id"]))
		c.setState("idle", payload)
	case "media.session.pause":
		if hook := c.hooks.PauseMedia; hook != nil {
			hook(stringValue(payload["session_id"]))
		}
	case "media.session.resume":
		if hook := c.hooks.ResumeMedia; hook != nil {
			hook(stringValue(payload["session_id"]))
		}
	case "media.session.volume":
		err := error(nil)
		if hook := c.hooks.VolumeMedia; hook != nil {
			hook(stringValue(payload["session_id"]), clamp(intValue(payload["volume_percent"], 100), 0, 100))
		} else {
			err = fmt.Errorf("media volume unavailable")
		}
		result := map[string]any{"reply_to": message.ID, "ok": err == nil}
		if err != nil {
			result["error"] = err.Error()
		}
		c.sendJSON("media.session.volume.result", "", result)
	case "setup.reset", "provisioning.reset":
		hook := c.hooks.SetupReset
		if hook == nil {
			c.sendJSON(message.Type+".ack", message.ID, map[string]any{"ok": false, "error": "setup reset unavailable"})
			return
		}
		// Queue the acknowledgement before touching persistent networking.
		// ResetToSetup gives the writer 500 ms before emOS performs its synced
		// reboot, while Tater intentionally forgets the old pairing at once.
		c.sendJSON(message.Type+".ack", message.ID, map[string]any{"ok": true})
		if err := hook(); err != nil {
			log.Printf("[tater-native] setup reset failed: %v", err)
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

func normalizedMediaChannel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "left":
		return "left"
	case "right":
		return "right"
	default:
		return "mono"
	}
}

func int64Value(value any, fallback int64) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return parsed
		}
	}
	return fallback
}

func boolValueDefault(value any, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return boolValue(value)
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
