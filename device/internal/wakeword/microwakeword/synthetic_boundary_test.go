package microwakeword

import (
	"os"
	"strconv"
	"testing"
	"time"
)

func syntheticWakeDetection(t *testing.T, name string, detectedAt time.Time, period time.Duration,
	window int, policy DetectionPolicy, scores []float32) WakeDetection {
	t.Helper()
	engine := &fakeShadowEngine{
		batches: [][]float32{append([]float32(nil), scores...)},
		period:  period,
	}
	crossed := make(chan WakeDetection, 1)
	scorer, err := NewShadowScorerWithPolicy(engine, window, 0, policy, ShadowHooks{
		CrossEvent: func(detection WakeDetection) { crossed <- detection },
	})
	if err != nil {
		t.Fatalf("open synthetic %s scorer: %v", name, err)
	}
	defer scorer.Close()

	// The fake engine supplies the requested chronological scores in one batch.
	// Their period, rather than the arbitrary PCM size, places them on the same
	// audio timeline used by production scorers.
	scorer.PushBytesAt(make([]byte, 1280*2), detectedAt)
	select {
	case detection := <-crossed:
		return detection
	case <-time.After(time.Second):
		t.Fatalf("synthetic %s scores did not cross", name)
		return WakeDetection{}
	}
}

func strictSyntheticPolicy(profile string, threshold float32, patience int) DetectionPolicy {
	return DetectionPolicy{
		Profile: profile, Threshold: threshold, PeakThreshold: threshold,
		MinimumActiveWindow: patience, MinimumRiseScore: -1,
		Refractory: DefaultShadowRefractory,
	}
}

// TestSyntheticFastSpeechBoundaryRetention models the calibrated score cadence
// and patience of every supported Hey Tater mode. It intentionally assumes a
// fast speaker begins the command before the detector emits its final accepted
// crossing. "Clipped" is the portion that would remain before the command
// marker and therefore be excluded from command VAD/STT.
func TestSyntheticFastSpeechBoundaryRetention(t *testing.T) {
	detectedAt := time.Unix(2_000_000_000, 0)
	mww := syntheticWakeDetection(t, "mww", detectedAt, 10*time.Millisecond, 5,
		MakeDetectionPolicy("normal", "balanced", 0.95, 5),
		[]float32{0.99, 0.99, 0.99, 0.99, 0.99})
	oww := syntheticWakeDetection(t, "oww", detectedAt, 80*time.Millisecond, 5,
		strictSyntheticPolicy("openwakeword", 0.92, 5),
		[]float32{0.96, 0.96, 0.96, 0.96, 0.96})
	nwwCNN := syntheticWakeDetection(t, "nww-cnn", detectedAt, 80*time.Millisecond, 2,
		strictSyntheticPolicy("nanowakeword-cnn", 0.99, 2), []float32{1, 1})
	nwwGRU := syntheticWakeDetection(t, "nww-gru", detectedAt, 80*time.Millisecond, 2,
		strictSyntheticPolicy("nanowakeword-gru", 0.98, 2), []float32{0.99, 0.99})
	owwConfirmation := syntheticWakeDetection(t, "oww-confirmation", detectedAt, 80*time.Millisecond, 2,
		strictSyntheticPolicy("openwakeword-confirmation", 0.99, 2), []float32{1, 1})

	dualBoundary := func(t *testing.T, companion WakeDetection) time.Time {
		t.Helper()
		matcher := NewAgreementMatcher(DualWakeAgreementWindow)
		if _, ok := matcher.ObserveDetection(AgreementMWW, 0, mww); ok {
			t.Fatal("MWW alone unexpectedly produced dual agreement")
		}
		match, ok := matcher.ObserveDetection(AgreementOWW, 0, companion)
		if !ok {
			t.Fatal("synthetic detector pair did not agree")
		}
		return match.CommandBoundaryAt
	}

	cases := []struct {
		name     string
		boundary time.Time
		wantLead time.Duration
	}{
		{name: "MWW", boundary: mww.CommandBoundaryAt, wantLead: 120 * time.Millisecond},
		{name: "OWW", boundary: oww.CommandBoundaryAt, wantLead: 320 * time.Millisecond},
		{name: "NWW-CNN", boundary: nwwCNN.CommandBoundaryAt, wantLead: 160 * time.Millisecond},
		{name: "NWW-GRU", boundary: nwwGRU.CommandBoundaryAt, wantLead: 160 * time.Millisecond},
		{name: "MWW+OWW", boundary: dualBoundary(t, owwConfirmation), wantLead: 160 * time.Millisecond},
		{name: "MWW+NWW", boundary: dualBoundary(t, nwwCNN), wantLead: 160 * time.Millisecond},
	}
	fastSpeechLeads := []time.Duration{
		40 * time.Millisecond,
		80 * time.Millisecond,
		120 * time.Millisecond,
		200 * time.Millisecond,
		320 * time.Millisecond,
	}

	t.Log("mode      boundary lead | clipped command audio: old -> new")
	for _, testCase := range cases {
		lead := detectedAt.Sub(testCase.boundary)
		if lead != testCase.wantLead {
			t.Errorf("%s boundary lead = %s, want %s", testCase.name, lead, testCase.wantLead)
		}
		row := testCase.name + " " + lead.String()
		for _, speechLead := range fastSpeechLeads {
			newClipped := speechLead - lead
			if newClipped < 0 {
				newClipped = 0
			}
			row += " | " + speechLead.String() + "->" + newClipped.String()
		}
		t.Log(row)
	}
}

