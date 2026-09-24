package server

import (
	"log"
	"math"
	"sync"
	"sync/atomic"
	"time"

	internalLed "github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/led"
	"github.com/TaterTotterson/Tater-Echo-Firmware/pkg/buttons"
	"github.com/TaterTotterson/Tater-Echo-Firmware/pkg/led"
	"github.com/TaterTotterson/Tater-Echo-Firmware/pkg/mic"
	"github.com/TaterTotterson/Tater-Echo-Firmware/pkg/speaker"
	"golang.org/x/sys/unix"
)

// ledMode controls which subsystem currently owns the LED ring.
// Higher value = higher priority.
type ledMode int

const (
	ledModeDirection ledMode = iota // beamformer arc — lowest priority
	ledModeSystem                   // controller/mute/pulse — highest priority
)

type Server struct {
	ledController    led.Controller
	ledMu            sync.Mutex
	buttonController buttons.Controller
	mic              mic.Microphone
	speaker          speaker.Speaker
	volume           *volumeController
	mute             *muteController

	ledModeMu sync.Mutex
	ledMode   ledMode

	// baseLEDs stores the controller-set ring state so direction overlay
	// can always be applied fresh on top without accumulating.
	baseLEDs   [12]led.Led
	baseLEDsMu sync.Mutex

	// listeningLEDs is true when the controller has set the solid green
	// listening ring — the only state where direction overlay is shown.
	listeningLEDs bool

	// anim owns the device-rendered ring animation (led_anim messages).
	anim animator

	// linkDown is true while there is no controller session — disconnected
	// or pending approval. Read on the LED paint path and the button paths,
	// written from the connection callbacks; atomic rather than mutexed
	// because it is read per pulse frame (every 50ms) and never in a
	// compound decision with anything else it guards. See SetLinkDown.
	linkDown atomic.Bool

	// setupFeedback temporarily owns the ring while the local six-press
	// recovery gesture is in progress. Controller animations keep updating
	// baseLEDs underneath it and resume as soon as the gesture is cancelled.
	setupFeedback atomic.Bool

	// audioLevel holds the live speaker RMS as float64 bits — written by
	// the speaker's ALSA pump via SetAudioLevel, read by the meter anim.
	audioLevel atomic.Uint64

	// directionAngle is the most recent beamformer bearing. Listening uses
	// it for the directional marker; voice_ring reuses it so a response can
	// bloom from the direction in which the user spoke.
	directionAngle atomic.Uint64
	directionKnown atomic.Bool
	// directionPosition is the smoothed LED-space bearing used only for the
	// listening visual. Guarded by baseLEDsMu with the base frame it paints.
	directionPosition      float64
	directionPositionKnown bool
	// directionRenderMu keeps the audio callback and display ticker from
	// completing adjacent eased frames out of order at the hardware boundary.
	directionRenderMu sync.Mutex
	// directionTarget is the latest acoustic bearing in fractional LED space.
	// A 30fps renderer eases directionPosition toward it so the beam moves like
	// the emOS boot head instead of jumping once per microphone observation.
	directionTarget      float64
	directionTargetKnown bool
	// lastSpeechPosition is the acoustic bearing from the most recent period
	// classified as near-end speech. Trailing silence/noise may move the live
	// listening beam but cannot choose the subsequent reply animation.
	lastSpeechPosition      float64
	lastSpeechPositionKnown bool
	directionSpeechStarted  bool
	replyDirection          int
	replyDirectionKnown     bool

	// volumeSeeded is true once the device has an authoritative volume this
	// run: seeded from the controller's stored startupVolume on the first
	// config push (SeedVolume), or set by any button press / volume_set
	// before that. While false, the device must not report volume_state —
	// the controller persists every report into startupVolume, and a boot-
	// default report would clobber the saved value (the reboot-reset bug).
	volumeSeeded atomic.Bool
}

