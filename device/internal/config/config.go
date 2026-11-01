// Package config provides the shared, concurrency-safe runtime configuration
// updated by Tater-native settings.
package config

import (
	"log"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/outchain"
)

// Device holds all runtime-tunable parameters for this device.
// Zero values are replaced by defaults on first access via Get().
type Device struct {
	mu sync.RWMutex
	// firmwareTarget selects board-calibrated defaults. It is configured by
	// the daemon before the first Get call and is not changed at runtime.
	firmwareTarget string

	// Microphone / VAD
	VadThreshold float64

	// Wake word
	// MwwShadowEnabled independently enables the Tater microWakeWord runtime.
	// Crossings start a native turn through the direct Tater transport.
	// MwwThreshold=0 uses the model manifest's calibrated threshold.
	MwwShadowEnabled bool
	MwwThreshold     float64
	MwwSlidingWindow int
	MwwCloseMiss     float64
	MwwModel         string
	MwwSensitivity   string
	MwwEnvironment   string
	OwwEnabled       bool
	OwwModel         string
	OwwThreshold     float64
	OwwPatience      int
	NwwEnabled       bool
	NwwModel         string
	NwwArchitecture  string
	NwwThreshold     float64
	NwwPatience      int
	// DuckDb is how far MUSIC is attenuated while a voice turn plays over
	// it, in dB (negative = quieter). Config rather than a constant because
	// it is a taste parameter that needs iterating in a real room, the same
	// reasoning as the LED meter response curve — not something to discover
	// via a firmware OTA per attempt.
	DuckDb float64
	// MicGainDb is a fixed digital gain (dB) applied to the full 24-bit
	// capture before quantising to the 16-bit stream (see beamformer
	// extractChannel). Measured speech at normal levels sits at 0.0001–
	// 0.0006 FS RMS — only ~3–20 LSB in 16-bit terms — so gain must be
	// applied pre-truncation to recover real captured resolution rather
	// than amplify 16-bit quantisation noise. Fixed by design: this is
	// the "fixed gain" stage of the dumb-transducer architecture — all
	// adaptation lives upstream in Tater. 0 = unity.
	MicGainDb int

	// BeamAngle fixes the beamformer steering direction in degrees
	// (0–360, clockwise from 12 o'clock). -1 = auto (track loudest source).
	BeamAngle          float64
	BeamformingEnabled bool

	// Acoustic echo cancellation (speexdsp, internal/aec). Applies to the
	// whole mic path (wake stream included) — defaults off until validated
	// per deployment. AecDelayMs is the bulk write-to-ear latency the
	// reference stream is shifted by; measured on hardware (2026-07-08)
	// the right value is 0 — the mic side reads whole 160ms ALSA batches
	// (see GetAudioStream), which eats most of the speaker's ≈340ms output
	// buffering, and the filter tail absorbs the remainder. Values ≥100
	// made the echo arrive before its reference (non-causal → zero
	// cancellation). AecTailMs is the adaptive filter length, which must
	// cover residual delay error plus room reverb. Device clamps: delay
	// 0–1000ms, tail 50–500ms.
	AecEnabled *bool
	AecDelayMs int
	AecTailMs  int

	// AecRefSource picks where the far-end reference comes from: "auto",
	// "hw" or "sw".
	//
	// It is an override for the detection, not a statement about the board.
	// "auto" detects the hardware loopback and falls
	// back to the software tap on a board without one, which is right
	// almost always; "hw" and "sw" pin it, so the two paths can be
	// A/B'd from the dashboard.
	//
	// This started as EM_AEC_HW_REF, on the argument that the reference is
	// a property of the board rather than a user preference. That was
	// wrong: the board property is already DETECTED, and what a person
	// needs to set is which answer to trust — which cannot be a device
	// env var, because changing one means an edit to start_server.sh on
	// the device and a server restart. Making the measurement expensive is
	// how it stays unmeasured.
	AecRefSource string

	// Output is the speaker output chain's configuration (eqBands,
	// eqLoudness, bassGuard*, limiter*). Held here whether or not the
	// native settings have enabled it, so values are ready when activated.
	// Read with OutputChain().
	Output outchain.Params

	initialised bool
}

