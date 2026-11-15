package taternative

import (
	"context"
	"encoding/binary"
	"math"
	"sync"
	"testing"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/liveh264"
	"github.com/gorilla/websocket"
)

func TestSatChatCapabilitiesMatchHardwareRoles(t *testing.T) {
	for _, target := range []string{"crown", "athena", "checkers", "cronos", "rook"} {
		capabilities := CapabilitiesForTarget(target)
		if capabilities["sat_chat_audio"] != true || capabilities["sat_chat_ui"] != true {
			t.Fatalf("%s did not advertise Sat Chat audio and UI: %#v", target, capabilities)
		}
		if capabilities["sat_chat_can_initiate"] != true || capabilities["sat_chat_can_end"] != true {
			t.Fatalf("%s did not advertise Sat Chat controls: %#v", target, capabilities)
		}
		if capabilities["sat_chat_video"] != true {
			t.Fatalf("%s did not advertise Sat Chat video: %#v", target, capabilities)
		}
	}

	for _, target := range []string{"biscuit", "radar"} {
		capabilities := CapabilitiesForTarget(target)
		if capabilities["sat_chat_audio"] != true {
			t.Fatalf("%s did not advertise Sat Chat audio", target)
		}
		if capabilities["sat_chat_can_initiate"] == true || capabilities["sat_chat_can_end"] == true {
			t.Fatalf("%s unexpectedly advertised Sat Chat controls: %#v", target, capabilities)
		}
		if capabilities["sat_chat_video"] == true {
			t.Fatalf("%s unexpectedly advertised Sat Chat video: %#v", target, capabilities)
		}
	}
}

func TestSatChatVideoFramesAreTaggedAndSeparatedFromPCM(t *testing.T) {
	jpeg := []byte{0xff, 0xd8, 0x01, 0x02, 0xff, 0xd9}
	frame := EncodeSatChatVideoFrame(jpeg)
	decoded, ok := decodeSatChatVideoFrame(frame)
	if !ok || string(decoded) != string(jpeg) {
		t.Fatalf("decoded Sat Chat video = %x, %t; want %x", decoded, ok, jpeg)
	}
	if _, ok := decodeSatChatVideoFrame(constantPCM(1000, 40)); ok {
		t.Fatal("raw PCM was misclassified as Sat Chat video")
	}
	if got := EncodeSatChatVideoFrame([]byte("not a jpeg")); got != nil {
		t.Fatalf("invalid camera frame was accepted: %x", got)
	}
}

