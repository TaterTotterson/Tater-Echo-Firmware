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
