package screenambient

import (
	"errors"
	"testing"
	"time"
)

type gatedSensor struct {
	entered chan struct{}
	values  chan ambientSample
	closed  chan struct{}
}

func (s *gatedSensor) Name() string { return "blocking-test-sensor" }
func (s *gatedSensor) Sample() (float64, bool, error) {
	s.entered <- struct{}{}
	v := <-s.values
	return v.lux, v.ok, v.err
}
func (s *gatedSensor) Close() error { close(s.closed); return nil }

func TestAsyncSensorNeverBlocksDisplayOrClose(t *testing.T) {
	s := &gatedSensor{make(chan struct{}, 1), make(chan ambientSample, 1), make(chan struct{})}
	a := NonBlocking(s, time.Hour)
	<-s.entered
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			if _, ok, err := a.Sample(); ok || err != nil {
				t.Error("pending sensor produced a sample")
			}
		}
		a.Close()
		a.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		s.values <- ambientSample{}
		t.Fatal("display polling/Close waited for a sensor read")
	}
	select {
	case <-s.closed:
		t.Fatal("underlying sensor closed concurrently with Sample")
	default:
	}
	s.values <- ambientSample{lux: 42, ok: true}
	select {
	case <-s.closed:
	case <-time.After(time.Second):
		t.Fatal("sensor did not close after its read completed")
	}
}

func TestAsyncSensorDeliversFreshLuxAndErrorsOnce(t *testing.T) {
	for _, expected := range []ambientSample{{lux: 42, ok: true}, {err: errors.New("sensor failure")}} {
		s := &gatedSensor{make(chan struct{}, 1), make(chan ambientSample, 1), make(chan struct{})}
		a := NonBlocking(s, time.Hour)
		if a.Name() != s.Name() {
			t.Fatal("sensor identity changed")
		}
		<-s.entered
		s.values <- expected
		deadline := time.After(time.Second)
		for {
			lux, ok, err := a.Sample()
			if ok || err != nil {
				if lux != expected.lux || ok != expected.ok || err != expected.err {
					t.Fatal(lux, ok, err)
				}
				break
			}
			select {
			case <-deadline:
				t.Fatal("fresh sample not delivered")
			case <-time.After(time.Millisecond):
			}
		}
		if _, ok, err := a.Sample(); ok || err != nil {
			t.Fatal("stale sample/error repeated")
		}
		a.Close()
		<-s.closed
	}
}
