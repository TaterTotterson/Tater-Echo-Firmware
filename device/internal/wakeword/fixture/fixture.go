// Package fixture reads the golden captures in wakeword/testdata and defines
// what it means for two inference engines to agree.
//
// It exists because two consumers need the same answer from the same bytes:
// the host test in wakeword/ort, and the on-device probe in
// device/tools/oww_probe. A format with a magic number, a strict
// fully-consumed check and a tolerance policy is exactly the thing that should
// not be written twice — a probe that parsed the file slightly differently, or
// compared with a slightly looser tolerance, would report agreement the test
// would not.
package fixture

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/wakeword"
)

// Record is one 80ms chunk's worth of what Python computed.
type Record struct {
	// MelInLen is how many samples were handed to the melspectrogram model:
	// 1280 on the first chunk (nothing precedes it) and 1760 after, once 480
	// samples of left context exist.
	MelInLen int
	// MelOut is the model's RAW output, before the /10+2 transform the
	// streaming pipeline applies.
	MelOut []float32
	Emb    []float32
	Score  float32
}

// ORT is testdata/ort_fixture.bin: input audio plus per-chunk model outputs,
// plus a probe block. See testdata/gen_ort_fixture.py for the format and for
// why the audio is carried in the file rather than regenerated.
type ORT struct {
	Audio   []int16
	Records []Record

	// The probe block covers what the audio path exercises weakly. Synthetic
	// noise scores 0.0000 at every chunk, so a classifier hard-wired to return
	// zero passes on the audio alone; these are one wide-range tensor per
	// model, whose classifier score is small but distinctly non-zero.
	EmbProbeIn, EmbProbeOut []float32
	ClsProbeIn              []float32
	ClsProbeOut             float32
}

const ortMagic = "OWWORT02"

// LoadORT reads and validates the fixture. Every size is derived from the
// file's own headers and checked against the model geometry, and the whole
// file must be consumed — a fixture that parses but leaves bytes over means
// the writer and reader disagree, which is worth an error rather than a
// silently truncated comparison.
func LoadORT(path string) (*ORT, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) < len(ortMagic)+4 || string(raw[:len(ortMagic)]) != ortMagic {
		return nil, fmt.Errorf("fixture %s: bad magic (want %s)", path, ortMagic)
	}

	p := len(ortMagic)
	// need reports whether n more bytes are available, so a truncated file
	// produces an error instead of a slice-bounds panic.
	need := func(n int) error {
		if p+n > len(raw) {
			return fmt.Errorf("fixture %s: truncated at byte %d, wanted %d more", path, p, n)
		}
		return nil
	}
	u32 := func() (int, error) {
		if err := need(4); err != nil {
			return 0, err
		}
		v := binary.LittleEndian.Uint32(raw[p:])
		p += 4
		return int(v), nil
	}
	f32n := func(n int) ([]float32, error) {
		if err := need(4 * n); err != nil {
			return nil, err
		}
		out := make([]float32, n)
		for i := range out {
			out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[p:]))
			p += 4
		}
		return out, nil
	}

	f := &ORT{}
	nSamples, err := u32()
	if err != nil {
		return nil, err
	}
	if err := need(2 * nSamples); err != nil {
		return nil, err
	}
	f.Audio = make([]int16, nSamples)
	for i := range f.Audio {
		f.Audio[i] = int16(binary.LittleEndian.Uint16(raw[p:]))
		p += 2
	}

	nRecs, err := u32()
	if err != nil {
		return nil, err
	}
	f.Records = make([]Record, nRecs)
	for i := range f.Records {
		if f.Records[i].MelInLen, err = u32(); err != nil {
			return nil, err
		}
		frames, err := u32()
		if err != nil {
			return nil, err
		}
		if f.Records[i].MelOut, err = f32n(frames * wakeword.MelBins); err != nil {
			return nil, err
		}
		if f.Records[i].Emb, err = f32n(wakeword.FeatDim); err != nil {
			return nil, err
		}
		score, err := f32n(1)
		if err != nil {
			return nil, err
		}
		f.Records[i].Score = score[0]
	}

	if f.EmbProbeIn, err = f32n(wakeword.MelWindow * wakeword.MelBins); err != nil {
		return nil, err
	}
	if f.EmbProbeOut, err = f32n(wakeword.FeatDim); err != nil {
		return nil, err
	}
	if f.ClsProbeIn, err = f32n(wakeword.FeatWindow * wakeword.FeatDim); err != nil {
		return nil, err
	}
	clsOut, err := f32n(1)
	if err != nil {
		return nil, err
	}
	f.ClsProbeOut = clsOut[0]

	if p != len(raw) {
		return nil, fmt.Errorf("fixture %s: %d of %d bytes consumed", path, p, len(raw))
	}
	if nSamples < len(f.Records)*wakeword.ChunkSamples {
		return nil, fmt.Errorf("fixture %s: %d samples cannot supply %d chunks",
			path, nSamples, len(f.Records))
	}
	return f, nil
}

