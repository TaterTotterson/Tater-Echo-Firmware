package taternative

import (
	"context"
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type drainTestSpeaker struct {
	ended      chan struct{}
	waiting    chan struct{}
	release    chan struct{}
	pumpCalled bool
}

func newDrainTestSpeaker() *drainTestSpeaker {
	return &drainTestSpeaker{
		ended:   make(chan struct{}),
		waiting: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (s *drainTestSpeaker) Init() error { return nil }
func (s *drainTestSpeaker) PumpPeriod([]byte) error {
	s.pumpCalled = true
	return nil
}
func (s *drainTestSpeaker) EndStream()             { close(s.ended) }
func (s *drainTestSpeaker) Flush()                 {}
func (s *drainTestSpeaker) PumpMusic([]byte) error { return nil }
func (s *drainTestSpeaker) EndMusicStream()        {}
func (s *drainTestSpeaker) FlushMusic()            {}
func (s *drainTestSpeaker) SetDuck(float64)        {}
func (s *drainTestSpeaker) Close()                 {}
func (s *drainTestSpeaker) WaitVoiceIdle(ctx context.Context) error {
	select {
	case <-s.ended:
	default:
		return errors.New("WaitVoiceIdle called before EndStream")
	}
	close(s.waiting)
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestDecodeWAVDownmixesAndResamples(t *testing.T) {
	// Two stereo frames at 24 kHz become four mono frames at 48 kHz.
	data := make([]byte, 8)
	binary.LittleEndian.PutUint16(data[0:], uint16(int16(1000)))
	binary.LittleEndian.PutUint16(data[2:], uint16(int16(3000)))
	negative := int16(-1000)
	binary.LittleEndian.PutUint16(data[4:], uint16(negative))
	binary.LittleEndian.PutUint16(data[6:], uint16(int16(1000)))
	wav := make([]byte, 44+len(data))
	copy(wav[0:], "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 2)
	binary.LittleEndian.PutUint32(wav[24:], 24000)
	binary.LittleEndian.PutUint32(wav[28:], 24000*4)
	binary.LittleEndian.PutUint16(wav[32:], 4)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], uint32(len(data)))
	copy(wav[44:], data)
	pcm, err := decodeAudio(wav, "audio/wav", "http://tater/test.wav")
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm) != 8 {
		t.Fatalf("decoded bytes = %d, want 8", len(pcm))
	}
	if got := int16(binary.LittleEndian.Uint16(pcm)); got != 2000 {
		t.Fatalf("first downmixed sample = %d, want 2000", got)
	}
}

func TestPlayVoiceWaitsForAudibleDrain(t *testing.T) {
	wav := make([]byte, 44+4)
	copy(wav[0:], "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], playbackRate)
	binary.LittleEndian.PutUint32(wav[28:], playbackRate*2)
	binary.LittleEndian.PutUint16(wav[32:], 2)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], 4)

	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(wav)
	}))
	defer httpServer.Close()

	speaker := newDrainTestSpeaker()
	player := NewLocalPlayer(speaker)
	done := make(chan error, 1)
	go func() {
		done <- player.PlayVoice(context.Background(), PlayRequest{URL: httpServer.URL})
	}()

	select {
	case <-speaker.waiting:
	case <-time.After(time.Second):
		t.Fatal("PlayVoice never reached audible-drain wait")
	}
	if !speaker.pumpCalled {
		t.Fatal("audio was not queued before drain wait")
	}
	select {
	case err := <-done:
		t.Fatalf("PlayVoice returned before audible playback drained: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(speaker.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("PlayVoice did not return after speaker became idle")
	}
}

func TestPlayEmbeddedSetupSoundWaitsForAudibleDrain(t *testing.T) {
	speaker := newDrainTestSpeaker()
	player := NewLocalPlayer(speaker)
	done := make(chan error, 1)
	go func() {
		done <- player.PlayEmbeddedSound(context.Background(), "short-definite-fart")
	}()
	select {
	case <-speaker.waiting:
	case <-time.After(time.Second):
		t.Fatal("embedded setup sound never reached audible-drain wait")
	}
	if !speaker.pumpCalled {
		t.Fatal("embedded setup sound was not queued")
	}
	close(speaker.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("embedded setup sound did not finish")
	}
}

