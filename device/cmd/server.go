package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/actionbutton"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/aec"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/als"
	internalbuttons "github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/buttons"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/jack"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/mic"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/mixer"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/speaker"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bluetooth"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/client"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/clock"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/config"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/listen"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/platform"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/server"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/taternative"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/wakeword/microwakeword"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/wifi"
	"github.com/TaterTotterson/Tater-Echo-Firmware/pkg/board"
	pkgbuttons "github.com/TaterTotterson/Tater-Echo-Firmware/pkg/buttons"
	"github.com/TaterTotterson/Tater-Echo-Firmware/pkg/led"
)

const usage = `usage: server [command]

With no command, runs the Tater Echo device daemon (normally started by
start_server.sh, which restarts it; do not run a second copy by hand).

  version         print the firmware version and build time
  platform-init   apply the board's platform settings, for emOS's boot
  setup-portal    serve the emOS first-boot captive portal on port 80
  help            this text
`

func main() {
	// Only a bare invocation runs the daemon. `server --version` used to
	// start a second instance in the foreground, fighting the supervised one
	// for the mic and the controller link, so anything unrecognised is
	// refused rather than ignored.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "platform-init":
			os.Exit(platformInit())
		case "setup-portal":
			if err := taternative.RunSetupPortal(taternative.SetupPortalOptions{}); err != nil {
				log.Fatalf("Tater setup portal: %v", err)
			}
			os.Exit(0)
		case "version", "--version", "-v":
			built := "unknown"
			if sec, err := strconv.ParseInt(clock.BuildUnix, 10, 64); err == nil {
				built = time.Unix(sec, 0).UTC().Format(time.RFC3339)
			}
			fmt.Printf("Tater Echo Firmware %s (built %s)\n", client.Version, built)
			os.Exit(0)
		case "help", "--help", "-h":
			fmt.Print(usage)
			os.Exit(0)
		default:
			fmt.Fprintf(os.Stderr, "unknown argument %q\n\n%s", os.Args[1], usage)
			os.Exit(2)
		}
	}
	log.SetOutput(os.Stdout)
	log.Printf("Tater Echo Firmware %s starting", client.Version)

	deviceID := client.GetSerialNo()
	log.Printf("Device ID: %s", deviceID)

	// A WiFi change that never got committed (crash/power cycle mid-switch)
	// is rolled back before anything tries to use the network — same
	// self-healing philosophy as the A/B binary slots.
	wifi.RecoverIfPending()

	// Amazon's WiFi Simple Setup daemon (BLE+WiFi provisioning of
	// neighbouring Amazon devices) is useless on a repurposed device and
	// was caught busy-looping at ~50% CPU / 40% sys on one unit (Office,
	// 2026-07-13 — likely retrying the Bluetooth transport the BLE proxy
	// takes over). Same stock-service takeover as `stop mixer` /
	// `stop acebutton` / `stop ledcontroller` in the hardware bindings.
	// Idempotent: a no-op on boots where init never starts it (Lounge).
	exec.Command("stop", "smarthomewifid").Run()

	// Keep a second CPU core online. procfs, so this has to be re-applied on
	// every start — see applyCoreFloor for why the mic pipeline's 160ms
	// deadline makes it worth doing.
	applyCoreFloor()

	buttonController, err := internalbuttons.NewButtonController()
	if err != nil {
		log.Fatalf("Failed to initialize Button controller: %v", err)
	}

	microphone, err := mic.NewMicrophone()
	if err != nil {
		log.Fatalf("Failed to initialize Microphone: %v", err)
	}

	// AEC canceller — far end fed by the speaker's echo tap, near end run
	// by the data client on the mono mic stream. Starts disabled; armed by
	// applyAecConfig from env defaults below and on every config push.
	canceller := aec.New()

	// The level tap drives the energy-reactive LED ring ("meter" pattern).
	// The Server doesn't exist yet when the speaker starts its pump loop,
	// so the tap goes through an atomic pointer armed just below.
	var srvPtr atomic.Pointer[server.Server]
	var showServerPtr atomic.Pointer[show.Server]
	pcmSpeaker, err := speaker.NewPcmSpeaker(canceller.WriteFar, func(rms float64) {
		if srv := srvPtr.Load(); srv != nil {
			srv.SetAudioLevel(rms)
		}
		if screen := showServerPtr.Load(); screen != nil {
			screen.Update(func(snapshot *show.Snapshot) {
				snapshot.AudioLevel = math.Min(1, rms*2)
			})
		}
	})
	if err != nil {
		log.Fatalf("Failed to initialize PCM Speaker: %v", err)
	}

	s := server.NewServer(buttonController, microphone, pcmSpeaker)
	srvPtr.Store(s)

	// Local duck at the device's own wake crossing, confirmed or released by
	// the controller's duck message (OnDuck below).
	localDuck = speaker.NewLocalDuck(pcmSpeaker.SetDuck, speaker.LocalDuckHold)

	buttonController.SetVolumeCallback(func(direction string) {
		// Inert without a controller: nothing is playing to be louder or
		// quieter, and showing the arc would acknowledge a device that
		// cannot act. The mute button stays live — see Server.SetLinkDown.
		if s.LinkDown() {
			log.Println("[cmd] volume button ignored — no controller session")
			return
		}
		if direction == "up" {
			s.VolumeStepUp()
		} else {
			s.VolumeStepDown()
		}
	})
	buttonController.SetMuteCallback(func() {
		s.MuteToggle()
	})

	ctx := context.Background()
	bootstrapPath := strings.TrimSpace(os.Getenv("TATER_NATIVE_CONFIG"))
	bootstrap, err := taternative.LoadBootstrap(bootstrapPath)
	if err != nil {
		log.Fatalf("Tater native bootstrap: %v", err)
	}
	nativeURL := bootstrap.URL
	if value := strings.TrimSpace(os.Getenv("TATER_NATIVE_URL")); value != "" {
		nativeURL = value
	}
	nativeMode := nativeURL != ""
	var nativeClient *taternative.Client
	var nativePlayer *taternative.LocalPlayer
	resetToSetup := func(source string, playSound bool) error {
		log.Printf("[tater-native] setup reset requested by %s", source)
		if nativeClient != nil {
			nativeClient.StopCapture(true)
		}
		if nativePlayer != nil {
			nativePlayer.StopVoice()
			nativePlayer.StopMedia()
			nativePlayer.SetTimerAlarm(false)
		}
		s.ShowSetupResetSuccess()
		if playSound && nativePlayer != nil {
			soundCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			if err := nativePlayer.PlayEmbeddedSound(soundCtx, "short-definite-fart"); err != nil && soundCtx.Err() == nil {
				log.Printf("[tater-native] setup reset sound failed: %v", err)
			}
			cancel()
		}
		if err := taternative.ResetToSetup(taternative.SetupResetOptions{}); err != nil {
			s.ClearSetupResetFeedback()
			s.StartAnim(nativeStateAnimation("error"))
			return err
		}
		return nil
	}
	actionGesture := actionbutton.New(actionbutton.Config{}, actionbutton.Callbacks{
		StartIntercom: func() bool {
			if nativeClient == nil || s.LinkDown() {
				log.Println("[tater-native] intercom hold ignored — no Tater session")
				return false
			}
			if s.IsMuted() {
				log.Println("[tater-native] intercom hold suppressed — muted")
				return false
			}
			s.ClearSetupResetFeedback()
			s.CancelVolumeDisplay()
			log.Println("[tater-native] action button hold: intercom voice.start")
			return nativeClient.StartIntercom()
		},
		StopIntercom: func() {
			if nativeClient != nil {
				log.Println("[tater-native] action button release: intercom voice.stop")
				nativeClient.StopCapture(false)
			}
		},
		ShowClicks:    s.ShowSetupResetClicks,
		ShowCountdown: s.ShowSetupResetCountdown,
		ClearFeedback: s.ClearSetupResetFeedback,
		SetupComplete: func() {
			if err := resetToSetup("action button gesture", true); err != nil {
				log.Printf("[tater-native] physical setup reset failed: %v", err)
			}
		},
	})
	if nativeMode {
		// Direct-native mode always needs the local detector. The incoming
		// settings frame may refine model/threshold after hello.
		enabled := true
		config.Get().Apply(config.ConfigMessage{MwwShadowEnabled: &enabled})
	}

	dataClient := client.NewDataClient(deviceID, microphone, pcmSpeaker, canceller)
	canceller.SetStatePath(aec.DefaultStatePath) // saved echo path: loaded on the hardware reference
	applyAecConfig(canceller, dataClient)        // arm from env defaults before any config push

	// Direction callback — update LED ring to show estimated source angle
	dataClient.OnDirectionChanged(func(angle float64, activity bool, speech bool) {
		s.SetDirectionObservation(angle, activity, speech)
		if screen := showServerPtr.Load(); screen != nil {
			screen.Update(func(snapshot *show.Snapshot) {
				if activity || speech {
					value := math.Mod(angle+360, 360)
					snapshot.DirectionDegrees = &value
				} else {
					snapshot.DirectionDegrees = nil
				}
			})
		}
	})
	controlClient := client.NewControlClient(
		deviceID,
		func(leds []led.Led, listening *bool) {
			// A raw frame from the controller supersedes any running
			// device-local animation — stop it so its next tick can't
			// paint over this frame.
			s.StopAnim()
			s.SetLEDs(leds, listening)
		},
		func(lockMic bool) {
			if s.IsMuted() {
				// Mute is device-sovereign — the physical button cannot be
				// overridden remotely. Refuse the controller's mic_start.
				log.Println("[cmd] mic_start from controller rejected — device is muted")
				return
			}
			// Under private listening the wake stream belongs to the device
			// (see the mic_stop callback), so a button turn's lock_mic must
			// REPLACE it rather than being refused as "already active".
			if lockMic && dataClient.ListenState() != client.ListenStream &&
				!dataClient.TurnStreamActive() {
				dataClient.StopMic()
			}
			dataClient.StartMic(lockMic)
		},
		func() {
			// Under private listening the wake stream sends nothing, and it
			// is what hears a barge-in over the reply, so the controller's
			// mic_stop ends a button or follow-up turn but never the local
			// listening. A turn's end hands straight back to it.
			//
			// Nor does it touch a SESSION: those end only by id
			// (listen_close). mic_stop carries none, so one crossing a
			// barge-in's oww_wake on the wire would close the new session
			// the controller is about to take.
			if dataClient.ListenState() != client.ListenStream {
				if dataClient.TurnStreamActive() {
					dataClient.StopMic()
					if !s.IsMuted() {
						dataClient.StartMic(false)
					}
				}
				return
			}
			dataClient.StopMic()
		},
	)

	// Private-listening sessions (docs/listening.md). The gate ignores any id
	// that is not the open session, so a late message cannot touch a new one.
	controlClient.OnListen(func(kind string, session uint32) {
		switch kind {
		case "listen_ack":
			dataClient.AckListen(session)
		case "listen_close":
			// A session the controller closes before confirming a duck is a
			// wake it did not take (ceded, or refused): un-duck the music.
			localDuck.Cancel()
			if dataClient.CloseListen(session) {
				log.Printf("[listen] session %d closed by the controller", session)
			}
		}
	})
	// A turn the device ended itself (no speech) hands back to the wake
	// stream here: nothing else will, under private listening. In stream
	// mode the controller restarts the stream itself, as it always has.
	dataClient.OnTurnEnded(func() {
		if s.IsMuted() || dataClient.ListenState() == client.ListenStream {
			return
		}
		log.Println("[data] turn ended on the device — back to the wake stream")
		dataClient.StartMic(false)
	})
	dataClient.OnListenEnd(func(e listen.End) {
		localDuck.Cancel()
		controlClient.SendListenEnd(e.Session, string(e.Reason))
	})

	// Device-rendered ring animations (led_anim) — the animation engine
	// runs on the device's own ticker, immune to controller/WiFi jitter.
	controlClient.OnLEDAnim(func(raw json.RawMessage) {
		var spec server.AnimSpec
		if err := json.Unmarshal(raw, &spec); err != nil {
			log.Printf("[cmd] bad led_anim spec: %v", err)
			return
		}
		s.StartAnim(spec)
	})

	// BLE proxy scanner — passive scan over /dev/stpbt, batches forwarded to
	// the controller on the DATA plane where the controller can read them
	// there, and on the control plane otherwise (#404). Armed from env
	// defaults here and toggled live on config push (bleProxyEnabled,
	// applyBleConfig).
	//
	// The plane is chosen per batch rather than once at registration: the
	// control connection can drop and re-register against a different
	// controller without this callback being rebuilt, and a stale choice
	// would either strand the adverts or put them back on the liveness
	// channel. It is two map reads on a path that runs a few times a second.
	nativeBLEEnabled := nativeMode && strings.EqualFold(client.FirmwareTarget, "biscuit")
	bleScanner := bluetooth.NewScanner(func(batch []bluetooth.Advert) {
		if nativeBLEEnabled {
			if nativeClient == nil {
				return
			}
			adverts := make([]taternative.BLEAdvertisement, 0, len(batch))
			for _, advert := range batch {
				adverts = append(adverts, taternative.BLEAdvertisement{
					Address: advert.Addr, AddressType: advert.AddrType,
					EventType: advert.EventType, RSSI: advert.Rssi, Data: advert.Data,
				})
			}
			nativeClient.ReportBLEAdvertisements(adverts)
			return
		}
		if controlClient.HasFeature(client.FeatureBleAdvertsData) {
			payload, err := json.Marshal(map[string]interface{}{"adverts": batch})
			if err != nil {
				log.Printf("[cmd] ble adverts: marshal failed: %v", err)
				return
			}
			// Dropped rather than falling back: see DataClient.SendBleAdverts.
			dataClient.SendBleAdverts(payload)
			return
		}
		controlClient.SendBleAdverts(batch)
	})
	if !nativeBLEEnabled {
		applyBleConfig(bleScanner)
	}

	// Button events — forward to controller via control plane
	_, err = buttonController.SubscribeToButton(func(event pkgbuttons.ButtonClickEvent) {
		log.Printf("Button event: clickType=%d down=%v", event.ClickType, event.Down)
		// Direct-native Echo firmware recognizes these gestures locally, just
		// like the ESP firmware. Setup recovery must keep working while Tater
		// or Wi-Fi is unavailable, so this deliberately runs before LinkDown.
		if nativeMode && event.ClickType == pkgbuttons.DotClick {
			if event.Down {
				actionGesture.Press()
			} else {
				actionGesture.Release()
			}
			return
		}
		// Inert without a controller session: the dot cannot start a turn
		// with nothing to send it to, and the ring flash CancelVolumeDisplay
		// produces would acknowledge a press that achieves nothing. Dropped
		// here rather than at the binding — the binding is the portable
		// hardware layer and knows nothing about sessions.
		if s.LinkDown() {
			log.Println("[cmd] action button ignored — no controller session")
			return
		}
		// Muted presses are FORWARDED, with the mute state attached, and the
		// controller decides what the gesture is allowed to do. Dropping them
		// here was right while the dot button meant only "start a voice turn";
		// it became wrong when a hold started firing an HA event, because a
		// hold bound to something unrelated to speech then stopped working
		// whenever the mic was muted.
		//
		// This does not weaken mute. The mic_start rejection above is what
		// makes mute sovereign — the controller cannot open the mic while
		// muted however it reads this event, and the ADC is muted in hardware
		// regardless.
		event.Muted = s.IsMuted()
		// A press outranks the volume arc: adjusting volume and immediately
		// pressing the button used to leave the arc holding the ring for the
		// rest of its 2s window, with nothing showing that the device had
		// started listening. Release, not press, so it lines up with the
		// event the controller actually starts a turn on.
		if event.ClickType == pkgbuttons.DotClick && !event.Down {
			s.CancelVolumeDisplay()
		}
		controlClient.SendButton(event)
	})
	if err != nil {
		log.Fatalf("Button subscription failed: %v", err)
	}

	// Disconnected — orange pulse
	//
	// pulseKind is what the ring is CURRENTLY showing, and restarting a pulse
	// that is already running is the bug it exists to prevent. OnDisconnected
	// fires once per reconnect-loop iteration, not once per disconnection —
	// so every retry used to cancel the goroutine mid-cycle and start a new
	// one from phase zero, which is mid-brightness and rising. The ring ran
	// roughly two smooth cycles and then hard-cut back to the middle, at an
	// interval that is not a multiple of the pulse period, so the jump landed
	// somewhere different each time. Reported as "like a poorly repeating
	// gif" and it is exactly that: a loop being restarted, not a loop.
	var (
		pulseCancel context.CancelFunc
		pulseKind   string
	)
	// Watch the ambient light sensor for step changes (a lamp switching on)
	// and report them immediately; the steady-state value rides the ~30s
	// stats tick. No-ops on a device without the sensor.
	//
	// Started here rather than earlier because it captures controlClient,
	// which does not exist until above — and the amd64 build cannot catch
	// that, since this package is excluded from it by build constraints
	// (mic/speaker are ARM-only). Only compile.sh compiles this file.
	go als.Watch(ctx, func(lux int) {
		controlClient.SendAmbientLight(lux)
	})

	// Headphone jack. BOTH directions need work from us, and so does the
	// state the device booted into — accdet acts only on a transition, and
	// Init leaves the internal amp on regardless of what is plugged in.
	// SetJackRouting owns the whole mapping (issue #80 for the removal half,
	// measured against a stock Dot 2026-09-03 for the rest).
	go jack.Watch(ctx, func(inserted bool) {
		pcmSpeaker.SetJackRouting(inserted)
	})
	// Android's audio HAL rewrites the codec on every mediaserver restart —
	// roughly once a minute with a plug inserted — so applying the routing on
	// the jack edge alone holds for about a minute and then the jack goes
	// quiet again. Measured 2026-09-03. Nothing can stop mediaserver (the
	// framework crash-loops without it), so the routing is reconciled instead.
	go pcmSpeaker.WatchJackRouting(ctx)

	if nativeMode {
		nativePlayer = taternative.NewLocalPlayer(pcmSpeaker)
		otaInstaller := taternative.NewOTAInstaller()
		otaInstaller.Restart = func() {
			_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		}
		tokenPath := bootstrap.TokenPath
		if value := strings.TrimSpace(os.Getenv("TATER_TOKEN_PATH")); value != "" {
			tokenPath = value
		}
		if tokenPath == "" {
			tokenPath = "/data/local/etc/tater/device_token"
		}
		deviceName := bootstrap.DeviceName
		if value := strings.TrimSpace(os.Getenv("TATER_DEVICE_NAME")); value != "" {
			deviceName = value
		}
		if deviceName == "" {
			deviceName = "Tater Echo " + deviceID
		}
		room := firstNonEmpty(strings.TrimSpace(os.Getenv("TATER_ROOM")), bootstrap.Room)
		if client.FirmwareTarget == "checkers" {
			screen := show.New(show.DefaultAddress, show.Snapshot{
				Phase: "offline", DeviceName: deviceName, Room: room,
				Message: "Connecting to Tater", Muted: s.IsMuted(),
				VolumePercent: deviceVolumePercent(s.VolumeLevel()),
			}, func(command show.Command) {
				switch command.Action {
				case "screen.ready":
					// The complete current snapshot was already sent on accept.
				case "mute.toggle":
					s.MuteToggle()
				case "volume.delta":
					if command.Value != nil && *command.Value < 0 {
						s.VolumeStepDown()
					} else if command.Value != nil && *command.Value > 0 {
						s.VolumeStepUp()
					}
				case "intercom.start":
					if nativeClient != nil && !s.IsMuted() && !s.LinkDown() {
						nativeClient.StartIntercom()
					}
				case "intercom.stop":
					if nativeClient != nil {
						nativeClient.StopCapture(false)
					}
				}
			})
			showServerPtr.Store(screen)
			go func() {
				if err := screen.Run(ctx); err != nil && ctx.Err() == nil {
					log.Printf("[show] screen service stopped: %v", err)
				}
			}()
		}
		detectedBoard := board.IDOf(board.Detect(""))
		if detectedBoard == "unknown" {
			detectedBoard = client.FirmwareTarget
		}
		var nativeErr error
		nativeClient, nativeErr = taternative.New(taternative.Config{
			URL: nativeURL, Token: firstNonEmpty(strings.TrimSpace(os.Getenv("TATER_TOKEN")), bootstrap.Token), TokenPath: tokenPath,
			DeviceID: deviceID, HardwareID: deviceID, DeviceName: deviceName,
			Board: detectedBoard, FirmwareTarget: client.FirmwareTarget, FirmwareVersion: client.Version,
			Room: room, Capabilities: taternative.CapabilitiesForTarget(client.FirmwareTarget),
		}, taternative.Hooks{
			ReplyDirection: s.SetReplyDirectionDegrees,
			Connected: func(selector string) {
				log.Printf("[tater-native] connected as %s", selector)
				if pulseCancel != nil {
					pulseCancel()
					pulseCancel = nil
				}
				pulseKind = ""
				s.SetLinkDown(false)
				if s.IsMuted() {
					s.RestoreMuteRing()
				} else {
					s.StartAnim(nativeStateAnimation("idle"))
					dataClient.StartLocalMic()
				}
				nativeClient.ReportSettings(map[string]any{
					"volume_percent": deviceVolumePercent(s.VolumeLevel()),
				})
				updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) {
					snapshot.Connected = true
					snapshot.Phase = "idle"
					snapshot.Message = "Ready when you are"
				})
			},
			Disconnected: func(err error) {
				if err != nil && err != context.Canceled {
					log.Printf("[tater-native] disconnected: %v", err)
				}
				s.StopAnim()
				s.SetLinkDown(true)
				if pulseKind != "orange" {
					if pulseCancel != nil {
						pulseCancel()
					}
					pulseCtx, cancel := context.WithCancel(ctx)
					pulseCancel, pulseKind = cancel, "orange"
					go pulseOrange(pulseCtx, s)
				}
				updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) {
					snapshot.Connected = false
					snapshot.Phase = "offline"
					snapshot.Message = "Connecting to Tater"
					snapshot.AudioLevel = 0
				})
			},
			State: func(state string, payload map[string]any) {
				dataClient.ApplyNativeBeamState(state)
				if state == "idle" || state == "error" {
					s.ClearDirection()
				}
				s.StartAnim(nativeStateAnimation(state))
				updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) {
					snapshot.Phase = showPhase(state)
					snapshot.Message = showMessage(state, payload)
					if state == "idle" || state == "error" {
						snapshot.DirectionDegrees = nil
						snapshot.AudioLevel = 0
						snapshot.Media = nil
					}
				})
			},
			Settings: func(values map[string]any) (map[string]any, error) {
				applied, err := applyTaterSettings(values, s, canceller, dataClient, nativePlayer)
				applyMWWConfig(dataClient, nil,
					func(wakeWord string, score float32, _ time.Time) bool {
						return nativeClient != nil && nativeClient.Wake(wakeWord, score)
					},
					func(wakeWord string, score float32, _ time.Time) {
						if nativeClient != nil {
							nativeClient.CloseMiss(wakeWord, score)
						}
					})
				return applied, err
			},
			Status: func() map[string]any {
				wakeEngine := mwwNativeStatus()
				for key, value := range nativePlayer.WakeSoundStatus() {
					wakeEngine[key] = value
				}
				if nativeClient != nil {
					wakeEngine["capture"] = nativeClient.TrainerStatus()
					wakeEngine["verifier"] = nativeClient.WakeVerifierStatus()
				}
				var memory runtime.MemStats
				runtime.ReadMemStats(&memory)
				status := map[string]any{
					"volume_percent": deviceVolumePercent(s.VolumeLevel()),
					"muted":          s.IsMuted(),
					"wake_engine":    wakeEngine,
					"ble":            bleScanner.Stats(),
					"memory": map[string]any{
						"heap_alloc_kb": memory.HeapAlloc / 1024,
						"heap_sys_kb":   memory.HeapSys / 1024,
						"stack_sys_kb":  memory.StackSys / 1024,
						"rss_kb":        selfRSSKb(),
						"goroutines":    runtime.NumGoroutine(),
						"num_gc":        memory.NumGC,
					},
				}
				if angle, ok := s.Direction(); ok {
					status["doa_deg"] = math.Round(angle*10) / 10
				}
				return status
			},
			PlayWakeSound: nativePlayer.PlayWakeSound,
			PlayVoice:     nativePlayer.PlayVoice, StopVoice: nativePlayer.StopVoice,
			PlayOverlay: nativePlayer.PlayOverlay,
			PlayScene:   nativePlayer.PlayScene,
			StartMedia: func(playCtx context.Context, request taternative.MediaRequest) error {
				updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) {
					snapshot.Media = &show.Media{Title: request.Title, Artist: request.Artist, Album: request.Album}
					snapshot.Phase = "music"
				})
				err := nativePlayer.PlayMedia(playCtx, request)
				updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) { snapshot.Media = nil })
				return err
			},
			PrepareMedia: func(playCtx context.Context, request taternative.MediaRequest) (taternative.MediaPreparation, error) {
				prepared, err := nativePlayer.PrepareMedia(playCtx, request)
				if err == nil {
					updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) {
						snapshot.Media = &show.Media{Title: request.Title, Artist: request.Artist, Album: request.Album}
					})
				}
				return prepared, err
			},
			CommitMedia: nativePlayer.CommitMedia,
			AdjustMedia: nativePlayer.AdjustMedia,
			StopMedia: func(string) {
				nativePlayer.StopMedia()
				updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) { snapshot.Media = nil })
			},
			PauseMedia:  func(string) { nativePlayer.PauseMedia() },
			ResumeMedia: func(string) { nativePlayer.ResumeMedia() },
			VolumeMedia: func(_ string, percent int) { nativePlayer.SetMediaVolume(percent) },
			TimerAlarm: func(active bool, _ taternative.Timer) {
				nativePlayer.SetTimerAlarm(active)
				updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) { snapshot.TimerActive = active })
				if active {
					s.StartAnim(nativeTimerAnimation())
				} else {
					state := "idle"
					if nativeClient != nil {
						state = nativeClient.State()
					}
					s.StartAnim(nativeStateAnimation(state))
				}
			},
			SetupReset: func() error { return resetToSetup("Tater command", false) },
			OTA:        otaInstaller.Install,
		})
		if nativeErr != nil {
			log.Fatalf("Tater native configuration invalid: %v", nativeErr)
		}
		if nativeBLEEnabled {
			// Start only after nativeClient is fully constructed so the first
			// batch cannot race initialization or disappear into a nil client.
			bleScanner.SetEnabled(true)
		}
		dataClient.OnPCM(nativeClient.PushAudio)
		applyMWWConfig(dataClient, nil,
			func(wakeWord string, score float32, _ time.Time) bool {
				return nativeClient.Wake(wakeWord, score)
			},
			func(wakeWord string, score float32, _ time.Time) {
				nativeClient.CloseMiss(wakeWord, score)
			})
		s.SetLinkDown(true)
		pulseCtx, cancel := context.WithCancel(ctx)
		pulseCancel, pulseKind = cancel, "orange"
		go pulseOrange(pulseCtx, s)
	}

	controlClient.OnDisconnected(func() {
		// Stop any device-local animation: the controller that owned it is
		// gone, and the pulse below would otherwise fight its ticker. Safe to
		// repeat — StopAnim only bumps the animator generation, and the pulse
		// paints through SetLEDs rather than the animator, so it is not what
		// this cancels.
		s.StopAnim()
		if pulseKind == "orange" {
			return // already pulsing; restarting is what breaks the cycle
		}
		if pulseCancel != nil {
			pulseCancel()
		}
		pulseCtx, cancel := context.WithCancel(ctx)
		pulseCancel = cancel
		pulseKind = "orange"
		// Ring belongs to the link state from here, and the action/volume
		// buttons go inert. Set BEFORE the pulse starts, or its first frames
		// are swallowed by the mute suppression on a muted device.
		s.SetLinkDown(true)
		go pulseOrange(pulseCtx, s)
	})

	// Pending approval — slow white pulse
	controlClient.OnPending(func() {
		if pulseKind == "white" {
			return
		}
		if pulseCancel != nil {
			pulseCancel()
		}
		pulseCtx, cancel := context.WithCancel(ctx)
		pulseCancel = cancel
		pulseKind = "white"
		// Pending approval is the same condition one step earlier: there is
		// nothing above this device, so the white pulse owns the ring and the
		// buttons do nothing.
		s.SetLinkDown(true)
		go pulseWhite(pulseCtx, s)
	})

	// Connected — stop pulse, report current mute state, restore ring or hand
	// back to direction arc depending on mute state.
	controlClient.OnConnected(func() {
		if pulseCancel != nil {
			pulseCancel()
			pulseCancel = nil
		}
		pulseKind = ""
		// Who runs the output chain is decided by THIS controller's ack, and
		// settled before any of its audio can arrive.
		pcmSpeaker.SetOutputChainActive(controlClient.HasFeature(client.FeatureOutputChain))
		// Session restored: the ring goes back to the controller, the mute
		// ring reasserts below if it applies, and the buttons work again.
		s.SetLinkDown(false)
		// Always report mute state on (re)connect — the controller may have
		// restarted and lost its record of our state. Volume is only
		// reported once the device holds an authoritative level (seeded
		// from config or set locally): the controller persists every
		// volume_state into startupVolume, so reporting the boot-default
		// level here is what used to clobber the saved volume on reboot.
		// On a fresh boot the config push seeds the volume, and Set()'s
		// change callback sends the report instead.
		muted := s.IsMuted()
		controlClient.SendMuteState(muted)
		if s.VolumeSeeded() {
			controlClient.SendVolumeState(s.VolumeLevel())
		}
		s.StopAnim() // fresh controller session owns the ring from here
		if muted {
			// Orange pulse overwrote the red ring — restore it.
			s.RestoreMuteRing()
		} else {
			s.SetLEDs(allLEDs(0, 0, 0), nil)
			s.LEDModeDirection()
		}
		// Send an immediate stats snapshot so the dashboard populates on
		// (re)connect rather than waiting up to 30s for the first tick.
		go func() {
			st := collectStats()
			st.Ble = bleScanner.Stats()
			st.MwwShadow = mwwShadowStats(dataClient)
			st.AecRef = canceller.RefSource()
			controlClient.SendStats(st)
		}()
		// Deliver any unacknowledged WiFi change outcome (including the
		// "restarted before commit" result RecoverIfPending leaves
		// behind). Not cleared here — the controller's wifi_commit ack
		// does that (wifi.Commit), so a result lost in transit re-sends.
		if r := wifi.PendingResult(); r != nil {
			controlClient.SendWifiResult(r.OK, r.SSID, r.Error)
		}
	})

	// Config applied — apply hardware changes to the mixer, AEC params to
	// the canceller. AEC/BLE read the merged post-Apply snapshot rather than
	// the (partial) message so unmentioned fields keep their values.
	controlClient.OnConfigApplied(func(msg config.ConfigMessage) {
		applyHardwareConfig(msg)
		// The merged config, not the partial message, for the reason given
		// above. Active is re-read from the ack on every push: a reconnect
		// can land on a controller that does not hand the chain over.
		pcmSpeaker.SetOutputChain(config.Get().OutputChain())
		pcmSpeaker.SetOutputChainActive(controlClient.HasFeature(client.FeatureOutputChain))
		// startupVolume is the controller's persisted record of this
		// device's volume (updated on every volume_state report) — restore
		// it through the Server, not a raw tinymix write: SeedVolume keeps
		// the recorded level in sync and only honours the first push per
		// run, so a reconnect's config can't stomp a live volume change.
		if msg.StartupVolume > 0 {
			s.SeedVolume(msg.StartupVolume)
		}
		applyAecConfig(canceller, dataClient)
		if nativeBLEEnabled {
			bleScanner.SetEnabled(true)
		} else {
			applyBleConfig(bleScanner)
		}
		applyMWWConfig(dataClient, controlClient, nil, nil)
	})

	// Speaker flush — barge-in: cut buffered TTS the moment the controller
	// hears the wake word during playback.
	controlClient.OnSpeakerFlush(func() {
		pcmSpeaker.Flush()
	})

	// Music flush — the user genuinely stopped or paused. A voice turn ducks
	// instead and must never send this.
	controlClient.OnMusicFlush(func() {
		pcmSpeaker.FlushMusic()
	})

	// Duck — music is attenuated under a voice turn and restored at the end.
	// The depth is read at duck time rather than latched, so a config change
	// takes effect on the next turn without a restart.
	controlClient.OnDuck(func(on bool) {
		localDuck.Confirm()
		if on {
			pcmSpeaker.SetDuck(config.Get().DuckDb)
		} else {
			pcmSpeaker.SetDuck(0)
		}
	})

	// Per-stream playback stats — underrun/period counts reported upstream
	// once per completed TTS stream, persisted against the voice turn.
	pcmSpeaker.OnStreamStats(func(st speaker.StreamStats) {
		// How close the Echo came to hearing a barge-in over this stream.
		// Under private listening nothing else can say: the controller
		// hears no audio during a reply.
		controlClient.SendPlaybackStats(st.Periods, st.Underruns, st, nil)
	})

	// WiFi change — the executor owns the whole switch/rollback sequence
	// (internal/wifi); the reconnect gate polls IsConnected. The outcome
	// is sent as wifi_result with at-least-once delivery: retried on a
	// ticker (and by the OnConnected drain above) until the controller's
	// wifi_commit ack clears it. IsConnected can report true against a
	// half-open TCP connection the interface bounce killed, so a single
	// send is not enough — the very first hardware success vanished that
	// way while the WS looked connected the whole time.
	controlClient.OnWifiChange(func(ssid []byte, psk string) {
		go func() {
			wifi.Change(ssid, psk, controlClient.IsConnected)
			for i := 0; i < 30; i++ { // ~5 min, then give up (dashboard TTL is 4)
				r := wifi.PendingResult()
				if r == nil {
					return
				}
				if controlClient.IsConnected() {
					controlClient.SendWifiResult(r.OK, r.SSID, r.Error)
				}
				time.Sleep(10 * time.Second)
			}
		}()
	})
	controlClient.OnWifiCommit(wifi.Commit)
	controlClient.OnWifiScan(func() {
		go func() {
			nets, err := wifi.Scan()
			if err != nil {
				controlClient.SendWifiScanResult(nil, err.Error())
				return
			}
			controlClient.SendWifiScanResult(nets, "")
		}()
	})

	// Beam lock/unlock — controller locks the beamformer onto the speaker's
	// perimeter mic at wake detection (mid-stream, no restart) and releases
	// it at turn end. Requests are consumed by the mic streaming goroutine.
	controlClient.OnBeamLock(func(lock bool) {
		if lock {
			dataClient.RequestBeamLock()
		} else {
			dataClient.RequestBeamUnlock()
		}
	})

	// Mute state change — notify controller so dashboard can reflect it,
	// and stop/restart the mic stream device-side so mute is authoritative
	// regardless of controller state (C5 fix, 2026-07-05 review). Previously
	// only the *controller-initiated* mic_start was refused while muted (see
	// the mic_start callback above) — an already-running stream (e.g. the
	// permanent OWW listening stream) kept running if mute was toggled
	// mid-stream, so audio kept leaving the device while the ring showed
	// red. Note this is a partial fix: it stops audio leaving the device
	// over the network, but does not address the still-open, hardware-
	// unverified half of C5 — whether tinymix ctls 105/106 (chip A only)
	// actually silence the physical ADC path for ch6 and the perimeter
	// mics on chips B–D. That requires an on-device `tinymix -D 0` full
	// dump to confirm the sibling mute controls before touching them (see
	// review C5 fix sequence) — deliberately not guessed at here.
	s.SetMuteChangeCallback(func(muted bool) {
		updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) {
			snapshot.Muted = muted
			if muted {
				snapshot.Message = "Microphones muted"
			}
		})
		if nativeClient != nil {
			nativeClient.ReportSettings(map[string]any{"muted": muted})
			if muted {
				nativeClient.StopCapture(true)
			}
		} else {
			controlClient.SendMuteState(muted)
		}
		if muted {
			// Reported as muted rather than as the stream stopping, which
			// StopMic would otherwise record.
			dataClient.CloseAnyListen(listen.ReasonMuted)
			dataClient.StopMic()
		} else {
			// Restore the permanent OWW listening stream on unmute — no
			// lock_mic, matching the normal idle state. If the controller
			// also sends its own mic_start around the same time, StartMic
			// is idempotent (ignores the call while already active).
			if nativeClient != nil {
				dataClient.StartLocalMic()
			} else {
				dataClient.StartMic(false)
			}
		}
	})

	// Volume change — notify controller so HA entity and dashboard reflect it.
	// Fires on every Set() call: physical button press or future volume_set command.
	s.SetVolumeChangeCallback(func(level int) {
		updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) {
			snapshot.VolumePercent = deviceVolumePercent(level)
		})
		if nativeClient != nil {
			nativeClient.ReportSettings(map[string]any{"volume_percent": deviceVolumePercent(level)})
		} else {
			controlClient.SendVolumeState(level)
		}
		// The hardware echo reference is tapped upstream of the DAC volume
		// control, so it holds full scale whatever the user sets. Tell the
		// canceller the scalar it cannot see, or every volume change is an
		// echo-path gain step the adaptive filter can only find by
		// re-converging — measured on 2026-08-29 as cancellation dropping to
		// -1.7dB after a change and taking 3-4s to recover, repeatedly.
		canceller.SetPlaybackLevel(level)
	})
	// Seed it from where the device actually is, right now. The callback
	// above only fires on a CHANGE, and the two things that would produce
	// one at startup both have holes: SeedVolume is skipped entirely when
	// the controller pushes startupVolume=0 (a device it has no record
	// for), and Set() is a no-op-shaped path nothing guarantees runs. Miss
	// it and refScale stays 0 — read as unity — while the codec sits at
	// whatever level the previous run left behind, which is round one's
	// 33dB-hot reference reappearing on a device nobody touched.
	canceller.SetPlaybackLevel(s.VolumeLevel())

	// Volume set from controller (HA MediaPlayerCommandRequest forwarded down).
	// Calls Set() which applies tinymix, updates LEDs, and fires the change
	// callback above — so SendVolumeState fires automatically, closing the loop.
	controlClient.OnVolumeSet(func(level int) {
		s.SetVolume(level)
	})

	// Heap-profile dump on SIGUSR1 — the ~1MB/h leak hunt (2026-07-17).
	// The device accepts no inbound connections, so instead of an HTTP pprof
	// endpoint the profile is written to /tmp and pulled over the shell
	// proxy: `kill -USR1 $(pidof server)` then base64 the file out. A GC
	// runs first so the profile reflects live objects, not garbage awaiting
	// collection. Fixed filenames (2 slots, alternating) so repeated dumps
	// for before/after diffing can't fill /tmp.
	usrCh := make(chan os.Signal, 1)
	signal.Notify(usrCh, syscall.SIGUSR1)
	go func() {
		slot := 0
		for range usrCh {
			runtime.GC()
			path := fmt.Sprintf("/tmp/heap-%d.pprof", slot)
			f, err := os.Create(path)
			if err != nil {
				log.Printf("[pprof] create %s: %v", path, err)
				continue
			}
			if err := pprof.WriteHeapProfile(f); err != nil {
				log.Printf("[pprof] write %s: %v", path, err)
			} else {
				log.Printf("[pprof] heap profile written to %s", path)
			}
			f.Close()
			slot = 1 - slot
		}
	}()

	log.Println("Ready")
	time.Sleep(2 * time.Second)

	go func() {
		if nativeClient != nil {
			if err := nativeClient.Run(ctx); err != nil && err != context.Canceled {
				log.Printf("Tater native client stopped: %v", err)
			}
			return
		}
		if err := controlClient.Run(ctx, dataClient); err != nil && err != context.Canceled {
			log.Printf("Control client stopped: %v", err)
		}
	}()

	// Periodic stats reporter — every 30s. SendStats silently drops when
	// the device is not connected, so this goroutine runs unconditionally.
	// Every 10th tick (~5min) a [mem] line goes to the local log: the
	// process RSS is growing ~1.2MB/h (measured 2026-07-16) and the Go
	// runtime's own accounting is what distinguishes heap growth (leak —
	// HeapAlloc climbs), fragmentation/retained-but-free memory (HeapAlloc
	// flat, HeapSys/RSS climb), and goroutine leaks (goroutines climb).
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		tick := 0
		for range ticker.C {
			st := collectStats()
			st.Ble = bleScanner.Stats()
			st.MwwShadow = mwwShadowStats(dataClient)
			st.AecRef = canceller.RefSource()
			controlClient.SendStats(st)
			if tick%10 == 0 {
				var ms runtime.MemStats
				runtime.ReadMemStats(&ms)
				memLine := fmt.Sprintf("[mem] goroutines=%d heap_alloc=%dKB heap_sys=%dKB heap_idle=%dKB released=%dKB stack=%dKB rss=%dKB num_gc=%d pause_total=%dms",
					runtime.NumGoroutine(),
					ms.HeapAlloc/1024, ms.HeapSys/1024, ms.HeapIdle/1024,
					ms.HeapReleased/1024, ms.StackSys/1024, selfRSSKb(),
					ms.NumGC, ms.PauseTotalNs/1e6)
				log.Print(memLine)
				// Forward to the controller's device_logs too — the local
				// /tmp/server.log is RAM-backed and dies with every reboot,
				// and the 2026-07 leak hunt needed these lines pulled over
				// the shell proxy by hand. One message per ~5min; SendLog
				// silently drops while disconnected, same as SendStats.
				controlClient.SendLog("info", memLine)
			}
			tick++
		}
	}()

	// Graceful shutdown on SIGTERM/SIGINT — both the OTA restart
	// (`kill $PPID` from the deploy shell) and start_server.sh's trap send
	// SIGTERM, so this runs on every normal stop. The speaker Close mutes
	// and disables the amp before the PCM stream tears down: without it,
	// every stop/restart/OTA clicked (amp cut mid-stream) and the amp was
	// left driving an idle DAC while the server was down (audible hiss
	// between OTA slots). Nothing else needs orderly teardown — mic/LED/
	// WS state all reset cleanly on the next start.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	sig := <-sigCh
	log.Printf("Received %v — shutting down (muting output, amp off)", sig)
	bleScanner.SetEnabled(false) // scan off + /dev/stpbt closed so the chip idles
	if nativeClient != nil {
		nativeClient.Close()
	}
	dataClient.SetMWWShadowScorer(nil)
	pcmSpeaker.Close()
	os.Exit(0)
}