// Chunk returns the i'th 80ms slice of audio.
func (f *ORT) Chunk(i int) []int16 {
	return f.Audio[i*wakeword.ChunkSamples : (i+1)*wakeword.ChunkSamples]
}

// Tolerance for comparing one inference engine against another.
//
// RelTol is relative to the SCALE OF THE TENSOR (its largest magnitude), not
// to each element individually, and that distinction is the whole point.
// Per-element relative error is meaningless for a tensor whose values straddle
// zero: the same 2e-5 absolute error reads as 2e-4 on an element at 0.1 and as
// 2000 on an element at 1e-8, so a per-element criterion fails on whichever
// element happens to sit nearest zero while saying nothing about whether the
// engines agree. Measured on an Echo Dot: ONNX Runtime 1.19.2 with XNNPACK
// against 1.23.2 with the CPU provider agreed to 1.5e-6 of tensor scale, while
// the worst single element differed by 1.7e-4 relatively. The first number is
// the informative one, and it matches the ~7 significant figures measured
// independently between ARM and x86.
//
// 1e-4 of scale is therefore generous by two orders of magnitude, and remains
// far below anything that could move a wake decision — the classifier score,
// the only value with a threshold attached, agreed to 8.9e-8 against a
// threshold of 0.5 in the same run.
const (
	RelTol = 1e-4
	AbsTol = 1e-5
)

// scaleOf returns a tensor's magnitude, the basis for its tolerance.
func scaleOf(want []float32) float64 {
	var s float64
	for _, v := range want {
		if a := math.Abs(float64(v)); a > s {
			s = a
		}
	}
	return s
}

// tolerance is the absolute difference permitted in a tensor of this scale.
// The AbsTol floor matters for tensors that are legitimately all near zero —
// the classifier's score on non-speech audio is 0.0000, and scaling a
// tolerance off that would demand bit-exactness.
func tolerance(scale float64) float64 { return AbsTol + RelTol*scale }

// CloseEnough compares two scalars, i.e. a tensor of one value.
func CloseEnough(got, want float32) bool {
	return math.Abs(float64(got-want)) <= tolerance(math.Abs(float64(want)))
}

// Diff describes how two tensors disagree. Zero value means they match.
type Diff struct {
	// N is how many values are outside tolerance.
	N int
	// Worst is the largest absolute difference seen, and WorstAt where.
	Worst   float64
	WorstAt int
	// Scale is the expected tensor's magnitude and Tol the difference that was
	// permitted. Both are recorded so a caller can report how close the
	// engines actually were, which is more useful than a pass/fail — a run
	// that passes at 1.5e-6 of scale and one that passes at 9e-5 are telling
	// you different things about the hardware.
	Scale float64
	Tol   float64
	// Examples holds up to four human-readable mismatches, so a caller can
	// report something useful without dumping thousands of lines.
	Examples []string
}

