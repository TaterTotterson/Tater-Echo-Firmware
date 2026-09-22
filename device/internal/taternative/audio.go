package taternative

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync/atomic"
	"time"
)

const ttsSegmentGrace = 650 * time.Millisecond

// PushAudio accepts one processed 16 kHz mono S16_LE chunk. It always updates
// the pre-roll ring and only transmits while a native voice turn is pending or
// active. The caller may reuse pcm as soon as this method returns.
func (c *Client) PushAudio(pcm []byte) {
	usable := len(pcm) - len(pcm)%2
	if usable == 0 {
		return
	}
	frame := append([]byte(nil), pcm[:usable]...)
	c.audioMu.Lock()
	if len(c.preRoll) == cap(c.preRoll) {
		copy(c.preRoll, c.preRoll[1:])
		c.preRoll[len(c.preRoll)-1] = frame
	} else {
		c.preRoll = append(c.preRoll, frame)
	}
	if c.voicePending {
		if len(c.pendingAudio) >= preRollChunks {
			copy(c.pendingAudio, c.pendingAudio[1:])
			c.pendingAudio[len(c.pendingAudio)-1] = frame
			atomic.AddUint64(&c.audioDropped, 1)
		} else {
			c.pendingAudio = append(c.pendingAudio, frame)
		}
		c.audioMu.Unlock()
		return
	}
	active := c.voiceActive
	c.audioMu.Unlock()
	if active {
		c.sendBinary(frame)
	}
}

// Wake starts a local-wake turn and snapshots the complete pre-roll ring.
// False means the client was disconnected or another voice turn already owns
// the pipeline.
func (c *Client) Wake(wakeWord string, score float32) bool {
	if !c.connected.Load() {
		return false
	}
	c.stateMu.RLock()
	bargeIn := boolValue(c.settings["barge_in_enabled"])
	speaking := c.state == "speaking"
	verifierMode := strings.ToLower(stringValue(c.settings["wake_verifier_mode"]))
	c.stateMu.RUnlock()
	if speaking && !bargeIn {
		return false
	}
	switch verifierMode {
	case "observe":
		c.queueWakeVerification(strings.TrimSpace(wakeWord), score, false)
		return c.startWake(wakeWord, score)
	case "enforce":
		return c.queueWakeVerification(strings.TrimSpace(wakeWord), score, true)
	default:
		return c.startWake(wakeWord, score)
	}
}

func (c *Client) startWake(wakeWord string, score float32) bool {
	// A local wake during TTS is barge-in: cancel buffered voice playback
	// before opening the microphone turn. Persistent music stays alive and is
	// ducked by the eventual reply.
	c.stopVoice()
	return c.startVoice("local_wake", strings.TrimSpace(wakeWord), score, true)
}

// StartButton starts a push-to-talk turn without a wake phrase.
func (c *Client) StartButton() bool {
	// The physical action button is an explicit push-to-talk request and may
	// always interrupt TTS, even when hands-free barge-in is disabled.
	c.StopCapture(true)
	c.stopVoice()
	return c.startVoice("button", "", 0, false)
}

func (c *Client) startContinued() bool {
	return c.startVoice("continued_chat", "", 0, false)
}

func (c *Client) startVoice(source, wakeWord string, score float32, includePreRoll bool) bool {
	if !c.connected.Load() {
		return false
	}
	c.audioMu.Lock()
	if c.voicePending || c.voiceActive {
		c.wakeSuppressed++
		c.audioMu.Unlock()
		return false
	}
	c.voicePending = true
	c.pendingAudio = nil
	c.wakePreRoll = nil
	if includePreRoll {
		c.wakePreRoll = cloneFrames(c.preRoll)
	}
	c.audioMu.Unlock()
	payload := map[string]any{
		"wake_word": wakeWord, "source": source, "request_flags": 0,
		"audio_format": map[string]any{"rate": SampleRate, "width": SampleWidth, "channels": Channels},
	}
	if score > 0 {
		payload["wake_score"] = score
	}
	if !c.sendJSON("voice.start", "", payload) {
		c.audioMu.Lock()
		c.voicePending = false
		c.wakePreRoll = nil
		c.pendingAudio = nil
		c.audioMu.Unlock()
		return false
	}
	c.setState("listening", map[string]any{"source": source})
	return true
}

func cloneFrames(in [][]byte) [][]byte {
	out := make([][]byte, len(in))
	for i := range in {
		out[i] = append([]byte(nil), in[i]...)
	}
	return out
}

