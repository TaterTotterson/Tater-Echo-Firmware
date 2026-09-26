// Package beamformer implements directional mic selection for the
// Echo Dot Gen 2 (biscuit) 7-microphone array.
//
// # Mic geometry (confirmed empirically, 2026-05)
//
// 6 perimeter mics at r=36mm, 60° intervals, 30° offset from 12 o'clock.
// 1 centre mic. Ch7 and Ch8 are NOT mics: they are a stereo loopback of the
// device's own playback — the hardware echo reference (see echoRefCh below
// and SETUP.md's Mic Array section, measured 2026-08-29).
//
//	Ch0 → MK1 → 330°  (11 o'clock)  confirmed empirically 2026-05
//	Ch1 → MK2 →  30°  ( 1 o'clock)
//	Ch2 → MK3 →  90°  ( 3 o'clock)
//	Ch3 → MK4 → 150°  ( 5 o'clock)
//	Ch4 → MK5 → 210°  ( 7 o'clock)
//	Ch5 → MK6 → 270°  ( 9 o'clock)
//	Ch6 → MK7 → centre (omnidirectional)
//	Ch7 → playback echo reference, LEFT  (the driver never plays this side)
//	Ch8 → playback echo reference, RIGHT (what the driver actually emits)
//
// # Algorithm
//
// Direction estimation always runs — smoothers update once per physical
// 512-frame ALSA period regardless of BeamformingEnabled. GoTinyAlsa hands
// Process five periods at once, so treating a call as one observation makes
// every time constant five times slower than its name and comment.
//
// Output channel is determined by lock state, not by the config flag:
//   - Unlocked: always ch6 (centre/omni). Covers OWW listening and any turn
//     where Lock() was a no-op (beamforming disabled).
//   - Locked: the perimeter mic selected at Lock() time, or the mic nearest
//     to BeamAngle if a fixed steering direction is configured.
//
// BeamformingEnabled only gates Lock() — if false, Lock() is a no-op and
// the device stays on ch6 for both OWW and voice turns.
//
// # Direction estimation
//
// Two parallel smoothers run continuously, one update per 32ms subframe:
//
//   - energySmooth (α=0.9, ~320ms): fast, tracks speech onset
//   - energyBaseline (α=0.995, ~10s): slow, tracks steady background noise
//
// At Lock() time, the direction with the highest ratio of energySmooth to
// energyBaseline is selected. This picks the direction that just had a
// sudden energy increase (speech onset) rather than the direction with the
// highest absolute energy (TV, fan, etc.).
//
// Direction is also exposed for LED ring visualisation.
package beamformer

import (
	"log"
	"math"
)

const (
	// ALSA stream parameters — must match pcm_microphone.go
	nChannels    = 9
	sampleRate   = 16000
	byteSample   = 3                      // S24_3LE
	frameSize    = nChannels * byteSample // 27 bytes per frame
	periodFrames = 512

	// Number of candidate steering directions — one per perimeter mic
	nDirections = 6

	// Centre mic channel — used for wake word detection (omnidirectional)
	centreCh = 6

	// Hardware echo reference — NOT a microphone. Ch7 and Ch8 are a stereo
	// loopback of the device's own playback, arriving in the same TDM frame
	// as the mic samples, and the internal driver plays the RIGHT channel
	// only (measured 2026-08-29: left silent gives 55dB less at the mic; see
	// SETUP.md's Mic Array section). So ch8 is the reference and ch7 carries
	// a signal the speaker never emits — using ch7 would be cancelling
	// against audio nobody heard.
	echoRefCh = 8

	// Smoothing constants
	smoothAlpha   = 0.9   // fast smoother (~320ms time constant at 32ms/period)
	baselineAlpha = 0.995 // slow smoother (~10s time constant) — tracks background noise

	// A direction below this best-vs-runner-up separation is not reliable
	// enough to replace the centre microphone. Picking ch0 on an all-equal
	// room is worse than admitting the array has no bearing.
	minLockConfidence = 0.05
	// An absolute coherence floor keeps random, low-level pair correlation
	// from breaking an otherwise honest energy tie.
	minSpatialScore = 0.08

	// Lock-back window. Controller-side wake detection lands 300–500ms
	// after the wake word ends, by which time the fast smoother's onset
	// spike has largely decayed — selecting on the *present* picks a mic
	// unrelated to the speaker. Instead Lock() looks back over a ring of
	// per-direction period energies covering the whole wake word plus the
	// detection latency, and scores each direction by its energy burst
	// within that window relative to its noise baseline.
	historyPeriods = 64 // ~2.0s at 32ms/period
	burstTopN      = 8  // periods averaged for a direction's burst (~256ms)
)

