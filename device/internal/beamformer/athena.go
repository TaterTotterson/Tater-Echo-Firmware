package beamformer

// Athena's mainline ALSA front end exposes four TLV320ADC5140 microphones as
// 96 kHz S32_LE. The samples are 24-bit data left-aligned in the 32-bit
// container. Each channel is filtered and decimated independently so the wake
// path can estimate their relative arrival times and form a coherent four-mic
// beam without inventing a physical channel order that has not been measured.
//
// The ordinary unlocked transport remains the conservative omni mix. A
// confidence-gated aligned beam feeds wake scoring, and the same delay vector
// is frozen for the accepted voice turn. Uncertain correlation, too few live
// channels, or a failed channel falls back to omni. Physical bearing reporting
// remains disabled until a tap test maps converter channels to positions.

import (
	"encoding/binary"
	"log"
	"math"
)

const (
	athenaChannels  = 4
	athenaRatio     = 6
	athenaFrameSize = athenaChannels * 4

	// TECHO5 measured Athena 5.89 dB below Crown after conversion. Keep the
	// same normalization so Tater's established 16 kHz mic gain sees the level
	// it was tuned against.
	athenaMicBoost = 5.89
	// 192 taps at 96 kHz gives the decimator a 1 ms group delay and roughly
	// 74 dB Blackman stop-band rejection, matching the pinned platform path.
	athenaTaps = 192

	// Real left/right captures on Athena measured a 1.0-1.4 sample span. Four
	// samples still cover about 86 mm of acoustic path difference while
	// rejecting the impossible long-lag peaks that diffuse room noise can
	// produce when the search is wider than the physical array.
	athenaMaxLag         = 4
	athenaHistorySamples = athenaMaxLag + 2
	athenaMinSignalRMS   = 1.0
	// The 96 kHz front end's measured idle floor becomes roughly 35-50 S16
	// units after decimation and its fixed 5.89 dB normalization. Requiring 70
	// keeps diffuse idle noise from steering while normal side speech measured
	// 80-300. The first speech batch can still acquire in 32 ms.
	athenaMinAlignmentRMS = 70.0
	athenaMinCorrelation  = 0.30
	athenaMinPeakMargin   = 0.02
	athenaAlignmentHold   = 10
	athenaCoherentPath    = 0
	athenaOmniPath        = 1
)

var athenaLowPass = func() [athenaTaps]float64 {
	var result [athenaTaps]float64
	const cutoff = 0.5 / athenaRatio
	center := float64(athenaTaps-1) / 2
	var sum float64
	for i := range result {
		x := float64(i) - center
		value := 2 * cutoff
		if x != 0 {
			value = math.Sin(2*math.Pi*cutoff*x) / (math.Pi * x)
		}
		angle := 2 * math.Pi * float64(i) / float64(athenaTaps-1)
		value *= 0.42 - 0.5*math.Cos(angle) + 0.08*math.Cos(2*angle)
		result[i] = value
		sum += value
	}
	boost := math.Pow(10, athenaMicBoost/20)
	for i := range result {
		result[i] = result[i] / sum * boost
	}
	return result
}()

type AthenaFrontEnd struct {
	// One FIR history per physical converter channel. firAt is the next slot
	// to write, which makes decimation O(channels*taps) per output sample
	// instead of shifting 192 samples at the 96 kHz input rate.
	firHistory [athenaChannels][athenaTaps]float64
	firAt      int
	phase      int

	channels  [athenaChannels][]float64
	history   [athenaChannels][athenaHistorySamples]float64
	healthy   [athenaChannels]bool
	healthyN  int
	signalRMS float64

	// arrival is each channel's measured arrival time relative to the first
	// healthy channel. Alignment delays earlier channels until the latest one.
	arrival        [athenaChannels]float64
	aligned        [athenaChannels]bool
	alignedN       int
	delayValid     bool
	alignmentAge   int
	steeringLocked bool

	outputPath      int
	delaySpan       float64
	lastConfidence  float64
	lastCorrelation float64
	lastWakePCM     []byte
	lastOmniPCM     []byte

	clipped          uint64
	clippedByChannel [7]uint64
}

func NewAthena() *AthenaFrontEnd {
	return &AthenaFrontEnd{outputPath: athenaOmniPath}
}

func (a *AthenaFrontEnd) Lock(enabled bool) {
	if enabled && a.hasAlignedPair() {
		a.steeringLocked = true
		log.Printf("[beam] Athena locked adaptive four-mic beam (span=%.2f samples, mics=%d)",
			a.delaySpan, a.alignedN)
	}
}

