package microwakeword

import (
	"encoding/binary"
	"fmt"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// ShadowQueueChunks bounds work waiting behind the inference goroutine. The
// microphone supplies 80 ms chunks, so eight entries permit a short scheduler
// stall without allowing old audio to build up indefinitely. When the queue is
// full, the oldest pending chunk is replaced by the newest microphone audio.
// Wake detection is latency-sensitive; scoring stale audio after the phrase
// has ended is worse than skipping one internal frame.
const ShadowQueueChunks = 8

// ShadowStallTimeout is deliberately far above normal inference latency. If
// the microphone is still producing but the scorer has made no progress for
// this long, enqueue requests one streaming-state recovery. The worker applies
// it safely after any native inference already in flight returns.
const ShadowStallTimeout = 3 * time.Second

// ShadowProducerActiveWindow distinguishes a blocked scorer from an ordinary
// capture pause. A recovery is only requested when audio was also pushed
// recently enough to represent the same continuous 80 ms stream.
const ShadowProducerActiveWindow = 500 * time.Millisecond

var (
	shadowStallTimeout         = ShadowStallTimeout
	shadowProducerActiveWindow = ShadowProducerActiveWindow
)

// DefaultShadowRefractory collapses the adjacent above-threshold windows from
// one utterance into one crossing report.
const DefaultShadowRefractory = 1500 * time.Millisecond

var shadowClockStart = time.Now()

type shadowChunk struct {
	pcm        []int16
	capturedAt time.Time
	generation uint64
}

// ShadowStats describes one reporting window. Drain returns these counters and
// starts a fresh window; the detector's streaming state is not disturbed.
type ShadowStats struct {
	Chunks      uint64
	Samples     uint64
	Scores      uint64
	Drops       uint64
	StaleDrops  uint64
	Resets      uint64
	Crossings   uint64
	CloseMisses uint64
	Errors      uint64
	Recoveries  uint64
	LastErr     string

	// MaxRawScore is the largest individual model probability. MaxScore is
	// the largest sliding-window average, which is the value compared with
	// Threshold and is therefore the useful near-miss measurement.
	MaxRawScore float32
	MaxScore    float32
	Threshold   float32
	CloseMiss   float32
	WindowSize  int

	MaxInferMs uint32
	MaxGapMs   uint32
	MaxQueueMs uint32
}

// ShadowHealth contains monotonic lifetime counters and liveness information.
// Unlike Drain, Health never changes the reporting window.
type ShadowHealth struct {
	Ready            bool
	Closed           bool
	QueueDepth       int
	AffinityCPU      int
	RecoveryPending  bool
	LastPushAgeMs    int64
	LastProcessAgeMs int64
	TotalChunks      uint64
	TotalDrops       uint64
	TotalResets      uint64
	TotalErrors      uint64
	TotalRecoveries  uint64
}

// ShadowScorer runs a microWakeWord Engine away from the real-time microphone
// goroutine. Push and PushBytes only copy and enqueue audio; a full queue drops
// and counts one old pending chunk instead of delaying capture.
type ShadowScorer struct {
	engine Engine

	threshold        float32
	peakThreshold    float32
	closeMiss        float32
	window           int
	minActiveWindows int
	minRiseScore     float32
	profile          string
	refract          time.Duration
	onCross          func(score float32, at time.Time)
	onCloseMiss      func(score float32, at time.Time)
	info             string
	affinityCPU      int

	ch   chan shadowChunk
	quit chan struct{}
	done chan struct{}
	stop sync.Once

	closed     atomic.Bool
	generation atomic.Uint64
	maxInferMs atomic.Int64
	maxGapMs   atomic.Int64
	maxQueueMs atomic.Int64
	// A stream replacement can briefly overlap the outgoing mic goroutine, so
	// producer-gap state is atomic even though steady state has one producer.
	lastPushNs      atomic.Int64
	lastProcessNs   atomic.Int64
	totalChunks     atomic.Uint64
	totalDrops      atomic.Uint64
	totalResets     atomic.Uint64
	totalErrors     atomic.Uint64
	totalRecoveries atomic.Uint64
	recoveryPending atomic.Bool

	mu        sync.Mutex
	stats     ShadowStats
	ready     bool
	lastCross time.Time
}

// NewShadowScorer starts asynchronous shadow scoring. threshold is applied to
// the mean of the most recent windowSize scores. The callback reports a
// crossing only; deciding to start a turn is intentionally outside this type.
func NewShadowScorer(engine Engine, threshold float32, windowSize int,
	closeMiss float32, onCross func(score float32, at time.Time)) (*ShadowScorer, error) {
	return NewShadowScorerWithHooks(engine, threshold, windowSize, closeMiss, ShadowHooks{Cross: onCross})
}

// ShadowHooks exposes detector events without coupling the scorer to Tater's
// transport. CloseMiss may be called for several adjacent probability windows;
// consumers are responsible for collapsing one utterance into one report.
type ShadowHooks struct {
	Cross     func(score float32, at time.Time)
	CloseMiss func(score float32, at time.Time)
	// AffinityCPU pins only the native inference worker. A nil value leaves
	// scheduling unchanged. Pin failures are reported but never stop scoring.
	AffinityCPU *int
}

// NewShadowScorerWithHooks is NewShadowScorer with close-miss observations.
func NewShadowScorerWithHooks(engine Engine, threshold float32, windowSize int,
	closeMiss float32, hooks ShadowHooks) (*ShadowScorer, error) {
	policy := DetectionPolicy{
		Profile: "legacy", Threshold: threshold, MinimumRiseScore: -1,
		Refractory: DefaultShadowRefractory,
	}
	return newShadowScorer(engine, windowSize, closeMiss, policy, hooks, true)
}

// NewShadowScorerWithPolicy applies Tater's full room-profile acceptance
// policy while preserving the same asynchronous queue and event hooks.
func NewShadowScorerWithPolicy(engine Engine, windowSize int, closeMiss float32,
	policy DetectionPolicy, hooks ShadowHooks) (*ShadowScorer, error) {
	return newShadowScorer(engine, windowSize, closeMiss, policy, hooks, false)
}

func newShadowScorer(engine Engine, windowSize int, closeMiss float32,
	policy DetectionPolicy, hooks ShadowHooks, requireCloseBelowThreshold bool) (*ShadowScorer, error) {
	if engine == nil {
		return nil, fmt.Errorf("microwakeword: shadow engine is required")
	}
	if policy.Threshold <= 0 || policy.Threshold > 1 {
		return nil, fmt.Errorf("microwakeword: shadow threshold must be in (0, 1], got %g", policy.Threshold)
	}
	if windowSize < 1 || windowSize > 100 {
		return nil, fmt.Errorf("microwakeword: shadow window must be between 1 and 100, got %d", windowSize)
	}
	if closeMiss < 0 || closeMiss > 1 || (requireCloseBelowThreshold && closeMiss > policy.Threshold) {
		return nil, fmt.Errorf("microwakeword: shadow close-miss threshold is invalid for wake threshold %g: %g", policy.Threshold, closeMiss)
	}
	if policy.PeakThreshold <= 0 {
		policy.PeakThreshold = policy.Threshold
	}
	if policy.Refractory <= 0 {
		policy.Refractory = DefaultShadowRefractory
	}
	if policy.MinimumActiveWindow < 0 || policy.MinimumActiveWindow > windowSize {
		return nil, fmt.Errorf("microwakeword: active-window requirement must be between 0 and %d", windowSize)
	}

	s := &ShadowScorer{
		engine:           engine,
		threshold:        policy.Threshold,
		peakThreshold:    policy.PeakThreshold,
		closeMiss:        closeMiss,
		window:           windowSize,
		minActiveWindows: policy.MinimumActiveWindow,
		minRiseScore:     policy.MinimumRiseScore,
		profile:          policy.Profile,
		refract:          policy.Refractory,
		onCross:          hooks.Cross,
		onCloseMiss:      hooks.CloseMiss,
		info:             engine.Info(),
		affinityCPU:      -1,
		ch:               make(chan shadowChunk, ShadowQueueChunks),
		quit:             make(chan struct{}),
		done:             make(chan struct{}),
	}
	if hooks.AffinityCPU != nil {
		s.affinityCPU = *hooks.AffinityCPU
	}
	nowNs := int64(time.Since(shadowClockStart))
	s.lastProcessNs.Store(nowNs)
	s.generation.Store(1)
	go s.run()
	return s, nil
}

// Push queues mono 16 kHz S16 PCM without blocking the caller.
func (s *ShadowScorer) Push(samples []int16) {
	if len(samples) == 0 || s.closed.Load() {
		return
	}
	pcm := append([]int16(nil), samples...)
	s.enqueue(pcm, time.Now())
}

// PushBytes queues little-endian mono S16 PCM. An odd trailing byte is ignored
// rather than shifting the sample boundary.
func (s *ShadowScorer) PushBytes(raw []byte) {
	s.PushBytesAt(raw, time.Now())
}

// PushBytesAt is PushBytes with the capture timestamp supplied by the shared
// microphone pipeline. Candidate beams derived from the same raw frames then
// carry one time identity even though their inference goroutines run
// independently.
func (s *ShadowScorer) PushBytesAt(raw []byte, capturedAt time.Time) {
	if len(raw) < 2 || s.closed.Load() {
		return
	}
	pcm := make([]int16, len(raw)/2)
	for i := range pcm {
		pcm[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
	}
	s.enqueue(pcm, capturedAt)
}

func (s *ShadowScorer) enqueue(pcm []int16, capturedAt time.Time) {
	if capturedAt.IsZero() {
		capturedAt = time.Now()
	}
	nowNs := int64(time.Since(shadowClockStart))
	// Preserve monotonic order if an outgoing and replacement mic goroutine
	// overlap briefly. The older call must not move lastPush backwards and
	// inflate the next observed gap.
	var priorPushNs int64
	for {
		previous := s.lastPushNs.Load()
		if nowNs <= previous {
			priorPushNs = previous
			break
		}
		if s.lastPushNs.CompareAndSwap(previous, nowNs) {
			priorPushNs = previous
			if previous > 0 {
				updateMax(&s.maxGapMs, (nowNs-previous)/int64(time.Millisecond))
			}
			break
		}
	}
	// A detector which has stopped making progress should not remain silently
	// dead until the entire device is rebooted. Advance the generation once;
	// when the worker returns from any native call in flight, it discards that
	// stale result and resets its streaming feature state before continuing.
	lastProcessNs := s.lastProcessNs.Load()
	producerContinuous := priorPushNs > 0 &&
		time.Duration(nowNs-priorPushNs) <= shadowProducerActiveWindow
	if producerContinuous && lastProcessNs > 0 &&
		time.Duration(nowNs-lastProcessNs) >= shadowStallTimeout &&
		s.recoveryPending.CompareAndSwap(false, true) {
		s.generation.Add(1)
		s.totalRecoveries.Add(1)
		s.mu.Lock()
		s.stats.Recoveries++
		s.mu.Unlock()
	}
	chunk := shadowChunk{
		pcm:        pcm,
		capturedAt: capturedAt,
		generation: s.generation.Load(),
	}
	select {
	case s.ch <- chunk:
	default:
		// Latest audio wins. Discard one pending chunk before retrying rather
		// than discarding the newly captured audio and letting inference move
		// farther behind real time. A concurrent consumer may free the queue
		// between these selects; every outcome remains non-blocking.
		select {
		case <-s.ch:
		default:
		}
		select {
		case s.ch <- chunk:
		default:
		}
		s.totalDrops.Add(1)
		s.mu.Lock()
		s.stats.Drops++
		s.mu.Unlock()
	}
}

// Reset begins a new continuous-audio generation. Queued audio from the old
// stream is discarded by the consumer and the native state is reset before the
// first new chunk is scored.
func (s *ShadowScorer) Reset() {
	if s.closed.Load() {
		return
	}
	// Reset is called by the microphone producer at stream start, so it is
	// also the right boundary for producer-gap telemetry.
	s.lastPushNs.Store(0)
	s.generation.Add(1)
}

// Ready reports whether a complete probability window has been observed since
// the most recent reset.
func (s *ShadowScorer) Ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
}

// Health returns a non-destructive liveness snapshot for diagnostics and
// watchdog visibility.
func (s *ShadowScorer) Health() ShadowHealth {
	nowNs := int64(time.Since(shadowClockStart))
	ageMs := func(then int64) int64 {
		if then <= 0 || then > nowNs {
			return -1
		}
		return (nowNs - then) / int64(time.Millisecond)
	}
	s.mu.Lock()
	ready := s.ready
	s.mu.Unlock()
	return ShadowHealth{
		Ready: ready, Closed: s.closed.Load(), QueueDepth: len(s.ch),
		AffinityCPU: s.affinityCPU, RecoveryPending: s.recoveryPending.Load(),
		LastPushAgeMs: ageMs(s.lastPushNs.Load()), LastProcessAgeMs: ageMs(s.lastProcessNs.Load()),
		TotalChunks: s.totalChunks.Load(), TotalDrops: s.totalDrops.Load(),
		TotalResets: s.totalResets.Load(), TotalErrors: s.totalErrors.Load(),
		TotalRecoveries: s.totalRecoveries.Load(),
	}
}

// Drain returns and clears the current telemetry window without resetting the
// model or sliding probabilities.
func (s *ShadowScorer) Drain() ShadowStats {
	s.mu.Lock()
	out := s.stats
	s.stats = ShadowStats{}
	s.mu.Unlock()
	out.Threshold = s.threshold
	out.CloseMiss = s.closeMiss
	out.WindowSize = s.window
	out.MaxInferMs = uint32(s.maxInferMs.Swap(0))
	out.MaxGapMs = uint32(s.maxGapMs.Swap(0))
	out.MaxQueueMs = uint32(s.maxQueueMs.Swap(0))
	return out
}

// Info describes the native runtime/model combination loaded by OpenShadow.
func (s *ShadowScorer) Info() string { return s.info }

// Close stops scoring and releases the native engine. It is safe to call more
// than once and safe for a producer holding a stale scorer pointer.
func (s *ShadowScorer) Close() {
	s.stop.Do(func() {
		s.closed.Store(true)
		close(s.quit)
		<-s.done
		_ = s.engine.Close()
	})
}

func (s *ShadowScorer) run() {
	defer close(s.done)
	if s.affinityCPU >= 0 {
		// This goroutine owns the pinned OS thread until it exits. Returning a
		// pinned thread to the Go scheduler could constrain unrelated work.
		runtime.LockOSThread()
		if err := pinCurrentThread(s.affinityCPU); err != nil {
			s.recordErr(fmt.Errorf("microwakeword: pin scorer to CPU %d: %w", s.affinityCPU, err))
		}
	}
	var (
		generation uint64
		scores     = make([]float32, 0, s.window)
	)
	reset := func(nextGeneration uint64) bool {
		if err := s.engine.Reset(); err != nil {
			s.recordErr(err)
			return false
		}
		generation = nextGeneration
		scores = scores[:0]
		s.recoveryPending.Store(false)
		s.totalResets.Add(1)
		s.mu.Lock()
		s.ready = false
		s.stats.Resets++
		s.mu.Unlock()
		return true
	}

	for {
		var chunk shadowChunk
		select {
		case <-s.quit:
			return
		default:
		}
		select {
		case <-s.quit:
			return
		case chunk = <-s.ch:
		}

		currentGeneration := s.generation.Load()
		if chunk.generation != currentGeneration {
			s.mu.Lock()
			s.stats.StaleDrops++
			s.mu.Unlock()
			continue
		}
		// A newly created engine is already reset. Adopt the first chunk's
		// generation without making a redundant native call or reporting a
		// reset that did not recover a discontinuity.
		if generation == 0 {
			generation = chunk.generation
		}
		if chunk.generation != generation {
			if !reset(chunk.generation) {
				continue
			}
		}
		// A sequence gap is an internal queue overflow, not a microphone stream
		// replacement. Resetting OWW for one missed 80 ms frame erases its
		// accumulated feature history. An actual capture restart changes the
		// generation above and still performs a hard reset.
		updateMax(&s.maxQueueMs, time.Since(chunk.capturedAt).Milliseconds())
		s.totalChunks.Add(1)
		s.mu.Lock()
		s.stats.Chunks++
		s.stats.Samples += uint64(len(chunk.pcm))
		s.mu.Unlock()
		started := time.Now()
		out, err := s.engine.PushPCM(chunk.pcm)
		s.lastProcessNs.Store(int64(time.Since(shadowClockStart)))
		updateMax(&s.maxInferMs, time.Since(started).Milliseconds())
		if err != nil {
			s.recordErr(err)
			continue
		}
		// Reset can arrive while native inference is running. Its output belongs
		// to the discontinued stream and must never become a late crossing.
		if chunk.generation != s.generation.Load() {
			s.mu.Lock()
			s.stats.StaleDrops++
			s.mu.Unlock()
			continue
		}

		for _, raw := range out {
			if math.IsNaN(float64(raw)) || math.IsInf(float64(raw), 0) || raw < 0 || raw > 1 {
				s.recordErr(fmt.Errorf("microwakeword: invalid probability %g", raw))
				continue
			}
			scores = append(scores, raw)
			if len(scores) > s.window {
				copy(scores, scores[len(scores)-s.window:])
				scores = scores[:s.window]
			}

			s.mu.Lock()
			s.stats.Scores++
			if raw > s.stats.MaxRawScore {
				s.stats.MaxRawScore = raw
			}
			if len(scores) < s.window {
				s.mu.Unlock()
				continue
			}
			var sum, peak float32
			activeWindows := 0
			for _, score := range scores {
				sum += score
				if score > peak {
					peak = score
				}
				if score >= s.threshold {
					activeWindows++
				}
			}
			mean := sum / float32(s.window)
			if mean > s.stats.MaxScore {
				s.stats.MaxScore = mean
			}
			now := time.Now()
			edgeCount := len(scores) / 2
			rise := float32(0)
			if edgeCount > 0 {
				var early, late float32
				for index := 0; index < edgeCount; index++ {
					early += scores[index]
					late += scores[len(scores)-edgeCount+index]
				}
				rise = late/float32(edgeCount) - early/float32(edgeCount)
			}
			policyMatched := mean >= s.threshold && peak >= s.peakThreshold && rise >= s.minRiseScore
			if s.minActiveWindows > 0 {
				policyMatched = policyMatched && activeWindows >= s.minActiveWindows
			}
			crossed := policyMatched && now.Sub(s.lastCross) >= s.refract
			closeMissed := false
			if crossed {
				s.stats.Crossings++
				s.lastCross = now
			} else if s.closeMiss > 0 && mean >= s.closeMiss && mean < s.threshold {
				s.stats.CloseMisses++
				closeMissed = true
			}
			s.ready = true
			s.mu.Unlock()

			if crossed && s.onCross != nil {
				s.onCross(mean, chunk.capturedAt)
			}
			if closeMissed && s.onCloseMiss != nil {
				s.onCloseMiss(mean, chunk.capturedAt)
			}
		}
	}
}

func (s *ShadowScorer) recordErr(err error) {
	s.totalErrors.Add(1)
	s.mu.Lock()
	s.stats.Errors++
	s.stats.LastErr = err.Error()
	s.mu.Unlock()
}

func updateMax(dst *atomic.Int64, value int64) {
	if value < 0 {
		return
	}
	for old := dst.Load(); value > old; old = dst.Load() {
		if dst.CompareAndSwap(old, value) {
			return
		}
	}
}
