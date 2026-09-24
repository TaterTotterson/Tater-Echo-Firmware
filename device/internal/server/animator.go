package server

import (
	"log"
	"math"
	"sync"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/pkg/led"
)

// AnimSpec describes a device-rendered ring animation, received from the
// controller as a led_anim control message. The device renders every frame
// locally on its own ticker, so animation smoothness no longer depends on
// controller event-loop scheduling or WiFi jitter. The controller remains
// the source of truth for *which* animation should be showing: it re-sends
// the current spec on reconnect, and TTLSec is the dead-man switch for the
// opposite failure (controller gone mid-animation).
type AnimSpec struct {
	// Pattern: "off", "solid", the legacy "spin"/"rotate" patterns, "meter"
	// or its public Tater name "audio_glow" (brightness follows live speaker
	// RMS), or any native Tater animation:
	// sparkle, ping_pong, voice_ring, spinner, orbit, pulse, breathe, comet,
	// dual_comet, scanner, ripple, heartbeat, theater, wave, shimmer,
	// twinkle, or equalizer.
	Pattern string `json:"pattern"`
	// Colors semantics per pattern:
	//   solid        — palette painted 1:1 (1 colour = whole ring, else per-LED)
	//   spin         — [head, trail]
	//   rotate       — palette rotated one LED per frame
	//   native       — first colour is the selected Tater colour
	//   meter        — palette whose brightness is audio-modulated
	Colors [][3]uint8 `json:"colors"`
	// PeriodMs: frame interval for spin/rotate/native effects (0 → 80ms).
	// Meter ignores it (fixed 40ms tick).
	PeriodMs int `json:"periodMs"`
	// Listening marks solid frames as the listening ring so the
	// beamformer direction overlay engages (same flag as set_leds).
	Listening bool `json:"listening"`
	// TTLSec auto-clears the ring if no newer spec arrives — protects
	// against a controller that died mid-turn. 0 → no TTL.
	TTLSec int `json:"ttlSec"`

	// ── meter-only response curve ────────────────────────────────────────
	// Pointer-typed so 0 is expressible and "absent" is distinguishable
	// from "zero" (same reason micGainDb is a pointer in ConfigMessage).
	// Nil fields fall back to the meterDefaults below.
	//
	// Tunable from the dashboard because this is a *taste* parameter: the
	// original fixed curve measured only ~23% perceived brightness
	// variation on speech (see docs/led-ring-states.md), and finding a
	// value that reads well in a real room takes several passes. Shipping
	// them as config turns that loop into a page refresh instead of a
	// firmware OTA per iteration.
	Attack *float64 `json:"attack"` // envelope rise coefficient per 40ms tick
	Decay  *float64 `json:"decay"`  // envelope fall coefficient per 40ms tick
	Floor  *float64 `json:"floor"`  // perceptual brightness at silence
	Gamma  *float64 `json:"gamma"`  // output gamma; >1 expands the dark end
	Ref    *float64 `json:"ref"`    // RMS mapped to full scale
	Curve  *float64 `json:"curve"`  // input exponent; <1 lifts quiet detail
}

// meterDefaults are the shipped response curve. Derived in
// docs/led-ring-states.md §"Why it's barely visible":
//   - decay 0.30 → τ≈133ms, which tracks syllables (150-250ms). The old
//     0.12 gave τ≈333ms and smoothed everything below phrase rate away,
//     which is why the ring looked nearly static during speech.
//   - floor 0.06 + gamma 2.2: the paint is (floor+span*env)^gamma, so the
//     value is a *perceptual* target rather than a raw duty cycle. Without
//     the gamma the eye sees roughly the 1/2.2 power of the linear swing,
//     which is what compressed the old range into invisibility.
//   - ref 0.22 / curve 0.7 replace sqrt(rms/0.35): a gentler lift that
//     keeps quiet consonants visible without squashing normal speech into
//     the top of the range.
var meterDefaults = struct {
	attack, decay, floor, gamma, ref, curve float64
}{
	attack: 0.6,
	decay:  0.30,
	floor:  0.06,
	gamma:  2.2,
	ref:    0.22,
	curve:  0.7,
}