func (a *AthenaFrontEnd) PrepareSpeechLock()              { a.steeringLocked = false }
func (a *AthenaFrontEnd) LockCurrent(enabled bool)        { a.Lock(enabled) }
func (a *AthenaFrontEnd) Unlock()                         { a.steeringLocked = false }
func (*AthenaFrontEnd) EchoRefInto([]byte, []byte) []byte { return nil }

func (a *AthenaFrontEnd) Process(raw []byte, _ float64, gain float64) ([]byte, float64) {
	frames := a.decode(raw)
	if frames == 0 {
		return nil, -1
	}
	a.updateHealth()
	if !a.steeringLocked {
		a.updateAlignment()
	}

	// Cache the confidence-gated wake candidate before rememberHistory moves
	// the current batch into the cross-batch interpolation tail. DataClient
	// asks for this exact batch synchronously after Process returns.
	a.lastOmniPCM = a.extractOmni(a.lastOmniPCM[:0], gain)
	if a.hasAlignedPair() {
		a.lastWakePCM = a.extractAligned(a.lastWakePCM[:0], gain)
	} else {
		a.lastWakePCM = append(a.lastWakePCM[:0], a.lastOmniPCM...)
	}

	var mono []byte
	if a.steeringLocked && a.hasAlignedPair() {
		a.outputPath = athenaCoherentPath
		mono = a.extractAligned(nil, gain)
	} else {
		a.outputPath = athenaOmniPath
		mono = a.extractOmni(nil, gain)
	}
	a.rememberHistory()
	return mono, -1
}

// decode filters every microphone independently into the common 16 kHz
// domain. Linear filtering commutes with the old pre-filter average, so the
// omni result remains level-compatible while retaining the information needed
// to align channels for the wake and locked-turn beams.
func (a *AthenaFrontEnd) decode(raw []byte) int {
	frames := len(raw) / athenaFrameSize
	if frames == 0 {
		return 0
	}
	estimate := frames/athenaRatio + 1
	for channel := range a.channels {
		if cap(a.channels[channel]) < estimate {
			a.channels[channel] = make([]float64, 0, estimate)
		} else {
			a.channels[channel] = a.channels[channel][:0]
		}
	}
	for frame := 0; frame < frames; frame++ {
		base := frame * athenaFrameSize
		for channel := 0; channel < athenaChannels; channel++ {
			sample := int32(binary.LittleEndian.Uint32(raw[base+channel*4:]))
			a.firHistory[channel][a.firAt] = float64(sample) / 65536.0
		}
		a.firAt++
		if a.firAt == athenaTaps {
			a.firAt = 0
		}
		a.phase++
		if a.phase != athenaRatio {
			continue
		}
		a.phase = 0
		for channel := 0; channel < athenaChannels; channel++ {
			var filtered float64
			index := a.firAt - 1
			if index < 0 {
				index = athenaTaps - 1
			}
			for tap, coefficient := range athenaLowPass {
				filtered += coefficient * a.firHistory[channel][index]
				index--
				if index < 0 && tap+1 < athenaTaps {
					index = athenaTaps - 1
				}
			}
			a.channels[channel] = append(a.channels[channel], filtered)
		}
	}
	return len(a.channels[0])
}

func (a *AthenaFrontEnd) updateHealth() {
	frames := len(a.channels[0])
	if frames == 0 {
		a.healthyN = 0
		return
	}
	var rms [athenaChannels]float64
	for channel, samples := range a.channels {
		var mean float64
		for _, sample := range samples {
			mean += sample
		}
		mean /= float64(frames)
		var power float64
		for _, sample := range samples {
			d := sample - mean
			power += d * d
		}
		rms[channel] = math.Sqrt(power / float64(frames))
	}
	sorted := rms
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	median := 0.5 * (sorted[1] + sorted[2])
	a.signalRMS = median
	a.healthyN = 0
	for channel := range a.healthy {
		a.healthy[channel] = rms[channel] >= athenaMinSignalRMS
		if median >= athenaMinSignalRMS {
			a.healthy[channel] = a.healthy[channel] && rms[channel]*25 >= median && rms[channel] <= median*25
		}
		if a.healthy[channel] {
			a.healthyN++
		}
	}
}

