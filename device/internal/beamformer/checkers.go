package beamformer

// Checkers' measured capture stream is four-channel S24_3LE at 16 kHz:
//
//   ch0  screen-right microphone
//   ch1  screen-left microphone
//   ch2  inactive (bit-exact zero)
//   ch3  inactive (bit-exact zero)
//
// Physical captures on 2026-10-04 measured opposing side speech at -0.91 and
// +0.81 samples, with centred speech at 0.00 samples.  The resulting effective
// aperture is about 18 mm.  Two microphones still cannot resolve the
// front/back ambiguity, so this front end deliberately does not publish a
// 0-360 degree screen bearing.  It can, however, form stable left and right
// wake beams and carry the winning acoustic path into the voice turn.

import (
	"encoding/binary"
	"log"
	"math"
)

const (
	checkersChannels           = 4
	checkersFrameSize          = checkersChannels * byteSample
	checkersMaxLag             = 2 // measured aperture is 0.86 samples; retain reflection margin
	checkersHistorySamples     = checkersMaxLag + 2
	checkersBeamDelaySamples   = 0.86
	checkersLeftPath           = 0
	checkersRightPath          = 1
	checkersOmniPath           = 2
	checkersMinSignalRMS       = 32.0 // raw S24 units; rejects digital silence
	checkersMinCorrelation     = 0.18
	checkersMinPeakMargin      = 0.025
	checkersCalibrationMin     = 24 // accepted 32 ms diffuse-noise observations
	checkersWarmupBlocks       = 12 // suppress only after ~384 ms of observation
	checkersWakeNoiseMinGain   = 0.68
	checkersLockedNoiseMinGain = 0.48
)

// CheckersFrontEnd decodes the measured four-channel S24_3LE/16 kHz stream.
// Fire OS routes the two fitted microphones to ADC_A left/right; channels 2
// and 3 are bit-exact zero.  State is owned by the mic goroutine and guarded by
// DataClient.pipeMu during stream replacement.
type CheckersFrontEnd struct {
	clipped          uint64
	clippedByChannel [7]uint64

	left     []float64
	right    []float64
	combined []float64
	history  [2][checkersHistorySamples]float64

	// logLevelRatio is ln(left RMS/right RMS).  Learning is limited to
	// low-coherence room sound, where source direction cannot masquerade as
	// microphone sensitivity mismatch.  Symmetric correction preserves the
	// pair's geometric-mean level and is bounded to +/-6 dB per ADC path.
	logLevelRatio  float64
	calibrationObs int
	calibrated     bool

	delaySamples float64
	delayValid   bool
	// steeringLocked freezes the inter-mic delay chosen from the user's
	// speech onset. Without it the pair happily re-aims at television dialogue
	// after the user pauses, undoing the spatial rejection we built the pair
	// for. Wake listening remains adaptive; only an active voice turn locks.
	steeringLocked bool
	lockedPath     int
	outputPath     int
	noiseGain      float64
	blocksSeen     int

	lastCorrelation float64
	lastPeakMargin  float64
	lastCoherence   float64
	healthyChannels int
	leftHealthy     bool
	rightHealthy    bool
	periodDelay     float64
	periodDelayOK   bool
	periodActivity  float64
}

// NewCheckers returns a two-mic front end with unity calibration and no
// startup attenuation.  Calibration is automatic and never blocks capture.
func NewCheckers() *CheckersFrontEnd {
	return &CheckersFrontEnd{
		noiseGain:       1,
		healthyChannels: 2,
		leftHealthy:     true,
		rightHealthy:    true,
		lockedPath:      checkersOmniPath,
		outputPath:      checkersOmniPath,
	}
}

func (c *CheckersFrontEnd) Lock(enabled bool) {
	if enabled && c.delayValid {
		c.steeringLocked = true
		c.lockedPath = checkersPathForDelay(c.delaySamples)
	}
}

