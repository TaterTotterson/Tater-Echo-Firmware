package server

import (
	"fmt"
	"log"
	"sync"
	"time"

	internalLed "github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/led"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/mixer"
	"github.com/TaterTotterson/Tater-Echo-Firmware/pkg/led"
)

type muteController struct {
	opMu              sync.Mutex
	mu                sync.Mutex
	muted             bool
	hardwareVerified  bool
	hardwareMuted     bool
	adcControls       map[string]string
	lastHardwareError string
	lastVisualError   string
	ledCtrl           func() led.Controller
	setButtonLED      func(bool) error
	// dotMuted is set externally to block dot button events while muted
	onMuteChange func(muted bool)
	// persist, when set, is called after every Toggle() so the mute state
	// survives reboots and OTA restarts (state.json). Separate from
	// onMuteChange: that one is the controller-notification hook wired by
	// cmd, this one is internal.
	persist func()
}

func newMuteController(ledGetter func() led.Controller, onMuteChange func(muted bool)) *muteController {
	return &muteController{
		ledCtrl:      ledGetter,
		setButtonLED: internalLed.SetMuteButtonLED,
		onMuteChange: onMuteChange,
	}
}

// SetOnMuteChange wires a callback invoked when mute state changes.
// B7 fix (2026-07-05 review): previously Server.SetMuteChangeCallback
// reached directly into m.mu/m.onMuteChange from outside this struct.
// Encapsulating the lock here keeps muteController responsible for its
// own synchronisation, matching every other muteController method.
func (m *muteController) SetOnMuteChange(cb func(muted bool)) {
	m.mu.Lock()
	m.onMuteChange = cb
	m.mu.Unlock()
}

func (m *muteController) IsMuted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.muted
}

const (
	muteHardwareAttempts   = 3
	muteHardwareRetryDelay = 25 * time.Millisecond
)

// Toggle changes the device-sovereign physical mute. Muting is fail-closed:
// the logical state and stream gate change before the codec write, so even a
// failed ADC control cannot leave audio going over the network. Unmuting is
// the reverse: every ADC control must read back unmuted before the logical
// state changes or the microphone pipeline is allowed to restart.
func (m *muteController) Toggle() {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	m.mu.Lock()
	currentlyMuted := m.muted
	if !currentlyMuted {
		m.muted = true
	}
	// Copy under the lock — SetOnMuteChange writes this field under mu from
	// the main goroutine, and button events can fire before that wiring
	// completes (SubscribeToButton starts the evdev goroutines first).
	cb := m.onMuteChange
	persist := m.persist
	m.mu.Unlock()

	if !currentlyMuted {
		// Stop capture first. Hardware mute is defence in depth; the transport
		// gate must not wait behind mixer retries.
		if persist != nil {
			persist()
		}
		if cb != nil {
			cb(true)
		}
		log.Println("Mute: mic muted")
		if err := m.applyADCMute(true); err != nil {
			log.Printf("Mute: hardware mute incomplete: %v", err)
		}
		m.applyVisualState(true)
		return
	}

	// Remain logically muted and keep capture stopped until the codec proves
	// every ADC channel is open. A failed attempt is safe and can be retried
	// with the same physical button.
	log.Println("Mute: mic unmute requested")
	if err := m.applyADCMute(false); err != nil {
		log.Printf("Mute: refusing to unmute because hardware did not verify: %v", err)
		m.applyVisualState(true)
		return
	}
	m.mu.Lock()
	m.muted = false
	cb = m.onMuteChange
	persist = m.persist
	m.mu.Unlock()
	if persist != nil {
		persist()
	}
	m.applyVisualState(false)
	if cb != nil {
		cb(false)
	}
	log.Println("Mute: mic unmuted and verified")
}

// restore applies the persisted state in both directions. emOS supervises and
// restarts the daemon without resetting the codec, so assuming that an
// unmuted process starts with unmuted ADCs can preserve stale hardware mute.
// If unmute cannot be verified, fail closed and expose a truthful muted state.
func (m *muteController) restore(muted bool) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	m.mu.Lock()
	m.muted = muted
	m.mu.Unlock()
	log.Printf("Mute: restoring persisted state muted=%t", muted)
	if err := m.applyADCMute(muted); err != nil {
		if !muted {
			m.mu.Lock()
			m.muted = true
			m.mu.Unlock()
			original := err
			if fallbackErr := m.applyADCMute(true); fallbackErr != nil {
				err = fmt.Errorf("unmute failed (%v); fail-safe mute also failed (%v)", original, fallbackErr)
			} else {
				err = fmt.Errorf("unmute failed; fail-safe mute engaged: %w", original)
			}
			m.noteHardwareError(err)
		}
		return err
	}
	return nil
}

// reconcile reasserts the logical state after a daemon/controller reconnect.
// It is deliberately idempotent and uses codec readback, not a successful
// write alone, as proof. An unverifiable unmute transitions to safe mute and
// notifies the stream owner so audio cannot continue under a false status.
func (m *muteController) reconcile() error {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	m.mu.Lock()
	muted := m.muted
	cb := m.onMuteChange
	persist := m.persist
	m.mu.Unlock()
	if err := m.applyADCMute(muted); err != nil {
		if !muted {
			m.mu.Lock()
			m.muted = true
			m.mu.Unlock()
			if persist != nil {
				persist()
			}
			if cb != nil {
				cb(true)
			}
			original := err
			if fallbackErr := m.applyADCMute(true); fallbackErr != nil {
				err = fmt.Errorf("unmute reconciliation failed (%v); fail-safe mute also failed (%v)", original, fallbackErr)
			} else {
				err = fmt.Errorf("unmute reconciliation failed; fail-safe mute engaged: %w", original)
			}
			m.noteHardwareError(err)
			muted = true
		}
		m.applyVisualState(muted)
		return err
	}
	m.applyVisualState(muted)
	return nil
}