// ─── Hardware stats collection ────────────────────────────────────────────────

// mwwShadowStats drains the independent Tater microWakeWord observer. Raw and
// sliding maxima are both retained: the former diagnoses model activity while
// the latter is the actual value tested against the manifest threshold.
func mwwShadowStats(dc *client.DataClient) interface{} {
	sc := dc.MWWShadowScorer()
	if sc == nil {
		return nil
	}
	st := sc.Drain()
	return map[string]interface{}{
		"chunks":      st.Chunks,
		"samples":     st.Samples,
		"scores":      st.Scores,
		"drops":       st.Drops,
		"staleDrops":  st.StaleDrops,
		"resets":      st.Resets,
		"crossings":   st.Crossings,
		"closeMisses": st.CloseMisses,
		"maxRawScore": st.MaxRawScore,
		"maxScore":    st.MaxScore,
		"threshold":   st.Threshold,
		"closeMiss":   st.CloseMiss,
		"windowSize":  st.WindowSize,
		"errors":      st.Errors,
		"lastErr":     st.LastErr,
		"ready":       sc.Ready(),
		"maxInferMs":  st.MaxInferMs,
		"maxGapMs":    st.MaxGapMs,
		"maxQueueMs":  st.MaxQueueMs,
	}
}

func collectStats() client.DeviceStats {
	cpuPct := cpuPercent()
	memUsed, memTotal := memStats()
	stoUsed, stoTotal := storageStats()
	rssi := wifiRSSI()
	tx, rx, txErr, txDrop, rxCrc := netDeltas()
	speed, freq, bssid := linkInfo()
	cpuC, maxC, coreLimit := thermals()
	return client.DeviceStats{
		AmbientLux:       als.Lux(),
		CPUTempC:         cpuC,
		MaxTempC:         maxC,
		CoresOnline:      coresOnline(),
		CoresTotal:       coresTotal(),
		ThermalCoreLimit: coreLimit,
		CPUPct:           cpuPct,
		MemUsedMb:        memUsed,
		MemTotalMb:       memTotal,
		StorageUsedMb:    stoUsed,
		StorageTotalMb:   stoTotal,
		WifiRssi:         rssi,
		WifiSsid:         wifi.CurrentSSID(),
		LinkSpeedMbps:    speed,
		WifiFreqMhz:      freq,
		WifiBssid:        bssid,
		TxBytes:          tx,
		RxBytes:          rx,
		TxErrors:         txErr,
		TxDropped:        txDrop,
		RxCrcErrors:      rxCrc,
	}
}