var global = &Device{}

// ConfigureTarget selects the board-specific defaults used when the global
// config is first initialised. The daemon calls this before any Get call.
func ConfigureTarget(target string) {
	global.mu.Lock()
	defer global.mu.Unlock()
	global.firmwareTarget = strings.ToLower(strings.TrimSpace(target))
}

// Get returns the global device config, initialised from environment
// variables on first call.
func Get() *Device {
	global.mu.Lock()
	defer global.mu.Unlock()
	if !global.initialised {
		global.loadDefaults()
		global.initialised = true
	}
	return global
}

// loadDefaults populates from environment variables, falling back to
// hard-coded defaults. Must be called with mu held.
func (d *Device) loadDefaults() {
	d.VadThreshold = envFloat("VAD_THRESHOLD", 0.004)
	d.MwwShadowEnabled = envBool("MWW_SHADOW_ENABLED", false)
	d.MwwThreshold = envFloat("MWW_THRESHOLD", 0)
	d.MwwSlidingWindow = envInt("MWW_SLIDING_WINDOW", 0)
	d.MwwCloseMiss = envFloat("MWW_CLOSE_MISS_THRESHOLD", 0)
	d.MwwModel = envStr("MWW_MODEL", "hey_tater")
	d.MwwSensitivity = envStr("MWW_SENSITIVITY", "normal")
	d.MwwEnvironment = envStr("MWW_ENVIRONMENT", "balanced")
	d.OwwEnabled = envBool("OWW_ENABLED", false)
	d.OwwModel = envStr("OWW_MODEL", "hey_tater")
	d.OwwThreshold = envFloat("OWW_THRESHOLD", 0)
	d.OwwPatience = envInt("OWW_PATIENCE", 0)
	d.NwwEnabled = envBool("NWW_ENABLED", false)
	d.NwwModel = envStr("NWW_MODEL", "hey_tater")
	d.NwwArchitecture = envStr("NWW_ARCHITECTURE", "cnn")
	d.NwwThreshold = envFloat("NWW_THRESHOLD", 0)
	d.NwwPatience = envInt("NWW_PATIENCE", 0)
	d.DuckDb = envFloat("DUCK_DB", -18)
	d.MicGainDb = clampMicGainDb(envInt("MIC_GAIN_DB", defaultMicGainDb(d.firmwareTarget)))
	d.BeamAngle = envFloat("BEAM_ANGLE", -1)
	d.BeamformingEnabled = envBool("BEAMFORMING_ENABLED", true)
	// Echo AEC is firmware-owned and starts enabled. AEC_ENABLED remains an
	// engineering/recovery override, but live settings cannot change it:
	// the reference source and timing are calibrated per Echo target.
	aecEnabled := envBool("AEC_ENABLED", true)
	d.AecEnabled = &aecEnabled
	d.AecDelayMs = envInt("AEC_DELAY_MS", 0)
	d.AecTailMs = envInt("AEC_TAIL_MS", 300)
	// EM_AEC_HW_REF keeps working as the boot default for a device with no
	// live settings, and is superseded the moment they arrive.
	d.AecRefSource = normaliseAecRef(envStr("EM_AEC_HW_REF", AecRefAuto))
	d.Output = outchain.DefaultParams()
}

