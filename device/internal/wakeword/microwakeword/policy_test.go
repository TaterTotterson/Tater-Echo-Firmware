package microwakeword

import (
	"math"
	"testing"
	"time"
)

func closeTo(left, right float32) bool { return math.Abs(float64(left-right)) < 0.0001 }

func TestDetectionPolicyMatchesTaterRoomProfiles(t *testing.T) {
	balanced := MakeDetectionPolicy("normal", "balanced", 0.97, 5)
	if balanced.Profile != "balanced" || !closeTo(balanced.Threshold, 0.97) ||
		balanced.MinimumActiveWindow != 3 || balanced.Refractory != 1200*time.Millisecond {
		t.Fatalf("balanced policy = %+v", balanced)
	}
	far := MakeDetectionPolicy("high", "far_field", 0.92, 5)
	if far.Profile != "far_field" || far.MinimumActiveWindow != 1 || far.Refractory != 800*time.Millisecond {
		t.Fatalf("far-field policy = %+v", far)
	}
	strict := MakeDetectionPolicy("normal", "strict", 0.90, 5)
	if strict.Profile != "strict" || strict.Threshold < 247.0/255.0 ||
		strict.MinimumActiveWindow != 4 || strict.Refractory != 1600*time.Millisecond {
		t.Fatalf("strict policy = %+v", strict)
	}
	tv := MakeDetectionPolicy("normal", "tv_nearby", 0.97, 5)
	if tv.Profile != "tv_nearby" || !tv.RequireVerification ||
		tv.Threshold > 224.0/255.0+0.0001 || tv.PeakThreshold < 235.0/255.0 ||
		tv.Refractory != 2400*time.Millisecond {
		t.Fatalf("TV-nearby policy = %+v", tv)
	}
}

func TestShadowPolicyRequiresActiveWindowsAndPeak(t *testing.T) {
	engine := &fakeShadowEngine{batches: [][]float32{{0.99, 0.70, 0.70}}}
	policy := DetectionPolicy{
		Profile: "test", Threshold: 0.79, PeakThreshold: 0.95,
		MinimumActiveWindow: 2, MinimumRiseScore: -1, Refractory: time.Second,
	}
	s, err := NewShadowScorerWithPolicy(engine, 3, 0.6, policy, ShadowHooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Push(make([]int16, 1280))
	waitShadow(t, "policy window", s.Ready)
	if st := s.Drain(); st.Crossings != 0 {
		t.Fatalf("single active window crossed a two-window policy: %+v", st)
	}
}