// ─── Network telemetry ────────────────────────────────────────────────────────

// netCounters holds the previous sysfs read so stats can be reported as
// per-interval deltas. Only collectStats touches it (single stats goroutine).
var netCounters struct {
	tx, rx, txErr, txDrop, rxCrc uint64
	primed                       bool
}

// netDeltas returns tx/rx bytes and error counts accumulated since the
// previous call, read from /sys/class/net/wlan0/statistics/. Plain file
// reads — no process spawn — so this is cheap enough for every stats tick.
// The first call primes the baseline and reports zeros.
func netDeltas() (tx, rx, txErr, txDrop, rxCrc uint64) {
	read := func(name string) uint64 {
		b, err := os.ReadFile("/sys/class/net/wlan0/statistics/" + name)
		if err != nil {
			return 0
		}
		v, _ := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
		return v
	}
	ctx, crx := read("tx_bytes"), read("rx_bytes")
	cErr, cDrop, cCrc := read("tx_errors"), read("tx_dropped"), read("rx_crc_errors")

	// delta guards against counter resets (interface bounce) by clamping
	// a negative difference to 0 rather than reporting a huge number.
	delta := func(cur, prev uint64) uint64 {
		if cur < prev {
			return 0
		}
		return cur - prev
	}
	if netCounters.primed {
		tx = delta(ctx, netCounters.tx)
		rx = delta(crx, netCounters.rx)
		txErr = delta(cErr, netCounters.txErr)
		txDrop = delta(cDrop, netCounters.txDrop)
		rxCrc = delta(cCrc, netCounters.rxCrc)
	}
	netCounters.tx, netCounters.rx = ctx, crx
	netCounters.txErr, netCounters.txDrop, netCounters.rxCrc = cErr, cDrop, cCrc
	netCounters.primed = true
	return
}

