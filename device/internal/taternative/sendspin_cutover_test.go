package taternative

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type sceneTestSpeaker struct {
	mu        sync.Mutex
	music     []byte
	ended     int
	duckDB    float64
	duckCalls []timedDuckCall
}

type timedDuckCall struct {
	db       float64
	duration time.Duration
}

type rampWaitTestSpeaker struct {
	*sceneTestSpeaker
	waited chan struct{}
}

func (s *rampWaitTestSpeaker) WaitDuckRamp(context.Context) error {
	close(s.waited)
	return nil
}

func (s *sceneTestSpeaker) Init() error             { return nil }
func (s *sceneTestSpeaker) PumpPeriod([]byte) error { return nil }
func (s *sceneTestSpeaker) EndStream()              {}
func (s *sceneTestSpeaker) Flush()                  {}
func (s *sceneTestSpeaker) FlushMusic()             {}
func (s *sceneTestSpeaker) Close()                  {}
func (s *sceneTestSpeaker) SetDuck(db float64)      { s.duckDB = db }
func (s *sceneTestSpeaker) SetDuckRamp(db float64, duration time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.duckDB = db
	s.duckCalls = append(s.duckCalls, timedDuckCall{db: db, duration: duration})
}
func (s *sceneTestSpeaker) PumpMusic(data []byte) error {
	s.mu.Lock()
	s.music = append(s.music, data...)
	s.mu.Unlock()
	return nil
}
func (s *sceneTestSpeaker) EndMusicStream() {
	s.mu.Lock()
	s.ended++
	s.mu.Unlock()
}

func stereoWAV(left, right []int16) []byte {
	frames := len(left)
	data := make([]byte, frames*4)
	for index := 0; index < frames; index++ {
		binary.LittleEndian.PutUint16(data[index*4:], uint16(left[index]))
		binary.LittleEndian.PutUint16(data[index*4+2:], uint16(right[index]))
	}
	wav := make([]byte, 44+len(data))
	copy(wav[0:], "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 2)
	binary.LittleEndian.PutUint32(wav[24:], playbackRate)
	binary.LittleEndian.PutUint32(wav[28:], playbackRate*4)
	binary.LittleEndian.PutUint16(wav[32:], 4)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], uint32(len(data)))
	copy(wav[44:], data)
	return wav
}

func TestEchoAdvertisesSendspinInsteadOfTaterMediaSync(t *testing.T) {
	capabilities := DefaultCapabilities()
	for _, name := range []string{
		"sendspin_player", "sendspin_output_channel_selection", "audio_scenes", "tts_overlays",
	} {
		if capabilities[name] != true {
			t.Fatalf("capability %s = %#v", name, capabilities[name])
		}
	}
	if capabilities["sendspin_version"] != 1 {
		t.Fatalf("sendspin_version = %#v, want 1", capabilities["sendspin_version"])
	}
	wantModes := []string{"stereo", "left", "right", "mono"}
	gotModes, ok := capabilities["sendspin_output_channel_modes"].([]string)
	if !ok || len(gotModes) != len(wantModes) {
		t.Fatalf("Sendspin channel modes = %#v", capabilities["sendspin_output_channel_modes"])
	}
	for _, retired := range []string{
		"audio_session_version",
		"persistent_media_sessions", "synchronized_media_sessions", "stereo_channel_selection",
		"media_playhead_telemetry", "media_render_clock", "media_drift_correction",
		"media_rate_slew", "media_underrun_recovery", "synchronized_tts_overlays",
	} {
		if _, exists := capabilities[retired]; exists {
			t.Fatalf("retired capability %s is still advertised", retired)
		}
	}
}

