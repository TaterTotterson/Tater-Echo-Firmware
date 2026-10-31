package taternative

import (
	"context"
	"encoding/binary"
	"errors"
	"log"
	"math"
	"strings"
	"time"
)

const ttsSegmentGrace = 650 * time.Millisecond

const (
	// Sat Chat is a permanently closed acoustic path between rooms. AEC owns
	// echo removal; this small post-AEC gate keeps ordinary microphone hiss and
	// room noise from returning to unity during pauses and circulating forever.
	// It never mutes, so quiet consonants and room context remain natural.
	satChatSilenceGain      = 0.16
	satChatSpeechHoldChunks = 5 // 400 ms at the 80 ms transport boundary
	satChatGateRelease      = 0.70
)

// PushAudio accepts one processed 16 kHz mono S16_LE chunk. It always updates
// the pre-roll ring and only transmits while a native voice turn is pending or
// active. The caller may reuse pcm as soon as this method returns.
func (c *Client) PushAudio(pcm []byte) {
	c.pushAudio(pcm, nil)
}

// PushAudioWithSpeech carries the already-computed microphone VAD decision
// into Sat Chat. It is intentionally a separate entry point so every existing
// caller of PushAudio retains bit-for-bit behavior.
func (c *Client) PushAudioWithSpeech(pcm []byte, speech bool) {
	c.pushAudio(pcm, &speech)
}

func (c *Client) pushAudio(pcm []byte, speech *bool) {
	usable := len(pcm) - len(pcm)%2
	if usable == 0 {
		return
	}
	frame := append([]byte(nil), pcm[:usable]...)
	c.audioMu.Lock()
	c.preRoll = appendRollingFrame(c.preRoll, frame)
	c.captureRoll = appendRollingFrame(c.captureRoll, frame)
	if c.verifyCaptureActive {
		c.verifyPostRoll = appendRollingFrame(c.verifyPostRoll, frame)
	}
	if c.voicePending {
		if len(c.pendingAudio) >= preRollChunks {
			copy(c.pendingAudio, c.pendingAudio[1:])
			c.pendingAudio[len(c.pendingAudio)-1] = frame
			c.audioDropped.Add(1)
		} else {
			c.pendingAudio = append(c.pendingAudio, frame)
		}
		c.audioMu.Unlock()
		return
	}
	active := c.voiceActive
	c.audioMu.Unlock()
	if c.satChatActive.Load() {
		if speech != nil {
			c.applySatChatGate(frame, *speech)
		}
		c.sendBinary(frame)
		return
	}
	if active {
		c.sendBinary(frame)
	}
}

func (c *Client) applySatChatGate(frame []byte, speech bool) {
	c.satChatGateMu.Lock()
	start := c.satChatGateGain
	end := start
	if speech {
		// The batch VAD decision is known before either 80 ms chunk is
		// emitted, so restore the whole first speech chunk instead of fading
		// its leading consonant up from the silence floor.
		start, end = 1, 1
		c.satChatSpeechHold = satChatSpeechHoldChunks
		c.satChatVoiceFrames++
	} else if c.satChatSpeechHold > 0 {
		c.satChatSpeechHold--
		end = 1
	} else {
		end += satChatGateRelease * (satChatSilenceGain - end)
		if end < satChatSilenceGain {
			end = satChatSilenceGain
		}
		c.satChatGateFrames++
	}
	c.satChatGateGain = end
	c.satChatGateMu.Unlock()

	if start == 1 && end == 1 {
		return
	}
	samples := len(frame) / 2
	for index := 0; index < samples; index++ {
		// Ramp within each 80 ms packet so entering the silence floor never
		// creates a discontinuity that sounds like a click.
		gain := end
		if samples > 1 {
			gain = start + (end-start)*float64(index)/float64(samples-1)
		}
		value := int16(binary.LittleEndian.Uint16(frame[index*2:]))
		scaled := math.Round(float64(value) * gain)
		if scaled > math.MaxInt16 {
			scaled = math.MaxInt16
		} else if scaled < math.MinInt16 {
			scaled = math.MinInt16
		}
		binary.LittleEndian.PutUint16(frame[index*2:], uint16(int16(scaled)))
	}
}

