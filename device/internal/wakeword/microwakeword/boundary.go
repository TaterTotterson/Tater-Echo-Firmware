package microwakeword

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// BoundaryEstimator keeps the bundle's small endpoint model resident. It is
// replayed only after a wake detector accepts, so all detector modes share one
// exact acoustic boundary without adding continuous inference load.
type BoundaryEstimator struct {
	mu       sync.Mutex
	engine   Engine
	settings BundleWakeBoundary
	closed   bool
}

type BoundaryEstimate struct {
	At    time.Time
	Score float32
}

func OpenBoundaryEstimator(packageName string) (*BoundaryEstimator, error) {
	prefix, err := companionPrefix(packageName)
	if err != nil {
		return nil, err
	}
	raw, err := readLimited(filepath.Join(PackageDir(), prefix+CompanionBundleSuffix), maxCompanionBundleBytes)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrBoundaryUnavailable
		}
		return nil, err
	}
	bundle, err := ParseWakeBundle(raw)
	if err != nil {
		return nil, err
	}
	b := bundle.WakeBoundary
	if b.Model == "" {
		return nil, ErrBoundaryUnavailable
	}
	model, err := readVerifiedFile(filepath.Join(PackageDir(), b.Model), maxBoundaryModelBytes, b.ModelSHA256)
	if err != nil {
		return nil, err
	}
	runtime, err := OpenNativeRuntime(filepath.Join(PackageDir(), RuntimeFilename))
	if err != nil {
		return nil, err
	}
	engine, err := runtime.NewEngine(model, RuntimeSettings{
		WakeWord: bundle.WakeWord, Label: bundle.WakeWord, Threshold: b.Threshold,
		SlidingWindow: 1, TensorArenaSize: b.TensorArenaSize, Frontend: resolvedFrontend(b.Frontend),
	})
	if err != nil {
		return nil, err
	}
	return &BoundaryEstimator{engine: engine, settings: b}, nil
}

var ErrBoundaryUnavailable = fmt.Errorf("microwakeword: wake boundary model is not installed")

func (e *BoundaryEstimator) Estimate(pcm []byte, audioEndAt, detectedAt time.Time) (BoundaryEstimate, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.engine == nil {
		return BoundaryEstimate{}, false, ErrBoundaryUnavailable
	}
	if len(pcm) < 2 || len(pcm)%2 != 0 || audioEndAt.IsZero() || detectedAt.IsZero() {
		return BoundaryEstimate{}, false, nil
	}
	if err := e.engine.Reset(); err != nil {
		return BoundaryEstimate{}, false, err
	}
	samples := make([]int16, len(pcm)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(pcm[i*2:]))
	}
	scores, err := e.engine.PushPCM(samples)
	if err != nil {
		return BoundaryEstimate{}, false, err
	}
	if len(scores) == 0 {
		return BoundaryEstimate{}, false, nil
	}
	period := time.Duration(SupportedStepMS) * time.Millisecond
	if provider, ok := e.engine.(scorePeriodProvider); ok && provider.scorePeriod() > 0 {
		period = provider.scorePeriod()
	}
	lastAt := audioEndAt
	if provider, ok := e.engine.(scoreEndLagProvider); ok {
		lastAt = lastAt.Add(-provider.scoreEndLag())
	}
	windowStart := detectedAt.Add(-time.Duration(e.settings.SearchBeforeMS) * time.Millisecond)
	windowEnd := detectedAt.Add(time.Duration(e.settings.SearchAfterMS) * time.Millisecond)
	best := BoundaryEstimate{}
	for i, score := range scores {
		at := lastAt.Add(-time.Duration(len(scores)-1-i) * period)
		if at.Before(windowStart) || at.After(windowEnd) || score < e.settings.Threshold {
			continue
		}
		if best.At.IsZero() || score > best.Score {
			best = BoundaryEstimate{At: at.Add(time.Duration(e.settings.EndpointOffsetMS) * time.Millisecond), Score: score}
		}
	}
	return best, !best.At.IsZero(), nil
}

func (e *BoundaryEstimator) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	e.closed = true
	if e.engine != nil {
		return e.engine.Close()
	}
	return nil
}