func NewServer(buttonController buttons.Controller, microphone mic.Microphone, speaker speaker.Speaker) *Server {
	server := &Server{
		buttonController: buttonController,
		mic:              microphone,
		speaker:          speaker,
	}

	// Volume controller uses a getter so it handles the nil-during-boot window safely
	server.volume = newVolumeController(func() led.Controller {
		server.ledMu.Lock()
		defer server.ledMu.Unlock()
		return server.ledController
	})

	// Mute controller — same LED getter pattern
	server.mute = newMuteController(func() led.Controller {
		server.ledMu.Lock()
		defer server.ledMu.Unlock()
		return server.ledController
	}, nil)

	// Give volume controller access to mute state so it can restore the red ring
	server.volume.isMuted = func() bool {
		return server.mute.IsMuted()
	}

	// When the volume arc's display window ends, hand the ring back to the
	// last controller-set state (listening/thinking/playing mid-turn, all
	// off when idle). SetLEDs keeps recording frames into baseLEDs during
	// the window — it just doesn't paint them — so this repaint lands on
	// the current animation frame, not a stale one.
	server.volume.onDisplayExpire = func() {
		server.paintBaseLEDs()
	}

	// Restore persisted mute state before the persist hook is wired, so the
	// restore itself doesn't rewrite the file. Mute is device-sovereign —
	// it must come back with or without a controller.
	if st, ok := loadDeviceState(statePath); ok && st.Muted {
		server.mute.RestoreMuted() // ADC only; LEDs painted after init below
	}
	server.mute.persist = func() {
		saveDeviceState(statePath, deviceState{Muted: server.mute.IsMuted()})
	}

	// Audio direction estimates arrive in comparatively coarse batches. Keep
	// the visual on its own 30fps clock so movement between those observations
	// is continuous rather than a sequence of LED-sized jumps.
	go server.runDirectionAnimator()

	go func() {
		uptime, err := getUptime()
		// Reduced from 90 seconds as server is started at the end of the boot cycle anyway.
		minUptime := time.Second * 5

		if err != nil || uptime < minUptime {
			// If we start too soon the native bootup from the echo will break (LEDs will spin forever)
			stillWait := minUptime - uptime
			log.Printf("Uptime is currently at %0.2fs, waiting %0.2fs for LED setup\n", uptime.Seconds(), stillWait.Seconds())
			time.Sleep(stillWait)
		}

		ledController, err := internalLed.NewDefaultController()
		if err != nil {
			log.Fatalf("Failed to initialize LED controller: %v", err)
		}

		server.ledMu.Lock()
		server.ledController = ledController
		server.ledMu.Unlock()
		clearLeds(ledController)

		// Discrete red LED under the mic-off button (GPIO, separate from
		// the ring) — export + off. Non-fatal: an unmuted boot without a
		// button LED is cosmetic, everything else still works.
		if err := internalLed.InitMuteButtonLED(); err != nil {
			log.Printf("Mute button LED init failed: %v", err)
		}

		// A muted state restored from state.json was applied to the ADC
		// before the LED hardware was ready — paint the red ring and
		// button LED now.
		if server.mute.IsMuted() {
			server.mute.showMuteLEDs()
			setMuteButtonLED(true)
		}
	}()

	return server
}

// VolumeStepUp increases volume one step — called by button handler.
// A button press makes the device's level authoritative (see volumeSeeded):
// its change report updates the controller's stored value, and a config
// push arriving later this run must not override it.
func (s *Server) VolumeStepUp() {
	s.volumeSeeded.Store(true)
	s.volume.StepUp()
}

// VolumeStepDown decreases volume one step — called by button handler.
func (s *Server) VolumeStepDown() {
	s.volumeSeeded.Store(true)
	s.volume.StepDown()
}

// SetVolume sets volume to an explicit level (0–volumeMax) — called by controller
// command. Remote changes don't paint the volume arc: nobody is at the
// device, and the ring lighting up unprompted reads as a glitch.
func (s *Server) SetVolume(level int) {
	s.volumeSeeded.Store(true)
	s.volume.Set(level, false)
}