// resolveMeter returns spec's meter parameters with defaults filled in and
// each value clamped to a range that cannot produce a dead or seizure-grade
// ring — a bad config push must not be able to break the display.
func resolveMeter(spec AnimSpec) (attack, decay, floor, gamma, ref, curve float64) {
	pick := func(p *float64, def, lo, hi float64) float64 {
		if p == nil {
			return def
		}
		return math.Min(hi, math.Max(lo, *p))
	}
	attack = pick(spec.Attack, meterDefaults.attack, 0.05, 1.0)
	decay = pick(spec.Decay, meterDefaults.decay, 0.02, 1.0)
	floor = pick(spec.Floor, meterDefaults.floor, 0.0, 0.6)
	gamma = pick(spec.Gamma, meterDefaults.gamma, 1.0, 3.5)
	ref = pick(spec.Ref, meterDefaults.ref, 0.02, 1.0)
	curve = pick(spec.Curve, meterDefaults.curve, 0.3, 2.0)
	return
}

// animator owns the ring animation goroutine. One animation at a time; a
// new spec (or Stop) atomically replaces the current one via generation
// counting, so a stale goroutine can never paint over its successor.
type animator struct {
	mu  sync.Mutex
	gen int
}

const defaultAnimPeriod = 80 * time.Millisecond

func isAudioMeterPattern(pattern string) bool {
	return pattern == "meter" || pattern == "audio_glow"
}

// StartAnim replaces the current animation with spec.
func (s *Server) StartAnim(spec AnimSpec) {
	s.anim.mu.Lock()
	s.anim.gen++
	gen := s.anim.gen
	s.anim.mu.Unlock()
	if spec.Pattern != "solid" || !spec.Listening {
		// Native animations paint their first frame from a goroutine. Freeze
		// the listening turn synchronously so a DOA sample arriving in that
		// small handoff window cannot move the remembered reply direction.
		s.endDirectionalListening()
	}

	switch spec.Pattern {
	case "off":
		s.SetLEDs(blackFrame(), boolPtr(false))
	case "solid":
		s.SetLEDs(paletteFrame(spec.Colors), boolPtr(spec.Listening))
		if spec.TTLSec > 0 {
			go s.animExpiry(gen, time.Duration(spec.TTLSec)*time.Second)
		}
	case "spin", "rotate":
		go s.runAnim(gen, spec)
	case "sparkle", "ping_pong", "voice_ring", "spinner", "orbit", "pulse",
		"breathe", "comet", "dual_comet", "scanner", "ripple", "heartbeat",
		"theater", "wave", "shimmer", "twinkle", "equalizer":
		go s.runNativeAnim(gen, spec)
	default:
		if isAudioMeterPattern(spec.Pattern) {
			go s.runMeter(gen, spec)
			return
		}
		log.Printf("StartAnim: unknown pattern %q — clearing ring", spec.Pattern)
		s.SetLEDs(blackFrame(), boolPtr(false))
	}
}

// StopAnim cancels any running animation without touching the ring — used
// when another subsystem (e.g. a controller set_leds frame) takes over.
func (s *Server) StopAnim() {
	s.anim.mu.Lock()
	s.anim.gen++
	s.anim.mu.Unlock()
}

// animCurrent reports whether gen is still the live animation.
func (s *Server) animCurrent(gen int) bool {
	s.anim.mu.Lock()
	defer s.anim.mu.Unlock()
	return gen == s.anim.gen
}

// animExpiry clears the ring when a TTL'd static frame outlives its
// dead-man window without being replaced.
func (s *Server) animExpiry(gen int, ttl time.Duration) {
	time.Sleep(ttl)
	if !s.animCurrent(gen) {
		return
	}
	log.Printf("led_anim: TTL expired with no replacement — clearing ring")
	s.SetLEDs(blackFrame(), boolPtr(false))
}