func TestSyntheticDedicatedBoundaryRemovesWakeForEveryMode(t *testing.T) {
	wakeEnd := time.Unix(200, 0)
	modes := map[string]time.Duration{
		"MWW": 120 * time.Millisecond, "OWW": 320 * time.Millisecond,
		"NWW-CNN": 160 * time.Millisecond, "NWW-GRU": 160 * time.Millisecond,
		"MWW+OWW": 160 * time.Millisecond, "MWW+NWW": 160 * time.Millisecond,
	}
	for name, detectorDelay := range modes {
		detectedAt := wakeEnd.Add(detectorDelay)
		const period = 20 * time.Millisecond
		scores := make([]float32, 50)
		peak := len(scores) - 1 - int(detectorDelay/period)
		scores[peak] = 0.99
		estimator := &BoundaryEstimator{
			engine: &fakeBoundaryEngine{scores: scores, period: period},
			settings: BundleWakeBoundary{
				Threshold: 0.8, SearchBeforeMS: 700, SearchAfterMS: 120,
			},
		}
		estimate, ok, err := estimator.Estimate(make([]byte, 32000), detectedAt, detectedAt)
		if err != nil || !ok {
			t.Fatalf("%s estimate ok=%v err=%v", name, ok, err)
		}
		if !estimate.At.Equal(wakeEnd) {
			t.Fatalf("%s boundary=%v want wake end %v", name, estimate.At, wakeEnd)
		}
		retainedWake := wakeEnd.Sub(estimate.At)
		clippedCommand := estimate.At.Sub(wakeEnd)
		if retainedWake != 0 || clippedCommand != 0 {
			t.Fatalf("%s retained wake=%v clipped command=%v", name, retainedWake, clippedCommand)
		}
		t.Logf("%s: retained wake=0ms clipped command=0ms", name)
	}
}