func (m *muteController) applyADCMute(wantMuted bool) error {
	var lastErr error
	for attempt := 1; attempt <= muteHardwareAttempts; attempt++ {
		writeFailures := mixer.SetADCMute(wantMuted)
		actual, consistent, values, readErr := mixer.ADCMuteState()
		if readErr == nil && consistent && actual == wantMuted {
			m.recordHardwareState(true, actual, values, "")
			if attempt > 1 {
				log.Printf("Mute: ADC state recovered on attempt %d", attempt)
			}
			return nil
		}
		switch {
		case readErr != nil:
			lastErr = fmt.Errorf("attempt %d: %d write failure(s), readback failed: %w", attempt, writeFailures, readErr)
		case !consistent:
			lastErr = fmt.Errorf("attempt %d: %d write failure(s), ADC mute controls disagree", attempt, writeFailures)
		default:
			lastErr = fmt.Errorf("attempt %d: %d write failure(s), readback muted=%t want=%t", attempt, writeFailures, actual, wantMuted)
		}
		m.recordHardwareState(false, actual, values, lastErr.Error())
		if attempt < muteHardwareAttempts {
			time.Sleep(muteHardwareRetryDelay)
		}
	}
	return lastErr
}

func (m *muteController) recordHardwareState(verified, muted bool, values map[string]string, message string) {
	copyValues := make(map[string]string, len(values))
	for key, value := range values {
		copyValues[key] = value
	}
	m.mu.Lock()
	m.hardwareVerified = verified
	m.hardwareMuted = muted
	m.adcControls = copyValues
	m.lastHardwareError = message
	m.mu.Unlock()
}

func (m *muteController) noteHardwareError(err error) {
	if err == nil {
		return
	}
	m.mu.Lock()
	m.lastHardwareError = err.Error()
	m.mu.Unlock()
}

func (m *muteController) status() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	controls := make(map[string]string, len(m.adcControls))
	for key, value := range m.adcControls {
		controls[key] = value
	}
	return map[string]any{
		"muted":             m.muted,
		"hardware_verified": m.hardwareVerified,
		"hardware_muted":    m.hardwareMuted,
		"adc_controls":      controls,
		"last_error":        m.lastHardwareError,
		"visual_error":      m.lastVisualError,
	}
}

// reconcileVisual paints the current logical state while serialized against
// button and reconnect operations. The LED controller appears asynchronously
// during boot, so this closes the window where its delayed initialization
// could otherwise repaint a state that has just changed.
func (m *muteController) reconcileVisual() {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.applyVisualState(m.IsMuted())
}

// restoreMutedVisual is the guarded reconnect repaint. Keeping the muted
// check under opMu prevents a simultaneous physical unmute from being
// followed by a stale red frame.
func (m *muteController) restoreMutedVisual() {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if !m.IsMuted() {
		return
	}
	m.applyVisualState(true)
}

// applyVisualState reconciles both mute indicators. Biscuit has a discrete
// red LED under the mic-off button as well as the ring; targets without that
// GPIO can leave setButtonLED nil.
func (m *muteController) applyVisualState(muted bool) {
	var ringErr error
	if muted {
		ringErr = m.showMuteLEDs()
	} else {
		ringErr = m.clearLEDs()
	}
	var buttonErr error
	if m.setButtonLED != nil {
		buttonErr = retryMuteHardware(func() error { return m.setButtonLED(muted) })
	}
	var visualErr error
	switch {
	case ringErr != nil && buttonErr != nil:
		visualErr = fmt.Errorf("ring: %v; mute button: %v", ringErr, buttonErr)
	case ringErr != nil:
		visualErr = ringErr
	case buttonErr != nil:
		visualErr = buttonErr
	}
	m.mu.Lock()
	if visualErr != nil {
		m.lastVisualError = visualErr.Error()
	} else {
		m.lastVisualError = ""
	}
	m.mu.Unlock()
	if visualErr != nil {
		log.Printf("Mute visual reconciliation failed: %v", visualErr)
	}
}

func retryMuteHardware(operation func() error) error {
	var err error
	for attempt := 1; attempt <= muteHardwareAttempts; attempt++ {
		if err = operation(); err == nil {
			return nil
		}
		if attempt < muteHardwareAttempts {
			time.Sleep(muteHardwareRetryDelay)
		}
	}
	return err
}

func (m *muteController) showMuteLEDs() error {
	if m.ledCtrl == nil {
		return nil
	}
	lc := m.ledCtrl()
	if lc == nil {
		return nil
	}
	leds := make([]led.Led, numLEDs)
	for i := 0; i < numLEDs; i++ {
		leds[i] = led.Led{ID: i, R: 180, G: 0, B: 0} // red ring
	}
	return retryMuteHardware(func() error { return lc.SetLEDs(leds...) })
}

func (m *muteController) clearLEDs() error {
	if m.ledCtrl == nil {
		return nil
	}
	lc := m.ledCtrl()
	if lc == nil {
		return nil
	}
	leds := make([]led.Led, numLEDs)
	for i := range leds {
		leds[i].ID = i
	}
	return retryMuteHardware(func() error { return lc.SetLEDs(leds...) })
}