// runAnim renders spin/rotate frames until replaced or TTL-expired. Frames
// go through SetLEDs so the mute-ring and volume-arc paint suppressions
// (and baseLEDs recording for the hand-back repaint) apply unchanged.
func (s *Server) runAnim(gen int, spec AnimSpec) {
	period := defaultAnimPeriod
	if spec.PeriodMs > 0 {
		period = time.Duration(spec.PeriodMs) * time.Millisecond
	}
	var deadline time.Time
	if spec.TTLSec > 0 {
		deadline = time.Now().Add(time.Duration(spec.TTLSec) * time.Second)
	}

	ticker := time.NewTicker(period)
	defer ticker.Stop()

	pos := 0
	for {
		if !s.animCurrent(gen) {
			return
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			log.Printf("led_anim: TTL expired with no replacement — clearing ring")
			if s.animCurrent(gen) {
				s.SetLEDs(blackFrame(), boolPtr(false))
			}
			return
		}
		s.SetLEDs(animFrame(spec, pos), boolPtr(false))
		pos = (pos + 1) % 12
		<-ticker.C
	}
}

// SetAudioLevel records the live speaker RMS (0..1) — fed by the speaker's
// levelTap on the ALSA pump goroutine, read by the meter animation.
func (s *Server) SetAudioLevel(rms float64) {
	s.audioLevel.Store(math.Float64bits(rms))
}

func (s *Server) getAudioLevel() float64 {
	return math.Float64frombits(s.audioLevel.Load())
}

type nativeAnimState struct {
	sparkle     [12]float64
	voiceRadius float64
}

// runNativeAnim renders the same named effects exposed for the other Tater
// native satellites. The Echo has 12 pixels rather than 24, so spatial
// effects use the same proportions scaled to this ring.
func (s *Server) runNativeAnim(gen int, spec AnimSpec) {
	period := defaultAnimPeriod
	if spec.PeriodMs > 0 {
		period = time.Duration(spec.PeriodMs) * time.Millisecond
	}
	var deadline time.Time
	if spec.TTLSec > 0 {
		deadline = time.Now().Add(time.Duration(spec.TTLSec) * time.Second)
	}
	var color [3]uint8
	if len(spec.Colors) > 0 {
		color = spec.Colors[0]
	}
	state := nativeAnimState{}
	ticker := time.NewTicker(period)
	defer ticker.Stop()
	for tick := 0; ; tick++ {
		if !s.animCurrent(gen) {
			return
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			log.Printf("led_anim: TTL expired with no replacement — clearing ring")
			if s.animCurrent(gen) {
				s.SetLEDs(blackFrame(), boolPtr(false))
			}
			return
		}
		frame := nativeAnimationFrame(spec.Pattern, tick, color, s.getAudioLevel(), s.directionLEDIndex(), &state)
		s.SetLEDs(frame, boolPtr(false))
		<-ticker.C
	}
}

