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
	var params hwParams
	params.init()
	params.setMask(paramAccess, 3)
	params.setMask(paramFormat, 2)
	params.setInterval(paramChannels, channels)
	params.setInterval(paramRate, rate)
	params.setInterval(paramPeriodSize, PeriodFrames)
	params.setInterval(paramPeriods, periods)
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
