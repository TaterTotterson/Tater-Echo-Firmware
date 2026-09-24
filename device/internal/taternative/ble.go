package taternative

import (
	"encoding/hex"
	"strings"
	"time"
)

// BLEAdvertisement is the hardware-neutral native BLE wire record. The Echo
// scanner supplies raw AD bytes; Tater Native uses lowercase hex so every
// satellite family feeds the same presence decoder.
type BLEAdvertisement struct {
	Address     string
	AddressType int
	EventType   int
	RSSI        int
	Data        []byte
}

// ReportBLEAdvertisements sends one best-effort presence batch. BLE never
// competes with live microphone audio and never evicts a control frame.
func (c *Client) ReportBLEAdvertisements(adverts []BLEAdvertisement) bool {
	if len(adverts) == 0 || c.voiceCaptureInProgress() {
		return false
	}
	rows := make([]map[string]any, 0, len(adverts))
	for _, advert := range adverts {
		address := strings.ToLower(strings.TrimSpace(advert.Address))
		if address == "" || len(advert.Data) == 0 {
			continue
		}
		rows = append(rows, map[string]any{
			"address":      address,
			"address_type": advert.AddressType,
			"rssi":         advert.RSSI,
			"event_type":   advert.EventType,
			"data":         hex.EncodeToString(advert.Data),
			"age_ms":       0,
		})
	}
	if len(rows) == 0 {
		return false
	}
	return c.sendTelemetryJSON("ble.advertisements", map[string]any{
		"version":          1,
		"batch_id":         c.bleBatchID.Add(1),
		"device_uptime_ms": time.Since(c.started).Milliseconds(),
		"adverts":          rows,
	})
}

// ReportBLEEnrollmentStatus reports only public one-shot session state.
func (c *Client) ReportBLEEnrollmentStatus(enrollmentID, status, detail string) bool {
	payload := map[string]any{"enrollment_id": enrollmentID, "status": status}
	if detail != "" {
		payload["error"] = detail
	}
	return c.sendJSON("ble.enrollment.status", "", payload)
}

// ReportBLEEnrollmentResult sends the IRK once on the authenticated native
// socket. The controller consumes it directly into its private registry.
func (c *Client) ReportBLEEnrollmentResult(enrollmentID string, ok bool, irk, detail string) bool {
	payload := map[string]any{"enrollment_id": enrollmentID, "ok": ok}
	if ok && irk != "" {
		payload["irk"] = irk
	}
	if detail != "" {
		payload["error"] = detail
	}
	return c.sendJSON("ble.enrollment.result", "", payload)
}
