package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/actionbutton"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/aec"
	internalbuttons "github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/buttons"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/jack"
	hardwareled "github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/led"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/mic"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/mixer"
	hardwareprivacy "github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/privacy"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/speaker"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bluetooth"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/client"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/clock"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/config"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/platform"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/recovery"
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
  platform-init   apply the board's Tater Linux platform settings
  setup-portal    serve the first-boot captive portal on port 80
  setup-mode      serve the satellite setup hotspot and pairing page
  help            this text
`

const (
	satChatVideoFrameInterval = 100 * time.Millisecond
	satChatVideoCaptureYield  = 25 * time.Millisecond
	satChatVideoErrorBackoff  = 500 * time.Millisecond
)

func satChatVideoCaptureDelay(elapsed time.Duration, succeeded bool) time.Duration {
	if !succeeded {
		return satChatVideoErrorBackoff
	}
	delay := satChatVideoFrameInterval - elapsed
	if delay < satChatVideoCaptureYield {
		return satChatVideoCaptureYield
	}
	return delay
}

func main() {
	log.SetOutput(os.Stdout)
	config.ConfigureTarget(client.FirmwareTarget)
	// Biscuit and Radar have no dependable bootloader-level Volume Up route.
	// Inspect their held-key bitmap before either runtime takes the buttons so
	// the current factory image can perform its automatic-return TWRP handoff.
	// Screen satellites enter TWRP in their boot chain and intentionally skip
	// this Linux-side check.
	if len(os.Args) == 1 || (len(os.Args) == 2 && os.Args[1] == "setup-mode") {
		attemptStartupTWRPGesture()
	}

	// Only a bare invocation runs the daemon. `server --version` used to
	// start a second instance in the foreground, fighting the supervised one
	// for the mic and the native link, so anything unrecognised is
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
		case "setup-mode":
			if err := runScreenSetupMode(); err != nil {
				log.Fatalf("Tater screen setup mode: %v", err)
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

	log.Printf("Tater Echo Firmware %s starting", client.Version)
	mixer.ConfigureTarget(client.FirmwareTarget)
	hardwareled.ConfigureTarget(client.FirmwareTarget)

	deviceID := client.GetSerialNo()
	log.Printf("Device ID: %s", deviceID)

	// A WiFi change that never got committed (crash/power cycle mid-switch)
	// is rolled back before anything tries to use the network — same
	// self-healing philosophy as the A/B binary slots.
	wifi.RecoverIfPending()
	if updated, err := microwakeword.EnsureEmbeddedDefaultPackage(); err != nil {
		log.Printf("[wake] embedded Hey Tater package could not be installed: %v", err)
	} else if updated {
		log.Printf("[wake] installed the verified built-in Hey Tater MWW/OWW/NWW package")
	}
	if updated, err := microwakeword.EnsureReleaseCompanionRuntime(
		context.Background(), client.FirmwareTarget, client.Version); err != nil {
		log.Printf("[oww] release runtime bootstrap deferred: %v", err)
	} else if updated {
		log.Printf("[oww] installed the verified %s companion runtime before wake startup", client.FirmwareTarget)
	}

	// Amazon's WiFi Simple Setup daemon (BLE+WiFi provisioning of
	// neighbouring Amazon devices) is useless on a repurposed device and
	// was caught busy-looping at ~50% CPU / 40% sys on one unit (Office,
	// 2026-07-13 — likely retrying the Bluetooth transport the BLE proxy
	// takes over). Same stock-service takeover as `stop mixer` /
	// `stop acebutton` / `stop ledcontroller` in the hardware bindings.
	// Idempotent: a no-op on boots where init never starts it (Lounge).
	if platform.Base() != platform.TaterLinux {
		exec.Command("stop", "smarthomewifid").Run()
	}

	// Keep a second CPU core online. procfs, so this has to be re-applied on
	// every start — see applyCoreFloor for why the mic pipeline's 160ms
	// deadline makes it worth doing.
	applyCoreFloor()

	buttonController, err := internalbuttons.NewButtonControllerForTarget(client.FirmwareTarget)
	if err != nil {
		log.Fatalf("Failed to initialize Button controller: %v", err)
	}

	microphone, err := mic.NewMicrophoneForTarget(client.FirmwareTarget)
	if err != nil {
		log.Fatalf("Failed to initialize Microphone: %v", err)
	}

	// AEC canceller — far end fed by the speaker's echo tap, near end run
	// by the data client on the mono mic stream. Starts disabled; armed by
	// applyAecConfig from env defaults below and on every config push.
	canceller := aec.NewForTarget(client.FirmwareTarget)

	// The level tap drives the energy-reactive LED ring ("meter" pattern).
	// The Server doesn't exist yet when the speaker starts its pump loop,
	// so the tap goes through an atomic pointer armed just below.
	var srvPtr atomic.Pointer[server.Server]
	var showServerPtr atomic.Pointer[show.Server]
	pcmSpeaker, err := speaker.NewPcmSpeakerForTarget(client.FirmwareTarget, canceller.WriteFar, func(rms float64) {
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
	var rookPrivacy *hardwareprivacy.Controller
	var athenaPrivacy *hardwareprivacy.AthenaController
	if client.FirmwareTarget == "rook" {
		s.EnsureMuteStateFile()
		if controller, privacyErr := hardwareprivacy.Open(); privacyErr != nil {
			log.Printf("[privacy] Rook kernel privacy driver unavailable; retaining evdev mute fallback: %v", privacyErr)
		} else {
			rookPrivacy = controller
			log.Printf("[privacy] Rook kernel privacy state enabled")
		}
	} else if client.FirmwareTarget == "athena" {
		s.EnsureMuteStateFile()
		if controller, privacyErr := hardwareprivacy.OpenAthena(); privacyErr != nil {
			log.Printf("[privacy] Athena hardware switch unavailable; retaining evdev mute fallback: %v", privacyErr)
		} else {
			athenaPrivacy = controller
			log.Printf("[privacy] Athena hardware privacy switch enabled")
		}
	}
	srvPtr.Store(s)

	// Local duck starts at the device's own wake crossing and is reconciled by
	// the native voice lifecycle.
	localDuck = speaker.NewLocalDuck(pcmSpeaker.SetDuck, speaker.LocalDuckHold)

	buttonController.SetVolumeCallback(func(direction string) {
		// Inert without a Tater session: nothing is playing to be louder or
		// quieter, and showing the arc would acknowledge a device that
		// cannot act. The mute button stays live — see Server.SetLinkDown.
		if s.LinkDown() {
			log.Println("[cmd] volume button ignored — no Tater session")
			return
		}
		if direction == "up" {
			s.VolumeStepUp()
		} else {
			s.VolumeStepDown()
		}
	})
	var screenSetupResetting atomic.Bool
	buttonController.SetMuteCallback(func() {
		if screenSetupResetting.Load() {
			return // the held sixth press belongs to setup, not mute toggle
		}
		if rookPrivacy != nil || athenaPrivacy != nil {
			// The hardware privacy state has already changed for this button
			// event. Its watcher applies that explicit state; toggling here too
			// would occasionally undo the physical press due to event ordering.
			return
		}
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
	if nativeURL == "" {
		log.Fatalf("Tater native bootstrap has no server URL; run setup or configure TATER_NATIVE_URL")
	}
	sendspinName := strings.TrimSpace(bootstrap.DeviceName)
	if value := strings.TrimSpace(os.Getenv("TATER_DEVICE_NAME")); value != "" {
		sendspinName = value
	}
	if sendspinName == "" {
		sendspinName = "Tater Echo " + deviceID
	}
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
		resetOptions := taternative.SetupResetOptions{}
		if platform.Base() == platform.TaterLinux {
			resetOptions.WiFiPath = "/data/tater-linux/wpa_supplicant.conf"
			resetOptions.Restart = func() {
				if err := restartScreenAfterSetup(); err != nil {
					log.Printf("[setup] restart after reset: %v", err)
				}
			}
		}
		if err := taternative.ResetToSetup(resetOptions); err != nil {
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
	// Screen targets have no action button. Keep intercom on the touchscreen and use
	// a deliberately awkward physical recovery gesture instead: five short
	// Mute presses, then hold the sixth for five seconds. Ordinary mute presses
	// still work, so setup remains reachable even if the screen or Tater
	// connection is unhealthy without adding an easy-to-hit setup button.
	if isScreenTarget(client.FirmwareTarget) {
		var mutedBeforeGesture atomic.Bool
		screenSetupGesture := actionbutton.New(actionbutton.Config{IntercomHold: 24 * time.Hour}, actionbutton.Callbacks{
			ShowClicks: func(count, target int) {
				if count == 1 {
					// The edge callback precedes the normal release toggle.
					mutedBeforeGesture.Store(s.IsMuted())
				}
				updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) {
					snapshot.Message = fmt.Sprintf("Setup recovery · %d of %d", count, target)
				})
			},
			ShowCountdown: func(remaining, total int) {
				updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) {
					if remaining <= 0 {
						snapshot.Message = "Starting Tater setup"
					} else {
						snapshot.Message = fmt.Sprintf("Keep holding Mute · %d", remaining)
					}
				})
			},
			ClearFeedback: func() {
				updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) {
					snapshot.Message = "Ready when you are"
				})
			},
			SetupComplete: func() {
				screenSetupResetting.Store(true)
				// Five short Mute taps invert mute. Restore the starting state
				// before reboot so setup does not unexpectedly start muted.
				if s.IsMuted() != mutedBeforeGesture.Load() {
					s.MuteToggle()
				}
				if err := resetToSetup("screen satellite mute gesture", true); err != nil {
					screenSetupResetting.Store(false)
					log.Printf("[tater-native] physical setup reset failed: %v", err)
				}
			},
		})
		buttonController.SetMuteEventCallback(func(down bool, _ int64) {
			if down {
				screenSetupGesture.Press()
			} else {
				screenSetupGesture.Release()
			}
		})
	}
	// Every supported Echo is a Tater-native satellite. The incoming settings
	// frame may refine the wake model and threshold after hello. Native playback
	// is unprocessed upstream, so the device owns its speaker EQ, bass guard,
	// and limiter for the whole session.
	enabled := true
	config.Get().Apply(config.ConfigMessage{MwwShadowEnabled: &enabled})
	pcmSpeaker.SetOutputChain(config.Get().OutputChain())
	pcmSpeaker.SetOutputChainActive(true)

	dataClient := client.NewDataClientForTarget(deviceID, microphone, pcmSpeaker, canceller, client.FirmwareTarget)
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
	// BLE advertisements stay inside the authenticated native satellite
	// connection. Unsupported targets leave the scanner disabled.
	nativeBLEEnabled := isRingTarget(client.FirmwareTarget) ||
		isScreenTarget(client.FirmwareTarget)
	bleScanner := bluetooth.NewScanner(func(batch []bluetooth.Advert) {
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
	})
	bleScanner.Conns().SetOwnAddress(bluetooth.StaticRandomAddr(deviceID))
	bondPath := strings.TrimSpace(os.Getenv("TATER_BLE_BONDS_PATH"))
	if bondPath == "" {
		bondPath = "/data/local/etc/tater/ble_bonds.json"
		if platform.Base() == platform.TaterLinux {
			bondPath = "/data/tater-linux/ble_bonds.json"
		}
	}
	if err := bleScanner.Conns().SetBondStore(bondPath); err != nil {
		log.Printf("[ble] bonded-device store unavailable: %v", err)
	}
	var bleGATTManager bluetooth.GATTManager = bleScanner.Conns()
	activeGATTEnabled := nativeBLEEnabled
	if nativeBLEEnabled && platform.Base() == platform.TaterLinux &&
		(isRingTarget(client.FirmwareTarget) || strings.EqualFold(client.FirmwareTarget, "athena") ||
			strings.EqualFold(client.FirmwareTarget, "rook")) {
		manager, err := bluetooth.NewBlueZGATTManager(bleScanner.SetConnectionHold)
		if err != nil {
			activeGATTEnabled = false
			log.Printf("[ble] BlueZ active GATT unavailable: %v", err)
		} else {
			bleGATTManager = manager
			log.Printf("[ble] active GATT uses BlueZ on hci0")
		}
	}
	bleGATTBridge := bluetooth.NewBridge(bleGATTManager, func(raw []byte) {
		if nativeClient != nil {
			nativeClient.ReportBLEGATT(raw)
		}
	})
	bleGATTBridge.SetEnabled(activeGATTEnabled)

	// The action button is owned locally; the native client receives the
	// resulting intercom or timer action rather than a raw hardware event.
	_, err = buttonController.SubscribeToButton(func(event pkgbuttons.ButtonClickEvent) {
		log.Printf("Button event: clickType=%d down=%v", event.ClickType, event.Down)
		if event.ClickType == pkgbuttons.DotClick {
			if event.Down {
				if nativeClient != nil && nativeClient.TimerRinging() {
					if stopped := nativeClient.StopRingingTimers("action_button"); stopped > 0 {
						log.Printf("[timer] action button stopped %d ringing timer(s)", stopped)
						return
					}
				}
				actionGesture.Press()
			} else {
				actionGesture.Release()
			}
			return
		}
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
	// Headphone jack. BOTH directions need work from us, and so does the
	// state the device booted into — accdet acts only on a transition, and
	// Init leaves the internal amp on regardless of what is plugged in.
	// SetJackRouting owns the whole mapping (issue #80 for the removal half,
	// measured against a stock Dot 2026-09-03 for the rest).
	if client.FirmwareTarget != "checkers" && client.FirmwareTarget != "crown" && client.FirmwareTarget != "athena" {
		go jack.Watch(ctx, func(inserted bool) {
			pcmSpeaker.SetJackRouting(inserted)
		})
		// Android's audio HAL rewrites the codec on every mediaserver restart —
		// roughly once a minute with a plug inserted — so applying the routing on
		// the jack edge alone holds for about a minute and then the jack goes
		// quiet again. Measured 2026-09-03. Nothing can stop mediaserver (the
		// framework crash-loops without it), so the routing is reconciled instead.
		go pcmSpeaker.WatchJackRouting(ctx)
	}

	satChatCameraAddress := show.DefaultCameraAddress
	if isScreenTarget(client.FirmwareTarget) {
		if appAddress := startSlottedCameraHelper(ctx); appAddress != "" {
			satChatCameraAddress = appAddress
		}
	}
	nativePlayer = taternative.NewLocalPlayer(pcmSpeaker)
	satChatPlayer := taternative.NewSatChatPlayer(pcmSpeaker)
	applyPrimaryWake := func() {
		applyMWWConfig(dataClient,
			func(wakeWord string, score float32, _ time.Time) bool {
				if nativeClient == nil {
					return false
				}
				capture := dataClient.TakeWinningWakeCapture()
				accepted := nativeClient.WakeWithBoundaryCapture(
					wakeWord, score, capture.PreRoll, capture.PostWake, capture.Verifier,
				)
				if accepted && pcmSpeaker.MusicAudible(300*time.Millisecond) {
					// Do not wait for the Tater round trip before making
					// room for the user's command over local music.
					localDuck.Start(config.Get().DuckDb)
				}
				return accepted
			},
			func(wakeWord string, score float32, _ time.Time) {
				if nativeClient != nil {
					nativeClient.CloseMiss(wakeWord, score)
				}
			})
	}
	applyTimerStopWake := func() {
		if err := applyMWWTimerStopConfig(dataClient, func(score float32, _ time.Time) {
			if nativeClient == nil {
				return
			}
			if stopped := nativeClient.StopRingingTimers("wake_word"); stopped > 0 {
				log.Printf("[timer] local stop word score=%.3f stopped %d ringing timer(s)", score, stopped)
			}
		}); err != nil {
			log.Printf("[timer] local stop word unavailable: %v", err)
		}
	}
	applyCurrentWake := func() {
		if nativeClient != nil && nativeClient.TimerRinging() {
			applyTimerStopWake()
			return
		}
		applyPrimaryWake()
	}
	otaInstaller := taternative.NewOTAInstallerForTarget(client.FirmwareTarget)
	otaInstaller.Restart = func() {
		if platform.Base() == platform.TaterLinux {
			// The Linux application installer fsyncs its /data slot and active
			// link. The vendor kernel can wedge during orderly driver shutdown,
			// so use its no-sync emergency reset path.
			if err := rebootTaterLinux(); err != nil {
				log.Printf("[ota] Linux reboot failed: %v", err)
			}
			return
		}
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
	}
	tokenPath := bootstrap.TokenPath
	if value := strings.TrimSpace(os.Getenv("TATER_TOKEN_PATH")); value != "" {
		tokenPath = value
	}
	if tokenPath == "" {
		tokenPath = "/data/local/etc/tater/device_token"
	}
	deviceName := sendspinName
	room := firstNonEmpty(strings.TrimSpace(os.Getenv("TATER_ROOM")), bootstrap.Room)
	var screenDiagnostics atomic.Pointer[show.ScreenDiagnostics]
	var videoAudioMu sync.Mutex
	videoAudioStream := ""
	if isScreenTarget(client.FirmwareTarget) {
		screen := show.New(show.DefaultAddress, show.Snapshot{
			Phase: "offline", DeviceName: deviceName, Room: room,
			Message: "Connecting to Tater", Muted: s.IsMuted(),
			VolumePercent:               deviceVolumePercent(s.VolumeLevel()),
			DisplayEdgeGlow:             true,
			WallpaperIntervalMinutes:    1440,
			ScreenBrightness:            35,
			ScreenAutoBrightnessMinimum: 12,
			ScreenNightBrightness:       3,
			ScreenNightStart:            "22:00",
			ScreenNightEnd:              "07:00",
			ScreenNightDisplay:          "red_clock",
			ScreenClockFormat:           "12h",
		}, func(command show.Command) {
			switch command.Action {
			case "screen.ready":
				// The complete current snapshot was already sent on accept.
			case "screen.diagnostics":
				if command.Diagnostics != nil {
					next := *command.Diagnostics
					screenDiagnostics.Store(&next)
				}
			case "intercom.start":
				if nativeClient != nil && !s.IsMuted() && !s.LinkDown() {
					nativeClient.StartIntercom()
				}
			case "intercom.stop":
				if nativeClient != nil {
					nativeClient.StopCapture(false)
				}
			case "sat_chat.start":
				if nativeClient != nil && !s.IsMuted() && !s.LinkDown() {
					nativeClient.StartSatChat(command.Target, firstNonEmpty(command.Mode, "audio"))
				}
			case "sat_chat.end":
				if nativeClient != nil {
					nativeClient.EndSatChat(command.CallID)
				}
			case "climate.set_temperature":
				if nativeClient != nil && command.Value != nil {
					nativeClient.SetClimateTemperature(command.Target, *command.Value, command.Unit)
				}
			case "climate.set_mode":
				if nativeClient != nil {
					nativeClient.SetClimateMode(command.Target, command.Mode)
				}
			case "camera.open":
				if nativeClient != nil {
					nativeClient.OpenCameras()
				}
			case "camera.select":
				if nativeClient != nil {
					nativeClient.SelectCamera(command.Target)
				}
			case "camera.close":
				if nativeClient != nil {
					nativeClient.CloseCamera()
				}
				if screen := showServerPtr.Load(); screen != nil {
					screen.SetCameraFrame(nil)
				}
			case "camera.talk.start":
				if nativeClient != nil && !s.IsMuted() && !s.LinkDown() {
					nativeClient.StartCameraTalkback(command.Target)
				}
			case "camera.talk.stop":
				if nativeClient != nil {
					nativeClient.StopCapture(false)
				}
			case "video.pause", "video.resume", "video.stop", "video.progress", "video.ended", "video.error":
				action := strings.TrimPrefix(command.Action, "video.")
				updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) {
					if snapshot.Video == nil || snapshot.Video.SessionID != command.SessionID {
						return
					}
					switch action {
					case "pause":
						next := *snapshot.Video
						next.State = "paused"
						next.PositionMS = max(command.PositionMS, 0)
						snapshot.Video = &next
					case "resume":
						next := *snapshot.Video
						next.State = "playing"
						next.PositionMS = max(command.PositionMS, 0)
						next.Error = ""
						snapshot.Video = &next
					case "progress":
						// The renderer already owns the live clock. Updating the
						// snapshot here would change its decoder identity and restart
						// playback every time progress is reported upstream.
						return
					default:
						snapshot.Video = nil
					}
				})
				if nativeClient != nil {
					nativeClient.VideoAction(action, command.SessionID, command.PositionMS, command.Error)
				}
			case "timer.stop":
				if nativeClient != nil {
					nativeClient.StopDisplayedTimer("screen")
				}
			}
		})
		screen.SetVideoAudioHandler(func(audioCtx context.Context, sessionID, streamID string, input io.Reader) error {
			const periodBytes = 4096 // mono S16LE, 2048 frames at 48 kHz
			videoAudioMu.Lock()
			pcmSpeaker.FlushMusic()
			pcmSpeaker.EndMusicStream()
			pcmSpeaker.SetMusicLive(true)
			videoAudioStream = streamID
			videoAudioMu.Unlock()
			defer func() {
				videoAudioMu.Lock()
				if videoAudioStream == streamID {
					if audioCtx.Err() != nil {
						pcmSpeaker.FlushMusic()
					}
					pcmSpeaker.EndMusicStream()
					pcmSpeaker.SetMusicLive(false)
					videoAudioStream = ""
				}
				videoAudioMu.Unlock()
			}()

			period := make([]byte, periodBytes)
			for {
				n, readErr := io.ReadFull(input, period)
				videoAudioMu.Lock()
				if videoAudioStream != streamID {
					videoAudioMu.Unlock()
					return context.Canceled
				}
				if n > 0 && audioCtx.Err() == nil {
					clear(period[n:])
					if pumpErr := pcmSpeaker.PumpMusic(period); pumpErr != nil {
						videoAudioMu.Unlock()
						return pumpErr
					}
				}
				videoAudioMu.Unlock()
				if readErr != nil {
					if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
						return nil
					}
					return readErr
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
	nativeCapabilities := taternative.CapabilitiesForTarget(client.FirmwareTarget)
	if systemVersion := platform.Release(""); systemVersion != "" {
		nativeCapabilities["system_version"] = systemVersion
		nativeCapabilities["system_target"] = client.FirmwareTarget
	}
	if !activeGATTEnabled {
		nativeCapabilities["ble_gatt"] = false
		nativeCapabilities["ble_gatt_pairing"] = false
	}
	var (
		satChatVideoMu     sync.Mutex
		satChatVideoCancel context.CancelFunc
	)
	stopSatChatVideo := func() {
		satChatVideoMu.Lock()
		cancel := satChatVideoCancel
		satChatVideoCancel = nil
		satChatVideoMu.Unlock()
		if cancel != nil {
			cancel()
		}
		if screen := showServerPtr.Load(); screen != nil {
			screen.SetSatChatVideo(nil)
		}
	}
	streamCameraVideo := func(videoCtx context.Context, profile string, receive func([]byte)) error {
		streamErr := show.StreamCameraVideoProfile(videoCtx, satChatCameraAddress, profile, func(image []byte) {
			if videoCtx.Err() == nil && !s.IsMuted() && receive != nil {
				receive(image)
			}
		})
		if videoCtx.Err() != nil {
			return videoCtx.Err()
		}
		log.Printf("[camera-video] continuous stream unavailable at %s; using snapshot compatibility path: %v", satChatCameraAddress, streamErr)
		for videoCtx.Err() == nil {
			captureStarted := time.Now()
			captureOK := false
			if !s.IsMuted() {
				captureCtx, captureCancel := context.WithTimeout(videoCtx, 4*time.Second)
				snapshot, captureErr := show.CaptureCameraVideoFrameProfile(captureCtx, satChatCameraAddress, profile)
				captureCancel()
				if captureErr == nil && !s.IsMuted() && receive != nil {
					receive(snapshot.Image)
					captureOK = true
				}
			}
			delay := satChatVideoCaptureDelay(time.Since(captureStarted), captureOK)
			timer := time.NewTimer(delay)
			select {
			case <-videoCtx.Done():
				timer.Stop()
				return videoCtx.Err()
			case <-timer.C:
			}
		}
		return videoCtx.Err()
	}
	startSatChatVideo := func(profile string) {
		stopSatChatVideo()
		videoCtx, cancel := context.WithCancel(ctx)
		satChatVideoMu.Lock()
		satChatVideoCancel = cancel
		satChatVideoMu.Unlock()
		go func() {
			_ = streamCameraVideo(videoCtx, profile, func(image []byte) {
				if nativeClient != nil {
					nativeClient.SendSatChatVideo(image)
				}
			})
		}()
	}
	nativeClient, nativeErr = taternative.New(taternative.Config{
		URL: nativeURL, Token: firstNonEmpty(strings.TrimSpace(os.Getenv("TATER_TOKEN")), bootstrap.Token), TokenPath: tokenPath,
		DeviceID: deviceID, HardwareID: deviceID, DeviceName: deviceName,
		Board: detectedBoard, FirmwareTarget: client.FirmwareTarget, FirmwareVersion: client.Version,
		Room: room, Capabilities: nativeCapabilities,
	}, taternative.Hooks{
		ReplyDirection: s.SetReplyDirectionDegrees,
		DNDChanged: func(active bool) {
			sendspinSetBlocked(active)
			if active {
				satChatPlayer.Stop()
			}
		},
		BLEGATT: bleGATTBridge.Handle,
		CameraSnapshot: func(captureCtx context.Context) (taternative.CameraSnapshot, error) {
			if s.IsMuted() {
				return taternative.CameraSnapshot{}, fmt.Errorf("camera unavailable while muted")
			}
			snapshot, err := show.CaptureCameraSnapshot(captureCtx, satChatCameraAddress)
			if err != nil {
				return taternative.CameraSnapshot{}, err
			}
			if s.IsMuted() {
				return taternative.CameraSnapshot{}, fmt.Errorf("camera unavailable while muted")
			}
			return taternative.CameraSnapshot{
				Image: snapshot.Image, ContentType: snapshot.ContentType,
			}, nil
		},
		CameraStream: func(streamCtx context.Context, profile string, receive func([]byte)) error {
			if s.IsMuted() {
				return fmt.Errorf("camera unavailable while muted")
			}
			return streamCameraVideo(streamCtx, profile, receive)
		},
		DisplayWeather: func(payload map[string]any) {
			updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) {
				snapshot.Weather = showWeather(payload)
				if name := showPayloadText(payload, "assistant_name"); name != "" {
					snapshot.AssistantName = name
				}
				snapshot.TaterTimeUnixMS = int64(nativeNumber(payload["clock_unix_ms"], 0))
				snapshot.TaterUTCOffset = int(nativeNumber(payload["utc_offset_seconds"], 0))
				snapshot.TaterTimezone = firstNonEmpty(showPayloadText(payload, "timezone"), "Tater")
			})
		},
		DisplayClimate: func(payload map[string]any) {
			updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) {
				snapshot.Climate = showClimate(payload)
			})
		},
		DisplayCameras: func(cameras taternative.DisplayCameras) {
			rows := make([]show.Camera, 0, len(cameras.Items))
			for _, camera := range cameras.Items {
				rows = append(rows, show.Camera{
					ID: camera.ID, Name: camera.Name, Room: camera.Room, Doorbell: camera.Doorbell,
					Talkback: camera.Talkback, Available: camera.Available,
				})
			}
			updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) {
				frameURL := ""
				if snapshot.Cameras != nil && snapshot.Cameras.SelectedID == cameras.SelectedID {
					frameURL = snapshot.Cameras.FrameURL
				}
				snapshot.Cameras = &show.Cameras{
					Available: cameras.Available, Items: rows, SelectedID: cameras.SelectedID,
					SelectedName: cameras.SelectedName, SelectedRoom: cameras.SelectedRoom,
					SelectedTalkback: cameras.SelectedTalkback, Loading: cameras.Loading,
					Error: cameras.Error, FrameURL: frameURL, StreamURL: cameras.StreamURL,
					StreamActive: cameras.StreamActive,
				}
			})
		},
		DisplayVideo: func(video taternative.DisplayVideo) {
			if !video.Active || video.SessionID == "" || video.URL == "" {
				updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) { snapshot.Video = nil })
				pcmSpeaker.FlushMusic()
				pcmSpeaker.EndMusicStream()
				return
			}
			updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) {
				snapshot.Video = &show.Video{
					Active: true, SessionID: video.SessionID, URL: video.URL,
					Title: video.Title, Subtitle: video.Subtitle, State: video.State,
					PositionMS: video.PositionMS, DurationMS: video.DurationMS, FPS: video.FPS,
					Loop: video.Loop, Live: video.Live, ServerSeek: video.ServerSeek, Error: video.Error,
				}
			})
		},
		DisplayNotification: func(payload map[string]any) {
			notification, image, contentType := showNotification(payload)
			if screen := showServerPtr.Load(); screen != nil {
				screen.SetNotification(notification, image, contentType)
				screen.SetLastEvent(notification, image, contentType)
			}
		},
		DisplayLastEvent: func(payload map[string]any) {
			event, image, contentType := showNotification(payload)
			if screen := showServerPtr.Load(); screen != nil {
				screen.SetLastEvent(event, image, contentType)
			}
		},
		SatChatDirectory: func(participants []taternative.SatChatParticipant) {
			rows := make([]show.SatChatParticipant, 0, len(participants))
			for _, participant := range participants {
				rows = append(rows, show.SatChatParticipant{
					ID: participant.ID, Name: participant.Name, Room: participant.Room,
					Board: participant.Board, Audio: participant.Audio,
					Video: participant.Video, Busy: participant.Busy,
				})
			}
			updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) {
				if snapshot.SatChat == nil {
					snapshot.SatChat = &show.SatChat{}
				}
				snapshot.SatChat.Participants = rows
			})
		},
		SatChatState: func(call taternative.SatChatState) {
			if call.State == "active" {
				canceller.SetCallMode(true)
				satChatPlayer.Start()
				if strings.EqualFold(call.Mode, "video") {
					startSatChatVideo(call.VideoSendProfile)
				} else {
					stopSatChatVideo()
				}
			} else if call.State == "ended" {
				satChatPlayer.Stop()
				canceller.SetCallMode(false)
				stopSatChatVideo()
			}
			updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) {
				if snapshot.SatChat == nil {
					snapshot.SatChat = &show.SatChat{}
				}
				if call.State == "ended" || call.ID == "" {
					snapshot.SatChat.Call = nil
					return
				}
				snapshot.SatChat.Call = &show.SatChatCall{
					ID: call.ID, State: call.State, Mode: call.Mode,
					Caller: call.Caller, CallerName: call.CallerName,
					Target: call.Target, TargetName: call.TargetName, Reason: call.Reason,
					VideoSendProfile: call.VideoSendProfile,
				}
			})
		},
		SatChatAudio: satChatPlayer.Push,
		SatChatVideo: func(jpeg []byte) {
			if screen := showServerPtr.Load(); screen != nil {
				screen.SetSatChatVideo(jpeg)
			}
		},
		CameraFrame: func(jpeg []byte) {
			if screen := showServerPtr.Load(); screen != nil {
				screen.SetCameraFrame(jpeg)
			}
		},
		Connected: func(selector string) {
			log.Printf("[tater-native] connected as %s", selector)
			if platform.Base() == platform.TaterLinux {
				if err := os.WriteFile("/run/tater-connected", []byte(client.Version+"\n"), 0o644); err != nil {
					log.Printf("[ota] could not write Linux connection health marker: %v", err)
				}
			}
			if pulseCancel != nil {
				pulseCancel()
				pulseCancel = nil
			}
			pulseKind = ""
			s.SetLinkDown(false)
			if err := s.ReconcileMuteHardware(); err != nil {
				log.Printf("[tater-native] mute reconciliation on connect: %v", err)
			}
			if s.IsMuted() {
				s.RestoreMuteRing()
			} else {
				s.StartAnim(nativeStateAnimation("idle"))
				dataClient.StartLocalMic()
			}
			settings := map[string]any{"volume_percent": deviceVolumePercent(s.VolumeLevel())}
			for key, value := range usbEthernetSettings() {
				settings[key] = value
			}
			nativeClient.ReportSettings(settings)
			updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) {
				if snapshot.SatChat == nil {
					snapshot.SatChat = &show.SatChat{}
				}
				snapshot.SatChat.Self = selector
				snapshot.Connected = true
				snapshot.Phase = "idle"
				snapshot.Message = "Ready when you are"
				snapshot.ToolName = ""
				snapshot.ToolMessage = ""
			})
		},
		Disconnected: func(err error) {
			if platform.Base() == platform.TaterLinux {
				_ = os.Remove("/run/tater-connected")
			}
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
				snapshot.ToolName = ""
				snapshot.ToolMessage = ""
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
				phase := showPhase(state)
				snapshot.Phase = phase
				if phase == "tool_call" {
					toolName := firstNonEmpty(
						showPayloadText(payload, "tool_label"),
						showPayloadText(payload, "tool"),
						showPayloadText(payload, "tool_name"),
					)
					toolMessage := firstNonEmpty(
						showPayloadText(payload, "text"),
						showPayloadText(payload, "message"),
					)
					if toolName != "" {
						snapshot.ToolName = toolName
					}
					if toolMessage != "" {
						snapshot.ToolMessage = toolMessage
					}
					snapshot.Message = firstNonEmpty(snapshot.ToolMessage, "Working on that now")
				} else {
					snapshot.Message = showMessage(state, payload)
					snapshot.ToolName = ""
					snapshot.ToolMessage = ""
				}
				if state == "idle" || state == "error" {
					snapshot.DirectionDegrees = nil
					snapshot.AudioLevel = 0
				}
			})
		},
		Settings: func(values map[string]any) (map[string]any, error) {
			applied, err := applyTaterSettings(values, s, canceller, dataClient, nativePlayer)
			if err == nil {
				pcmSpeaker.SetOutputChain(config.Get().OutputChain())
				if _, themeChanged := applied["display_theme"]; themeChanged {
					updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) {
						value := applied["display_theme"]
						snapshot.DisplayTheme = normalizeDisplayTheme(value)
					})
				}
				if value, ok := applied["display_edge_glow"]; ok {
					updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) {
						snapshot.DisplayEdgeGlow = nativeBool(value, true)
					})
				}
				if value, ok := applied["display_wallpaper_urls"]; ok {
					urls := nativeStringSlice(value)
					updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) {
						snapshot.WallpaperURLs = append([]string(nil), urls...)
					})
				}
				if value, ok := applied["display_wallpaper_interval_minutes"]; ok {
					updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) {
						snapshot.WallpaperIntervalMinutes = normalizeWallpaperIntervalMinutes(value)
					})
				}
				if hasScreenSettings(applied) {
					updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) {
						applyScreenSettings(snapshot, applied)
					})
				}
				if _, ok := applied["led_music_animation"]; ok && nativeClient != nil && isRingTarget(client.FirmwareTarget) {
					state := nativeClient.State()
					if player := sendspinPlayer(); player != nil && shouldShowSendspinMusicVisual(
						client.FirmwareTarget,
						player.Active(),
						s.LinkDown(),
						nativeClient.TimerRinging(),
						state,
					) {
						nowPlaying := player.NowPlaying()
						s.StartAnim(nativeMusicAnimation(nowPlaying.PrimaryColor, nowPlaying.AccentColor))
					}
				}
			}
			applyCurrentWake()
			return applied, err
		},
		Status: func() map[string]any {
			wakeEngine := mwwNativeStatus()
			if scorerStats := shadowScorerStats(dataClient.MWWShadowScorers()); scorerStats != nil {
				wakeEngine["scorer_stats"] = scorerStats
			}
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
				"aec":                        canceller.Diagnostics(),
				"volume_percent":             deviceVolumePercent(s.VolumeLevel()),
				"muted":                      s.IsMuted(),
				"sendspin":                   sendspinStatus(),
				"mute_hardware":              s.MuteStatus(),
				"microphone_pipeline_active": dataClient.MicrophonePipelineActive(),
				"led_owner":                  s.LEDOwner(),
				"wake_engine":                wakeEngine,
				"ble":                        bleScanner.Stats(),
				"usb_ethernet":               usbEthernetDiagnostics(),
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
			if diagnostics := screenDiagnostics.Load(); diagnostics != nil {
				status["display"] = *diagnostics
			}
			if nativeClient != nil {
				status["sat_chat_audio_processing"] = nativeClient.SatChatAudioStatus()
			}
			return status
		},
		PlayWakeSound: nativePlayer.PlayWakeSound,
		PlayVoice:     nativePlayer.PlayVoice, StopVoice: nativePlayer.StopVoice,
		PlayOverlay: nativePlayer.PlayOverlay,
		PlayScene:   nativePlayer.PlayScene,
		TimerAlarm: func(active bool, _ taternative.Timer) {
			if active {
				// Arm the stronger correlation-only cleanup and install the
				// dedicated Stop scorer before the alarm goroutine can put its
				// first period on the speaker. This makes even an immediate spoken
				// "stop" observable and prevents the outgoing wake bank from seeing
				// the first chime.
				canceller.SetAlarmMode(true)
				applyTimerStopWake()
				nativePlayer.SetTimerAlarm(true)
			} else {
				nativePlayer.SetTimerAlarm(false)
				canceller.SetAlarmMode(false)
				logTimerStopWakeSummary(dataClient)
			}
			updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) { snapshot.TimerActive = active })
			if active {
				s.StartAnim(nativeTimerAnimation())
			} else {
				applyPrimaryWake()
				state := "idle"
				if nativeClient != nil {
					state = nativeClient.State()
				}
				s.StartAnim(nativeStateAnimation(state))
			}
		},
		TimerUpdate: func(timer taternative.TimerDisplay) {
			updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) {
				if !timer.Active {
					snapshot.Timer = nil
					return
				}
				snapshot.Timer = &show.Timer{
					Active: timer.Active, Ringing: timer.Ringing,
					Count: timer.Count, RingingCount: timer.RingingCount,
					ID: timer.ID, Name: timer.Name, Label: timer.Label,
					OriginalDurationMS: timer.OriginalDurationMS,
					RemainingMS:        timer.RemainingMS, DeadlineUnixMS: timer.DeadlineUnixMS,
				}
			})
		},
		SetupReset: func() error { return resetToSetup("Tater command", false) },
		OTA:        otaInstaller.Install,
		SystemOTA:  otaInstaller.InstallSystem,
	})
	if nativeErr != nil {
		log.Fatalf("Tater native configuration invalid: %v", nativeErr)
	}
	if nativeBLEEnabled {
		// Start only after nativeClient is fully constructed so the first
		// batch cannot race initialization or disappear into a nil client.
		bleScanner.SetEnabled(true)
	}
	dataClient.OnPCMWithSpeech(nativeClient.PushAudioWithSpeech)
	applyCurrentWake()
	s.SetLinkDown(true)
	pulseCtx, cancel := context.WithCancel(ctx)
	pulseCancel, pulseKind = cancel, "orange"
	go pulseOrange(pulseCtx, s)

	if err := startSendspinPlayer(
		pcmSpeaker,
		s,
		deviceID,
		sendspinName,
		client.FirmwareTarget,
		client.Version,
	); err != nil {
		log.Printf("[sendspin] player unavailable: %v", err)
	} else {
		go runSendspinPoll(
			pcmSpeaker,
			s,
			showServerPtr.Load(),
			client.FirmwareTarget,
			func() string {
				if nativeClient != nil {
					return nativeClient.State()
				}
				return "idle"
			},
			func() bool { return nativeClient != nil && nativeClient.TimerRinging() },
		)
	}

	// Mute remains device-sovereign. It closes the native voice turn and local
	// microphone pipeline immediately; unmute restores the permanent wake path.
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
		}
		if muted {
			dataClient.StopMic()
		} else {
			dataClient.StartLocalMic()
		}
	})
	if rookPrivacy != nil {
		watchInitial := s.IsMuted()
		driverMuted, privacyErr := rookPrivacy.State()
		if privacyErr != nil {
			log.Printf("[privacy] Rook startup state unavailable: %v", privacyErr)
		} else {
			watchInitial = driverMuted
			logicalMuted := s.IsMuted()
			switch {
			case logicalMuted && !driverMuted:
				// Persisted mute must remain fail-closed across a daemon restart.
				// The physical button is still the only path back out of privacy.
				if err := rookPrivacy.Engage(); err != nil {
					log.Printf("[privacy] Rook startup privacy engage failed: %v", err)
				} else {
					log.Printf("[privacy] Rook startup privacy engage requested")
				}
			case driverMuted && !logicalMuted:
				if err := s.SetMuted(true); err != nil {
					log.Printf("[privacy] Rook startup mute synchronization: %v", err)
				}
			}
		}
		go rookPrivacy.Watch(ctx, watchInitial, func(muted bool) error {
			if s.IsMuted() == muted {
				return nil
			}
			log.Printf("[privacy] Rook physical privacy changed muted=%t", muted)
			return s.SetMuted(muted)
		})
	}
	if athenaPrivacy != nil {
		watchInitial := s.IsMuted()
		hardwareMuted, privacyErr := athenaPrivacy.State()
		if privacyErr != nil {
			log.Printf("[privacy] Athena startup state unavailable: %v", privacyErr)
		} else {
			watchInitial = hardwareMuted
			if s.IsMuted() != hardwareMuted {
				if err := s.SetMuted(hardwareMuted); err != nil {
					log.Printf("[privacy] Athena startup mute synchronization: %v", err)
				}
			}
		}
		go athenaPrivacy.Watch(ctx, watchInitial, func(muted bool) error {
			if s.IsMuted() == muted {
				return nil
			}
			log.Printf("[privacy] Athena physical privacy changed muted=%t", muted)
			return s.SetMuted(muted)
		})
	}

	// Volume change — report it so Tater and the local display reflect it.
	// Fires on every Set() call: physical button press or future volume_set command.
	s.SetVolumeChangeCallback(func(level int) {
		updateShow(showServerPtr.Load(), func(snapshot *show.Snapshot) {
			snapshot.VolumePercent = deviceVolumePercent(level)
		})
		if nativeClient != nil {
			nativeClient.ReportSettings(map[string]any{"volume_percent": deviceVolumePercent(level)})
		}
		sendspinVolumeChanged(level)
		// The hardware echo reference is tapped upstream of the DAC volume
		// control, so it holds full scale whatever the user sets. Tell the
		// canceller the scalar it cannot see, or every volume change is an
		// echo-path gain step the adaptive filter can only find by
		// re-converging — measured on 2026-08-29 as cancellation dropping to
		// -1.7dB after a change and taking 3-4s to recover, repeatedly.
		canceller.SetPlaybackLevel(level)
	})
	// Seed AEC from the codec's actual startup level before the first live
	// native setting arrives. Otherwise the hardware reference is initially
	// interpreted at unity gain regardless of the persisted volume.
	canceller.SetPlaybackLevel(s.VolumeLevel())

	// Heap-profile dump on SIGUSR1 — the ~1MB/h leak hunt (2026-07-17).
	// Instead of exposing an HTTP pprof endpoint, the profile is written to
	// /tmp and can be collected over a local maintenance console. A GC
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
		if err := nativeClient.Run(ctx); err != nil && err != context.Canceled {
			log.Printf("Tater native client stopped: %v", err)
		}
	}()

	// Keep the local memory diagnostic used for long-running hardware checks.
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			log.Printf("[mem] goroutines=%d heap_alloc=%dKB heap_sys=%dKB heap_idle=%dKB released=%dKB stack=%dKB rss=%dKB num_gc=%d pause_total=%dms",
				runtime.NumGoroutine(),
				ms.HeapAlloc/1024, ms.HeapSys/1024, ms.HeapIdle/1024,
				ms.HeapReleased/1024, ms.StackSys/1024, selfRSSKb(),
				ms.NumGC, ms.PauseTotalNs/1e6)
		}
	}()

	// Graceful shutdown on SIGTERM/SIGINT — native OTA and start_server.sh's
	// trap send SIGTERM, so this runs on every normal stop. The speaker Close mutes
	// and disables the amp before the PCM stream tears down: without it,
	// every stop/restart/OTA clicked (amp cut mid-stream) and the amp was
	// left driving an idle DAC while the server was down (audible hiss
	// between OTA slots). Nothing else needs orderly teardown — mic/LED/
	// native state all resets cleanly on the next start.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	sig := <-sigCh
	log.Printf("Received %v — shutting down (muting output, amp off)", sig)
	bleScanner.SetEnabled(false) // scan off + /dev/stpbt closed so the chip idles
	if nativeClient != nil {
		nativeClient.Close()
	}
	stopSendspinPlayer(pcmSpeaker)
	dataClient.SetMWWShadowScorer(nil)
	pcmSpeaker.Close()
	os.Exit(0)
}

func isScreenTarget(target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	return target == "checkers" || target == "crown" || target == "athena" || target == "rook"
}

// startSlottedCameraHelper launches the camera binary delivered beside an A/B
// application OTA. The immutable rootfs helper remains on 43823 as a safe
// fallback; the slotted helper uses 43824 so init can continue supervising the
// factory copy without a port fight. Pdeathsig prevents an orphan helper from
// surviving an OTA daemon restart.
func startSlottedCameraHelper(ctx context.Context) string {
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	directory := filepath.Clean(filepath.Dir(executable))
	if !strings.Contains(filepath.ToSlash(directory), "/data/tater-linux/app/slots/") {
		return ""
	}
	helper := filepath.Join(directory, "tater-camera")
	info, err := os.Stat(helper)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return ""
	}
	address := "127.0.0.1:43824"
	command := exec.CommandContext(ctx, helper)
	command.Env = append(os.Environ(), "TATER_CAMERA_ADDRESS="+address)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
	if err := command.Start(); err != nil {
		log.Printf("[sat-chat] slotted camera helper did not start: %v", err)
		return ""
	}
	go func() {
		if err := command.Wait(); err != nil && ctx.Err() == nil {
			log.Printf("[sat-chat] slotted camera helper stopped: %v", err)
		}
	}()
	client := &http.Client{Timeout: 150 * time.Millisecond}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/health", nil)
		response, requestErr := client.Do(request)
		if requestErr == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusNoContent {
				log.Printf("[sat-chat] using slotted streaming camera helper at %s", address)
				return address
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = command.Process.Kill()
	log.Printf("[sat-chat] slotted camera helper did not become ready; using rootfs helper")
	return ""
}

func isRingTarget(target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	return target == "biscuit" || target == "radar"
}

// startSetupResetGesture keeps the physical setup-reset escape hatch alive
// while the captive portal owns the daemon.  The normal runtime installs the
// same gesture later, but a user must not be trapped merely because setup is
// already active or because a partially entered configuration is present.
func startSetupResetGesture(ctx context.Context, setupScreen *show.Server) error {
	controller, err := internalbuttons.NewButtonControllerForTarget(client.FirmwareTarget)
	if err != nil {
		return err
	}
	if err := controller.Init(); err != nil {
		return err
	}
	gesture := actionbutton.New(actionbutton.Config{IntercomHold: 24 * time.Hour}, actionbutton.Callbacks{
		ShowClicks: func(count, target int) {
			updateShow(setupScreen, func(snapshot *show.Snapshot) {
				snapshot.Message = fmt.Sprintf("Setup reset · %d of %d", count, target)
			})
		},
		ShowCountdown: func(remaining, _ int) {
			updateShow(setupScreen, func(snapshot *show.Snapshot) {
				if remaining <= 0 {
					snapshot.Message = "Restarting Tater setup"
				} else {
					snapshot.Message = fmt.Sprintf("Keep holding · %d", remaining)
				}
			})
		},
		ClearFeedback: func() {
			updateShow(setupScreen, func(snapshot *show.Snapshot) {
				snapshot.Message = "192.168.4.1"
			})
		},
		SetupComplete: func() {
			options := taternative.SetupResetOptions{
				WiFiPath: "/data/tater-linux/wpa_supplicant.conf",
				Restart: func() {
					if err := restartScreenAfterSetup(); err != nil {
						log.Printf("[setup] restart after setup-mode reset: %v", err)
					}
				},
			}
			if err := taternative.ResetToSetup(options); err != nil {
				log.Printf("[setup] physical reset failed in setup mode: %v", err)
			}
		},
	})
	if isScreenTarget(client.FirmwareTarget) {
		controller.SetMuteEventCallback(func(down bool, _ int64) {
			if down {
				gesture.Press()
			} else {
				gesture.Release()
			}
		})
	}
	subscription, err := controller.SubscribeToButton(func(event pkgbuttons.ButtonClickEvent) {
		if !isRingTarget(client.FirmwareTarget) || event.ClickType != pkgbuttons.DotClick {
			return
		}
		if event.Down {
			gesture.Press()
		} else {
			gesture.Release()
		}
	})
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		subscription.Cancel()
	}()
	return nil
}

func runScreenSetupMode() error {
	if !isScreenTarget(client.FirmwareTarget) && !isRingTarget(client.FirmwareTarget) {
		return fmt.Errorf("setup-mode is only supported by a Tater Linux satellite build")
	}
	log.SetOutput(os.Stdout)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	if isRingTarget(client.FirmwareTarget) {
		hardwareled.ConfigureTarget(client.FirmwareTarget)
		controller, err := hardwareled.NewDefaultController()
		if err != nil {
			log.Printf("[setup] LED ring unavailable: %v", err)
		} else {
			go twinkleSetupRing(ctx, controller)
		}
	}

	ssidBytes, _ := os.ReadFile("/run/tater-setup-ssid")
	ssid := strings.TrimSpace(string(ssidBytes))
	deviceLabel, defaultName := "Echo Show 5", "Tater Checkers"
	if strings.EqualFold(client.FirmwareTarget, "biscuit") {
		deviceLabel, defaultName = "Echo Dot 2nd Generation", "Tater Biscuit"
	} else if strings.EqualFold(client.FirmwareTarget, "radar") {
		deviceLabel, defaultName = "Echo 2nd Generation", "Tater Radar"
	} else if strings.EqualFold(client.FirmwareTarget, "crown") {
		deviceLabel, defaultName = "Echo Show 8", "Tater Crown"
	} else if strings.EqualFold(client.FirmwareTarget, "athena") {
		deviceLabel, defaultName = "Echo Show 8 2nd Generation", "Tater Athena"
	} else if strings.EqualFold(client.FirmwareTarget, "rook") {
		deviceLabel, defaultName = "Echo Spot", "Tater Spot"
	}
	room, message := "USB SETUP", "Connect USB and run the Tater installer"
	if ssid != "" {
		room, message = ssid, "192.168.4.1"
	}
	var setupScreen *show.Server
	if isScreenTarget(client.FirmwareTarget) {
		setupScreen = show.New(show.DefaultAddress, show.Snapshot{
			Phase: "setup", Connected: false, DeviceName: defaultName,
			Room:                        room,
			Message:                     message,
			VolumePercent:               50,
			DisplayEdgeGlow:             true,
			WallpaperIntervalMinutes:    1440,
			ScreenBrightness:            35,
			ScreenAutoBrightnessMinimum: 12,
			ScreenNightBrightness:       3,
			ScreenNightStart:            "22:00",
			ScreenNightEnd:              "07:00",
			ScreenNightDisplay:          "red_clock",
			ScreenClockFormat:           "12h",
		}, nil)
		go func() {
			if err := setupScreen.Run(ctx); err != nil && ctx.Err() == nil {
				log.Printf("[setup] screen service stopped: %v", err)
			}
		}()
	}
	if err := startSetupResetGesture(ctx, setupScreen); err != nil {
		log.Printf("[setup] physical reset gesture unavailable: %v", err)
	}

	if ssid != "" {
		go func() {
			options := taternative.SetupPortalOptions{
				Listen:        "192.168.4.1:80",
				WiFiPath:      "/data/tater-linux/wpa_supplicant.conf",
				DeviceLabel:   deviceLabel,
				DefaultName:   defaultName,
				SetupSSID:     ssid,
				SetupAddress:  "192.168.4.1",
				WiFiConfig:    wifi.LinuxProvisioningConfig,
				BootstrapLast: true,
				Restart: func() {
					if err := restartScreenAfterSetup(); err != nil {
						log.Printf("[setup] restart after pairing: %v", err)
					}
				},
			}
			if err := taternative.RunSetupPortal(options); err != nil && ctx.Err() == nil {
				log.Printf("[setup] hotspot portal stopped: %v", err)
			}
		}()
		log.Printf("[setup] hotspot %s ready at http://192.168.4.1", ssid)
	} else {
		log.Printf("[setup] hotspot unavailable; waiting for USB provisioning")
	}
	<-ctx.Done()
	return nil
}

func setupRingTwinkleFrame(tick int) []led.Led {
	const count = 12
	frame := make([]led.Led, count)
	for position := range frame {
		phase := (position*7 + tick*3) % 31
		if phase < 0 {
			phase += 31
		}
		level := 10
		switch phase {
		case 0:
			level = 243
		case 1, 30:
			level = 123
		case 2, 29:
			level = 51
		}
		frame[position] = led.Led{
			ID: position,
			R:  uint8(255 * level / 256),
			G:  uint8(227 * level / 256),
			B:  uint8(181 * level / 256),
		}
	}
	return frame
}

func twinkleSetupRing(ctx context.Context, controller led.Controller) {
	// This is the established Tater setup signal: independent warm-white
	// points briefly flare around a dim ring. Keep orange for early boot and
	// for the normal runtime's disconnected/reconnecting state.
	ticker := time.NewTicker(33 * time.Millisecond)
	defer ticker.Stop()
	tick := 0
	for {
		_ = controller.SetLEDs(setupRingTwinkleFrame(tick)...)
		tick++
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func rebootTaterLinux() error {
	trigger, err := os.OpenFile("/proc/sysrq-trigger", os.O_WRONLY, 0)
	if err == nil {
		if _, writeErr := trigger.WriteString("b"); writeErr == nil {
			_ = trigger.Close()
			return nil
		} else {
			err = writeErr
		}
		_ = trigger.Close()
	}
	if helperErr := exec.Command("/usr/local/bin/tater-reboot-now").Run(); helperErr != nil {
		return fmt.Errorf("emergency restart unavailable (%v); reboot helper: %w", err, helperErr)
	}
	return nil
}

func attemptStartupTWRPGesture() {
	if platform.Base() != platform.TaterLinux {
		return
	}
	command, err := recovery.StartupTWRPCommand(client.FirmwareTarget)
	if err != nil {
		return
	}
	inside, err := recovery.InStartupGestureWindow("/proc/uptime")
	if err != nil {
		log.Printf("[recovery] startup window unavailable: %v", err)
		return
	}
	if !inside {
		return
	}
	controller, err := internalbuttons.NewButtonControllerForTarget(client.FirmwareTarget)
	if err != nil {
		log.Printf("[recovery] Volume Up detector unavailable: %v", err)
		return
	}
	held, err := controller.VolumeUpHeldFor(recovery.StartupGestureHold)
	if err != nil {
		log.Printf("[recovery] Volume Up startup check failed: %v", err)
		return
	}
	if !held {
		return
	}
	log.Printf("[recovery] Volume Up held for %s during startup; entering TWRP", recovery.StartupGestureHold)
	output, runErr := exec.Command(command.Path, command.Args...).CombinedOutput()
	if message := strings.TrimSpace(string(output)); message != "" {
		log.Printf("[recovery] %s", message)
	}
	if runErr != nil {
		log.Printf("[recovery] TWRP request failed; continuing normal startup: %v", runErr)
		return
	}
	// The reboot helper performs a synchronized restart and should not return
	// on success. Fail open to the normal runtime if it unexpectedly does.
	log.Printf("[recovery] TWRP command returned without rebooting; continuing normal startup")
}

// A setup form submission or physical reset is an intentional reboot, not a
// failed rootfs boot. Refresh an uncommitted slot's boot attempts before the
// direct restart; slotctl syncs its write and may leave /store writable only
// because this path immediately reboots. Older rootfs images lack rearm, so
// keep their existing recovery behavior if that command is unavailable.
func restartScreenAfterSetup() error {
	rearm := exec.Command("/usr/local/sbin/slotctl", "rearm")
	rearm.Env = append(os.Environ(), "SLOTCTL_LEAVE_RW=1")
	if output, err := rearm.CombinedOutput(); err != nil {
		log.Printf("[setup] rootfs trial rearm unavailable: %v: %s", err, strings.TrimSpace(string(output)))
	}
	return exec.Command("/usr/local/bin/tater-reboot-now").Run()
}

func shadowScorerStats(scorers []*microwakeword.ShadowScorer) interface{} {
	if len(scorers) == 0 {
		return nil
	}
	var st microwakeword.ShadowStats
	ready := true
	queueDepth := 0
	lastPushAgeMs := int64(0)
	lastProcessAgeMs := int64(0)
	var totalChunks, totalDrops, totalResets, totalErrors, totalRecoveries uint64
	lanes := make([]map[string]any, 0, len(scorers))
	for index, scorer := range scorers {
		lane := scorer.Drain()
		health := scorer.Health()
		st.Chunks += lane.Chunks
		st.Samples += lane.Samples
		st.Scores += lane.Scores
		st.Drops += lane.Drops
		st.StaleDrops += lane.StaleDrops
		st.Resets += lane.Resets
		st.Crossings += lane.Crossings
		st.CloseMisses += lane.CloseMisses
		st.Errors += lane.Errors
		st.Recoveries += lane.Recoveries
		if lane.MaxRawScore > st.MaxRawScore {
			st.MaxRawScore = lane.MaxRawScore
		}
		if lane.MaxScore > st.MaxScore {
			st.MaxScore = lane.MaxScore
		}
		if lane.MaxInferMs > st.MaxInferMs {
			st.MaxInferMs = lane.MaxInferMs
		}
		if lane.MaxGapMs > st.MaxGapMs {
			st.MaxGapMs = lane.MaxGapMs
		}
		if lane.MaxQueueMs > st.MaxQueueMs {
			st.MaxQueueMs = lane.MaxQueueMs
		}
		if lane.LastErr != "" {
			st.LastErr = lane.LastErr
		}
		st.Threshold, st.CloseMiss, st.WindowSize = lane.Threshold, lane.CloseMiss, lane.WindowSize
		st.PlaybackThreshold = lane.PlaybackThreshold
		st.PlaybackPeakThreshold = lane.PlaybackPeakThreshold
		st.PlaybackActive = st.PlaybackActive || lane.PlaybackActive
		ready = ready && health.Ready
		queueDepth += health.QueueDepth
		if health.LastPushAgeMs > lastPushAgeMs {
			lastPushAgeMs = health.LastPushAgeMs
		}
		if health.LastProcessAgeMs > lastProcessAgeMs {
			lastProcessAgeMs = health.LastProcessAgeMs
		}
		totalChunks += health.TotalChunks
		totalDrops += health.TotalDrops
		totalResets += health.TotalResets
		totalErrors += health.TotalErrors
		totalRecoveries += health.TotalRecoveries
		lanes = append(lanes, map[string]any{
			"lane": index, "ready": health.Ready, "closed": health.Closed,
			"affinityCpu": health.AffinityCPU, "queueDepth": health.QueueDepth,
			"recoveryPending": health.RecoveryPending,
			"lastPushAgeMs":   health.LastPushAgeMs, "lastProcessAgeMs": health.LastProcessAgeMs,
			"totalChunks": health.TotalChunks, "totalDrops": health.TotalDrops,
			"totalResets": health.TotalResets, "totalErrors": health.TotalErrors,
			"totalRecoveries": health.TotalRecoveries,
		})
	}
	return map[string]interface{}{
		"beamScorers":           len(scorers),
		"chunks":                st.Chunks,
		"samples":               st.Samples,
		"scores":                st.Scores,
		"drops":                 st.Drops,
		"staleDrops":            st.StaleDrops,
		"resets":                st.Resets,
		"crossings":             st.Crossings,
		"closeMisses":           st.CloseMisses,
		"maxRawScore":           st.MaxRawScore,
		"maxScore":              st.MaxScore,
		"threshold":             st.Threshold,
		"playbackThreshold":     st.PlaybackThreshold,
		"playbackPeakThreshold": st.PlaybackPeakThreshold,
		"playbackActive":        st.PlaybackActive,
		"closeMiss":             st.CloseMiss,
		"windowSize":            st.WindowSize,
		"errors":                st.Errors,
		"recoveries":            st.Recoveries,
		"lastErr":               st.LastErr,
		"ready":                 ready,
		"maxInferMs":            st.MaxInferMs,
		"maxGapMs":              st.MaxGapMs,
		"maxQueueMs":            st.MaxQueueMs,
		"queueDepth":            queueDepth,
		"lastPushAgeMs":         lastPushAgeMs,
		"lastProcessAgeMs":      lastProcessAgeMs,
		"totalChunks":           totalChunks,
		"totalDrops":            totalDrops,
		"totalResets":           totalResets,
		"totalErrors":           totalErrors,
		"totalRecoveries":       totalRecoveries,
		"lanes":                 lanes,
	}
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

// mwwShadowState makes config reapplication idempotent. Rebuilding is more
// expensive than changing a scalar: it reloads the TFLM model and discards its
// streaming state, so only an actual enable/model/threshold change does it.
var mwwShadowState struct {
	sync.RWMutex
	enabled         bool
	mwwEnabled      bool
	owwEnabled      bool
	nwwEnabled      bool
	ready           bool
	model           string
	owwModel        string
	nwwModel        string
	nwwArchitecture string
	wakeWord        string
	label           string
	source          string
	sensitivity     string
	environment     string
	threshold       float64
	slidingWindow   int
	closeMiss       float64
	owwThreshold    float64
	owwPatience     int
	nwwThreshold    float64
	nwwPatience     int
	lastErr         string
}

var owwCompanionState struct {
	sync.RWMutex
	kind         string
	model        string
	architecture string
	mode         string
	ready        bool
	lastErr      string
	settings     microwakeword.OWWSettings
	nanoSettings microwakeword.NanoSettings
	verifier     *microwakeword.CompanionVerifier
	agreement    *microwakeword.AgreementMatcher
	scorers      []*microwakeword.ShadowScorer
}

// CPU 1 is kept online by the Echo platform core floor where hot-plugging is
// present. Only the selected OWW or NWW inference thread is pinned there; the microphone,
// MWW, audio, networking, and UI remain under the normal Go/kernel scheduler.
// If a platform cannot honor the affinity, scoring continues and status
// reports the pinning error instead of disabling wake detection.
const companionInferenceCPU = 1

func replaceWakeCompanion(kind, model, architecture, mode string, ready bool,
	settings microwakeword.OWWSettings, nanoSettings microwakeword.NanoSettings,
	verifier *microwakeword.CompanionVerifier,
	agreement *microwakeword.AgreementMatcher, scorers []*microwakeword.ShadowScorer, err error) {
	lastErr := ""
	if !ready {
		// An absent optional companion is intentionally quiet while OWW is
		// disabled. Once the user explicitly selects dual or OWW-only mode,
		// however, that same absence is actionable and must be visible in the
		// satellite status instead of looking like a healthy idle detector.
		if err != nil && (!errors.Is(err, microwakeword.ErrCompanionUnavailable) || mode != "disabled") {
			lastErr = err.Error()
		}
		if installErr := microwakeword.CompanionInstallError(model); installErr != "" &&
			installErr != microwakeword.ErrCompanionUnavailable.Error() {
			lastErr = installErr
		}
	}
	owwCompanionState.Lock()
	old := owwCompanionState.verifier
	owwCompanionState.kind = kind
	owwCompanionState.model = model
	owwCompanionState.architecture = architecture
	owwCompanionState.mode = mode
	owwCompanionState.ready = ready
	owwCompanionState.lastErr = lastErr
	owwCompanionState.settings = settings
	owwCompanionState.nanoSettings = nanoSettings
	owwCompanionState.verifier = verifier
	owwCompanionState.agreement = agreement
	owwCompanionState.scorers = append([]*microwakeword.ShadowScorer(nil), scorers...)
	owwCompanionState.Unlock()
	if old != nil && old != verifier {
		old.Close()
	}
}

func owwCompanionStatus() map[string]any {
	owwCompanionState.RLock()
	kind := owwCompanionState.kind
	model := owwCompanionState.model
	architecture := owwCompanionState.architecture
	mode := owwCompanionState.mode
	ready := owwCompanionState.ready
	lastErr := owwCompanionState.lastErr
	settings := owwCompanionState.settings
	nanoSettings := owwCompanionState.nanoSettings
	verifier := owwCompanionState.verifier
	agreement := owwCompanionState.agreement
	scorers := append([]*microwakeword.ShadowScorer(nil), owwCompanionState.scorers...)
	owwCompanionState.RUnlock()
	name := "open_wake_word"
	if kind == "nww" {
		name = "nano_wake_word"
	}
	status := map[string]any{
		"name": name, "mode": mode, "ready": ready,
		"model": model, "last_error": lastErr,
	}
	if kind == "nww" {
		status["architecture"] = architecture
		if nanoSettings.Threshold > 0 && nanoSettings.Patience > 0 {
			status["threshold"] = nanoSettings.Threshold
			status["patience"] = nanoSettings.Patience
		}
	}
	if settings.Threshold > 0 && settings.Patience > 0 {
		status["threshold"] = settings.Threshold
		status["patience"] = settings.Patience
	}
	if verifier != nil {
		verifierSettings := verifier.Settings()
		status["threshold"] = verifierSettings.Threshold
		status["patience"] = verifierSettings.Patience
		status["runtime"] = verifier.Info()
		status["stats"] = verifier.Stats()
	}
	if agreement != nil {
		status["agreement_window_ms"] = microwakeword.DualWakeAgreementWindow.Milliseconds()
		status["stats"] = agreement.Stats()
	}
	if scorerStats := shadowScorerStats(scorers); scorerStats != nil {
		status["scorer_stats"] = scorerStats
	}
	return status
}

func setWakeDetectorSelection(mwwEnabled, owwEnabled, nwwEnabled bool, owwModel, nwwModel, nwwArchitecture string,
	owwSettings microwakeword.OWWSettings, nwwSettings microwakeword.NanoSettings) {
	mwwShadowState.Lock()
	mwwShadowState.mwwEnabled = mwwEnabled
	mwwShadowState.owwEnabled = owwEnabled
	mwwShadowState.nwwEnabled = nwwEnabled
	mwwShadowState.owwModel = owwModel
	mwwShadowState.nwwModel = nwwModel
	mwwShadowState.nwwArchitecture = nwwArchitecture
	mwwShadowState.owwThreshold = float64(owwSettings.Threshold)
	mwwShadowState.owwPatience = owwSettings.Patience
	mwwShadowState.nwwThreshold = float64(nwwSettings.Threshold)
	mwwShadowState.nwwPatience = nwwSettings.Patience
	mwwShadowState.Unlock()
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
		"mode":                 map[bool]string{true: "local", false: "disabled"}[mwwShadowState.enabled],
		"mww_enabled":          mwwShadowState.mwwEnabled,
		"oww_enabled":          mwwShadowState.owwEnabled,
		"nww_enabled":          mwwShadowState.nwwEnabled,
		"oww_model":            mwwShadowState.owwModel,
		"nww_model":            mwwShadowState.nwwModel,
		"nww_architecture":     mwwShadowState.nwwArchitecture,
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
		"confirmation":         owwCompanionStatus(),
	}
}

// applyMWWConfig manages the local wake scorers and opens native voice turns.
func applyMWWConfig(dc *client.DataClient,
	onWake func(string, float32, time.Time) bool,
	onCloseMiss func(string, float32, time.Time)) {
	snap := config.Get().Snapshot()
	mwwEnabled := snap.MwwShadowEnabled != nil && *snap.MwwShadowEnabled
	owwEnabled := snap.OwwEnabled != nil && *snap.OwwEnabled
	nwwEnabled := snap.NwwEnabled != nil && *snap.NwwEnabled
	// OWW+NWW is intentionally not a supported policy. Both are companion
	// embedding classifiers and running them together would contend for the
	// same isolated inference core without the MWW gate the UI promises.
	if owwEnabled && nwwEnabled {
		nwwEnabled = false
	}
	enabled := mwwEnabled || owwEnabled || nwwEnabled
	model := snap.MwwModel
	owwModel := strings.TrimSpace(snap.OwwModel)
	if owwModel == "" {
		owwModel = microwakeword.DefaultPackage
	}
	nwwModel := strings.TrimSpace(snap.NwwModel)
	if nwwModel == "" {
		nwwModel = microwakeword.DefaultPackage
	}
	nwwArchitecture := strings.ToLower(strings.TrimSpace(snap.NwwArchitecture))
	if nwwArchitecture == "" {
		nwwArchitecture = "cnn"
	}
	companionKind, companionModel := "", ""
	if owwEnabled {
		companionKind, companionModel = "oww", owwModel
	} else if nwwEnabled {
		companionKind, companionModel = "nww", nwwModel
	}
	dualEnabled := mwwEnabled && companionKind != ""
	scorerOverrides := microwakeword.LivePolicyOverrides(
		snap.MwwSensitivity,
		snap.MwwEnvironment,
	)
	if snap.MwwThreshold != nil && *snap.MwwThreshold > 0 {
		scorerOverrides.Threshold = float32(*snap.MwwThreshold)
	}
	if snap.MwwSlidingWindow != nil && *snap.MwwSlidingWindow > 0 {
		scorerOverrides.SlidingWindow = *snap.MwwSlidingWindow
	}
	if snap.MwwCloseMiss != nil && *snap.MwwCloseMiss > 0 {
		scorerOverrides.CloseMissThreshold = float32(*snap.MwwCloseMiss)
	}
	owwOverrides := microwakeword.OWWSettings{}
	if snap.OwwThreshold != nil && *snap.OwwThreshold > 0 {
		owwOverrides.Threshold = float32(*snap.OwwThreshold)
	}
	if snap.OwwPatience != nil && *snap.OwwPatience > 0 {
		owwOverrides.Patience = *snap.OwwPatience
	}
	nwwOverrides := microwakeword.NanoSettings{}
	if snap.NwwThreshold != nil && *snap.NwwThreshold > 0 {
		nwwOverrides.Threshold = float32(*snap.NwwThreshold)
	}
	if snap.NwwPatience != nil && *snap.NwwPatience > 0 {
		nwwOverrides.Patience = *snap.NwwPatience
	}
	sensitivity := scorerOverrides.Sensitivity
	environment := scorerOverrides.Environment
	threshold := float64(0)
	slidingWindow := 0
	closeMiss := float64(0)
	var manifest microwakeword.Manifest
	var manifestErr error
	if mwwEnabled {
		manifest, manifestErr = microwakeword.ReadPackageManifest(model)
		if manifestErr == nil {
			calibrated := manifest.RuntimeConfig()
			threshold = float64(calibrated.Threshold)
			slidingWindow = calibrated.SlidingWindow
			closeMiss = float64(calibrated.CloseMissThreshold)
			if scorerOverrides.Threshold > 0 {
				threshold = float64(scorerOverrides.Threshold)
			}
			if scorerOverrides.SlidingWindow > 0 {
				slidingWindow = scorerOverrides.SlidingWindow
			}
			if scorerOverrides.CloseMissThreshold > 0 {
				closeMiss = float64(scorerOverrides.CloseMissThreshold)
			}
		}
	}
	if !enabled {
		if dc.MWWShadowScorer() != nil {
			dc.SetMWWShadowScorer(nil)
			log.Printf("[mww-shadow] disabled")
		}
		setMWWState(false, false, model, "", "", "", sensitivity, environment,
			threshold, slidingWindow, closeMiss, "")
		setWakeDetectorSelection(false, false, false, owwModel, nwwModel, nwwArchitecture, owwOverrides, nwwOverrides)
		replaceWakeCompanion("", "", "", "disabled", false,
			microwakeword.OWWSettings{}, microwakeword.NanoSettings{}, nil, nil, nil, nil)
		return
	}

	mwwShadowState.RLock()
	same := mwwShadowState.enabled && mwwShadowState.model == model &&
		mwwShadowState.mwwEnabled == mwwEnabled && mwwShadowState.owwEnabled == owwEnabled &&
		mwwShadowState.nwwEnabled == nwwEnabled && mwwShadowState.owwModel == owwModel &&
		mwwShadowState.nwwModel == nwwModel && mwwShadowState.nwwArchitecture == nwwArchitecture &&
		mwwShadowState.sensitivity == sensitivity && mwwShadowState.environment == environment &&
		mwwShadowState.threshold == threshold && mwwShadowState.slidingWindow == slidingWindow &&
		mwwShadowState.closeMiss == closeMiss &&
		mwwShadowState.owwThreshold == float64(owwOverrides.Threshold) &&
		mwwShadowState.owwPatience == owwOverrides.Patience &&
		mwwShadowState.nwwThreshold == float64(nwwOverrides.Threshold) &&
		mwwShadowState.nwwPatience == nwwOverrides.Patience
	previousErr := mwwShadowState.lastErr
	mwwShadowState.RUnlock()
	expectedScorers := dc.WakeScorerCount()
	banksReady := len(dc.MWWShadowScorers()) == expectedScorers
	if dualEnabled {
		banksReady = banksReady && len(dc.OWWShadowScorers()) == expectedScorers
	}
	if banksReady && same {
		return
	}
	wakeWord, label, source := "hey_tater", "Hey Tater", "embedded"
	if mwwEnabled && manifestErr == nil {
		wakeWord = strings.TrimSpace(manifest.WakeWord)
		label = strings.TrimSpace(manifest.Label)
		if label == "" {
			label = wakeWord
		}
		if model != microwakeword.DefaultPackage {
			source = "custom"
		}
	}

	scorers := make([]*microwakeword.ShadowScorer, 0, expectedScorers)
	companionScorers := make([]*microwakeword.ShadowScorer, 0, expectedScorers)
	var activeOWWSettings microwakeword.OWWSettings
	var activeNanoSettings microwakeword.NanoSettings
	var agreement *microwakeword.AgreementMatcher
	if dualEnabled {
		agreement = microwakeword.NewAgreementMatcher(microwakeword.DualWakeAgreementWindow)
	}
	dispatch := func(lane int, detectedWord string, score float32, at time.Time) {
		dc.ExtendWinningWakeAudio(lane, at)
		ageMs := time.Since(at).Milliseconds()
		if onWake != nil && onWake(detectedWord, score, at) {
			log.Printf("[wake] local wake lane=%d score=%.3f age=%dms — native wake claimed", lane, score, ageMs)
			return
		}
		log.Printf("[wake] lane=%d crossing score=%.3f age=%dms was not claimed", lane, score, ageMs)
	}
	dispatchAgreement := func(match microwakeword.AgreementMatch) {
		// MWW marks the phrase boundary used by the verifier. The companion may agree on
		// a later frame, but that scheduling/model delta must not slide the
		// verifier window forward and crop the first wake word.
		phraseAt := match.MWWAt
		if phraseAt.IsZero() {
			phraseAt = match.At
		}
		if !dc.ClaimWakeLane(match.Lane, phraseAt) {
			return
		}
		log.Printf("[%s] parallel agreement lane=%d mww=%.3f companion=%.3f delta=%dms", companionKind,
			match.Lane, match.MWWScore, match.OWWScore, match.MWWAt.Sub(match.OWWAt).Abs().Milliseconds())
		dispatch(match.Lane, wakeWord, match.MWWScore, phraseAt)
	}
	openCompanion := func(lane int, confirmationMode bool) (*microwakeword.ShadowScorer, microwakeword.WakeBundle, error) {
		cpu := companionInferenceCPU
		hooks := microwakeword.ShadowHooks{
			AffinityCPU: &cpu,
			Cross: func(score float32, at time.Time) {
				if agreement != nil {
					if match, ok := agreement.Observe(microwakeword.AgreementOWW, lane, score, at); ok {
						dispatchAgreement(match)
					}
					return
				}
				if dc.ClaimWakeLane(lane, at) {
					dispatch(lane, wakeWord, score, at)
				}
			},
		}
		if companionKind == "nww" {
			scorer, bundle, settings, err := microwakeword.OpenNanoShadowTuned(
				companionModel, nwwArchitecture, nwwOverrides, hooks)
			activeNanoSettings = settings
			return scorer, bundle, err
		}
		if confirmationMode {
			scorer, bundle, err := microwakeword.OpenCompanionAgreementShadowTuned(companionModel, owwOverrides, hooks)
			if err == nil {
				activeOWWSettings = owwOverrides
				if activeOWWSettings.Threshold <= 0 {
					activeOWWSettings.Threshold = bundle.OpenWakeWord.RecommendedConfirmationThreshold
				}
				if activeOWWSettings.Patience <= 0 {
					activeOWWSettings.Patience = bundle.OpenWakeWord.RecommendedConfirmationPatience
				}
			}
			return scorer, bundle, err
		}
		scorer, bundle, err := microwakeword.OpenCompanionShadowTuned(companionModel, owwOverrides, hooks)
		if err == nil {
			activeOWWSettings = owwOverrides
			if activeOWWSettings.Threshold <= 0 {
				activeOWWSettings.Threshold = bundle.OpenWakeWord.RecommendedThreshold
			}
			if activeOWWSettings.Patience <= 0 {
				activeOWWSettings.Patience = bundle.OpenWakeWord.RecommendedPatience
			}
		}
		return scorer, bundle, err
	}
	var openErr error
	if mwwEnabled {
		for lane := 0; lane < expectedScorers; lane++ {
			lane := lane
			sc, err := microwakeword.OpenShadowTunedWithHooks(model, scorerOverrides, microwakeword.ShadowHooks{
				Cross: func(score float32, at time.Time) {
					if agreement == nil {
						if dc.ClaimWakeLane(lane, at) {
							dispatch(lane, wakeWord, score, at)
						}
						return
					}
					if match, ok := agreement.Observe(microwakeword.AgreementMWW, lane, score, at); ok {
						dispatchAgreement(match)
					}
				},
				CloseMiss: func(score float32, at time.Time) {
					if lane == 0 && onCloseMiss != nil {
						onCloseMiss(wakeWord, score, at)
					}
				},
			})
			if err != nil {
				openErr = err
				break
			}
			scorers = append(scorers, sc)
			if agreement != nil {
				companionScorer, _, err := openCompanion(lane, true)
				if err != nil {
					openErr = err
					break
				}
				companionScorers = append(companionScorers, companionScorer)
			}
		}
	} else {
		for lane := 0; lane < expectedScorers; lane++ {
			lane := lane
			sc, bundle, err := openCompanion(lane, false)
			if err != nil {
				openErr = err
				break
			}
			if lane == 0 {
				wakeWord = strings.TrimSpace(bundle.Key)
				if wakeWord == "" {
					wakeWord = strings.TrimSpace(bundle.WakeWord)
				}
				label = strings.TrimSpace(bundle.WakeWord)
				if label == "" {
					label = wakeWord
				}
				if companionKind == "nww" {
					source = "nanowakeword"
				} else {
					source = "openwakeword"
				}
			}
			scorers = append(scorers, sc)
		}
	}
	if openErr != nil {
		for _, scorer := range append(scorers, companionScorers...) {
			scorer.Close()
		}
		if msg := openErr.Error(); msg != previousErr {
			log.Printf("[mww-shadow] not started: %v", openErr)
		}
		dc.SetMWWShadowScorer(nil)
		setMWWState(true, false, model, wakeWord, label, source, sensitivity, environment,
			threshold, slidingWindow, closeMiss, openErr.Error())
		setWakeDetectorSelection(mwwEnabled, owwEnabled, nwwEnabled, owwModel, nwwModel, nwwArchitecture, owwOverrides, nwwOverrides)
		mode := "mww_only"
		if dualEnabled {
			mode = "mww_" + companionKind
		} else if owwEnabled {
			mode = "oww_only"
		} else if nwwEnabled {
			mode = "nww_only"
		}
		replaceWakeCompanion(companionKind, companionModel, nwwArchitecture, mode, false,
			activeOWWSettings, activeNanoSettings, nil, nil, nil, openErr)
		return
	}

	if agreement != nil {
		dc.SetWakeShadowScorers(scorers, companionScorers)
	} else {
		dc.SetMWWShadowScorers(scorers)
	}
	detectorMode := "mww_only"
	if dualEnabled {
		detectorMode = "mww_" + companionKind
	} else if owwEnabled {
		detectorMode = "oww_only"
	} else if nwwEnabled {
		detectorMode = "nww_only"
	}
	activeCompanionScorers := companionScorers
	if !mwwEnabled {
		activeCompanionScorers = scorers
	}
	replaceWakeCompanion(companionKind, companionModel, nwwArchitecture, detectorMode,
		owwEnabled || nwwEnabled, activeOWWSettings, activeNanoSettings,
		nil, agreement, activeCompanionScorers, nil)
	setMWWState(true, true, model, wakeWord, label, source, sensitivity, environment,
		threshold, slidingWindow, closeMiss, "")
	setWakeDetectorSelection(mwwEnabled, owwEnabled, nwwEnabled, owwModel, nwwModel, nwwArchitecture, owwOverrides, nwwOverrides)
	activityMode := "shadow/report-only"
	if onWake != nil {
		activityMode = "active native wake"
	}
	log.Printf("[wake] scoring %s — %s (%d continuous wake stream(s), mode=%s)", scorers[0].Info(), activityMode, len(scorers), detectorMode)
	if agreement != nil {
		log.Printf("[%s] parallel dual wake ready — %d continuous companion stream(s), agreement window=%dms",
			companionKind, len(companionScorers), microwakeword.DualWakeAgreementWindow.Milliseconds())
	}
}

// applyMWWTimerStopConfig temporarily replaces the regular wake phrase with a
// dedicated local "stop" detector while a timer alarm is sounding. The stop
// callback is launched after the scorer callback returns: replacing a scorer
// synchronously from its own inference goroutine would wait on itself.
func applyMWWTimerStopConfig(dc *client.DataClient, onStop func(float32, time.Time)) error {
	if err := microwakeword.EnsureBuiltinTimerStopPackage(); err != nil {
		return err
	}
	mwwShadowState.RLock()
	alreadyActive := mwwShadowState.enabled && mwwShadowState.ready &&
		mwwShadowState.model == microwakeword.TimerStopPackage
	mwwShadowState.RUnlock()
	expectedScorers := dc.WakeScorerCount()
	if len(dc.MWWShadowScorers()) == expectedScorers && alreadyActive {
		return nil
	}
	manifest, err := microwakeword.ReadPackageManifest(microwakeword.TimerStopPackage)
	if err != nil {
		return err
	}
	settings := manifest.RuntimeConfig()
	scorers := make([]*microwakeword.ShadowScorer, 0, expectedScorers)
	for lane := 0; lane < expectedScorers; lane++ {
		lane := lane
		scorer, openErr := microwakeword.OpenTimerStopWithHooks(
			microwakeword.ShadowHooks{Cross: func(score float32, at time.Time) {
				if !dc.ClaimWakeLane(lane, at) {
					return
				}
				if onStop != nil {
					go onStop(score, at)
				}
			}},
		)
		if openErr != nil {
			for _, opened := range scorers {
				opened.Close()
			}
			return openErr
		}
		scorers = append(scorers, scorer)
	}
	dc.SetMWWShadowScorers(scorers)
	replaceWakeCompanion("", microwakeword.TimerStopPackage, "", "timer_stop", false,
		microwakeword.OWWSettings{}, microwakeword.NanoSettings{}, nil, nil, nil, nil)
	setWakeDetectorSelection(true, false, false, "", "", "", microwakeword.OWWSettings{}, microwakeword.NanoSettings{})
	label := strings.TrimSpace(manifest.Label)
	if label == "" {
		label = manifest.WakeWord
	}
	setMWWState(true, true, microwakeword.TimerStopPackage, manifest.WakeWord, label,
		"timer", "", "", float64(settings.Threshold), settings.SlidingWindow, 0, "")
	log.Printf("[mww] scoring %s — timer stop mode (%d continuous wake stream(s))", scorers[0].Info(), len(scorers))
	return nil
}

// logTimerStopWakeSummary emits one bounded diagnostic when the final alarm is
// silenced. A failed spoken attempt followed by the hardware/screen fallback
// therefore leaves its best model score in the device log without streaming
// microphone audio or flooding one line per inference window.
func logTimerStopWakeSummary(dc *client.DataClient) {
	mwwShadowState.RLock()
	active := mwwShadowState.enabled && mwwShadowState.model == microwakeword.TimerStopPackage
	mwwShadowState.RUnlock()
	if !active {
		return
	}
	for lane, scorer := range dc.MWWShadowScorers() {
		if scorer == nil {
			continue
		}
		stats := scorer.Drain()
		health := scorer.Health()
		log.Printf("[timer] stop detector summary lane=%d max_mean=%.3f max_raw=%.3f threshold=%.3f scores=%d crossings=%d drops=%d errors=%d",
			lane, stats.MaxScore, stats.MaxRawScore, stats.Threshold, stats.Scores,
			stats.Crossings, stats.Drops, health.TotalErrors)
	}
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

func nativeStringSlice(value any) []string {
	result := make([]string, 0, 8)
	appendValue := func(candidate any) {
		text := strings.TrimSpace(fmt.Sprint(candidate))
		if text == "" || (!strings.HasPrefix(text, "http://") && !strings.HasPrefix(text, "https://")) {
			return
		}
		for _, existing := range result {
			if existing == text {
				return
			}
		}
		if len(result) < 12 {
			result = append(result, text)
		}
	}
	switch values := value.(type) {
	case []any:
		for _, candidate := range values {
			appendValue(candidate)
		}
	case []string:
		for _, candidate := range values {
			appendValue(candidate)
		}
	}
	return result
}

var nativeVisuals = struct {
	sync.RWMutex
	brightness int
	color      [3]uint8
	listening  string
	thinking   string
	tool       string
	replying   string
	music      string
}{
	brightness: 80,
	color:      [3]uint8{255, 90, 31},
	listening:  "directional",
	thinking:   "sparkle",
	tool:       "ping_pong",
	replying:   "audio_glow",
	music:      "music_bars",
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
		"led_music_animation":     &nativeVisuals.music,
	} {
		if value, ok := values[key]; ok {
			*target = strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
			applied[key] = *target
		}
	}
}

func hasScreenSettings(values map[string]any) bool {
	for _, key := range []string{
		"screen_brightness", "screen_auto_brightness_enabled", "screen_auto_brightness_minimum",
		"screen_night_mode_enabled", "screen_night_brightness", "screen_night_start",
		"screen_night_end", "screen_night_display", "screen_clock_format",
	} {
		if _, ok := values[key]; ok {
			return true
		}
	}
	return false
}

func applyScreenSettings(snapshot *show.Snapshot, values map[string]any) {
	if snapshot == nil {
		return
	}
	if value, ok := values["screen_brightness"]; ok {
		snapshot.ScreenBrightness = normalizeScreenPercent(value, snapshot.ScreenBrightness)
	}
	if value, ok := values["screen_auto_brightness_enabled"]; ok {
		snapshot.ScreenAutoBrightness = nativeBool(value, false)
	}
	if value, ok := values["screen_auto_brightness_minimum"]; ok {
		minimum := int(math.Round(nativeNumber(value, float64(snapshot.ScreenAutoBrightnessMinimum))))
		snapshot.ScreenAutoBrightnessMinimum = max(1, min(50, minimum))
	}
	if value, ok := values["screen_night_mode_enabled"]; ok {
		snapshot.ScreenNightMode = nativeBool(value, false)
	}
	if value, ok := values["screen_night_brightness"]; ok {
		snapshot.ScreenNightBrightness = normalizeScreenPercent(value, snapshot.ScreenNightBrightness)
	}
	if value, ok := values["screen_night_start"]; ok {
		snapshot.ScreenNightStart = normalizeScreenClock(value, firstNonEmpty(snapshot.ScreenNightStart, "22:00"))
	}
	if value, ok := values["screen_night_end"]; ok {
		snapshot.ScreenNightEnd = normalizeScreenClock(value, firstNonEmpty(snapshot.ScreenNightEnd, "07:00"))
	}
	if value, ok := values["screen_night_display"]; ok {
		snapshot.ScreenNightDisplay = normalizeScreenNightDisplay(value)
	}
	if value, ok := values["screen_clock_format"]; ok {
		snapshot.ScreenClockFormat = normalizeScreenClockFormat(value)
	}
}

func normalizeScreenPercent(value any, fallback int) int {
	return max(0, min(100, int(math.Round(nativeNumber(value, float64(fallback))))))
}

func normalizeScreenClock(value any, fallback string) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	parsed, err := time.Parse("15:04", text)
	if err != nil {
		return fallback
	}
	return parsed.Format("15:04")
}

func normalizeScreenNightDisplay(value any) string {
	display := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
	switch display {
	case "dim", "red_clock", "off":
		return display
	default:
		return "red_clock"
	}
}

func normalizeScreenClockFormat(value any) string {
	switch strings.ToLower(strings.TrimSpace(fmt.Sprint(value))) {
	case "24h", "24":
		return "24h"
	default:
		return "12h"
	}
}

// applyTaterSettings maps Tater's native settings vocabulary onto the
// firmware's existing runtime configuration and hardware controllers.
func applyTaterSettings(values map[string]any, srv *server.Server, canceller *aec.Canceller,
	dc *client.DataClient, player *taternative.LocalPlayer) (map[string]any, error) {
	applied := make(map[string]any, len(values))
	for key, value := range values {
		// AEC is calibrated and owned by each Echo target. Ignore remote
		// overrides instead of claiming that the firmware applied them.
		switch key {
		case "aec_enabled", "aec_strength_percent", "aec_delay_ms":
			continue
		case "ssh_password":
			// Apply this separately through the root-only helper and never echo
			// it into settings.changed, including on an unrelated early error.
			continue
		}
		applied[key] = value
	}
	msg := config.ConfigMessage{}
	localWakeEnabled := strings.EqualFold(strings.TrimSpace(fmt.Sprint(values["wake_engine"])), "micro_wake_word")
	mwwRequested := localWakeEnabled && nativeBool(values["wake_mww_enabled"], true)
	owwRequested := localWakeEnabled && nativeBool(values["wake_oww_enabled"], false)
	nwwRequested := localWakeEnabled && nativeBool(values["wake_nww_enabled"], false)
	if owwRequested && nwwRequested {
		return applied, fmt.Errorf("openWakeWord and NanoWakeWord cannot be enabled together")
	}
	dualWakeRequested := mwwRequested && owwRequested
	selectedMWWPackage := ""
	applyNativeVisualSettings(values, applied)
	if value, ok := values["display_theme"]; ok {
		applied["display_theme"] = normalizeDisplayTheme(value)
	}
	if value, ok := values["display_wallpaper_interval_minutes"]; ok {
		applied["display_wallpaper_interval_minutes"] = normalizeWallpaperIntervalMinutes(value)
	}
	if value, ok := values["screen_brightness"]; ok {
		applied["screen_brightness"] = normalizeScreenPercent(value, 35)
	}
	if value, ok := values["screen_auto_brightness_enabled"]; ok {
		applied["screen_auto_brightness_enabled"] = nativeBool(value, false)
	}
	if value, ok := values["screen_auto_brightness_minimum"]; ok {
		minimum := int(math.Round(nativeNumber(value, 12)))
		applied["screen_auto_brightness_minimum"] = max(1, min(50, minimum))
	}
	if value, ok := values["screen_night_mode_enabled"]; ok {
		applied["screen_night_mode_enabled"] = nativeBool(value, false)
	}
	if value, ok := values["screen_night_brightness"]; ok {
		applied["screen_night_brightness"] = normalizeScreenPercent(value, 3)
	}
	if value, ok := values["screen_night_start"]; ok {
		applied["screen_night_start"] = normalizeScreenClock(value, "22:00")
	}
	if value, ok := values["screen_night_end"]; ok {
		applied["screen_night_end"] = normalizeScreenClock(value, "07:00")
	}
	if value, ok := values["screen_night_display"]; ok {
		applied["screen_night_display"] = normalizeScreenNightDisplay(value)
	}
	if value, ok := values["screen_clock_format"]; ok {
		applied["screen_clock_format"] = normalizeScreenClockFormat(value)
	}
	if value, ok := values["dnd_enabled"]; ok {
		applied["dnd_enabled"] = nativeBool(value, false)
	}
	if value, ok := values["dnd_schedule_enabled"]; ok {
		applied["dnd_schedule_enabled"] = nativeBool(value, false)
	}
	if value, ok := values["dnd_start"]; ok {
		applied["dnd_start"] = normalizeScreenClock(value, "22:00")
	}
	if value, ok := values["dnd_end"]; ok {
		applied["dnd_end"] = normalizeScreenClock(value, "07:00")
	}
	if value, ok := values["utc_offset_seconds"]; ok {
		offset := int(math.Round(nativeNumber(value, 0)))
		applied["utc_offset_seconds"] = max(-18*60*60, min(18*60*60, offset))
	}
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
	if value, ok := values["output_channel_mode"]; ok {
		mode := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
		if err := sendspinSetOutputChannel(mode); err != nil {
			return applied, err
		}
		applied["output_channel_mode"] = sendspinOutputChannel()
	}
	if err := applyUSBEthernetSetting(values, applied); err != nil {
		return applied, err
	}
	if err := applySSHSettings(values, applied); err != nil {
		return applied, err
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
	if value, ok := values["oww_threshold"]; ok {
		threshold := nativeNumber(value, 0)
		if threshold <= 0 || threshold > 1 {
			return applied, fmt.Errorf("oww_threshold must be in (0, 1]")
		}
		msg.OwwThreshold = &threshold
		applied["oww_threshold"] = threshold
	}
	if value, ok := values["oww_patience"]; ok {
		patience := int(math.Round(nativeNumber(value, 0)))
		if patience < 1 || patience > 20 {
			return applied, fmt.Errorf("oww_patience must be between 1 and 20")
		}
		msg.OwwPatience = &patience
		applied["oww_patience"] = patience
	}
	if value, ok := values["nww_threshold"]; ok {
		threshold := nativeNumber(value, 0)
		if threshold <= 0 || threshold > 1 {
			return applied, fmt.Errorf("nww_threshold must be in (0, 1]")
		}
		msg.NwwThreshold = &threshold
		applied["nww_threshold"] = threshold
	}
	if value, ok := values["nww_patience"]; ok {
		patience := int(math.Round(nativeNumber(value, 0)))
		if patience < 1 || patience > 20 {
			return applied, fmt.Errorf("nww_patience must be between 1 and 20")
		}
		msg.NwwPatience = &patience
		applied["nww_patience"] = patience
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
		localEnabled := engine == "micro_wake_word"
		mwwEnabled := localEnabled
		owwEnabled := false
		nwwEnabled := false
		if configured, present := values["wake_mww_enabled"]; present {
			mwwEnabled = localEnabled && nativeBool(configured, true)
		}
		if configured, present := values["wake_oww_enabled"]; present {
			owwEnabled = localEnabled && nativeBool(configured, false)
		}
		if configured, present := values["wake_nww_enabled"]; present {
			nwwEnabled = localEnabled && nativeBool(configured, false)
		}
		if owwEnabled && nwwEnabled {
			return applied, fmt.Errorf("openWakeWord and NanoWakeWord cannot be enabled together")
		}
		msg.MwwShadowEnabled = &mwwEnabled
		msg.OwwEnabled = &owwEnabled
		msg.NwwEnabled = &nwwEnabled
		applied["wake_engine"] = engine
		applied["wake_mww_enabled"] = mwwEnabled
		applied["wake_oww_enabled"] = owwEnabled
		applied["wake_nww_enabled"] = nwwEnabled
	} else {
		if value, ok := values["wake_mww_enabled"]; ok {
			enabled := nativeBool(value, true)
			msg.MwwShadowEnabled = &enabled
			applied["wake_mww_enabled"] = enabled
		}
		if value, ok := values["wake_oww_enabled"]; ok {
			enabled := nativeBool(value, false)
			msg.OwwEnabled = &enabled
			applied["wake_oww_enabled"] = enabled
		}
		if value, ok := values["wake_nww_enabled"]; ok {
			enabled := nativeBool(value, false)
			msg.NwwEnabled = &enabled
			applied["wake_nww_enabled"] = enabled
		}
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
			selectedMWWPackage = packageName
			applied["wake_word"] = "custom_url"
			applied["wake_word_url"] = strings.TrimSpace(fmt.Sprint(values["wake_word_url"]))
		} else if wakeWord != "" && wakeWord != "hey_tater" {
			return applied, fmt.Errorf("wake word %q is not installed on this Echo", wakeWord)
		} else {
			msg.MwwModel = "hey_tater"
			selectedMWWPackage = "hey_tater"
			applied["wake_word"] = "hey_tater"
		}
	}
	if value, ok := values["oww_wake_word"]; ok {
		wakeWord := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
		if wakeWord == "hey tater" {
			wakeWord = "hey_tater"
		}
		if wakeWord == "paired_bundle" {
			if !dualWakeRequested || selectedMWWPackage == "" {
				return applied, fmt.Errorf("paired OWW bundle requires dual mode and an installed MWW package")
			}
			pairedPackage, err := microwakeword.PairedCompanionPackage(selectedMWWPackage)
			if err != nil {
				return applied, err
			}
			msg.OwwModel = pairedPackage
			applied["oww_wake_word"] = "paired_bundle"
			applied["oww_wake_word_url"] = strings.TrimSpace(fmt.Sprint(values["oww_wake_word_url"]))
		} else if wakeWord == "custom_url" {
			packageName, err := microwakeword.InstallCompanionURLRevision(
				context.Background(),
				strings.TrimSpace(fmt.Sprint(values["oww_wake_word_url"])),
				strings.TrimSpace(fmt.Sprint(values["oww_model_revision"])),
			)
			if err != nil {
				return applied, err
			}
			msg.OwwModel = packageName
			applied["oww_wake_word"] = "custom_url"
			applied["oww_wake_word_url"] = strings.TrimSpace(fmt.Sprint(values["oww_wake_word_url"]))
		} else if wakeWord != "" && wakeWord != "hey_tater" {
			return applied, fmt.Errorf("openWakeWord %q is not installed on this Echo", wakeWord)
		} else {
			msg.OwwModel = "hey_tater"
			applied["oww_wake_word"] = "hey_tater"
		}
	}
	if value, ok := values["nww_wake_word"]; ok {
		wakeWord := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
		if wakeWord == "hey tater" {
			wakeWord = "hey_tater"
		}
		architecture := strings.ToLower(strings.TrimSpace(fmt.Sprint(values["nww_architecture"])))
		if architecture == "" {
			architecture = "cnn"
		}
		switch architecture {
		case "cnn", "gru":
		default:
			return applied, fmt.Errorf("unsupported NanoWakeWord architecture %q", architecture)
		}
		if wakeWord == "custom_url" {
			packageName, err := microwakeword.InstallNanoURLRevision(
				context.Background(),
				strings.TrimSpace(fmt.Sprint(values["nww_wake_word_url"])),
				strings.TrimSpace(fmt.Sprint(values["nww_model_revision"])),
				architecture,
			)
			if err != nil {
				return applied, err
			}
			msg.NwwModel = packageName
			msg.NwwArchitecture = architecture
			applied["nww_wake_word"] = "custom_url"
			applied["nww_wake_word_url"] = strings.TrimSpace(fmt.Sprint(values["nww_wake_word_url"]))
			applied["nww_architecture"] = architecture
		} else if wakeWord == "" || wakeWord == "hey_tater" {
			msg.NwwModel = microwakeword.DefaultPackage
			msg.NwwArchitecture = architecture
			applied["nww_wake_word"] = "hey_tater"
			applied["nww_architecture"] = architecture
		} else {
			return applied, fmt.Errorf("NanoWakeWord %q is not installed on this Echo", wakeWord)
		}
	} else if value, ok := values["nww_architecture"]; ok {
		architecture := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
		switch architecture {
		case "cnn", "gru":
			msg.NwwArchitecture = architecture
			applied["nww_architecture"] = architecture
		default:
			return applied, fmt.Errorf("unsupported NanoWakeWord architecture %q", architecture)
		}
	}
	if value, ok := values["barge_in_enabled"]; ok {
		enabled := nativeBool(value, true)
		applied["barge_in_enabled"] = enabled
	}
	if err := applyNativeOutputSettings(values, applied, &msg); err != nil {
		return applied, err
	}
	config.Get().Apply(msg)
	applyAecConfig(canceller, dc)
	return applied, nil
}

func normalizeDisplayTheme(value any) string {
	theme := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
	switch theme {
	case "tater", "ocean", "violet", "forest", "sunset":
		return theme
	default:
		return "tater"
	}
}

func normalizeWallpaperIntervalMinutes(value any) int {
	minutes := int(math.Round(nativeNumber(value, 1440)))
	switch minutes {
	case 0, 60, 360, 720, 1440:
		return minutes
	default:
		return 1440
	}
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
	tool, replying, music := nativeVisuals.tool, nativeVisuals.replying, nativeVisuals.music
	nativeVisuals.RUnlock()
	visual := func(name, fallback string) string {
		name = strings.ToLower(strings.TrimSpace(name))
		switch name {
		case "directional", "sparkle", "ping_pong", "audio_glow", "voice_ring", "spinner", "orbit",
			"pulse", "breathe", "comet", "dual_comet", "scanner", "ripple",
			"heartbeat", "theater", "wave", "shimmer", "twinkle", "equalizer", "solid",
			"music_pulse", "music_bars", "music_orbit", "music_wave", "off":
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
	case "speaking":
		return server.AnimSpec{Pattern: visual(replying, "audio_glow"), Colors: [][3]uint8{color}, PeriodMs: 50, TTLSec: 180}
	case "playing":
		return server.AnimSpec{Pattern: visual(music, "audio_glow"), Colors: [][3]uint8{color}, PeriodMs: 50, TTLSec: 180, Music: true}
	case "error":
		return server.AnimSpec{Pattern: "pulse", Colors: [][3]uint8{{200, 0, 0}}, PeriodMs: 38, TTLSec: 10}
	default:
		return server.AnimSpec{Pattern: "off"}
	}
}

func nativeMusicAnimation(primary, accent [3]uint8) server.AnimSpec {
	spec := nativeStateAnimation("playing")
	selected := brighterColor(primary, accent)
	if selected == ([3]uint8{}) {
		return spec
	}
	nativeVisuals.RLock()
	brightness := float64(nativeVisuals.brightness) / 100
	nativeVisuals.RUnlock()
	peak := max(selected[0], max(selected[1], selected[2]))
	if peak == 0 {
		return spec
	}
	// Album palettes are commonly intentionally dark. Normalize hue to the
	// user's LED brightness so the ring remains readable without replacing
	// the selected music animation.
	scale := 255 * brightness / float64(peak)
	for index := range selected {
		selected[index] = uint8(math.Min(255, math.Round(float64(selected[index])*scale)))
	}
	spec.Colors = [][3]uint8{selected}
	return spec
}

func brighterColor(a, b [3]uint8) [3]uint8 {
	brightness := func(value [3]uint8) int {
		return int(value[0])*299 + int(value[1])*587 + int(value[2])*114
	}
	if brightness(b) > brightness(a) {
		return b
	}
	return a
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
		return "tool_call"
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
	case "tool_call":
		return "Working on that now"
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

func showWeather(payload map[string]any) *show.Weather {
	if !nativeBool(payload["available"], false) {
		return nil
	}
	weather := &show.Weather{
		TemperatureText:       showPayloadText(payload, "temperature_text"),
		TemperatureUnit:       showPayloadText(payload, "temperature_unit"),
		IndoorTemperatureText: showPayloadText(payload, "indoor_temperature_text"),
		IndoorHumidityText:    showPayloadText(payload, "indoor_humidity_text"),
		Condition:             showPayloadText(payload, "condition"),
		ConditionKind:         showPayloadText(payload, "condition_kind"),
		Daylight:              showPayloadText(payload, "daylight"),
		FeelsLikeText:         showPayloadText(payload, "feels_like_text"),
		FeelsLikeRelation:     showPayloadText(payload, "feels_like_relation"),
		HumidityText:          showPayloadText(payload, "humidity_text"),
		WindText:              showPayloadText(payload, "wind_text"),
		RainText:              showPayloadText(payload, "rain_text"),
		LightningText:         showPayloadText(payload, "lightning_text"),
		Source:                showPayloadText(payload, "source"),
		Stale:                 nativeBool(payload["stale"], false),
	}
	if weather.TemperatureText == "" && weather.Condition == "" {
		return nil
	}
	return weather
}

func showClimate(payload map[string]any) *show.Climate {
	climate := &show.Climate{
		Available:          nativeBool(payload["available"], false),
		RoomID:             showPayloadText(payload, "room_id"),
		RoomName:           showPayloadText(payload, "room_name"),
		TemperatureUnit:    firstNonEmpty(showPayloadText(payload, "temperature_unit"), "F"),
		HVACMode:           showPayloadText(payload, "hvac_mode"),
		HVACAction:         showPayloadText(payload, "hvac_action"),
		CanSetTemperature:  nativeBool(payload["can_set_temperature"], false),
		CanSetMode:         nativeBool(payload["can_set_mode"], false),
		MinimumTemperature: nativeNumber(payload["minimum_temperature"], 45),
		MaximumTemperature: nativeNumber(payload["maximum_temperature"], 90),
		TemperatureStep:    nativeNumber(payload["temperature_step"], 1),
	}
	for _, field := range []struct {
		key string
		out **float64
	}{
		{"current_temperature", &climate.CurrentTemperature},
		{"target_temperature", &climate.TargetTemperature},
		{"current_humidity", &climate.CurrentHumidity},
	} {
		if payload[field.key] != nil {
			value := nativeNumber(payload[field.key], math.NaN())
			if !math.IsNaN(value) {
				*field.out = &value
			}
		}
	}
	if raw, ok := payload["available_hvac_modes"].([]any); ok {
		for _, item := range raw {
			if mode := strings.TrimSpace(fmt.Sprint(item)); mode != "" {
				climate.AvailableHVACModes = append(climate.AvailableHVACModes, mode)
			}
		}
	}
	if raw, ok := payload["room_temperatures"].([]any); ok {
		for _, item := range raw {
			row, ok := item.(map[string]any)
			if !ok || row["temperature"] == nil {
				continue
			}
			climate.RoomTemperatures = append(climate.RoomTemperatures, show.RoomTemperature{
				RoomID: showPayloadText(row, "room_id"), RoomName: showPayloadText(row, "room_name"),
				Temperature:     nativeNumber(row["temperature"], 0),
				TemperatureUnit: firstNonEmpty(showPayloadText(row, "temperature_unit"), climate.TemperatureUnit),
			})
		}
	}
	return climate
}

func showNotification(payload map[string]any) (*show.Notification, []byte, string) {
	var image []byte
	if encoded := showPayloadText(payload, "image_data_b64"); encoded != "" {
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			log.Printf("[show] ignored malformed notification image: %v", err)
		} else {
			image = decoded
		}
	}
	return &show.Notification{
		ID: showPayloadText(payload, "id"), Kind: showPayloadText(payload, "kind"),
		Label:    firstNonEmpty(showPayloadText(payload, "display_label"), showPayloadText(payload, "label")),
		Priority: showPayloadText(payload, "priority"), Title: showPayloadText(payload, "title"),
		CameraName:      showPayloadText(payload, "camera_name"),
		Description:     showPayloadText(payload, "description"),
		Source:          showPayloadText(payload, "source"),
		ImageFit:        showPayloadText(payload, "image_fit"),
		ImageURL:        showPayloadText(payload, "image_url"),
		CreatedAtUnixMS: int64(nativeNumber(payload["created_at"], 0) * 1000),
		ExpiresAtUnixMS: int64(nativeNumber(payload["expires_at"], 0) * 1000),
		TTLMS:           int64(nativeNumber(payload["ttl_seconds"], 0) * 1000),
	}, image, showPayloadText(payload, "image_content_type")
}

func showPayloadText(payload map[string]any, key string) string {
	value := strings.TrimSpace(fmt.Sprint(payload[key]))
	if value == "<nil>" {
		return ""
	}
	return value
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
const platformInitMarker = "TATER_PLATFORM_INIT_V1"

// platformInit applies the detected board's kernel tuning and exits. Tater Linux only:
// on FireOS the vendor's thermal_manager owns this. An unknown board is not a
// failure — it keeps the kernel defaults, which are the stricter setting.
func platformInit() int {
	fmt.Printf("platform-init (%s) base=%s", platformInitMarker, platform.Base())
	b := board.Detect("")
	fmt.Printf(" board=%s", board.IDOf(b))
	if !platform.Linux() {
		fmt.Println(" — not Linux, nothing to do")
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
