package client

import (
	"encoding/binary"
	"io"
	"log"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/aec"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/beamformer"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/wakeword/microwakeword"
	"github.com/TaterTotterson/Tater-Echo-Firmware/pkg/mic"
)

type captureTestMic struct {
	ch     chan mic.Capture
	ready  chan struct{}
	closed chan struct{}
}

func (*captureTestMic) Subscribe() chan []byte                { panic("timed microphone used legacy subscription") }
func (*captureTestMic) Unsubscribe(chan []byte)               {}
func (m *captureTestMic) SubscribeCapture() chan mic.Capture  { close(m.ready); return m.ch }
func (m *captureTestMic) UnsubscribeCapture(chan mic.Capture) { close(m.closed) }

type captureTestEngine struct {
	mu     sync.Mutex
	resets int
}

func (*captureTestEngine) PushPCM([]int16) ([]float32, error) { return []float32{0.1}, nil }
func (e *captureTestEngine) Reset() error                     { e.mu.Lock(); e.resets++; e.mu.Unlock(); return nil }
func (*captureTestEngine) Close() error                       { return nil }
func (*captureTestEngine) Info() string                       { return "capture test" }

func startCaptureTest(t *testing.T) (*DataClient, *captureTestMic, *microwakeword.ShadowScorer, chan struct{}) {
	t.Helper()
	m := &captureTestMic{ch: make(chan mic.Capture, 4), ready: make(chan struct{}), closed: make(chan struct{})}
	d := NewDataClient("capture-test", m, nil, aec.New())
	scorer, err := microwakeword.NewShadowScorer(&captureTestEngine{}, .95, 5, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	d.SetMWWShadowScorer(scorer)
	emitted := make(chan struct{}, 8)
	d.OnPCM(func([]byte) { emitted <- struct{}{} })
	d.StartLocalMic()
	select {
	case <-m.ready:
	case <-time.After(time.Second):
		t.Fatal("no subscription")
	}
	t.Cleanup(func() {
		d.StopMic()
		select {
		case <-m.closed:
		case <-time.After(time.Second):
			t.Error("pipeline did not exit")
		}
		scorer.Close()
	})
	return d, m, scorer, emitted
}

func awaitCaptureChunks(t *testing.T, emitted <-chan struct{}, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-emitted:
		case <-time.After(time.Second):
			t.Fatal("audio chunk missing")
		}
	}
}

func TestCaptureBatchUsesSampleTimestampsBeforeDSP(t *testing.T) {
	d, m, _, emitted := startCaptureTest(t)
	end := time.Now().Add(-300 * time.Millisecond) // queued capture retains its original time
	m.ch <- mic.Capture{PCM: make([]byte, 2560*9*3), CapturedAt: end, Sequence: 1, Generation: 1}
	awaitCaptureChunks(t, emitted, 2)
	d.wakeAudioMu.Lock()
	defer d.wakeAudioMu.Unlock()
	frames := d.wakeLaneAudio[0]
	if len(frames) != 2 {
		t.Fatalf("frames=%d", len(frames))
	}
	if !frames[0].at.Equal(end.Add(-80*time.Millisecond)) || !frames[1].at.Equal(end) {
		t.Fatalf("batch timestamps: %v, %v; want %v, %v", frames[0].at, frames[1].at, end.Add(-80*time.Millisecond), end)
	}
}

func TestWakeChunksKeepSampleTimeAcrossShortCapturePeriods(t *testing.T) {
	d, m, _, emitted := startCaptureTest(t)
	base := time.Now()
	for i := 1; i <= 3; i++ {
		m.ch <- mic.Capture{PCM: make([]byte, 512*9*3), CapturedAt: base.Add(time.Duration(i) * 32 * time.Millisecond), Sequence: uint64(i), Generation: 1}
	}
	awaitCaptureChunks(t, emitted, 1)
	for i := 4; i <= 5; i++ {
		m.ch <- mic.Capture{PCM: make([]byte, 512*9*3), CapturedAt: base.Add(time.Duration(i) * 32 * time.Millisecond), Sequence: uint64(i), Generation: 1}
	}
	awaitCaptureChunks(t, emitted, 1)
	d.wakeAudioMu.Lock()
	defer d.wakeAudioMu.Unlock()
	frames := d.wakeLaneAudio[0]
	if len(frames) != 2 || !frames[0].at.Equal(base.Add(80*time.Millisecond)) || !frames[1].at.Equal(base.Add(160*time.Millisecond)) {
		t.Fatalf("partial capture buffers changed sample time: %+v", frames)
	}
}