// LockWakeDirection carries the exact fixed beam that crossed microWakeWord
// into the bounded voice turn.  The direction values are internal acoustic
// path identities, not an asserted 360-degree bearing.
func (c *CheckersFrontEnd) LockWakeDirection(direction int, enabled bool) {
	if !enabled {
		return
	}
	switch direction {
	case checkersLeftPath:
		c.delaySamples = -checkersBeamDelaySamples
	case checkersRightPath:
		c.delaySamples = checkersBeamDelaySamples
	default:
		return
	}
	c.delayValid = true
	c.steeringLocked = true
	c.lockedPath = direction
	log.Printf("[beam] Checkers locked winning %s wake beam (delay=%+.2f samples)", checkersPathName(direction), c.delaySamples)
}

// PrepareSpeechLock deliberately leaves the pair adaptive while waiting for
// the first near-end speech. LockCurrent then freezes the delay estimated from
// that same speech batch, matching Biscuit's continued-chat behavior.
func (c *CheckersFrontEnd) PrepareSpeechLock() {
	c.steeringLocked = false
	c.delayValid = false
	c.lockedPath = checkersOmniPath
	c.outputPath = checkersOmniPath
}
func (c *CheckersFrontEnd) LockCurrent(enabled bool) {
	c.Lock(enabled)
}
func (c *CheckersFrontEnd) Unlock() {
	c.steeringLocked = false
	c.lockedPath = checkersOmniPath
	c.outputPath = checkersOmniPath
}

func (c *CheckersFrontEnd) ensure(frames int) {
	if cap(c.left) < frames {
		c.left = make([]float64, frames)
		c.right = make([]float64, frames)
		c.combined = make([]float64, frames)
		return
	}
	c.left = c.left[:frames]
	c.right = c.right[:frames]
	c.combined = c.combined[:frames]
}

func (c *CheckersFrontEnd) Process(raw []byte, _ float64, gain float64) ([]byte, float64) {
	frames := len(raw) / checkersFrameSize
	if frames == 0 {
		return nil, -1
	}
	if c.noiseGain == 0 { // keep a useful zero-value for package-local callers
		c.noiseGain = 1
	}
	c.outputPath = checkersOmniPath
	c.ensure(frames)
	for i := 0; i < frames; i++ {
		base := i * checkersFrameSize
		c.left[i] = float64(decodePackedS24(raw[base : base+3]))
		c.right[i] = float64(decodePackedS24(raw[base+3 : base+6]))
	}

	var delayWeighted, correlationWeighted, marginWeighted, totalWeight float64
	for start := 0; start < frames; start += periodFrames {
		end := start + periodFrames
		if end > frames {
			end = frames
		}
		c.processBlock(c.left[start:end], c.right[start:end], c.combined[start:end])
		if c.periodDelayOK {
			// The ALSA producer delivers five periods at once. Preserve the
			// speech-bearing estimate across that whole batch instead of letting
			// a quiet final period overwrite it immediately before LockCurrent.
			weight := c.periodActivity * (0.10 + c.lastPeakMargin)
			delayWeighted += c.periodDelay * weight
			correlationWeighted += c.lastCorrelation * weight
			marginWeighted += c.lastPeakMargin * weight
			totalWeight += weight
		}
	}
	if !c.steeringLocked && totalWeight > 0 {
		c.delaySamples = delayWeighted / totalWeight
		c.delayValid = true
		c.lastCorrelation = correlationWeighted / totalWeight
		c.lastPeakMargin = marginWeighted / totalWeight
	}
	if c.steeringLocked && c.healthyChannels == 2 {
		c.outputPath = c.lockedPath
	}

	out := make([]byte, frames*2)
	for i, sample := range c.combined {
		// packed S24 to S16 is /256; gain is applied before quantisation so
		// quiet speech retains the captured 24-bit resolution.
		value := math.Round(sample * gain / 256.0)
		if value > 32767 {
			value = 32767
			c.clipped++
			c.clippedByChannel[0]++
		} else if value < -32768 {
			value = -32768
			c.clipped++
			c.clippedByChannel[0]++
		}
		binary.LittleEndian.PutUint16(out[i*2:], uint16(int16(value)))
	}
	return out, -1
}

