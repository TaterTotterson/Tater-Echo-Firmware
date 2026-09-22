// Package taternative implements Tater's outbound native-satellite protocol.
//
// The Echo opens one WebSocket to Tater, sends JSON control envelopes and raw
// 16 kHz mono S16_LE microphone frames, and receives state, playback, settings,
// timer and OTA commands. Hardware-specific behavior is supplied through
// Hooks so the wire protocol remains host-testable.
package taternative

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	ProtocolVersion = 1
	NativeWSPath    = "/api/tater/satellite/v1/ws"
	SampleRate      = 16000
	SampleWidth     = 2
	Channels        = 1
)

// Envelope is the versioned JSON control frame used in both directions.
type Envelope struct {
	Version int            `json:"v"`
	Type    string         `json:"type"`
	ID      string         `json:"id"`
	TS      float64        `json:"ts"`
	Payload map[string]any `json:"payload"`
}

func envelope(messageType, id string, payload map[string]any) Envelope {
	if payload == nil {
		payload = map[string]any{}
	}
	if id == "" {
		id = newMessageID()
	}
	return Envelope{
		Version: ProtocolVersion,
		Type:    strings.TrimSpace(messageType),
		ID:      id,
		TS:      float64(time.Now().UnixNano()) / 1e9,
		Payload: payload,
	}
}

func marshalEnvelope(messageType, id string, payload map[string]any) ([]byte, error) {
	return json.Marshal(envelope(messageType, id, payload))
}

// NormalizeURL accepts either a Tater base URL or the full native WebSocket
// endpoint. HTTP schemes are converted to their WebSocket equivalents.
func NormalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("tater native URL is required")
	}
	if !strings.Contains(raw, "://") {
		raw = "ws://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse Tater URL: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported Tater URL scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return "", errors.New("Tater URL has no host")
	}
	if strings.TrimRight(u.Path, "/") != NativeWSPath {
		u.Path = path.Join(strings.TrimRight(u.Path, "/"), NativeWSPath)
	}
	return u.String(), nil
}

func boolValue(v any) bool {
	switch value := v.(type) {
	case bool:
		return value
	case string:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "1", "true", "yes", "on", "enabled":
			return true
		}
	case float64:
		return value != 0
	case int:
		return value != 0
	}
	return false
}

func intValue(v any, fallback int) int {
	switch value := v.(type) {
	case float64:
		return int(value)
	case float32:
		return int(value)
	case int:
		return value
	case int64:
		return int(value)
	case json.Number:
		if n, err := value.Int64(); err == nil {
			return int(n)
		}
	}
	return fallback
}

func stringValue(v any) string {
	if v == nil {
		return ""
	}
	if value, ok := v.(string); ok {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(fmt.Sprint(v))
}