func (c *Client) handleVoiceStartAck(payload map[string]any) {
	c.audioMu.Lock()
	if !c.voicePending {
		c.audioMu.Unlock()
		return
	}
	c.voicePending = false
	if !boolValue(payload["ok"]) {
		c.voiceActive = false
		c.wakePreRoll = nil
		c.pendingAudio = nil
		c.audioMu.Unlock()
		c.setState("idle", payload)
		log.Printf("[tater-native] voice start rejected: %s", stringValue(payload["error"]))
		return
	}
	c.voiceActive = true
	frames := append(cloneFrames(c.wakePreRoll), cloneFrames(c.pendingAudio)...)
	c.wakePreRoll = nil
	c.pendingAudio = nil
	c.audioMu.Unlock()
	for _, frame := range frames {
		c.sendBinary(frame)
	}
}

func (c *Client) stopVoiceCapture() {
	c.audioMu.Lock()
	c.voicePending = false
	c.voiceActive = false
	c.wakePreRoll = nil
	c.pendingAudio = nil
	c.audioMu.Unlock()
}

// StopCapture closes a pending/active microphone turn and notifies Tater. It
// is used for hardware mute and push-to-talk replacement, where merely
// stopping ALSA would leave the server waiting for a VAD end that cannot come.
func (c *Client) StopCapture(abort bool) bool {
	c.audioMu.Lock()
	active := c.voicePending || c.voiceActive
	c.voicePending = false
	c.voiceActive = false
	c.wakePreRoll = nil
	c.pendingAudio = nil
	c.audioMu.Unlock()
	if !active {
		return false
	}
	return c.sendJSON("voice.stop", "", map[string]any{"abort": abort})
}

// queueVoice adds one TTS/tone segment to the current response. Tater may
// deliver a response as several adjacent play.url commands, so the worker
// serializes them and waits briefly before sending one playback.finished.
func (c *Client) queueVoice(req PlayRequest) bool {
	c.playMu.Lock()
	epoch := c.voiceGen
	c.playMu.Unlock()
	select {
	case c.voiceQueue <- queuedVoice{req: req, epoch: epoch}:
		return true
	default:
		log.Printf("[tater-native] voice playback queue full; dropping %q", req.URL)
		return false
	}
}

func (c *Client) voiceWorker() {
	var ready *queuedVoice
	for {
		var first queuedVoice
		if ready != nil {
			first = *ready
			ready = nil
		} else {
			select {
			case <-c.ctx.Done():
				return
			case <-c.voiceStop:
				continue
			case first = <-c.voiceQueue:
			}
		}
		ready = c.playVoiceResponse(first)
	}
}

// playVoiceResponse plays one logical response, which can contain multiple
// queued audio segments. A returned item belongs to a newer epoch and must be
// handled as the first segment of a new response.
func (c *Client) playVoiceResponse(first queuedVoice) *queuedVoice {
	generation := first.epoch
	c.playMu.Lock()
	if generation != c.voiceGen {
		c.playMu.Unlock()
		return nil
	}
	c.voiceResponsePending = true
	c.playMu.Unlock()

	c.setState("speaking", map[string]any{"tts_kind": first.req.TTSKind})
	ok := true
	reason := ""
	continueConversation := false
	stateAfter := ""
	next := first
	for {
		continueConversation = continueConversation || next.req.ContinueConversation
		if value := strings.ToLower(strings.TrimSpace(next.req.StateAfter)); value != "" {
			stateAfter = value
		}
		if err := c.playVoiceSegment(generation, next.req); err != nil {
			if !c.voiceResponseCurrent(generation) {
				return nil
			}
			ok, reason = false, err.Error()
			break
		}

		timer := time.NewTimer(ttsSegmentGrace)
	waitForSegment:
		select {
		case <-c.ctx.Done():
			timer.Stop()
			return nil
		case stoppedEpoch := <-c.voiceStop:
			if stoppedEpoch == generation {
				timer.Stop()
				return nil
			}
			// A cancellation from an older response may still be buffered if
			// its playback hook returned before observing this notification.
			goto waitForSegment
		case queued := <-c.voiceQueue:
			timer.Stop()
			if queued.epoch != generation {
				return &queued
			}
			next = queued
			continue
		case <-timer.C:
		}
		break
	}

	c.playMu.Lock()
	current := c.voiceGen == generation && c.voiceResponsePending
	if current {
		c.voiceCancel = nil
		c.voiceResponsePending = false
	}
	c.playMu.Unlock()
	if !current {
		return nil
	}
	payload := map[string]any{"ok": ok}
	if reason != "" {
		payload["error"] = reason
	}
	c.sendJSON("playback.finished", "", payload)
	if continueConversation && ok {
		c.startContinued()
	} else {
		if stateAfter == "" {
			stateAfter = "idle"
		}
		c.setState(stateAfter, payload)
	}
	return nil
}

