package aec

import (
	"encoding/binary"
	"io"
	"log"
	"math"
	"testing"
)

func quietBenchmark(b *testing.B) func() {
	b.Helper()
	w := log.Writer()
	log.SetOutput(io.Discard)
	return func() { log.SetOutput(w) }
}

func benchFrames(n int) ([]byte, []byte) {
	mic := make([]byte, n*2)
	ref := make([]byte, n*2)
	for i := 0; i < n; i++ {
		m := int16(3000 * math.Sin(float64(i)*0.05))
		r := int16(8000 * math.Sin(float64(i)*0.05+0.3))
		binary.LittleEndian.PutUint16(mic[i*2:], uint16(m))
		binary.LittleEndian.PutUint16(ref[i*2:], uint16(r))
	}
	return mic, ref
}

// One 512-sample (32ms) period through the hardware-reference canceller at
// its shipped 64ms tail. This keeps the copy-returning public API represented.
func BenchmarkCancelOnePeriodHardwareCopy(b *testing.B) {
	defer quietBenchmark(b)()
	c := New()
	c.SetParams(true, 0, 300)
	c.SetHardwareRef(true)
	mic, ref := benchFrames(FrameSize)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.ProcessWithRef(mic, ref)
	}
}

// The live path owns its beamformer output and cancels in place.
func BenchmarkCancelOnePeriodHardwareInPlace(b *testing.B) {
	defer quietBenchmark(b)()
	c := New()
	c.SetParams(true, 0, 300)
	c.SetHardwareRef(true)
	mic, ref := benchFrames(FrameSize)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.ProcessWithRefInPlace(mic, ref)
	}
}

// The actual GoTinyAlsa delivery shape: five Speex periods in one 160ms
// batch. Seven retained mic states do not multiply processing cost because
// only the selected path runs for a batch.
func BenchmarkCancelHardwareBatchInPlace(b *testing.B) {
	defer quietBenchmark(b)()
	c := New()
	c.SetParams(true, 0, 300)
	c.SetHardwareRef(true)
	mic, ref := benchFrames(5 * FrameSize)
	c.SelectPath(2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.ProcessWithRefInPlace(mic, ref)
	}
}
