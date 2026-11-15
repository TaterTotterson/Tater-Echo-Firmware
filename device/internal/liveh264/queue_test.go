package liveh264

import "testing"

func TestQueueOverflowWaitsForNewIndependentKeyframe(t *testing.T) {
	q := NewQueue()
	for n := uint32(0); n < QueuedFrames; n++ {
		q.Push(fixture(n, n == 0))
	}
	q.Push(fixture(QueuedFrames, false))
	q.Push(fixture(QueuedFrames+1, false))
	if len(q.frames) != 0 {
		t.Fatal("retained stale or dependent pictures after overflow")
	}
	q.Push(fixture(QueuedFrames+2, true))
	q.Push(fixture(QueuedFrames+3, false))
	q.Close()
	for _, expected := range []uint32{QueuedFrames + 2, QueuedFrames + 3} {
		frame, ok := <-q.Frames()
		if !ok || frame.Sequence != expected {
			t.Fatal("did not resume at new keyframe")
		}
	}
	if _, ok := <-q.Frames(); ok {
		t.Fatal("queue not closed")
	}
}

func TestQueueOverflowKeepsIncomingKeyframe(t *testing.T) {
	q := NewQueue()
	for n := uint32(0); n < QueuedFrames; n++ {
		q.Push(fixture(n, n == 0))
	}
	q.Push(fixture(100, true))
	if len(q.frames) != 1 || (<-q.Frames()).Sequence != 100 {
		t.Fatal("lost replacement keyframe")
	}
}

func TestQueueRetainsOneGOPWhileDecoderStarts(t *testing.T) {
	q := NewQueue()
	q.Push(fixture(0, true))
	<-q.Frames() // Consumer starts the decoder before it can feed more input.
	for n := uint32(1); n < 30; n++ {
		q.Push(fixture(n, false))
	}
	q.Close()
	for expected := uint32(1); expected < 30; expected++ {
		frame, ok := <-q.Frames()
		if !ok || frame.Sequence != expected {
			t.Fatal("decoder startup discarded its first GOP before it could produce a picture")
		}
	}
}
