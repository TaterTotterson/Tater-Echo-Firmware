// Package mixer reaches ALSA mixer controls by NAME, through tinyalsa's own
// mixer_get_ctl_by_name (#546).
//
// Control ids are positional. The FireOS 6 kernel registers two more controls
// than FireOS 5's, from id ~161 on, so every id past that point names a
// different control there: the codec routes landed on the single-ended IN2
// inputs instead of DIF1, and "HPR Output Mixer R_DAC Switch" (234) became
// "Left Input Mixer IN3_L P Switch". Writing 1 to the wrong switch is a valid
// write, so nothing failed and audio was simply silent. A name either resolves
// to the right control or does not resolve at all.
//
// It also removes a process spawn per write. The old path ran the tinymix
// binary for every control, and spawns are not free on this hardware — a heavy
// shell command was once observed inducing mic capture stalls.
package mixer

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
)

// Control names on biscuit. Measured present, and unique, on the FireOS 5 and
// FireOS 6 kernels (2026-09-17).
const (
	SpeakerAmp     = "Ext_Speaker_Amp_Switch"
	PlaybackVolume = "PCM Playback Volume" // DAC digital volume, 0.5dB steps, 127 = 0dB
	HPDriverGain   = "HP Driver Gain Volume"
)

// targetProfile contains only controls whose meaning differs between boards.
// Route switches live in codec because they are an ordered group applied
// before opening a PCM. Logical volume remains 0..127 everywhere so Tater's
// stored level does not change when the hardware codec does.
type targetProfile struct {
	playbackVolume string
	playbackMax    int
	speakerAmp     string
	speakerOn      string
	speakerOff     string
	adcMute        []string
}

var biscuitProfile = targetProfile{
	playbackVolume: PlaybackVolume,
	playbackMax:    127,
	speakerAmp:     SpeakerAmp,
	speakerOn:      "On",
	speakerOff:     "Off",
	adcMute: []string{
		"ADC_A Left Mute", "ADC_A Right Mute",
		"ADC_B Left Mute", "ADC_B Right Mute",
		"ADC_C Left Mute", "ADC_C Right Mute",
		"ADC_D Left Mute", "ADC_D Right Mute",
	},
}

var checkersProfile = targetProfile{
	// RT5616's stock route uses 173 as its normal top level. Keep the two
	// unused positive-gain steps out of Tater's scale, just as Biscuit caps
	// its DAC at unity rather than at the control's numeric maximum.
	playbackVolume: "DAC1 Playback Volume",
	playbackMax:    173,
	speakerAmp:     SpeakerAmp,
	// Checkers' external speaker GPIO is active-low. This was observed both
	// in the stock ext_speaker_output path and in the live route trace.
	speakerOn:  "Off",
	speakerOff: "On",
	adcMute:    []string{"ADC_A Left Mute", "ADC_A Right Mute"},
}

var (
	targetMu      sync.RWMutex
	activeProfile = biscuitProfile
)

// ConfigureTarget selects the measured mixer semantics for this process.
// It must run once, before any hardware controller is constructed.
func ConfigureTarget(target string) {
	targetMu.Lock()
	defer targetMu.Unlock()
	if strings.EqualFold(strings.TrimSpace(target), "checkers") {
		activeProfile = checkersProfile
		return
	}
	activeProfile = biscuitProfile
}

func profile() targetProfile {
	targetMu.RLock()
	p := activeProfile
	p.adcMute = append([]string(nil), activeProfile.adcMute...)
	targetMu.RUnlock()
	return p
}

// SetPlaybackLevel maps Tater's stable 0..logicalMax volume onto the board's
// codec range and writes it by name.
func SetPlaybackLevel(level, logicalMax int) error {
	p := profile()
	if logicalMax <= 0 {
		return fmt.Errorf("mixer: invalid logical volume maximum %d", logicalMax)
	}
	if level < 0 {
		level = 0
	}
	if level > logicalMax {
		level = logicalMax
	}
	raw := (level*p.playbackMax + logicalMax/2) / logicalMax
	return Set(p.playbackVolume, strconv.Itoa(raw))
}

// GetPlaybackLevel reads the board codec and maps it back to Tater's stable
// logical scale.
func GetPlaybackLevel(logicalMax int) (int, error) {
	p := profile()
	if logicalMax <= 0 {
		return 0, fmt.Errorf("mixer: invalid logical volume maximum %d", logicalMax)
	}
	v, err := Get(p.playbackVolume)
	if err != nil {
		return 0, err
	}
	raw, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("mixer: %q returned %q: %w", p.playbackVolume, v, err)
	}
	if raw < 0 {
		raw = 0
	}
	if raw > p.playbackMax {
		raw = p.playbackMax
	}
	return (raw*logicalMax + p.playbackMax/2) / p.playbackMax, nil
}

// SetSpeakerEnabled accounts for Checkers' active-low external amplifier.
func SetSpeakerEnabled(enabled bool) error {
	p := profile()
	value := p.speakerOff
	if enabled {
		value = p.speakerOn
	}
	return Set(p.speakerAmp, value)
}

// SetADCMute applies every physical microphone mute on the selected board and
// returns the number of writes that failed.
func SetADCMute(muted bool) int {
	value := "0"
	if muted {
		value = "1"
	}
	failed := 0
	for _, control := range profile().adcMute {
		if Set(control, value) != nil {
			failed++
		}
	}
	return failed
}

// Backend is the device implementation. Values are strings as tinymix prints
// and accepts them: enum names, "On"/"Off" for switches, decimal integers.
type Backend interface {
	Set(name string, values []string) error
	Get(name string) (string, error)
}

var (
	mu      sync.Mutex
	backend Backend = unavailable{}
	warned          = map[string]bool{}
)

// Use installs a backend. The device build installs tinyalsa's in init; tests
// install a fake.
func Use(b Backend) {
	mu.Lock()
	backend = b
	warned = map[string]bool{}
	mu.Unlock()
}

// Set writes a control. One value is applied to every channel of the control;
// otherwise there must be one value per channel.
func Set(name string, values ...string) error {
	mu.Lock()
	defer mu.Unlock()
	if len(values) == 0 {
		return fmt.Errorf("mixer: %q: no value", name)
	}
	err := backend.Set(name, values)
	if err != nil && !warned[name] {
		// Once per control: a missing control stays missing, and these are
		// called on paths that repeat.
		warned[name] = true
		log.Printf("[mixer] %v", err)
	}
	return err
}

// Get reads a control's first value.
func Get(name string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	return backend.Get(name)
}

type unavailable struct{}

func (unavailable) Set(name string, _ []string) error {
	return fmt.Errorf("mixer: %q: no mixer backend in this build", name)
}

func (unavailable) Get(name string) (string, error) {
	return "", fmt.Errorf("mixer: %q: no mixer backend in this build", name)
}

// boolValue parses a switch value the way tinymix accepts one.
func boolValue(s string) (int, bool) {
	switch s {
	case "1", "On", "on":
		return 1, true
	case "0", "Off", "off":
		return 0, true
	}
	return 0, false
}

// spread expands values to one per channel.
func spread(name string, values []string, n int) ([]string, error) {
	if n < 1 {
		return nil, fmt.Errorf("mixer: %q has no values", name)
	}
	if len(values) == 1 {
		out := make([]string, n)
		for i := range out {
			out[i] = values[0]
		}
		return out, nil
	}
	if len(values) != n {
		return nil, fmt.Errorf("mixer: %q takes %d values, got %d", name, n, len(values))
	}
	return values, nil
}
