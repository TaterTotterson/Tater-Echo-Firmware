package main

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/speaker"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/firewall"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/sendspin"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/server"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

const sendspinStore = "/data/local/etc/tater/sendspin.json"

const (
	sendspinPollInterval       = 100 * time.Millisecond
	sendspinScreenPollInterval = time.Second / 30
)

var taterSendspin struct {
	sync.Mutex
	client *sendspin.Client
}

func startSendspinPlayer(spk *speaker.PcmSpeaker, volume *server.Server, deviceID, name, target, version string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Tater Echo " + tailString(deviceID, 4)
	}
	player, err := sendspin.New(sendspin.Config{
		StorePath: sendspinStore,
		Name:      name,
		Instance:  "tater-echo-" + deviceID,
		Product:   "Tater Echo " + strings.TrimSpace(target),
		Version:   version,
		Screen:    isScreenTarget(target),
		Visuals:   strings.EqualFold(strings.TrimSpace(target), "biscuit") || isScreenTarget(target),
		// Tater's ESP satellites are immediately usable by Tater and Music
		// Assistant. Echoes follow the same no-extra-pairing behavior.
		Unpaired: true,
	})
	if err != nil {
		return err
	}
	player.OnVolume(func(percent int) {
		volume.SetVolume(percentToDeviceLevel(percent))
	})
	player.SetVolume(deviceLevelToPercent(volume.VolumeLevel()))
	if err := firewall.Open(sendspin.DefaultPort); err != nil {
		log.Printf("[sendspin] could not open port %d: %v", sendspin.DefaultPort, err)
	}
	if err := player.Start(); err != nil {
		firewall.Close(sendspin.DefaultPort)
		return err
	}
	spk.SetMusicSource(player)
	taterSendspin.Lock()
	taterSendspin.client = player
	taterSendspin.Unlock()
	log.Printf("[sendspin] Tater player ready as %q", name)
	return nil
}

func stopSendspinPlayer(spk *speaker.PcmSpeaker) {
	taterSendspin.Lock()
	player := taterSendspin.client
	taterSendspin.client = nil
	taterSendspin.Unlock()
	spk.SetMusicSource(nil)
	if player != nil {
		player.Stop("user_request")
	}
	firewall.Close(sendspin.DefaultPort)
}

func sendspinPlayer() *sendspin.Client {
	taterSendspin.Lock()
	defer taterSendspin.Unlock()
	return taterSendspin.client
}

func sendspinVolumeChanged(level int) {
	if player := sendspinPlayer(); player != nil {
		player.SetVolume(deviceLevelToPercent(level))
	}
}

func sendspinSetOutputChannel(mode string) error {
	player := sendspinPlayer()
	if player == nil {
		return fmt.Errorf("Sendspin player is unavailable")
	}
	if !player.SetOutputChannel(mode) {
		return fmt.Errorf("unsupported Sendspin output channel %q", mode)
	}
	return nil
}

func sendspinOutputChannel() string {
	if player := sendspinPlayer(); player != nil {
		return player.OutputChannel()
	}
	return "stereo"
}

func sendspinStatus() any {
	if player := sendspinPlayer(); player != nil {
		return player.Status()
	}
	return nil
}

