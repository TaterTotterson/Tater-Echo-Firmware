package main

import (
	"testing"
	"time"
)

func TestH264CaptureCadencePreservesFullSourceTimingWithoutCatchup(t *testing.T) {
	start := time.Unix(1, 0)
	c := h264CaptureCadence{interval: time.Second / 15}
	count := 0
	for i := 0; i < 300; i++ {
		if c.accept(start.Add(time.Duration(i) * 33 * time.Millisecond)) {
			count++
		}
	}
	if count < 148 || count > 150 {
		t.Fatal("unexpected paced sensor frame count", count)
	}
	afterStall := start.Add(30 * time.Second)
	if !c.accept(afterStall) || c.accept(afterStall) || c.accept(afterStall.Add(time.Millisecond)) {
		t.Fatal("capture burst after stall")
	}
	if !c.accept(afterStall.Add(time.Second/15 + time.Millisecond)) {
		t.Fatal("capture did not resume")
	}
	if c.accept(start) {
		t.Fatal("accepted backwards sensor time")
	}
}

func TestH264CaptureCadenceOnlyAcceptsQualifiedRates(t *testing.T) {
	for _, s := range []string{"", "15", "30"} {
		if n, e := h264CaptureFPS(s); e != nil || n < 15 {
			t.Fatal(s, n, e)
		}
	}
	for _, s := range []string{"0", "60", "-1", "NaN", "15&format=annexb"} {
		if _, e := h264CaptureFPS(s); e == nil {
			t.Fatal("accepted rate", s)
		}
	}
	c := h264CaptureCadence{}
	if !c.accept(time.Time{}) || !c.accept(time.Time{}) {
		t.Fatal("unpaced capture was limited")
	}
}
