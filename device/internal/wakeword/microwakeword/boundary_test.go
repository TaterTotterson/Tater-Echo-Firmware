package microwakeword

import (
	"math"
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

func TestBoundaryEstimatorSelectsLeadingCrossingOfStrongestRun(t *testing.T) {
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
	want := detectedAt.Add(-60 * time.Millisecond)
	if !estimate.At.Equal(want) || estimate.Score != 0.96 {
		t.Fatalf("estimate=%+v want at=%v score=.96", estimate, want)
	}
}

func TestBoundaryEstimatorConfidenceRuns(t *testing.T) {
	for _, tc := range []struct {
		name         string
		scores       []float32
		searchBefore int
		searchAfter  int
		audioEndMS   int
		endpointMS   int
		wantLeadMS   int
		wantScore    float32
		wantOK       bool
	}{
		{
			// Recorded Sat1 scores: the peak at 2950 ms clipped "what";
			// the leading crossing at 2890 ms retained the complete question.
			name:         "recorded rising confidence and plateau",
			scores:       []float32{0.5, 0.8863, 0.9686, 0.9922, 1, 1},
			searchBefore: 700, searchAfter: 120,
			wantLeadMS: 80, wantScore: 1, wantOK: true,
		},
		{
			name:         "separate weaker early and late blips",
			scores:       []float32{0.85, 0.2, 0.86, 0.99, 0.1, 0.9},
			searchBefore: 700, searchAfter: 120,
			wantLeadMS: 60, wantScore: 0.99, wantOK: true,
		},
		{
			name:         "equal peaks keep the earlier run",
			scores:       []float32{0.9, 1, 0.1, 1},
			searchBefore: 700, searchAfter: 120,
			wantLeadMS: 60, wantScore: 1, wantOK: true,
		},
		{
			name:         "leading crossing is bounded by search window",
			scores:       []float32{1, 0.9, 1},
			searchBefore: 20, searchAfter: 0,
			wantLeadMS: 20, wantScore: 1, wantOK: true,
		},
		{
			name:         "search end is inclusive and later peak is excluded",
			scores:       []float32{0.85, 0.9, 1},
			searchBefore: 20, searchAfter: 20, audioEndMS: 40,
			wantLeadMS: 0, wantScore: 0.9, wantOK: true,
		},
		{
			name:         "threshold is inclusive and endpoint offset follows selection",
			scores:       []float32{0.82, 0.99},
			searchBefore: 700, searchAfter: 120, endpointMS: -10,
			wantLeadMS: 30, wantScore: 0.99, wantOK: true,
		},
		{
			name:         "no valid endpoint",
			scores:       []float32{0.819, 0.1},
			searchBefore: 700, searchAfter: 120,
		},
		{
			name:         "nonfinite score ends a confidence run",
			scores:       []float32{0.9, float32(math.NaN()), 0.99},
			searchBefore: 700, searchAfter: 120,
			wantLeadMS: 0, wantScore: 0.99, wantOK: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			detectedAt := time.Unix(100, 0)
			estimator := &BoundaryEstimator{
				engine: &fakeBoundaryEngine{scores: tc.scores, period: 20 * time.Millisecond},
				settings: BundleWakeBoundary{
					Threshold: 0.82, SearchBeforeMS: tc.searchBefore, SearchAfterMS: tc.searchAfter,
					EndpointOffsetMS: tc.endpointMS,
				},
			}
			estimate, ok, err := estimator.Estimate(make([]byte, 1600*2), detectedAt.Add(time.Duration(tc.audioEndMS)*time.Millisecond), detectedAt)
			if err != nil || ok != tc.wantOK {
				t.Fatalf("estimate=%+v ok=%v err=%v, want ok=%v", estimate, ok, err, tc.wantOK)
			}
			if !tc.wantOK {
				if !estimate.At.IsZero() {
					t.Fatalf("invalid endpoint returned %+v", estimate)
				}
				return
			}
			want := detectedAt.Add(-time.Duration(tc.wantLeadMS) * time.Millisecond)
			if !estimate.At.Equal(want) || estimate.Score != tc.wantScore {
				t.Fatalf("estimate=%+v, want at=%v score=%v", estimate, want, tc.wantScore)
			}
		})
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
