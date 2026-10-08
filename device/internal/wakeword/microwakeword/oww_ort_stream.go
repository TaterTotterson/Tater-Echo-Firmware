package microwakeword

import (
	"fmt"
	"math"
	"path/filepath"
)

// These dimensions are the openWakeWord 0.6 streaming feature contract.
// Keeping the pipeline stateful is the important performance difference from
// the bounded confirmation path: each 80 ms frame advances the
// feature rings once instead of rebuilding almost two seconds of features on
// a Cortex-A7 after every MWW candidate.
const (
	owwSampleRate     = 16000
	owwChunkSamples   = 1280 // 80 ms
	owwContextSamples = 480
	owwMelBins        = 32
	owwMelWindow      = 76
	owwMelRingFrames  = 970
	owwFeatureDim     = 96
	owwFeatureWindow  = 16
	owwFeatureRing    = 120
	// openWakeWord's predict_clip path surrounds bounded clips with one
	// second of leading silence and 400 ms of trailing silence. Candidate
	// snapshots can legitimately be shorter than the classifier's 1.28 s
	// real-feature warm-up (especially just after a wake beam retarget), so
	// omitting this padding yields no scores even when MWW heard the phrase.
	owwConfirmationPrefixChunks = 10
	owwConfirmationSuffixChunks = 5
)

type owwORTInferer interface {
	Melspectrogram([]float32) ([]float32, int, error)
	Embed([]float32) ([]float32, error)
	Classify([]float32) (float32, error)
	Info() string
	Close() error
}

type ortOWWEngine struct {
	inferer  owwORTInferer
	settings OWWSettings
	info     string

	pending []int16
	context []int16
	mel     []float32
	feature []float32
	scratch []float32
}

func newORTOWWEngine(dir, classifierPath string, settings OWWSettings) (OWWEngine, error) {
	if settings.Threshold <= 0 || settings.Threshold > 1 ||
		settings.Patience < 1 || settings.Patience > 20 {
		return nil, fmt.Errorf("microwakeword: invalid ONNX OWW threshold/patience")
	}
	inferer, err := openOWWORTInferer(
		filepath.Join(dir, OWWORTRuntimeFilename),
		filepath.Join(dir, OWWMelspectrogramONNXFilename),
		filepath.Join(dir, OWWEmbeddingONNXFilename),
		classifierPath,
	)
	if err != nil {
		return nil, err
	}
	engine := &ortOWWEngine{
		inferer: inferer, settings: settings,
		info: inferer.Info(), scratch: make([]float32, owwMelWindow*owwMelBins),
	}
	if err := engine.Reset(); err != nil {
		_ = inferer.Close()
		return nil, err
	}
	return engine, nil
}

func (engine *ortOWWEngine) Info() string { return engine.info }

func (engine *ortOWWEngine) Reset() error {
	if engine.inferer == nil {
		return fmt.Errorf("microwakeword: ONNX OWW engine is closed")
	}
	engine.pending = engine.pending[:0]
	engine.context = engine.context[:0]
	engine.feature = engine.feature[:0]
	engine.mel = make([]float32, owwMelWindow*owwMelBins)
	for index := range engine.mel {
		engine.mel[index] = 1
	}
	return nil
}

func (engine *ortOWWEngine) Close() error {
	if engine.inferer == nil {
		return nil
	}
	err := engine.inferer.Close()
	engine.inferer = nil
	return err
}

func (engine *ortOWWEngine) PushPCM(samples []int16) ([]float32, error) {
	if engine.inferer == nil {
		return nil, fmt.Errorf("microwakeword: ONNX OWW engine is closed")
	}
	engine.pending = append(engine.pending, samples...)
	scores := make([]float32, 0, len(engine.pending)/owwChunkSamples)
	consumed := 0
	for len(engine.pending)-consumed >= owwChunkSamples {
		score, ready, err := engine.processChunk(engine.pending[consumed : consumed+owwChunkSamples])
		if err != nil {
			return nil, err
		}
		consumed += owwChunkSamples
		if ready {
			scores = append(scores, score)
		}
	}
	if consumed > 0 {
		copy(engine.pending, engine.pending[consumed:])
		engine.pending = engine.pending[:len(engine.pending)-consumed]
	}
	return scores, nil
}

