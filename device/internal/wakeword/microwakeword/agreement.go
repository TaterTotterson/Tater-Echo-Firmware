package microwakeword

import (
	"sync"
	"time"
)

// DualWakeAgreementWindow allows the two independently streaming models to
// place their crossing on different 80 ms frames while still requiring them
// to describe the same utterance. Capture timestamps are used rather than
// callback time, so scheduler and inference latency do not widen this window.
const DualWakeAgreementWindow = 1200 * time.Millisecond

type AgreementSource uint8

const (
	AgreementMWW AgreementSource = iota + 1
	AgreementOWW
)

type AgreementMatch struct {
	Lane     int
	At       time.Time
	MWWAt    time.Time
	OWWAt    time.Time
	MWWScore float32
	OWWScore float32
}

type AgreementStats struct {
	MWWCrossings uint64  `json:"mwwCrossings"`
	OWWCrossings uint64  `json:"owwCrossings"`
	Agreements   uint64  `json:"agreements"`
	Expired      uint64  `json:"expired"`
	LastDeltaMS  int64   `json:"lastDeltaMs"`
	LastMWWScore float32 `json:"lastMwwScore"`
	LastOWWScore float32 `json:"lastOwwScore"`
}

type agreementEvent struct {
	at    time.Time
	score float32
}

// AgreementMatcher correlates independently streaming MWW and OWW crossings.
// Each wake lane is matched only with itself, preserving the exact directional
// audio path instead of allowing two different speakers to satisfy the pair.
type AgreementMatcher struct {
	mu     sync.Mutex
	window time.Duration
	mww    map[int]agreementEvent
	oww    map[int]agreementEvent
	stats  AgreementStats
}

func NewAgreementMatcher(window time.Duration) *AgreementMatcher {
	if window <= 0 {
		window = DualWakeAgreementWindow
	}
	return &AgreementMatcher{
		window: window,
		mww:    make(map[int]agreementEvent),
		oww:    make(map[int]agreementEvent),
	}
}

func (matcher *AgreementMatcher) Observe(source AgreementSource, lane int, score float32, at time.Time) (AgreementMatch, bool) {
	if matcher == nil || (source != AgreementMWW && source != AgreementOWW) {
		return AgreementMatch{}, false
	}
	if at.IsZero() {
		at = time.Now()
	}
	matcher.mu.Lock()
	defer matcher.mu.Unlock()

	event := agreementEvent{at: at, score: score}
	current, other := matcher.mww, matcher.oww
	if source == AgreementMWW {
		matcher.stats.MWWCrossings++
		matcher.stats.LastMWWScore = score
	} else {
		matcher.stats.OWWCrossings++
		matcher.stats.LastOWWScore = score
		current, other = matcher.oww, matcher.mww
	}
	current[lane] = event
	counterpart, ok := other[lane]
	if !ok {
		return AgreementMatch{}, false
	}
	delta := at.Sub(counterpart.at)
	if delta < 0 {
		delta = -delta
	}
	if delta > matcher.window {
		// Retain only the newer side; it may still pair with the other model's
		// next crossing. Keeping the old side could join separate utterances.
		matcher.stats.Expired++
		if counterpart.at.Before(at) {
			delete(other, lane)
		} else {
			delete(current, lane)
		}
		return AgreementMatch{}, false
	}

	mwwEvent := matcher.mww[lane]
	owwEvent := matcher.oww[lane]
	delete(matcher.mww, lane)
	delete(matcher.oww, lane)
	matcher.stats.Agreements++
	matcher.stats.LastDeltaMS = delta.Milliseconds()
	matchedAt := mwwEvent.at
	if owwEvent.at.After(matchedAt) {
		matchedAt = owwEvent.at
	}
	return AgreementMatch{
		Lane: lane, At: matchedAt, MWWAt: mwwEvent.at, OWWAt: owwEvent.at,
		MWWScore: mwwEvent.score, OWWScore: owwEvent.score,
	}, true
}

func (matcher *AgreementMatcher) ResetLane(lane int) {
	if matcher == nil {
		return
	}
	matcher.mu.Lock()
	delete(matcher.mww, lane)
	delete(matcher.oww, lane)
	matcher.mu.Unlock()
}

func (matcher *AgreementMatcher) Stats() AgreementStats {
	if matcher == nil {
		return AgreementStats{}
	}
	matcher.mu.Lock()
	defer matcher.mu.Unlock()
	return matcher.stats
}