func runSendspinPoll(spk *speaker.PcmSpeaker, ring *server.Server, screen *show.Server, target string, state func() string, timerRinging func() bool) {
	interval := sendspinPollInterval
	if screen != nil {
		// Screen targets render at 30 FPS. Drain timestamped Sendspin visualizer
		// frames at the same cadence so the display does not hold each spectrum
		// sample for 100 ms and visibly step between them.
		interval = sendspinScreenPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var logged time.Time
	showingMusic := false
	var infoRevision, visualizerRevision, beatSequence, peakSequence uint64
	var screenActive bool
	var musicPrimary, musicAccent [3]uint8
	for range ticker.C {
		player := sendspinPlayer()
		if player == nil {
			return
		}
		external := spk.MusicPlaneBusy()
		player.SetExternal(external)
		currentState := "idle"
		if state != nil {
			currentState = strings.ToLower(strings.TrimSpace(state()))
		}
		ringing := timerRinging != nil && timerRinging()
		presentation := player.NowPlaying()
		presentationActive := !external && (player.Active() || (presentation.Active && strings.TrimSpace(presentation.Title) != ""))
		if screen != nil && (presentation.InfoRevision != infoRevision || presentationActive != screenActive) {
			infoRevision = presentation.InfoRevision
			screenActive = presentationActive
			if presentationActive {
				artwork, artworkContentType := player.NowPlayingArtwork()
				screen.SetMedia(&show.Media{
					Active: true, PlaybackState: presentation.PlaybackState, GroupName: presentation.GroupName,
					Title: firstNonEmpty(presentation.Title, "Music"), Artist: presentation.Artist,
					AlbumArtist: presentation.AlbumArtist, Album: presentation.Album,
					PrimaryColor: presentation.PrimaryColor, AccentColor: presentation.AccentColor,
					ProgressMS: presentation.ProgressMS, DurationMS: presentation.DurationMS,
					PlaybackSpeed: presentation.PlaybackSpeed, ProgressUpdatedAtUnixMS: time.Now().UnixMilli(),
					Loudness: presentation.Loudness, Peak: presentation.Peak, PeakSequence: presentation.PeakSequence,
					Spectrum: presentation.Spectrum, BeatSequence: presentation.BeatSequence,
				}, artwork, artworkContentType)
			} else {
				screen.SetMedia(nil, nil, "")
			}
		}
		if screen != nil && presentationActive && presentation.VisualizerRevision != visualizerRevision {
			visualizerRevision = presentation.VisualizerRevision
			screen.Update(func(snapshot *show.Snapshot) {
				if snapshot.Media == nil {
					return
				}
				next := *snapshot.Media
				next.Loudness = presentation.Loudness
				next.Peak = presentation.Peak
				next.PeakSequence = presentation.PeakSequence
				next.Spectrum = append([]float64(nil), presentation.Spectrum...)
				next.BeatSequence = presentation.BeatSequence
				snapshot.Media = &next
			})
		}
		transient := presentation.BeatSequence != beatSequence || presentation.PeakSequence != peakSequence
		if presentationActive && presentation.HasLoudness {
			ring.SetMusicVisualizer(presentation.Loudness, transient)
		}
		beatSequence, peakSequence = presentation.BeatSequence, presentation.PeakSequence
		showMusic := shouldShowSendspinMusicVisual(target, player.Active(), ring.LinkDown(), ringing, currentState)
		paletteChanged := presentation.PrimaryColor != musicPrimary || presentation.AccentColor != musicAccent
		if showMusic && (!showingMusic || paletteChanged) {
			if !presentation.HasLoudness {
				ring.ClearMusicVisualizer()
			}
			ring.StartAnim(nativeMusicAnimation(presentation.PrimaryColor, presentation.AccentColor))
			showingMusic = true
			musicPrimary, musicAccent = presentation.PrimaryColor, presentation.AccentColor
		} else if !showMusic && showingMusic {
			ring.ClearMusicVisualizer()
			// "playing" describes the stream that just ended; it is not a
			// useful animation to restore after Sendspin becomes inactive.
			if currentState == "playing" {
				currentState = "idle"
			}
			ring.StartAnim(nativeStateAnimation(currentState))
			showingMusic = false
		}
		if player.Active() && time.Since(logged) >= time.Minute {
			logged = time.Now()
			status := player.Status()
			log.Printf("[sendspin] playing: synced=%v syncErr=%dus buffered=%dms snaps=%d corrections=%d underruns=%d lateDrops=%d lastErr=%dus",
				status.Synced, status.SyncErrUs, status.BufferedMs,
				status.Player.Snaps, status.Player.Corrections, status.Player.Underruns,
				status.Player.LateDrops, status.Player.LastErrorUs)
			if syncDiag, ok := player.SyncDiag(); ok {
				log.Printf("[sendspin] sync: n=%d delay=%d..%dus meas=%d..%dus offset=%dus drift=%.2fppm",
					syncDiag.N, syncDiag.DelayMinUs, syncDiag.DelayMaxUs,
					syncDiag.MeasMinUs, syncDiag.MeasMaxUs, syncDiag.OffsetUs, syncDiag.DriftPpm)
			}
			outDiag := player.OutDiag()
			log.Printf("[sendspin] dac: n=%d resid=%d..%dus big=%d nudgeMax=%dus gated=%d coasted=%d resets=%d rate=%.1fppm",
				outDiag.N, outDiag.ResidMinUs, outDiag.ResidMaxUs, outDiag.BigResid,
				outDiag.NudgeMaxUs, outDiag.Gated, outDiag.Coasted, outDiag.Resets, outDiag.RatePpm)
		}
	}
}

func shouldShowSendspinMusicVisual(target string, active, linkDown, timerRinging bool, state string) bool {
	if !strings.EqualFold(strings.TrimSpace(target), "biscuit") || !active || linkDown || timerRinging {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "", "idle", "playing":
		return true
	default:
		return false
	}
}

func deviceLevelToPercent(level int) int {
	return sendspin.LevelToPercent(level, 127)
}

func percentToDeviceLevel(percent int) int {
	return sendspin.PercentToLevel(percent, 127)
}

func tailString(value string, count int) string {
	if len(value) <= count {
		return value
	}
	return value[len(value)-count:]
}