// micAngles defines the physical angle (degrees, clockwise from 12 o'clock)
// for each ALSA channel. Index = channel number.
// Confirmed empirically 2026-05 via tone injection + analyse_capture.py.
var micAngles = [7]float64{
	330, // ch0 — MK1
	30,  // ch1 — MK2
	90,  // ch2 — MK3
	150, // ch3 — MK4
	210, // ch4 — MK5
	270, // ch5 — MK6
	0,   // ch6 — MK7 centre
}

// candidateAngles are the steering directions tested for direction estimation.
// One per perimeter mic, matching the mic positions exactly.
var candidateAngles = [nDirections]float64{330, 30, 90, 150, 210, 270}

// directionToChannel maps candidateAngles index → ALSA channel number for
// the perimeter mic at that direction.
//
//	candidateAngles[0]=330° → ch0 (MK1)
//	candidateAngles[1]=30°  → ch1 (MK2)
//	candidateAngles[2]=90°  → ch2 (MK3)
//	candidateAngles[3]=150° → ch3 (MK4)
//	candidateAngles[4]=210° → ch4 (MK5)
//	candidateAngles[5]=270° → ch5 (MK6)
var directionToChannel = [nDirections]int{0, 1, 2, 3, 4, 5}

// Beamformer holds direction estimation state and locked mic selection.
type Beamformer struct {
	// energySmooth: fast EWMA of per-direction HF energy (~320ms time constant).
	// Tracks speech onset.
	energySmooth [nDirections]float64

	// energyBaseline: slow EWMA of per-direction HF energy (~10s time constant).
	// Tracks steady background noise (TV, fan, etc.).
	energyBaseline [nDirections]float64

	// playbackBaseline is the per-direction floor while ch8 proves the Dot's
	// own speaker is active. It prevents TTS from becoming a false near-end
	// onset without teaching the ordinary room baseline the speaker's echo.
	playbackBaseline [nDirections]float64
	playbackReady    int

	// baselineReady counts periods until baseline is initialised (~3s warmup).
	baselineReady int

	// energyHistory is a ring of per-period, per-direction HF energies —
	// the lock-back window (see constants above). Written every Process()
	// period while unlocked; frozen during a locked turn so a follow-up
	// continuation lock still sees the window around the last utterance
	// rather than only what came after it.
	energyHistory [historyPeriods][nDirections]float64
	historyIdx    int
	historyCount  int

	// lockedChannel is the ALSA channel selected at gate open.
	// -1 means unlocked (use live best-direction selection).
	lockedChannel int

	// trackSpeech enables live, unlocked DOA while waiting for the speech that
	// will be locked. Native initial and continued-chat listening deliberately
	// share this path; ordinary idle wake listening keeps it off.
	trackSpeech bool

	// Live selection state. batchScores holds the strongest calibrated onset
	// in the most recent physical ALSA batch and is what LockCurrent consumes.
	// The visual candidate uses physical-subframe voting plus hysteresis.
	batchScores        [nDirections]float64
	batchSpatial       [nDirections]float64
	visualDirection    int
	lastConfidence     float64
	lastSpatialConf    float64
	lastPlaybackActive bool
	outputChannel      int

	// clippedSamples counts output samples clamped to int16 range by the
	// mic gain in extractChannel. Only touched from the mic goroutine
	// (Process and the diagnostics that read it) — no synchronisation.
	clippedSamples   uint64
	clippedByChannel [7]uint64

	// Reusable per-batch analysis buffers (§3.5, 2026-07-07). ALSA normally
	// delivers five 512-frame periods together; the slices grow once to that
	// batch size so DOA analyzes all 160ms rather than sampling only its first
	// 32ms. extractChannel still returns a fresh allocation because data.go's
	// preroll ring retains those slices across batches.
	chanBuf [nDirections][]float32
	hfBuf   [nDirections][]float32
}

// New creates a Beamformer.
func New() *Beamformer {
	b := &Beamformer{
		lockedChannel:   -1,
		visualDirection: -1,
		outputChannel:   centreCh,
	}
	for ci := 0; ci < nDirections; ci++ {
		b.chanBuf[ci] = make([]float32, periodFrames)
		b.hfBuf[ci] = make([]float32, periodFrames)
	}
	return b
}

func (b *Beamformer) ensureAnalysisFrames(frames int) {
	for ci := 0; ci < nDirections; ci++ {
		if cap(b.chanBuf[ci]) < frames {
			b.chanBuf[ci] = make([]float32, frames)
			b.hfBuf[ci] = make([]float32, frames)
			continue
		}
		b.chanBuf[ci] = b.chanBuf[ci][:frames]
		b.hfBuf[ci] = b.hfBuf[ci][:frames]
	}
}

