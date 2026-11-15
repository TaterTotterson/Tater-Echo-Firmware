package taternative

import (
	"bytes"
	"context"
	"encoding/binary"
	"sync"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/liveh264"

	"github.com/TaterTotterson/Tater-Echo-Firmware/pkg/speaker"
)

var satChatVideoPrefix = []byte{'T', 'S', 'C', 'V', '1'}

const maxSatChatVideoFrameBytes = 2 * 1024 * 1024

// EncodeSatChatVideoFrame tags one JPEG so Sat Chat can carry video and the
// established raw PCM stream over the same WebSocket without changing audio.
func EncodeSatChatVideoFrame(jpeg []byte) []byte {
	if len(jpeg) < 4 || len(jpeg) > maxSatChatVideoFrameBytes || jpeg[0] != 0xff || jpeg[1] != 0xd8 {
		return nil
	}
	frame := make([]byte, len(satChatVideoPrefix)+len(jpeg))
	copy(frame, satChatVideoPrefix)
	copy(frame[len(satChatVideoPrefix):], jpeg)
	return frame
}

func decodeSatChatVideoFrame(frame []byte) ([]byte, bool) {
	if len(frame) <= len(satChatVideoPrefix)+3 || len(frame)-len(satChatVideoPrefix) > maxSatChatVideoFrameBytes ||
		!bytes.HasPrefix(frame, satChatVideoPrefix) {
		return nil, false
	}
	jpeg := frame[len(satChatVideoPrefix):]
	if jpeg[0] != 0xff || jpeg[1] != 0xd8 {
		return nil, false
	}
	return jpeg, true
}

func decodeSatChatState(payload map[string]any) SatChatState {
	return SatChatState{
		ID: stringValue(payload["id"]), State: stringValue(payload["state"]),
		Mode: stringValue(payload["mode"]), Caller: stringValue(payload["caller"]),
		CallerName: stringValue(payload["caller_name"]), Target: stringValue(payload["target"]),
		TargetName: stringValue(payload["target_name"]), Reason: stringValue(payload["reason"]),
		VideoSendProfile: stringValue(payload["video_send_profile"]),
	}
}

func decodeSatChatParticipants(value any) []SatChatParticipant {
	rows, _ := value.([]any)
	participants := make([]SatChatParticipant, 0, len(rows))
	for _, value := range rows {
		row, _ := value.(map[string]any)
		if row == nil || stringValue(row["id"]) == "" {
			continue
		}
		participants = append(participants, SatChatParticipant{
			ID: stringValue(row["id"]), Name: stringValue(row["name"]),
			Room: stringValue(row["room"]), Board: stringValue(row["board"]),
			Audio: boolValue(row["audio"]), Video: boolValue(row["video"]),
			CanInitiate: boolValue(row["can_initiate"]), CanEnd: boolValue(row["can_end"]),
			HasUI: boolValue(row["has_ui"]),
			Busy:  boolValue(row["busy"]),
		})
	}
	return participants
}

// SatChatParticipant is one connected endpoint advertised by Tater.
type SatChatParticipant struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Room        string `json:"room,omitempty"`
	Board       string `json:"board,omitempty"`
	Audio       bool   `json:"audio"`
	Video       bool   `json:"video"`
	CanInitiate bool   `json:"can_initiate"`
	CanEnd      bool   `json:"can_end"`
	HasUI       bool   `json:"has_ui"`
	Busy        bool   `json:"busy"`
}

// SatChatState is the complete call state used by the native screen.
type SatChatState struct {
	ID               string `json:"id,omitempty"`
	State            string `json:"state,omitempty"`
	Mode             string `json:"mode,omitempty"`
	Caller           string `json:"caller,omitempty"`
	CallerName       string `json:"caller_name,omitempty"`
	Target           string `json:"target,omitempty"`
	TargetName       string `json:"target_name,omitempty"`
	Reason           string `json:"reason,omitempty"`
	VideoSendProfile string `json:"video_send_profile,omitempty"`
}

