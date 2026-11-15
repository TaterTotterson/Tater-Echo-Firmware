package show

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/liveh264"
)

func TestSatChatSnapshotDoesNotAliasLiveCallState(t *testing.T) {
	s := New("127.0.0.1:0", Snapshot{SatChat: &SatChat{Call: &SatChatCall{ID: "call", State: "active", Mode: "video"}, Participants: []SatChatParticipant{{ID: "peer"}}}}, nil)
	before := s.Snapshot()
	s.Update(func(state *Snapshot) {
		state.SatChat.VideoStreamURL = "new-stream"
		state.SatChat.Call.State = "ended"
		state.SatChat.Participants[0].ID = "changed"
	})
	if before.SatChat.VideoStreamURL != "" || before.SatChat.Call.State != "active" || before.SatChat.Participants[0].ID != "peer" {
		t.Fatal("snapshot changed after its read lock was released")
	}
	before.SatChat.Call.State = "mutated-reader"
	if s.Snapshot().SatChat.Call.State == "mutated-reader" {
		t.Fatal("snapshot reader can mutate live call state")
	}
}

func TestH264RelayHasStableURLAndClosesAtCallEnd(t *testing.T) {
	s := New("127.0.0.1:0", Snapshot{SatChat: &SatChat{Call: &SatChatCall{ID: "call", State: "active", Mode: "video"}}}, nil)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	s.imageListener = listener
	f := liveh264.Frame{Width: 1280, Height: 720, FPS: 30, StreamID: 123, Keyframe: true, Data: []byte{0, 0, 0, 1, 0x65, 0x88}}
	s.SetSatChatH264(liveh264.Encode(f))
	url := s.Snapshot().SatChat.VideoStreamURL
	if url == "" {
		t.Fatal("missing stream URL")
	}
	relay := s.satChatH264
	for n := 1; n < 12; n++ {
		f.Sequence = uint32(n)
		f.PTSUS = int64(n) * 33333
		f.Keyframe = false
		s.SetSatChatH264(liveh264.Encode(f))
	}
	if s.Snapshot().SatChat.VideoStreamURL != url || len(relay.packets) != 12 {
		t.Fatal("URL churn or unbounded video queue")
	}
	recorder := httptest.NewRecorder()
	s.serveSatChatH264(recorder, httptest.NewRequest(http.MethodGet, "/sat-chat-h264?stream=old", nil))
	if recorder.Code != 404 {
		t.Fatal("accepted stale stream")
	}
	s.Update(func(state *Snapshot) { state.SatChat.Call = nil })
	select {
	case <-relay.done:
	default:
		t.Fatal("call end kept H.264 stream alive")
	}
	if s.Snapshot().SatChat.VideoStreamURL != "" {
		t.Fatal("call end retained stream URL")
	}
	s.SetSatChatH264(liveh264.Encode(f))
	if s.satChatH264 != nil {
		t.Fatal("late packet resurrected call")
	}
}

func TestH264RelayOverflowDiscardsWholeGOPAndWaitsForKeyframe(t *testing.T) {
	s := New("127.0.0.1:0", Snapshot{SatChat: &SatChat{Call: &SatChatCall{ID: "call", State: "active", Mode: "video"}}}, nil)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	s.imageListener = listener
	f := liveh264.Frame{Width: 1280, Height: 960, FPS: 30, StreamID: 123, Keyframe: true, Data: []byte{0, 0, 0, 1, 0x65, 0x88}}
	s.SetSatChatH264(liveh264.Encode(f))
	for n := 1; n <= liveh264.QueuedFrames+4; n++ {
		f.Sequence = uint32(n)
		f.Keyframe = false
		s.SetSatChatH264(liveh264.Encode(f))
	}
	if len(s.satChatH264.packets) != 0 {
		t.Fatal("overflow retained dependent frames without their GOP")
	}
	f.Sequence = 60
	f.Keyframe = true
	s.SetSatChatH264(liveh264.Encode(f))
	f.Sequence = 61
	f.Keyframe = false
	s.SetSatChatH264(liveh264.Encode(f))
	for _, want := range []uint32{60, 61} {
		got, err := liveh264.Decode(<-s.satChatH264.packets)
		if err != nil || got.Sequence != want {
			t.Fatal(got.Sequence, err)
		}
	}
}

func TestH264RelayCachedKeyframeDoesNotReplayEarlierPackets(t *testing.T) {
	s := New("127.0.0.1:0", Snapshot{SatChat: &SatChat{Call: &SatChatCall{ID: "call", State: "active", Mode: "video"}}}, nil)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	s.imageListener = listener
	f := liveh264.Frame{Width: 1280, Height: 720, FPS: 30, StreamID: 123, Data: []byte{0, 0, 0, 1, 0x65, 0x88}}
	for n := 0; n < 4; n++ {
		f.Sequence = uint32(n)
		f.Keyframe = n%2 == 0
		s.SetSatChatH264(liveh264.Encode(f))
	}
	server := httptest.NewServer(http.HandlerFunc(s.serveSatChatH264))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/sat-chat-h264?stream=123", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	for _, want := range []uint32{2, 3} {
		frame, err := liveh264.Read(response.Body)
		if err != nil || frame.Sequence != want {
			t.Fatal(frame.Sequence, want, err)
		}
	}
	cancel()
}