// Lock selects the mic with the highest energy onset relative to its noise
// floor baseline and holds it until Unlock.
//
// enabled is the BeamformingEnabled config flag. If false, Lock() is a no-op
// and the beamformer continues outputting ch6 (centre/omni). This means the
// config flag only gates directional selection — not whether smoothers run.
// Smoothers always run so the baseline is warm if beamforming is later enabled.
//
// Using onset ratio rather than absolute energy means a voice in a quiet
// direction beats a TV in a loud direction. Falls back to raw smooth energy
// if the baseline hasn't warmed up yet (~3s after start), since onset ratios
// are meaningless when energyBaseline is near zero.
func (b *Beamformer) Lock(enabled bool) {
	b.trackSpeech = false
	if !enabled {
		// Beamforming disabled — stay on ch6 (lockedChannel remains -1).
		// Smoothers are still running, so if beamforming is turned on later
		// the baseline will already be warmed up.
		log.Printf("[beam] Lock() called but beamforming disabled — staying on ch6 (omni)")
		return
	}
	if b.lockedChannel >= 0 {
		return
	}

	best := 0
	var bestScore, confidence float64
	mode := "energy"
	switch {
	case b.baselineReady >= 100 && b.historyCount >= burstTopN*2:
		// Lock-back: score each direction by its energy burst within the
		// recorded window (which contains the wake word) relative to its
		// baseline. Immune to the detection latency that made live onset
		// ratios pick a decayed, often unrelated direction.
		var scores [nDirections]float64
		for di := range scores {
			scores[di] = b.burstRatio(di)
		}
		best, bestScore, confidence = bestDirection(scores)
		mode = "burst_ratio"
	case b.baselineReady >= 100:
		// History not populated yet (fresh start) — live onset ratio.
		var scores [nDirections]float64
		for di := range scores {
			scores[di] = b.onsetRatio(di)
		}
		best, bestScore, confidence = bestDirection(scores)
		mode = "onset_ratio"
	default:
		// Baseline not ready — use raw smooth energy to avoid picking a
		// direction based on a near-zero baseline inflating the ratio
		best, bestScore, confidence = bestDirection(b.energySmooth)
	}

	b.lastConfidence = confidence
	if confidence < minLockConfidence {
		log.Printf("[beam] %s uncertain (score=%.3f confidence=%.2f) — staying on ch6 (centre)",
			mode, bestScore, confidence)
		return
	}
	if mode == "burst_ratio" {
		log.Printf("[beam] locked to ch%d (%.0f°) %s=%.2f confidence=%.2f (lock-back over %d periods)",
			directionToChannel[best], candidateAngles[best], mode, bestScore, confidence, b.historyCount)
	} else {
		log.Printf("[beam] locked to ch%d (%.0f°) %s=%.2f confidence=%.2f",
			directionToChannel[best], candidateAngles[best], mode, bestScore, confidence)
	}
	b.lockedChannel = directionToChannel[best]
}

// PrepareSpeechLock switches to live visual DOA while the audio path waits for
// near-end speech. It preserves all estimator history; native initial and
// continued-chat listening both use this same preparation path.
func (b *Beamformer) PrepareSpeechLock() {
	b.Unlock()
	b.trackSpeech = true
}

// LockCurrent selects from the current speech onset rather than wake-word
// history. Native listening uses it for both initial and continued-chat turns
// so the audio pickup follows the same speech that drives their live DOA.
func (b *Beamformer) LockCurrent(enabled bool) {
	if !enabled {
		log.Printf("[beam] LockCurrent() called but beamforming disabled — staying on ch6 (omni)")
		return
	}
	if b.lockedChannel >= 0 {
		return
	}

	best, bestScore, mode := b.liveDirection()
	var confidence float64
	if b.trackSpeech {
		_, _, confidence = bestDirection(b.batchScores)
	} else {
		var scores [nDirections]float64
		for di := range scores {
			scores[di] = b.energySmooth[di]
			if b.baselineReady >= 100 {
				scores[di] = b.onsetRatio(di)
			}
		}
		_, _, confidence = bestDirection(scores)
	}
	if mode == "spatial_tiebreak" {
		confidence = math.Max(confidence, b.lastSpatialConf)
	}
	if confidence < minLockConfidence {
		log.Printf("[beam] speech direction uncertain (%s=%.3f confidence=%.2f) — staying on ch6 (centre)",
			mode, bestScore, confidence)
		return
	}
	b.lockedChannel = directionToChannel[best]
	b.lastConfidence = confidence
	log.Printf("[beam] speech-locked to ch%d (%.0f°) %s=%.2f confidence=%.2f spatial=%.2f",
		b.lockedChannel, candidateAngles[best], mode, bestScore, confidence, b.lastSpatialConf)
}

