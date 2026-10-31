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

func TestDecodeDisplayCamerasCarriesEphemeralStreamURL(t *testing.T) {
	decoded := decodeDisplayCameras(map[string]any{
		"selected_id":   "front",
		"stream_url":    "https://ha.invalid/api/hls/token/playlist.m3u8",
		"stream_active": true,
	})
	if decoded.SelectedID != "front" || decoded.StreamURL != "https://ha.invalid/api/hls/token/playlist.m3u8" || !decoded.StreamActive {
		t.Fatalf("decoded camera stream = %#v", decoded)
	}
}

func TestCameraStreamReturnsCorrelatedStartAndPublishesLatestJPEG(t *testing.T) {
	jpeg := []byte("\xff\xd8live-show-camera\xff\xd9")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &Client{
		ctx: ctx, out: make(chan outbound, 2), videoOut: make(chan outbound, 1),
		hooks: Hooks{CameraStream: func(streamCtx context.Context, profile string, receive func([]byte)) error {
			if profile != "echo-low" {
				t.Errorf("stream profile = %q, want echo-low", profile)
			}
			receive(jpeg)
			<-streamCtx.Done()
			return streamCtx.Err()
		}},
	}
	client.connected.Store(true)
	client.handle(Envelope{Type: "camera.stream.start", ID: "stream-request", Payload: map[string]any{"profile": "echo-low"}})

	select {
	case frame := <-client.out:
		var response Envelope
		if err := json.Unmarshal(frame.data, &response); err != nil {
			t.Fatal(err)
		}
		if response.Type != "camera.stream.start.result" || response.Payload["reply_to"] != "stream-request" || response.Payload["ok"] != true {
			t.Fatalf("stream response = %#v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("camera stream start result was not queued")
	}

	select {
	case frame := <-client.videoOut:
		decoded, ok := decodeCameraFrame(frame.data)
		if !ok || string(decoded) != string(jpeg) {
			t.Fatalf("camera stream frame = %x, ok=%v", decoded, ok)
		}
	case <-time.After(time.Second):
		t.Fatal("camera stream frame was not queued")
	}
	client.stopCameraStream()
}

func TestSatChatPreemptsCameraStreamAndReportsEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	client := &Client{
		ctx: ctx, out: make(chan outbound, 4), videoOut: make(chan outbound, 1),
		hooks: Hooks{CameraStream: func(streamCtx context.Context, _ string, _ func([]byte)) error {
			close(started)
			<-streamCtx.Done()
			return streamCtx.Err()
		}},
	}
	client.connected.Store(true)
	client.handle(Envelope{Type: "camera.stream.start", ID: "stream-request"})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("camera stream did not start")
	}
	<-client.out // camera.stream.start.result
	client.handle(Envelope{Type: "sat_chat.started"})
	select {
	case frame := <-client.out:
		var response Envelope
		if err := json.Unmarshal(frame.data, &response); err != nil {
			t.Fatal(err)
		}
		if response.Type != "camera.stream.ended" || response.Payload["reason"] != "sat_chat" {
			t.Fatalf("preemption response = %#v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("camera stream preemption was not reported")
	}
}
