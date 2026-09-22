package taternative

import (
	"encoding/binary"
	"errors"
	"log"
	"strings"
	"time"
)

const (
	wakeVerifierVersion      = 1
	wakeVerifierCodecPCM16LE = 1
	wakeVerifierFlagEnforce  = 0x01
	wakeVerifierHeaderBytes  = 20
	wakeVerifierMinMS        = 500
	wakeVerifierMaxMS        = 2000
	wakeVerifierMaxPending   = 32
)

var wakeVerifierMagic = [4]byte{'T', 'W', 'V', '1'}

func buildWakeVerifierPacket(pcm []byte, requestID uint32, enforce bool) ([]byte, error) {
	if len(pcm) == 0 || len(pcm)%2 != 0 {
		return nil, errors.New("wake verifier PCM must contain complete 16-bit samples")
	}
	samples := len(pcm) / 2
	if samples > SampleRate*2 {
		return nil, errors.New("wake verifier PCM exceeds two seconds")
	}
	packet := make([]byte, wakeVerifierHeaderBytes+len(pcm))
	copy(packet[:4], wakeVerifierMagic[:])
	packet[4] = wakeVerifierVersion
	packet[5] = wakeVerifierCodecPCM16LE
	flags := uint16(0)
	if enforce {
		flags = wakeVerifierFlagEnforce
	}
	binary.LittleEndian.PutUint16(packet[6:8], flags)
	binary.LittleEndian.PutUint32(packet[8:12], requestID)
	binary.LittleEndian.PutUint32(packet[12:16], SampleRate)
	binary.LittleEndian.PutUint32(packet[16:20], uint32(samples))
	copy(packet[wakeVerifierHeaderBytes:], pcm)
	return packet, nil
}

func (c *Client) wakeVerifierSettings() (windowMS, timeoutMS int) {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	windowMS = clamp(intValue(c.settings["wake_verifier_window_ms"], 1000), wakeVerifierMinMS, wakeVerifierMaxMS)
	timeoutMS = clamp(intValue(c.settings["wake_verifier_timeout_ms"], 500), 100, 2000)
	return
}

func (c *Client) wakeVerifierAudio(windowMS int) []byte {
	requested := SampleRate * 2 * windowMS / 1000
	minimum := SampleRate * 2 * wakeVerifierMinMS / 1000
	c.audioMu.Lock()
	frames := cloneFrames(c.preRoll)
	c.audioMu.Unlock()
	total := 0
	for _, frame := range frames {
		total += len(frame)
	}
	if total < minimum {
		return nil
	}
	pcm := make([]byte, 0, total)
	for _, frame := range frames {
		pcm = append(pcm, frame...)
	}
	if len(pcm) > requested {
		pcm = pcm[len(pcm)-requested:]
	}
	return pcm
}

func (c *Client) queueWakeVerification(wakeWord string, score float32, enforce bool) bool {
	windowMS, timeoutMS := c.wakeVerifierSettings()
	pcm := c.wakeVerifierAudio(windowMS)
	if len(pcm) == 0 {
		log.Printf("[tater-native] wake verification skipped: capture ring not ready")
		if enforce {
			return c.startWake(wakeWord, score)
		}
		return false
	}

	c.verifyMu.Lock()
	if enforce {
		for _, pending := range c.verifyRequests {
			if pending.enforce {
				c.verifyMu.Unlock()
				return false
			}
		}
	}
	c.verifyNext++
	if c.verifyNext == 0 {
		c.verifyNext = 1
	}
	requestID := c.verifyNext
	if len(c.verifyRequests) >= wakeVerifierMaxPending {
		for id, pending := range c.verifyRequests {
			if !pending.enforce {
				if pending.timer != nil {
					pending.timer.Stop()
				}
				delete(c.verifyRequests, id)
				break
			}
		}
	}
	pending := &wakeVerification{enforce: enforce, wakeWord: wakeWord, score: score}
	c.verifyRequests[requestID] = pending
	c.verifyLastReason = "pending"
	if enforce {
		pending.timer = time.AfterFunc(time.Duration(timeoutMS)*time.Millisecond, func() {
			c.completeWakeVerification(requestID, true, false, "satellite_timeout_fail_open")
		})
	}
	c.verifyMu.Unlock()

	packet, err := buildWakeVerifierPacket(pcm, requestID, enforce)
	if err != nil || !c.sendBinary(packet) {
		return c.completeWakeVerification(requestID, true, false, "queue_fail_open")
	}
	mode := "observe"
	if enforce {
		mode = "enforce"
	}
	log.Printf("[tater-native] wake verification queued request=%d mode=%s samples=%d", requestID, mode, len(pcm)/2)
	return true
}

func (c *Client) handleWakeVerificationResult(payload map[string]any) {
	requestID := uint32(intValue(payload["request_id"], 0))
	if requestID == 0 {
		return
	}
	available := true
	if _, present := payload["available"]; present {
		available = boolValue(payload["available"])
	}
	c.completeWakeVerification(requestID, boolValue(payload["accepted"]), available, stringValue(payload["reason"]))
}

func (c *Client) completeWakeVerification(requestID uint32, accepted, available bool, reason string) bool {
	c.verifyMu.Lock()
	pending := c.verifyRequests[requestID]
	if pending == nil {
		c.verifyMu.Unlock()
		return false
	}
	delete(c.verifyRequests, requestID)
	if pending.timer != nil {
		pending.timer.Stop()
	}
	c.verifyCompleted++
	failOpen := !available
	if !accepted && !failOpen {
		c.verifyRejections++
	}
	if failOpen {
		c.verifyFailOpen++
	}
	if strings.TrimSpace(reason) == "" {
		if accepted {
			reason = "accepted"
		} else {
			reason = "rejected"
		}
	}
	c.verifyLastReason = reason
	c.verifyMu.Unlock()

	log.Printf("[tater-native] wake verification result request=%d accepted=%t available=%t enforced=%t reason=%s", requestID, accepted, available, pending.enforce, reason)
	if pending.enforce && (accepted || failOpen) {
		return c.startWake(pending.wakeWord, pending.score)
	}
	return false
}

func (c *Client) cancelWakeVerifications() {
	c.verifyMu.Lock()
	for id, pending := range c.verifyRequests {
		if pending.timer != nil {
			pending.timer.Stop()
		}
		delete(c.verifyRequests, id)
	}
	c.verifyMu.Unlock()
}

func (c *Client) wakeVerifierStatus() map[string]any {
	c.verifyMu.Lock()
	defer c.verifyMu.Unlock()
	pendingID := uint32(0)
	for id, pending := range c.verifyRequests {
		if pending.enforce {
			pendingID = id
			break
		}
	}
	return map[string]any{
		"pending": pendingID != 0, "pending_id": pendingID,
		"completed": c.verifyCompleted, "rejections": c.verifyRejections,
		"fail_open": c.verifyFailOpen, "last_reason": c.verifyLastReason,
	}
}
