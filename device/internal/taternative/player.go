package taternative

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/pkg/speaker"
	"github.com/hajimehoshi/go-mp3"
)

const (
	playbackRate     = 48000
	playbackPeriod   = 4096
	maxAudioDownload = 64 * 1024 * 1024
	maxWakeSoundRaw  = 2 * 1024 * 1024
	maxWakeSoundPCM  = 8 * 1024 * 1024
	defaultWakeCache = "/data/local/share/tater/wake-sounds"
)

var builtInWakeSounds = map[string]string{
	"default":                "wake_word_triggered",
	"blip2":                  "blip2",
	"message-notification-4": "message-notification-4",
	"notification-ding":      "notification-ding",
	"notification-squeak":    "notification-squeak",
	"phone-chime":            "phone-chime",
	"pop-up-sound":           "pop-up-sound",
	"short-definite-fart":    "short-definite-fart",
	"star_treck_communications_start_transmission": "star_treck_communications_start_transmission",
	"star_treck_computer_work_beep":                "star_treck_computer_work_beep",
	"tater_notify_digital_blip":                    "tater_notify_digital_blip",
	"turning-off-microphone-percussion-1":          "turning-off-microphone-percussion-1",
	"wake_word_triggered":                          "wake_word_triggered",
	"waterdrop":                                    "waterdrop",
}

// LocalPlayer downloads WAV/MP3 audio, decodes it to the Echo speaker's
// 48 kHz mono S16_LE format and feeds the existing voice/music planes.
type LocalPlayer struct {
	Speaker     speaker.Speaker
	HTTP        *http.Client
	paused      atomic.Bool
	volume      atomic.Int32
	voiceMu     sync.Mutex
	alarmMu     sync.Mutex
	alarmCancel context.CancelFunc

	wakeMu               sync.RWMutex
	wakeCacheDir         string
	wakeEnabled          bool
	wakeID               string
	wakeURL              string
	wakePCM              []byte
	wakeGeneration       uint64
	wakeDownloadCancel   context.CancelFunc
	wakeDownloadRunning  bool
	wakeDownloadURL      string
	wakeCacheHits        uint64
	wakeCacheWrites      uint64
	wakeCacheFailures    uint64
	wakeDownloadFailures uint64
	wakeLastError        string
}

// voiceIdleWaiter is implemented by the hardware speaker. Keeping it
// optional lets the player remain usable with simple test speakers while the
// Echo can wait for its deep playback queue and ALSA buffer to become silent.
type voiceIdleWaiter interface {
	WaitVoiceIdle(context.Context) error
}

func NewLocalPlayer(spk speaker.Speaker) *LocalPlayer {
	p := &LocalPlayer{
		Speaker:      spk,
		HTTP:         &http.Client{Timeout: 90 * time.Second},
		wakeCacheDir: defaultWakeCache,
		wakeID:       "no_sound",
	}
	p.volume.Store(100)
	return p
}

func (p *LocalPlayer) PlayVoice(ctx context.Context, req PlayRequest) error {
	if p.Speaker == nil {
		return errors.New("speaker unavailable")
	}
	pcm, err := p.fetchAndDecode(ctx, req.URL)
	if err != nil {
		return err
	}
	p.voiceMu.Lock()
	defer p.voiceMu.Unlock()
	duckDB := 0.0
	if target := intValue(req.Ducking["target_percent"], 100); target < 100 {
		if target <= 0 {
			duckDB = -96
		} else {
			duckDB = 20 * math.Log10(float64(target)/100)
		}
		p.Speaker.SetDuck(duckDB)
		defer p.Speaker.SetDuck(0)
	}
	if err := p.pump(ctx, pcm, false, 100); err != nil {
		p.Speaker.Flush()
		p.Speaker.EndStream()
		return err
	}
	p.Speaker.EndStream()
	if waiter, ok := p.Speaker.(voiceIdleWaiter); ok {
		if err := waiter.WaitVoiceIdle(ctx); err != nil {
			p.Speaker.Flush()
			return err
		}
	}
	return nil
}

