package taternative

import (
	"encoding/json"
	"testing"

	"github.com/gorilla/websocket"
)

func TestBLEGATTResultKeepsNativeRequestCorrelation(t *testing.T) {
	var forwarded []byte
	c := &Client{
		hooks: Hooks{BLEGATT: func(raw []byte) { forwarded = append([]byte(nil), raw...) }},
		out:   make(chan outbound, 4), bleGATTRequests: make(map[uint32]string),
	}
	c.connected.Store(true)
	c.handleBLEGATT("native-42", map[string]any{
		"t": "read", "req": float64(7), "addr": "c0:00:00:00:00:0a", "handle": float64(12),
	})
	if len(forwarded) == 0 {
		t.Fatal("GATT request was not forwarded")
	}
	if !c.ReportBLEGATT([]byte(`{"t":"result","req":7,"ok":true,"value":"aGk="}`)) {
		t.Fatal("GATT result was not queued")
	}
	frame := <-c.out
	if frame.kind != websocket.TextMessage {
		t.Fatalf("frame kind %d", frame.kind)
	}
	var envelope Envelope
	if err := json.Unmarshal(frame.data, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Type != "ble.gatt.result" || envelope.Payload["reply_to"] != "native-42" || envelope.Payload["ok"] != true {
		t.Fatalf("unexpected result %#v", envelope)
	}
}

func TestBLEGATTEventIsUncorrelated(t *testing.T) {
	c := &Client{out: make(chan outbound, 2), bleGATTRequests: make(map[uint32]string)}
	c.connected.Store(true)
	if !c.ReportBLEGATT([]byte(`{"t":"notify","addr":"c0:00:00:00:00:0a","handle":9,"value":"AQ=="}`)) {
		t.Fatal("notification was not queued")
	}
	var envelope Envelope
	if err := json.Unmarshal((<-c.out).data, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Type != "ble.gatt.event" {
		t.Fatalf("type %q", envelope.Type)
	}
}

func TestBLEGATTRejectsMissingRequestID(t *testing.T) {
	c := &Client{out: make(chan outbound, 2), bleGATTRequests: make(map[uint32]string)}
	c.connected.Store(true)
	c.handleBLEGATT("native-bad", map[string]any{"t": "read"})
	var envelope Envelope
	if err := json.Unmarshal((<-c.out).data, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Payload["error"] != "bad_request" || envelope.Payload["reply_to"] != "native-bad" {
		t.Fatalf("unexpected refusal %#v", envelope.Payload)
	}
}