func (c *CheckersFrontEnd) processBlock(left, right, out []float64) {
	c.periodDelayOK = false
	c.periodActivity = 0
	if len(left) == 0 {
		return
	}
	if len(left) < 16 {
		// Tiny reads do not contain enough information for a correlation or
		// channel-health decision.  Preserve the old, unsurprising average;
		// production ALSA periods are 512 frames.
		for i := range out {
			out[i] = 0.5 * (left[i] + right[i])
		}
		c.updateHistory(left, right)
		return
	}
	c.blocksSeen++

	meanL, meanR, rmsL, rmsR, zeroCorrelation := channelStats(left, right)
	c.periodActivity = math.Max(rmsL, rmsR)
	// Split the correction symmetrically, so a 2:1 ADC mismatch becomes
	// sqrt(1/2):sqrt(2) rather than boosting only the quieter/noisier input.
	leftScale := math.Exp(-0.5 * c.logLevelRatio)
	rightScale := math.Exp(0.5 * c.logLevelRatio)
	leftScale = clamp(leftScale, 0.5, 2)
	rightScale = clamp(rightScale, 0.5, 2)

	leftHealthy := rmsL >= checkersMinSignalRMS
	rightHealthy := rmsR >= checkersMinSignalRMS
	if leftHealthy && rightHealthy {
		ratio := rmsL / rmsR
		leftHealthy = ratio > 0.04
		rightHealthy = ratio < 25
	}
	c.leftHealthy, c.rightHealthy = leftHealthy, rightHealthy

	switch {
	case leftHealthy && !rightHealthy:
		c.healthyChannels = 1
		c.delayValid = false
		c.noiseGain = 1
		for i := range out {
			out[i] = left[i] * leftScale
		}
		c.updateHistory(left, right)
		return
	case rightHealthy && !leftHealthy:
		c.healthyChannels = 1
		c.delayValid = false
		c.noiseGain = 1
		for i := range out {
			out[i] = right[i] * rightScale
		}
		c.updateHistory(left, right)
		return
	case !leftHealthy && !rightHealthy:
		// Preserve true silence.  Suppressing or calibrating quantisation dust
		// creates more artefacts than it removes.
		c.healthyChannels = 0
		for i := range out {
			out[i] = 0
		}
		c.updateHistory(left, right)
		return
	}
	c.healthyChannels = 2

	correlations := c.lagCorrelations(left, right, leftScale, rightScale)
	bestIndex := 0
	for i := 1; i < len(correlations); i++ {
		if correlations[i] > correlations[bestIndex] {
			bestIndex = i
		}
	}
	bestLag := bestIndex - checkersMaxLag
	best := correlations[bestIndex]
	runner := -1.0
	for i, corr := range correlations {
		if absInt(i-bestIndex) <= 1 {
			continue
		}
		if corr > runner {
			runner = corr
		}
	}
	margin := best - runner
	c.lastCorrelation = clamp(best, 0, 1)
	c.lastPeakMargin = clamp(margin, 0, 1)
	estimatedLag := float64(bestLag)
	if bestIndex > 0 && bestIndex+1 < len(correlations) {
		a, b, d := correlations[bestIndex-1], best, correlations[bestIndex+1]
		denominator := a - 2*b + d
		if math.Abs(denominator) > 1e-9 {
			estimatedLag += clamp(0.5*(a-d)/denominator, -0.5, 0.5)
		}
	}
	if !c.steeringLocked && best >= checkersMinCorrelation && margin >= checkersMinPeakMargin {
		c.periodDelay = estimatedLag
		c.periodDelayOK = true
		if !c.delayValid {
			c.delaySamples = estimatedLag
			c.delayValid = true
		} else {
			// About 70 ms to settle at 32 ms periods: quick enough for speech
			// onset, slow enough not to zipper between adjacent sample delays.
			c.delaySamples += 0.38 * (estimatedLag - c.delaySamples)
		}
	}

	// Diffuse room noise is direction-neutral.  A strongly coherent source
	// with near-zero TDOA is also useful: it is effectively equidistant from
	// both mics and lets quiet rooms calibrate when they contain too little
	// diffuse sound.  Directional speech (non-zero delay) is never allowed to
	// teach the level trim.
	diffuseCalibration := math.Abs(zeroCorrelation) < 0.28 && best < 0.45
	broadsideCalibration := best >= 0.55 && math.Abs(estimatedLag) < 0.75
	if rmsL >= checkersMinSignalRMS && rmsR >= checkersMinSignalRMS && (diffuseCalibration || broadsideCalibration) {
		c.observeLevelBalance(rmsL / rmsR)
	}

	var sumPower, differencePower float64
	meanSum := 0.5 * (meanL*leftScale + meanR*rightScale)
	meanDifference := 0.5 * (meanL*leftScale - meanR*rightScale)
	for i := range out {
		var l, r float64
		switch {
		case c.delayValid && c.delaySamples > 0:
			l = delayedSample(left, c.history[0][:], i, c.delaySamples) * leftScale
			r = right[i] * rightScale
		case c.delayValid && c.delaySamples < 0:
			l = left[i] * leftScale
			r = delayedSample(right, c.history[1][:], i, -c.delaySamples) * rightScale
		default:
			l = left[i] * leftScale
			r = right[i] * rightScale
		}
		sum := 0.5 * (l + r)
		difference := 0.5 * (l - r)
		out[i] = sum
		// High-pass runs immediately after this front end.  Remove the same
		// block DC here only for the coherence estimate so ADC bias cannot be
		// mistaken for perfectly coherent speech.
		acSum := sum - meanSum
		acDifference := difference - meanDifference
		sumPower += acSum * acSum
		differencePower += acDifference * acDifference
	}

	coherence := 0.0
	if sumPower+differencePower > 1 {
		coherence = clamp((sumPower-differencePower)/(sumPower+differencePower), 0, 1)
	}
	c.lastCoherence = coherence
	targetGain := 1.0
	if c.blocksSeen >= checkersWarmupBlocks {
		// The aligned sum is the desired coherent field; the difference is a
		// free estimate of diffuse/interfering sound.  The shallow floor is
		// deliberate: average+postfilter improves diffuse noise by roughly
		// 6 dB while never carving quiet wake-word phonemes into silence.
		shaped := coherence * coherence * (3 - 2*coherence)
		floor := checkersWakeNoiseMinGain
		if c.steeringLocked {
			// Once the user's bearing is frozen, incoherent energy is explicitly
			// off-beam. Apply stronger attenuation to the STT-bound turn without
			// making wake-word audio more fragile before the lock exists.
			floor = checkersLockedNoiseMinGain
		}
		targetGain = floor + (1-floor)*shaped
	}
	oldGain := c.noiseGain
	coefficient := 0.08 // enter suppression slowly
	if targetGain > c.noiseGain {
		coefficient = 0.72 // restore speech onsets within one period
	}
	c.noiseGain += coefficient * (targetGain - c.noiseGain)
	for i := range out {
		fraction := float64(i+1) / float64(len(out))
		periodGain := oldGain + fraction*(c.noiseGain-oldGain)
		out[i] *= periodGain
	}
	c.updateHistory(left, right)
}