// Apply updates the config from native settings.
// Only non-zero / non-empty values from the message are applied so that
// a partial config push doesn't zero out unmentioned fields.
func (d *Device) Apply(msg ConfigMessage) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.initialised {
		d.loadDefaults()
		d.initialised = true
	}

	if msg.VadThreshold > 0 {
		d.VadThreshold = msg.VadThreshold
	}
	if msg.MwwShadowEnabled != nil {
		d.MwwShadowEnabled = *msg.MwwShadowEnabled
	}
	if msg.MwwThreshold != nil && *msg.MwwThreshold >= 0 && *msg.MwwThreshold <= 1 {
		d.MwwThreshold = *msg.MwwThreshold
	}
	if msg.MwwSlidingWindow != nil && *msg.MwwSlidingWindow >= 0 && *msg.MwwSlidingWindow <= 100 {
		d.MwwSlidingWindow = *msg.MwwSlidingWindow
	}
	if msg.MwwCloseMiss != nil && *msg.MwwCloseMiss >= 0 && *msg.MwwCloseMiss <= 1 {
		d.MwwCloseMiss = *msg.MwwCloseMiss
	}
	if msg.MwwModel != "" {
		d.MwwModel = msg.MwwModel
	}
	if msg.MwwSensitivity != "" {
		d.MwwSensitivity = msg.MwwSensitivity
	}
	if msg.MwwEnvironment != "" {
		d.MwwEnvironment = msg.MwwEnvironment
	}
	if msg.OwwEnabled != nil {
		d.OwwEnabled = *msg.OwwEnabled
	}
	if msg.OwwModel != "" {
		d.OwwModel = msg.OwwModel
	}
	if msg.OwwThreshold != nil && *msg.OwwThreshold >= 0 && *msg.OwwThreshold <= 1 {
		d.OwwThreshold = *msg.OwwThreshold
	}
	if msg.OwwPatience != nil && *msg.OwwPatience >= 0 && *msg.OwwPatience <= 20 {
		d.OwwPatience = *msg.OwwPatience
	}
	if msg.NwwEnabled != nil {
		d.NwwEnabled = *msg.NwwEnabled
	}
	if msg.NwwModel != "" {
		d.NwwModel = msg.NwwModel
	}
	if msg.NwwArchitecture != "" {
		d.NwwArchitecture = msg.NwwArchitecture
	}
	if msg.NwwThreshold != nil && *msg.NwwThreshold >= 0 && *msg.NwwThreshold <= 1 {
		d.NwwThreshold = *msg.NwwThreshold
	}
	if msg.NwwPatience != nil && *msg.NwwPatience >= 0 && *msg.NwwPatience <= 20 {
		d.NwwPatience = *msg.NwwPatience
	}
	// Negative-going, so the usual "non-zero means set" rule is inverted:
	// a duck of 0dB is a legitimate setting ("do not duck at all") and must
	// be distinguishable from an absent field, hence the pointer.
	if msg.DuckDb != nil {
		d.DuckDb = *msg.DuckDb
	}
	if msg.MicGainDb != nil {
		d.MicGainDb = clampMicGainDb(*msg.MicGainDb)
	}
	if msg.BeamAngle != nil {
		d.BeamAngle = *msg.BeamAngle
	}
	if msg.BeamformingEnabled != nil {
		d.BeamformingEnabled = *msg.BeamformingEnabled
	}
	// AecEnabled and AecDelayMs are deliberately ignored. Echo targets own
	// their calibrated AEC policy locally, so live settings cannot disable it
	// or replace its write-to-ear delay.
	if msg.AecTailMs > 0 {
		d.AecTailMs = msg.AecTailMs
	}
	if msg.AecRefSource != "" {
		d.AecRefSource = normaliseAecRef(msg.AecRefSource)
	}
	applyOutput(&d.Output, msg)
}

