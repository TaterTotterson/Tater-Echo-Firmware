package beamformer

// Rook's measured capture stream is six-channel S24_3LE at 16 kHz:
//
//   ch0  front-right microphone
//   ch1  rear-right microphone
//   ch2  rear-left microphone
//   ch3  front-left microphone
//   ch4  playback loopback (used as the hardware AEC reference when proven)
//   ch5  second playback loopback
//
// The microphone map and aperture were measured on a physical 2017 Echo Spot
// on 2026-10-04. Cardinal speech gives about one sample of time difference at
// 16 kHz, which is a compact ~22 mm x 20 mm square. This is deliberately its
// own geometry; borrowing Biscuit's 72 mm circular array would steer Rook in
// the wrong direction.

import (
	"encoding/binary"
	"log"
	"math"
)

const (
	rookChannels  = 6
	rookMics      = 4
	rookFrameSize = rookChannels * byteSample
	rookRefCh     = 4

	rookDirections = 4
	rookOmniPath   = 4 // AEC path kept separate from the four steered beams

	// Half dimensions inferred from the measured cardinal TDOA. At 16 kHz,
	// 22 mm right-to-left is 1.03 samples and 20 mm front-to-back is 0.93.
	rookHalfWidthMetres = 0.011
	rookHalfDepthMetres = 0.010
	rookMaxSpatialLag   = 3
	rookHistorySamples  = 4

	rookMinSignalRMS    = 32.0
	rookMinSpatialScore = 0.10
	rookMinConfidence   = 0.08
	rookNoiseAlpha      = 0.995
)

var rookAngles = [rookDirections]float64{0, 90, 180, 270}

// Coordinates use +x toward the device's right and +y toward the screen.
// Angles are clockwise from the screen/front, matching Tater's DOA contract.
var rookMicPositions = [rookMics][2]float64{
	{rookHalfWidthMetres, rookHalfDepthMetres},   // ch0 front-right
	{rookHalfWidthMetres, -rookHalfDepthMetres},  // ch1 rear-right
	{-rookHalfWidthMetres, -rookHalfDepthMetres}, // ch2 rear-left
	{-rookHalfWidthMetres, rookHalfDepthMetres},  // ch3 front-left
}

// RookFrontEnd performs four-microphone SRP-style direction estimation and
// fractional delay-and-sum pickup. State belongs to DataClient's mic goroutine
// and is protected by its pipeMu during stream replacement.
type RookFrontEnd struct {
	clipped          uint64
	clippedByChannel [7]uint64

	channels  [rookMics][]float32
	hf        [rookMics][]float32
	history   [rookMics][rookHistorySamples]float32
	healthy   [rookMics]bool
	healthyN  int
	levelLog  [rookMics]float64
	levelGain [rookMics]float64
	levelObs  int

	lockedDirection int
	trackSpeech     bool
	outputPath      int
	visualDirection int
	visualAngle     float64

	wakeDirections [2]int
	wakeReady      bool

	batchSpatial    [rookDirections]float64
	batchActivity   float64
	batchDirection  int
	batchConfidence float64
	noisePower      float64
	noiseReady      int

	lastConfidence float64
	lastSpatial    float64
	lastCoherence  float64
	playbackActive bool
}

func NewRook() *RookFrontEnd {
	r := &RookFrontEnd{
		lockedDirection: -1,
		outputPath:      rookOmniPath,
		visualDirection: -1,
		visualAngle:     -1,
		wakeDirections:  [2]int{0, 2},
	}
	for channel := range r.levelGain {
		r.levelGain[channel] = 1
	}
	return r
}

func (r *RookFrontEnd) Lock(enabled bool) {
	r.trackSpeech = false
	if !enabled {
		log.Printf("[beam] Rook lock disabled — staying on four-mic omni")
		return
	}
	best, confidence := r.batchDirection, r.batchConfidence
	score := r.batchSpatial[best]
	if score < rookMinSpatialScore || confidence < rookMinConfidence {
		log.Printf("[beam] Rook bearing uncertain (score=%.2f confidence=%.2f) — staying omni", score, confidence)
		return
	}
	r.lockDirection(best, "current array")
}