func TestCaptureLossClearsPartialAudioAndInvalidatesOldDetections(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "subscriber_drop", true: "producer_restart"}[restart], func(t *testing.T) {
			d, m, scorer, emitted := startCaptureTest(t)
			end := time.Now()
			m.ch <- mic.Capture{PCM: make([]byte, 2560*9*3), CapturedAt: end, Sequence: 1, Generation: 1}
			awaitCaptureChunks(t, emitted, 2)
			deadline := time.Now().Add(time.Second)
			for scorer.Health().TotalChunks < 2 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if scorer.Health().TotalChunks < 2 {
				t.Fatal("scorer did not consume initial audio")
			}
			oldGeneration := d.wakeStreamGeneration.Load()
			m.ch <- mic.Capture{PCM: make([]byte, 512*9*3), CapturedAt: end.Add(32 * time.Millisecond), Sequence: 2, Generation: 1}
			generation, sequence := uint64(1), uint64(4)
			if restart {
				generation, sequence = 2, 3 // contiguous sequence; only generation changes
			}
			m.ch <- mic.Capture{PCM: make([]byte, 1280*9*3), CapturedAt: end.Add(time.Second), Sequence: sequence, Generation: generation}
			awaitCaptureChunks(t, emitted, 1)
			if d.WakeStreamCurrent(oldGeneration) {
				t.Fatal("pre-gap detection is still valid")
			}
			d.wakeAudioMu.Lock()
			frames := d.wakeLaneAudio[0]
			if len(frames) != 1 || !frames[0].at.Equal(end.Add(time.Second)) {
				t.Errorf("gap spliced old PCM into capture: %+v", frames)
			}
			d.wakeAudioMu.Unlock()
			deadline = time.Now().Add(time.Second)
			for scorer.Health().TotalResets == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if scorer.Health().TotalResets == 0 {
				t.Fatal("capture loss did not reset streaming detector")
			}
		})
	}
}

func TestMicrophoneHealthRequiresRecentSampleProgress(t *testing.T) {
	d := &DataClient{micActive: true}
	if d.MicrophonePipelineActive() {
		t.Fatal("ownership without audio reported healthy")
	}
	d.lastCaptureNS.Store(1 + time.Since(captureProgressStart).Nanoseconds() - int64(4*time.Second))
	if d.MicrophonePipelineActive() {
		t.Fatal("stalled capture reported healthy")
	}
	d.lastCaptureNS.Store(1 + time.Since(captureProgressStart).Nanoseconds())
	if !d.MicrophonePipelineActive() {
		t.Fatal("flowing capture reported unhealthy")
	}
}

// Compare the old single-centre AEC with the shared directional path using
// actual 160ms hardware-shaped buffers. This includes beam formation and the
// aggressive playback residual search, but excludes neural wake inference.
func BenchmarkPlaybackWakeBatch(b *testing.B) {
	oldLog := log.Writer()
	log.SetOutput(io.Discard)
	defer log.SetOutput(oldLog)
	raw := make([]byte, 2560*9*3)
	for frame := 0; frame < 2560; frame++ {
		for channel := 0; channel < 9; channel++ {
			value := int32(120000 * math.Sin(float64(frame)*.09+float64(channel)*.1))
			offset := (frame*9 + channel) * 3
			raw[offset], raw[offset+1], raw[offset+2] = byte(value), byte(value>>8), byte(value>>16)
		}
	}
	for _, shared := range []bool{false, true} {
		b.Run(map[bool]string{false: "centre", true: "shared_directional"}[shared], func(b *testing.B) {
			front := beamformer.New()
			c := aec.New()
			c.SetParams(true, 0, 300)
			defer c.SetParams(false, 0, 300)
			c.SetHardwareRef(true)
			c.SetMusicMode(true)
			var ref []byte
			// Warm the measured channel-health and direction estimator.
			for i := 0; i < 20; i++ {
				front.Process(raw, -1, 1)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				mono, _ := front.Process(raw, -1, 1)
				candidate := front.WakeBeam(raw, 2, 1)
				if shared {
					if selected, _ := front.UseWakeBeam(candidate); selected != nil {
						mono = selected
					}
				}
				c.SelectPath(front.OutputChannel())
				ref = front.EchoRefInto(raw, ref)
				output := c.ProcessWithRefInPlace(mono, ref)
				// Read a sample to keep the output observable without retaining buffers.
				if len(output) > 1 {
					_ = binary.LittleEndian.Uint16(output)
				}
			}
		})
	}
}