func TestRetiredTaterSyncCommandsAreIgnored(t *testing.T) {
	c, err := New(Config{URL: "ws://tater.test", DeviceID: "echo-test"}, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.connected.Store(true)
	for _, command := range []string{
		"audio.clock.sync", "media.session.prepare", "media.session.commit",
		"media.session.adjust", "media.session.stop",
	} {
		c.handle(Envelope{Type: command, ID: "retired", Payload: map[string]any{}})
	}
	select {
	case frame := <-c.out:
		t.Fatalf("retired sync command emitted a response: %s", frame.data)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestStereoChannelDecoderStillSupportsLocalScenes(t *testing.T) {
	wav := stereoWAV([]int16{100, 200}, []int16{1000, 2000})
	left, err := decodeAudioChannel(wav, "audio/wav", "test.wav", "left")
	if err != nil {
		t.Fatal(err)
	}
	right, err := decodeAudioChannel(wav, "audio/wav", "test.wav", "right")
	if err != nil {
		t.Fatal(err)
	}
	mono, err := decodeAudioChannel(wav, "audio/wav", "test.wav", "mono")
	if err != nil {
		t.Fatal(err)
	}
	if got := int16(binary.LittleEndian.Uint16(left)); got != 100 {
		t.Fatalf("left sample = %d, want 100", got)
	}
	if got := int16(binary.LittleEndian.Uint16(right)); got != 1000 {
		t.Fatalf("right sample = %d, want 1000", got)
	}
	if got := int16(binary.LittleEndian.Uint16(mono)); got != 550 {
		t.Fatalf("mono sample = %d, want 550", got)
	}
}

func TestLoopedLocalSceneMediaHonorsCancellation(t *testing.T) {
	asset, _, err := newMediaAsset(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer asset.Close()
	if _, err := asset.file.Write([]byte{0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	asset.publish(2, nil, true)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cursor := int64(2)
	done := make(chan error, 1)
	go func() {
		_, _, _, readErr := asset.readFrames(ctx, &cursor, 1, true)
		done <- readErr
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("looped media cancellation error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("looped media ignored scene cancellation")
	}
}

func TestNativeOverlayRemainsAvailableAfterSendspinCutover(t *testing.T) {
	received := make(chan OverlayRequest, 1)
	c, err := New(Config{URL: "ws://tater.test", DeviceID: "echo-test"}, Hooks{
		PlayOverlay: func(_ context.Context, req OverlayRequest, started func()) error {
			received <- req
			started()
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.connected.Store(true)
	c.handle(Envelope{Type: "audio.overlay.start", ID: "request", Payload: map[string]any{
		"overlay_id": "reply-1", "group_id": "kitchen", "start_at_us": float64(1234),
		"foreground": map[string]any{"url": "http://tater/reply.wav", "kind": "tts", "volume_percent": float64(80)},
		"ducking":    map[string]any{"target_percent": float64(35), "attack_ms": float64(150), "release_ms": float64(350)},
		"finish":     map[string]any{"stop_media": true, "fade_ms": float64(500)},
	}})
	select {
	case req := <-received:
		if req.OverlayID != "reply-1" || req.VolumePercent != 80 || req.DuckingTargetPercent != 35 {
			t.Fatalf("overlay request = %#v", req)
		}
	case <-time.After(time.Second):
		t.Fatal("overlay hook was not called")
	}
	wanted := map[string]bool{"audio.overlay.started": false, "audio.overlay.finished": false}
	deadline := time.After(time.Second)
	for !wanted["audio.overlay.started"] || !wanted["audio.overlay.finished"] {
		select {
		case frame := <-c.out:
			var envelope Envelope
			if err := json.Unmarshal(frame.data, &envelope); err != nil {
				t.Fatal(err)
			}
			if _, ok := wanted[envelope.Type]; ok {
				wanted[envelope.Type] = true
			}
		case <-deadline:
			t.Fatalf("overlay events missing: %#v", wanted)
		}
	}
}

func TestOverlayKeepsExactDuckTimings(t *testing.T) {
	wav := stereoWAV([]int16{0, 0, 0, 0}, []int16{0, 0, 0, 0})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(wav)
	}))
	defer server.Close()

	speaker := &sceneTestSpeaker{}
	player := NewLocalPlayer(speaker)
	request := OverlayRequest{
		URL: server.URL, VolumePercent: 0, DuckingTargetPercent: 25,
		DuckingAttack: 5 * time.Millisecond, DuckingRelease: 7 * time.Millisecond,
	}
	if err := player.PlayOverlay(context.Background(), request, nil); err != nil {
		t.Fatal(err)
	}
	speaker.mu.Lock()
	defer speaker.mu.Unlock()
	want := []timedDuckCall{
		{db: 20 * math.Log10(.25), duration: 5 * time.Millisecond},
		{db: 0, duration: 7 * time.Millisecond},
	}
	if len(speaker.duckCalls) != len(want) {
		t.Fatalf("duck calls = %#v, want %#v", speaker.duckCalls, want)
	}
	for index := range want {
		if math.Abs(speaker.duckCalls[index].db-want[index].db) > .001 ||
			speaker.duckCalls[index].duration != want[index].duration {
			t.Fatalf("duck call %d = %#v, want %#v", index, speaker.duckCalls[index], want[index])
		}
	}
}

func TestOverlayWaitsForRenderedFadeInsteadOfWallClock(t *testing.T) {
	wav := stereoWAV([]int16{0, 0}, []int16{0, 0})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(wav)
	}))
	defer server.Close()

	speaker := &rampWaitTestSpeaker{sceneTestSpeaker: &sceneTestSpeaker{}, waited: make(chan struct{})}
	player := NewLocalPlayer(speaker)
	request := OverlayRequest{
		URL: server.URL, VolumePercent: 100, DuckingTargetPercent: 35,
		StopMediaWhenFinished: true, BackgroundFadeOut: 5 * time.Second,
	}
	started := time.Now()
	if err := player.PlayOverlay(context.Background(), request, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-speaker.waited:
	default:
		t.Fatal("overlay did not wait for the renderer's fade completion")
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("overlay used wall-clock fade sleep: %v", elapsed)
	}
}

func TestSceneKeepsMusicMutedUntilBackgroundStops(t *testing.T) {
	speaker := &sceneTestSpeaker{}
	player := NewLocalPlayer(speaker)
	setSpeakerDuck(speaker, -96, 500*time.Millisecond)

	cancelled := make(chan struct{})
	backgroundDone := make(chan error)
	finished := make(chan error, 1)
	go func() {
		finished <- player.finishSceneBackground(func() { close(cancelled) }, backgroundDone, nil)
	}()

	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("scene background was not cancelled")
	}
	speaker.mu.Lock()
	duckBeforeStop := speaker.duckDB
	speaker.mu.Unlock()
	if duckBeforeStop != -96 {
		t.Fatalf("music gain reset before background stopped: %v dB", duckBeforeStop)
	}

	backgroundDone <- context.Canceled
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	speaker.mu.Lock()
	duckAfterStop := speaker.duckDB
	speaker.mu.Unlock()
	if duckAfterStop != 0 {
		t.Fatalf("music gain after background stop = %v dB, want 0", duckAfterStop)
	}
}