// SeedVolume restores the controller's stored startupVolume — the source of
// truth for volume, kept current by the volume_state echo — on the first
// config push of each run. Applying it on *every* push would race a live
// volume change against a stale config snapshot, and going through Set()
// (rather than the raw tinymix write this replaced) keeps the recorded
// level, HA entity, and hardware in agreement.
func (s *Server) SeedVolume(level int) {
	if s.volumeSeeded.Swap(true) {
		return
	}
	log.Printf("Seeding volume from controller startupVolume=%d", level)
	s.volume.Set(level, false)
}

// VolumeSeeded reports whether the device has an authoritative volume this
// run. Until it does, the connect-time volume_state report is suppressed —
// see the volumeSeeded field comment.
func (s *Server) VolumeSeeded() bool {
	return s.volumeSeeded.Load()
}

// VolumeLevel returns the current volume level (0–volumeMax).
func (s *Server) VolumeLevel() int {
	return s.volume.Get()
}

// SetVolumeChangeCallback wires a callback invoked when volume changes.
// The callback receives the new level (0–volumeMax).
func (s *Server) SetVolumeChangeCallback(cb func(level int)) {
	s.volume.SetOnVolumeChange(cb)
}

// MuteToggle toggles mic mute state — called by button handler.
func (s *Server) MuteToggle() {
	s.mute.Toggle()
}

// SetMuteChangeCallback wires a callback invoked when mute state changes.
func (s *Server) SetMuteChangeCallback(cb func(muted bool)) {
	s.mute.SetOnMuteChange(cb)
}

// IsMuted returns true when the mic is muted — used to block dot button.
func (s *Server) IsMuted() bool {
	return s.mute.IsMuted()
}

// CancelVolumeDisplay releases the volume arc's 2s hold on the ring so a
// turn's listening frame can paint immediately. See volumeController.
func (s *Server) CancelVolumeDisplay() {
	s.volume.CancelDisplay()
}

// RestoreMuteRing re-applies the red mute ring IF the device is muted. Called
// on reconnect to recover the visual state that the orange pulse animation
// overwrote.
//
// The guard is the whole function. Without it this painted a full red ring on
// every reconnect regardless of mute state — and a device pending approval
// reconnects repeatedly, so the pending pulse was interrupted by a red flash
// every time, on a device whose mic was not muted. Seen on 3611NF's first emOS
// boot, 2026-09-06, and read as a fault indicator, which is exactly what a red
// ring means to anyone looking at it.
//
// Every other painter of this ring already checks: server.go's startup path
// gates on IsMuted(), and volume.go's display-expiry gates on isMuted(). This
// was the one that did not.
func (s *Server) RestoreMuteRing() {
	if !s.mute.IsMuted() {
		return
	}
	s.mute.showMuteLEDs()
}

// SetLinkDown records whether the device currently has a controller session.
// True while disconnected AND while pending approval — both mean the same
// thing to everything that reads it: there is nothing above this device.
//
// It governs two things:
//
//   - The RING belongs to the link state. The mute ring is otherwise
//     device-sovereign and suppresses every other paint, which made a muted
//     device that lost its controller sit there showing red — a state that
//     is not merely less useful than the orange pulse, it is wrong, because
//     red says "muted and working". The pulse now paints through mute.
//     RestoreMuteRing on reconnect has always expected exactly this ("orange
//     pulse overwrote the red ring — restore it"); the suppression it was
//     written against is what stopped the pulse ever painting.
//   - The action and volume BUTTONS are inert. Neither can do anything
//     without a controller — the dot cannot start a turn and volume changes
//     a level nothing is playing at — so acknowledging them with a ring
//     flash or an arc invents a working device.
//
// The MUTE button stays live, deliberately. It is the one control that
// works with no controller at all: the ADC mute is hardware and the button
// LED is a GPIO, neither needs the network. Making it inert would also mean
// a user who mutes during an outage gets a live mic back when the controller
// returns, since mute is persisted — a silent privacy surprise, which is
// worse than a ring showing the wrong colour.
func (s *Server) SetLinkDown(down bool) {
	s.linkDown.Store(down)
}