// applyOutput merges the output-chain keys. Every one of them has a
// legitimate zero — a flat band, a 0dBFS threshold, "off" — so each is a
// pointer (or a slice) and absent means untouched. eqBands shorter than
// NumBands pads with 0, as em_eq does; longer is truncated.
func applyOutput(p *outchain.Params, msg ConfigMessage) {
	if msg.EqBands != nil {
		var b [outchain.NumBands]float64
		copy(b[:], msg.EqBands)
		p.Bands = b
	}
	if msg.EqLoudness != nil {
		p.Loudness = *msg.EqLoudness
	}
	if msg.BassGuardEnabled != nil {
		p.GuardEnabled = *msg.BassGuardEnabled
	}
	if msg.BassGuardDb != nil {
		p.GuardDb = *msg.BassGuardDb
	}
	if msg.LimiterEnabled != nil {
		p.LimiterEnabled = *msg.LimiterEnabled
	}
	if msg.LimiterThreshold != nil {
		p.LimiterThresholdDb = *msg.LimiterThreshold
	}
	if msg.LimiterRelease != nil {
		p.LimiterReleaseMs = *msg.LimiterRelease
	}
}

// OutputChain returns the output chain's current configuration.
func (d *Device) OutputChain() outchain.Params {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.Output
}

// Snapshot returns a consistent copy of all config values.
func (d *Device) Snapshot() ConfigMessage {
	d.mu.RLock()
	defer d.mu.RUnlock()
	beamAngle := d.BeamAngle
	// Copy pointer-valued settings rather than leaking addresses into the live
	// mutex-guarded struct.
	beamformingEnabled := d.BeamformingEnabled
	mwwShadowEnabled := d.MwwShadowEnabled
	mwwThreshold := d.MwwThreshold
	mwwSlidingWindow := d.MwwSlidingWindow
	mwwCloseMiss := d.MwwCloseMiss
	owwEnabled := d.OwwEnabled
	nwwEnabled := d.NwwEnabled
	owwThreshold := d.OwwThreshold
	owwPatience := d.OwwPatience
	nwwThreshold := d.NwwThreshold
	nwwPatience := d.NwwPatience
	micGainDb := d.MicGainDb
	aecEnabled := false
	if d.AecEnabled != nil {
		aecEnabled = *d.AecEnabled
	}
	aecDelayMs := d.AecDelayMs
	return ConfigMessage{
		VadThreshold:       d.VadThreshold,
		MwwShadowEnabled:   &mwwShadowEnabled,
		MwwThreshold:       &mwwThreshold,
		MwwSlidingWindow:   &mwwSlidingWindow,
		MwwCloseMiss:       &mwwCloseMiss,
		MwwModel:           d.MwwModel,
		MwwSensitivity:     d.MwwSensitivity,
		MwwEnvironment:     d.MwwEnvironment,
		OwwEnabled:         &owwEnabled,
		OwwModel:           d.OwwModel,
		OwwThreshold:       &owwThreshold,
		OwwPatience:        &owwPatience,
		NwwEnabled:         &nwwEnabled,
		NwwModel:           d.NwwModel,
		NwwArchitecture:    d.NwwArchitecture,
		NwwThreshold:       &nwwThreshold,
		NwwPatience:        &nwwPatience,
		MicGainDb:          &micGainDb,
		BeamAngle:          &beamAngle,
		BeamformingEnabled: &beamformingEnabled,
		AecEnabled:         &aecEnabled,
		AecDelayMs:         &aecDelayMs,
		AecTailMs:          d.AecTailMs,
		AecRefSource:       d.AecRefSource,
	}
}