func TestSatChatVideoKeepsOnlyNewestFrameOffAudioQueue(t *testing.T) {
	c, err := New(Config{URL: "ws://tater.test", DeviceID: "echo-test"}, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.connected.Store(true)

	if !c.sendBestEffortBinary([]byte("old")) || !c.sendBestEffortBinary([]byte("new")) {
		t.Fatal("video enqueue failed")
	}
	if got := len(c.out); got != 0 {
		t.Fatalf("audio/control queue has %d video frames, want 0", got)
	}
	if got := string((<-c.videoOut).data); got != "new" {
		t.Fatalf("queued video = %q, want newest frame", got)
	}
}

func TestSatChatWriterPrioritizesAudioAndControlOverVideo(t *testing.T) {
	c, err := New(Config{URL: "ws://tater.test", DeviceID: "echo-test"}, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.out <- outbound{kind: websocket.BinaryMessage, data: []byte("audio")}
	c.videoOut <- outbound{kind: websocket.BinaryMessage, data: []byte("video")}

	frame, err := c.nextOutbound(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := string(frame.data); got != "audio" {
		t.Fatalf("first outbound frame = %q, want audio", got)
	}
}

func h264QueueFrame(sequence uint32, stream uint64, keyframe bool) liveh264.Frame {
	return liveh264.Frame{Width: 1280, Height: 720, FPS: 30, Sequence: sequence,
		StreamID: stream, PTSUS: int64(sequence) * 33333, Keyframe: keyframe,
		Data: []byte{0, 0, 0, 1, 0x65}}
}

func TestSatChatH264SenderPreservesBurstOffAudioQueue(t *testing.T) {
	c, err := New(Config{URL: "ws://tater.test", DeviceID: "echo-test"}, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.connected.Store(true)
	c.setSatChatActive(true)
	for i := uint32(0); i < 12; i++ {
		if !c.SendSatChatH264(h264QueueFrame(i, 1, i == 0)) {
			t.Fatalf("rejected frame %d", i)
		}
	}
	if len(c.out) != 0 || len(c.videoOut) != 12 {
		t.Fatalf("queues audio=%d video=%d", len(c.out), len(c.videoOut))
	}
	for i := uint32(0); i < 12; i++ {
		frame, err := liveh264.Decode((<-c.videoOut).data)
		if err != nil || frame.Sequence != i {
			t.Fatalf("frame %d: %+v, %v", i, frame, err)
		}
	}
}

func TestSatChatH264SenderOverflowWaitsForFreshKeyframe(t *testing.T) {
	c, err := New(Config{URL: "ws://tater.test", DeviceID: "echo-test"}, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.connected.Store(true)
	c.setSatChatActive(true)
	if c.SendSatChatH264(h264QueueFrame(1, 1, false)) {
		t.Fatal("accepted initial predictive frame")
	}
	for i := uint32(0); i < liveh264.QueuedFrames; i++ {
		if !c.SendSatChatH264(h264QueueFrame(i, 1, i == 0)) {
			t.Fatalf("rejected burst frame %d", i)
		}
	}
	if c.SendSatChatH264(h264QueueFrame(32, 1, false)) || len(c.videoOut) != 0 {
		t.Fatal("overflow retained broken GOP")
	}
	if c.SendSatChatH264(h264QueueFrame(33, 1, false)) {
		t.Fatal("accepted prediction after overflow")
	}
	if !c.SendSatChatH264(h264QueueFrame(34, 1, true)) || !c.SendSatChatH264(h264QueueFrame(35, 1, false)) {
		t.Fatal("did not resume at keyframe")
	}
	if len(c.videoOut) != 2 {
		t.Fatal("wrong recovered burst length")
	}
	if c.SendSatChatH264(h264QueueFrame(1, 2, false)) || len(c.videoOut) != 0 {
		t.Fatal("new stream retained old GOP or initial prediction")
	}
	if !c.SendSatChatH264(h264QueueFrame(2, 2, true)) {
		t.Fatal("new stream rejected keyframe")
	}
	c.setSatChatActive(false)
	if len(c.videoOut) != 0 || c.SendSatChatH264(h264QueueFrame(3, 2, false)) {
		t.Fatal("ended call retained video")
	}
	c.setSatChatActive(true)
	if c.SendSatChatH264(h264QueueFrame(4, 2, false)) {
		t.Fatal("restarted call did not wait for keyframe")
	}
	if !c.SendSatChatH264(h264QueueFrame(5, 2, true)) {
		t.Fatal("restart rejected keyframe")
	}
	if !c.sendBestEffortBinary([]byte("jpeg")) || len(c.videoOut) != 1 || string((<-c.videoOut).data) != "jpeg" {
		t.Fatal("JPEG did not keep newest-only behavior")
	}
}

func TestSatChatH264SenderKeyframeOverflowAndDisconnect(t *testing.T) {
	c, err := New(Config{URL: "ws://tater.test", DeviceID: "echo-test"}, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.connected.Store(true)
	c.setSatChatActive(true)
	for i := uint32(0); i < liveh264.QueuedFrames; i++ {
		c.SendSatChatH264(h264QueueFrame(i, 1, i == 0))
	}
	picture := h264QueueFrame(32, 1, true)
	if !c.SendSatChatH264(picture) || len(c.videoOut) != 1 {
		t.Fatal("overflow did not retain fresh keyframe")
	}
	picture.Data[4] = 0
	c.out <- outbound{kind: websocket.TextMessage, data: []byte("end-call")}
	control, err := c.nextOutbound(context.Background())
	if err != nil || string(control.data) != "end-call" {
		t.Fatal("video delayed control")
	}
	queued, err := liveh264.Decode((<-c.videoOut).data)
	if err != nil || queued.Sequence != 32 || !queued.Keyframe || queued.Data[4] != 0x65 {
		t.Fatal("keyframe content or ownership changed")
	}
	if c.sendBestEffortBinary([]byte("TSCV2invalid")) {
		t.Fatal("accepted invalid H.264")
	}
	c.SendSatChatH264(h264QueueFrame(33, 1, false))
	c.markDisconnected(nil)
	if len(c.videoOut) != 0 || len(c.out) != 0 {
		t.Fatal("disconnect retained outgoing media")
	}
	c.connected.Store(true)
	c.setSatChatActive(true)
	if c.SendSatChatH264(h264QueueFrame(34, 1, false)) {
		t.Fatal("reconnect accepted prediction without keyframe")
	}
}

func TestSatChatConcurrentVideoProducersRemainBounded(t *testing.T) {
	c, err := New(Config{URL: "ws://tater.test", DeviceID: "echo-test"}, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.connected.Store(true)
	c.setSatChatActive(true)
	var workers sync.WaitGroup
	for n := 0; n < 4; n++ {
		workers.Add(1)
		go func(stream uint64) {
			defer workers.Done()
			for i := uint32(0); i < 80; i++ {
				c.SendSatChatH264(h264QueueFrame(i, stream, i%30 == 0))
			}
		}(uint64(n + 1))
	}
	workers.Wait()
	if len(c.videoOut) > liveh264.QueuedFrames || len(c.out) != 0 {
		t.Fatal("video queue bounds or audio isolation violated")
	}
}

func TestDecodeSatChatStateIsImmediatelyActive(t *testing.T) {
	state := decodeSatChatState(map[string]any{
		"id": "call-1", "state": "active", "mode": "audio",
		"caller": "native:kitchen", "target": "native:office",
		"video_send_profile": "echo-low",
	})
	if state.ID != "call-1" || state.State != "active" || state.Mode != "audio" || state.VideoSendProfile != "echo-low" {
		t.Fatalf("decoded state = %#v", state)
	}
}

func TestSatChatSilenceGateHoldsSpeechThenReducesNoise(t *testing.T) {
	c, err := New(Config{URL: "ws://tater.test", DeviceID: "echo-test"}, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.connected.Store(true)
	c.setSatChatActive(true)
	frame := constantPCM(10000, 1280)

	c.PushAudioWithSpeech(frame, true)
	wantAmplitude(t, (<-c.out).data, 10000, 20)
	for index := 0; index < satChatSpeechHoldChunks; index++ {
		c.PushAudioWithSpeech(frame, false)
		wantAmplitude(t, (<-c.out).data, 10000, 20)
	}
	for index := 0; index < 5; index++ {
		c.PushAudioWithSpeech(frame, false)
		frameOut := (<-c.out).data
		if index == 4 {
			wantAmplitude(t, frameOut, int16(math.Round(10000*satChatSilenceGain)), 30)
		}
	}
	status := c.SatChatAudioStatus()
	if got := status["gate_gain"].(float64); got > 0.18 || got < satChatSilenceGain {
		t.Fatalf("silence gate gain = %.3f, want close to %.2f", got, satChatSilenceGain)
	}
}

func TestSatChatSilenceGateReopensImmediatelyForSpeech(t *testing.T) {
	c, err := New(Config{URL: "ws://tater.test", DeviceID: "echo-test"}, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.connected.Store(true)
	c.setSatChatActive(true)
	frame := constantPCM(10000, 1280)
	for index := 0; index < 6; index++ {
		c.PushAudioWithSpeech(frame, false)
		<-c.out
	}
	c.PushAudioWithSpeech(frame, true)
	got := (<-c.out).data
	wantAmplitude(t, got[:2], 10000, 20)
}

func constantPCM(value int16, samples int) []byte {
	pcm := make([]byte, samples*2)
	for index := 0; index < samples; index++ {
		binary.LittleEndian.PutUint16(pcm[index*2:], uint16(value))
	}
	return pcm
}

func wantAmplitude(t *testing.T, pcm []byte, want int16, tolerance float64) {
	t.Helper()
	last := int16(binary.LittleEndian.Uint16(pcm[len(pcm)-2:]))
	if math.Abs(float64(last-want)) > tolerance {
		t.Fatalf("last sample = %d, want %d (+/- %.0f)", last, want, tolerance)
	}
}