// LockWakeDirection carries the exact beam that crossed microWakeWord into
// the voice turn. Direction rookOmniPath is the degraded one-mic/omni fallback.
func (r *RookFrontEnd) LockWakeDirection(direction int, enabled bool) {
	r.trackSpeech = false
	if !enabled || direction < 0 || direction >= rookDirections {
		return
	}
	r.lockDirection(direction, "winning wake beam")
}

func (r *RookFrontEnd) lockDirection(direction int, reason string) {
	r.lockedDirection = direction
	r.visualDirection = direction
	r.visualAngle = rookAngles[direction]
	log.Printf("[beam] Rook locked %s at %.0f°", reason, rookAngles[direction])
}

// Initial and continued-chat turns share this same acquisition path: release
// any previous beam, expose live DOA, then let LockCurrent freeze the first
// speech-bearing batch.
func (r *RookFrontEnd) PrepareSpeechLock() {
	r.Unlock()
	r.trackSpeech = true
}

func (r *RookFrontEnd) LockCurrent(enabled bool) {
	if !enabled {
		return
	}
	best, confidence := r.batchDirection, r.batchConfidence
	score := r.batchSpatial[best]
	if score < rookMinSpatialScore || confidence < rookMinConfidence {
		log.Printf("[beam] Rook speech bearing uncertain (score=%.2f confidence=%.2f) — staying omni", score, confidence)
		return
	}
	r.trackSpeech = false
	r.lockDirection(best, "speech onset")
}

func (r *RookFrontEnd) Unlock() {
	r.lockedDirection = -1
	r.trackSpeech = false
	r.outputPath = rookOmniPath
}

func (r *RookFrontEnd) ensure(frames int) {
	for channel := 0; channel < rookMics; channel++ {
		if cap(r.channels[channel]) < frames {
			r.channels[channel] = make([]float32, frames)
			r.hf[channel] = make([]float32, frames)
			continue
		}
		r.channels[channel] = r.channels[channel][:frames]
		r.hf[channel] = r.hf[channel][:frames]
	}
}

