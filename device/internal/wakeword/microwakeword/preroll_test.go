package microwakeword

import (
	"slices"
	"testing"
	"time"
)

func TestPrerollPreservesChronologicalAudioAcrossWrap(t *testing.T) {
	p, err := NewPreroll(10, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	p.Write([]int16{0, 1, 2, 3, 4, 5})
	p.Write([]int16{6, 7, 8, 9, 10, 11, 12})
	want := []int16{3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	if got := p.Snapshot(); !slices.Equal(got, want) {
		t.Fatalf("Snapshot() = %v, want %v", got, want)
	}
	if p.Len() != 10 || p.Capacity() != 10 {
		t.Fatalf("Len/Capacity = %d/%d, want 10/10", p.Len(), p.Capacity())
	}
}

func TestPrerollOversizedWriteKeepsNewestSamples(t *testing.T) {
	p, err := NewPreroll(5, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	p.Write([]int16{1, 2})
	p.Write([]int16{3, 4, 5, 6, 7, 8, 9})
	want := []int16{5, 6, 7, 8, 9}
	if got := p.Snapshot(); !slices.Equal(got, want) {
		t.Fatalf("Snapshot() = %v, want %v", got, want)
	}
	// Snapshot must not alias the ring handed back to the live writer.
	got := p.Snapshot()
	got[0] = 99
	if next := p.Snapshot(); next[0] != 5 {
		t.Fatalf("Snapshot aliases ring: %v", next)
	}
}

func TestPrerollResetReusesCapacity(t *testing.T) {
	p, err := NewPreroll(16000, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if p.Capacity() != 32000 {
		t.Fatalf("Capacity = %d, want 32000", p.Capacity())
	}
	p.Write([]int16{1, 2, 3})
	p.Reset()
	if p.Len() != 0 || len(p.Snapshot()) != 0 || p.Capacity() != 32000 {
		t.Fatalf("unexpected reset state: len=%d cap=%d snapshot=%v", p.Len(), p.Capacity(), p.Snapshot())
	}
	p.Write([]int16{4, 5})
	if got := p.Snapshot(); !slices.Equal(got, []int16{4, 5}) {
		t.Fatalf("Snapshot after reset = %v", got)
	}
}

func TestNewPrerollRejectsUnboundedInputs(t *testing.T) {
	for _, tc := range []struct {
		rate int
		dur  time.Duration
	}{
		{0, time.Second},
		{MaxPrerollRate + 1, time.Second},
		{16000, 0},
		{16000, MaxPrerollDuration + time.Nanosecond},
		{1, time.Nanosecond},
	} {
		if _, err := NewPreroll(tc.rate, tc.dur); err == nil {
			t.Fatalf("NewPreroll(%d, %s) unexpectedly succeeded", tc.rate, tc.dur)
		}
	}
}