// LinkDown reports whether the device is without a controller session.
func (s *Server) LinkDown() bool {
	return s.linkDown.Load()
}

func clearLeds(ledController led.Controller) {
	numLEDs, err := ledController.GetNumLEDs()
	if err != nil {
		log.Printf("clearLeds: failed to get LED count: %v", err)
		return
	}

	leds := make([]led.Led, numLEDs)
	for i := 0; i < numLEDs; i++ {
		leds[i] = led.Led{
			ID: i,
			R:  0,
			G:  0,
			B:  0,
		}
	}
	if err = ledController.SetLEDs(leds...); err != nil {
		log.Printf("clearLeds: failed to set LEDs: %v", err)
	}
}

func getUptime() (time.Duration, error) {
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil {
		return time.Duration(0), err
	}
	return time.Second * time.Duration(info.Uptime), nil
}

// SetLEDMode sets the current LED priority mode.
func (s *Server) SetLEDMode(m ledMode) {
	s.ledModeMu.Lock()
	s.ledMode = m
	s.ledModeMu.Unlock()
}

// LEDModeSystem claims the LED ring for system use.
func (s *Server) LEDModeSystem() { s.SetLEDMode(ledModeSystem) }

// LEDModeDirection releases the LED ring back to the beamformer arc.
func (s *Server) LEDModeDirection() { s.SetLEDMode(ledModeDirection) }

// SetDirectionLEDs overlays a direction marker and treats it as speech. Kept
// as the simple API for callers/tests without VAD metadata.
func (s *Server) SetDirectionLEDs(angleDeg float64) {
	s.SetDirectionObservation(angleDeg, true, true)
}

// SetDirectionObservation overlays a live DOA marker. activity is a lenient
// acoustic threshold used to leave the neutral glow; speech is the stricter
// turn-gate decision used only for reply-direction memory.
func (s *Server) SetDirectionObservation(angleDeg float64, activity, speech bool) {
	if angleDeg < 0 {
		return
	}
	if s.setupFeedback.Load() {
		return
	}
	// Same paint suppressions as SetLEDs: the volume arc owns the ring for
	// its display window, and the mute ring is device-sovereign.
	if (s.volume != nil && s.volume.DisplayActive()) || (s.mute != nil && s.mute.IsMuted()) {
		return
	}

	s.baseLEDsMu.Lock()
	if !s.listeningLEDs {
		s.baseLEDsMu.Unlock()
		return
	}
	// Match the other native satellites: the solid listening colour owns the
	// ring until near-end speech is heard. During pauses, hold the last beam
	// instead of letting room noise make it wander.
	if !activity {
		s.baseLEDsMu.Unlock()
		return
	}
	s.directionSpeechStarted = true
	const ledOffset = 240.0
	target := math.Mod(angleDeg-ledOffset+360, 360) / 30
	if !s.directionPositionKnown {
		s.directionPosition = target
		s.directionPositionKnown = true
		s.directionTarget = target
		s.directionTargetKnown = true
	} else {
		s.directionTarget = target
		s.directionTargetKnown = true
	}
	if speech {
		// Remember the acoustic target, not the deliberately lagging visual.
		// A short phrase can end before the eased head arrives, but its reply
		// should still point at the place the voice was actually measured.
		s.lastSpeechPosition = target
		s.lastSpeechPositionKnown = true
	}
	s.baseLEDsMu.Unlock()
	s.directionAngle.Store(math.Float64bits(angleDeg))
	s.directionKnown.Store(true)
	s.renderDirectionFrame()
}

const (
	directionFrameInterval = 33 * time.Millisecond
	directionFollow        = 0.28
	directionMaxStep       = 0.50
	directionSnapDistance  = 0.02
)

