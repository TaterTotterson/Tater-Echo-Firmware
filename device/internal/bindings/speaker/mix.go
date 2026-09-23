package speaker

import "math"

// Ducking and mixing for the two playback streams.
//
// No build tag and no tinyalsa import, deliberately: this is the arithmetic
// the ALSA write path runs on every period, and it is the part worth testing
// on the host. pcm_speaker.go is `//go:build server` and cannot be compiled
// or tested anywhere but the device.
//
// WHY THE DEVICE MIXES AT ALL. Barge-in used to pause the music, answer, and
// resume. Every assistant people compare us to ducks instead, and pausing
// carried real bugs with it: a Music Assistant flow stream cannot be seeked,
// so a 28-second turn cost 28 seconds of the song, and the media_player
// entity had to report PLAYING while internally paused (#62) or Home
// Assistant would decline the user's own pause.
//
// It has to happen HERE rather than in the controller because of `LEAD_S`:
// the music feed runs 4 seconds ahead of realtime, so when a wake word fires
// the next 4 seconds of music are already in this device's buffer. Audio
// that has left the controller cannot be ducked by the controller. Doing it
// on the device means the music keeps its full ~5.5s of link-stall
// protection AND ducking is instant, because the gain is applied to audio we
// are already holding.

// Q15 fixed point: 32768 == unity. Integer maths on a 32-bit A53 with no
// FPU pressure on the audio path, and exact at unity — a float multiply
// would leave a stream that is nominally not ducked very slightly altered.
const unityGain int32 = 1 << 15

// Mixer combines the voice and music streams for one output period.
//
// Single-consumer by contract: only the ALSA write goroutine touches it, so
// nothing here is synchronised. The gain TARGET is set from elsewhere and is
// the one field that needs atomicity — it lives in PcmSpeaker, not here.
type Mixer struct {
	gain            int32 // current, Q15
	rampStart       int32
	rampTarget      int32
	rampFramesTotal int64
	rampFramesDone  int64
}

// DuckGain converts decibels of attenuation to Q15.
//
// 0 dB is returned as exactly unity rather than a rounded conversion, so
// "not ducked" is bit-identical to the input.
func DuckGain(db float64) int32 {
	if db >= 0 {
		return unityGain
	}
	g := math.Pow(10, db/20.0) * float64(unityGain)
	if g < 0 {
		return 0
	}
	return int32(g + 0.5)
}

// Gain reports the current (ramped) gain, for tests and logging.
func (m *Mixer) Gain() int32 { return m.gain }

// SetGainImmediate jumps the ramp to a value. Used at stream start, where
// there is no audio to click.
func (m *Mixer) SetGainImmediate(g int32) {
	m.gain = g
	m.rampStart = g
	m.rampTarget = g
	m.rampFramesTotal = 0
	m.rampFramesDone = 0
}

// SetRamp schedules an exact sample-counted gain transition. It is called by
// the ALSA goroutine when it observes a new atomic duck request, so all mixer
// state remains single-consumer and race-free.
func (m *Mixer) SetRamp(target int32, frames int64) {
	if frames <= 0 || target == m.gain {
		m.SetGainImmediate(target)
		return
	}
	m.rampStart = m.gain
	m.rampTarget = target
	m.rampFramesTotal = frames
	m.rampFramesDone = 0
}

// Mix produces one output period from whichever streams have audio.
//
// Both buffers are owned by the caller once dequeued, so mixing happens IN
// PLACE and the function allocates nothing — this runs ~23 times a second
// for as long as anything is playing.
//
// Returns the buffer to write, or nil when there is nothing to play (the
// caller pumps silence, which is what paces this loop).
func (m *Mixer) Mix(voice, music []byte, target int32) []byte {
	frames := len(voice) / 4
	if len(music)/4 > frames {
		frames = len(music) / 4
	}
	if frames == 0 {
		frames = 1
	}
	if target != m.rampTarget {
		m.SetRamp(target, int64(frames))
	}
	return m.mixConfigured(voice, music, frames)
}

// MixConfigured uses the envelope previously selected with SetRamp. The
// caller supplies clockFrames because silence still advances a timed ramp.
func (m *Mixer) MixConfigured(voice, music []byte, clockFrames int) []byte {
	if clockFrames <= 0 {
		clockFrames = 1
	}
	return m.mixConfigured(voice, music, clockFrames)
}

func (m *Mixer) mixConfigured(voice, music []byte, clockFrames int) []byte {
	switch {
	case voice == nil && music == nil:
		m.advanceGain(int64(clockFrames))
		return nil

	case music == nil:
		// Voice alone is never ducked — it is the thing being listened to.
		m.advanceGain(int64(clockFrames))
		return voice

	case voice == nil:
		m.applyConfiguredGain(music)
		return music

	default:
		m.applyConfiguredGain(music)
		mixInto(voice, music)
		return voice
	}
}

func (m *Mixer) advanceGain(frames int64) {
	if m.rampFramesTotal <= 0 || m.rampFramesDone >= m.rampFramesTotal {
		m.gain = m.rampTarget
		return
	}
	m.rampFramesDone += frames
	if m.rampFramesDone >= m.rampFramesTotal {
		m.rampFramesDone = m.rampFramesTotal
		m.gain = m.rampTarget
		return
	}
	delta := int64(m.rampTarget - m.rampStart)
	m.gain = m.rampStart + int32(delta*m.rampFramesDone/m.rampFramesTotal)
}

// applyGain scales a period, ramping across it sample by sample.
//
// The ramp is per SAMPLE, not per period: a gain change applied at a period
// boundary is a step discontinuity, which is a click. Interpolating across
// the period turns the same change into a fade.
func (m *Mixer) applyGain(buf []byte, target int32) {
	frames := len(buf) / 4 // stereo S16
	if frames == 0 {
		return
	}
	if target != m.rampTarget {
		m.SetRamp(target, int64(frames))
	}
	m.applyConfiguredGain(buf)
}

func (m *Mixer) applyConfiguredGain(buf []byte) {
	frames := len(buf) / 4
	if frames == 0 {
		return
	}
	for i := 0; i < frames; i++ {
		g := m.gain
		// L and R are duplicates on this hardware, but scale both rather
		// than assuming it — the assumption is one refactor away from being
		// silently wrong, and the cost is one multiply.
		for c := 0; c < 2; c++ {
			off := i*4 + c*2
			s := int32(int16(uint16(buf[off]) | uint16(buf[off+1])<<8))
			s = (s * g) >> 15
			buf[off] = byte(uint16(s) & 0xff)
			buf[off+1] = byte(uint16(s) >> 8)
		}
		m.advanceGain(1)
	}
}

// mixInto sums music into voice with saturation.
//
// Saturating rather than wrapping: an int16 overflow wraps a loud peak to
// full-scale opposite polarity, which is a far worse noise than the clipping
// it replaces. With music ducked ~18dB under a response that peaks well
// below full scale, the sum should not reach this often — but "should not"
// is not a reason to leave the wrap in.
func mixInto(dst, src []byte) {
	n := len(dst)
	if len(src) < n {
		n = len(src)
	}
	for i := 0; i+1 < n; i += 2 {
		a := int32(int16(uint16(dst[i]) | uint16(dst[i+1])<<8))
		b := int32(int16(uint16(src[i]) | uint16(src[i+1])<<8))
		s := a + b
		if s > math.MaxInt16 {
			s = math.MaxInt16
		} else if s < math.MinInt16 {
			s = math.MinInt16
		}
		dst[i] = byte(uint16(s) & 0xff)
		dst[i+1] = byte(uint16(s) >> 8)
	}
}
