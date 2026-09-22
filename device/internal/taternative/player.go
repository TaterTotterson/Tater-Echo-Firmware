package taternative

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hajimehoshi/go-mp3"
	"github.com/wilbowes/EchoMuse/pkg/speaker"
)

const (
	playbackRate     = 48000
	playbackPeriod   = 4096
	maxAudioDownload = 64 * 1024 * 1024
)

// LocalPlayer downloads WAV/MP3 audio, decodes it to the Echo speaker's
// 48 kHz mono S16_LE format and feeds the existing voice/music planes.
type LocalPlayer struct {
	Speaker     speaker.Speaker
	HTTP        *http.Client
	paused      atomic.Bool
	volume      atomic.Int32
	alarmMu     sync.Mutex
	alarmCancel context.CancelFunc
}

func NewLocalPlayer(spk speaker.Speaker) *LocalPlayer {
	p := &LocalPlayer{
		Speaker: spk,
		HTTP:    &http.Client{Timeout: 90 * time.Second},
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
	defer p.Speaker.EndStream()
	if err := p.pump(ctx, pcm, false, 100); err != nil {
		p.Speaker.Flush()
		return err
	}
	return nil
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
