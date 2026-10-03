package taternative

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	trainerCaptureChunks       = 38 // just over three seconds at 80 ms/chunk
	trainerMinimumCaptureBytes = SampleRate * SampleWidth / 2
	trainerUploadPath          = "/api/upload_captured_audio_raw"
	closeMissConfirmation      = 900 * time.Millisecond
	closeMissCooldown          = 10 * time.Second
)

type trainerCapture struct {
	eventType string
	wakeWord  string
	score     float32
	threshold float64
	pcm       []byte
}

// CloseMiss records a local detector near miss. Repeated probability windows
// from one utterance collapse into its strongest score after a short settle
// period, matching the ESP32 satellites' confirmation/cooldown policy.
func (c *Client) CloseMiss(wakeWord string, score float32) {
	if !c.connected.Load() || score <= 0 {
		return
	}
	c.stateMu.RLock()
	enabled := boolValue(c.settings["capture_close_misses"])
	state := c.state
	c.stateMu.RUnlock()
	if !enabled || (state != "idle" && state != "playing") {
		return
	}

	c.trainerMu.Lock()
	now := time.Now()
	if !c.trainerLastClose.IsZero() && now.Sub(c.trainerLastClose) < closeMissCooldown {
		c.trainerMu.Unlock()
		return
	}
	if score > c.trainerCloseScore {
		c.trainerCloseScore = score
		c.trainerCloseWord = strings.TrimSpace(wakeWord)
	}
	if c.trainerCloseTimer == nil {
		c.trainerCloseTimer = time.AfterFunc(closeMissConfirmation, c.flushCloseMiss)
	}
	c.trainerMu.Unlock()
}

func (c *Client) cancelCloseMiss() {
	c.trainerMu.Lock()
	if c.trainerCloseTimer != nil {
		c.trainerCloseTimer.Stop()
	}
	c.trainerCloseTimer = nil
	c.trainerCloseScore = 0
	c.trainerCloseWord = ""
	c.trainerMu.Unlock()
}

func (c *Client) flushCloseMiss() {
	c.trainerMu.Lock()
	score := c.trainerCloseScore
	wakeWord := c.trainerCloseWord
	c.trainerCloseTimer = nil
	c.trainerCloseScore = 0
	c.trainerCloseWord = ""
	c.trainerLastClose = time.Now()
	c.trainerMu.Unlock()
	if score > 0 {
		c.queueTrainerCapture("close_miss", wakeWord, score)
	}
}

func (c *Client) queueTrainerCapture(eventType, wakeWord string, score float32) bool {
	c.stateMu.RLock()
	enabled := boolValue(c.settings["capture_wake_audio"])
	if eventType != "wake_detected" {
		enabled = boolValue(c.settings["capture_close_misses"])
	}
	trainerURL := stringValue(c.settings["trainer_app_url"])
	threshold := numberValue(c.settings["wake_threshold"], 0)
	c.stateMu.RUnlock()
	if !enabled || trainerURL == "" {
		return false
	}
	uploadURL, err := resolveTrainerUploadURL(trainerURL, c.url)
	if err != nil {
		c.recordTrainerFailure(err)
		return false
	}

	c.audioMu.Lock()
	frames := cloneFrames(c.captureRoll)
	c.audioMu.Unlock()
	total := 0
	for _, frame := range frames {
		total += len(frame)
	}
	if total < trainerMinimumCaptureBytes {
		c.recordTrainerFailure(fmt.Errorf("trainer capture buffer is not ready"))
		return false
	}
	pcm := make([]byte, 0, total)
	for _, frame := range frames {
		pcm = append(pcm, frame...)
	}

	c.trainerMu.Lock()
	if c.trainerUploadRunning {
		c.trainerMu.Unlock()
		return false
	}
	c.trainerUploadRunning = true
	c.trainerMu.Unlock()
	request := trainerCapture{
		eventType: eventType,
		wakeWord:  strings.TrimSpace(wakeWord),
		score:     score,
		threshold: threshold,
		pcm:       pcm,
	}
	go c.uploadTrainerCapture(uploadURL, request)
	return true
}