func nativeAnimationFrame(pattern string, tick int, color [3]uint8, audioLevel float64, direction int, state *nativeAnimState) []led.Led {
	levels := make([]float64, 12)
	switch pattern {
	case "sparkle":
		breathStep := tick % 18
		if breathStep > 9 {
			breathStep = 18 - breathStep
		}
		for i := range levels {
			target := 0.025 + float64(breathStep)*0.006
			bitPhase := (i*7 + tick*5) % 29
			if bitPhase == 0 {
				target += 0.86
			} else if bitPhase == 1 || bitPhase == 28 {
				target += 0.46
			}
			calcPhase := (i*5 + tick*2) % 17
			if calcPhase == 0 || calcPhase == 8 {
				target += 0.50
			} else if calcPhase == 1 || calcPhase == 9 {
				target += 0.24
			}
			target = clampUnit(target)
			alpha := 0.20
			if target > state.sparkle[i] {
				alpha = 0.58
			}
			state.sparkle[i] += (target - state.sparkle[i]) * alpha
			levels[i] = state.sparkle[i]
		}
	case "ping_pong":
		span := 6
		period := (span - 1) * 2
		phase := tick % period
		leadA := phase
		forward := true
		if phase >= span {
			leadA = period - phase
			forward = false
		}
		leadB := 11 - leadA
		setMax(levels, leadA, 1)
		setMax(levels, leadB, 1)
		trail := -1
		if !forward {
			trail = 1
		}
		setMax(levels, wrapLED(leadA+trail), 192.0/255)
		setMax(levels, wrapLED(leadB-trail), 192.0/255)
		setMax(levels, wrapLED(leadA+2*trail), 128.0/255)
		setMax(levels, wrapLED(leadB-2*trail), 128.0/255)
	case "spinner", "orbit":
		head := tick / 2
		if pattern == "orbit" {
			head = tick
		}
		head %= 12
		for _, p := range []struct {
			offset int
			level  float64
		}{{0, 1}, {6, 1}, {-1, 192.0 / 255}, {5, 192.0 / 255}, {-2, 128.0 / 255}, {4, 128.0 / 255}} {
			setMax(levels, wrapLED(head+p.offset), p.level)
		}
	case "pulse":
		step := tick % 24
		if step > 12 {
			step = 24 - step
		}
		fillLevels(levels, 0.10+float64(step)/12*0.82)
	case "breathe":
		fillLevels(levels, 0.08+triangleWave(tick, 34)*0.84)
	case "comet", "dual_comet":
		head := tick % 12
		second := (head + 6) % 12
		for i := range levels {
			levels[i] = cometLevel(wrapLED(head - i))
			if pattern == "dual_comet" {
				levels[i] = math.Max(levels[i], cometLevel(wrapLED(second-i)))
			}
		}
	case "scanner":
		phase := tick % 22
		head := phase
		if phase >= 12 {
			head = 22 - phase
		}
		for i := range levels {
			switch int(ringDistance(float64(i), float64(head))) {
			case 0:
				levels[i] = 1
			case 1:
				levels[i] = 0.54
			case 2:
				levels[i] = 0.22
			default:
				levels[i] = 0.025
			}
		}
	case "ripple":
		radius := triangleWave(tick, 24) * 12 * 0.48
		for i := range levels {
			levels[i] = 0.035 + clampUnit(1-math.Abs(ringDistance(float64(i), 6)-radius)/1.45)*0.88
		}
	case "heartbeat":
		phase := tick % 34
		pulse := 0.0
		if phase <= 4 {
			pulse = 1 - float64(phase)/5
		} else if phase >= 8 && phase <= 11 {
			pulse = 0.72 * (1 - float64(phase-8)/4)
		}
		for i := range levels {
			levels[i] = 0.04 + pulse*0.92
			if i%2 != 0 {
				levels[i] *= 0.72
			}
		}
	case "theater":
		phase := tick % 6
		for i := range levels {
			switch (i + phase) % 6 {
			case 0:
				levels[i] = 1
			case 1:
				levels[i] = 0.48
			case 5:
				levels[i] = 0.24
			default:
				levels[i] = 0.025
			}
		}
	case "wave":
		center := float64((tick / 2) % 12)
		cross := math.Mod(center+6, 12)
		for i := range levels {
			level := 0.05 + clampUnit(1-ringDistance(float64(i), center)/4.2)*0.84
			level += clampUnit(1-ringDistance(float64(i), cross)/2.4) * 0.16
			levels[i] = clampUnit(level)
		}
	case "shimmer":
		breath := 0.08 + triangleWave(tick, 28)*0.18
		for i := range levels {
			phase := (i*17 + tick*5) % 37
			switch {
			case phase == 0:
				levels[i] = 1
			case phase <= 3 || phase >= 34:
				levels[i] = 0.46
			case (phase+i)%11 == 0:
				levels[i] = 0.28
			default:
				levels[i] = breath
			}
		}
	case "twinkle":
		for i := range levels {
			phase := (i*7 + tick*3) % 31
			switch phase {
			case 0:
				levels[i] = 0.95
			case 1, 30:
				levels[i] = 0.48
			case 2, 29:
				levels[i] = 0.20
			default:
				levels[i] = 0.04
			}
		}
	case "equalizer":
		for i := range levels {
			phase := (i*5 + tick*3) % 20
			if phase > 10 {
				phase = 20 - phase
			}
			levels[i] = 0.05 + float64(phase)/10*0.72
			if (i*13+tick)%17 == 0 {
				levels[i] = 1
			}
		}
	case "voice_ring":
		center := wrapLED(direction)
		normalized := clampUnit(audioLevel * 7.5)
		targetRadius := math.Min(5.8, 1.0+normalized*4.8)
		alpha := 0.22
		if targetRadius > state.voiceRadius {
			alpha = 0.50
		}
		state.voiceRadius += (targetRadius - state.voiceRadius) * alpha
		return voiceRingFrame(tick, color, center, state.voiceRadius)
	default:
		fillLevels(levels, 1)
	}
	return levelsFrame(color, levels)
}