func (c *CheckersFrontEnd) observeLevelBalance(ratio float64) {
	observed := math.Log(ratio)
	// A source very close to one mic is not a calibration signal.  The bound
	// also prevents a failing channel from teaching a huge gain.
	observed = clamp(observed, -math.Log(2), math.Log(2))
	alpha := 0.025
	if c.calibrationObs < checkersCalibrationMin {
		alpha = 0.10
	}
	c.logLevelRatio += alpha * (observed - c.logLevelRatio)
	c.calibrationObs++
	if !c.calibrated && c.calibrationObs >= checkersCalibrationMin {
		c.calibrated = true
		log.Printf("[beam] Checkers mic pair calibrated: level_balance=%.1fdB", 20*c.logLevelRatio/math.Log(10))
	}
}

// lagCorrelations performs a small GCC-like search on first differences.
// Differencing removes DC/slow room rumble and gives speech a sharper peak
// than raw waveform correlation.  Positive lag means right arrives later.
func (c *CheckersFrontEnd) lagCorrelations(left, right []float64, leftScale, rightScale float64) [2*checkersMaxLag + 1]float64 {
	var result [2*checkersMaxLag + 1]float64
	for lag := -checkersMaxLag; lag <= checkersMaxLag; lag++ {
		startL, startR := 1, 1
		if lag < 0 {
			startL += -lag
		} else {
			startR += lag
		}
		count := len(left) - absInt(lag) - 1
		if count < 16 {
			continue
		}
		var dot, powerL, powerR float64
		for n := 0; n < count; n++ {
			li, ri := startL+n, startR+n
			l := (left[li] - left[li-1]) * leftScale
			r := (right[ri] - right[ri-1]) * rightScale
			dot += l * r
			powerL += l * l
			powerR += r * r
		}
		if powerL > 1 && powerR > 1 {
			result[lag+checkersMaxLag] = dot / math.Sqrt(powerL*powerR)
		}
	}
	return result
}