// Ok reports whether the tensors agreed.
func (d Diff) Ok() bool { return d.N == 0 }

// RelToScale is the worst difference as a fraction of the tensor's scale — the
// figure to quote when describing how well two engines agree.
func (d Diff) RelToScale() float64 {
	if d.Scale == 0 {
		return 0
	}
	return d.Worst / d.Scale
}

// Compare checks got against want elementwise, at a tolerance derived from
// want's scale. A length mismatch is reported as a single mismatch rather than
// a panic, because on a real device the interesting version of that bug is a
// model whose output shape is not what the pipeline assumed.
func Compare(got, want []float32) Diff {
	var d Diff
	if len(got) != len(want) {
		d.N = 1
		d.Examples = append(d.Examples, fmt.Sprintf("length %d, want %d", len(got), len(want)))
		return d
	}
	d.Scale = scaleOf(want)
	d.Tol = tolerance(d.Scale)
	for i := range got {
		delta := math.Abs(float64(got[i] - want[i]))
		if delta > d.Worst {
			d.Worst, d.WorstAt = delta, i
		}
		if delta > d.Tol {
			d.N++
			if len(d.Examples) < 4 {
				d.Examples = append(d.Examples, fmt.Sprintf(
					"[%d] = %v, want %v (off by %.3g, tolerance %.3g)",
					i, got[i], want[i], delta, d.Tol))
			}
		}
	}
	return d
}

// spy wraps an Inferer to capture the tensors flowing between the models while
// the real pipeline drives them. Copies are taken because implementations are
// free to reuse their output buffers, and ort.Inferer does.
type spy struct {
	inner    wakeword.Inferer
	melInLen []int
	melOuts  [][]float32
	embs     [][]float32
}

func (s *spy) Melspec(samples []float32) ([]float32, int, error) {
	out, frames, err := s.inner.Melspec(samples)
	if err == nil {
		s.melInLen = append(s.melInLen, len(samples))
		s.melOuts = append(s.melOuts, append([]float32(nil), out...))
	}
	return out, frames, err
}

func (s *spy) Embed(window []float32) ([]float32, error) {
	out, err := s.inner.Embed(window)
	if err == nil {
		s.embs = append(s.embs, append([]float32(nil), out...))
	}
	return out, err
}

func (s *spy) Classify(feats []float32) (float32, error) { return s.inner.Classify(feats) }

// Report is the outcome of Verify: how an inference engine compared with
// Python at every stage, per 80ms chunk.
type Report struct {
	Melspec   []Diff
	Embedding []Diff
	Score     []Diff

	// ScoreFrom is the first chunk whose score is comparable. Python seeds its
	// feature ring with embeddings of random warm-up audio; the Go pipeline
	// starts clean and reports not-ready instead, so scores only become
	// comparable once the ring holds nothing but real audio.
	ScoreFrom int

	// Structural holds failures that are not numerical — a wrong number of
	// melspectrogram calls, an unexpected input length, a chunk that produced
	// the wrong number of embeddings. These matter more than a tolerance miss:
	// they mean the pipeline and the models disagree about shape.
	Structural []string

	// Err is set if the run could not be completed at all.
	Err error
}

// Ok reports whether the engine reproduced Python everywhere.
func (r Report) Ok() bool {
	if r.Err != nil || len(r.Structural) > 0 {
		return false
	}
	for _, set := range [][]Diff{r.Melspec, r.Embedding, r.Score} {
		for _, d := range set {
			if !d.Ok() {
				return false
			}
		}
	}
	return true
}

