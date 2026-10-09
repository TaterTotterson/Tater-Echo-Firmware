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
	SpeakerAmp         = "Ext_Speaker_Amp_Switch"
	InternalSpeakerAmp = "Speaker_Amp_Switch"
	PlaybackVolume     = "PCM Playback Volume" // DAC digital volume, 0.5dB steps, 127 = 0dB
	HPDriverGain       = "HP Driver Gain Volume"
	DacMux             = "Audio_DacMux_Setting" // jack line level (#566); measured on FireOS 5 only
)

// targetProfile contains only controls whose meaning differs between boards.
// Route switches live in codec because they are an ordered group applied
// before opening a PCM. Logical volume remains 0..127 everywhere so Tater's
// stored level does not change when the hardware codec does.
type targetProfile struct {
	playbackVolume string
	playbackMax    int
	speakerAmps    []ampControl
	adcMute        []string
}

type ampControl struct {
	name string
	on   string
	off  string
}

var biscuitProfile = targetProfile{
	playbackVolume: PlaybackVolume,
	playbackMax:    127,
	speakerAmps:    []ampControl{{name: SpeakerAmp, on: "On", off: "Off"}},
	adcMute: []string{
		"ADC_A Left Mute", "ADC_A Right Mute",
		"ADC_B Left Mute", "ADC_B Right Mute",
		"ADC_C Left Mute", "ADC_C Right Mute",
		"ADC_D Left Mute", "ADC_D Right Mute",
	},
}

var radarProfile = targetProfile{
	playbackVolume: PlaybackVolume,
	playbackMax:    127,
	// Radar has separate codec and external-amplifier gates. Both must be
	// enabled after the DAC is clocking; either one left off produces silence.
	speakerAmps: []ampControl{
		{name: InternalSpeakerAmp, on: "On", off: "Off"},
		{name: SpeakerAmp, on: "On", off: "Off"},
	},
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
	// Checkers' external speaker GPIO is active-low. This was observed both
	// in the stock ext_speaker_output path and in the live route trace.
	speakerAmps: []ampControl{{name: SpeakerAmp, on: "Off", off: "On"}},
	adcMute:     []string{"ADC_A Left Mute", "ADC_A Right Mute"},
}

var rookProfile = targetProfile{
	playbackVolume: PlaybackVolume,
	playbackMax:    127,
	speakerAmps:    []ampControl{{name: SpeakerAmp, on: "On", off: "Off"}},
	adcMute: []string{
		"ADC_A Left Mute", "ADC_A Right Mute",
		"ADC_B Left Mute", "ADC_B Right Mute",
	},
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
	if strings.EqualFold(strings.TrimSpace(target), "radar") {
		activeProfile = radarProfile
		return
	}
	if strings.EqualFold(strings.TrimSpace(target), "rook") {
		activeProfile = rookProfile
		return
	}
	activeProfile = biscuitProfile
}

func profile() targetProfile {
	targetMu.RLock()
	p := activeProfile
	p.adcMute = append([]string(nil), activeProfile.adcMute...)
	p.speakerAmps = append([]ampControl(nil), activeProfile.speakerAmps...)
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

// SetSpeakerEnabled applies every gate and polarity in the selected board's
// speaker path.
func SetSpeakerEnabled(enabled bool) error {
	p := profile()
	var failed []string
	for _, amp := range p.speakerAmps {
		value := amp.off
		if enabled {
			value = amp.on
		}
		if err := Set(amp.name, value); err != nil {
			failed = append(failed, err.Error())
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%s", strings.Join(failed, "; "))
	}
	return nil
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

// ADCMuteState reads every physical ADC mute control for the selected board.
// The returned state is trustworthy only when consistent is true and err is
// nil. Keeping the raw values makes a failed reconciliation diagnosable from
// the satellite status API without requiring a serial console.
func ADCMuteState() (muted bool, consistent bool, values map[string]string, err error) {
	controls := profile().adcMute
	values = make(map[string]string, len(controls))
	consistent = true
	var first bool
	var problems []string
	for _, control := range controls {
		raw, readErr := Get(control)
		if readErr != nil {
			values[control] = "error"
			problems = append(problems, readErr.Error())
			consistent = false
			continue
		}
		values[control] = raw
		value, ok := boolValue(raw)
		if !ok {
			problems = append(problems, fmt.Sprintf("mixer: %q returned invalid mute value %q", control, raw))
			consistent = false
			continue
		}
		controlMuted := value != 0
		if !first {
			muted = controlMuted
			first = true
		} else if controlMuted != muted {
			consistent = false
		}
	}
	if !first {
		consistent = false
	}
	if len(problems) > 0 {
		err = fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return muted, consistent, values, err
}

// Backend is the device implementation. Values are strings as tinymix prints
// and accepts them: enum names, "On"/"Off" for switches, decimal integers.
type Backend interface {
	Set(name string, values []string) error
	Get(name string) (string, error)
}

// byteBackend is optional because only Radar's speaker tuning uses an ALSA
// byte control. Keeping it separate avoids pretending ordinary fake and host
// backends can safely encode a coefficient blob as decimal mixer values.
type byteBackend interface {
	SetBytes(name string, values []byte) error
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

// SetBytes writes a complete byte-typed control in one atomic ALSA ioctl.
// Writing its elements one at a time would zero every element except the last
// on tinyalsa versions shipped by these devices.
func SetBytes(name string, values []byte) error {
	mu.Lock()
	defer mu.Unlock()
	if len(values) == 0 {
		return fmt.Errorf("mixer: %q: no byte values", name)
	}
	b, ok := backend.(byteBackend)
	if !ok {
		return fmt.Errorf("mixer: %q: backend does not support byte controls", name)
	}
	err := b.SetBytes(name, values)
	if err != nil && !warned[name] {
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
