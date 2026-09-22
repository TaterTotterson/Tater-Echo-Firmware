package microwakeword

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeShadowEngine struct {
	mu         sync.Mutex
	batches    [][]float32
	inputs     [][]int16
	delay      time.Duration
	fail       bool
	resetCount int
	closeCount int
}

func (f *fakeShadowEngine) PushPCM(pcm []int16) ([]float32, error) {
	f.mu.Lock()
	f.inputs = append(f.inputs, append([]int16(nil), pcm...))
	delay, fail := f.delay, f.fail
	var scores []float32
	if len(f.batches) > 0 {
		scores = append([]float32(nil), f.batches[0]...)
		f.batches = f.batches[1:]
	}
	f.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	if fail {
		return nil, errors.New("synthetic inference failure")
	}
	return scores, nil
}

func (f *fakeShadowEngine) Reset() error {
	f.mu.Lock()
	f.resetCount++
	f.mu.Unlock()
	return nil
}

func (f *fakeShadowEngine) Close() error {
	f.mu.Lock()
	f.closeCount++
	f.mu.Unlock()
	return nil
}

func (f *fakeShadowEngine) Info() string { return "fake stride=2" }

func waitShadow(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func shadowPeek(s *ShadowScorer) ShadowStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

func TestShadowUsesSlidingWindowMean(t *testing.T) {
	engine := &fakeShadowEngine{batches: [][]float32{{0.4, 1.0, 1.0}}}
	var (
		mu       sync.Mutex
		crossing []float32
	)
	s, err := NewShadowScorer(engine, 0.79, 3, 0.7, func(score float32, _ time.Time) {
		mu.Lock()
		crossing = append(crossing, score)
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.Push(make([]int16, 1280))
	waitShadow(t, "three scores", func() bool { return shadowPeek(s).Scores == 3 })
	waitShadow(t, "crossing callback", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(crossing) == 1
	})

	st := s.Drain()
	if st.Crossings != 1 || st.MaxScore < 0.799 || st.MaxScore > 0.801 {
		t.Fatalf("unexpected sliding-window result: %+v", st)
	}
	if st.MaxRawScore != 1 || st.Threshold != 0.79 || st.WindowSize != 3 {
		t.Fatalf("unexpected score metadata: %+v", st)
	}
	if !s.Ready() {
		t.Fatal("scorer did not become ready after one complete window")
	}
}

func TestShadowPushBytesDecodesLittleEndian(t *testing.T) {
	engine := &fakeShadowEngine{}
	s, err := NewShadowScorer(engine, 0.5, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.PushBytes([]byte{0x34, 0x12, 0xfe, 0xff, 0xaa})
	waitShadow(t, "byte input", func() bool { return shadowPeek(s).Chunks == 1 })
	engine.mu.Lock()
	got := append([]int16(nil), engine.inputs[0]...)
	engine.mu.Unlock()
	if len(got) != 2 || got[0] != 0x1234 || got[1] != -2 {
		t.Fatalf("decoded samples = %v, want [4660 -2]", got)
	}
}

func TestShadowPushNeverBlocksAndCountsDrops(t *testing.T) {
	engine := &fakeShadowEngine{delay: 200 * time.Millisecond}
	s, err := NewShadowScorer(engine, 0.5, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	started := time.Now()
	for i := 0; i < ShadowQueueChunks*10; i++ {
		s.Push(make([]int16, 1280))
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Push blocked behind inference for %v", elapsed)
	}
	if st := s.Drain(); st.Drops == 0 {
		t.Fatal("full inference queue did not report drops")
	}
}

func TestShadowResetDoesNotSpliceStreams(t *testing.T) {
	engine := &fakeShadowEngine{batches: [][]float32{{0.9}, {0.9}, {0.9}}}
	s, err := NewShadowScorer(engine, 0.5, 2, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.Push([]int16{1})
	waitShadow(t, "first stream score", func() bool { return shadowPeek(s).Scores == 1 })
	s.Reset()
	s.Push([]int16{2})
	waitShadow(t, "native reset", func() bool {
		engine.mu.Lock()
		defer engine.mu.Unlock()
		return engine.resetCount == 1
	})
	if s.Ready() {
		t.Fatal("one score from each side of Reset filled one sliding window")
	}
	s.Push([]int16{3})
	waitShadow(t, "new stream window", s.Ready)
	if st := s.Drain(); st.Crossings != 1 || st.Resets != 1 {
		t.Fatalf("unexpected post-reset stats: %+v", st)
	}
}

func TestShadowResetDiscardsInferenceAlreadyInFlight(t *testing.T) {
	engine := &fakeShadowEngine{
		batches: [][]float32{{1}},
		delay:   100 * time.Millisecond,
	}
	var crossings int
	s, err := NewShadowScorer(engine, 0.5, 1, 0, func(float32, time.Time) {
		crossings++
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Push([]int16{1})
	waitShadow(t, "inference to start", func() bool {
		engine.mu.Lock()
		defer engine.mu.Unlock()
		return len(engine.inputs) == 1
	})
	s.Reset()
	waitShadow(t, "in-flight output to be discarded", func() bool {
		return shadowPeek(s).StaleDrops == 1
	})
	st := s.Drain()
	if crossings != 0 || st.Crossings != 0 || st.Scores != 0 {
		t.Fatalf("stale inference escaped reset: crossings=%d stats=%+v", crossings, st)
	}
}

func TestShadowInferenceErrorIsReportedAndSurvived(t *testing.T) {
	engine := &fakeShadowEngine{fail: true}
	s, err := NewShadowScorer(engine, 0.5, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Push([]int16{1})
	waitShadow(t, "inference error", func() bool { return shadowPeek(s).Errors == 1 })
	engine.mu.Lock()
	engine.fail = false
	engine.batches = [][]float32{{0.2}}
	engine.mu.Unlock()
	s.Push([]int16{2})
	waitShadow(t, "recovered score", func() bool { return shadowPeek(s).Scores == 1 })
	st := s.Drain()
	if st.Errors != 1 || st.LastErr == "" || st.Chunks != 2 {
		t.Fatalf("unexpected recovery stats: %+v", st)
	}
}

func TestShadowCloseIsIdempotent(t *testing.T) {
	engine := &fakeShadowEngine{}
	s, err := NewShadowScorer(engine, 0.5, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s.Close()
	s.Push([]int16{1})
	engine.mu.Lock()
	closed := engine.closeCount
	inputs := len(engine.inputs)
	engine.mu.Unlock()
	if closed != 1 || inputs != 0 {
		t.Fatalf("close count=%d inputs after close=%d", closed, inputs)
	}
}
