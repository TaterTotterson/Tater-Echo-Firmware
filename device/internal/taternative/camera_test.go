package taternative

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestCameraSnapshotReturnsCorrelatedJPEG(t *testing.T) {
	jpeg := []byte("\xff\xd8show-camera\xff\xd9")
	client := &Client{
		out: make(chan outbound, 1),
		hooks: Hooks{CameraSnapshot: func(context.Context) (CameraSnapshot, error) {
			return CameraSnapshot{Image: jpeg, ContentType: "image/jpeg"}, nil
		}},
	}
	client.connected.Store(true)
	client.handle(Envelope{Type: "camera.snapshot", ID: "vision-request"})

	select {
	case frame := <-client.out:
		if frame.kind != websocket.TextMessage {
			t.Fatalf("frame kind = %d", frame.kind)
		}
		var response Envelope
		if err := json.Unmarshal(frame.data, &response); err != nil {
			t.Fatal(err)
		}
		if response.Type != "camera.snapshot.result" || response.Payload["reply_to"] != "vision-request" {
			t.Fatalf("response = %#v", response)
		}
		if response.Payload["ok"] != true {
			t.Fatalf("snapshot failed: %#v", response.Payload)
		}
		decoded, err := base64.StdEncoding.DecodeString(stringValue(response.Payload["image_base64"]))
		if err != nil || string(decoded) != string(jpeg) {
			t.Fatalf("decoded image = %q, err=%v", decoded, err)
		}
	case <-time.After(time.Second):
		t.Fatal("camera result was not queued")
	}
}