// liveDirection returns the strongest recent acoustic onset. Once the room
// baseline is warm, using an onset ratio rather than raw microphone energy
// prevents a naturally hotter microphone or steady appliance from owning the
// visual bearing. Before warm-up, raw energy is the only meaningful fallback.
func (b *Beamformer) liveDirection() (direction int, score float64, mode string) {
	if b.trackSpeech {
		direction, score, _ = bestDirection(b.batchScores)
		mode = "batch_onset"
		spatial, spatialScore, spatialConfidence := bestDirection(b.batchSpatial)
		_, _, energyConfidence := bestDirection(b.batchScores)
		if energyConfidence < 0.18 && spatialScore >= minSpatialScore && spatialConfidence >= minLockConfidence {
			direction = spatial
			score = spatialScore
			mode = "spatial_tiebreak"
		}
		return direction, score, mode
	}
	mode = "energy"
	score = b.energySmooth[0]
	if b.baselineReady >= 100 {
		mode = "onset_ratio"
		score = b.onsetRatio(0)
	}
	for di := 1; di < nDirections; di++ {
		candidate := b.energySmooth[di]
		if b.baselineReady >= 100 {
			candidate = b.onsetRatio(di)
		}
		if candidate > score {
			score = candidate
			direction = di
		}
	}
	return direction, score, mode
}

// bestDirection returns the largest score and its normalised separation from
// the runner-up. Confidence is zero for all-equal/all-zero input and approaches
// one as the winner separates. This makes "no usable bearing" explicit rather
// than accidentally preferring channel zero.
func bestDirection(scores [nDirections]float64) (best int, score, confidence float64) {
	second := -math.MaxFloat64
	score = scores[0]
	for di := 1; di < nDirections; di++ {
		v := scores[di]
		if v > score {
			second = score
			score = v
			best = di
		} else if v > second {
			second = v
		}
	}
	if second == -math.MaxFloat64 {
		second = score
	}
	confidence = (score - second) / math.Max(math.Abs(score), 1e-12)
	if confidence < 0 {
		confidence = 0
	} else if confidence > 1 {
		confidence = 1
	}
	return best, score, confidence
}

// onsetRatio returns energySmooth[di] / energyBaseline[di].
// High ratio = sudden energy increase = likely speech onset.
func (b *Beamformer) onsetRatio(di int) float64 {
	baseline := b.energyBaseline[di]
	if baseline < 1e-10 {
		return b.energySmooth[di]
	}
	return b.energySmooth[di] / baseline
}

// burstRatio returns direction di's burst energy over the lock-back window
// divided by its noise baseline. Burst = mean of the top burstTopN period
// energies in the ring — a peak statistic, so it finds the wake word
// wherever it sits in the window without needing exact alignment, and a
// single glitch period can't dominate the way a plain max would.
func (b *Beamformer) burstRatio(di int) float64 {
	n := b.historyCount
	if n > historyPeriods {
		n = historyPeriods
	}
	// Partial selection of the top burstTopN values — n is at most 64 and
	// this runs once per direction per Lock(), so O(n·topN) is fine and
	// allocation-free.
	var top [burstTopN]float64
	for i := 0; i < n; i++ {
		v := b.energyHistory[i][di]
		for j := 0; j < burstTopN; j++ {
			if v > top[j] {
				v, top[j] = top[j], v
			}
		}
	}
	count := burstTopN
	if n < burstTopN {
		count = n
	}
	var burst float64
	for j := 0; j < count; j++ {
		burst += top[j]
	}
	burst /= float64(count)

	baseline := b.energyBaseline[di]
	if baseline < 1e-10 {
		return burst
	}
	return burst / baseline
}

// Unlock releases the locked mic selection.
// Call when the VAD gate closes (voice turn ends).
func (b *Beamformer) Unlock() {
	if b.lockedChannel >= 0 {
		log.Printf("[beam] unlocked from ch%d", b.lockedChannel)
	}
	b.lockedChannel = -1
	b.trackSpeech = false
}

// LockedAngle returns the physical bearing selected by the most recent Lock,
// or -1 while the beam is unlocked. Callers serialize it with Process/Lock.
func (b *Beamformer) LockedAngle() float64 {
	if b.lockedChannel < 0 {
		return -1
	}
	for direction, channel := range directionToChannel {
		if channel == b.lockedChannel {
			return candidateAngles[direction]
		}
	}
	return -1
}