// linkInfoCache holds the last wpa_cli result and when it was taken.
var linkInfoCache struct {
	speed, freq int
	bssid       string
	at          time.Time
}

// linkInfoInterval — how often the wpa_cli subprocess is actually run.
// Unlike everything else in collectStats this costs a process spawn, and
// PHY rate / band / AP change on the scale of minutes, not seconds. Cached
// values are reused between refreshes so every stats message still carries
// the fields.
const linkInfoInterval = 2 * time.Minute

// linkInfo returns negotiated PHY rate (Mbps), frequency (MHz) and BSSID.
//
// Requires the -p control-socket path: plain `wpa_cli -i wlan0` answers
// UNKNOWN COMMAND on FireOS because the default socket dir doesn't exist.
// Returns zero values if wpa_supplicant isn't reachable — the fields are
// omitempty, so the controller sees them absent rather than wrong.
func linkInfo() (speed, freq int, bssid string) {
	if time.Since(linkInfoCache.at) < linkInfoInterval {
		return linkInfoCache.speed, linkInfoCache.freq, linkInfoCache.bssid
	}
	linkInfoCache.at = time.Now()

	out, err := exec.Command("wpa_cli", "-p", "/data/misc/wifi/sockets",
		"-i", "wlan0", "signal_poll").Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
			if !ok {
				continue
			}
			n, convErr := strconv.Atoi(v)
			if convErr != nil {
				continue
			}
			switch k {
			case "LINKSPEED":
				linkInfoCache.speed = n
			case "FREQUENCY":
				linkInfoCache.freq = n
			}
		}
	}
	if out, err := exec.Command("wpa_cli", "-p", "/data/misc/wifi/sockets",
		"-i", "wlan0", "status").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(line), "bssid="); ok {
				linkInfoCache.bssid = v
				break
			}
		}
	}
	return linkInfoCache.speed, linkInfoCache.freq, linkInfoCache.bssid
}