// ConfigMessage is the typed subset of native settings shared by runtime
// components.
type ConfigMessage struct {
	MicGainDb          *int     `json:"micGainDb,omitempty"`
	VadThreshold       float64  `json:"vadThreshold,omitempty"`
	MwwShadowEnabled   *bool    `json:"mwwShadowEnabled,omitempty"`
	MwwThreshold       *float64 `json:"mwwThreshold,omitempty"`
	MwwSlidingWindow   *int     `json:"mwwSlidingWindow,omitempty"`
	MwwCloseMiss       *float64 `json:"mwwCloseMissThreshold,omitempty"`
	MwwModel           string   `json:"mwwModel,omitempty"`
	MwwSensitivity     string   `json:"mwwSensitivity,omitempty"`
	MwwEnvironment     string   `json:"mwwEnvironment,omitempty"`
	OwwEnabled         *bool    `json:"owwEnabled,omitempty"`
	OwwModel           string   `json:"owwModel,omitempty"`
	OwwThreshold       *float64 `json:"owwThreshold,omitempty"`
	OwwPatience        *int     `json:"owwPatience,omitempty"`
	NwwEnabled         *bool    `json:"nwwEnabled,omitempty"`
	NwwModel           string   `json:"nwwModel,omitempty"`
	NwwArchitecture    string   `json:"nwwArchitecture,omitempty"`
	NwwThreshold       *float64 `json:"nwwThreshold,omitempty"`
	NwwPatience        *int     `json:"nwwPatience,omitempty"`
	DuckDb             *float64 `json:"duckDb,omitempty"`
	BeamAngle          *float64 `json:"beamAngle,omitempty"`
	BeamformingEnabled *bool    `json:"beamformingEnabled,omitempty"`
	HasBeamforming     bool     `json:"hasBeamforming,omitempty"`
	AecEnabled         *bool    `json:"aecEnabled,omitempty"`
	AecDelayMs         *int     `json:"aecDelayMs,omitempty"`
	AecTailMs          int      `json:"aecTailMs,omitempty"`
	AecRefSource       string   `json:"aecRefSource,omitempty"`

	// Output chain (internal/outchain). Pointers because zero is a real
	// setting for every one of them; see applyOutput.
	EqBands          []float64 `json:"eqBands,omitempty"`
	EqLoudness       *bool     `json:"eqLoudness,omitempty"`
	BassGuardEnabled *bool     `json:"bassGuardEnabled,omitempty"`
	BassGuardDb      *float64  `json:"bassGuardDb,omitempty"`
	LimiterEnabled   *bool     `json:"limiterEnabled,omitempty"`
	LimiterThreshold *float64  `json:"limiterThreshold,omitempty"`
	LimiterRelease   *float64  `json:"limiterRelease,omitempty"`
}

// clampMicGainDb bounds the fixed mic gain to a sane range: 0dB (unity —
// the pre-gain behaviour, bit-exact) up to +42dB. The 24-bit capture holds
// 8 bits (48dB) below the old 16-bit truncation point; beyond +42dB the
// gain is amplifying the capture's own noise floor with no headroom left.
func clampMicGainDb(db int) int {
	if db < 0 {
		return 0
	}
	if db > 42 {
		return 42
	}
	return db
}

// defaultMicGainDb compensates for the lower capture level measured on the
// shared Puffin seven-microphone path used by Biscuit and Radar. Other targets
// need only a smaller lift over the old +24dB fleet default.
func defaultMicGainDb(target string) int {
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "biscuit", "radar":
		return 40
	default:
		return 28
	}
}

const (
	// AecRefSource values. "auto" detects the hardware loopback and falls
	// back to the software tap; the other two pin it for an A/B.
	AecRefAuto = "auto"
	AecRefHW   = "hw"
	AecRefSW   = "sw"
)

// normaliseAecRef keeps an unknown value on the DETECTING path rather than
// pinning one. A typo that pinned "sw" would silently disable the hardware
// reference on every device it reached, and read as the feature not working.
func normaliseAecRef(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case AecRefHW, "on", "true", "1":
		return AecRefHW
	case AecRefSW, "off", "false", "0":
		return AecRefSW
	case "", AecRefAuto:
		return AecRefAuto
	default:
		log.Printf("[config] unknown aecRefSource %q — detecting", v)
		return AecRefAuto
	}
}

// ─── env helpers ──────────────────────────────────────────────────────────────

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		return v == "1" || v == "true" || v == "True"
	}
	return def
}

func envStr(key string, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