type liveVoiceSpeaker interface {
	SetVoiceLive(bool)
}

// SatChatPlayer converts the microphone wire format (16 kHz mono S16_LE) to
// the Echo voice plane (48 kHz mono S16_LE). Its bounded queue drops stale
// audio rather than allowing network jitter to turn into growing call delay.
type SatChatPlayer struct {
	Speaker speaker.Speaker

	mu     sync.Mutex
	cancel context.CancelFunc
	frames chan []byte
}

func NewSatChatPlayer(spk speaker.Speaker) *SatChatPlayer {
	return &SatChatPlayer{Speaker: spk}
}

func (p *SatChatPlayer) Start() {
	if p == nil || p.Speaker == nil {
		return
	}
	p.Stop()
	p.Speaker.Flush()
	p.Speaker.EndStream()
	if live, ok := p.Speaker.(liveVoiceSpeaker); ok {
		live.SetVoiceLive(true)
	}
	ctx, cancel := context.WithCancel(context.Background())
	frames := make(chan []byte, 24)
	p.mu.Lock()
	p.cancel = cancel
	p.frames = frames
	p.mu.Unlock()
	go p.run(ctx, frames)
}

func (p *SatChatPlayer) Stop() {
	if p == nil || p.Speaker == nil {
		return
	}
	p.mu.Lock()
	cancel := p.cancel
	p.cancel = nil
	p.frames = nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	p.Speaker.Flush()
	p.Speaker.EndStream()
	if live, ok := p.Speaker.(liveVoiceSpeaker); ok {
		live.SetVoiceLive(false)
	}
}

func (p *SatChatPlayer) Push(pcm []byte) {
	usable := len(pcm) - len(pcm)%2
	if p == nil || usable == 0 {
		return
	}
	frame := append([]byte(nil), pcm[:usable]...)
	p.mu.Lock()
	frames := p.frames
	p.mu.Unlock()
	if frames == nil {
		return
	}
	select {
	case frames <- frame:
	default:
		// Keep live speech live: evict the oldest network frame.
		select {
		case <-frames:
		default:
		}
		select {
		case frames <- frame:
		default:
		}
	}
}

func (p *SatChatPlayer) run(ctx context.Context, frames <-chan []byte) {
	pending := make([]byte, 0, playbackPeriod*2)
	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-frames:
			samples := make([]int16, len(frame)/2)
			for index := range samples {
				samples[index] = int16(binary.LittleEndian.Uint16(frame[index*2:]))
			}
			pending = append(pending, resampleS16(samples, SampleRate, playbackRate)...)
			for len(pending) >= playbackPeriod {
				period := append([]byte(nil), pending[:playbackPeriod]...)
				pending = pending[playbackPeriod:]
				if err := p.Speaker.PumpPeriod(period); err != nil {
					return
				}
			}
		}
	}
}

func (c *Client) StartSatChat(target, mode string) bool {
	return c.sendJSON("sat_chat.start", "", map[string]any{"target": target, "mode": mode})
}

func (c *Client) EndSatChat(callID string) bool {
	return c.sendJSON("sat_chat.end", "", map[string]any{"call_id": callID})
}

// SendSatChatVideo queues a best-effort JPEG frame. Video is deliberately
// droppable so camera cadence can never delay voice audio or end-call control.
func (c *Client) SendSatChatVideo(jpeg []byte) bool {
	if !c.satChatActive.Load() {
		return false
	}
	frame := EncodeSatChatVideoFrame(jpeg)
	if len(frame) == 0 {
		return false
	}
	return c.sendBestEffortBinary(frame)
}

func (c *Client) SendSatChatH264(frame liveh264.Frame) bool {
	if !c.satChatActive.Load() {
		return false
	}
	wire := liveh264.Encode(frame)
	return len(wire) > 0 && c.sendBestEffortBinary(wire)
}