// ConfigureWakeSound applies Tater's live wake-sound settings and prepares
// the selected sound in the background. Built-ins are bundled from the same
// assets as the ESP32 satellites so they work without DNS or Internet access.
// Custom URLs are decoded and cached on disk so reconnects do not redownload.
func (p *LocalPlayer) ConfigureWakeSound(values map[string]any) error {
	p.wakeMu.Lock()
	enabled := p.wakeEnabled
	id := p.wakeID
	customURL := p.wakeURL
	if value, ok := values["wake_sound_enabled"]; ok {
		enabled = boolValue(value)
	}
	if value, ok := values["wake_sound"]; ok {
		id = strings.ToLower(strings.TrimSpace(stringValue(value)))
	}
	if value, ok := values["wake_sound_url"]; ok {
		customURL = strings.TrimSpace(stringValue(value))
	}
	if id == "" {
		id = "no_sound"
	}
	resolved := ""
	switch {
	case !enabled || id == "no_sound":
		enabled = false
	case id == "custom":
		resolved = customURL
	default:
		if embeddedID := builtInWakeSounds[id]; embeddedID != "" {
			resolved = "embedded:" + embeddedID
		}
	}
	if enabled && resolved == "" {
		p.wakeMu.Unlock()
		return fmt.Errorf("wake sound %q is not available", id)
	}
	if resolved != "" && !strings.HasPrefix(resolved, "embedded:") {
		parsed, err := url.Parse(resolved)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			p.wakeMu.Unlock()
			return fmt.Errorf("wake sound URL must use http:// or https://")
		}
	}

	unchanged := p.wakeEnabled == enabled && p.wakeID == id && p.wakeURL == resolved
	p.wakeEnabled = enabled
	p.wakeID = id
	p.wakeURL = resolved
	if unchanged && (!enabled || len(p.wakePCM) > 0 || p.wakeDownloadRunning) {
		p.wakeMu.Unlock()
		return nil
	}
	p.wakeGeneration++
	generation := p.wakeGeneration
	if p.wakeDownloadCancel != nil {
		p.wakeDownloadCancel()
		p.wakeDownloadCancel = nil
	}
	p.wakePCM = nil
	p.wakeLastError = ""
	if !enabled {
		p.wakeDownloadRunning = false
		p.wakeDownloadURL = ""
		p.wakeMu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.wakeDownloadCancel = cancel
	p.wakeDownloadRunning = true
	p.wakeDownloadURL = resolved
	p.wakeMu.Unlock()
	go p.prepareWakeSound(ctx, generation, resolved)
	return nil
}

func (p *LocalPlayer) prepareWakeSound(ctx context.Context, generation uint64, rawURL string) {
	pcm, cacheHit, err := p.loadWakeSound(ctx, rawURL)
	p.wakeMu.Lock()
	defer p.wakeMu.Unlock()
	if generation != p.wakeGeneration {
		return
	}
	p.wakeDownloadCancel = nil
	p.wakeDownloadRunning = false
	p.wakeDownloadURL = ""
	if err != nil {
		p.wakeDownloadFailures++
		p.wakeLastError = err.Error()
		return
	}
	if cacheHit {
		p.wakeCacheHits++
	} else {
		p.wakeCacheWrites++
	}
	p.wakePCM = pcm
	p.wakeLastError = ""
}