// cpuPercent samples /proc/stat twice over 500ms and returns utilisation %.
func cpuPercent() float64 {
	type snap struct{ total, idle uint64 }

	read := func() (snap, bool) {
		f, err := os.Open("/proc/stat")
		if err != nil {
			return snap{}, false
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "cpu ") {
				continue
			}
			fields := strings.Fields(line)[1:] // skip "cpu"
			var vals [8]uint64
			for i := 0; i < len(fields) && i < 8; i++ {
				vals[i], _ = strconv.ParseUint(fields[i], 10, 64)
			}
			// user nice system idle iowait irq softirq steal
			idle := vals[3] + vals[4] // idle + iowait
			total := vals[0] + vals[1] + vals[2] + vals[3] +
				vals[4] + vals[5] + vals[6] + vals[7]
			return snap{total, idle}, true
		}
		return snap{}, false
	}

	s1, ok1 := read()
	time.Sleep(500 * time.Millisecond)
	s2, ok2 := read()
	if !ok1 || !ok2 {
		return 0
	}
	dTotal := float64(s2.total - s1.total)
	if dTotal <= 0 {
		return 0
	}
	dIdle := float64(s2.idle - s1.idle)
	pct := (1 - dIdle/dTotal) * 100
	// Round to one decimal place
	return math.Round(pct*10) / 10
}

// memStats reads /proc/meminfo and returns (used MB, total MB).
func memStats() (usedMb, totalMb int) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()

	var totalKb, availKb uint64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		val, _ := strconv.ParseUint(fields[1], 10, 64)
		switch fields[0] {
		case "MemTotal:":
			totalKb = val
		case "MemAvailable:":
			availKb = val
		}
	}
	if totalKb == 0 {
		return 0, 0
	}
	usedKb := totalKb - availKb
	return int(usedKb / 1024), int(totalKb / 1024)
}

// storageStats returns (used MB, total MB) for /data via statfs.
func storageStats() (usedMb, totalMb int) {
	var st syscall.Statfs_t
	if err := syscall.Statfs("/data", &st); err != nil {
		return 0, 0
	}
	bsize := uint64(st.Bsize)
	total := st.Blocks * bsize
	free := st.Bfree * bsize
	used := total - free
	const mb = 1024 * 1024
	return int(used / mb), int(total / mb)
}

// selfRSSKb reads the process's resident set size from /proc/self/status —
// the OS's ground truth, against which the Go runtime numbers in the [mem]
// log line are compared. 0 if unreadable.
func selfRSSKb() int {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && fields[0] == "VmRSS:" {
			kb, _ := strconv.Atoi(fields[1])
			return kb
		}
	}
	return 0
}