func voiceRingFrame(tick int, color [3]uint8, center int, radius float64) []led.Led {
	frame := blackFrame()
	brightness := math.Max(float64(color[0]), math.Max(float64(color[1]), float64(color[2]))) / 255
	for i := range frame {
		distance := int(ringDistance(float64(i), float64(center)))
		level := radius - float64(distance)
		if level <= 0 {
			continue
		}
		level = math.Min(level, 1)
		ripple := 0.86 + 0.14*float64((tick+distance*5)%4)/3
		colorLevel := (0.12 + level*0.76) * ripple
		centerLevel := math.Max(0, 1-float64(distance)/2.4) * 0.52
		frame[i].R = clampChannel(float64(color[0])*colorLevel + 255*centerLevel*brightness)
		frame[i].G = clampChannel(float64(color[1])*colorLevel + 240*centerLevel*brightness)
		frame[i].B = clampChannel(float64(color[2])*colorLevel + 170*centerLevel*brightness)
	}
	return frame
}

func levelsFrame(color [3]uint8, levels []float64) []led.Led {
	frame := blackFrame()
	for i := range frame {
		level := clampUnit(levels[i])
		frame[i].R = uint8(float64(color[0])*level + 0.5)
		frame[i].G = uint8(float64(color[1])*level + 0.5)
		frame[i].B = uint8(float64(color[2])*level + 0.5)
	}
	return frame
}

func setMax(levels []float64, index int, value float64) {
	if value > levels[index] {
		levels[index] = value
	}
}

func fillLevels(levels []float64, value float64) {
	for i := range levels {
		levels[i] = value
	}
}

func triangleWave(tick, period int) float64 {
	step := tick % period
	half := period / 2
	if step > half {
		step = period - step
	}
	if half == 0 {
		return 1
	}
	return clampUnit(float64(step) / float64(half))
}

func ringDistance(a, b float64) float64 {
	d := math.Abs(a - b)
	return math.Min(d, 12-d)
}

func wrapLED(index int) int {
	return (index%12 + 12) % 12
}

func cometLevel(distance int) float64 {
	switch distance {
	case 0:
		return 1
	case 1:
		return 0.68
	case 2:
		return 0.40
	case 3:
		return 0.20
	case 4:
		return 0.09
	default:
		return 0.015
	}
}

func clampUnit(value float64) float64 {
	return math.Max(0, math.Min(1, value))
}

func clampChannel(value float64) uint8 {
	return uint8(math.Max(0, math.Min(255, value)) + 0.5)
}