func (r *RookFrontEnd) Process(raw []byte, steerAngle float64, gain float64) ([]byte, float64) {
	frames := len(raw) / rookFrameSize
	if frames == 0 {
		return nil, -1
	}
	r.outputPath = rookOmniPath
	if frames < 16 {
		r.healthyN = 0
		for channel := 0; channel < rookMics; channel++ {
			r.healthy[channel] = false
			for frame := 0; frame < frames; frame++ {
				base := frame*rookFrameSize + channel*byteSample
				if decodePackedS24(raw[base:]) != 0 {
					r.healthy[channel] = true
					break
				}
			}
			if r.healthy[channel] {
				r.healthyN++
			}
		}
		return r.extractOmniRaw(raw, gain), -1
	}

	r.ensure(frames)
	r.decode(raw)
	defer r.rememberHistory()
	r.bandDiff()
	r.batchSpatial = [rookDirections]float64{}
	r.batchActivity = 0
	r.playbackActive = false
	periods := 0
	var votes [rookDirections]int

	for start := 0; start < frames; start += periodFrames {
		end := start + periodFrames
		if end > frames {
			end = frames
		}
		if end-start < 16 {
			continue
		}
		farActive := rookPlaybackRefActive(raw, start, end)
		r.playbackActive = r.playbackActive || farActive
		spatial := r.spatialScores(start, end)
		for direction := range spatial {
			r.batchSpatial[direction] += spatial[direction]
		}
		best, spatialScore, spatialConfidence := rookBestDirection(spatial)
		votes[best]++
		periods++
		if r.lockedDirection < 0 && !farActive && r.healthyN == rookMics &&
			(spatialScore < 0.25 || spatialConfidence < 0.05) {
			r.observeLevels(start, end)
		}

		power := r.arrayPower(start, end)
		baseline := r.noisePower
		if r.noiseReady == 0 {
			baseline = power
			r.noisePower = power
		}
		if baseline > 1e-12 {
			ratio := power / baseline
			if ratio > r.batchActivity {
				r.batchActivity = ratio
			}
		}
		if r.lockedDirection < 0 && !farActive {
			r.noisePower = rookNoiseAlpha*r.noisePower + (1-rookNoiseAlpha)*power
			if r.noiseReady < 100 {
				r.noiseReady++
			}
		}
	}

	if periods > 0 {
		for direction := range r.batchSpatial {
			r.batchSpatial[direction] /= float64(periods)
		}
	}
	best, score, confidence := rookBestDirection(r.batchSpatial)
	voted := 0
	for direction := 1; direction < rookDirections; direction++ {
		if votes[direction] > votes[voted] ||
			(votes[direction] == votes[voted] && r.batchSpatial[direction] > r.batchSpatial[voted]) {
			voted = direction
		}
	}
	secondVotes := 0
	for direction, count := range votes {
		if direction != voted && count > secondVotes {
			secondVotes = count
		}
	}
	voteConfidence := 0.0
	if periods > 0 {
		voteConfidence = float64(votes[voted]-secondVotes) / float64(periods)
	}
	// A compact four-mic aperture can have a small averaged score margin in a
	// reflective room even when every 32 ms physical period agrees. Preserve
	// that independent evidence instead of declaring a clear side source
	// unknown merely because its adjacent beam is also coherent.
	if voteConfidence >= 0.60 {
		best = voted
		score = r.batchSpatial[best]
		confidence = math.Max(confidence, 0.25*voteConfidence)
	}
	r.batchDirection = best
	r.batchConfidence = confidence
	r.lastConfidence = confidence
	r.lastSpatial = confidence
	r.lastCoherence = clamp(score, 0, 1)
	if r.lockedDirection < 0 {
		r.updateWakeDirections(best, score, confidence)
	}

	// The voted cardinal gives stability; the cubic score vector supplies a
	// continuous angle between cardinals and a circular EWMA removes jumps.
	if periods > 0 {
		if confidence >= rookMinConfidence && score >= rookMinSpatialScore {
			r.visualDirection = voted
			measured := rookContinuousAngle(r.batchSpatial, voted)
			if r.visualAngle < 0 {
				r.visualAngle = measured
			} else {
				r.visualAngle = math.Mod(r.visualAngle+0.45*angleDiff(measured, r.visualAngle)+360, 360)
			}
		}
	}

	angle := -1.0
	if r.trackSpeech || r.lockedDirection >= 0 {
		angle = r.visualAngle
		if angle < 0 && r.visualDirection >= 0 {
			angle = rookAngles[r.visualDirection]
		}
	}

	if r.lockedDirection < 0 {
		return r.extractOmni(gain), angle
	}
	direction := r.lockedDirection
	if steerAngle >= 0 {
		direction = rookNearestDirection(steerAngle)
		if !r.trackSpeech {
			angle = rookAngles[direction]
		}
	}
	r.outputPath = direction
	return r.extractSteered(direction, gain), angle
}

func (r *RookFrontEnd) decode(raw []byte) {
	frames := len(r.channels[0])
	var sum, square [rookMics]float64
	for frame := 0; frame < frames; frame++ {
		base := frame * rookFrameSize
		for channel := 0; channel < rookMics; channel++ {
			value := float32(decodePackedS24(raw[base+channel*byteSample:])) / 8388608.0
			r.channels[channel][frame] = value
			sum[channel] += float64(value)
			square[channel] += float64(value) * float64(value)
		}
	}
	var rms [rookMics]float64
	for channel := range rms {
		mean := sum[channel] / float64(frames)
		variance := square[channel]/float64(frames) - mean*mean
		if variance > 0 {
			rms[channel] = math.Sqrt(variance) * 8388608.0
		}
	}
	sorted := rms
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	median := 0.5 * (sorted[1] + sorted[2])
	r.healthyN = 0
	for channel := range r.healthy {
		r.healthy[channel] = rms[channel] >= rookMinSignalRMS
		if median > rookMinSignalRMS {
			r.healthy[channel] = r.healthy[channel] && rms[channel]*25 >= median && rms[channel] <= median*25
		}
		if r.healthy[channel] {
			r.healthyN++
		}
	}
}

