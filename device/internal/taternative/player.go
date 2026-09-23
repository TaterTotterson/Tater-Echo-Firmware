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
	playbackRate            = 48000
	playbackPeriod          = 4096
	echoOutputLatencyFrames = 4096
	maxAudioDownload        = 64 * 1024 * 1024
	maxWakeSoundRaw         = 2 * 1024 * 1024
	maxWakeSoundPCM         = 8 * 1024 * 1024
	defaultWakeCache        = "/data/local/share/tater/wake-sounds"
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
	Speaker             speaker.Speaker
	HTTP                *http.Client
	paused              atomic.Bool
	volume              atomic.Int32
	voiceMu             sync.Mutex
	alarmMu             sync.Mutex
	alarmCancel         context.CancelFunc
	mediaMu             sync.Mutex
	prepared            *preparedMedia
	mediaAdjust         atomic.Int64
	mediaLastCorrection atomic.Int64
	mediaCorrections    []mediaCorrection
	mediaSlew           mediaSlew
	mediaRebuffering    atomic.Bool
	mediaRejoinCount    atomic.Uint64
	mediaRejoinFrames   atomic.Uint64
	mediaFadeFrames     atomic.Int64
	mediaCacheDir       string

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

type preparedMedia struct {
	request    MediaRequest
	asset      *mediaAsset
	startFrame int64
	committed  bool
}

type mediaCorrection struct {
	applyAtOutputFrame uint64
	deltaFrames        int64
}

type musicIdleWaiter interface {
	WaitMusicIdle(context.Context) error
}

type musicPlaybackTelemetry interface {
	MusicPlaybackStatus() speaker.MusicPlaybackStatus
}

type timedDucker interface {
	SetDuckRamp(db float64, duration time.Duration)
}

func setSpeakerDuck(spk speaker.Speaker, db float64, duration time.Duration) {
	if timed, ok := spk.(timedDucker); ok {
		timed.SetDuckRamp(db, duration)
		return
	}
	spk.SetDuck(db)
}

// voiceIdleWaiter is implemented by the hardware speaker. Keeping it
// optional lets the player remain usable with simple test speakers while the
// Echo can wait for its deep playback queue and ALSA buffer to become silent.
type voiceIdleWaiter interface {
	WaitVoiceIdle(context.Context) error
}

