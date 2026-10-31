package speaker

import (
	"encoding/binary"
	"testing"
)

func TestPlaybackDriftExpansion(t *testing.T) {
	var drift playbackDriftCorrection
	input := make([]byte, driftInputFrames*4)
	for frame := 0; frame < driftInputFrames; frame++ {
		binary.LittleEndian.PutUint16(input[frame*4:], uint16(int16(1200)))
		negative := int16(-1200)
		binary.LittleEndian.PutUint16(input[frame*4+2:], uint16(negative))
	}
	output := drift.expand(input)
	if len(output) != driftOutputFrames*4 {
		t.Fatalf("output length = %d, want %d", len(output), driftOutputFrames*4)
	}
	for frame := 0; frame < driftOutputFrames; frame++ {
		left := int16(binary.LittleEndian.Uint16(output[frame*4:]))
		right := int16(binary.LittleEndian.Uint16(output[frame*4+2:]))
		if left != 1200 || right != -1200 {
			t.Fatalf("frame %d = (%d, %d), want (1200, -1200)", frame, left, right)
		}
	}
	if got := int16(binary.LittleEndian.Uint16(drift.expand(input)[:2])); got != 1200 {
		t.Fatalf("second period first sample = %d, want 1200", got)
	}
}

func TestPlaybackDriftPeriodBoundary(t *testing.T) {
	var drift playbackDriftCorrection
	first := make([]byte, driftInputFrames*4)
	second := make([]byte, driftInputFrames*4)
	for frame := 0; frame < driftInputFrames; frame++ {
		value := int16(frame * 8)
		binary.LittleEndian.PutUint16(first[frame*4:], uint16(value))
		binary.LittleEndian.PutUint16(first[frame*4+2:], uint16(value))
		value = int16((driftInputFrames + frame) * 8)
		binary.LittleEndian.PutUint16(second[frame*4:], uint16(value))
		binary.LittleEndian.PutUint16(second[frame*4+2:], uint16(value))
	}
	last := int16(binary.LittleEndian.Uint16(drift.expand(first)[(driftOutputFrames-1)*4:]))
	begin := int16(binary.LittleEndian.Uint16(drift.expand(second)[:2]))
	if last != int16((driftInputFrames-1)*8) || begin < last || begin > last+8 {
		t.Fatalf("period boundary last=%d begin=%d", last, begin)
	}
}

func TestPlaybackDriftCompressionConsumesHardwareTimeline(t *testing.T) {
	timeline := make([]byte, driftOutputFrames*4)
	compressed := make([]byte, driftInputFrames*4)
	for frame := 0; frame < driftOutputFrames; frame++ {
		value := int16(frame * 8)
		binary.LittleEndian.PutUint16(timeline[frame*4:], uint16(value))
		binary.LittleEndian.PutUint16(timeline[frame*4+2:], uint16(-value))
	}
	got := compressPlaybackDrift(compressed, timeline)
	if len(got) != driftInputFrames*4 {
		t.Fatalf("compressed bytes = %d, want %d", len(got), driftInputFrames*4)
	}
	first := int16(binary.LittleEndian.Uint16(got[:2]))
	last := int16(binary.LittleEndian.Uint16(got[(driftInputFrames-1)*4:]))
	if first < 0 || first > 8 || last != int16((driftOutputFrames-1)*8) {
		t.Fatalf("compressed endpoints = %d..%d, want 0..%d", first, last, (driftOutputFrames-1)*8)
	}
}

func TestPlaybackDriftCompressionRoundTripStaysSmooth(t *testing.T) {
	timeline := make([]byte, driftOutputFrames*4)
	compressed := make([]byte, driftInputFrames*4)
	for frame := 0; frame < driftOutputFrames; frame++ {
		value := int16(frame * 8)
		binary.LittleEndian.PutUint16(timeline[frame*4:], uint16(value))
		binary.LittleEndian.PutUint16(timeline[frame*4+2:], uint16(value))
	}
	var drift playbackDriftCorrection
	roundTrip := drift.expand(compressPlaybackDrift(compressed, timeline))
	var maxDelta int
	for frame := 1; frame < driftOutputFrames; frame++ {
		previous := int(int16(binary.LittleEndian.Uint16(roundTrip[(frame-1)*4:])))
		current := int(int16(binary.LittleEndian.Uint16(roundTrip[frame*4:])))
		delta := current - previous
		if delta < 0 {
			delta = -delta
		}
		if delta > maxDelta {
			maxDelta = delta
		}
	}
	if maxDelta > 9 {
		t.Fatalf("round-trip introduced a %d-sample step, want at most 9", maxDelta)
	}
}