// Summary renders the report as a few lines of text, for a diagnostic tool
// with no testing.T to report through.
func (r Report) Summary() string {
	if r.Err != nil {
		return "FAIL: " + r.Err.Error()
	}
	stage := func(name string, set []Diff) string {
		bad, worst, rel, scale, tol := 0, 0.0, 0.0, 0.0, 0.0
		for _, d := range set {
			if !d.Ok() {
				bad++
			}
			if d.Worst > worst {
				worst = d.Worst
			}
			if r := d.RelToScale(); r > rel {
				rel = r
			}
			if d.Scale > scale {
				scale = d.Scale
			}
			if d.Tol > tol {
				tol = d.Tol
			}
		}
		verdict := "ok"
		if bad > 0 {
			verdict = fmt.Sprintf("FAIL (%d chunks differ)", bad)
		}
		// Report the agreement, not just the verdict: how closely two engines
		// match is the interesting output even when they match well enough.
		//
		// The of-scale figure is only meaningful for a tensor with some
		// magnitude to be relative TO. The classifier score on non-speech
		// audio is ~1e-7, so quoting a ratio against it printed "0.75 of
		// tensor scale" for a run that agreed to 9e-8 and passed on the
		// absolute floor — a number that reads as alarming and means nothing.
		if scale >= 1 {
			return fmt.Sprintf("  %-10s %-24s worst %.3g abs, %.3g of tensor scale\n",
				name, verdict, worst, rel)
		}
		return fmt.Sprintf("  %-10s %-24s worst %.3g abs (tolerance %.3g)\n",
			name, verdict, worst, tol)
	}
	out := stage("melspec", r.Melspec) + stage("embedding", r.Embedding) +
		stage("score", r.Score[r.ScoreFrom:])
	for _, s := range r.Structural {
		out += "  STRUCTURAL: " + s + "\n"
	}
	// The first mismatch of each stage is worth showing: a systematic error
	// looks different from a single outlier, and the distinction decides
	// whether to suspect the models or the buffering.
	for name, set := range map[string][]Diff{"melspec": r.Melspec, "embedding": r.Embedding, "score": r.Score} {
		for i, d := range set {
			if !d.Ok() {
				out += fmt.Sprintf("  first %s mismatch, chunk %d: %v\n", name, i, d.Examples)
				break
			}
		}
	}
	return out
}

// Verify drives the real streaming pipeline over the fixture's audio using the
// given engine, and compares every stage against what Python produced.
//
// This lives here, rather than in either caller, because the host test and the
// on-device probe must ask exactly the same question. A probe that verified
// slightly less than the test would report success the test would not, and it
// is the probe whose answer gets trusted — it runs on the hardware.
func Verify(inf wakeword.Inferer, fx *ORT) Report {
	s := &spy{inner: inf}
	d := wakeword.New(s)
	r := Report{ScoreFrom: wakeword.FeatWindow}

	scores := make([]float32, len(fx.Records))
	for i := range fx.Records {
		n, err := d.Push(fx.Chunk(i))
		if err != nil {
			r.Err = fmt.Errorf("chunk %d: push: %w", i, err)
			return r
		}
		if n != 1 {
			r.Structural = append(r.Structural,
				fmt.Sprintf("chunk %d produced %d embeddings, want 1", i, n))
		}
		if !d.Ready() {
			continue
		}
		if scores[i], err = d.Score(); err != nil {
			r.Err = fmt.Errorf("chunk %d: score: %w", i, err)
			return r
		}
	}

	if len(s.melOuts) != len(fx.Records) {
		r.Structural = append(r.Structural,
			fmt.Sprintf("melspec ran %d times, want %d", len(s.melOuts), len(fx.Records)))
		return r
	}
	for i, rec := range fx.Records {
		if s.melInLen[i] != rec.MelInLen {
			r.Structural = append(r.Structural, fmt.Sprintf(
				"chunk %d fed melspec %d samples, Python fed %d",
				i, s.melInLen[i], rec.MelInLen))
		}
		r.Melspec = append(r.Melspec, Compare(s.melOuts[i], rec.MelOut))
		r.Embedding = append(r.Embedding, Compare(s.embs[i], rec.Emb))

		var d Diff
		if i >= r.ScoreFrom {
			d = Compare([]float32{scores[i]}, []float32{rec.Score})
		}
		r.Score = append(r.Score, d)
	}
	return r
}