func (p *LocalPlayer) loadWakeSound(ctx context.Context, rawURL string) ([]byte, bool, error) {
	if embeddedID, ok := strings.CutPrefix(rawURL, "embedded:"); ok {
		encoded, exists := embeddedWakeSoundsGzipBase64[embeddedID]
		if !exists {
			return nil, false, fmt.Errorf("embedded wake sound %q is unavailable", embeddedID)
		}
		compressed, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, false, fmt.Errorf("decode embedded wake sound %q: %w", embeddedID, err)
		}
		reader, err := gzip.NewReader(bytes.NewReader(compressed))
		if err != nil {
			return nil, false, fmt.Errorf("open embedded wake sound %q: %w", embeddedID, err)
		}
		raw, readErr := io.ReadAll(io.LimitReader(reader, maxWakeSoundRaw+1))
		closeErr := reader.Close()
		if readErr != nil {
			return nil, false, fmt.Errorf("read embedded wake sound %q: %w", embeddedID, readErr)
		}
		if closeErr != nil {
			return nil, false, fmt.Errorf("close embedded wake sound %q: %w", embeddedID, closeErr)
		}
		if len(raw) > maxWakeSoundRaw {
			return nil, false, fmt.Errorf("embedded wake sound %q exceeds %d bytes", embeddedID, maxWakeSoundRaw)
		}
		pcm, err := decodeAudio(raw, "audio/wav", embeddedID+".wav")
		if err != nil {
			return nil, false, fmt.Errorf("decode embedded wake sound %q: %w", embeddedID, err)
		}
		if len(pcm) == 0 || len(pcm) > maxWakeSoundPCM {
			return nil, false, fmt.Errorf("decoded wake sound exceeds %d bytes", maxWakeSoundPCM)
		}
		return pcm, true, nil
	}

	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(rawURL)))
	cachePath := filepath.Join(p.wakeCacheDir, digest+".pcm")
	if cached, err := os.ReadFile(cachePath); err == nil {
		if len(cached) > 0 && len(cached) <= maxWakeSoundPCM && len(cached)%2 == 0 {
			return cached, true, nil
		}
		p.wakeMu.Lock()
		p.wakeCacheFailures++
		p.wakeMu.Unlock()
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, false, fmt.Errorf("create wake sound request: %w", err)
	}
	response, err := p.HTTP.Do(request)
	if err != nil {
		return nil, false, fmt.Errorf("download wake sound: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, false, fmt.Errorf("download wake sound: HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxWakeSoundRaw+1))
	if err != nil {
		return nil, false, fmt.Errorf("read wake sound: %w", err)
	}
	if len(raw) > maxWakeSoundRaw {
		return nil, false, fmt.Errorf("wake sound exceeds %d bytes", maxWakeSoundRaw)
	}
	contentType := strings.ToLower(strings.Split(response.Header.Get("Content-Type"), ";")[0])
	pcm, err := decodeAudio(raw, contentType, rawURL)
	if err != nil {
		return nil, false, err
	}
	if len(pcm) == 0 || len(pcm) > maxWakeSoundPCM {
		return nil, false, fmt.Errorf("decoded wake sound exceeds %d bytes", maxWakeSoundPCM)
	}
	if err := os.MkdirAll(p.wakeCacheDir, 0o755); err != nil {
		p.wakeMu.Lock()
		p.wakeCacheFailures++
		p.wakeMu.Unlock()
		return pcm, false, nil
	}
	temporary, err := os.CreateTemp(p.wakeCacheDir, ".wake-*.pcm")
	if err != nil {
		p.wakeMu.Lock()
		p.wakeCacheFailures++
		p.wakeMu.Unlock()
		return pcm, false, nil
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if _, err = temporary.Write(pcm); err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temporaryName, cachePath)
	}
	if err != nil {
		p.wakeMu.Lock()
		p.wakeCacheFailures++
		p.wakeMu.Unlock()
	}
	return pcm, false, nil
}

// PlayWakeSound queues the prepared sound locally without delaying voice.start.
// False means wake sounds are disabled or the newly selected asset is still
// downloading. Barge-in callers deliberately skip this hook altogether.
func (p *LocalPlayer) PlayWakeSound() bool {
	p.wakeMu.RLock()
	if !p.wakeEnabled || len(p.wakePCM) == 0 || p.Speaker == nil {
		p.wakeMu.RUnlock()
		return false
	}
	pcm := p.wakePCM
	p.wakeMu.RUnlock()
	go func() {
		p.voiceMu.Lock()
		defer p.voiceMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := p.pump(ctx, pcm, false, 100); err != nil {
			p.Speaker.Flush()
		}
		p.Speaker.EndStream()
	}()
	return true
}

// WakeSoundStatus is shaped like the ESP32 wake-engine diagnostics consumed by
// Tater's satellite details page.
func (p *LocalPlayer) WakeSoundStatus() map[string]any {
	p.wakeMu.RLock()
	defer p.wakeMu.RUnlock()
	return map[string]any{
		"wake_sound_download_running":  p.wakeDownloadRunning,
		"wake_sound_download_url":      p.wakeDownloadURL,
		"wake_sound_cache_hits":        p.wakeCacheHits,
		"wake_sound_cache_writes":      p.wakeCacheWrites,
		"wake_sound_cache_failures":    p.wakeCacheFailures,
		"wake_sound_download_failures": p.wakeDownloadFailures,
		"wake_sound_ready":             p.wakeEnabled && len(p.wakePCM) > 0,
		"wake_sound_id":                p.wakeID,
		"wake_sound_last_error":        p.wakeLastError,
	}
}

func (p *LocalPlayer) StopVoice() { p.Speaker.Flush() }