// smoothRingPosition takes one display-rate step along the shortest path
// around a twelve-segment ring. The exponential follow gives the movement the
// same settle-in character as the boot progress head, while the speed cap
// prevents a single noisy estimate from whipping across the ring.
func smoothRingPosition(current, target float64) float64 {
	delta := target - current
	if delta > 6 {
		delta -= 12
	} else if delta < -6 {
		delta += 12
	}
	if math.Abs(delta) <= directionSnapDistance {
		return math.Mod(target+12, 12)
	}
	step := delta * directionFollow
	if step > directionMaxStep {
		step = directionMaxStep
	} else if step < -directionMaxStep {
		step = -directionMaxStep
	}
	return math.Mod(current+step+12, 12)
}

func (s *Server) runDirectionAnimator() {
	ticker := time.NewTicker(directionFrameInterval)
	defer ticker.Stop()
	for range ticker.C {
		s.renderDirectionFrame()
	}
}

// renderDirectionFrame advances and paints one smooth DOA frame. It keeps
// painting while the bearing is steady as well: controller scene frames can
// refresh the base listening colour, and the local overlay must remain on top.
func (s *Server) renderDirectionFrame() {
	s.directionRenderMu.Lock()
	defer s.directionRenderMu.Unlock()

	if s.setupFeedback.Load() {
		return
	}
	if (s.volume != nil && s.volume.DisplayActive()) || (s.mute != nil && s.mute.IsMuted()) {
		return
	}

	s.baseLEDsMu.Lock()
	if !s.listeningLEDs || !s.directionPositionKnown || !s.directionTargetKnown {
		s.baseLEDsMu.Unlock()
		return
	}
	s.directionPosition = smoothRingPosition(s.directionPosition, s.directionTarget)
	position := s.directionPosition
	base := s.baseLEDs
	s.baseLEDsMu.Unlock()

	s.ledMu.Lock()
	lc := s.ledController
	s.ledMu.Unlock()
	if lc == nil {
		return
	}

	leds := directionalFrame(base, position)

	if err := lc.SetLEDs(leds...); err != nil {
		log.Printf("direction LED frame error: %v", err)
	}
}

// directionalFrame turns the selected listening colour into a narrow beam:
// a warm-white tip at the measured bearing, two coloured shoulder pixels,
// and only a dim ownership glow elsewhere. Unlike the previous three-pixel
// highlight over a fully lit ring, the direction is legible across the room.
func directionalFrame(base [12]led.Led, position float64) []led.Led {
	frame := make([]led.Led, 12)
	for i := range frame {
		pixel := base[i]
		brightness := math.Max(float64(pixel.R), math.Max(float64(pixel.G), float64(pixel.B))) / 255
		distance := ringDistance(float64(i), position)
		beam := math.Max(0, 1-distance/3.2)
		colorLevel := 0.035 + beam*0.55
		tip := math.Max(0, 1-distance/1.15) * 0.72 * brightness
		frame[i] = led.Led{
			ID: i,
			R:  clampChannel(float64(pixel.R)*colorLevel + 255*tip),
			G:  clampChannel(float64(pixel.G)*colorLevel + 240*tip),
			B:  clampChannel(float64(pixel.B)*colorLevel + 170*tip),
		}
	}
	return frame
}

func (s *Server) directionLEDIndex() int {
	s.baseLEDsMu.Lock()
	if s.replyDirectionKnown {
		index := s.replyDirection
		s.baseLEDsMu.Unlock()
		return index
	}
	if s.directionPositionKnown {
		index := int(math.Floor(s.directionPosition+0.5)) % 12
		s.baseLEDsMu.Unlock()
		return index
	}
	s.baseLEDsMu.Unlock()
	if !s.directionKnown.Load() {
		return 0
	}
	const ledOffset = 240
	angle := math.Float64frombits(s.directionAngle.Load())
	normalized := int(math.Round(angle/30)) * 30
	return ((normalized - ledOffset + 360) % 360) / 30 % 12
}