func testMonoWAV(rate int, samples []int16) []byte {
	data := make([]byte, len(samples)*2)
	for index, sample := range samples {
		binary.LittleEndian.PutUint16(data[index*2:], uint16(sample))
	}
	wav := make([]byte, 44+len(data))
	copy(wav[0:], "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], uint32(rate))
	binary.LittleEndian.PutUint32(wav[28:], uint32(rate*2))
	binary.LittleEndian.PutUint16(wav[32:], 2)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], uint32(len(data)))
	copy(wav[44:], data)
	return wav
}

func waitWakeSoundReady(t *testing.T, player *LocalPlayer) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ready, _ := player.WakeSoundStatus()["wake_sound_ready"].(bool); ready {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("wake sound did not become ready: %#v", player.WakeSoundStatus())
}

func TestEveryBuiltInWakeSoundIsEmbeddedAndDecodes(t *testing.T) {
	player := NewLocalPlayer(nil)
	for settingID, embeddedID := range builtInWakeSounds {
		t.Run(settingID, func(t *testing.T) {
			pcm, embedded, err := player.loadWakeSound(context.Background(), "embedded:"+embeddedID)
			if err != nil {
				t.Fatal(err)
			}
			if !embedded {
				t.Fatal("bundled wake sound was treated as a network asset")
			}
			if len(pcm) == 0 || len(pcm)%2 != 0 {
				t.Fatalf("decoded PCM length = %d", len(pcm))
			}
		})
	}
}

func TestBuiltInWakeSoundNeedsNoNetwork(t *testing.T) {
	speaker := newDrainTestSpeaker()
	player := NewLocalPlayer(speaker)
	player.HTTP = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network must not be used for a built-in wake sound")
	})}
	if err := player.ConfigureWakeSound(map[string]any{
		"wake_sound_enabled": true,
		"wake_sound":         "pop-up-sound",
	}); err != nil {
		t.Fatal(err)
	}
	waitWakeSoundReady(t, player)
	if got := player.WakeSoundStatus()["wake_sound_download_url"]; got != "" {
		t.Fatalf("wake sound download URL = %#v, want empty after preparation", got)
	}
	if !player.PlayWakeSound() {
		t.Fatal("bundled wake sound did not play")
	}
	select {
	case <-speaker.ended:
	case <-time.After(time.Second):
		t.Fatal("bundled wake sound was not queued to the speaker")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestWakeSoundChangesLiveAndUsesDiskCache(t *testing.T) {
	wav := testMonoWAV(16000, []int16{500, -500, 1000, -1000})
	var downloads atomic.Int32
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		downloads.Add(1)
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(wav)
	}))
	defer httpServer.Close()

	cacheDir := t.TempDir()
	speaker := newDrainTestSpeaker()
	player := NewLocalPlayer(speaker)
	player.wakeCacheDir = cacheDir
	if err := player.ConfigureWakeSound(map[string]any{
		"wake_sound_enabled": true,
		"wake_sound":         "custom",
		"wake_sound_url":     httpServer.URL + "/wake.wav",
	}); err != nil {
		t.Fatal(err)
	}
	waitWakeSoundReady(t, player)
	if !player.PlayWakeSound() {
		t.Fatal("prepared wake sound did not play")
	}
	select {
	case <-speaker.ended:
	case <-time.After(time.Second):
		t.Fatal("wake sound was not queued to the speaker")
	}

	// A fresh player simulates a service restart. It must use the decoded
	// on-device cache instead of requiring the source URL again.
	second := NewLocalPlayer(newDrainTestSpeaker())
	second.wakeCacheDir = cacheDir
	if err := second.ConfigureWakeSound(map[string]any{
		"wake_sound_enabled": true,
		"wake_sound":         "custom",
		"wake_sound_url":     httpServer.URL + "/wake.wav",
	}); err != nil {
		t.Fatal(err)
	}
	waitWakeSoundReady(t, second)
	if got := downloads.Load(); got != 1 {
		t.Fatalf("wake sound downloads = %d, want 1", got)
	}
	if hits := second.WakeSoundStatus()["wake_sound_cache_hits"]; hits != uint64(1) {
		t.Fatalf("wake sound cache hits = %#v, want 1", hits)
	}

	if err := second.ConfigureWakeSound(map[string]any{
		"wake_sound_enabled": false,
		"wake_sound":         "no_sound",
	}); err != nil {
		t.Fatal(err)
	}
	if second.PlayWakeSound() {
		t.Fatal("disabled wake sound still played")
	}
}