// Process returns mono S16_LE audio and the estimated source angle.
//
// gain is the linear fixed mic gain (config MicGainDb converted to linear;
// 1.0 = unity) applied to the full 24-bit samples during S16 extraction —
// see extractChannel. Direction estimation is unaffected: it runs on
// energy ratios, which are gain-invariant.
//
// The enabled flag (BeamformingEnabled) no longer gates smoother updates —
// direction estimation always runs so the baseline stays warm regardless of
// config state. The flag only affects Lock() behaviour (see Lock() docs).
//
// Output channel is determined by lock state alone:
//   - Unlocked (lockedChannel == -1): always ch6 (centre/omni). This covers
//     OWW listening and any voice turn where Lock() was a no-op (beamforming
//     disabled). ch6 is equidistant from all directions — no directional bias.
//   - Locked, steerAngle >= 0 (fixed-beam): mic nearest to steerAngle. Config-
//     driven direction, ignores the energy-based lock channel.
//   - Locked, steerAngle < 0 (auto): the perimeter mic selected at Lock() time.
//
// angle is the estimated dominant source direction (0–360°, clockwise from
// 12 o'clock), or -1 during ordinary unlocked wake listening. Continued-chat
// preparation explicitly enables live visual DOA while unlocked.
func (b *Beamformer) Process(raw []byte, steerAngle float64, gain float64) (mono []byte, angle float64) {
	frames := len(raw) / frameSize
	b.outputChannel = centreCh
	if frames < periodFrames {
		return b.extractChannel(raw, centreCh, gain), -1
	}
	b.ensureAnalysisFrames(frames)

	// Always decode and update smoothers — direction estimation runs
	// continuously regardless of BeamformingEnabled. This keeps the baseline
	// warm so Lock() gets a good onset ratio the moment beamforming is enabled.
	b.decodeChannels(raw)
	b.bandDiff()
	b.batchScores = [nDirections]float64{}
	b.batchSpatial = [nDirections]float64{}
	b.lastSpatialConf = 0
	b.lastPlaybackActive = false
	var visualVotes [nDirections]int
	spatialPeriods := 0

	// ALSA supplies five 512-frame periods together. Estimator state is
	// nevertheless time-based on the physical 32ms period; updating once for
	// the whole 160ms buffer was the source of the former five-times-slow DOA.
	for start := 0; start+periodFrames <= frames; start += periodFrames {
		end := start + periodFrames
		farActive := playbackRefActive(raw, start, end)
		b.lastPlaybackActive = b.lastPlaybackActive || farActive
		var periodScores [nDirections]float64

		for di := range candidateAngles {
			energy := hfEnergyRange(b.hfBuf[di], start, end)
			b.energySmooth[di] = smoothAlpha*b.energySmooth[di] + (1-smoothAlpha)*energy

			baseline, ready := b.energyBaseline[di], b.baselineReady
			if farActive {
				baseline, ready = b.playbackBaseline[di], b.playbackReady
			}
			score := energy
			if ready >= 16 && baseline >= 1e-10 {
				score = energy / baseline
			}
			periodScores[di] = score
			if score > b.batchScores[di] {
				b.batchScores[di] = score
			}

			// Maintain separate ambient and playback floors. Playback never
			// enters the wake-word lock-back ring; continued-chat locks use the
			// current batch against the playback floor instead.
			if b.lockedChannel < 0 {
				if farActive {
					b.playbackBaseline[di] = baselineAlpha*b.playbackBaseline[di] + (1-baselineAlpha)*energy
				} else {
					b.energyBaseline[di] = baselineAlpha*b.energyBaseline[di] + (1-baselineAlpha)*energy
					b.energyHistory[b.historyIdx][di] = energy
				}
			}
		}

		if b.lockedChannel < 0 {
			if farActive {
				if b.playbackReady < 100 {
					b.playbackReady++
				}
			} else {
				b.historyIdx = (b.historyIdx + 1) % historyPeriods
				if b.historyCount < historyPeriods {
					b.historyCount++
				}
				if b.baselineReady < 100 {
					b.baselineReady++
				}
			}
		}

		energyWinner, _, _ := bestDirection(periodScores)
		if b.trackSpeech {
			spatial := b.spatialScores(start, end)
			for di := range spatial {
				b.batchSpatial[di] += spatial[di]
			}
			spatialPeriods++
			spatialWinner, spatialScore, spatialConfidence := bestDirection(spatial)
			_, _, energyConfidence := bestDirection(periodScores)
			if energyConfidence < 0.18 && spatialScore >= minSpatialScore && spatialConfidence >= minLockConfidence {
				energyWinner = spatialWinner
			}
		}
		visualVotes[energyWinner]++
	}

	if spatialPeriods > 0 {
		for di := range b.batchSpatial {
			b.batchSpatial[di] /= float64(spatialPeriods)
		}
		_, _, b.lastSpatialConf = bestDirection(b.batchSpatial)
	}
	_, _, b.lastConfidence = bestDirection(b.batchScores)

	// Restore the initial-turn behavior validated on the physical Echo: while
	// unlocked, ordinary wake listening reports no DOA; once locked, its visual
	// direction comes from the fast smoother below. Continued chat is the sole
	// exception and can report current-period DOA before its speech lock.
	if b.lockedChannel < 0 && !b.trackSpeech {
		return b.extractChannel(raw, centreCh, gain), -1
	}

	bestDir, _, confidence := bestDirection(b.energySmooth)
	if b.trackSpeech {
		bestDir = 0
		for di := 1; di < nDirections; di++ {
			if visualVotes[di] > visualVotes[bestDir] ||
				(visualVotes[di] == visualVotes[bestDir] && b.batchScores[di] > b.batchScores[bestDir]) {
				bestDir = di
			}
		}
		// Hysteresis only holds an established bearing when the new batch is
		// ambiguous. A clear new speaker direction moves immediately.
		_, _, confidence = bestDirection(b.batchScores)
		if b.visualDirection >= 0 && confidence < minLockConfidence {
			bestDir = b.visualDirection
		} else {
			b.visualDirection = bestDir
		}
	} else if b.visualDirection >= 0 && confidence < minLockConfidence {
		bestDir = b.visualDirection
	} else {
		b.visualDirection = bestDir
	}
	angle = candidateAngles[bestDir]

	// Unlocked: always ch6 (omni). Covers OWW listening and disabled-beamforming
	// voice turns. No directional bias, no channel splices.
	if b.lockedChannel < 0 {
		b.outputChannel = centreCh
		return b.extractChannel(raw, centreCh, gain), angle
	}

	// Locked — select output channel and reported angle.

	var ch int
	if steerAngle >= 0 {
		// Preserve the original initial-turn fixed-beam indicator. Follow-up
		// visual DOA remains live and independent of its audio pickup.
		fixedDir := nearestDirection(steerAngle)
		ch = directionToChannel[fixedDir]
		if !b.trackSpeech {
			angle = candidateAngles[fixedDir]
		}
	} else {
		// Auto: use the channel selected at Lock() time
		ch = b.lockedChannel
	}

	b.outputChannel = ch
	return b.extractChannel(raw, ch, gain), angle
}

