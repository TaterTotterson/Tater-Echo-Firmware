package microwakeword

import (
	"testing"
	"time"
)

type fakeBoundaryEngine struct {
	scores []float32
	period time.Duration
}

func (f *fakeBoundaryEngine) PushPCM([]int16) ([]float32, error) {
	return append([]float32(nil), f.scores...), nil
}
func (f *fakeBoundaryEngine) Reset() error               { return nil }
func (f *fakeBoundaryEngine) Close() error               { return nil }
func (f *fakeBoundaryEngine) Info() string               { return "fake boundary" }
func (f *fakeBoundaryEngine) scorePeriod() time.Duration { return f.period }

func TestBoundaryEstimatorSelectsHighestEndpointScore(t *testing.T) {
	detectedAt := time.Unix(100, 0)
	engine := &fakeBoundaryEngine{
		scores: []float32{0.1, 0.81, 0.96, 0.84, 0.2},
		period: 20 * time.Millisecond,
	}
	estimator := &BoundaryEstimator{
		engine: engine,
		settings: BundleWakeBoundary{
			Threshold: 0.8, SearchBeforeMS: 200, SearchAfterMS: 50,
		},
	}
	pcm := make([]byte, 1600*2)
	estimate, ok, err := estimator.Estimate(pcm, detectedAt, detectedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected an endpoint")
	}
	want := detectedAt.Add(-40 * time.Millisecond)
	if !estimate.At.Equal(want) || estimate.Score != 0.96 {
		t.Fatalf("estimate=%+v want at=%v score=.96", estimate, want)
	}
}

func TestGuardExactCommandBoundaryRetainsCommandLead(t *testing.T) {
	endpointAt := time.Unix(100, 0)
	if got, want := GuardExactCommandBoundary(endpointAt), endpointAt.Add(-80*time.Millisecond); !got.Equal(want) {
		t.Fatalf("guarded boundary = %v, want %v", got, want)
	}
	if got := GuardExactCommandBoundary(time.Time{}); !got.IsZero() {
		t.Fatalf("zero endpoint became %v", got)
	}
}