// wifiRSSI reads /proc/net/wireless and returns the signal level in dBm,
// or nil if the interface is not available.
//
// Some kernels encode the level field as a positive offset (0–255) rather
// than signed dBm; values > 0 are adjusted by subtracting 256 to recover
// the actual dBm reading (e.g. 206 → -50 dBm).
func wifiRSSI() *int {
	f, err := os.Open("/proc/net/wireless")
	if err != nil {
		return nil
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	lineNum := 0
	for sc.Scan() {
		lineNum++
		if lineNum <= 2 {
			continue // skip two header lines
		}
		fields := strings.Fields(sc.Text())
		// fields: [iface status link level noise ...]
		if len(fields) < 4 {
			continue
		}
		// level is fields[3], may have a trailing "."
		rssiStr := strings.TrimRight(fields[3], ".")
		rssi, err := strconv.Atoi(rssiStr)
		if err != nil {
			continue
		}
		// Correct offset encoding used by some kernels
		if rssi > 0 {
			rssi -= 256
		}
		// Sanity check — valid RSSI is roughly -30 to -100 dBm
		if rssi < -120 || rssi > 0 {
			continue
		}
		return &rssi
	}
	return nil
}

// ─── Hardware config ──────────────────────────────────────────────────────────

// applyHardwareConfig sets the mixer controls for fields that map to hardware.
// Called whenever the controller pushes a config message.
func applyHardwareConfig(msg config.ConfigMessage) {
	// Non-nil rather than non-zero: 0 is the bottom of each control's own
	// range and a legitimate setting. Under the old guard the dashboard
	// offered it, the config stored it, and the mic stayed where it was —
	// a control that appeared to work. Absent is still absent, so firmware
	// meeting a controller that omits the key behaves as it always did.
	if msg.AdcDigitalGain != nil {
		g := strconv.Itoa(*msg.AdcDigitalGain)
		for _, adc := range []string{"A", "B", "C", "D"} {
			mixer.Set("ADC_"+adc+" Digital Volume Control", g)
		}
	}
	if msg.AdcMicpga != nil {
		g := strconv.Itoa(*msg.AdcMicpga)
		for _, adc := range []string{"A", "B", "C", "D"} {
			mixer.Set("ADC_"+adc+" MICPGA Volume Ctrl", g)
		}
	}
}

// applyAecConfig pushes the current effective AEC config into the canceller.
// SetParams no-ops when nothing changed, so calling it on every config push
// is free; when delay/tail change it rebuilds the echo state (adaptive
// filter state is meaningless across a timing change anyway).
func applyAecConfig(canceller *aec.Canceller, dataClient *client.DataClient) {
	snap := config.Get().Snapshot()
	enabled := snap.AecEnabled != nil && *snap.AecEnabled
	delayMs := 250
	if snap.AecDelayMs != nil {
		delayMs = *snap.AecDelayMs
	}
	canceller.SetParams(enabled, delayMs, snap.AecTailMs)
	// The reference SOURCE lives on the data client, not the canceller: it
	// is the mic goroutine that extracts ch8 and decides per period whether
	// to hand it over, so that goroutine has to own the switch.
	dataClient.SetAecRefSource(snap.AecRefSource)
}

// applyBleConfig starts/stops the BLE proxy scanner from the current
// effective config. SetEnabled is idempotent, so calling it on every config
// push is free.
// mwwShadowState makes config reapplication idempotent. Rebuilding is more
// expensive than changing a scalar: it reloads the TFLM model and discards its
// streaming state, so only an actual enable/model/threshold change does it.
var mwwShadowState struct {
	sync.RWMutex
	enabled       bool
	ready         bool
	model         string
	wakeWord      string
	label         string
	source        string
	sensitivity   string
	environment   string
	threshold     float64
	slidingWindow int
	closeMiss     float64
	lastErr       string
}

func setMWWState(enabled, ready bool, model, wakeWord, label, source, sensitivity, environment string,
	threshold float64, slidingWindow int, closeMiss float64, lastErr string) {
	mwwShadowState.Lock()
	mwwShadowState.enabled = enabled
	mwwShadowState.ready = ready
	mwwShadowState.model = model
	mwwShadowState.wakeWord = wakeWord
	mwwShadowState.label = label
	mwwShadowState.source = source
	mwwShadowState.sensitivity = sensitivity
	mwwShadowState.environment = environment
	mwwShadowState.threshold = threshold
	mwwShadowState.slidingWindow = slidingWindow
	mwwShadowState.closeMiss = closeMiss
	mwwShadowState.lastErr = lastErr
	mwwShadowState.Unlock()
}

func mwwNativeStatus() map[string]any {
	mwwShadowState.RLock()
	defer mwwShadowState.RUnlock()
	return map[string]any{
		"name":                 "micro_wake_word",
		"ready":                mwwShadowState.ready,
		"model":                mwwShadowState.model,
		"active_wake_word":     mwwShadowState.wakeWord,
		"active_wake_label":    mwwShadowState.label,
		"active_model_source":  mwwShadowState.source,
		"sensitivity":          mwwShadowState.sensitivity,
		"environment":          mwwShadowState.environment,
		"threshold":            mwwShadowState.threshold,
		"sliding_window":       mwwShadowState.slidingWindow,
		"close_miss_threshold": mwwShadowState.closeMiss,
		"last_error":           mwwShadowState.lastErr,
	}
}

// applyMWWConfig manages the Tater microWakeWord scorer. It is observational
// when onWake is nil (legacy controller mode) and opens native voice turns
// when direct Tater mode supplies the callback.
func applyMWWConfig(dc *client.DataClient, cc *client.ControlClient,
	onWake func(string, float32, time.Time) bool,
	onCloseMiss func(string, float32, time.Time)) {
	snap := config.Get().Snapshot()
	enabled := snap.MwwShadowEnabled != nil && *snap.MwwShadowEnabled
	model := snap.MwwModel
	sensitivity := snap.MwwSensitivity
	environment := snap.MwwEnvironment
	threshold := float64(0)
	if snap.MwwThreshold != nil {
		threshold = *snap.MwwThreshold
	}
	slidingWindow := 0
	if snap.MwwSlidingWindow != nil {
		slidingWindow = *snap.MwwSlidingWindow
	}
	closeMiss := float64(0)
	if snap.MwwCloseMiss != nil {
		closeMiss = *snap.MwwCloseMiss
	}

	if !enabled {
		if dc.MWWShadowScorer() != nil {
			dc.SetMWWShadowScorer(nil)
			log.Printf("[mww-shadow] disabled")
		}
		setMWWState(false, false, model, "", "", "", sensitivity, environment,
			threshold, slidingWindow, closeMiss, "")
		return
	}

	mwwShadowState.RLock()
	same := mwwShadowState.enabled && mwwShadowState.model == model &&
		mwwShadowState.sensitivity == sensitivity && mwwShadowState.environment == environment &&
		mwwShadowState.threshold == threshold && mwwShadowState.slidingWindow == slidingWindow &&
		mwwShadowState.closeMiss == closeMiss
	previousErr := mwwShadowState.lastErr
	mwwShadowState.RUnlock()
	if dc.MWWShadowScorer() != nil && same {
		return
	}
	manifest, manifestErr := microwakeword.ReadPackageManifest(model)
	wakeWord, label, source := "hey_tater", "Hey Tater", "embedded"
	if manifestErr == nil {
		wakeWord = strings.TrimSpace(manifest.WakeWord)
		label = strings.TrimSpace(manifest.Label)
		if label == "" {
			label = wakeWord
		}
		if model != microwakeword.DefaultPackage {
			source = "custom"
		}
	}

	sc, err := microwakeword.OpenShadowTunedWithHooks(model, microwakeword.ScorerOverrides{
		Threshold: float32(threshold), SlidingWindow: slidingWindow,
		CloseMissThreshold: float32(closeMiss), Sensitivity: sensitivity, Environment: environment,
	}, microwakeword.ShadowHooks{
		Cross: func(score float32, at time.Time) {
			ageMs := time.Since(at).Milliseconds()
			if onWake != nil && onWake(wakeWord, score, at) {
				log.Printf("[mww] local wake score=%.3f age=%dms — native wake claimed", score, ageMs)
				return
			}
			log.Printf("[mww-shadow] crossing score=%.3f age=%dms (report only)", score, ageMs)
			if cc != nil && cc.HasFeature(client.FeatureMWWShadow) {
				cc.SendMWWShadowCross(score, ageMs)
			}
		},
		CloseMiss: func(score float32, at time.Time) {
			if onCloseMiss != nil {
				onCloseMiss(wakeWord, score, at)
			}
		},
	})
	if err != nil {
		if msg := err.Error(); msg != previousErr {
			log.Printf("[mww-shadow] not started: %v", err)
		}
		dc.SetMWWShadowScorer(nil)
		setMWWState(true, false, model, wakeWord, label, source, sensitivity, environment,
			threshold, slidingWindow, closeMiss, err.Error())
		return
	}

	dc.SetMWWShadowScorer(sc)
	setMWWState(true, true, model, wakeWord, label, source, sensitivity, environment,
		threshold, slidingWindow, closeMiss, "")
	mode := "shadow/report-only"
	if onWake != nil {
		mode = "active native wake"
	}
	log.Printf("[mww] scoring %s — %s", sc.Info(), mode)
}

func deviceVolumePercent(level int) int {
	// server.volumeMax is the codec's unity-gain index and intentionally
	// private to the server package. Native settings use a human 0..100 scale.
	const unityIndex = 127
	if level <= 0 {
		return 0
	}
	if level >= unityIndex {
		return 100
	}
	return int(math.Round(float64(level) * 100 / unityIndex))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func nativeNumber(value any, fallback float64) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case json.Number:
		if n, err := v.Float64(); err == nil {
			return n
		}
	}
	return fallback
}

func nativeBool(value any, fallback bool) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on", "enabled":
			return true
		case "0", "false", "no", "off", "disabled":
			return false
		}
	}
	return fallback
}

var nativeVisuals = struct {
	sync.RWMutex
	brightness int
	color      [3]uint8
	listening  string
	thinking   string
	tool       string
	replying   string
}{
	brightness: 80,
	color:      [3]uint8{255, 90, 31},
	listening:  "directional",
	thinking:   "sparkle",
	tool:       "ping_pong",
	replying:   "audio_glow",
}