func NewLocalPlayer(spk speaker.Speaker) *LocalPlayer {
	p := &LocalPlayer{
		Speaker:       spk,
		HTTP:          &http.Client{Timeout: 90 * time.Second},
		wakeCacheDir:  defaultWakeCache,
		mediaCacheDir: "/data/local/share/tater/media-cache",
		wakeID:        "no_sound",
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
		attack := time.Duration(clamp(intValue(req.Ducking["attack_ms"], 150), 0, 10000)) * time.Millisecond
		release := time.Duration(clamp(intValue(req.Ducking["release_ms"], 350), 0, 10000)) * time.Millisecond
		setSpeakerDuck(p.Speaker, duckDB, attack)
		defer setSpeakerDuck(p.Speaker, 0, release)
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

// PlayEmbeddedSound plays one bundled wake-sound asset synchronously. The
// physical setup-reset gesture uses this for the same audible confirmation as
// the ESP satellites, and its caller supplies a short timeout so a damaged
// asset can never prevent recovery.
func (p *LocalPlayer) PlayEmbeddedSound(ctx context.Context, id string) error {
	if p.Speaker == nil {
		return errors.New("speaker unavailable")
	}
	pcm, _, err := p.loadWakeSound(ctx, "embedded:"+strings.TrimSpace(id))
	if err != nil {
		return err
	}
	p.voiceMu.Lock()
	defer p.voiceMu.Unlock()
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
	setSpeakerDuck(p.Speaker, 0, 0)
	asset, err := p.startMediaAsset(ctx, req.URL, req.Channel)
	if err != nil {
		return err
	}
	defer asset.Close()
	startFrame := int64(maxInt(req.StartPositionMS, 0)) * playbackRate / 1000
	if _, err := asset.waitFrames(ctx, startFrame+mediaPrebufferFrames); err != nil {
		return err
	}
	defer p.Speaker.EndMusicStream()
	cursor := startFrame
	for {
		samples, ended, _, err := asset.readFrames(ctx, &cursor, playbackPeriod/2, req.Loop)
		if err != nil {
			p.Speaker.FlushMusic()
			return err
		}
		if len(samples) > 0 {
			chunk := resampleMediaBlock(samples, len(samples))
			if volume := int(p.volume.Load()); volume != 100 {
				chunk = scalePCM(chunk, volume)
			}
			if err := p.Speaker.PumpMusic(chunk); err != nil {
				p.Speaker.FlushMusic()
				return err
			}
		}
		if ended && !req.Loop {
			return nil
		}
	}
}

// PrepareMedia downloads and decodes a synchronized session without feeding
// the speaker. Tater commits every member only after all of them report ready,
// so network and decoder timing cannot skew the audible start.
func (p *LocalPlayer) PrepareMedia(ctx context.Context, req MediaRequest) (MediaPreparation, error) {
	if p.Speaker == nil {
		return MediaPreparation{}, errors.New("speaker unavailable")
	}
	asset, err := p.startMediaAsset(ctx, req.URL, req.Channel)
	if err != nil {
		return MediaPreparation{}, err
	}
	startFrame := int64(maxInt(req.StartPositionMS, 0)) * playbackRate / 1000
	frames, err := asset.waitFrames(ctx, startFrame+mediaPrebufferFrames)
	if err != nil {
		asset.Close()
		return MediaPreparation{}, err
	}
	if startFrame > frames {
		startFrame = frames
	}
	p.mediaMu.Lock()
	old := p.prepared
	p.prepared = &preparedMedia{request: req, asset: asset, startFrame: startFrame}
	p.mediaAdjust.Store(0)
	p.mediaLastCorrection.Store(0)
	p.mediaCorrections = nil
	p.mediaSlew.replace(0, 0)
	p.mediaRebuffering.Store(false)
	p.mediaRejoinCount.Store(0)
	p.mediaRejoinFrames.Store(0)
	p.mediaFadeFrames.Store(0)
	p.volume.Store(int32(clamp(req.VolumePercent, 0, 100)))
	p.paused.Store(false)
	p.mediaMu.Unlock()
	if old != nil && old.asset != nil {
		old.asset.Close()
	}
	preparation := MediaPreparation{
		BufferedFrames: maxInt(0, int(frames-startFrame)),
		SampleRateHz:   playbackRate,
	}
	if telemetry, ok := p.Speaker.(musicPlaybackTelemetry); ok {
		preparation.OutputLatencyFrames = telemetry.MusicPlaybackStatus().OutputLatencyFrames
	}
	return preparation, nil
}

// CommitMedia schedules an already prepared session on the same monotonic
// clock exposed by audio.clock.sync. The returned channel completes only once
// the speaker plane has drained or playback has been cancelled.
func (p *LocalPlayer) CommitMedia(
	ctx context.Context,
	sessionID string,
	startAtUS int64,
	report func(MediaPlaybackEvent),
) (<-chan error, error) {
	p.mediaMu.Lock()
	prepared := p.prepared
	if prepared == nil || prepared.request.SessionID != sessionID {
		p.mediaMu.Unlock()
		return nil, errors.New("prepared media session not found")
	}
	if prepared.committed {
		p.mediaMu.Unlock()
		return nil, errors.New("prepared media session was already committed")
	}
	prepared.committed = true
	req := prepared.request
	asset := prepared.asset
	startFrame := prepared.startFrame
	p.mediaMu.Unlock()
	done := make(chan error, 1)
	go func() {
		done <- p.playPreparedMedia(ctx, req, asset, startFrame, startAtUS, report)
		close(done)
	}()
	return done, nil
}

func (p *LocalPlayer) playPreparedMedia(
	ctx context.Context,
	req MediaRequest,
	asset *mediaAsset,
	startFrame int64,
	startAtUS int64,
	report func(MediaPlaybackEvent),
) error {
	defer asset.Close()
	defer func() {
		p.mediaMu.Lock()
		if p.prepared != nil && p.prepared.request.SessionID == req.SessionID {
			p.prepared = nil
			p.mediaAdjust.Store(0)
			p.mediaLastCorrection.Store(0)
			p.mediaCorrections = nil
		}
		p.mediaMu.Unlock()
	}()
	if delayUS := startAtUS - monotonicMicros(); delayUS > 0 {
		timer := time.NewTimer(time.Duration(delayUS) * time.Microsecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	actualStartUS := monotonicMicros()
	setSpeakerDuck(p.Speaker, 0, 0)
	channel := normalizedMediaChannel(req.Channel)
	telemetry, hasRenderClock := p.Speaker.(musicPlaybackTelemetry)
	if report != nil && !hasRenderClock {
		report(MediaPlaybackEvent{
			Kind: "started", SessionID: req.SessionID, GroupID: req.GroupID,
			Channel: channel, SampleRateHz: playbackRate,
			ScheduledStartUS: startAtUS, ActualStartUS: actualStartUS,
		})
	}
	streamEnded := false
	defer func() {
		if !streamEnded {
			p.Speaker.EndMusicStream()
		}
	}()
	baseFrames := startFrame
	if hasRenderClock && report != nil {
		monitorCtx, cancelMonitor := context.WithCancel(ctx)
		monitorDone := make(chan struct{})
		go func() {
			defer close(monitorDone)
			p.monitorPreparedMedia(monitorCtx, telemetry, req, startAtUS, baseFrames, report)
		}()
		defer func() {
			cancelMonitor()
			<-monitorDone
		}()
	}
	sourceFrames := baseFrames
	outputFrames := int64(0)
	lastReport := actualStartUS
	lastCorrection := 0
	cursor := startFrame
	fadeFramesRemaining := 0
	const rejoinFadeFrames = playbackRate / 20
	for {
		select {
		case <-ctx.Done():
			p.Speaker.FlushMusic()
			return ctx.Err()
		default:
		}
		if p.paused.Load() {
			select {
			case <-ctx.Done():
				p.Speaker.FlushMusic()
				return ctx.Err()
			case <-time.After(20 * time.Millisecond):
				continue
			}
		}

		jump := p.mediaAdjust.Swap(0)
		if jump > 0 {
			cursor += jump
			sourceFrames += jump
			lastCorrection += int(jump)
			p.recordMediaCorrection(jump)
		}
		p.mediaMu.Lock()
		slewStep := p.mediaSlew.next(playbackPeriod / 2)
		p.mediaMu.Unlock()
		inputFrames := int64(playbackPeriod/2) + slewStep
		if inputFrames < 1 {
			inputFrames = 1
		}
		samples, ended, waited, err := asset.readFrames(ctx, &cursor, int(inputFrames), req.Loop)
		if err != nil {
			return err
		}
		if len(samples) == 0 {
			if ended && !req.Loop {
				break
			}
			continue
		}
		if waited {
			if telemetry, ok := p.Speaker.(musicPlaybackTelemetry); ok {
				if telemetry.MusicPlaybackStatus().BufferedFrames < 6*(playbackPeriod/2) {
					p.mediaRebuffering.Store(true)
				}
			}
		}
		outputCount := playbackPeriod / 2
		if ended && !req.Loop && len(samples) < int(inputFrames) {
			outputCount = maxInt(1, int(math.Round(float64(len(samples))*float64(playbackPeriod/2)/float64(inputFrames))))
		}
		chunk := resampleMediaBlock(samples, outputCount)
		appliedCorrection := int64(len(samples) - outputCount)
		if appliedCorrection != 0 {
			lastCorrection += int(appliedCorrection)
			p.recordMediaCorrection(appliedCorrection)
		}
		if requested := int(p.mediaFadeFrames.Swap(0)); requested > fadeFramesRemaining {
			fadeFramesRemaining = requested
		}
		if fadeFramesRemaining > 0 {
			applyMonoFadeIn(chunk, &fadeFramesRemaining, rejoinFadeFrames)
		}
		if volume := int(p.volume.Load()); volume != 100 {
			chunk = scalePCM(chunk, volume)
		}
		if err := p.Speaker.PumpMusic(chunk); err != nil {
			return err
		}
		sourceFrames += int64(len(samples))
		outputFrames += int64(outputCount)
		nowUS := monotonicMicros()
		if report != nil && !hasRenderClock && nowUS-lastReport >= int64(time.Second/time.Microsecond) {
			report(MediaPlaybackEvent{
				Kind: "playhead", SessionID: req.SessionID, GroupID: req.GroupID,
				Channel: channel, SampleRateHz: playbackRate,
				ScheduledStartUS: startAtUS, ActualStartUS: actualStartUS,
				SourceFrames: sourceFrames, RenderedFrames: sourceFrames,
				OutputFrames:     outputFrames,
				BufferedFrames:   assetBufferedFrames(asset, cursor),
				CorrectionFrames: lastCorrection, SatelliteTimeUS: nowUS,
			})
			lastReport = nowUS
			lastCorrection = 0
		}
	}
	p.Speaker.EndMusicStream()
	streamEnded = true
	if waiter, ok := p.Speaker.(musicIdleWaiter); ok {
		if err := waiter.WaitMusicIdle(ctx); err != nil {
			p.Speaker.FlushMusic()
			return err
		}
	}
	return nil
}

func (p *LocalPlayer) recordMediaCorrection(deltaFrames int64) {
	if deltaFrames == 0 {
		return
	}
	p.mediaLastCorrection.Store(deltaFrames)
	telemetry, ok := p.Speaker.(musicPlaybackTelemetry)
	if !ok {
		return
	}
	status := telemetry.MusicPlaybackStatus()
	applyAt := status.RenderedFrames + uint64(maxInt(status.BufferedFrames, 0))
	p.mediaMu.Lock()
	p.mediaCorrections = append(p.mediaCorrections, mediaCorrection{
		applyAtOutputFrame: applyAt,
		deltaFrames:        deltaFrames,
	})
	p.mediaMu.Unlock()
}

func (p *LocalPlayer) renderedTimelineFrames(baseFrames int64, outputFrames uint64) int64 {
	timeline := baseFrames + int64(outputFrames)
	p.mediaMu.Lock()
	for _, correction := range p.mediaCorrections {
		if correction.applyAtOutputFrame <= outputFrames {
			timeline += correction.deltaFrames
		}
	}
	p.mediaMu.Unlock()
	if timeline < 0 {
		return 0
	}
	return timeline
}

func (p *LocalPlayer) monitorPreparedMedia(
	ctx context.Context,
	telemetry musicPlaybackTelemetry,
	req MediaRequest,
	startAtUS int64,
	baseFrames int64,
	report func(MediaPlaybackEvent),
) {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	started := false
	actualStartUS := int64(0)
	lastReportUS := int64(0)
	lastUnderruns := uint64(0)
	lastRendered := uint64(0)
	channel := normalizedMediaChannel(req.Channel)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		status := telemetry.MusicPlaybackStatus()
		nowUS := monotonicMicros()
		if status.UnderrunEvents > lastUnderruns {
			p.mediaRebuffering.Store(true)
			lastUnderruns = status.UnderrunEvents
		}
		if p.mediaRebuffering.Load() && status.BufferedFrames >= 6*(playbackPeriod/2) && status.RenderedFrames > lastRendered {
			p.mediaRebuffering.Store(false)
			p.mediaRejoinCount.Add(1)
			p.mediaRejoinFrames.Add(uint64(status.BufferedFrames))
			p.mediaFadeFrames.Store(playbackRate / 20)
		}
		lastRendered = status.RenderedFrames
		if !started {
			if status.FirstRenderedUnixNano <= 0 {
				continue
			}
			ageUS := (time.Now().UnixNano() - status.FirstRenderedUnixNano) / int64(time.Microsecond)
			if ageUS < 0 {
				ageUS = 0
			}
			actualStartUS = nowUS - ageUS
			report(MediaPlaybackEvent{
				Kind: "started", SessionID: req.SessionID, GroupID: req.GroupID,
				Channel: channel, SampleRateHz: playbackRate,
				ScheduledStartUS: startAtUS, ActualStartUS: actualStartUS,
				OutputLatencyFrames: status.OutputLatencyFrames,
			})
			started = true
			lastReportUS = actualStartUS
		}
		if nowUS-lastReportUS < int64(time.Second/time.Microsecond) {
			continue
		}
		timelineFrames := p.renderedTimelineFrames(baseFrames, status.RenderedFrames)
		report(MediaPlaybackEvent{
			Kind: "playhead", SessionID: req.SessionID, GroupID: req.GroupID,
			Channel: channel, SampleRateHz: playbackRate,
			ScheduledStartUS: startAtUS, ActualStartUS: actualStartUS,
			SourceFrames: timelineFrames, RenderedFrames: timelineFrames,
			OutputFrames:             int64(status.RenderedFrames),
			BufferedFrames:           status.BufferedFrames,
			OutputLatencyFrames:      status.OutputLatencyFrames,
			CorrectionFrames:         int(p.mediaLastCorrection.Swap(0)),
			UnderrunEvents:           int(status.UnderrunEvents),
			BackgroundUnderrunEvents: int(status.UnderrunEvents),
			Rebuffering:              p.mediaRebuffering.Load(),
			RejoinCount:              int(p.mediaRejoinCount.Load()),
			RejoinFrames:             int64(p.mediaRejoinFrames.Load()),
			SatelliteTimeUS:          nowUS,
		})
		lastReportUS = nowUS
	}
}

func (p *LocalPlayer) AdjustMedia(sessionID string, correctionFrames int, mode string, settle time.Duration) error {
	p.mediaMu.Lock()
	prepared := p.prepared
	if prepared == nil || prepared.request.SessionID != sessionID {
		p.mediaMu.Unlock()
		return errors.New("media session not found")
	}
	correction := int64(clamp(correctionFrames, -480000, 480000))
	normalizedMode := strings.ToLower(strings.TrimSpace(mode))
	if normalizedMode == "jump" || normalizedMode == "legacy" {
		// Rejoin jumps are intentionally forward-only. Rewinding a live or
		// looping stream could replay speech/music and make group time worse.
		if correction > 0 {
			p.mediaAdjust.Add(correction)
		} else if normalizedMode == "legacy" && correction < 0 {
			p.mediaSlew.replace(correction, 1)
		}
		p.mediaMu.Unlock()
		return nil
	}
	settleFrames := int64(settle) * playbackRate / int64(time.Second)
	p.mediaSlew.replace(correction, settleFrames)
	p.mediaMu.Unlock()
	return nil
}

func (p *LocalPlayer) StopMedia() {
	p.mediaMu.Lock()
	prepared := p.prepared
	p.prepared = nil
	p.mediaAdjust.Store(0)
	p.mediaLastCorrection.Store(0)
	p.mediaCorrections = nil
	p.mediaSlew.replace(0, 0)
	p.mediaRebuffering.Store(false)
	p.mediaFadeFrames.Store(0)
	p.mediaMu.Unlock()
	p.Speaker.FlushMusic()
	setSpeakerDuck(p.Speaker, 0, 0)
	if prepared != nil && prepared.asset != nil && !prepared.committed {
		prepared.asset.Close()
	}
}
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

func assetBufferedFrames(asset *mediaAsset, cursor int64) int {
	if asset == nil {
		return 0
	}
	frames, _, _, _ := asset.snapshot()
	if frames <= cursor {
		return 0
	}
	return int(frames - cursor)
}

func applyMonoFadeIn(raw []byte, remaining *int, total int) {
	if remaining == nil || *remaining <= 0 || total <= 0 {
		return
	}
	for offset := 0; offset+1 < len(raw) && *remaining > 0; offset += 2 {
		completed := total - *remaining
		gain := float64(completed) / float64(total)
		sample := int16(binary.LittleEndian.Uint16(raw[offset:]))
		binary.LittleEndian.PutUint16(raw[offset:], uint16(int16(math.Round(float64(sample)*gain))))
		*remaining = *remaining - 1
	}
}

func (p *LocalPlayer) fetchAndDecode(ctx context.Context, rawURL string) ([]byte, error) {
	return p.fetchAndDecodeChannel(ctx, rawURL, "mono")
}

func (p *LocalPlayer) fetchAndDecodeChannel(ctx context.Context, rawURL, channel string) ([]byte, error) {
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
	return decodeAudioChannel(body, contentType, rawURL, channel)
}

func decodeAudio(raw []byte, contentType, rawURL string) ([]byte, error) {
	return decodeAudioChannel(raw, contentType, rawURL, "mono")
}

func decodeAudioChannel(raw []byte, contentType, rawURL, channel string) ([]byte, error) {
	if len(raw) >= 12 && string(raw[:4]) == "RIFF" && string(raw[8:12]) == "WAVE" {
		return decodeWAVChannel(raw, channel)
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
		mono := selectS16Channel(stereo, 2, channel)
		return resampleS16(mono, decoder.SampleRate(), playbackRate), nil
	}
	return nil, fmt.Errorf("unsupported audio type %q", contentType)
}

func decodeWAV(raw []byte) ([]byte, error) {
	return decodeWAVChannel(raw, "mono")
}

func decodeWAVChannel(raw []byte, channel string) ([]byte, error) {
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
	mono := selectS16Channel(data, channels, channel)
	return resampleS16(mono, rate, playbackRate), nil
}

func downmixS16(raw []byte, channels int) []int16 {
	return selectS16Channel(raw, channels, "mono")
}

func selectS16Channel(raw []byte, channels int, channel string) []int16 {
	frameBytes := channels * 2
	frames := len(raw) / frameBytes
	out := make([]int16, frames)
	for frame := 0; frame < frames; frame++ {
		if channel == "left" || channel == "right" {
			selected := 0
			if channel == "right" && channels > 1 {
				selected = 1
			}
			offset := frame*frameBytes + selected*2
			out[frame] = int16(binary.LittleEndian.Uint16(raw[offset:]))
			continue
		}
		var sum int64
		for channel := 0; channel < channels; channel++ {
			offset := frame*frameBytes + channel*2
			sum += int64(int16(binary.LittleEndian.Uint16(raw[offset:])))
		}
		out[frame] = int16(sum / int64(channels))
	}
	return out
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
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