func (p *LocalPlayer) PlayMedia(ctx context.Context, req MediaRequest) error {
	if p.Speaker == nil {
		return errors.New("speaker unavailable")
	}
	p.volume.Store(int32(clamp(req.VolumePercent, 0, 100)))
	p.paused.Store(false)
	pcm, err := p.fetchAndDecode(ctx, req.URL)
	if err != nil {
		return err
	}
	start := req.StartPositionMS * playbackRate * 2 / 1000
	start -= start % 2
	if start > len(pcm) {
		start = len(pcm)
	}
	pcm = pcm[start:]
	defer p.Speaker.EndMusicStream()
	for {
		if err := p.pump(ctx, pcm, true, int(p.volume.Load())); err != nil {
			p.Speaker.FlushMusic()
			return err
		}
		if !req.Loop {
			return nil
		}
	}
}

func (p *LocalPlayer) StopMedia()   { p.Speaker.FlushMusic() }
func (p *LocalPlayer) PauseMedia()  { p.paused.Store(true) }
func (p *LocalPlayer) ResumeMedia() { p.paused.Store(false) }
func (p *LocalPlayer) SetMediaVolume(percent int) {
	p.volume.Store(int32(clamp(percent, 0, 100)))
}

// SetTimerAlarm starts or stops a local repeating two-tone alarm. The tone
// uses the voice plane and needs no separately installed audio asset.
func (p *LocalPlayer) SetTimerAlarm(active bool) {
	p.alarmMu.Lock()
	if p.alarmCancel != nil {
		p.alarmCancel()
		p.alarmCancel = nil
	}
	if !active || p.Speaker == nil {
		p.alarmMu.Unlock()
		if !active && p.Speaker != nil {
			p.Speaker.Flush()
			p.Speaker.EndStream()
		}
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.alarmCancel = cancel
	p.alarmMu.Unlock()
	go func() {
		defer p.Speaker.EndStream()
		tone := timerTone()
		for {
			if err := p.pump(ctx, tone, false, 80); err != nil {
				return
			}
		}
	}()
}

func timerTone() []byte {
	const duration = 900 * time.Millisecond
	samples := int(duration.Seconds() * playbackRate)
	out := make([]byte, samples*2)
	for i := 0; i < samples; i++ {
		phase := float64(i) / playbackRate
		frequency := 660.0
		if (i/(playbackRate/6))%2 == 1 {
			frequency = 880
		}
		envelope := 1.0
		cycle := i % (playbackRate / 6)
		if cycle < 240 {
			envelope = float64(cycle) / 240
		}
		value := int16(math.Sin(2*math.Pi*frequency*phase) * 9000 * envelope)
		binary.LittleEndian.PutUint16(out[i*2:], uint16(value))
	}
	return out
}

func (p *LocalPlayer) pump(ctx context.Context, pcm []byte, music bool, initialVolume int) error {
	for offset := 0; offset < len(pcm); {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if music && p.paused.Load() {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(20 * time.Millisecond):
				continue
			}
		}
		end := offset + playbackPeriod
		if end > len(pcm) {
			end = len(pcm)
		}
		chunk := pcm[offset:end]
		if len(chunk)%2 != 0 {
			chunk = chunk[:len(chunk)-1]
		}
		if music {
			volume := int(p.volume.Load())
			if volume != 100 {
				chunk = scalePCM(chunk, volume)
			}
			if err := p.Speaker.PumpMusic(chunk); err != nil {
				return err
			}
		} else {
			if initialVolume != 100 {
				chunk = scalePCM(chunk, initialVolume)
			}
			if err := p.Speaker.PumpPeriod(chunk); err != nil {
				return err
			}
		}
		offset = end
	}
	return nil
}

func scalePCM(raw []byte, percent int) []byte {
	out := append([]byte(nil), raw...)
	factor := float64(clamp(percent, 0, 100)) / 100
	for i := 0; i+1 < len(out); i += 2 {
		sample := int16(binary.LittleEndian.Uint16(out[i:]))
		value := int(math.Round(float64(sample) * factor))
		if value > math.MaxInt16 {
			value = math.MaxInt16
		} else if value < math.MinInt16 {
			value = math.MinInt16
		}
		binary.LittleEndian.PutUint16(out[i:], uint16(int16(value)))
	}
	return out
}

