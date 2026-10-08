package taternative

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/pkg/speaker"
)

// PlayOverlay prepares a bounded foreground spool, starts it on Tater's
// shared monotonic clock, and keeps the persistent music plane alive beneath
// it. The hardware mixer applies ducking to music only.
func (p *LocalPlayer) PlayOverlay(ctx context.Context, req OverlayRequest, started func()) error {
	if p.Speaker == nil {
		return errors.New("speaker unavailable")
	}
	asset, err := p.startMediaAsset(ctx, req.URL, "mono")
	if err != nil {
		return err
	}
	defer asset.Close()
	if _, err := asset.waitFrames(ctx, mediaPrebufferFrames); err != nil {
		return err
	}
	if delayUS := req.StartAtUS - monotonicMicros(); delayUS > 0 {
		timer := time.NewTimer(time.Duration(delayUS) * time.Microsecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}

	p.voiceMu.Lock()
	defer p.voiceMu.Unlock()
	target := clamp(req.DuckingTargetPercent, 0, 100)
	duckDB := -96.0
	if target > 0 {
		duckDB = 20 * math.Log10(float64(target)/100)
	}
	setSpeakerDuck(p.Speaker, duckDB, req.DuckingAttack)
	resetDuck := true
	defer func() {
		if resetDuck {
			setSpeakerDuck(p.Speaker, 0, 0)
		}
	}()
	if started != nil {
		started()
	}

	cursor := int64(0)
	for {
		samples, ended, _, err := asset.readFrames(ctx, &cursor, playbackPeriod/2, false)
		if err != nil {
			p.Speaker.Flush()
			p.Speaker.EndStream()
			return err
		}
		if len(samples) > 0 {
			chunk := resampleMediaBlock(samples, len(samples))
			if req.VolumePercent != 100 {
				chunk = scalePCM(chunk, req.VolumePercent)
			}
			if err := p.Speaker.PumpPeriod(chunk); err != nil {
				p.Speaker.Flush()
				p.Speaker.EndStream()
				return err
			}
		}
		if ended {
			break
		}
	}
	p.Speaker.EndStream()
	if waiter, ok := p.Speaker.(voiceIdleWaiter); ok {
		if err := waiter.WaitVoiceIdle(ctx); err != nil {
			p.Speaker.Flush()
			return err
		}
	}
	if req.StopMediaWhenFinished && req.BackgroundFadeOut > 0 {
		setSpeakerDuck(p.Speaker, -96, req.BackgroundFadeOut)
		if err := waitSpeakerDuckRamp(ctx, p.Speaker, req.BackgroundFadeOut); err != nil {
			return err
		}
		// Tater stops the background when it receives overlay.finished. Keep
		// the music gain at silence until that stop arrives; StopMedia resets
		// it for the next session.
		resetDuck = false
		return nil
	}
	setSpeakerDuck(p.Speaker, 0, req.DuckingRelease)
	if err := waitPlaybackDuration(ctx, req.DuckingRelease); err != nil {
		return err
	}
	resetDuck = false
	return nil
}

// duckRampWaiter lets the hardware renderer report the completion of its
// sample-counted gain envelope. A wall-clock sleep can expire before the ALSA
// loop observes a new ramp, clipping the last period of a fade.
type duckRampWaiter interface {
	WaitDuckRamp(context.Context) error
}

func waitSpeakerDuckRamp(ctx context.Context, spk speaker.Speaker, duration time.Duration) error {
	if waiter, ok := spk.(duckRampWaiter); ok {
		return waiter.WaitDuckRamp(ctx)
	}
	return waitPlaybackDuration(ctx, duration)
}

func waitPlaybackDuration(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// PlayScene provides the direct audio.scene.start compatibility path for a
// single Echo. Tater sends synchronized multi-satellite playback through
// Sendspin; the native scene path remains for a local foreground/background
// composition and uses the same hardware ducking planes.
func (p *LocalPlayer) PlayScene(ctx context.Context, req SceneRequest) error {
	if strings.TrimSpace(req.ForegroundURL) == "" {
		return errors.New("foreground URL is required")
	}
	if strings.TrimSpace(req.BackgroundURL) == "" {
		return p.PlayVoice(ctx, PlayRequest{
			URL: req.ForegroundURL, TTSKind: req.ForegroundKind,
			Ducking: map[string]any{"target_percent": req.DuckingTargetPercent},
		})
	}
	sceneCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	backgroundDone := make(chan error, 1)
	go func() {
		backgroundDone <- p.PlayMedia(sceneCtx, MediaRequest{
			URL: req.BackgroundURL, VolumePercent: req.BackgroundVolumePercent,
			Loop: req.BackgroundLoop, Channel: "mono",
		})
	}()
	err := p.PlayOverlay(sceneCtx, OverlayRequest{
		OverlayID: req.SceneID, URL: req.ForegroundURL, Kind: req.ForegroundKind,
		VolumePercent:        req.ForegroundVolumePercent,
		DuckingTargetPercent: req.DuckingTargetPercent,
		DuckingAttack:        req.DuckingAttack, DuckingRelease: req.DuckingRelease,
		StopMediaWhenFinished: true, BackgroundFadeOut: req.BackgroundFadeOut,
	}, nil)
	return p.finishSceneBackground(cancel, backgroundDone, err)
}

func (p *LocalPlayer) finishSceneBackground(
	cancel context.CancelFunc,
	backgroundDone <-chan error,
	err error,
) error {
	cancel()
	select {
	case backgroundErr := <-backgroundDone:
		if err == nil && backgroundErr != nil && !errors.Is(backgroundErr, context.Canceled) {
			err = backgroundErr
		}
	case <-time.After(time.Second):
		// Keep the gain at silence while stopping a stuck background. Resetting
		// it first can briefly expose the queued music at full volume.
		p.Speaker.FlushMusic()
	}
	setSpeakerDuck(p.Speaker, 0, 0)
	return err
}
