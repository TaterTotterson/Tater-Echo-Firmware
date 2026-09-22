package microwakeword

import (
	"fmt"
	"time"
)

const (
	MaxPrerollDuration = 10 * time.Second
	MaxPrerollRate     = 192000
)

// Preroll is a bounded ring of mono PCM samples. It is intentionally
// lock-free: the audio/inference coordinator owns it from one goroutine.
type Preroll struct {
	samples []int16
	next    int
	size    int
}

// NewPreroll allocates enough samples for duration at sampleRate. Durations
// are truncated to a whole sample, which is exact for the millisecond values
// used by the voice path.
func NewPreroll(sampleRate int, duration time.Duration) (*Preroll, error) {
	if sampleRate < 1 || sampleRate > MaxPrerollRate {
		return nil, fmt.Errorf("microwakeword: pre-roll sample rate must be between 1 and %d, got %d", MaxPrerollRate, sampleRate)
	}
	if duration <= 0 || duration > MaxPrerollDuration {
		return nil, fmt.Errorf("microwakeword: pre-roll duration must be in (0, %s], got %s", MaxPrerollDuration, duration)
	}
	whole := int64(duration/time.Second) * int64(sampleRate)
	remainder := int64(duration%time.Second) * int64(sampleRate) / int64(time.Second)
	capacity := whole + remainder
	if capacity < 1 {
		return nil, fmt.Errorf("microwakeword: pre-roll duration holds no complete samples")
	}
	return &Preroll{samples: make([]int16, int(capacity))}, nil
}

// Capacity returns the maximum number of samples retained.
func (p *Preroll) Capacity() int { return len(p.samples) }

// Len returns the number of currently retained samples.
func (p *Preroll) Len() int { return p.size }

// Write retains the newest samples, overwriting the oldest when full.
func (p *Preroll) Write(samples []int16) {
	if len(samples) == 0 {
		return
	}
	capacity := len(p.samples)
	if len(samples) >= capacity {
		copy(p.samples, samples[len(samples)-capacity:])
		p.next = 0
		p.size = capacity
		return
	}

	first := min(len(samples), capacity-p.next)
	copy(p.samples[p.next:], samples[:first])
	copy(p.samples, samples[first:])
	p.next = (p.next + len(samples)) % capacity
	p.size = min(capacity, p.size+len(samples))
}

// Snapshot returns a chronological copy from oldest to newest. The copy is
// safe to hand to the network path while live capture continues.
func (p *Preroll) Snapshot() []int16 {
	out := make([]int16, p.size)
	if p.size == 0 {
		return out
	}
	capacity := len(p.samples)
	start := (p.next - p.size + capacity) % capacity
	first := min(p.size, capacity-start)
	copy(out, p.samples[start:start+first])
	copy(out[first:], p.samples[:p.size-first])
	return out
}

// Reset discards all retained audio without reallocating the ring.
func (p *Preroll) Reset() {
	p.next = 0
	p.size = 0
}
