package microwakeword

import (
	"encoding/binary"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// ShadowQueueChunks bounds work waiting behind the inference goroutine. The
// microphone supplies 80 ms chunks, so eight entries permit a short scheduler
// stall without allowing old audio to build up indefinitely.
const ShadowQueueChunks = 8

// DefaultShadowRefractory collapses the adjacent above-threshold windows from
// one utterance into one crossing report.
const DefaultShadowRefractory = 1500 * time.Millisecond

var shadowClockStart = time.Now()

type shadowChunk struct {
	pcm        []int16
	capturedAt time.Time
	generation uint64
	sequence   uint64
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

// ShadowScorer runs a microWakeWord Engine away from the real-time microphone
// goroutine. Push and PushBytes only copy and enqueue audio; a full queue drops
// and counts the chunk instead of delaying capture.
type ShadowScorer struct {
	engine Engine

	threshold float32
	closeMiss float32
	window    int
	refract   time.Duration
	onCross   func(score float32, at time.Time)
	info      string

	ch   chan shadowChunk
	quit chan struct{}
	done chan struct{}
	stop sync.Once

	closed     atomic.Bool
	generation atomic.Uint64
	sequence   atomic.Uint64
	maxInferMs atomic.Int64
	maxGapMs   atomic.Int64
	maxQueueMs atomic.Int64
	// A stream replacement can briefly overlap the outgoing mic goroutine, so
	// producer-gap state is atomic even though steady state has one producer.
	lastPushNs atomic.Int64

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
	if engine == nil {
		return nil, fmt.Errorf("microwakeword: shadow engine is required")
	}
	if threshold <= 0 || threshold > 1 {
		return nil, fmt.Errorf("microwakeword: shadow threshold must be in (0, 1], got %g", threshold)
	}
	if windowSize < 1 || windowSize > 100 {
		return nil, fmt.Errorf("microwakeword: shadow window must be between 1 and 100, got %d", windowSize)
	}
	if closeMiss < 0 || closeMiss > threshold {
		return nil, fmt.Errorf("microwakeword: shadow close-miss threshold must be in [0, %g], got %g", threshold, closeMiss)
	}

	s := &ShadowScorer{
		engine:    engine,
		threshold: threshold,
		closeMiss: closeMiss,
		window:    windowSize,
		refract:   DefaultShadowRefractory,
		onCross:   onCross,
		info:      engine.Info(),
		ch:        make(chan shadowChunk, ShadowQueueChunks),
		quit:      make(chan struct{}),
		done:      make(chan struct{}),
	}
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
	s.enqueue(pcm)
}

// PushBytes queues little-endian mono S16 PCM. An odd trailing byte is ignored
// rather than shifting the sample boundary.
func (s *ShadowScorer) PushBytes(raw []byte) {
	if len(raw) < 2 || s.closed.Load() {
		return
	}
	pcm := make([]int16, len(raw)/2)
	for i := range pcm {
		pcm[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
	}
	s.enqueue(pcm)
}

func (s *ShadowScorer) enqueue(pcm []int16) {
	now := time.Now()
	nowNs := int64(time.Since(shadowClockStart))
	// Preserve monotonic order if an outgoing and replacement mic goroutine
	// overlap briefly. The older call must not move lastPush backwards and
	// inflate the next observed gap.
	for {
		previous := s.lastPushNs.Load()
		if nowNs <= previous {
			break
		}
		if s.lastPushNs.CompareAndSwap(previous, nowNs) {
			if previous > 0 {
				updateMax(&s.maxGapMs, (nowNs-previous)/int64(time.Millisecond))
			}
			break
		}
	}
	chunk := shadowChunk{
		pcm:        pcm,
		capturedAt: now,
		generation: s.generation.Load(),
		sequence:   s.sequence.Add(1),
	}
	select {
	case s.ch <- chunk:
	default:
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
	var (
		generation uint64
		lastSeq    uint64
		scores     = make([]float32, 0, s.window)
	)
	reset := func(nextGeneration uint64) bool {
		if err := s.engine.Reset(); err != nil {
			s.recordErr(err)
			return false
		}
		generation = nextGeneration
		lastSeq = 0
		scores = scores[:0]
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
			lastSeq = chunk.sequence - 1
		}
		if chunk.generation != generation || (lastSeq != 0 && chunk.sequence != lastSeq+1) {
			if !reset(chunk.generation) {
				continue
			}
		}
		lastSeq = chunk.sequence

		updateMax(&s.maxQueueMs, time.Since(chunk.capturedAt).Milliseconds())
		s.mu.Lock()
		s.stats.Chunks++
		s.stats.Samples += uint64(len(chunk.pcm))
		s.mu.Unlock()
		started := time.Now()
		out, err := s.engine.PushPCM(chunk.pcm)
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
			var sum float32
			for _, score := range scores {
				sum += score
			}
			mean := sum / float32(s.window)
			if mean > s.stats.MaxScore {
				s.stats.MaxScore = mean
			}
			now := time.Now()
			crossed := mean >= s.threshold && now.Sub(s.lastCross) >= s.refract
			if crossed {
				s.stats.Crossings++
				s.lastCross = now
			} else if s.closeMiss > 0 && mean >= s.closeMiss && mean < s.threshold {
				s.stats.CloseMisses++
			}
			s.ready = true
			s.mu.Unlock()

			if crossed && s.onCross != nil {
				s.onCross(mean, chunk.capturedAt)
			}
		}
	}
}

func (s *ShadowScorer) recordErr(err error) {
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
