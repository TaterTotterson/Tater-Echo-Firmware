package server

import (
	"errors"
	"sync"
	"testing"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/mixer"
	pkgled "github.com/TaterTotterson/Tater-Echo-Firmware/pkg/led"
)

var biscuitADCMuteControls = []string{
	"ADC_A Left Mute", "ADC_A Right Mute",
	"ADC_B Left Mute", "ADC_B Right Mute",
	"ADC_C Left Mute", "ADC_C Right Mute",
	"ADC_D Left Mute", "ADC_D Right Mute",
}

type muteMixerFake struct {
	mu         sync.Mutex
	values     map[string]string
	failWrites int
	sets       int
}

func newMuteMixerFake(muted bool) *muteMixerFake {
	value := "Off"
	if muted {
		value = "On"
	}
	values := make(map[string]string, len(biscuitADCMuteControls))
	for _, control := range biscuitADCMuteControls {
		values[control] = value
	}
	return &muteMixerFake{values: values}
}

func (f *muteMixerFake) Set(name string, values []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sets++
	if f.failWrites > 0 {
		f.failWrites--
		return errors.New("injected mixer write failure")
	}
	if len(values) != 1 {
		return errors.New("test mute control expects one value")
	}
	if values[0] == "1" || values[0] == "On" {
		f.values[name] = "On"
	} else {
		f.values[name] = "Off"
	}
	return nil
}

func (f *muteMixerFake) Get(name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	value, ok := f.values[name]
	if !ok {
		return "", errors.New("unknown mixer control")
	}
	return value, nil
}

type muteLEDFake struct {
	mu        sync.Mutex
	failures  int
	setCalls  int
	lastFrame []pkgled.Led
}

func (f *muteLEDFake) Init() error              { return nil }
func (f *muteLEDFake) GetNumLEDs() (int, error) { return 12, nil }
func (f *muteLEDFake) SetLEDs(frame ...pkgled.Led) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls++
	if f.failures > 0 {
		f.failures--
		return errors.New("injected LED write failure")
	}
	f.lastFrame = append([]pkgled.Led(nil), frame...)
	return nil
}

func testMuteController(ring *muteLEDFake) *muteController {
	m := newMuteController(func() pkgled.Controller { return ring }, nil)
	m.setButtonLED = func(bool) error { return nil }
	return m
}

func useMuteMixer(t *testing.T, backend *muteMixerFake) {
	t.Helper()
	mixer.ConfigureTarget("biscuit")
	mixer.Use(backend)
	t.Cleanup(func() { mixer.Use(nilMixerBackend{}) })
}

// nilMixerBackend restores a deterministic unavailable backend after tests;
// the production server-tag build replaces it from tinyalsa init.
type nilMixerBackend struct{}

func (nilMixerBackend) Set(string, []string) error { return errors.New("mixer unavailable") }
func (nilMixerBackend) Get(string) (string, error) { return "", errors.New("mixer unavailable") }

func TestRestoreUnmutedClearsStaleHardwareMute(t *testing.T) {
	backend := newMuteMixerFake(true)
	useMuteMixer(t, backend)
	m := testMuteController(&muteLEDFake{})

	if err := m.restore(false); err != nil {
		t.Fatal(err)
	}
	if m.IsMuted() {
		t.Fatal("logical state stayed muted after verified startup unmute")
	}
	status := m.status()
	if status["hardware_verified"] != true || status["hardware_muted"] != false {
		t.Fatalf("unexpected hardware status: %#v", status)
	}
}

func TestRestoreRetriesTransientMixerFailure(t *testing.T) {
	backend := newMuteMixerFake(true)
	backend.failWrites = 1
	useMuteMixer(t, backend)
	m := testMuteController(&muteLEDFake{})

	if err := m.restore(false); err != nil {
		t.Fatal(err)
	}
	if backend.sets <= len(biscuitADCMuteControls) {
		t.Fatalf("set calls=%d, want a retry after mixed readback", backend.sets)
	}
}

func TestFailedUnmuteRemainsMutedAndDoesNotRestartCapture(t *testing.T) {
	backend := newMuteMixerFake(true)
	backend.failWrites = 100
	useMuteMixer(t, backend)
	ring := &muteLEDFake{}
	m := testMuteController(ring)
	m.muted = true
	var changes []bool
	m.onMuteChange = func(muted bool) { changes = append(changes, muted) }

	m.Toggle()
	if !m.IsMuted() {
		t.Fatal("failed hardware unmute changed logical state to unmuted")
	}
	if len(changes) != 0 {
		t.Fatalf("failed unmute notified capture owner: %v", changes)
	}
	if len(ring.lastFrame) != 12 || ring.lastFrame[0].R == 0 {
		t.Fatalf("failed unmute did not preserve red mute ring: %#v", ring.lastFrame)
	}
}

func TestReconnectReconcilesStaleHardwareAndRetriesLED(t *testing.T) {
	backend := newMuteMixerFake(true)
	useMuteMixer(t, backend)
	ring := &muteLEDFake{failures: 2}
	m := testMuteController(ring)
	m.muted = false

	if err := m.reconcile(); err != nil {
		t.Fatal(err)
	}
	if m.IsMuted() {
		t.Fatal("successful reconnect reconciliation changed logical state")
	}
	if ring.setCalls != 3 {
		t.Fatalf("LED set calls=%d, want 3", ring.setCalls)
	}
	for _, pixel := range ring.lastFrame {
		if pixel.R != 0 || pixel.G != 0 || pixel.B != 0 {
			t.Fatalf("unmuted reconnect left a lit pixel: %#v", pixel)
		}
	}
}