func applyNativeVisualSettings(values, applied map[string]any) {
	nativeVisuals.Lock()
	defer nativeVisuals.Unlock()
	if value, ok := values["led_brightness"]; ok {
		nativeVisuals.brightness = int(math.Round(nativeNumber(value, float64(nativeVisuals.brightness))))
		if nativeVisuals.brightness < 0 {
			nativeVisuals.brightness = 0
		} else if nativeVisuals.brightness > 100 {
			nativeVisuals.brightness = 100
		}
		applied["led_brightness"] = nativeVisuals.brightness
	}
	if value, ok := values["led_color"]; ok {
		text := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(fmt.Sprint(value))), "#")
		if len(text) == 3 {
			text = text[0:1] + text[0:1] + text[1:2] + text[1:2] + text[2:3] + text[2:3]
		}
		if number, err := strconv.ParseUint(text, 16, 24); err == nil && len(text) == 6 {
			nativeVisuals.color = [3]uint8{uint8(number >> 16), uint8(number >> 8), uint8(number)}
			applied["led_color"] = "#" + text
		}
	}
	for key, target := range map[string]*string{
		"led_listening_animation": &nativeVisuals.listening,
		"led_thinking_animation":  &nativeVisuals.thinking,
		"led_tool_call_animation": &nativeVisuals.tool,
		"led_replying_animation":  &nativeVisuals.replying,
	} {
		if value, ok := values[key]; ok {
			*target = strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
			applied[key] = *target
		}
	}
}

// applyTaterSettings maps Tater's native settings vocabulary onto the
// firmware's existing runtime configuration and hardware controllers.
func applyTaterSettings(values map[string]any, srv *server.Server, canceller *aec.Canceller,
	dc *client.DataClient, player *taternative.LocalPlayer) (map[string]any, error) {
	applied := make(map[string]any, len(values))
	for key, value := range values {
		applied[key] = value
	}
	msg := config.ConfigMessage{}
	applyNativeVisualSettings(values, applied)
	if player != nil {
		if err := player.ConfigureWakeSound(values); err != nil {
			return applied, err
		}
	}

	if value, ok := values["volume_percent"]; ok {
		percent := int(math.Round(nativeNumber(value, float64(deviceVolumePercent(srv.VolumeLevel())))))
		if percent < 0 {
			percent = 0
		} else if percent > 100 {
			percent = 100
		}
		srv.SetVolume(int(math.Round(float64(percent) * 127 / 100)))
		applied["volume_percent"] = percent
	}
	if value, ok := values["wake_threshold"]; ok {
		threshold := nativeNumber(value, 0)
		if threshold <= 0 || threshold > 1 {
			return applied, fmt.Errorf("wake_threshold must be in (0, 1]")
		}
		msg.MwwThreshold = &threshold
		applied["wake_threshold"] = threshold
	}
	if value, ok := values["wake_sliding_window"]; ok {
		window := int(math.Round(nativeNumber(value, 0)))
		if window < 1 || window > 100 {
			return applied, fmt.Errorf("wake_sliding_window must be between 1 and 100")
		}
		msg.MwwSlidingWindow = &window
		applied["wake_sliding_window"] = window
	}
	if value, ok := values["close_miss_threshold"]; ok {
		threshold := nativeNumber(value, 0)
		if threshold <= 0 || threshold > 1 {
			return applied, fmt.Errorf("close_miss_threshold must be in (0, 1]")
		}
		msg.MwwCloseMiss = &threshold
		applied["close_miss_threshold"] = threshold
	}
	if value, ok := values["wake_sensitivity"]; ok {
		sensitivity := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
		switch sensitivity {
		case "conservative", "normal", "high":
		default:
			return applied, fmt.Errorf("unsupported wake_sensitivity %q", sensitivity)
		}
		msg.MwwSensitivity = sensitivity
		applied["wake_sensitivity"] = sensitivity
	}
	if value, ok := values["wake_environment"]; ok {
		environment := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
		switch environment {
		case "balanced", "tv_nearby", "strict", "far_field":
		default:
			return applied, fmt.Errorf("unsupported wake_environment %q", environment)
		}
		msg.MwwEnvironment = environment
		applied["wake_environment"] = environment
	}
	if value, ok := values["wake_engine"]; ok {
		engine := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
		switch engine {
		case "off", "button", "micro_wake_word", "server":
		default:
			return applied, fmt.Errorf("unsupported wake_engine %q", engine)
		}
		enabled := engine == "micro_wake_word"
		msg.MwwShadowEnabled = &enabled
		applied["wake_engine"] = engine
	}
	if value, ok := values["wake_word"]; ok {
		wakeWord := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
		if wakeWord == "hey tater" {
			wakeWord = "hey_tater"
		}
		if wakeWord == "custom_url" {
			packageName, err := microwakeword.InstallPackageURLRevision(
				context.Background(),
				strings.TrimSpace(fmt.Sprint(values["wake_word_url"])),
				strings.TrimSpace(fmt.Sprint(values["wake_model_revision"])),
			)
			if err != nil {
				return applied, err
			}
			msg.MwwModel = packageName
			applied["wake_word"] = "custom_url"
			applied["wake_word_url"] = strings.TrimSpace(fmt.Sprint(values["wake_word_url"]))
		} else if wakeWord != "" && wakeWord != "hey_tater" {
			return applied, fmt.Errorf("wake word %q is not installed on this Echo", wakeWord)
		} else {
			msg.MwwModel = "hey_tater"
			applied["wake_word"] = "hey_tater"
		}
	}
	if value, ok := values["aec_enabled"]; ok {
		enabled := nativeBool(value, true)
		msg.AecEnabled = &enabled
		applied["aec_enabled"] = enabled
	}
	if value, ok := values["aec_delay_ms"]; ok {
		delay := int(math.Round(nativeNumber(value, 0)))
		if delay < 0 {
			delay = 0
		} else if delay > 1000 {
			delay = 1000
		}
		msg.AecDelayMs = &delay
		applied["aec_delay_ms"] = delay
	}
	if value, ok := values["barge_in_enabled"]; ok {
		enabled := nativeBool(value, true)
		msg.BargeInEnabled = &enabled
		applied["barge_in_enabled"] = enabled
	}
	config.Get().Apply(msg)
	applyAecConfig(canceller, dc)
	return applied, nil
}

func nativeStateAnimation(state string) server.AnimSpec {
	nativeVisuals.RLock()
	brightness := float64(nativeVisuals.brightness) / 100
	color := [3]uint8{
		uint8(math.Round(float64(nativeVisuals.color[0]) * brightness)),
		uint8(math.Round(float64(nativeVisuals.color[1]) * brightness)),
		uint8(math.Round(float64(nativeVisuals.color[2]) * brightness)),
	}
	listening, thinking := nativeVisuals.listening, nativeVisuals.thinking
	tool, replying := nativeVisuals.tool, nativeVisuals.replying
	nativeVisuals.RUnlock()
	visual := func(name, fallback string) string {
		name = strings.ToLower(strings.TrimSpace(name))
		switch name {
		case "directional", "sparkle", "ping_pong", "audio_glow", "voice_ring", "spinner", "orbit",
			"pulse", "breathe", "comet", "dual_comet", "scanner", "ripple",
			"heartbeat", "theater", "wave", "shimmer", "twinkle", "equalizer", "solid":
			return name
		default:
			return fallback
		}
	}
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "listening":
		pattern := visual(listening, "directional")
		if pattern == "directional" {
			return server.AnimSpec{Pattern: "solid", Colors: [][3]uint8{color}, Listening: true, TTLSec: 30}
		}
		return server.AnimSpec{Pattern: pattern, Colors: [][3]uint8{color}, PeriodMs: 70, TTLSec: 30}
	case "thinking":
		return server.AnimSpec{Pattern: visual(thinking, "sparkle"), Colors: [][3]uint8{color}, PeriodMs: 70, TTLSec: 135}
	case "tool_call":
		return server.AnimSpec{Pattern: visual(tool, "ping_pong"), Colors: [][3]uint8{color}, PeriodMs: 70, TTLSec: 135}
	case "speaking", "playing":
		return server.AnimSpec{Pattern: visual(replying, "audio_glow"), Colors: [][3]uint8{color}, PeriodMs: 50, TTLSec: 180}
	case "error":
		return server.AnimSpec{Pattern: "pulse", Colors: [][3]uint8{{200, 0, 0}}, PeriodMs: 38, TTLSec: 10}
	default:
		return server.AnimSpec{Pattern: "off"}
	}
}

func updateShow(screen *show.Server, change func(*show.Snapshot)) {
	if screen != nil {
		screen.Update(change)
	}
}

func showPhase(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "playing":
		return "music"
	case "tool_call":
		return "thinking"
	case "idle", "listening", "thinking", "speaking", "error":
		return strings.ToLower(strings.TrimSpace(state))
	default:
		return "idle"
	}
}

func showMessage(state string, payload map[string]any) string {
	for _, key := range []string{"message", "text", "title"} {
		if value := strings.TrimSpace(fmt.Sprint(payload[key])); value != "" && value != "<nil>" {
			return value
		}
	}
	switch showPhase(state) {
	case "listening":
		return "I’m listening"
	case "thinking":
		return "Thinking"
	case "speaking":
		return "Replying"
	case "music":
		return "Now playing"
	case "error":
		return "Needs attention"
	default:
		return "Ready when you are"
	}
}

func scaleNativeColor(color [3]uint8, scale float64) [3]uint8 {
	return [3]uint8{
		uint8(math.Round(float64(color[0]) * scale)),
		uint8(math.Round(float64(color[1]) * scale)),
		uint8(math.Round(float64(color[2]) * scale)),
	}
}

func nativeTimerAnimation() server.AnimSpec {
	return server.AnimSpec{Pattern: "pulse", Colors: [][3]uint8{{255, 80, 0}}, PeriodMs: 27}
}

var localDuck *speaker.LocalDuck

func applyBleConfig(scanner *bluetooth.Scanner) {
	snap := config.Get().Snapshot()
	scanner.SetEnabled(snap.BleProxyEnabled != nil && *snap.BleProxyEnabled)
}

func allLEDs(r, g, b uint8) []led.Led {
	leds := make([]led.Led, 12)
	for i := range leds {
		leds[i] = led.Led{ID: i, R: r, G: g, B: b}
	}
	return leds
}

// ─── LED animations ───────────────────────────────────────────────────────────

// pulsePhase returns the current point in a cycle of `period` that began at
// `start`, as a fraction in [0,1).
//
// Elapsed time, not a step counter: a counter advances one step per tick
// whether or not the tick was on time, so a delayed tick stretches that cycle
// rather than skipping ahead within it — the ring slows down under load and
// never catches up. animator.go's runPulse already works this way.
func pulsePhase(start time.Time, period time.Duration) float64 {
	return math.Mod(float64(time.Since(start))/float64(period), 1.0)
}