func (c *Client) uploadTrainerCapture(uploadURL string, capture trainerCapture) {
	ctx, cancel := context.WithTimeout(c.ctx, 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(capture.pcm))
	if err == nil {
		request.Header.Set("Content-Type", "application/octet-stream")
		request.Header.Set("X-Audio-Format", "pcm_s16le")
		request.Header.Set("X-Sample-Rate", strconv.Itoa(SampleRate))
		request.Header.Set("X-Original-Name", "echo_native_wake_capture.raw")
		request.Header.Set("X-Source-Device", c.cfg.DeviceName)
		request.Header.Set("X-Wake-Word", capture.wakeWord)
		request.Header.Set("X-Event-Type", capture.eventType)
		request.Header.Set("X-Blocked-By-Vad", "false")
		request.Header.Set("X-Max-Probability", fmt.Sprintf("%.6f", capture.score))
		request.Header.Set("X-Average-Probability", fmt.Sprintf("%.6f", capture.score))
		request.Header.Set("X-Probability-Cutoff", fmt.Sprintf("%.3f", capture.threshold))
		request.Header.Set("X-Peak-Probability-Cutoff", fmt.Sprintf("%.3f", capture.threshold))
		request.Header.Set("X-Active-Windows", "1")
		request.Header.Set("X-Min-Active-Windows", "1")
		request.Header.Set("X-Detection-Profile", "echo_micro_wake_word")
		request.Header.Set("X-Rise-Score", "0")
		request.Header.Set("X-Probability-History", "")
		request.Header.Set("X-Notes", "tater echo native satellite")
	}
	if err == nil {
		response, requestErr := c.trainerHTTP.Do(request)
		err = requestErr
		if response != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			response.Body.Close()
			if err == nil && (response.StatusCode < 200 || response.StatusCode >= 300) {
				err = fmt.Errorf("trainer upload HTTP %d", response.StatusCode)
			}
		}
	}

	c.trainerMu.Lock()
	c.trainerUploadRunning = false
	if err != nil {
		c.trainerUploadFailures++
		c.trainerLastError = err.Error()
	} else {
		c.trainerUploads++
		c.trainerLastEvent = capture.eventType
		c.trainerLastError = ""
	}
	c.trainerMu.Unlock()
}

func (c *Client) recordTrainerFailure(err error) {
	if err == nil {
		return
	}
	c.trainerMu.Lock()
	c.trainerUploadFailures++
	c.trainerLastError = err.Error()
	c.trainerMu.Unlock()
}

// TrainerStatus matches the capture diagnostics nested under wake_engine by
// the ESP32 firmware.
func (c *Client) TrainerStatus() map[string]any {
	c.trainerMu.Lock()
	defer c.trainerMu.Unlock()
	c.audioMu.Lock()
	samples := 0
	for _, frame := range c.captureRoll {
		samples += len(frame) / SampleWidth
	}
	c.audioMu.Unlock()
	return map[string]any{
		"upload_running":     c.trainerUploadRunning,
		"uploads":            c.trainerUploads,
		"upload_failures":    c.trainerUploadFailures,
		"last_event_type":    c.trainerLastEvent,
		"last_error":         c.trainerLastError,
		"buffer_samples":     samples,
		"buffer_seconds":     float64(samples) / SampleRate,
		"buffer_psram":       false,
		"close_miss_pending": c.trainerCloseTimer != nil,
	}
}

func resolveTrainerUploadURL(baseURL, nativeURL string) (string, error) {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return "", fmt.Errorf("trainer URL must use http:// or https://")
	}
	if strings.EqualFold(base.Hostname(), "trainer.local") {
		native, parseErr := url.Parse(nativeURL)
		if parseErr != nil || native.Hostname() == "" {
			return "", fmt.Errorf("cannot resolve trainer.local from Tater URL")
		}
		if port := base.Port(); port != "" {
			base.Host = net.JoinHostPort(native.Hostname(), port)
		} else {
			base.Host = native.Hostname()
		}
	}
	if !strings.HasSuffix(strings.TrimRight(base.Path, "/"), trainerUploadPath) {
		base.Path = strings.TrimRight(base.Path, "/") + trainerUploadPath
	}
	return base.String(), nil
}

func numberValue(value any, fallback float64) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case string:
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64); err == nil {
			return parsed
		}
	}
	return fallback
}