func (p *LocalPlayer) fetchAndDecode(ctx context.Context, rawURL string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create audio request: %w", err)
	}
	response, err := p.HTTP.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download audio: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("download audio: HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAudioDownload+1))
	if err != nil {
		return nil, fmt.Errorf("read audio: %w", err)
	}
	if len(body) > maxAudioDownload {
		return nil, fmt.Errorf("audio exceeds %d bytes", maxAudioDownload)
	}
	contentType := strings.ToLower(strings.Split(response.Header.Get("Content-Type"), ";")[0])
	return decodeAudio(body, contentType, rawURL)
}

func decodeAudio(raw []byte, contentType, rawURL string) ([]byte, error) {
	if len(raw) >= 12 && string(raw[:4]) == "RIFF" && string(raw[8:12]) == "WAVE" {
		return decodeWAV(raw)
	}
	ext := ""
	if u, err := url.Parse(rawURL); err == nil {
		ext = strings.ToLower(filepath.Ext(u.Path))
	}
	if contentType == "audio/mpeg" || contentType == "audio/mp3" || ext == ".mp3" || (len(raw) >= 3 && string(raw[:3]) == "ID3") {
		decoder, err := mp3.NewDecoder(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("decode MP3: %w", err)
		}
		stereo, err := io.ReadAll(io.LimitReader(decoder, maxAudioDownload*4))
		if err != nil {
			return nil, fmt.Errorf("read decoded MP3: %w", err)
		}
		mono := downmixS16(stereo, 2)
		return resampleS16(mono, decoder.SampleRate(), playbackRate), nil
	}
	return nil, fmt.Errorf("unsupported audio type %q", contentType)
}

func decodeWAV(raw []byte) ([]byte, error) {
	var format, channels, rate, bits int
	var data []byte
	for offset := 12; offset+8 <= len(raw); {
		name := string(raw[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(raw[offset+4 : offset+8]))
		offset += 8
		if size < 0 || offset+size > len(raw) {
			return nil, errors.New("invalid WAV chunk length")
		}
		chunk := raw[offset : offset+size]
		switch name {
		case "fmt ":
			if len(chunk) < 16 {
				return nil, errors.New("short WAV fmt chunk")
			}
			format = int(binary.LittleEndian.Uint16(chunk[0:2]))
			channels = int(binary.LittleEndian.Uint16(chunk[2:4]))
			rate = int(binary.LittleEndian.Uint32(chunk[4:8]))
			bits = int(binary.LittleEndian.Uint16(chunk[14:16]))
		case "data":
			data = chunk
		}
		offset += size + size%2
	}
	if format != 1 || bits != 16 || channels < 1 || channels > 8 || rate < 8000 || rate > 192000 || len(data) == 0 {
		return nil, fmt.Errorf("unsupported WAV format=%d channels=%d rate=%d bits=%d", format, channels, rate, bits)
	}
	mono := downmixS16(data, channels)
	return resampleS16(mono, rate, playbackRate), nil
}

func downmixS16(raw []byte, channels int) []int16 {
	frameBytes := channels * 2
	frames := len(raw) / frameBytes
	out := make([]int16, frames)
	for frame := 0; frame < frames; frame++ {
		var sum int64
		for channel := 0; channel < channels; channel++ {
			offset := frame*frameBytes + channel*2
			sum += int64(int16(binary.LittleEndian.Uint16(raw[offset:])))
		}
		out[frame] = int16(sum / int64(channels))
	}
	return out
}

func resampleS16(in []int16, sourceRate, targetRate int) []byte {
	if len(in) == 0 || sourceRate <= 0 || targetRate <= 0 {
		return nil
	}
	count := int(math.Ceil(float64(len(in)) * float64(targetRate) / float64(sourceRate)))
	out := make([]byte, count*2)
	if len(in) == 1 {
		for i := 0; i < count; i++ {
			binary.LittleEndian.PutUint16(out[i*2:], uint16(in[0]))
		}
		return out
	}
	ratio := float64(sourceRate) / float64(targetRate)
	for i := 0; i < count; i++ {
		position := float64(i) * ratio
		left := int(position)
		if left >= len(in)-1 {
			left = len(in) - 1
			binary.LittleEndian.PutUint16(out[i*2:], uint16(in[left]))
			continue
		}
		fraction := position - float64(left)
		value := float64(in[left]) + fraction*float64(int(in[left+1])-int(in[left]))
		binary.LittleEndian.PutUint16(out[i*2:], uint16(int16(math.Round(value))))
	}
	return out
}
