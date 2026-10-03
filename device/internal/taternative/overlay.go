package taternative

import (
	"context"
	"errors"
)

func (c *Client) startOverlay(req OverlayRequest) {
	c.stopOverlay(false)
	c.playMu.Lock()
	ctx, cancel := context.WithCancel(c.ctx)
	c.overlayCancel = cancel
	c.overlayGen++
	generation := c.overlayGen
	c.overlayID = req.OverlayID
	c.overlayGroup = req.GroupID
	c.playMu.Unlock()

	go func() {
		startedSent := false
		onStarted := func() {
			c.playMu.Lock()
			current := c.overlayGen == generation && c.overlayID == req.OverlayID
			c.playMu.Unlock()
			if !current || startedSent {
				return
			}
			startedSent = true
			c.sendJSON("audio.overlay.started", "", map[string]any{
				"overlay_id": req.OverlayID, "group_id": req.GroupID,
				"actual_start_us":    monotonicMicros(),
				"scheduled_start_us": req.StartAtUS,
			})
			c.setState("speaking", map[string]any{"audio_overlay": true, "overlay_id": req.OverlayID})
		}
		var err error
		if hook := c.hooks.PlayOverlay; hook != nil {
			err = hook(ctx, req, onStarted)
		} else {
			err = errors.New("audio overlays unavailable")
		}
		c.playMu.Lock()
		current := c.overlayGen == generation && c.overlayID == req.OverlayID
		if current {
			c.overlayCancel = nil
			c.overlayID = ""
			c.overlayGroup = ""
		}
		mediaActive := c.mediaID != ""
		c.playMu.Unlock()
		if !current {
			return
		}
		ok := err == nil
		payload := map[string]any{
			"overlay_id": req.OverlayID, "group_id": req.GroupID, "ok": ok,
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			payload["error"] = err.Error()
		}
		c.sendJSON("audio.overlay.finished", "", payload)
		c.sendJSON("playback.finished", "", map[string]any{"ok": ok, "overlay_id": req.OverlayID})
		c.stateMu.RLock()
		continuedChatEnabled := boolValue(c.settings["continued_chat"])
		c.stateMu.RUnlock()
		if req.ContinueConversation && ok && continuedChatEnabled && req.ConversationID != "" {
			c.setState("listening", map[string]any{"source": "continued_chat"})
			if !c.startContinued(req.ConversationID) {
				c.setState("idle", map[string]any{"continued_chat": false})
			}
		} else if mediaActive {
			c.setState("playing", payload)
		} else {
			c.setState("idle", payload)
		}
	}()
}

func (c *Client) stopOverlay(report bool) {
	c.playMu.Lock()
	id := c.overlayID
	group := c.overlayGroup
	if c.overlayCancel != nil {
		c.overlayCancel()
		c.overlayCancel = nil
	}
	c.overlayGen++
	c.overlayID = ""
	c.overlayGroup = ""
	c.playMu.Unlock()
	if report && id != "" {
		c.sendJSON("audio.overlay.finished", "", map[string]any{
			"overlay_id": id, "group_id": group, "ok": false, "stopped": true,
		})
	}
}

func (c *Client) startScene(req SceneRequest) {
	c.stopScene(false)
	c.stopOverlay(false)
	c.stopMedia("")
	c.playMu.Lock()
	ctx, cancel := context.WithCancel(c.ctx)
	c.sceneCancel = cancel
	c.sceneGen++
	generation := c.sceneGen
	c.sceneID = req.SceneID
	c.playMu.Unlock()
	c.setState("speaking", map[string]any{"audio_scene": true, "scene_id": req.SceneID})
	go func() {
		var err error
		if hook := c.hooks.PlayScene; hook != nil {
			err = hook(ctx, req)
		} else {
			err = errors.New("audio scenes unavailable")
		}
		c.playMu.Lock()
		current := c.sceneGen == generation && c.sceneID == req.SceneID
		if current {
			c.sceneCancel = nil
			c.sceneID = ""
		}
		c.playMu.Unlock()
		if !current {
			return
		}
		ok := err == nil
		payload := map[string]any{"scene_id": req.SceneID, "ok": ok}
		if err != nil && !errors.Is(err, context.Canceled) {
			payload["error"] = err.Error()
		}
		c.sendJSON("audio.scene.finished", "", payload)
		c.sendJSON("playback.finished", "", map[string]any{"ok": ok, "scene_id": req.SceneID})
		c.setState("idle", payload)
	}()
}

func (c *Client) stopScene(report bool) {
	c.playMu.Lock()
	id := c.sceneID
	if c.sceneCancel != nil {
		c.sceneCancel()
		c.sceneCancel = nil
	}
	c.sceneGen++
	c.sceneID = ""
	c.playMu.Unlock()
	if report && id != "" {
		c.sendJSON("audio.scene.finished", "", map[string]any{
			"scene_id": id, "ok": false, "stopped": true,
		})
	}
}