func channelStats(left, right []float64) (meanL, meanR, rmsL, rmsR, correlation float64) {
	for i := range left {
		meanL += left[i]
		meanR += right[i]
	}
	meanL /= float64(len(left))
	meanR /= float64(len(right))
	var powerL, powerR, dot float64
	for i := range left {
		l, r := left[i]-meanL, right[i]-meanR
		powerL += l * l
		powerR += r * r
		dot += l * r
	}
	rmsL = math.Sqrt(powerL / float64(len(left)))
	rmsR = math.Sqrt(powerR / float64(len(right)))
	if powerL > 1 && powerR > 1 {
		correlation = dot / math.Sqrt(powerL*powerR)
	}
	return
}

func delayedSample(current, history []float64, index int, delay float64) float64 {
	position := float64(index) - delay
	base := int(math.Floor(position))
	fraction := position - float64(base)
	a := historicalSample(current, history, base)
	b := historicalSample(current, history, base+1)
	return a + fraction*(b-a)
}

func historicalSample(current, history []float64, index int) float64 {
	if index >= 0 {
		if index >= len(current) {
			return current[len(current)-1]
		}
		return current[index]
	}
	historyIndex := len(history) + index
	if historyIndex < 0 {
		historyIndex = 0
	}
	return history[historyIndex]
}

func (c *CheckersFrontEnd) updateHistory(left, right []float64) {
	for channel, samples := range [][]float64{left, right} {
		if len(samples) >= checkersHistorySamples {
			copy(c.history[channel][:], samples[len(samples)-checkersHistorySamples:])
			continue
		}
		shift := checkersHistorySamples - len(samples)
		copy(c.history[channel][:shift], c.history[channel][len(samples):])
		copy(c.history[channel][shift:], samples)
	}
}