// pulseOrange — sine-wave orange pulse while disconnected from server.
//
// Started ONCE per disconnection, not once per reconnect attempt — see
// pulseKind at the OnDisconnected call site. Cancelling and relaunching this
// resets the phase to mid-brightness, which is visible as a stutter.
func pulseOrange(ctx context.Context, s *server.Server) {
	const (
		minBr  = 0.05
		maxBr  = 0.6
		period = 2000 * time.Millisecond
		stepMs = 50
	)
	ticker := time.NewTicker(stepMs * time.Millisecond)
	defer ticker.Stop()
	start := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t := pulsePhase(start, period)
			br := minBr + (maxBr-minBr)*(0.5+0.5*math.Sin(2*math.Pi*t))
			s.SetLEDs(allLEDs(uint8(255*br), uint8(40*br), 0), nil)
		}
	}
}

// pulseWhite — slow white pulse while pending controller approval.
// Slower and dimmer than orange to be visually distinct.
func pulseWhite(ctx context.Context, s *server.Server) {
	const (
		minBr  = 0.02
		maxBr  = 0.35
		period = 3000 * time.Millisecond // slower than orange
		stepMs = 50
	)
	ticker := time.NewTicker(stepMs * time.Millisecond)
	defer ticker.Stop()
	start := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t := pulsePhase(start, period)
			br := minBr + (maxBr-minBr)*(0.5+0.5*math.Sin(2*math.Pi*t))
			v := uint8(255 * br)
			s.SetLEDs(allLEDs(v, v, v), nil)
		}
	}
}

// ─── Thermals and core hotplug ────────────────────────────────────────────────
//
// Two facts about this SoC make these worth reporting, and they are related.
//
// The MT8163 is a QUAD-core Cortex-A53, but MediaTek's hotplug strategy parks
// cores that are not needed: /sys/devices/system/cpu/online is usually just
// "0". A second core comes online only after utilisation holds above
// /proc/hps/up_threshold (80%) for up_times (2) samples. So a device sitting
// at 54% is not near a ceiling — it is comfortably inside one core's budget
// with three more parked.
//
// That directly undermines cpuPct, which is derived from the aggregate
// /proc/stat line and is therefore a share of ONLINE capacity: the same
// absolute work halves its reported percentage the moment a second core
// appears. Reporting coresOnline alongside it is what makes the number
// interpretable rather than merely available.
//
// Thermals matter for the opposite reason — to show there is nothing to worry
// about, or to show when there is. thermalCoreLimit is the sharpest indicator
// this SoC offers: it is how many cores the thermal governor will currently
// permit, so anything below 4 means throttling has begun, which shows up as
// capacity loss long before a temperature reading looks alarming.

// thermalZones maps a zone type ("mtktscpu") to its temp file. Resolved once —
// the names are stable for the life of the boot, and rescanning 11 sysfs
// directories every 30s to learn nothing would be silly.
var (
	thermalOnce   sync.Once
	thermalByType map[string]string
)

func resolveThermalZones() map[string]string {
	thermalOnce.Do(func() {
		thermalByType = map[string]string{}
		dirs, err := filepath.Glob("/sys/class/thermal/thermal_zone*")
		if err != nil {
			return
		}
		for _, d := range dirs {
			b, err := os.ReadFile(filepath.Join(d, "type"))
			if err != nil {
				continue
			}
			thermalByType[strings.TrimSpace(string(b))] = filepath.Join(d, "temp")
		}
		log.Printf("[thermal] %d zones: %s", len(thermalByType), strings.Join(zoneTypes(), " "))
	})
	return thermalByType
}

func zoneTypes() []string {
	out := make([]string, 0, len(thermalByType))
	for t := range thermalByType {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// readMilliC reads a sysfs temperature (millidegrees C) as degrees.
func readMilliC(path string) (float64, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, false
	}
	// Sanity bound: a plausible reading is roughly -20..150C. Some MTK zones
	// report a sentinel (0, or a huge value) when their sensor is not wired,
	// and averaging that into a trend would quietly ruin it — the same reason
	// the RF counters are deliberately not surfaced.
	c := float64(n) / 1000.0
	if c < -20 || c > 150 {
		return 0, false
	}
	return c, true
}

// thermals returns the CPU zone temperature, the hottest zone of any kind, and
// how many cores the thermal governor currently permits.
//
// mtktscpu is the SoC/CPU zone. The hottest-of-all figure is reported too
// because the PMIC and board sensors can run warmer than the CPU, and a device
// in trouble will not necessarily show it on the zone you thought to watch.
func thermals() (cpuC *float64, maxC *float64, coreLimit int) {
	zones := resolveThermalZones()
	if c, ok := readMilliC(zones["mtktscpu"]); ok {
		cpuC = &c
	}
	var hottest float64
	var any bool
	for _, p := range zones {
		if c, ok := readMilliC(p); ok && (!any || c > hottest) {
			hottest, any = c, true
		}
	}
	if any {
		maxC = &hottest
	}
	// /proc/hps/num_limit_thermal — cores the thermal governor allows. Absent
	// on a kernel without MTK HPS, reported as 0 = unknown rather than 0 cores.
	if b, err := os.ReadFile("/proc/hps/num_limit_thermal"); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
			coreLimit = n
		}
	}
	return cpuC, maxC, coreLimit
}

// coresOnline counts online CPUs from /sys/devices/system/cpu/online, whose
// format is a range list ("0", "0-3", "0,2-3").
func coresOnline() int {
	b, err := os.ReadFile("/sys/devices/system/cpu/online")
	if err != nil {
		return 0
	}
	n := 0
	for _, part := range strings.Split(strings.TrimSpace(string(b)), ",") {
		if part == "" {
			continue
		}
		lo, hi, found := strings.Cut(part, "-")
		a, err1 := strconv.Atoi(strings.TrimSpace(lo))
		if !found {
			if err1 == nil {
				n++
			}
			continue
		}
		z, err2 := strconv.Atoi(strings.TrimSpace(hi))
		if err1 == nil && err2 == nil && z >= a {
			n += z - a + 1
		}
	}
	return n
}

// hpsCoreFloor is the minimum number of CPU cores kept online.
//
// The MT8163 has four Cortex-A53 cores and MediaTek's hotplug strategy parks
// all but one, bringing a second up only after utilisation holds above
// /proc/hps/up_threshold (80%) for up_times (2) samples. That is a sensible
// default for an idle appliance and a poor one for this workload: the mic
// pipeline has a hard 160ms deadline (the ALSA ring's whole depth) and now
// shares a core with wake word inference that runs in ~31ms bursts. Time-
// slicing those on one core works — measured, zero stalls — but it works with
// no margin for a coincidence, and it depends on hotplug reacting in time to a
// burst that has already started.
//
// A floor of 2 lets the two actually run in parallel, and leaves up_threshold
// to scale to 3 and 4 exactly as before. The cost is one A53 core out of idle,
// which on a mains-powered device sitting at 33C is not a real cost: measured
// +0.3C at the PMIC and no change at the CPU zone.
//
// Set via num_base_perf_serv, which is HPS's core-count FLOOR (the num_limit_*
// files are its ceilings, all 4 here). Deliberately NOT done by writing
// cpu1/online directly: HPS would re-park it within down_times samples, and
// fighting the governor is how you get a setting that appears to work and
// silently stops.
const hpsCoreFloor = 2

// platformInitMarker is how start_server.sh tells a binary that has the
// platform-init mode from one that would ignore the argument and start a
// second server. It greps the binary for this string.
const platformInitMarker = "EM_PLATFORM_INIT_V1"

// platformInit applies the detected board's kernel tuning and exits. emOS only:
// on FireOS the vendor's thermal_manager owns this. An unknown board is not a
// failure — it keeps the kernel defaults, which are the stricter setting.
func platformInit() int {
	fmt.Printf("platform-init (%s) base=%s", platformInitMarker, platform.Base())
	b := board.Detect("")
	fmt.Printf(" board=%s", board.IDOf(b))
	if platform.Base() != platform.EmOS {
		fmt.Println(" — not emOS, nothing to do")
		return 0
	}
	if b == nil || b.Tuning == nil {
		fmt.Println(" — no profile, kernel defaults kept")
		return 0
	}
	lines, err := board.Apply("", b.Tuning)
	for _, l := range lines {
		fmt.Printf(" | %s", l)
	}
	if err != nil {
		fmt.Printf(" — FAILED: %v\n", err)
		return 1
	}
	fmt.Println(" — ok")
	return 0
}

// applyCoreFloor raises the hotplug floor, best-effort.
//
// procfs, so it does not survive a reboot — which is why it lives here, in the
// binary, rather than in a provisioning script: it travels with the firmware
// and re-applies on every start. Absent on a kernel without MTK HPS, in which
// case there is nothing to do and nothing to warn about.
func applyCoreFloor() {
	const path = "/proc/hps/num_base_perf_serv"
	before, err := os.ReadFile(path)
	if err != nil {
		return // not an MTK HPS kernel
	}
	if strings.TrimSpace(string(before)) == strconv.Itoa(hpsCoreFloor) {
		return
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(hpsCoreFloor)), 0o644); err != nil {
		log.Printf("[cpu] could not raise core floor to %d: %v", hpsCoreFloor, err)
		return
	}
	log.Printf("[cpu] core floor %s -> %d (online=%d, hotplug still scales above up_threshold)",
		strings.TrimSpace(string(before)), hpsCoreFloor, coresOnline())
}

// coresTotal is how many cores the SoC has, online or parked. Reported so a
// "1 of 4 online" reads as a power state rather than a one-core device — which
// is how the MT8163's hotplug behaviour gets misread.
func coresTotal() int {
	b, err := os.ReadFile("/sys/devices/system/cpu/present")
	if err != nil {
		return 0
	}
	n := 0
	for _, part := range strings.Split(strings.TrimSpace(string(b)), ",") {
		lo, hi, found := strings.Cut(part, "-")
		a, err1 := strconv.Atoi(strings.TrimSpace(lo))
		if !found {
			if err1 == nil {
				n++
			}
			continue
		}
		z, err2 := strconv.Atoi(strings.TrimSpace(hi))
		if err1 == nil && err2 == nil && z >= a {
			n += z - a + 1
		}
	}
	return n
}