func (a *AthenaFrontEnd) updateAlignment() {
	if a.healthyN < 2 || a.signalRMS < athenaMinAlignmentRMS || len(a.channels[0]) < 32 {
		a.ageAlignment()
		return
	}
	anchor := -1
	for channel, healthy := range a.healthy {
		if healthy {
			anchor = channel
			break
		}
	}
	if anchor < 0 {
		a.ageAlignment()
		return
	}

	var observed [athenaChannels]float64
	var accepted [athenaChannels]bool
	accepted[anchor] = true
	acceptedN := 1
	correlationSum, marginSum := 0.0, 0.0
	for channel := 0; channel < athenaChannels; channel++ {
		if channel == anchor || !a.healthy[channel] {
			continue
		}
		lag, correlation, margin, ok := athenaRelativeDelay(a.channels[anchor], a.channels[channel])
		if !ok {
			continue
		}
		observed[channel] = lag
		accepted[channel] = true
		acceptedN++
		correlationSum += correlation
		marginSum += margin
	}
	if acceptedN < 2 {
		a.ageAlignment()
		return
	}

	wasValid := a.delayValid
	if !a.delayValid {
		a.arrival = observed
	} else {
		for channel := range a.arrival {
			if accepted[channel] {
				a.arrival[channel] += 0.42 * (observed[channel] - a.arrival[channel])
			}
		}
	}
	a.aligned = accepted
	a.alignedN = acceptedN
	a.delayValid = true
	a.alignmentAge = 0
	a.updateDelaySpan()
	a.lastCorrelation = correlationSum / float64(acceptedN-1)
	a.lastConfidence = marginSum / float64(acceptedN-1)
	if !wasValid {
		log.Printf("[beam] Athena adaptive four-mic alignment ready: span=%.2f samples correlation=%.2f mics=%d",
			a.delaySpan, a.lastCorrelation, a.alignedN)
	}
}

func (a *AthenaFrontEnd) ageAlignment() {
	a.alignmentAge++
	if a.alignmentAge <= athenaAlignmentHold {
		return
	}
	a.delayValid = false
	a.alignedN = 0
	a.aligned = [athenaChannels]bool{}
	a.lastConfidence = 0
	a.lastCorrelation = 0
	a.delaySpan = 0
}

func (a *AthenaFrontEnd) updateDelaySpan() {
	first := true
	minimum, maximum := 0.0, 0.0
	for channel, aligned := range a.aligned {
		if !aligned || !a.healthy[channel] {
			continue
		}
		arrival := a.arrival[channel]
		if first {
			minimum, maximum, first = arrival, arrival, false
			continue
		}
		if arrival < minimum {
			minimum = arrival
		}
		if arrival > maximum {
			maximum = arrival
		}
	}
	if first {
		a.delaySpan = 0
		return
	}
	a.delaySpan = maximum - minimum
}

// athenaRelativeDelay searches first differences, which reject DC and slow
// room rumble while keeping speech onsets sharp. Positive lag means b arrives
// later than a. A parabolic peak gives the fractional delay used by the beam.
func athenaRelativeDelay(a, b []float64) (delay, correlation, margin float64, ok bool) {
	var correlations [2*athenaMaxLag + 1]float64
	for lag := -athenaMaxLag; lag <= athenaMaxLag; lag++ {
		startA, startB := 1, 1
		if lag < 0 {
			startA += -lag
		} else {
			startB += lag
		}
		count := len(a) - absInt(lag) - 1
		if len(b)-absInt(lag)-1 < count {
			count = len(b) - absInt(lag) - 1
		}
		if count < 16 {
			continue
		}
		var dot, powerA, powerB float64
		for n := 0; n < count; n++ {
			ai, bi := startA+n, startB+n
			x := a[ai] - a[ai-1]
			y := b[bi] - b[bi-1]
			dot += x * y
			powerA += x * x
			powerB += y * y
		}
		if powerA > 1 && powerB > 1 {
			correlations[lag+athenaMaxLag] = dot / math.Sqrt(powerA*powerB)
		}
	}
	bestIndex := 0
	for index := 1; index < len(correlations); index++ {
		if correlations[index] > correlations[bestIndex] {
			bestIndex = index
		}
	}
	correlation = correlations[bestIndex]
	runner := -1.0
	for index, value := range correlations {
		if index == bestIndex {
			continue
		}
		if value > runner {
			runner = value
		}
	}
	margin = correlation - runner
	delay = float64(bestIndex - athenaMaxLag)
	if bestIndex > 0 && bestIndex+1 < len(correlations) {
		left, center, right := correlations[bestIndex-1], correlation, correlations[bestIndex+1]
		denominator := left - 2*center + right
		if math.Abs(denominator) > 1e-9 {
			delay += clamp(0.5*(left-right)/denominator, -0.5, 0.5)
		}
	}
	ok = correlation >= athenaMinCorrelation && margin >= athenaMinPeakMargin
	return
}

func (a *AthenaFrontEnd) extractOmni(dst []byte, gain float64) []byte {
	frames := len(a.channels[0])
	dst = sizedAthenaPCM(dst, frames)
	for frame := 0; frame < frames; frame++ {
		var sum float64
		count := 0
		for channel := 0; channel < athenaChannels; channel++ {
			if a.healthyN > 0 && !a.healthy[channel] {
				continue
			}
			sum += a.channels[channel][frame]
			count++
		}
		if count == 0 {
			count = athenaChannels
			for channel := 0; channel < athenaChannels; channel++ {
				sum += a.channels[channel][frame]
			}
		}
		a.writeSample(dst, frame, sum*gain/float64(count), athenaOmniPath)
	}
	return dst
}