func clamp(value, minimum, maximum float64) float64 {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func checkersPathForDelay(delay float64) int {
	if delay < -0.25 {
		return checkersLeftPath
	}
	if delay > 0.25 {
		return checkersRightPath
	}
	return checkersOmniPath
}

func checkersPathName(path int) string {
	switch path {
	case checkersLeftPath:
		return "left"
	case checkersRightPath:
		return "right"
	default:
		return "omni"
	}
}

// WakeBeamCount uses one persistent neural scorer for each physically measured
// side of the array.  Centre/front speech remains coherent in both broad beams.
func (*CheckersFrontEnd) WakeBeamCount() int { return 2 }

func (c *CheckersFrontEnd) WakeBeams(raw []byte, gain float64) []WakeBeam {
	if c.healthyChannels < 2 {
		return []WakeBeam{{Direction: checkersOmniPath, Angle: -1, PCM: c.extractLiveRaw(raw, gain)}}
	}
	return []WakeBeam{
		c.WakeBeam(raw, checkersLeftPath, gain),
		c.WakeBeam(raw, checkersRightPath, gain),
	}
}

func (c *CheckersFrontEnd) WakeBeam(raw []byte, direction int, gain float64) WakeBeam {
	delay := 0.0
	switch direction {
	case checkersLeftPath:
		delay = -checkersBeamDelaySamples
	case checkersRightPath:
		delay = checkersBeamDelaySamples
	default:
		direction = checkersOmniPath
	}
	return WakeBeam{Direction: direction, Angle: -1, PCM: c.extractSteeredRaw(raw, delay, direction, gain)}
}

func (c *CheckersFrontEnd) extractSteeredRaw(raw []byte, delay float64, path int, gain float64) []byte {
	frames := len(raw) / checkersFrameSize
	if frames == 0 {
		return nil
	}
	leftScale := clamp(math.Exp(-0.5*c.logLevelRatio), 0.5, 2)
	rightScale := clamp(math.Exp(0.5*c.logLevelRatio), 0.5, 2)
	leftDelay, rightDelay := 0.0, 0.0
	if delay > 0 {
		leftDelay = delay
	} else {
		rightDelay = -delay
	}
	out := make([]byte, frames*2)
	for frame := 0; frame < frames; frame++ {
		left := checkersRawDelayed(raw, frames, frame, 0, leftDelay) * leftScale
		right := checkersRawDelayed(raw, frames, frame, 1, rightDelay) * rightScale
		c.writeRawSample(out, frame, 0.5*(left+right)*gain/256, path)
	}
	return out
}

func (c *CheckersFrontEnd) extractLiveRaw(raw []byte, gain float64) []byte {
	frames := len(raw) / checkersFrameSize
	out := make([]byte, frames*2)
	if frames == 0 || (!c.leftHealthy && !c.rightHealthy) {
		return out
	}
	channel, scale := 0, clamp(math.Exp(-0.5*c.logLevelRatio), 0.5, 2)
	if !c.leftHealthy {
		channel, scale = 1, clamp(math.Exp(0.5*c.logLevelRatio), 0.5, 2)
	}
	for frame := 0; frame < frames; frame++ {
		base := frame*checkersFrameSize + channel*byteSample
		c.writeRawSample(out, frame, float64(decodePackedS24(raw[base:]))*scale*gain/256, checkersOmniPath)
	}
	return out
}

func checkersRawDelayed(raw []byte, frames, frame, channel int, delay float64) float64 {
	position := float64(frame) - delay
	base := int(math.Floor(position))
	fraction := position - float64(base)
	if base < 0 {
		base, fraction = 0, 0
	}
	next := base + 1
	if next >= frames {
		next = frames - 1
	}
	aOffset := base*checkersFrameSize + channel*byteSample
	bOffset := next*checkersFrameSize + channel*byteSample
	a := float64(decodePackedS24(raw[aOffset:]))
	b := float64(decodePackedS24(raw[bOffset:]))
	return a + fraction*(b-a)
}

func (c *CheckersFrontEnd) writeRawSample(out []byte, frame int, sample float64, path int) {
	value := math.Round(sample)
	if value > 32767 {
		value = 32767
		c.clipped++
		c.clippedByChannel[path]++
	} else if value < -32768 {
		value = -32768
		c.clipped++
		c.clippedByChannel[path]++
	}
	binary.LittleEndian.PutUint16(out[frame*2:], uint16(int16(value)))
}

// Checkers has no verified in-band playback-reference channel. The shared
// software speaker tap remains active, so AEC still has a safe far-end source.
func (*CheckersFrontEnd) EchoRefInto([]byte, []byte) []byte { return nil }

func (c *CheckersFrontEnd) ClippedSamples() uint64      { return c.clipped }
func (c *CheckersFrontEnd) ClippedByChannel() [7]uint64 { return c.clippedByChannel }
func (c *CheckersFrontEnd) OutputChannel() int          { return c.outputPath }
func (c *CheckersFrontEnd) Diagnostics() Diagnostics {
	return Diagnostics{
		OutputChannel:      c.outputPath,
		Confidence:         c.lastCorrelation,
		Spatial:            c.lastPeakMargin,
		Calibrated:         c.calibrated,
		DelaySamples:       c.delaySamples,
		LevelBalanceDB:     20 * c.logLevelRatio / math.Log(10),
		NoiseGain:          c.noiseGain,
		Coherence:          c.lastCoherence,
		HealthyMicChannels: c.healthyChannels,
	}
}
