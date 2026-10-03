package taternative

import (
	"encoding/json"
	"testing"
	"time"
)

func TestReportBLEAdvertisementsUsesNativePresenceShape(t *testing.T) {
	c := &Client{out: make(chan outbound, 1), started: time.Now().Add(-2 * time.Second)}
	c.connected.Store(true)
	if !c.ReportBLEAdvertisements([]BLEAdvertisement{{
		Address: "AA:BB:CC:DD:EE:FF", AddressType: 1, EventType: 4, RSSI: -61,
		Data: []byte{0x02, 0x01, 0x06},
	}}) {
		t.Fatal("ReportBLEAdvertisements returned false")
	}
	frame := <-c.out
	var message Envelope
	if err := json.Unmarshal(frame.data, &message); err != nil {
		t.Fatal(err)
	}
	if message.Type != "ble.advertisements" || intValue(message.Payload["version"], 0) != 1 {
		t.Fatalf("unexpected envelope: %#v", message)
	}
	rows, ok := message.Payload["adverts"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("adverts = %#v", message.Payload["adverts"])
	}
	row, _ := rows[0].(map[string]any)
	if row["address"] != "aa:bb:cc:dd:ee:ff" || row["data"] != "020106" {
		t.Fatalf("advert wire shape = %#v", row)
	}
	if intValue(row["address_type"], -1) != 1 || intValue(row["rssi"], 0) != -61 {
		t.Fatalf("advert metadata = %#v", row)
	}
	if intValue(row["event_type"], -1) != 4 {
		t.Fatalf("advert event type = %#v", row["event_type"])
	}
}

func TestReportBLEAdvertisementsDoesNotCompeteWithVoice(t *testing.T) {
	c := &Client{out: make(chan outbound, 1), started: time.Now()}
	c.connected.Store(true)
	c.voiceActive = true
	if c.ReportBLEAdvertisements([]BLEAdvertisement{{
		Address: "aa:bb:cc:dd:ee:ff", Data: []byte{1},
	}}) {
		t.Fatal("BLE telemetry was queued during microphone capture")
	}
	if len(c.out) != 0 {
		t.Fatal("BLE telemetry reached the outbound queue during microphone capture")
	}
}

func TestTelemetryDoesNotEvictQueuedControl(t *testing.T) {
	c := &Client{out: make(chan outbound, 1), started: time.Now()}
	c.connected.Store(true)
	original := outbound{kind: 1, data: []byte("control")}
	c.out <- original
	if c.ReportBLEAdvertisements([]BLEAdvertisement{{
		Address: "aa:bb:cc:dd:ee:ff", Data: []byte{1},
	}}) {
		t.Fatal("BLE telemetry reported success with a full queue")
	}
	if got := <-c.out; string(got.data) != "control" {
		t.Fatalf("telemetry evicted control frame: %q", got.data)
	}
}