func (c *Client) playVoiceSegment(generation uint64, req PlayRequest) error {
	ctx, cancel := context.WithCancel(c.ctx)
	c.playMu.Lock()
	if generation != c.voiceGen || !c.voiceResponsePending {
		c.playMu.Unlock()
		cancel()
		return context.Canceled
	}
	c.voiceCancel = cancel
	c.playMu.Unlock()

	var err error
	if hook := c.hooks.PlayVoice; hook != nil {
		err = hook(ctx, req)
	} else {
		err = errors.New("voice playback unavailable")
	}
	cancel()
	c.playMu.Lock()
	if c.voiceGen == generation && c.voiceResponsePending {
		c.voiceCancel = nil
	}
	c.playMu.Unlock()
	return err
}

func (c *Client) voiceResponseCurrent(generation uint64) bool {
	c.playMu.Lock()
	defer c.playMu.Unlock()
	return c.voiceGen == generation && c.voiceResponsePending
}

func (c *Client) stopVoice() {
	c.playMu.Lock()
	active := c.voiceResponsePending
	stoppedEpoch := c.voiceGen
	if c.voiceCancel != nil {
		c.voiceCancel()
		c.voiceCancel = nil
	}
	c.voiceGen++
	c.voiceResponsePending = false
	c.playMu.Unlock()
	for {
		select {
		case <-c.voiceQueue:
		default:
			goto drained
		}
	}
drained:
	if active {
		// Keep only the cancellation for the currently stopped response.
		// Epoch tagging prevents a late notification from cancelling the
		// first segment of a later response.
		select {
		case <-c.voiceStop:
		default:
		}
		select {
		case c.voiceStop <- stoppedEpoch:
		default:
		}
	}
	if hook := c.hooks.StopVoice; hook != nil {
		hook()
	}
	if active {
		// Queue completion before a barge-in voice.start so Tater finishes
		// the old announcement before opening the replacement turn.
		c.sendJSON("playback.finished", "", map[string]any{"ok": true, "stopped": true})
	}
}

func (c *Client) startMedia(req MediaRequest) {
	c.stopMedia("")
	c.playMu.Lock()
	ctx, cancel := context.WithCancel(c.ctx)
	c.mediaCancel = cancel
	c.mediaGen++
	generation := c.mediaGen
	c.mediaID = req.SessionID
	c.mediaGroup = req.GroupID
	c.playMu.Unlock()

	payload := map[string]any{
		"session_id": req.SessionID, "group_id": req.GroupID, "ok": true,
		"channel": "stereo", "sample_rate_hz": 48000,
	}
	c.sendJSON("media.session.started", "", payload)
	c.setState("playing", payload)
	ok, reason := true, ""
	if hook := c.hooks.StartMedia; hook != nil {
		if err := hook(ctx, req); err != nil {
			ok, reason = false, err.Error()
		}
	} else {
		ok, reason = false, "persistent media unavailable"
	}
	c.playMu.Lock()
	current := c.mediaGen == generation
	if current {
		c.mediaCancel = nil
		c.mediaID = ""
		c.mediaGroup = ""
	}
	c.playMu.Unlock()
	if !current {
		return
	}
	finished := map[string]any{"session_id": req.SessionID, "group_id": req.GroupID, "ok": ok}
	if reason != "" {
		finished["reason"] = reason
	}
	c.sendJSON("media.session.finished", "", finished)
	c.setState("idle", finished)
}

func (c *Client) stopMedia(requested string) {
	c.playMu.Lock()
	active := c.mediaID
	group := c.mediaGroup
	if requested != "" && active != requested {
		c.playMu.Unlock()
		return
	}
	if c.mediaCancel != nil {
		c.mediaCancel()
		c.mediaCancel = nil
	}
	c.mediaGen++
	c.mediaID = ""
	c.mediaGroup = ""
	c.playMu.Unlock()
	if active != "" && c.hooks.StopMedia != nil {
		c.hooks.StopMedia(active)
	}
	if active != "" {
		c.sendJSON("media.session.finished", "", map[string]any{
			"session_id": active, "group_id": group, "ok": true, "stopped": true,
		})
	}
}