// runMeter throbs the palette with the live speaker level: fast attack,
// decay envelope over the RMS the speaker reports at its ALSA write — so
// the ring follows what is audible *now*, not the controller's send pace
// (~5.5s of device buffer sits between the two). The floor keeps the ring
// visibly owned by the turn through inter-word silence. Response curve is
// config-tunable — see AnimSpec's meter fields and meterDefaults.
func (s *Server) runMeter(gen int, spec AnimSpec) {
	var deadline time.Time
	if spec.TTLSec > 0 {
		deadline = time.Now().Add(time.Duration(spec.TTLSec) * time.Second)
	}
	base := paletteFrame(spec.Colors)
	attack, decay, floor, gamma, ref, curve := resolveMeter(spec)
	span := 1.0 - floor
	env := 0.0
	ticker := time.NewTicker(40 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !s.animCurrent(gen) {
			return
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			log.Printf("led_anim: TTL expired with no replacement — clearing ring")
			if s.animCurrent(gen) {
				s.SetLEDs(blackFrame(), boolPtr(false))
			}
			return
		}
		// Input curve: normalise the speaker RMS against ref, then apply
		// curve (<1 lifts quiet detail so consonants register).
		level := math.Pow(math.Min(1, s.getAudioLevel()/ref), curve)
		if level > env {
			env += attack * (level - env) // fast attack
		} else {
			env += decay * (level - env) // decay tracks syllable rate
		}
		// floor+span*env is a PERCEPTUAL brightness target; raising it to
		// gamma converts to the duty cycle that the eye reads as that
		// target. Painting the target directly (the pre-2026-07-25
		// behaviour) is what made the throb near-invisible.
		b := math.Pow(floor+span*env, gamma)
		s.SetLEDs(scaleFrame(base, b), boolPtr(false))
		<-ticker.C
	}
}

// scaleFrame returns frame with every channel scaled by b (0..1).
func scaleFrame(frame []led.Led, b float64) []led.Led {
	out := make([]led.Led, len(frame))
	for i, l := range frame {
		out[i] = led.Led{
			ID: l.ID,
			R:  uint8(float64(l.R)*b + 0.5),
			G:  uint8(float64(l.G)*b + 0.5),
			B:  uint8(float64(l.B)*b + 0.5),
		}
	}
	return out
}

// animFrame renders one frame of a spin/rotate animation.
func animFrame(spec AnimSpec, pos int) []led.Led {
	frame := make([]led.Led, 12)
	for i := range frame {
		frame[i].ID = i
	}
	switch spec.Pattern {
	case "spin":
		var head, trail [3]uint8
		if len(spec.Colors) > 0 {
			head = spec.Colors[0]
		}
		if len(spec.Colors) > 1 {
			trail = spec.Colors[1]
		}
		frame[pos%12] = led.Led{ID: pos % 12, R: head[0], G: head[1], B: head[2]}
		p := (pos + 11) % 12
		frame[p] = led.Led{ID: p, R: trail[0], G: trail[1], B: trail[2]}
	case "rotate":
		n := len(spec.Colors)
		if n == 0 {
			return frame
		}
		for i := range frame {
			c := spec.Colors[((i-pos)%n+n)%n]
			frame[i].R, frame[i].G, frame[i].B = c[0], c[1], c[2]
		}
	}
	return frame
}

// paletteFrame maps a colour list onto the ring: one colour fills the
// whole ring, otherwise colours map per-LED (short lists leave the rest
// dark, matching set_leds partial-frame behaviour).
func paletteFrame(colors [][3]uint8) []led.Led {
	frame := make([]led.Led, 12)
	for i := range frame {
		frame[i].ID = i
		var c [3]uint8
		switch {
		case len(colors) == 1:
			c = colors[0]
		case i < len(colors):
			c = colors[i]
		}
		frame[i].R, frame[i].G, frame[i].B = c[0], c[1], c[2]
	}
	return frame
}

func blackFrame() []led.Led {
	frame := make([]led.Led, 12)
	for i := range frame {
		frame[i].ID = i
	}
	return frame
}

func boolPtr(b bool) *bool { return &b }