func (engine *ortOWWEngine) Confirm(samples []int16) (OWWResult, error) {
	if len(samples) == 0 {
		return OWWResult{}, fmt.Errorf("microwakeword: openWakeWord candidate audio is empty")
	}
	if err := engine.Reset(); err != nil {
		return OWWResult{}, err
	}
	chunkCount := owwConfirmationPrefixChunks + owwConfirmationSuffixChunks +
		(len(samples)+owwChunkSamples-1)/owwChunkSamples
	audio := make([]int16, 0, chunkCount*owwChunkSamples)
	audio = append(audio, make([]int16, owwConfirmationPrefixChunks*owwChunkSamples)...)
	audio = append(audio, samples...)
	audio = append(audio, make([]int16, owwConfirmationSuffixChunks*owwChunkSamples)...)
	for len(audio)%owwChunkSamples != 0 {
		audio = append(audio, 0)
	}
	scores, err := engine.PushPCM(audio)
	if err != nil {
		return OWWResult{}, err
	}
	result := OWWResult{}
	run := 0
	for _, score := range scores {
		if score > result.Score {
			result.Score = score
		}
		if score >= engine.settings.Threshold {
			run++
			if run > result.Consecutive {
				result.Consecutive = run
			}
		} else {
			run = 0
		}
	}
	result.Accepted = result.Consecutive >= engine.settings.Patience
	return result, nil
}

func (engine *ortOWWEngine) processChunk(chunk []int16) (float32, bool, error) {
	inputPCM := make([]int16, 0, len(engine.context)+len(chunk))
	inputPCM = append(inputPCM, engine.context...)
	inputPCM = append(inputPCM, chunk...)
	input := make([]float32, len(inputPCM))
	for index, sample := range inputPCM {
		input[index] = float32(sample)
	}

	mel, frames, err := engine.inferer.Melspectrogram(input)
	if err != nil {
		return 0, false, fmt.Errorf("microwakeword: OWW melspectrogram: %w", err)
	}
	if frames*owwMelBins != len(mel) {
		return 0, false, fmt.Errorf("microwakeword: OWW melspectrogram shape mismatch")
	}
	for _, value := range mel {
		// Matches openwakeword.utils.AudioFeatures.melspec_transform.
		engine.mel = append(engine.mel, value/10+2)
	}
	if maximum := owwMelRingFrames * owwMelBins; len(engine.mel) > maximum {
		copy(engine.mel, engine.mel[len(engine.mel)-maximum:])
		engine.mel = engine.mel[:maximum]
	}
	copy(engine.scratch, engine.mel[len(engine.mel)-owwMelWindow*owwMelBins:])
	embedding, err := engine.inferer.Embed(engine.scratch)
	if err != nil {
		return 0, false, fmt.Errorf("microwakeword: OWW embedding: %w", err)
	}
	if len(embedding) != owwFeatureDim {
		return 0, false, fmt.Errorf("microwakeword: OWW embedding shape mismatch")
	}
	engine.feature = append(engine.feature, embedding...)
	if maximum := owwFeatureRing * owwFeatureDim; len(engine.feature) > maximum {
		copy(engine.feature, engine.feature[len(engine.feature)-maximum:])
		engine.feature = engine.feature[:maximum]
	}

	contextStart := len(inputPCM) - owwContextSamples
	if contextStart < 0 {
		contextStart = 0
	}
	engine.context = append(engine.context[:0], inputPCM[contextStart:]...)
	if len(engine.feature) < owwFeatureWindow*owwFeatureDim {
		return 0, false, nil
	}
	score, err := engine.inferer.Classify(
		engine.feature[len(engine.feature)-owwFeatureWindow*owwFeatureDim:],
	)
	if err != nil {
		return 0, false, fmt.Errorf("microwakeword: OWW classify: %w", err)
	}
	if math.IsNaN(float64(score)) || math.IsInf(float64(score), 0) || score < 0 || score > 1 {
		return 0, false, fmt.Errorf("microwakeword: OWW classifier returned %g", score)
	}
	return score, true, nil
}
