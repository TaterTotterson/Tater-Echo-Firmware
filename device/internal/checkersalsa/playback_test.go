package checkersalsa

import (
	"encoding/binary"
	"testing"
)

func TestHardwareParamsLayout(t *testing.T) {
	wantSize := 604
	if longSize == 8 {
		wantSize = 608
	}
	if hwParamsSize != wantSize {
		t.Fatalf("hw_params size = %d, want %d", hwParamsSize, wantSize)
	}
	if xferiSize != 3*longSize {
		t.Fatalf("xferi size = %d, want %d", xferiSize, 3*longSize)
	}
}

func TestPlaybackHardwareParams(t *testing.T) {
	params := playbackHwParams(PeriodFrames)
	if got := binary.LittleEndian.Uint32(params[maskOff:]); got != 1<<3 {
		t.Fatalf("access mask = %#x, want RW_INTERLEAVED", got)
	}
	if got := binary.LittleEndian.Uint32(params[maskOff+maskSize:]); got != 1<<2 {
		t.Fatalf("format mask = %#x, want S16_LE", got)
	}
	for _, tc := range []struct {
		param int
		want  uint32
	}{
		{paramChannels, channels},
		{paramRate, rate},
		{paramPeriodSize, PeriodFrames},
		{paramPeriods, periods},
	} {
		if got := params.interval(tc.param); got != tc.want {
			t.Errorf("interval %d = %d, want %d", tc.param, got, tc.want)
		}
	}
}

func TestSpotPlaybackHardwareParams(t *testing.T) {
	for _, frames := range []int{1536, 2048, 2304} {
		params := playbackHwParams(frames)
		if got := params.interval(paramPeriodSize); got != uint32(frames) {
			t.Errorf("Spot period size = %d, want %d", got, frames)
		}
		if got := params.interval(paramPeriods); got != periods {
			t.Errorf("Spot period count = %d, want %d", got, periods)
		}
	}
}

func TestParsePlaybackDelay(t *testing.T) {
	delay, err := parsePlaybackDelay([]byte("state: RUNNING\ndelay       : 6432\navail       : 1760\n"))
	if err != nil || delay != 6432 {
		t.Fatalf("delay = %d, %v; want 6432", delay, err)
	}
	if _, err := parsePlaybackDelay([]byte("state: XRUN\n")); err == nil {
		t.Fatal("missing delay did not fail")
	}
}