// SatChatAudioStatus exposes enough state to validate the live gate without
// logging on the real-time microphone goroutine.
func (c *Client) SatChatAudioStatus() map[string]any {
	c.satChatGateMu.Lock()
	defer c.satChatGateMu.Unlock()
	return map[string]any{
		"active":         c.satChatActive.Load(),
		"gate_gain":      c.satChatGateGain,
		"silence_frames": c.satChatGateFrames,
		"speech_frames":  c.satChatVoiceFrames,
	}
}

func (c *Client) setSatChatActive(active bool) bool {
	wasActive := c.satChatActive.Swap(active)
	c.satChatGateMu.Lock()
	c.satChatGateGain = 1
	c.satChatSpeechHold = 0
	c.satChatGateMu.Unlock()
	return wasActive
}

func appendRollingFrame(frames [][]byte, frame []byte) [][]byte {
	if len(frames) == cap(frames) {
		copy(frames, frames[1:])
		frames[len(frames)-1] = frame
		return frames
	}
	return append(frames, frame)
}

// Wake starts a local-wake turn using the established shared mono pre-roll.
// Array targets call WakeWithCapture with the exact beam that crossed.
func (c *Client) Wake(wakeWord string, score float32) bool {
	return c.WakeWithPreRoll(wakeWord, score, nil)
}

// WakeWithPreRoll preserves the established API for callers whose verifier
// and command STT use the same rolling capture. Array targets should use
// WakeWithCapture so verifier timing cannot shorten command pre-roll.
func (c *Client) WakeWithPreRoll(wakeWord string, score float32, preferredPreRoll [][]byte) bool {
	return c.WakeWithCapture(wakeWord, score, preferredPreRoll, nil)
}

// WakeWithCapture carries one acoustic path through the wake, but gives the
// verifier a phrase-aligned clip independent from the longer command pre-roll.
// A nil verifierCapture retains the configurable legacy window selection.
func (c *Client) WakeWithCapture(wakeWord string, score float32, commandPreRoll, verifierCapture [][]byte) bool {
	return c.WakeWithBoundaryCapture(wakeWord, score, commandPreRoll, nil, verifierCapture)
}

// WakeWithBoundaryCapture separates audio through the detector-crossing frame
// from command audio captured afterward. Tater can discard the former from
// STT while retaining the latter even when model agreement delayed dispatch.
func (c *Client) WakeWithBoundaryCapture(wakeWord string, score float32, wakePreRoll, commandPostWake, verifierCapture [][]byte) bool {
	if !c.connected.Load() {
		return false
	}
	wakePreRoll = cloneFrames(wakePreRoll)
	commandPostWake = cloneFrames(commandPostWake)
	verifierCapture = cloneFrames(verifierCapture)
	c.stateMu.RLock()
	verifierMode := strings.ToLower(stringValue(c.settings["wake_verifier_mode"]))
	forceVerifier := strings.EqualFold(stringValue(c.settings["wake_environment"]), "tv_nearby")
	c.stateMu.RUnlock()
	if c.handsFreeWakeBlocked() {
		return false
	}
	c.cancelCloseMiss()
	trainerCapture := append(cloneFrames(wakePreRoll), cloneFrames(commandPostWake)...)
	c.queueTrainerCaptureWithFrames("wake_detected", wakeWord, score, trainerCapture)
	switch {
	case forceVerifier || verifierMode == "enforce":
		return c.queueWakeVerificationWithBoundary(
			strings.TrimSpace(wakeWord), score, true,
			verifierCapture, wakePreRoll, commandPostWake,
		)
	case verifierMode == "observe":
		c.queueWakeVerificationWithBoundary(
			strings.TrimSpace(wakeWord), score, false,
			verifierCapture, wakePreRoll, commandPostWake,
		)
		return c.startWakeWithBufferedAudio(wakeWord, score, wakePreRoll, commandPostWake)
	default:
		return c.startWakeWithBufferedAudio(wakeWord, score, wakePreRoll, commandPostWake)
	}
}

func (c *Client) startWake(wakeWord string, score float32) bool {
	return c.startWakeWithPreRoll(wakeWord, score, nil)
}

func (c *Client) startWakeWithPreRoll(wakeWord string, score float32, preferredPreRoll [][]byte) bool {
	return c.startWakeWithBufferedAudio(wakeWord, score, preferredPreRoll, nil)
}

func (c *Client) startWakeWithBufferedAudio(wakeWord string, score float32, preferredPreRoll, bufferedPostWake [][]byte) bool {
	return c.startWakeWithAudio(wakeWord, score, preferredPreRoll, bufferedPostWake, false)
}