// hfEnergy returns the mean squared HF energy for direction di.
// hfChannels is indexed 0–5 by direction (matching decodeChannels output),
// not by ALSA channel number — directionToChannel maps direction→channel
// for audio extraction, but hfChannels uses direction as the index directly.
func hfEnergy(hfChannels [6][]float32, di int) float64 {
	return hfEnergyRange(hfChannels[di], 0, len(hfChannels[di]))
}

func hfEnergyRange(channel []float32, start, end int) float64 {
	n := end - start
	var energy float64
	for _, v := range channel[start:end] {
		energy += float64(v) * float64(v)
	}
	return energy / float64(n)
}

// playbackRefActive uses the defining property of the biscuit's ch8 loopback:
// it is bit-exact zero while idle and non-zero while the speaker carries a
// signal. The detector that promotes ch8 to AEC reference applies the same
// two-sided proof; here a single subframe only chooses which already-local
// noise floor to update and cannot change the AEC source.
func playbackRefActive(raw []byte, start, end int) bool {
	for frame := start; frame < end; frame++ {
		off := frame*frameSize + echoRefCh*byteSample
		if raw[off] != 0 || raw[off+1] != 0 || raw[off+2] != 0 {
			return true
		}
	}
	return false
}

const (
	micRadiusMetres = 0.036
	speedOfSound    = 343.0
	maxSpatialLag   = 4 // opposite mics span at most 3.36 samples at 16kHz
)

