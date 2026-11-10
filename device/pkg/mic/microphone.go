package mic

import (
	"context"
	"time"
)

type AudioCallback func(audioData []byte)

type Microphone interface {
	Init() error
	Listen(callback AudioCallback, context context.Context) error
}

// Subscribable is implemented by mic backends that support multiple concurrent
// readers via a fan-out model (i.e. PcmMicrophone). The vadStreamHandler uses
// this to tap the permanent ALSA stream without opening a second PCM session.
type Subscribable interface {
	Subscribe() chan []byte
	Unsubscribe(ch chan []byte)
}

// Capture describes one immutable raw ALSA batch. CapturedAt is its sample end,
// before subscriber queuing or DSP. Sequence counts batches even when a subscriber
// drops one; Generation changes when the producer restarts or loses continuity.
type Capture struct {
	PCM        []byte
	CapturedAt time.Time
	Sequence   uint64
	Generation uint64
}

// CaptureSubscribable adds timing and loss information without changing the
// byte-only API used by diagnostic tools and older microphone backends.
type CaptureSubscribable interface {
	SubscribeCapture() chan Capture
	UnsubscribeCapture(chan Capture)
}