func (r *RookFrontEnd) bandDiff() {
	for channel := 0; channel < rookMics; channel++ {
		in, out := r.channels[channel], r.hf[channel]
		if len(out) > 0 {
			out[0] = 0
		}
		if len(out) > 1 {
			out[1] = 0
		}
		for i := 2; i < len(out); i++ {
			out[i] = (in[i] - in[i-2]) * 0.5
		}
	}
}

func (r *RookFrontEnd) arrayPower(start, end int) float64 {
	var power float64
	count := 0
	for channel := 0; channel < rookMics; channel++ {
		if !r.healthy[channel] {
			continue
		}
		for _, sample := range r.hf[channel][start:end] {
			power += float64(sample) * float64(sample)
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return power / float64(count)
}

// observeLevels learns capsule/ADC sensitivity only from diffuse or ambiguous
// room sound. Directional speech is intentionally excluded so being closer to
// one side cannot be mistaken for a microphone gain mismatch. The correction
// preserves geometric-mean level and is bounded to +/-3 dB per channel.
func (r *RookFrontEnd) observeLevels(start, end int) {
	var observed [rookMics]float64
	for channel := 0; channel < rookMics; channel++ {
		var power float64
		for _, sample := range r.hf[channel][start:end] {
			power += float64(sample) * float64(sample)
		}
		power /= float64(end - start)
		if power < 1e-12 {
			return
		}
		observed[channel] = 0.5 * math.Log(power)
	}
	alpha := 0.025
	if r.levelObs < 24 {
		alpha = 0.10
	}
	if r.levelObs == 0 {
		r.levelLog = observed
	} else {
		for channel := range r.levelLog {
			r.levelLog[channel] += alpha * (observed[channel] - r.levelLog[channel])
		}
	}
	r.levelObs++
	var mean float64
	for _, level := range r.levelLog {
		mean += level
	}
	mean /= rookMics
	for channel, level := range r.levelLog {
		r.levelGain[channel] = clamp(math.Exp(mean-level), 0.7071, 1.4142)
	}
	if r.levelObs == 24 {
		log.Printf("[beam] Rook four-mic level calibration ready: gains=%v", r.levelGain)
	}
}

func (r *RookFrontEnd) spatialScores(start, end int) (scores [rookDirections]float64) {
	pairs := 0
	for i := 0; i < rookMics; i++ {
		if !r.healthy[i] {
			continue
		}
		for j := i + 1; j < rookMics; j++ {
			if !r.healthy[j] {
				continue
			}
			var corr [2*rookMaxSpatialLag + 1]float64
			for lag := -rookMaxSpatialLag; lag <= rookMaxSpatialLag; lag++ {
				corr[lag+rookMaxSpatialLag] = normalisedCorrelation(r.hf[i][start:end], r.hf[j][start:end], lag)
			}
			for direction := range scores {
				arrival := rookArrivalSamples(direction)
				scores[direction] += rookInterpolateCorrelation(corr, arrival[j]-arrival[i])
			}
			pairs++
		}
	}
	if pairs > 0 {
		for direction := range scores {
			scores[direction] /= float64(pairs)
		}
	}
	return scores
}

func rookArrivalSamples(direction int) (arrival [rookMics]float64) {
	if direction < 0 || direction >= rookDirections {
		return arrival
	}
	theta := rookAngles[direction] * math.Pi / 180
	sx, sy := math.Sin(theta), math.Cos(theta)
	for channel, position := range rookMicPositions {
		arrival[channel] = -(position[0]*sx + position[1]*sy) * sampleRate / speedOfSound
	}
	return arrival
}

func rookSteeringDelays(direction int) (delays [rookMics]float64) {
	arrival := rookArrivalSamples(direction)
	latest := arrival[0]
	for _, value := range arrival[1:] {
		if value > latest {
			latest = value
		}
	}
	for channel := range delays {
		delays[channel] = latest - arrival[channel]
	}
	return delays
}

func rookInterpolateCorrelation(corr [2*rookMaxSpatialLag + 1]float64, lag float64) float64 {
	if lag <= -rookMaxSpatialLag {
		return corr[0]
	}
	if lag >= rookMaxSpatialLag {
		return corr[len(corr)-1]
	}
	lo := math.Floor(lag)
	fraction := lag - lo
	index := int(lo) + rookMaxSpatialLag
	return corr[index]*(1-fraction) + corr[index+1]*fraction
}

func rookBestDirection(scores [rookDirections]float64) (best int, score, confidence float64) {
	best = 0
	second := -math.MaxFloat64
	for direction := 1; direction < rookDirections; direction++ {
		if scores[direction] > scores[best] {
			second = scores[best]
			best = direction
		} else if scores[direction] > second {
			second = scores[direction]
		}
	}
	score = scores[best]
	if second == -math.MaxFloat64 {
		second = score
	}
	confidence = (score - second) / math.Max(math.Abs(score), 1e-12)
	return
}

func rookRunnerUp(scores [rookDirections]float64, best int) int {
	second := -1
	for direction := range scores {
		if direction == best {
			continue
		}
		if second < 0 || scores[direction] > scores[second] {
			second = direction
		}
	}
	return second
}

func rookContinuousAngle(scores [rookDirections]float64, fallback int) float64 {
	minimum := scores[0]
	for _, score := range scores[1:] {
		if score < minimum {
			minimum = score
		}
	}
	var x, y float64
	for direction, score := range scores {
		weight := math.Max(0, score-minimum)
		weight = weight * weight * weight
		theta := rookAngles[direction] * math.Pi / 180
		x += weight * math.Sin(theta)
		y += weight * math.Cos(theta)
	}
	if math.Hypot(x, y) < 1e-12 {
		return rookAngles[fallback]
	}
	return math.Mod(math.Atan2(x, y)*180/math.Pi+360, 360)
}

func rookNearestDirection(angle float64) int {
	best := 0
	difference := math.Abs(angleDiff(angle, rookAngles[0]))
	for direction := 1; direction < rookDirections; direction++ {
		candidate := math.Abs(angleDiff(angle, rookAngles[direction]))
		if candidate < difference {
			best, difference = direction, candidate
		}
	}
	return best
}

func (r *RookFrontEnd) updateWakeDirections(best int, score, confidence float64) {
	if r.healthyN < 2 || score < rookMinSpatialScore || confidence < rookMinConfidence {
		return
	}
	second := rookRunnerUp(r.batchSpatial, best)
	if !r.wakeReady {
		r.wakeDirections = [2]int{best, second}
		r.wakeReady = true
		return
	}
	if best == r.wakeDirections[0] {
		r.wakeDirections[1] = second
		return
	}
	// A speech-like energy rise may move a lane immediately. In steady room
	// sound require stronger directional evidence so the model is not reset
	// repeatedly by a fan or television.
	if (r.batchActivity >= 1.20 && confidence >= 0.10) || confidence >= 0.22 {
		r.wakeDirections = [2]int{best, second}
	}
}

func (r *RookFrontEnd) extractOmni(gain float64) []byte {
	frames := len(r.channels[0])
	out := make([]byte, frames*2)
	for frame := 0; frame < frames; frame++ {
		var sum float64
		count := 0
		for channel := 0; channel < rookMics; channel++ {
			if r.healthy[channel] {
				sum += float64(r.channels[channel][frame]) * r.levelGain[channel]
				count++
			}
		}
		if count == 0 {
			count = rookMics
			for channel := 0; channel < rookMics; channel++ {
				sum += float64(r.channels[channel][frame])
			}
		}
		r.writeSample(out, frame, sum*gain*32768/float64(count), rookOmniPath)
	}
	return out
}

func (r *RookFrontEnd) extractOmniRaw(raw []byte, gain float64) []byte {
	frames := len(raw) / rookFrameSize
	out := make([]byte, frames*2)
	for frame := 0; frame < frames; frame++ {
		base := frame * rookFrameSize
		var sum int64
		for channel := 0; channel < rookMics; channel++ {
			sum += int64(decodePackedS24(raw[base+channel*byteSample:]))
		}
		r.writeSample(out, frame, float64(sum)*gain/(rookMics*256), rookOmniPath)
	}
	return out
}

func (r *RookFrontEnd) extractSteered(direction int, gain float64) []byte {
	frames := len(r.channels[0])
	out := make([]byte, frames*2)
	delays := rookSteeringDelays(direction)
	count := r.healthyN
	if count < 1 {
		return r.extractOmni(gain)
	}
	for frame := 0; frame < frames; frame++ {
		var sum float64
		for channel := 0; channel < rookMics; channel++ {
			if r.healthy[channel] {
				sum += float64(r.steeredSample(channel, frame, delays[channel])) * r.levelGain[channel]
			}
		}
		r.writeSample(out, frame, sum*gain*32768/float64(count), direction)
	}
	return out
}

func (r *RookFrontEnd) extractSteeredRaw(raw []byte, direction int, gain float64) []byte {
	frames := len(raw) / rookFrameSize
	if frames == 0 {
		return nil
	}
	out := make([]byte, frames*2)
	delays := rookSteeringDelays(direction)
	count := r.healthyN
	if count < 2 {
		return r.extractOmniRaw(raw, gain)
	}
	for frame := 0; frame < frames; frame++ {
		var sum float64
		for channel := 0; channel < rookMics; channel++ {
			if !r.healthy[channel] {
				continue
			}
			position := float64(frame) - delays[channel]
			base := int(math.Floor(position))
			fraction := position - float64(base)
			if base < 0 {
				base, fraction = 0, 0
			}
			next := base + 1
			if next >= frames {
				next = frames - 1
			}
			a := rookRawMicSample(raw, base, channel)
			b := rookRawMicSample(raw, next, channel)
			sum += (a + fraction*(b-a)) * r.levelGain[channel]
		}
		r.writeSample(out, frame, sum*gain/float64(count*256), direction)
	}
	return out
}

func rookRawMicSample(raw []byte, frame, channel int) float64 {
	offset := frame*rookFrameSize + channel*byteSample
	return float64(decodePackedS24(raw[offset:]))
}

func (r *RookFrontEnd) steeredSample(channel, frame int, delay float64) float32 {
	position := float64(frame) - delay
	base := int(math.Floor(position))
	fraction := float32(position - float64(base))
	a := r.beamSample(channel, base)
	b := r.beamSample(channel, base+1)
	return a + fraction*(b-a)
}

func (r *RookFrontEnd) beamSample(channel, frame int) float32 {
	if frame >= 0 {
		if frame >= len(r.channels[channel]) {
			return r.channels[channel][len(r.channels[channel])-1]
		}
		return r.channels[channel][frame]
	}
	index := rookHistorySamples + frame
	if index < 0 {
		index = 0
	}
	return r.history[channel][index]
}

func (r *RookFrontEnd) rememberHistory() {
	for channel := 0; channel < rookMics; channel++ {
		samples := r.channels[channel]
		if len(samples) >= rookHistorySamples {
			copy(r.history[channel][:], samples[len(samples)-rookHistorySamples:])
			continue
		}
		shift := rookHistorySamples - len(samples)
		copy(r.history[channel][:shift], r.history[channel][len(samples):])
		copy(r.history[channel][shift:], samples)
	}
}

func (r *RookFrontEnd) writeSample(out []byte, frame int, sample float64, path int) {
	value := math.Round(sample)
	if value > 32767 {
		value = 32767
		r.clipped++
		r.clippedByChannel[path]++
	} else if value < -32768 {
		value = -32768
		r.clipped++
		r.clippedByChannel[path]++
	}
	binary.LittleEndian.PutUint16(out[frame*2:], uint16(int16(value)))
}

// WakeBeamCount keeps CPU and model memory bounded while covering the two
// strongest bearings. Rook's compact aperture gives both beams broad enough
// pickup that an in-between source is present in both views.
func (*RookFrontEnd) WakeBeamCount() int { return 2 }

// WakeDirection reports the primary direction maintained by the array
// estimator without constructing the legacy candidate audio beams.
func (r *RookFrontEnd) WakeDirection() (int, bool) {
	if r.healthyN < 2 {
		return rookOmniPath, false
	}
	return r.wakeDirections[0], r.wakeReady
}

func (r *RookFrontEnd) WakeBeams(raw []byte, gain float64) []WakeBeam {
	if r.healthyN < 2 {
		return []WakeBeam{{Direction: rookOmniPath, Angle: -1, PCM: r.extractOmniRaw(raw, gain)}}
	}
	return []WakeBeam{
		r.WakeBeam(raw, r.wakeDirections[0], gain),
		r.WakeBeam(raw, r.wakeDirections[1], gain),
	}
}

func (r *RookFrontEnd) WakeBeam(raw []byte, direction int, gain float64) WakeBeam {
	if direction < 0 || direction >= rookDirections || r.healthyN < 2 {
		return WakeBeam{Direction: rookOmniPath, Angle: -1, PCM: r.extractOmniRaw(raw, gain)}
	}
	return WakeBeam{
		Direction: direction,
		Angle:     rookAngles[direction],
		PCM:       r.extractSteeredRaw(raw, direction, gain),
	}
}

// The first loopback channel is aligned to the microphones. DataClient still
// requires both proven silence and proven playback before promoting it over
// the software speaker tap, so a different hardware revision safely falls
// back instead of cancelling against a microphone.
func (*RookFrontEnd) EchoRefInto(raw, dst []byte) []byte {
	frames := len(raw) / rookFrameSize
	if frames == 0 {
		return nil
	}
	if cap(dst) < frames*2 {
		dst = make([]byte, frames*2)
	} else {
		dst = dst[:frames*2]
	}
	for frame := 0; frame < frames; frame++ {
		base := frame*rookFrameSize + rookRefCh*byteSample
		value := int16(decodePackedS24(raw[base:]) >> 8)
		binary.LittleEndian.PutUint16(dst[frame*2:], uint16(value))
	}
	return dst
}

func rookPlaybackRefActive(raw []byte, start, end int) bool {
	for frame := start; frame < end; frame++ {
		base := frame * rookFrameSize
		for channel := rookMics; channel < rookChannels; channel++ {
			offset := base + channel*byteSample
			if raw[offset] != 0 || raw[offset+1] != 0 || raw[offset+2] != 0 {
				return true
			}
		}
	}
	return false
}

func (r *RookFrontEnd) ClippedSamples() uint64      { return r.clipped }
func (r *RookFrontEnd) ClippedByChannel() [7]uint64 { return r.clippedByChannel }
func (r *RookFrontEnd) OutputChannel() int          { return r.outputPath }
func (r *RookFrontEnd) Diagnostics() Diagnostics {
	minimum, maximum := r.levelLog[0], r.levelLog[0]
	for _, level := range r.levelLog[1:] {
		if level < minimum {
			minimum = level
		}
		if level > maximum {
			maximum = level
		}
	}
	return Diagnostics{
		OutputChannel:      r.outputPath,
		Confidence:         r.lastConfidence,
		Spatial:            r.lastSpatial,
		PlaybackActive:     r.playbackActive,
		Calibrated:         r.levelObs >= 24,
		LevelBalanceDB:     20 * (maximum - minimum) / math.Log(10),
		NoiseGain:          1,
		Coherence:          r.lastCoherence,
		HealthyMicChannels: r.healthyN,
	}
}
