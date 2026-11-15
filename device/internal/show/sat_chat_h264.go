package show

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/liveh264"
)

type satChatH264Relay struct {
	callID           string
	streamID         uint64
	packets          chan []byte
	done             chan struct{}
	keyframe         []byte
	waitingKeyframe  bool
	keyframeSequence uint32
}

func (s *Server) clearSatChatH264Locked() {
	if s.satChatH264 != nil {
		close(s.satChatH264.done)
		s.satChatH264 = nil
	}
	if s.state.SatChat != nil {
		s.state.SatChat.VideoStreamURL = ""
	}
}

func (s *Server) SetSatChatH264(wire []byte) {
	frame, err := liveh264.Decode(wire)
	if err != nil {
		return
	}
	s.mu.Lock()
	if s.closed || s.state.SatChat == nil || s.state.SatChat.Call == nil || s.state.SatChat.Call.State != "active" || s.state.SatChat.Call.Mode != "video" || s.imageListener == nil {
		s.mu.Unlock()
		return
	}
	callID := s.state.SatChat.Call.ID
	changed := s.satChatH264 == nil || s.satChatH264.callID != callID || s.satChatH264.streamID != frame.StreamID
	if changed {
		s.clearSatChatH264Locked()
		s.satChatH264 = &satChatH264Relay{callID: callID, streamID: frame.StreamID, packets: make(chan []byte, liveh264.QueuedFrames), done: make(chan struct{}), waitingKeyframe: true}
		s.state.SatChat.VideoFrameURL = ""
		s.state.SatChat.VideoStreamURL = fmt.Sprintf("http://%s/sat-chat-h264?stream=%d", s.imageListener.Addr(), frame.StreamID)
		s.state.UpdatedAtUnixMS = time.Now().UnixMilli()
	}
	relay := s.satChatH264
	owned := append([]byte(nil), wire...)
	if frame.Keyframe {
		relay.keyframe = owned
		relay.keyframeSequence = frame.Sequence
	}
	// Predictive H.264 frames are not independently disposable JPEGs. Keep a
	// bounded GOP through short reader stalls; overflow drops the entire
	// pending GOP and waits for a new independently decodable picture.
	if !relay.waitingKeyframe || frame.Keyframe {
		select {
		case relay.packets <- owned:
			relay.waitingKeyframe = false
		default:
		drain:
			for {
				select {
				case <-relay.packets:
				default:
					break drain
				}
			}
			relay.waitingKeyframe = true
			if frame.Keyframe {
				relay.packets <- owned
				relay.waitingKeyframe = false
			}
		}
	}
	s.mu.Unlock()
	if changed {
		select {
		case s.notify <- struct{}{}:
		default:
		}
	}
}

func (s *Server) serveSatChatH264(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.RLock()
	relay := s.satChatH264
	if relay == nil || r.URL.Query().Get("stream") != fmt.Sprint(relay.streamID) {
		s.mu.RUnlock()
		http.NotFound(w, r)
		return
	}
	keyframe := relay.keyframe
	primedSequence := relay.keyframeSequence
	s.mu.RUnlock()
	w.Header().Set("Content-Type", "application/vnd.tater.sat-chat.h264")
	w.Header().Set("Cache-Control", "no-store")
	control := http.NewResponseController(w)
	publish := func(packet []byte) bool {
		_ = control.SetWriteDeadline(time.Now().Add(5 * time.Second))
		err := liveh264.Write(w, packet)
		if err == nil {
			err = control.Flush()
		}
		if err != nil && os.Getenv("TATER_VDEC_TRACE") == "1" {
			log.Printf("[sat-chat-h264] loopback relay write stopped: %v", err)
		}
		return err == nil
	}
	if len(keyframe) > 0 && !publish(keyframe) {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-relay.done:
			return
		case packet := <-relay.packets:
			if len(keyframe) > 0 {
				frame, err := liveh264.Decode(packet)
				if err != nil || frame.Sequence <= primedSequence {
					continue
				}
			}
			if !publish(packet) {
				return
			}
		}
	}
}
