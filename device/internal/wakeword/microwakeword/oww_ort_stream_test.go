package microwakeword

import (
	"fmt"
	"testing"
)

type fakeORTInferer struct {
	scores        []float32
	classifyCalls int
	closed        bool
}

func (inferer *fakeORTInferer) Melspectrogram([]float32) ([]float32, int, error) {
	frames := 8
	return make([]float32, frames*owwMelBins), frames, nil
}

func (inferer *fakeORTInferer) Embed([]float32) ([]float32, error) {
	return make([]float32, owwFeatureDim), nil
}

func (inferer *fakeORTInferer) Classify([]float32) (float32, error) {
	if inferer.classifyCalls >= len(inferer.scores) {
		return 0, fmt.Errorf("unexpected classifier call %d", inferer.classifyCalls+1)
	}
	score := inferer.scores[inferer.classifyCalls]
	inferer.classifyCalls++
	return score, nil
}

func (*fakeORTInferer) Info() string { return "fake ONNX Runtime" }

func (inferer *fakeORTInferer) Close() error {
	inferer.closed = true
	return nil
}

func newFakeORTEngine(inferer *fakeORTInferer, threshold float32, patience int) *ortOWWEngine {
	engine := &ortOWWEngine{
		inferer: inferer,
		settings: OWWSettings{
			Threshold: threshold,
			Patience:  patience,
		},
		info:    inferer.Info(),
		scratch: make([]float32, owwMelWindow*owwMelBins),
	}
	if err := engine.Reset(); err != nil {
		panic(err)
	}
	return engine
}

func TestORTOWWStreamingWarmupAndPartialChunks(t *testing.T) {
	inferer := &fakeORTInferer{scores: []float32{0.25}}
	engine := newFakeORTEngine(inferer, 0.9, 1)

	// Fifteen complete 80 ms chunks only warm the 16-frame embedding window.
	scores, err := engine.PushPCM(make([]int16, 15*owwChunkSamples+owwChunkSamples/2))
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != 0 || inferer.classifyCalls != 0 {
		t.Fatalf("warmup produced scores=%v calls=%d", scores, inferer.classifyCalls)
	}

	// The retained half chunk completes frame sixteen and produces one score.
	scores, err = engine.PushPCM(make([]int16, owwChunkSamples/2))
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != 1 || scores[0] != 0.25 || inferer.classifyCalls != 1 {
		t.Fatalf("completed chunk produced scores=%v calls=%d", scores, inferer.classifyCalls)
	}
}

func TestORTOWWConfirmRequiresConsecutiveScores(t *testing.T) {
	// 18 candidate chunks plus the predict_clip padding produce 18 scores
	// after the clean 16-embedding warm-up. Put the accepted run at the end.
	scores := make([]float32, 18)
	copy(scores[len(scores)-3:], []float32{0.95, 0.96, 0.97})
	inferer := &fakeORTInferer{scores: scores}
	engine := newFakeORTEngine(inferer, 0.92, 3)

	result, err := engine.Confirm(make([]int16, 18*owwChunkSamples))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Accepted || result.Consecutive != 3 || result.Score != 0.97 {
		t.Fatalf("unexpected confirmation result: %+v", result)
	}
}

func TestORTOWWConfirmPadsShortRetargetedCandidate(t *testing.T) {
	// The live Biscuit can cross MWW only eight frames (640 ms) after a
	// directional lane retarget. predict_clip padding must still give OWW
	// enough feature history and trailing frames to confirm that phrase.
	scores := make([]float32, 8)
	copy(scores[len(scores)-3:], []float32{0.95, 0.96, 0.97})
	inferer := &fakeORTInferer{scores: scores}
	engine := newFakeORTEngine(inferer, 0.92, 3)

	result, err := engine.Confirm(make([]int16, 8*owwChunkSamples))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Accepted || result.Consecutive != 3 || result.Score != 0.97 {
		t.Fatalf("short candidate was not confirmed: %+v", result)
	}
}

func TestORTOWWConfirmRejectsBrokenRun(t *testing.T) {
	scores := make([]float32, 18)
	copy(scores[len(scores)-3:], []float32{0.95, 0.4, 0.97})
	inferer := &fakeORTInferer{scores: scores}
	engine := newFakeORTEngine(inferer, 0.92, 2)

	result, err := engine.Confirm(make([]int16, 18*owwChunkSamples))
	if err != nil {
		t.Fatal(err)
	}
	if result.Accepted || result.Consecutive != 1 || result.Score != 0.97 {
		t.Fatalf("unexpected confirmation result: %+v", result)
	}
}
