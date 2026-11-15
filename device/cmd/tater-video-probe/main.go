//go:build linux && server

// tater-video-probe is an operator-present Checkers integration diagnostic.
// It owns the real speaker and loopback Show protocol long enough for an
// separately launched tater-show binary to prove hardware video/audio sync.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/speaker"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/videostream"
)

const probeAcknowledgement = "checkers-video-audio-hardware"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var rawURL, acknowledgement string
	var width, height, fps int
	var timeout time.Duration
	flag.StringVar(&rawURL, "url", "", "HTTP(S) media URL")
	flag.IntVar(&width, "width", 960, "display width")
	flag.IntVar(&height, "height", 480, "display height")
	flag.IntVar(&fps, "fps", 30, "frame rate (24 or 30)")
	flag.DurationVar(&timeout, "timeout", 2*time.Minute, "overall timeout")
	flag.StringVar(&acknowledgement, "acknowledge", "", "required hardware acknowledgement")
	flag.Parse()

	if acknowledgement != probeAcknowledgement || width != 960 || height != 480 ||
		(fps != 24 && fps != 30) || timeout < 5*time.Second || timeout > 5*time.Minute {
		return errors.New("refusing: invalid bounds or missing --acknowledge " + probeAcknowledgement)
	}
	if _, err := videostream.CheckURL(rawURL); err != nil {
		return fmt.Errorf("refusing: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	completed := make(chan show.Command, 1)
	screen := show.New(show.DefaultAddress, show.Snapshot{
		Phase: "idle", DeviceName: "Checkers hardware probe",
		Video: &show.Video{
			Active: true, SessionID: "hardware-probe", URL: rawURL,
			Title: "Tater hardware validation", State: "playing", FPS: fps,
		},
	}, func(command show.Command) {
		if command.SessionID != "hardware-probe" {
			return
		}
		switch command.Action {
		case "video.ended", "video.error":
			select {
			case completed <- command:
			default:
			}
		}
	})

	pcm, err := speaker.NewPcmSpeakerForTarget("checkers", nil, nil)
	if err != nil {
		return fmt.Errorf("create speaker: %w", err)
	}
	defer pcm.Close()

	var audioMu sync.Mutex
	audioSession, audioStream := "", ""
	var audioBase, audioFinal uint64
	screen.SetVideoAudioHandler(func(audioCtx context.Context, sessionID, streamID string, input io.Reader) error {
		const periodBytes = 4096
		audioMu.Lock()
		pcm.FlushMusic()
		pcm.EndMusicStream()
		pcm.SetMusicLive(true)
		audioSession, audioStream = sessionID, streamID
		audioBase = pcm.MusicFramesPlayed()
		audioFinal = 0
		audioMu.Unlock()
		defer func() {
			audioMu.Lock()
			ownsStream := audioSession == sessionID && audioStream == streamID
			canceled := audioCtx.Err() != nil
			if ownsStream {
				played := pcm.MusicFramesPlayed()
				if played >= audioBase {
					audioFinal = played - audioBase
				}
				if canceled {
					pcm.FlushMusic()
				}
				pcm.EndMusicStream()
				pcm.SetMusicLive(false)
			}
			audioMu.Unlock()

			if ownsStream && !canceled {
				drainContext, stopDrain := context.WithTimeout(audioCtx, 8*time.Second)
				_ = pcm.WaitMusicStreamIdle(drainContext)
				stopDrain()
			}

			audioMu.Lock()
			if audioSession == sessionID && audioStream == streamID {
				played := pcm.MusicFramesPlayed()
				if played >= audioBase {
					audioFinal = played - audioBase
				}
				audioSession, audioStream, audioBase = "", "", 0
			}
			audioMu.Unlock()
		}()

		period := make([]byte, periodBytes)
		for {
			n, readErr := io.ReadFull(input, period)
			audioMu.Lock()
			if audioSession != sessionID || audioStream != streamID {
				audioMu.Unlock()
				return context.Canceled
			}
			if n > 0 && audioCtx.Err() == nil {
				clear(period[n:])
				if pumpErr := pcm.PumpMusic(period); pumpErr != nil {
					audioMu.Unlock()
					return pumpErr
				}
			}
			audioMu.Unlock()
			if readErr != nil {
				if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
					return nil
				}
				return readErr
			}
		}
	})
	screen.SetVideoAudioClockHandler(func(sessionID, streamID string) (uint64, bool) {
		audioMu.Lock()
		defer audioMu.Unlock()
		if audioSession != sessionID || audioStream != streamID {
			return 0, false
		}
		played := pcm.MusicFramesPlayed()
		if played < audioBase {
			return 0, false
		}
		return played - audioBase, true
	})

	serverDone := make(chan error, 1)
	go func() { serverDone <- screen.Run(ctx) }()
	select {
	case command := <-completed:
		cancel()
		if command.Action == "video.error" {
			return fmt.Errorf("renderer failed: %s", strings.TrimSpace(command.Error))
		}
		audioMu.Lock()
		playedFrames := audioFinal
		if audioSession != "" {
			playedFrames = pcm.MusicFramesPlayed() - audioBase
		}
		audioMu.Unlock()
		fmt.Printf("video hardware probe complete: played_frames=%d\n", playedFrames)
		return nil
	case err := <-serverDone:
		if err != nil {
			return fmt.Errorf("show server: %w", err)
		}
		return errors.New("show server stopped before renderer completion")
	case <-ctx.Done():
		return fmt.Errorf("video hardware probe: %w", ctx.Err())
	}
}