// Direction returns the latest live beamformer bearing.
func (s *Server) Direction() (float64, bool) {
	if !s.directionKnown.Load() {
		return 0, false
	}
	return math.Float64frombits(s.directionAngle.Load()), true
}

// ClearDirection marks the previous turn's bearing stale and resets the
// smoothing anchor so the next person is shown immediately rather than
// animating around the ring from an old room position.
func (s *Server) ClearDirection() {
	s.directionKnown.Store(false)
	s.baseLEDsMu.Lock()
	s.directionPositionKnown = false
	s.directionTargetKnown = false
	s.directionTarget = 0
	s.lastSpeechPosition = 0
	s.lastSpeechPositionKnown = false
	s.directionSpeechStarted = false
	s.replyDirectionKnown = false
	s.replyDirection = 0
	s.baseLEDsMu.Unlock()
}

// endDirectionalListening freezes the accumulated turn direction without
// clearing it. Reply animations consume that memory after listening ends.
func (s *Server) endDirectionalListening() {
	s.baseLEDsMu.Lock()
	if s.lastSpeechPositionKnown {
		s.replyDirection = int(math.Floor(s.lastSpeechPosition+0.5)) % 12
		s.replyDirectionKnown = true
	} else if s.directionPositionKnown {
		// Fail open for rooms where the local VAD never asserts: preserve the
		// direction the user actually saw rather than defaulting to LED zero.
		s.replyDirection = int(math.Floor(s.directionPosition+0.5)) % 12
		s.replyDirectionKnown = true
	}
	s.listeningLEDs = false
	s.baseLEDsMu.Unlock()
}

// SetLEDs applies LED state directly — called by the controller client.
//
// Two conditions suppress the hardware paint (state is still recorded in
// baseLEDs so the ring can be restored later):
//   - volume display window: the turn animations repaint continuously, so
//     without this the volume arc survives ~one frame and reads as a
//     glitch. The window's expiry repaints baseLEDs (see onDisplayExpire).
//   - muted: the red ring is device-sovereign. Turns could not previously
//     overlap mute (mic stopped), but mute-terminates-turn (2026-07-10)
//     means the cancelled turn's LED cleanup arrives after the red ring
//     is up — it must not clear it. Unmute clears the ring explicitly.
//
// listeningHint is the controller's explicit "this frame is the listening
// ring" flag (nil from pre-scene controllers). When absent, fall back to
// the historical heuristic — a 12-LED all-green frame — which only works
// for the standard scene.
func (s *Server) SetLEDs(leds []led.Led, listeningHint *bool) {
	s.LEDModeSystem()
	var listeningRing bool
	if listeningHint != nil {
		listeningRing = *listeningHint
	} else {
		listeningRing = len(leds) == 12
		if listeningRing {
			for _, l := range leds {
				if l.R != 0 || l.B != 0 || l.G == 0 {
					listeningRing = false
					break
				}
			}
		}
	}
	s.baseLEDsMu.Lock()
	wasListening := s.listeningLEDs
	for _, l := range leds {
		if l.ID >= 0 && l.ID < 12 {
			s.baseLEDs[l.ID] = l
		}
	}
	s.listeningLEDs = listeningRing
	if listeningRing && !wasListening {
		// A new listening state is a new turn, including the microphone reopen
		// after a continued-chat reply. Start it without the previous user's
		// bearing; the first fresh DOA sample paints immediately.
		s.directionKnown.Store(false)
		s.directionPositionKnown = false
		s.directionTargetKnown = false
		s.directionTarget = 0
		s.lastSpeechPosition = 0
		s.lastSpeechPositionKnown = false
		s.directionSpeechStarted = false
		s.replyDirectionKnown = false
		s.replyDirection = 0
	}
	s.baseLEDsMu.Unlock()
	if s.setupFeedback.Load() {
		return
	}
	if suppressPaint(s.volume.DisplayActive(), s.mute.IsMuted(), s.LinkDown()) {
		return
	}
	s.paintBaseLEDs()
}