// spatialScores evaluates six fixed source bearings from inter-microphone
// arrival-time evidence. It is a small-lag steered-response calculation: all
// 15 perimeter-mic pairs are normalised independently, then sampled at the
// fractional delay predicted by the measured array geometry. This supplies a
// phase/time-delay tie-breaker for localisation; it never sums microphone
// audio into the output path.
func (b *Beamformer) spatialScores(start, end int) (scores [nDirections]float64) {
	pairs := 0
	for i := 0; i < nDirections; i++ {
		ai := micAngles[i] * math.Pi / 180
		xi, yi := micRadiusMetres*math.Sin(ai), micRadiusMetres*math.Cos(ai)
		for j := i + 1; j < nDirections; j++ {
			aj := micAngles[j] * math.Pi / 180
			xj, yj := micRadiusMetres*math.Sin(aj), micRadiusMetres*math.Cos(aj)
			var corr [2*maxSpatialLag + 1]float64
			for lag := -maxSpatialLag; lag <= maxSpatialLag; lag++ {
				corr[lag+maxSpatialLag] = normalisedCorrelation(
					b.hfBuf[i][start:end], b.hfBuf[j][start:end], lag)
			}
			for direction, deg := range candidateAngles {
				theta := deg * math.Pi / 180
				sx, sy := math.Sin(theta), math.Cos(theta)
				// A mic farther toward the source receives the wave earlier.
				delayI := -(xi*sx + yi*sy) * sampleRate / speedOfSound
				delayJ := -(xj*sx + yj*sy) * sampleRate / speedOfSound
				scores[direction] += interpolateCorrelation(corr, delayJ-delayI)
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

// normalisedCorrelation returns corr(a[n], b[n+lag]). Per-pair normalisation
// is the sensitivity calibration: capsule/ADC gain changes numerator and
// denominator equally, so a naturally hotter microphone cannot win merely by
// amplitude. The energy-onset path applies the equivalent calibration through
// its independent per-mic baseline.
func normalisedCorrelation(a, b []float32, lag int) float64 {
	startA, startB := 0, 0
	if lag >= 0 {
		startB = lag
	} else {
		startA = -lag
	}
	n := len(a) - startA
	if m := len(b) - startB; m < n {
		n = m
	}
	if n <= 0 {
		return 0
	}
	var ab, aa, bb float64
	for k := 0; k < n; k++ {
		x, y := float64(a[startA+k]), float64(b[startB+k])
		ab += x * y
		aa += x * x
		bb += y * y
	}
	denom := math.Sqrt(aa * bb)
	if denom < 1e-20 {
		return 0
	}
	return ab / denom
}

func interpolateCorrelation(corr [2*maxSpatialLag + 1]float64, lag float64) float64 {
	if lag <= -maxSpatialLag {
		return corr[0]
	}
	if lag >= maxSpatialLag {
		return corr[len(corr)-1]
	}
	lo := math.Floor(lag)
	frac := lag - lo
	i := int(lo) + maxSpatialLag
	return corr[i]*(1-frac) + corr[i+1]*frac
}

// bandDiff computes the stride-2 difference of each decoded channel into
// hfBuf: out[i] = (in[i] - in[i-2]) / 2. Frequency response peaks at 4kHz
// (fs/4), zero at 0Hz and 8kHz. Reuses hfBuf across periods (§3.5) — the
// first two samples are cleared explicitly since the loop never writes them.
func (b *Beamformer) bandDiff() {
	for ci := 0; ci < nDirections; ci++ {
		in, out := b.chanBuf[ci], b.hfBuf[ci]
		out[0], out[1] = 0, 0
		for i := 2; i < len(in); i++ {
			out[i] = (in[i] - in[i-2]) * 0.5
		}
	}
}

// decodeChannels decodes all 6 perimeter channels from a raw S24_3LE period
// into chanBuf as float32 normalised to [-1, 1]. Reuses chanBuf across
// periods (§3.5) — every element is overwritten, no clearing needed.
func (b *Beamformer) decodeChannels(raw []byte) {
	for i := 0; i < len(b.chanBuf[0]); i++ {
		base := i * frameSize
		for ci := 0; ci < nDirections; ci++ {
			offset := base + ci*byteSample
			b.chanBuf[ci][i] = decodeS24Sample(raw[offset], raw[offset+1], raw[offset+2])
		}
	}
}

// decodeS24Sample decodes 3 bytes of S24_3LE to float32 in [-1, 1].
func decodeS24Sample(b0, b1, b2 byte) float32 {
	val := int32(b0) | int32(b1)<<8 | int32(b2)<<16
	if val&0x800000 != 0 {
		val |= ^int32(0xFFFFFF)
	}
	return float32(val) / 8388608.0
}

// extractChannel extracts a single channel as S16_LE mono, applying the
// fixed mic gain to the full 24-bit sample before quantising to 16-bit.
//
// This used to take the upper 2 bytes of each 3-byte S24_3LE sample,
// discarding the low 8 bits — where nearly all of the signal lives at this
// hardware's capture levels (measured speech RMS 0.0001–0.0006 FS, i.e.
// ~3–20 LSB in 16-bit terms; 20h fleet logs, 2026-07-07). Applying gain
// here, against the 24-bit data, recovers real captured resolution;
// applying it any later would only amplify 16-bit quantisation noise.
//
// gain is linear (1.0 = unity). Q12 fixed point: the >>20 combines the
// Q12 descale with the 24→16 bit reduction (>>8), so gain 1.0 reproduces
// the old upper-2-bytes behaviour bit-exactly. Samples outside int16
// range are clamped and counted in clippedSamples.
func (b *Beamformer) extractChannel(raw []byte, ch int, gain float64) []byte {
	n := len(raw) / frameSize
	out := make([]byte, n*2)
	offset0 := ch * byteSample
	gainQ := int64(gain*4096.0 + 0.5)
	for i := 0; i < n; i++ {
		base := i*frameSize + offset0
		val := int32(raw[base]) | int32(raw[base+1])<<8 | int32(raw[base+2])<<16
		if val&0x800000 != 0 {
			val |= ^int32(0xFFFFFF)
		}
		v := (int64(val) * gainQ) >> 20
		if v > 32767 {
			v = 32767
			b.clippedSamples++
			if ch < len(b.clippedByChannel) {
				b.clippedByChannel[ch]++
			}
		} else if v < -32768 {
			v = -32768
			b.clippedSamples++
			if ch < len(b.clippedByChannel) {
				b.clippedByChannel[ch]++
			}
		}
		out[i*2] = byte(uint16(v))
		out[i*2+1] = byte(uint16(v) >> 8)
	}
	return out
}

// EchoRef extracts the hardware echo reference (ch8) from the same raw
// period the mic channels come from, as 16kHz mono S16 — the AEC's far-end
// input, sample-aligned with the near-end by construction because both
// arrive in one TDM frame off one ADC clock.
//
// UNITY GAIN, deliberately and not negotiably. Every mic extraction applies
// micGainDb (+24dB by default) pre-truncation to recover resolution from
// speech sitting at ~-70dBFS. The reference is not speech at -70dBFS: it is
// the playback stream at full digital scale, measured at -7.3dBFS, and
// +24dB on that is 17dB of hard clipping. A clipped reference does not
// merely cancel badly, it teaches the adaptive filter a distorted echo
// path.
//
// Returns nil when the buffer is short, which callers read as "no hardware
// reference this period" and fall back rather than cancelling against
// silence.
func (b *Beamformer) EchoRef(raw []byte) []byte {
	if len(raw) < frameSize {
		return nil
	}
	return b.extractChannel(raw, echoRefCh, 1.0)
}

// EchoRefInto is the allocation-free form used by the live mic path. dst is
// reused by DataClient only until the synchronous AEC call returns.
func (b *Beamformer) EchoRefInto(raw, dst []byte) []byte {
	if len(raw) < frameSize {
		return nil
	}
	n := len(raw) / frameSize
	if cap(dst) < n*2 {
		dst = make([]byte, n*2)
	} else {
		dst = dst[:n*2]
	}
	for i := 0; i < n; i++ {
		base := i*frameSize + echoRefCh*byteSample
		val := int32(raw[base]) | int32(raw[base+1])<<8 | int32(raw[base+2])<<16
		if val&0x800000 != 0 {
			val |= ^int32(0xFFFFFF)
		}
		v := int16(val >> 8)
		dst[i*2] = byte(v)
		dst[i*2+1] = byte(v >> 8)
	}
	return dst
}

// ClippedSamples returns the running count of samples clamped by the mic
// gain. Read from the mic goroutine only (see field comment).
func (b *Beamformer) ClippedSamples() uint64 {
	return b.clippedSamples
}

// ClippedByChannel returns the seven real microphones' clamp counters.
func (b *Beamformer) ClippedByChannel() [7]uint64 { return b.clippedByChannel }

// OutputChannel identifies the physical mic used for the most recent Process
// result. DataClient selects the matching AEC state before cancellation.
func (b *Beamformer) OutputChannel() int { return b.outputChannel }

// Diagnostics is a cheap snapshot read on the mic goroutine for periodic
// field logs. Confidence is the best-vs-second separation in [0,1].
type Diagnostics struct {
	OutputChannel      int
	Confidence         float64
	Spatial            float64
	PlaybackActive     bool
	Calibrated         bool
	DelaySamples       float64
	LevelBalanceDB     float64
	NoiseGain          float64
	Coherence          float64
	HealthyMicChannels int
}

func (b *Beamformer) Diagnostics() Diagnostics {
	return Diagnostics{
		OutputChannel:  b.outputChannel,
		Confidence:     b.lastConfidence,
		Spatial:        b.lastSpatialConf,
		PlaybackActive: b.lastPlaybackActive,
	}
}

// nearestDirection returns the index into candidateAngles closest to angleDeg.
func nearestDirection(angleDeg float64) int {
	best := 0
	bestDiff := math.Abs(angleDiff(angleDeg, candidateAngles[0]))
	for i := 1; i < nDirections; i++ {
		d := math.Abs(angleDiff(angleDeg, candidateAngles[i]))
		if d < bestDiff {
			bestDiff = d
			best = i
		}
	}
	return best
}

// angleDiff returns the signed angular difference a-b, wrapped to [-180, 180].
func angleDiff(a, b float64) float64 {
	d := math.Mod(a-b+360, 360)
	if d > 180 {
		d -= 360
	}
	return d
}

// CandidateAngles returns the steering angles used for direction estimation.
// Exposed for LED mapping in cmd/server.go.
func CandidateAngles() [nDirections]float64 {
	return candidateAngles
}
