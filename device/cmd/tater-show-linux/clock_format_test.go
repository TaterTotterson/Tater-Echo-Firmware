//go:build linux

package main

import (
	"testing"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

func TestClockPartsFollowScreenClockFormat(t *testing.T) {
	evening := time.Date(2026, time.October, 26, 18, 48, 0, 0, time.UTC)
	morning := time.Date(2026, time.October, 27, 9, 5, 0, 0, time.UTC)
	for _, test := range []struct {
		format, digits, suffix string
		local                  time.Time
	}{
		{"12h", "6:48", "PM", evening},
		{"12h", "9:05", "AM", morning},
		{"", "6:48", "PM", evening},
		{"24h", "18:48", "", evening},
		{"24h", "09:05", "", morning},
	} {
		r := &renderer{state: show.Snapshot{ScreenClockFormat: test.format}}
		digits, suffix := r.clockParts(test.local)
		if digits != test.digits || suffix != test.suffix {
			t.Errorf("format %q at %s = %q %q, want %q %q", test.format, test.local.Format("15:04"), digits, suffix, test.digits, test.suffix)
		}
	}
}

func TestClockSuffixUsesPreloadedFace(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{36, 48, 74, 82, 96} {
		suffixSize := clockSuffixSize(size)
		if faces.regular[suffixSize] == nil || suffixSize*8 > size*3 || suffixSize < 12 {
			t.Errorf("clock size %d uses suffix size %d", size, suffixSize)
		}
	}
}

func TestClockKeyChangesWithFormat(t *testing.T) {
	now := time.Date(2026, time.October, 27, 1, 48, 0, 0, time.UTC)
	twelve := &renderer{state: show.Snapshot{ScreenClockFormat: "12h"}}
	twentyFour := &renderer{state: show.Snapshot{ScreenClockFormat: "24h"}}
	if twelve.clockKey(now) == twentyFour.clockKey(now) {
		t.Fatal("clock format change would not redraw the screen")
	}
}

func TestTwelveHourSpotClockFitsRoundScreen(t *testing.T) {
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	r := &renderer{faces: faces, state: show.Snapshot{ScreenClockFormat: "12h"}}
	widest := time.Date(2026, time.October, 26, 12, 48, 0, 0, time.UTC)
	digits, suffix := r.clockParts(widest)
	if width := r.clockWidth(digits, suffix, 82); width > 380 {
		t.Fatalf("12-hour Spot clock is %d px wide", width)
	}
}