// suppressPaint decides whether a ring paint is held back. Pure, so the
// truth table can be tested without hardware — the same reason the
// controller keeps its link-auth and barge decisions out of their callers.
//
//   - volumeActive: the arc owns the ring for its 2s window against
//     animations, which repaint ~every 100ms and would stomp it in one frame.
//   - muted: the red ring is device-sovereign, so a cancelled turn's LED
//     cleanup cannot clear it.
//   - linkDown: ...unless there is no controller, in which case the ring
//     belongs to the link pulse. Red on a device with nothing above it is
//     not less informative than orange, it is false — it says "muted and
//     working". The mute BUTTON's own LED keeps reporting the mic, so
//     nothing about the mute state stops being visible.
func suppressPaint(volumeActive, muted, linkDown bool) bool {
	if volumeActive {
		return true
	}
	return muted && !linkDown
}

// paintBaseLEDs paints the ring from the stored controller state.
func (s *Server) paintBaseLEDs() {
	if s.setupFeedback.Load() {
		return
	}
	s.ledMu.Lock()
	lc := s.ledController
	s.ledMu.Unlock()
	if lc == nil {
		return
	}
	s.baseLEDsMu.Lock()
	base := s.baseLEDs
	s.baseLEDsMu.Unlock()
	leds := make([]led.Led, len(base))
	for i := range base {
		leds[i] = base[i]
		leds[i].ID = i
	}
	if err := lc.SetLEDs(leds...); err != nil {
		log.Printf("SetLEDs error: %v", err)
	}
}

// ShowSetupResetClicks paints the five-click arming progress locally. It does
// not overwrite baseLEDs, so cancelling the sequence restores the current
// controller animation instead of a stale idle frame.
func (s *Server) ShowSetupResetClicks(count, total int) {
	if total <= 0 {
		return
	}
	count = max(0, min(count, total))
	lit := (count*12 + total - 1) / total
	s.setupFeedback.Store(true)
	s.paintSetupFeedback(lit, led.Led{R: 255, G: 72, B: 0})
}

// ShowSetupResetCountdown drains the orange ring during the sixth hold.
func (s *Server) ShowSetupResetCountdown(remaining, total int) {
	if total <= 0 {
		return
	}
	remaining = max(0, min(remaining, total))
	lit := (remaining*12 + total - 1) / total
	s.setupFeedback.Store(true)
	s.paintSetupFeedback(lit, led.Led{R: 255, G: 72, B: 0})
}

// ShowSetupResetSuccess confirms the reset before emOS reboots into its white
// setup-mode animation and Tater-Setup-XXXX hotspot.
func (s *Server) ShowSetupResetSuccess() {
	s.setupFeedback.Store(true)
	s.paintSetupFeedback(12, led.Led{R: 42, G: 220, B: 132})
}

// ClearSetupResetFeedback hands the ring back to mute/controller/link state.
func (s *Server) ClearSetupResetFeedback() {
	if !s.setupFeedback.Swap(false) {
		return
	}
	if s.mute != nil && s.mute.IsMuted() && !s.LinkDown() {
		s.RestoreMuteRing()
		return
	}
	s.paintBaseLEDs()
}

func (s *Server) paintSetupFeedback(lit int, color led.Led) {
	s.ledMu.Lock()
	lc := s.ledController
	s.ledMu.Unlock()
	if lc == nil {
		return
	}
	frame := make([]led.Led, 12)
	for i := range frame {
		frame[i] = led.Led{ID: i, R: 4, G: 1, B: 0}
		if i < lit {
			frame[i].R, frame[i].G, frame[i].B = color.R, color.G, color.B
		}
	}
	if err := lc.SetLEDs(frame...); err != nil {
		log.Printf("setup feedback LEDs: %v", err)
	}
}