// TestNativeSyntheticSpeechBoundary feeds raw 16 kHz mono S16LE audio through
// the real local MWW runtime when explicitly requested. The command offset is
// measured from the beginning of the PCM file, allowing a generated wake and
// command clip to report old-versus-new clipping without an Echo satellite.
func TestNativeSyntheticSpeechBoundary(t *testing.T) {
	library := os.Getenv("TATER_MWW_LIBRARY")
	modelPath := os.Getenv("TATER_MWW_MODEL")
	boundaryModelPath := os.Getenv("TATER_MWW_BOUNDARY_MODEL")
	pcmPath := os.Getenv("TATER_MWW_SYNTHETIC_PCM")
	commandMSRaw := os.Getenv("TATER_MWW_SYNTHETIC_COMMAND_MS")
	if library == "" || modelPath == "" || pcmPath == "" || commandMSRaw == "" {
		t.Skip("set TATER_MWW_LIBRARY, TATER_MWW_MODEL, TATER_MWW_SYNTHETIC_PCM, and TATER_MWW_SYNTHETIC_COMMAND_MS")
	}
	commandMS, err := strconv.ParseInt(commandMSRaw, 10, 64)
	if err != nil || commandMS < 0 {
		t.Fatalf("invalid TATER_MWW_SYNTHETIC_COMMAND_MS %q", commandMSRaw)
	}
	raw, err := os.ReadFile(pcmPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 || len(raw)%2 != 0 {
		t.Fatalf("synthetic PCM must contain complete S16LE samples, got %d bytes", len(raw))
	}
	model, err := os.ReadFile(modelPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest := mustManifest(t)
	runtime, err := OpenNativeRuntime(library)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := runtime.NewEngine(model, manifest.RuntimeConfig())
	if err != nil {
		t.Fatal(err)
	}
	crossed := make(chan WakeDetection, 1)
	settings := manifest.RuntimeConfig()
	scorer, err := NewShadowScorerWithPolicy(engine, settings.SlidingWindow,
		settings.CloseMissThreshold,
		MakeDetectionPolicy("normal", "balanced", settings.Threshold, settings.SlidingWindow),
		ShadowHooks{CrossEvent: func(detection WakeDetection) { crossed <- detection }})
	if err != nil {
		_ = engine.Close()
		t.Fatal(err)
	}
	defer scorer.Close()

	audioStart := time.Unix(2_000_000_000, 0)
	duration := time.Duration(len(raw)/2) * time.Second / SupportedSampleRate
	scorer.PushBytesAt(raw, audioStart.Add(duration))
	select {
	case detection := <-crossed:
		commandAt := audioStart.Add(time.Duration(commandMS) * time.Millisecond)
		oldClipped := detection.DetectedAt.Sub(commandAt)
		if oldClipped < 0 {
			oldClipped = 0
		}
		newClipped := detection.CommandBoundaryAt.Sub(commandAt)
		if newClipped < 0 {
			newClipped = 0
		}
		wakeTail := commandAt.Sub(detection.CommandBoundaryAt)
		if wakeTail < 0 {
			wakeTail = 0
		}
		t.Logf("real MWW synthetic speech: detection=%s boundary_lead=%s command_start_to_detection=%s clipped_old=%s clipped_new=%s retained_wake_tail=%s",
			detection.DetectedAt.Sub(audioStart), detection.DetectedAt.Sub(detection.CommandBoundaryAt),
			detection.DetectedAt.Sub(commandAt), oldClipped, newClipped, wakeTail)

		if boundaryModelPath == "" {
			t.Log("TATER_MWW_BOUNDARY_MODEL is unset; detector timing measured without endpoint-model replay")
			return
		}
		boundaryModel, boundaryErr := os.ReadFile(boundaryModelPath)
		if boundaryErr != nil {
			t.Fatal(boundaryErr)
		}
		boundarySettings := settings
		boundarySettings.Threshold = 0.8
		boundarySettings.SlidingWindow = 1
		boundarySettings.TensorArenaSize = MinimumNativeArenaSize
		boundaryEngine, boundaryErr := runtime.NewEngine(boundaryModel, boundarySettings)
		if boundaryErr != nil {
			t.Fatal(boundaryErr)
		}
		estimator := &BoundaryEstimator{
			engine: boundaryEngine,
			settings: BundleWakeBoundary{
				Threshold: 0.8, SearchBeforeMS: 700, SearchAfterMS: 120,
			},
		}
		defer estimator.Close()
		estimate, ok, boundaryErr := estimator.Estimate(raw, audioStart.Add(duration), detection.DetectedAt)
		if boundaryErr != nil {
			t.Fatal(boundaryErr)
		}
		if !ok {
			t.Fatal("boundary model produced no threshold-valid endpoint")
		}
		modelClipped := estimate.At.Sub(commandAt)
		if modelClipped < 0 {
			modelClipped = 0
		}
		modelWakeTail := commandAt.Sub(estimate.At)
		if modelWakeTail < 0 {
			modelWakeTail = 0
		}
		t.Logf("resident boundary model: endpoint=%s score=%.3f clipped_command=%s retained_wake_tail=%s",
			estimate.At.Sub(audioStart), estimate.Score, modelClipped, modelWakeTail)
		if newClipped > oldClipped {
			t.Fatalf("new boundary clipped more command audio: old=%s new=%s", oldClipped, newClipped)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("real MWW model did not accept the synthetic wake phrase")
	}
}
