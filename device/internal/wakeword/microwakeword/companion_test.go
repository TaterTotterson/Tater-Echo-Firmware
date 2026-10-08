package microwakeword

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func companionDigest(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func validWakeBundle(t *testing.T) []byte {
	t.Helper()
	bundle := WakeBundle{
		SchemaVersion: 1,
		Type:          "tater_wake_word_bundle",
		WakeWord:      "hey tater",
		Key:           "hey_tater",
		MicroWakeWord: BundleMicroWakeWord{
			Manifest: "hey_tater.json", Model: "hey_tater.tflite",
			ManifestSHA256: companionDigest([]byte("manifest")),
			ModelSHA256:    companionDigest([]byte("model")),
		},
		OpenWakeWord: BundleOpenWakeWord{
			Metadata: "hey_tater.oww.json", MetadataSHA256: companionDigest([]byte("metadata")),
			RecommendedThreshold: 0.91, RecommendedPatience: 3,
			RecommendedConfirmationThreshold: 0.86, RecommendedConfirmationPatience: 2,
			Artifacts: map[string]BundleArtifact{
				"onnx": {File: "hey_tater.oww.onnx", SHA256: companionDigest([]byte("oww")), SizeBytes: 3},
			},
		},
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParseWakeBundleAcceptsTrainerContract(t *testing.T) {
	bundle, err := ParseWakeBundle(validWakeBundle(t))
	if err != nil {
		t.Fatal(err)
	}
	if bundle.OpenWakeWord.RecommendedThreshold != 0.91 ||
		bundle.OpenWakeWord.RecommendedPatience != 3 ||
		bundle.OpenWakeWord.RecommendedConfirmationThreshold != 0.86 ||
		bundle.OpenWakeWord.RecommendedConfirmationPatience != 2 {
		t.Fatalf("unexpected calibration: %+v", bundle.OpenWakeWord)
	}
}

func TestParseWakeBundleFallsBackForLegacyConfirmationPolicy(t *testing.T) {
	var bundle map[string]any
	if err := json.Unmarshal(validWakeBundle(t), &bundle); err != nil {
		t.Fatal(err)
	}
	oww := bundle["open_wake_word"].(map[string]any)
	delete(oww, "recommended_confirmation_threshold")
	delete(oww, "recommended_confirmation_patience")
	raw, _ := json.Marshal(bundle)
	parsed, err := ParseWakeBundle(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.OpenWakeWord.RecommendedConfirmationThreshold != parsed.OpenWakeWord.RecommendedThreshold ||
		parsed.OpenWakeWord.RecommendedConfirmationPatience != parsed.OpenWakeWord.RecommendedPatience {
		t.Fatalf("legacy bundle did not inherit standalone policy: %+v", parsed.OpenWakeWord)
	}
}

func TestParseWakeBundleRejectsInvalidConfirmationPolicy(t *testing.T) {
	var bundle map[string]any
	if err := json.Unmarshal(validWakeBundle(t), &bundle); err != nil {
		t.Fatal(err)
	}
	bundle["open_wake_word"].(map[string]any)["recommended_confirmation_threshold"] = 1.1
	raw, _ := json.Marshal(bundle)
	if _, err := ParseWakeBundle(raw); err == nil {
		t.Fatal("bundle with invalid confirmation threshold was accepted")
	}
}

func TestCompanionSettingsSelectModeSpecificCalibration(t *testing.T) {
	bundle, err := ParseWakeBundle(validWakeBundle(t))
	if err != nil {
		t.Fatal(err)
	}
	standalone := companionSettings(bundle, false)
	confirmation := companionSettings(bundle, true)
	if standalone.Threshold != 0.91 || standalone.Patience != 3 {
		t.Fatalf("unexpected standalone settings: %+v", standalone)
	}
	if confirmation.Threshold != 0.86 || confirmation.Patience != 2 {
		t.Fatalf("unexpected confirmation settings: %+v", confirmation)
	}
}

func TestParseWakeBundleRejectsMissingONNXCompanion(t *testing.T) {
	var bundle map[string]any
	if err := json.Unmarshal(validWakeBundle(t), &bundle); err != nil {
		t.Fatal(err)
	}
	bundle["open_wake_word"].(map[string]any)["artifacts"] = map[string]any{}
	raw, _ := json.Marshal(bundle)
	if _, err := ParseWakeBundle(raw); err == nil {
		t.Fatal("bundle without an ONNX classifier was accepted")
	}
}

type fakeOWWEngine struct {
	got    []int16
	result OWWResult
}

func (f *fakeOWWEngine) Confirm(samples []int16) (OWWResult, error) {
	f.got = append([]int16(nil), samples...)
	return f.result, nil
}
func (*fakeOWWEngine) PushPCM([]int16) ([]float32, error) { return nil, nil }
func (*fakeOWWEngine) Reset() error                       { return nil }
func (*fakeOWWEngine) Close() error                       { return nil }
func (*fakeOWWEngine) Info() string                       { return "fake" }

func TestCompanionVerifierUsesExactLittleEndianWinningBeam(t *testing.T) {
	engine := &fakeOWWEngine{result: OWWResult{Accepted: true, Score: 0.97, Consecutive: 4}}
	verifier := &CompanionVerifier{engine: engine, settings: OWWSettings{Threshold: 0.9, Patience: 3}}
	result, err := verifier.ConfirmFrames([][]byte{{0x34, 0x12, 0xfe, 0xff}, {0x02, 0x00}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Accepted || len(engine.got) != 3 || engine.got[0] != 0x1234 || engine.got[1] != -2 || engine.got[2] != 2 {
		t.Fatalf("result=%+v samples=%v", result, engine.got)
	}
	stats := verifier.Stats()
	if stats.Candidates != 1 || stats.Accepted != 1 || stats.Rejected != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestCompanionVerifierKeepsOnlyRecentWakeWindow(t *testing.T) {
	engine := &fakeOWWEngine{result: OWWResult{Accepted: true, Score: 0.97, Consecutive: 3}}
	verifier := &CompanionVerifier{engine: engine, settings: OWWSettings{Threshold: 0.9, Patience: 3}}
	frames := make([][]byte, maxConfirmationFrames+6)
	for index := range frames {
		frames[index] = []byte{byte(index), 0}
	}
	if _, err := verifier.ConfirmFrames(frames); err != nil {
		t.Fatal(err)
	}
	if len(engine.got) != maxConfirmationFrames {
		t.Fatalf("confirmation samples = %d, want %d recent frames", len(engine.got), maxConfirmationFrames)
	}
	if engine.got[0] != 6 || engine.got[len(engine.got)-1] != int16(len(frames)-1) {
		t.Fatalf("confirmation window = %v, want frames 6..%d", engine.got, len(frames)-1)
	}
}

type blockingOWWEngine struct {
	entered chan struct{}
	release chan struct{}
}

func (f *blockingOWWEngine) Confirm([]int16) (OWWResult, error) {
	close(f.entered)
	<-f.release
	return OWWResult{Accepted: true, Score: 0.97, Consecutive: 3}, nil
}
func (*blockingOWWEngine) PushPCM([]int16) ([]float32, error) { return nil, nil }
func (*blockingOWWEngine) Reset() error                       { return nil }
func (*blockingOWWEngine) Close() error                       { return nil }
func (*blockingOWWEngine) Info() string                       { return "blocking-fake" }

func TestCompanionVerifierDropsRatherThanQueuesWhileBusy(t *testing.T) {
	engine := &blockingOWWEngine{entered: make(chan struct{}), release: make(chan struct{})}
	verifier := &CompanionVerifier{engine: engine, settings: OWWSettings{Threshold: 0.9, Patience: 3}}
	done := make(chan error, 1)
	go func() {
		_, err := verifier.ConfirmFrames([][]byte{{1, 0}})
		done <- err
	}()
	<-engine.entered
	if _, err := verifier.ConfirmFrames([][]byte{{2, 0}}); !errors.Is(err, ErrCompanionBusy) {
		t.Fatalf("second candidate error = %v, want ErrCompanionBusy", err)
	}
	close(engine.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	stats := verifier.Stats()
	if stats.Candidates != 2 || stats.Accepted != 1 || stats.BusyDrops != 1 || stats.InFlight {
		t.Fatalf("unexpected non-queueing stats: %+v", stats)
	}
}

func TestCompanionVerifierRequiresOWWAgreement(t *testing.T) {
	engine := &fakeOWWEngine{result: OWWResult{Accepted: false, Score: 0.72, Consecutive: 0}}
	verifier := &CompanionVerifier{engine: engine, settings: OWWSettings{Threshold: 0.92, Patience: 3}}
	result, err := verifier.ConfirmFrames([][]byte{{1, 0}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Accepted {
		t.Fatal("MWW candidate was accepted without OWW agreement")
	}
	stats := verifier.Stats()
	if stats.Candidates != 1 || stats.Accepted != 0 || stats.Rejected != 1 {
		t.Fatalf("unexpected rejection stats: %+v", stats)
	}
}

func TestConfirmationFreshnessRejectsOnlyStaleResults(t *testing.T) {
	now := time.Now()
	if ConfirmationIsStale(now.Add(-MaximumConfirmationAge+time.Millisecond), now) {
		t.Fatal("fresh OWW confirmation was marked stale")
	}
	if !ConfirmationIsStale(now.Add(-MaximumConfirmationAge-time.Millisecond), now) {
		t.Fatal("late OWW confirmation was allowed to wake")
	}
	if !ConfirmationIsStale(time.Time{}, now) {
		t.Fatal("candidate without a capture time was allowed to wake")
	}
}
