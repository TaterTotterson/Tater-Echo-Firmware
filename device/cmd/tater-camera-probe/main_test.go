//go:build linux

package main

import (
	"testing"
	"time"
)

func TestSoakRequiresExplicitOptInAndIsStillBounded(t *testing.T) {
	for _, test := range []struct {
		duration time.Duration
		soak     bool
		valid    bool
	}{
		{0, false, false}, {time.Second, false, true}, {30 * time.Second, false, true},
		{31 * time.Second, false, false}, {5 * time.Minute, true, true},
		{10 * time.Minute, true, true}, {10*time.Minute + time.Nanosecond, true, false},
		{0, true, false},
	} {
		if validProbeDuration(test.duration, test.soak) != test.valid {
			t.Fatalf("duration=%s soak=%t", test.duration, test.soak)
		}
	}
}
