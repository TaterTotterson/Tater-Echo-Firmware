package taternative

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// handleBLEGATT correlates the native request id with EchoMuse's compact
// uint32 GATT request id, then forwards the unchanged payload to the local
// connection manager. The bridge performs long operations asynchronously so
// the native WebSocket reader remains responsive to pings and audio commands.
func (c *Client) handleBLEGATT(messageID string, payload map[string]any) {
	hook := c.hooks.BLEGATT
	req, ok := bleGATTRequestID(payload["req"])
	if hook == nil || !ok {
		errorCode := "unavailable"
		if !ok {
			errorCode = "bad_request"
		}
		c.sendJSON("ble.gatt.result", "", map[string]any{
			"reply_to": messageID, "t": "result", "req": req,
			"ok": false, "error": errorCode,
		})
		return
	}

	c.bleGATTRequestMu.Lock()
	c.bleGATTRequests[req] = messageID
	c.bleGATTRequestMu.Unlock()
	// A connect can legally occupy most of 20 seconds. Expire only after the
	// bridge's longest request window so abandoned controller calls do not
	// accumulate forever.
	time.AfterFunc(45*time.Second, func() {
		c.bleGATTRequestMu.Lock()
		if c.bleGATTRequests[req] == messageID {
			delete(c.bleGATTRequests, req)
		}
		c.bleGATTRequestMu.Unlock()
	})
	raw, err := json.Marshal(payload)
	if err != nil {
		c.completeBLEGATT(req, messageID, map[string]any{"t": "result", "req": req, "ok": false, "error": "bad_request"})
		return
	}
	hook(raw)
}

func bleGATTRequestID(value any) (uint32, bool) {
	var number uint64
	switch v := value.(type) {
	case float64:
		if v < 0 || v > math.MaxUint32 || math.Trunc(v) != v {
			return 0, false
		}
		number = uint64(v)
	case int:
		if v < 0 {
			return 0, false
		}
		number = uint64(v)
	case uint32:
		number = uint64(v)
	case uint64:
		number = v
	default:
		return 0, false
	}
	if number > math.MaxUint32 {
		return 0, false
	}
	return uint32(number), true
}

func (c *Client) completeBLEGATT(req uint32, messageID string, payload map[string]any) {
	c.bleGATTRequestMu.Lock()
	if c.bleGATTRequests[req] == messageID {
		delete(c.bleGATTRequests, req)
	}
	c.bleGATTRequestMu.Unlock()
	payload["reply_to"] = messageID
	c.sendJSON("ble.gatt.result", "", payload)
}

// ReportBLEGATT carries one local bridge result or event over the existing
// authenticated native-satellite WebSocket. Correlated results resolve the
// waiting Tater request; notifications, disconnects, and slot changes remain
// unsolicited events.
func (c *Client) ReportBLEGATT(raw []byte) bool {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return false
	}
	if fmt.Sprint(payload["t"]) != "result" {
		return c.sendJSON("ble.gatt.event", "", payload)
	}
	req, ok := bleGATTRequestID(payload["req"])
	if !ok {
		return false
	}
	c.bleGATTRequestMu.Lock()
	messageID := c.bleGATTRequests[req]
	delete(c.bleGATTRequests, req)
	c.bleGATTRequestMu.Unlock()
	if messageID == "" {
		return false
	}
	payload["reply_to"] = messageID
	return c.sendJSON("ble.gatt.result", "", payload)
}