func (c *Client) startWakeWithVerifierCapture(wakeWord string, score float32, preferredPreRoll, bufferedPostWake [][]byte) bool {
	return c.startWakeWithAudio(wakeWord, score, preferredPreRoll, bufferedPostWake, true)
}

func (c *Client) startWakeWithAudio(wakeWord string, score float32, preferredPreRoll, bufferedPostWake [][]byte, takeVerifierCapture bool) bool {
	c.wakeGateMu.Lock()
	defer c.wakeGateMu.Unlock()

	bargeIn, replyActive := c.handsFreeWakeGateSnapshot()
	if replyActive && !bargeIn {
		if takeVerifierCapture {
			c.clearVerifierCapture()
		}
		return false
	}
	// A local wake during TTS is barge-in: cancel buffered voice playback
	// before opening the microphone turn. Persistent music stays alive and is
	// ducked by the eventual reply.
	c.stopVoice()
	if !replyActive && !c.DNDActive() {
		if hook := c.hooks.PlayWakeSound; hook != nil {
			hook()
		}
	}
	return c.startVoiceWithBufferedAudio(
		"local_wake", strings.TrimSpace(wakeWord), "", score, true,
		preferredPreRoll, bufferedPostWake, takeVerifierCapture,
	)
}

// StartButton starts a push-to-talk turn without a wake phrase.
func (c *Client) StartButton() bool {
	// The physical action button is an explicit push-to-talk request and may
	// always interrupt TTS, even when hands-free barge-in is disabled.
	c.StopCapture(true)
	c.stopVoice()
	return c.startVoice("button", "", "", 0, false)
}

// StartIntercom opens the same press-and-hold broadcast turn as the ESP
// satellites. Tater recognizes the synthetic wake phrase and routes the
// captured audio through its native intercom flow; Release calls StopCapture
// with abort=false so the completed recording is processed and broadcast.
func (c *Client) StartIntercom() bool {
	c.StopCapture(true)
	c.stopVoice()
	return c.startVoice("center_button_hold", "push to intercom", "", 0, false)
}

func (c *Client) startContinued(conversationID string) bool {
	return c.startVoice("continued_chat", "", conversationID, 0, false)
}

func (c *Client) startVoice(source, wakeWord, conversationID string, score float32, includePreRoll bool) bool {
	return c.startVoiceWithPreRoll(source, wakeWord, conversationID, score, includePreRoll, nil)
}

func (c *Client) startVoiceWithPreRoll(source, wakeWord, conversationID string, score float32, includePreRoll bool, preferredPreRoll [][]byte) bool {
	return c.startVoiceWithBufferedAudio(source, wakeWord, conversationID, score, includePreRoll, preferredPreRoll, nil, false)
}