func (a *AthenaFrontEnd) extractAligned(dst []byte, gain float64) []byte {
	frames := len(a.channels[0])
	active := [athenaChannels]bool{}
	count := 0
	latest := -math.MaxFloat64
	for channel := 0; channel < athenaChannels; channel++ {
		active[channel] = a.aligned[channel] && a.healthy[channel]
		if active[channel] {
			count++
			if a.arrival[channel] > latest {
				latest = a.arrival[channel]
			}
		}
	}
	if count < 2 {
		return a.extractOmni(dst, gain)
	}
	dst = sizedAthenaPCM(dst, frames)
	for frame := 0; frame < frames; frame++ {
		var sum float64
		for channel := 0; channel < athenaChannels; channel++ {
			if !active[channel] {
				continue
			}
			delay := latest - a.arrival[channel]
			sum += delayedSample(a.channels[channel], a.history[channel][:], frame, delay)
		}
		a.writeSample(dst, frame, sum*gain/float64(count), athenaCoherentPath)
	}
	return dst
}

func (a *AthenaFrontEnd) hasAlignedPair() bool {
	if !a.delayValid {
		return false
	}
	count := 0
	for channel, aligned := range a.aligned {
		if aligned && a.healthy[channel] {
			count++
		}
	}
	return count >= 2
}

func sizedAthenaPCM(dst []byte, frames int) []byte {
	size := frames * 2
	if cap(dst) < size {
		return make([]byte, size)
	}
	return dst[:size]
}

func (a *AthenaFrontEnd) writeSample(out []byte, frame int, value float64, path int) {
	value = math.Round(value)
	if value > math.MaxInt16 {
		value = math.MaxInt16
		a.clipped++
		a.clippedByChannel[path]++
	} else if value < math.MinInt16 {
		value = math.MinInt16
		a.clipped++
		a.clippedByChannel[path]++
	}
	binary.LittleEndian.PutUint16(out[frame*2:], uint16(int16(value)))
}

func (a *AthenaFrontEnd) rememberHistory() {
	for channel, samples := range a.channels {
		if len(samples) >= athenaHistorySamples {
			copy(a.history[channel][:], samples[len(samples)-athenaHistorySamples:])
			continue
		}
		shift := athenaHistorySamples - len(samples)
		copy(a.history[channel][:shift], a.history[channel][len(samples):])
		copy(a.history[channel][shift:], samples)
	}
}

// Athena exposes one adaptive wake beam. Direction is an acoustic path id,
// not a claimed physical angle; exact left/right/front/back reporting waits
// for the physical channel map.
func (*AthenaFrontEnd) WakeBeamCount() int { return 1 }

func (a *AthenaFrontEnd) WakeDirection() (int, bool) {
	return athenaCoherentPath, a.hasAlignedPair()
}

func (a *AthenaFrontEnd) WakeBeams(raw []byte, gain float64) []WakeBeam {
	return []WakeBeam{a.WakeBeam(raw, athenaCoherentPath, gain)}
}

func (a *AthenaFrontEnd) WakeBeam(_ []byte, direction int, _ float64) WakeBeam {
	if direction != athenaCoherentPath || !a.hasAlignedPair() {
		return WakeBeam{Direction: athenaOmniPath, Angle: -1, PCM: append([]byte(nil), a.lastOmniPCM...)}
	}
	return WakeBeam{Direction: athenaCoherentPath, Angle: -1, PCM: append([]byte(nil), a.lastWakePCM...)}
}

func (a *AthenaFrontEnd) LockWakeDirection(direction int, enabled bool) {
	if direction == athenaCoherentPath {
		a.Lock(enabled)
	}
}

func (a *AthenaFrontEnd) ClippedSamples() uint64      { return a.clipped }
func (a *AthenaFrontEnd) ClippedByChannel() [7]uint64 { return a.clippedByChannel }
func (a *AthenaFrontEnd) OutputChannel() int          { return a.outputPath }
func (a *AthenaFrontEnd) Diagnostics() Diagnostics {
	return Diagnostics{
		OutputChannel:      a.outputPath,
		Confidence:         a.lastConfidence,
		Spatial:            a.lastCorrelation,
		Calibrated:         a.delayValid,
		DelaySamples:       a.delaySpan,
		NoiseGain:          1,
		Coherence:          a.lastCorrelation,
		HealthyMicChannels: a.healthyN,
	}
}
