package microwakeword

import (
	"testing"
	"time"
)

func TestAgreementMatcherAcceptsEitherArrivalOrder(t *testing.T) {
	base := time.Now()
	for _, first := range []AgreementSource{AgreementMWW, AgreementOWW} {
		matcher := NewAgreementMatcher(time.Second)
		second := AgreementOWW
		if first == AgreementOWW {
			second = AgreementMWW
		}
		if _, ok := matcher.Observe(first, 1, 0.96, base); ok {
			t.Fatal("one model produced an agreement")
		}
		match, ok := matcher.Observe(second, 1, 0.97, base.Add(240*time.Millisecond))
		if !ok || match.Lane != 1 || match.MWWScore == 0 || match.OWWScore == 0 {
			t.Fatalf("match = %+v, %t", match, ok)
		}
	}
}

func TestAgreementMatcherRequiresSameLaneAndBoundedTime(t *testing.T) {
	base := time.Now()
	matcher := NewAgreementMatcher(500 * time.Millisecond)
	matcher.Observe(AgreementMWW, 0, 0.96, base)
	if _, ok := matcher.Observe(AgreementOWW, 1, 0.97, base.Add(80*time.Millisecond)); ok {
		t.Fatal("different lanes agreed")
	}
	if _, ok := matcher.Observe(AgreementOWW, 0, 0.97, base.Add(time.Second)); ok {
		t.Fatal("separate utterances agreed")
	}
	stats := matcher.Stats()
	if stats.Agreements != 0 || stats.Expired != 1 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestAgreementMatcherResetLaneDropsHalfMatch(t *testing.T) {
	base := time.Now()
	matcher := NewAgreementMatcher(time.Second)
	matcher.Observe(AgreementMWW, 0, 0.96, base)
	matcher.ResetLane(0)
	if _, ok := matcher.Observe(AgreementOWW, 0, 0.97, base.Add(80*time.Millisecond)); ok {
		t.Fatal("event survived a directional lane reset")
	}
}

func TestAgreementMatcherUsesEarlierDetectorCommandBoundary(t *testing.T) {
	base := time.Now()
	matcher := NewAgreementMatcher(time.Second)
	mww := WakeDetection{
		Score: 0.96, DetectedAt: base.Add(200 * time.Millisecond),
		CommandBoundaryAt: base.Add(120 * time.Millisecond),
	}
	companion := WakeDetection{
		Score: 0.98, DetectedAt: base.Add(160 * time.Millisecond),
		CommandBoundaryAt: base.Add(40 * time.Millisecond),
	}
	if _, ok := matcher.ObserveDetection(AgreementMWW, 0, mww); ok {
		t.Fatal("one detector produced an agreement")
	}
	match, ok := matcher.ObserveDetection(AgreementOWW, 0, companion)
	if !ok {
		t.Fatal("matching detector events did not agree")
	}
	if !match.CommandBoundaryAt.Equal(companion.CommandBoundaryAt) {
		t.Fatalf("combined boundary = %v, want earlier companion boundary %v",
			match.CommandBoundaryAt, companion.CommandBoundaryAt)
	}
	if !match.MWWBoundaryAt.Equal(mww.CommandBoundaryAt) ||
		!match.OWWBoundaryAt.Equal(companion.CommandBoundaryAt) {
		t.Fatalf("per-detector boundaries were not preserved: %+v", match)
	}
}

func TestAgreementMatcherCompanionCannotDelayOrOverRewindBoundary(t *testing.T) {
	base := time.Now()

	delayMatcher := NewAgreementMatcher(time.Second)
	mww := WakeDetection{
		Score: 0.96, DetectedAt: base.Add(500 * time.Millisecond),
		CommandBoundaryAt: base.Add(420 * time.Millisecond),
	}
	laterCompanion := WakeDetection{
		Score: 0.98, DetectedAt: base.Add(600 * time.Millisecond),
		CommandBoundaryAt: base.Add(520 * time.Millisecond),
	}
	delayMatcher.ObserveDetection(AgreementMWW, 0, mww)
	match, ok := delayMatcher.ObserveDetection(AgreementOWW, 0, laterCompanion)
	if !ok || !match.CommandBoundaryAt.Equal(mww.CommandBoundaryAt) {
		t.Fatalf("later companion changed boundary: match=%+v ok=%t", match, ok)
	}

	rewindMatcher := NewAgreementMatcher(time.Second)
	earlyCompanion := WakeDetection{
		Score: 0.98, DetectedAt: base,
		CommandBoundaryAt: base.Add(-MaxCommandBoundaryRewind),
	}
	rewindMatcher.ObserveDetection(AgreementOWW, 0, earlyCompanion)
	match, ok = rewindMatcher.ObserveDetection(AgreementMWW, 0, mww)
	wantBoundary := mww.DetectedAt.Add(-MaxCommandBoundaryRewind)
	if !ok || !match.CommandBoundaryAt.Equal(wantBoundary) {
		t.Fatalf("combined boundary = %v, want bounded rewind %v (ok=%t)",
			match.CommandBoundaryAt, wantBoundary, ok)
	}
}