func (c *Client) startVoiceWithBufferedAudio(source, wakeWord, conversationID string, score float32, includePreRoll bool, preferredPreRoll, bufferedPostWake [][]byte, takeVerifierCapture bool) bool {
	if !c.connected.Load() {
		if takeVerifierCapture {
			c.clearVerifierCapture()
		}
		return false
	}
	c.audioMu.Lock()
	if c.voicePending || c.voiceActive {
		c.wakeSuppressed++
		if takeVerifierCapture {
			c.verifyPostRoll = nil
			c.verifyCaptureActive = false
		}
		c.audioMu.Unlock()
		return false
	}
	if takeVerifierCapture {
		// This lock is the handoff point from verifier-delay capture to the
		// pending voice turn. No microphone frame can fall between the two.
		bufferedPostWake = append(cloneFrames(bufferedPostWake), cloneFrames(c.verifyPostRoll)...)
		c.verifyPostRoll = nil
		c.verifyCaptureActive = false
	}
	c.voicePending = true
	c.voiceStopAfterAck = false
	c.voiceWakeBoundary = includePreRoll
	c.pendingAudio = cloneTailFrames(bufferedPostWake, preRollChunks)
	c.wakePreRoll = nil
	if includePreRoll {
		if len(preferredPreRoll) > 0 {
			c.wakePreRoll = cloneTailFrames(preferredPreRoll, preRollChunks)
		} else {
			c.wakePreRoll = cloneFrames(c.preRoll)
		}
	}
	c.audioMu.Unlock()
	payload := map[string]any{
		"wake_word": wakeWord, "source": source, "request_flags": 0,
		"audio_format": map[string]any{"rate": SampleRate, "width": SampleWidth, "channels": Channels},
	}
	if includePreRoll {
		// Tater excludes these frames from STT and command VAD. The ordered
		// boundary sent after voice.start.ack lets speech immediately following
		// the wake phrase drive both without a fixed wall-clock delay.
		payload["wake_preroll_boundary"] = wakePrerollBoundaryVersion
	}
	if score > 0 {
		payload["wake_score"] = score
	}
	if conversationID = strings.TrimSpace(conversationID); conversationID != "" {
		payload["conversation_id"] = conversationID
	}
	if !c.sendJSON("voice.start", "", payload) {
		c.audioMu.Lock()
		c.voicePending = false
		c.voiceWakeBoundary = false
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

func cloneTailFrames(in [][]byte, maximum int) [][]byte {
	if maximum > 0 && len(in) > maximum {
		in = in[len(in)-maximum:]
	}
	return cloneFrames(in)
}

func (c *Client) handleVoiceStartAck(payload map[string]any) {
	c.audioMu.Lock()
	if !c.voicePending {
		c.audioMu.Unlock()
		return
	}
	if !boolValue(payload["ok"]) {
		c.voicePending = false
		c.voiceActive = false
		c.voiceStopAfterAck = false
		c.voiceWakeBoundary = false
		c.wakePreRoll = nil
		c.pendingAudio = nil
		c.audioMu.Unlock()
		c.setState("idle", payload)
		log.Printf("[tater-native] voice start rejected: %s", stringValue(payload["error"]))
		return
	}
	// Keep voicePending set while the ordered pre-roll/boundary/post-wake
	// sequence is queued. PushAudio continues collecting new microphone frames
	// instead of racing live audio ahead of the saved wake audio.
	preRoll := cloneFrames(c.wakePreRoll)
	wakeBoundary := c.voiceWakeBoundary
	c.wakePreRoll = nil
	c.audioMu.Unlock()
	for _, frame := range preRoll {
		c.sendBinary(frame)
	}
	if wakeBoundary {
		c.sendJSON("voice.audio_boundary", "", map[string]any{
			"kind": "wake_preroll_end", "version": wakePrerollBoundaryVersion,
		})
	}

	stopAfterAck := false
	for {
		c.audioMu.Lock()
		if !c.voicePending {
			c.audioMu.Unlock()
			return
		}
		frames := cloneFrames(c.pendingAudio)
		c.pendingAudio = nil
		if len(frames) == 0 {
			c.voicePending = false
			c.voiceActive = true
			c.voiceWakeBoundary = false
			stopAfterAck = c.voiceStopAfterAck
			c.voiceStopAfterAck = false
			c.audioMu.Unlock()
			break
		}
		c.audioMu.Unlock()
		for _, frame := range frames {
			c.sendBinary(frame)
		}
	}
	if stopAfterAck {
		// A short press-to-talk message may end before the voice.start round
		// trip completes. Flush every queued PCM frame first, then place the
		// non-abort stop behind them on the same ordered writer queue.
		c.StopCapture(false)
	}
}

func (c *Client) stopVoiceCapture() {
	c.audioMu.Lock()
	c.voicePending = false
	c.voiceActive = false
	c.voiceStopAfterAck = false
	c.voiceWakeBoundary = false
	c.wakePreRoll = nil
	c.pendingAudio = nil
	c.audioMu.Unlock()
}

func (c *Client) voiceCaptureInProgress() bool {
	c.audioMu.Lock()
	defer c.audioMu.Unlock()
	return c.voicePending || c.voiceActive
}

// StopCapture closes a pending/active microphone turn and notifies Tater. It
// is used for hardware mute and push-to-talk replacement, where merely
// stopping ALSA would leave the server waiting for a VAD end that cannot come.
func (c *Client) StopCapture(abort bool) bool {
	c.audioMu.Lock()
	active := c.voicePending || c.voiceActive
	if c.voicePending && !abort {
		// Do not discard a short intercom message just because the user
		// released before Tater's start acknowledgement returned.
		c.voiceStopAfterAck = true
		c.audioMu.Unlock()
		return true
	}
	c.voicePending = false
	c.voiceActive = false
	c.voiceStopAfterAck = false
	c.voiceWakeBoundary = false
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
	c.wakeGateMu.Lock()
	defer c.wakeGateMu.Unlock()

	c.playMu.Lock()
	epoch := c.voiceGen
	select {
	case c.voiceQueue <- queuedVoice{req: req, epoch: epoch}:
		c.voiceResponseQueued = true
		c.playMu.Unlock()
		c.cancelWakeVerificationsForPlayback()
		return true
	default:
		c.playMu.Unlock()
		log.Printf("[tater-native] voice playback queue full; dropping %q", req.URL)
		return false
	}
}

func (c *Client) setPendingReopen(enabled bool, conversationID string) {
	conversationID = strings.TrimSpace(conversationID)
	c.playMu.Lock()
	c.pendingReopen = enabled && conversationID != ""
	c.pendingConversation = conversationID
	if !c.pendingReopen {
		c.pendingConversation = ""
	}
	c.playMu.Unlock()
}

func (c *Client) pendingReopenSnapshot() (bool, string) {
	c.playMu.Lock()
	defer c.playMu.Unlock()
	return c.pendingReopen, c.pendingConversation
}

func (c *Client) playbackTurnInProgress() bool {
	c.playMu.Lock()
	defer c.playMu.Unlock()
	return c.voiceResponseQueued || c.voiceResponsePending || c.overlayID != "" || c.sceneID != ""
}

// handsFreeWakeGateSnapshot reports whether hands-free interruption is enabled
// and whether a protected reply is queued or playing. Persistent media is
// intentionally excluded: local wake remains available over music, while TTS,
// tool narration, overlays, and scenes obey the barge-in setting.
func (c *Client) handsFreeWakeGateSnapshot() (bargeIn, replyActive bool) {
	c.stateMu.RLock()
	bargeIn = boolValue(c.settings["barge_in_enabled"])
	state := strings.ToLower(strings.TrimSpace(c.state))
	c.stateMu.RUnlock()

	c.playMu.Lock()
	replyActive = c.voiceResponseQueued || c.voiceResponsePending || c.overlayID != "" || c.sceneID != ""
	c.playMu.Unlock()
	if state == "speaking" || state == "tool_call" {
		replyActive = true
	}
	return
}

func (c *Client) handsFreeWakeBlocked() bool {
	bargeIn, replyActive := c.handsFreeWakeGateSnapshot()
	return replyActive && !bargeIn
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

	playbackState := "speaking"
	if toolVoiceRequest(first.req) {
		playbackState = "tool_call"
	}
	c.setState(playbackState, map[string]any{
		"tts_kind": first.req.TTSKind, "state_after": first.req.StateAfter,
	})
	ok := true
	reason := ""
	continueConversation := false
	conversationID := ""
	stateAfter := ""
	next := first
	for {
		continueConversation = continueConversation || next.req.ContinueConversation
		if value := strings.TrimSpace(next.req.ConversationID); value != "" {
			conversationID = value
		}
		if value := strings.ToLower(strings.TrimSpace(next.req.StateAfter)); value != "" {
			stateAfter = value
		}
		if err := c.playVoiceSegment(generation, next.req); err != nil {
			if !c.voiceResponseCurrent(generation) {
				return nil
			}
			log.Printf("[tater-native] voice playback failed: %v", err)
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
		if c.pendingReopen {
			continueConversation = true
			if conversationID == "" {
				conversationID = c.pendingConversation
			}
		}
		c.pendingReopen = false
		c.pendingConversation = ""
		c.voiceCancel = nil
		c.voiceResponsePending = false
		c.voiceResponseQueued = len(c.voiceQueue) > 0
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
	c.stateMu.RLock()
	continuedChatEnabled := boolValue(c.settings["continued_chat"])
	c.stateMu.RUnlock()
	if continueConversation && ok && continuedChatEnabled {
		c.setState("listening", map[string]any{"source": "continued_chat"})
		go func() {
			timer := time.NewTimer(350 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-c.ctx.Done():
				return
			case <-timer.C:
			}
			if !c.startContinued(conversationID) {
				c.setState("idle", map[string]any{"continued_chat": false})
			}
		}()
	} else {
		if stateAfter == "" {
			stateAfter = "idle"
		}
		c.setState(stateAfter, payload)
	}
	return nil
}

func toolVoiceRequest(req PlayRequest) bool {
	ttsKind := strings.ToLower(strings.TrimSpace(req.TTSKind))
	stateAfter := strings.ToLower(strings.TrimSpace(req.StateAfter))
	return ttsKind == "tool" || ttsKind == "tool_progress" || stateAfter == "tool_call"
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
	c.voiceResponseQueued = false
	c.voiceResponsePending = false
	c.pendingReopen = false
	c.pendingConversation = ""
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
