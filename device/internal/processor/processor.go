// Package processor implements the per-period audio processing pipeline
// for the EchoMuse mic stream.
//
// Pipeline:
//
//	mono S16_LE → 80Hz high-pass/DC blocker → AGC → mono S16_LE
//
// RNNoise NS was removed 2026-07-12: it never ran correctly on-device
// (48kHz-native model fed 16kHz audio — P0-3) and noise suppression now
// lives controller-side (em_ns.py, DTLN) on the ASR-bound stream only,
// per the dumb-transducer architecture. With it went the speech-probability
// interlock that refined AGC release — that interlock was dead code
// whenever NS was disabled (the shipped state since v2.6.x), so AGC
// behaviour is unchanged in practice: release is gated on the stream's
// RMS speech flag alone.
package processor

import (
	"encoding/binary"
	"math"
)

const (
	// AGC parameters
	agcTargetRMS        = 0.08 // target RMS (~-22dBFS)
	agcMaxGain          = 20.0
	agcMinGain          = 0.5
	agcAttackPerPeriod  = 0.05  // at the intended 512-sample/32ms cadence
	agcReleasePerPeriod = 0.005 // slow release — avoids pumping
	highPassHz          = 80.0
	sampleRate          = 16000.0
	periodSamples       = 512.0
)

var (
	highPassAlpha = math.Exp(-2 * math.Pi * highPassHz / sampleRate)
	// Normalise the one-pole form to unity at Nyquist. Using alpha for both
	// terms (the common DC-blocker shorthand) imposes an unnecessary ~2% loss
	// even at 1kHz with an 80Hz corner.
	highPassInput = (1 + highPassAlpha) / 2
)

// Processor holds inter-period state for the audio pipeline.
type Processor struct {
	// AGC state
	agcGain float64

	// One-pole high-pass state, continuous across batches within a stream.
	hpX1 float64
	hpY1 float64
}

// New returns a Processor with sensible initial state.
func New() *Processor {
	return &Processor{
		agcGain: 1.0,
	}
}

// ResetAGC returns the AGC gain to unity and clears the high-pass history.
// Called at mic stream start so a
// contaminated gain (e.g. loud TTS echo driving it to agcMinGain via the
// always-active attack path, which then takes many seconds of speech-gated
// release to recover) cannot carry across voice turns or survive a mic
// restart.
func (p *Processor) ResetAGC() {
	p.agcGain = 1.0
	p.hpX1, p.hpY1 = 0, 0
}

// Destroy frees resources held by the Processor. Retained as a no-op so the
// lifecycle contract survives the RNNoise removal.
func (p *Processor) Destroy() {}

// HighPass removes DC and sub-speech rumble in place before VAD and AGC. It is
// deliberately separate from Process because VAD must see the cleaned signal.
func (p *Processor) HighPass(mono []byte) []byte {
	for i := 0; i+1 < len(mono); i += 2 {
		x := float64(int16(binary.LittleEndian.Uint16(mono[i:])))
		y := highPassAlpha*p.hpY1 + highPassInput*(x-p.hpX1)
		p.hpX1, p.hpY1 = x, y
		if y > 32767 {
			y = 32767
		} else if y < -32768 {
			y = -32768
		}
		binary.LittleEndian.PutUint16(mono[i:], uint16(int16(math.Round(y))))
	}
	return mono
}

// Process applies AGC to one batch of mono S16_LE audio in place.
// agcEnabled gates automatic gain control — when false, audio passes through
// at unity gain (agcGain state is preserved so re-enabling is smooth).
// speech should be true when VAD has detected speech — AGC release is
// frozen during silence to prevent noise floor amplification.
func (p *Processor) Process(mono []byte, agcEnabled bool, speech bool) []byte {
	if len(mono) == 0 || !agcEnabled {
		return mono
	}

	n := len(mono) / 2
	var sum float64
	for i := 0; i < n; i++ {
		s := float64(int16(binary.LittleEndian.Uint16(mono[i*2:]))) / 32768.0
		sum += s * s
	}
	rms := math.Sqrt(sum / float64(n))

	if rms > 1e-6 {
		target := agcTargetRMS / rms
		periods := float64(n) / periodSamples
		if target < p.agcGain {
			coefficient := 1 - math.Pow(1-agcAttackPerPeriod, periods)
			p.agcGain += coefficient * (target - p.agcGain)
		} else if speech {
			coefficient := 1 - math.Pow(1-agcReleasePerPeriod, periods)
			p.agcGain += coefficient * (target - p.agcGain)
		}
	}

	if p.agcGain > agcMaxGain {
		p.agcGain = agcMaxGain
	} else if p.agcGain < agcMinGain {
		p.agcGain = agcMinGain
	}

	for i := 0; i < n; i++ {
		s := float64(int16(binary.LittleEndian.Uint16(mono[i*2:]))) * p.agcGain
		if s > 32767 {
			s = 32767
		} else if s < -32768 {
			s = -32768
		}
		binary.LittleEndian.PutUint16(mono[i*2:], uint16(int16(math.Round(s))))
	}
	return mono
}
