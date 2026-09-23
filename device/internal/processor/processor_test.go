package processor

import (
	"encoding/binary"
	"math"
	"testing"
)

func constantPCM(samples int, value int16) []byte {
	out := make([]byte, samples*2)
	for i := 0; i < samples; i++ {
		binary.LittleEndian.PutUint16(out[i*2:], uint16(value))
	}
	return out
}

func pcmRMS(pcm []byte) float64 {
	var sum float64
	n := len(pcm) / 2
	for i := 0; i < n; i++ {
		v := float64(int16(binary.LittleEndian.Uint16(pcm[i*2:])))
		sum += v * v
	}
	return math.Sqrt(sum / float64(n))
}

func TestAGCTimingIsIndependentOfBatchShape(t *testing.T) {
	periods := New()
	for i := 0; i < 5; i++ {
		periods.Process(constantPCM(512, 16000), true, true)
	}
	batch := New()
	batch.Process(constantPCM(5*512, 16000), true, true)

	if math.Abs(periods.agcGain-batch.agcGain) > 1e-12 {
		t.Fatalf("five periods gain %.12f != one hardware batch %.12f",
			periods.agcGain, batch.agcGain)
	}
}

func TestHighPassRejectsDCAndRunsInPlace(t *testing.T) {
	p := New()
	in := constantPCM(5*512, 4000)
	first := &in[0]
	out := p.HighPass(in)
	if &out[0] != first {
		t.Fatal("high-pass replaced the caller's buffer")
	}
	// Ignore the filter's startup transient. A steady DC input should have
	// decayed to effectively zero by the final 32ms period.
	tail := out[len(out)-512*2:]
	if got := pcmRMS(tail); got > 2 {
		t.Fatalf("DC remained after high-pass: tail RMS %.2f", got)
	}
}

func TestHighPassPreservesVoiceBand(t *testing.T) {
	p := New()
	in := make([]byte, 5*512*2)
	for i := 0; i < len(in)/2; i++ {
		v := int16(12000 * math.Sin(2*math.Pi*1000*float64(i)/sampleRate))
		binary.LittleEndian.PutUint16(in[i*2:], uint16(v))
	}
	want := pcmRMS(in)
	out := p.HighPass(in)
	// Discard the first period so filter startup is not part of the passband
	// measurement. At 1kHz the 80Hz pole should be effectively transparent.
	got := pcmRMS(out[512*2:])
	if got < want*0.99 || got > want*1.01 {
		t.Fatalf("1kHz voice-band RMS changed from %.1f to %.1f", want, got)
	}
}

func TestAGCMutatesOwnedBufferWithoutAllocatingReplacement(t *testing.T) {
	p := New()
	in := constantPCM(512, 20000)
	first := &in[0]
	out := p.Process(in, true, true)
	if &out[0] != first {
		t.Fatal("AGC replaced the caller's owned buffer")
	}
}
