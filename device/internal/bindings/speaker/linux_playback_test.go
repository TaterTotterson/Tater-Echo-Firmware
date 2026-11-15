//go:build server

package speaker

import (
	"errors"
	"testing"
	"time"
)

type recordingPlaybackSession struct {
	writes []int
}

func (s *recordingPlaybackSession) Pump(data []byte) error {
	s.writes = append(s.writes, len(data))
	return nil
}

func (*recordingPlaybackSession) Close() {}

type recordingMusicSource struct {
	frames    int
	playAt    time.Time
	uncertain time.Duration
}

func (*recordingMusicSource) Active() bool { return true }

func (s *recordingMusicSource) Fill(out []byte, playAt time.Time, uncertain time.Duration) bool {
	s.frames = len(out) / 4
	s.playAt = playAt
	s.uncertain = uncertain
	return true
}

func TestSendspinPullUsesTargetHardwareClock(t *testing.T) {
	for _, test := range []struct {
		target    string
		path      string
		wantDelay time.Duration
	}{
		{"athena", "/proc/asound/card0/pcm0p/sub0/status", 50 * time.Millisecond},
		{"checkers", "/proc/asound/card0/pcm23p/sub0/status", 100 * time.Millisecond},
		{"cronos", "/proc/asound/card0/pcm23p/sub0/status", 100 * time.Millisecond},
		{"crown", "/proc/asound/card0/pcm23p/sub0/status", 100 * time.Millisecond},
		{"rook", "/proc/asound/card0/pcm23p/sub0/status", 100 * time.Millisecond},
		{"biscuit", "/proc/asound/card0/pcm23p/sub0/status", 100 * time.Millisecond},
		{"radar", "/proc/asound/card0/pcm23p/sub0/status", 100 * time.Millisecond},
	} {
		t.Run(test.target, func(t *testing.T) {
			source := &recordingMusicSource{}
			p := &PcmSpeaker{target: test.target, srcBuf: make([]byte, periodBytes)}
			p.SetMusicSource(source)
			reads := 0
			before := time.Now()
			out := p.pullSourceWithStatus(func(path string) ([]byte, error) {
				reads++
				if path != test.path {
					t.Fatalf("status path = %q, want %q", path, test.path)
				}
				return []byte("state: RUNNING\ndelay: 4800\n"), nil
			})
			after := time.Now()
			if reads != 1 || len(out) != periodBytes || source.frames != periodSize {
				t.Fatalf("reads=%d bytes=%d source frames=%d, want 1, %d, %d", reads, len(out), source.frames, periodBytes, periodSize)
			}
			if source.playAt.Before(before.Add(test.wantDelay)) || source.playAt.After(after.Add(test.wantDelay)) {
				t.Fatalf("playAt offset = %v, want %v plus measurement duration", source.playAt.Sub(before), test.wantDelay)
			}
			if source.uncertain < 0 || source.uncertain > after.Sub(before)/2 {
				t.Fatalf("invalid measurement uncertainty: %v", source.uncertain)
			}
			if p.sourceLastFill.Load() == 0 {
				t.Fatal("successful pull did not record audible music")
			}
		})
	}
}

func TestSendspinPullRequiresRunningHardwareClock(t *testing.T) {
	for _, test := range []struct {
		name   string
		status string
		err    error
	}{
		{name: "missing device", err: errors.New("missing status file")},
		{name: "closed", status: "closed\n"},
		{name: "prepared", status: "state: PREPARED\ndelay: 0\n"},
		{name: "xrun", status: "state: XRUN\ndelay: 4800\n"},
		{name: "missing delay", status: "state: RUNNING\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := &recordingMusicSource{}
			p := &PcmSpeaker{target: "athena", srcBuf: make([]byte, periodBytes)}
			p.SetMusicSource(source)
			out := p.pullSourceWithStatus(func(string) ([]byte, error) {
				return []byte(test.status), test.err
			})
			if out != nil || source.frames != 0 || p.sourceLastFill.Load() != 0 {
				t.Fatal("unavailable renderer clock must not consume timestamped music")
			}
		})
	}
}

func TestAthenaSendspinMixerPeriodUpsamplesForHardware(t *testing.T) {
	session := &recordingPlaybackSession{}
	source := &recordingMusicSource{}
	p := &PcmSpeaker{
		target:          "athena",
		session:         session,
		alsaWriteFrames: 1536,
		srcBuf:          make([]byte, periodBytes),
	}
	p.SetMusicSource(source)
	out := p.pullSourceWithStatus(func(string) ([]byte, error) {
		return []byte("state: RUNNING\ndelay: 4800\n"), nil
	})
	if source.frames != 2048 || len(out) != 8192 {
		t.Fatalf("48 kHz mixer period = %d frames, %d bytes", source.frames, len(out))
	}
	if err := p.pump(out); err != nil {
		t.Fatalf("pump: %v", err)
	}
	wantWrites := []int{1536 * 4, 1536 * 4, 1024 * 4}
	if len(session.writes) != len(wantWrites) {
		t.Fatalf("96 kHz writes = %v, want %v", session.writes, wantWrites)
	}
	for i, want := range wantWrites {
		if session.writes[i] != want {
			t.Fatalf("96 kHz writes = %v, want %v", session.writes, wantWrites)
		}
	}
}

func TestLinuxDotTargetsUseTheSharedDirectPlaybackPath(t *testing.T) {
	for _, target := range []string{"biscuit", "radar", " BISCUIT ", "Radar"} {
		if !isLinuxDotPlaybackTarget(target) {
			t.Errorf("%q was not recognized as a Linux Dot playback target", target)
		}
	}
	for _, target := range []string{"checkers", "cronos", "crown", "athena", "rook", ""} {
		if isLinuxDotPlaybackTarget(target) {
			t.Errorf("%q incorrectly entered the Linux Dot playback path", target)
		}
	}
}

func TestLinuxDotTargetsUseWholeMixerHardwarePeriods(t *testing.T) {
	for _, target := range []string{"biscuit", "radar", " BISCUIT ", "Radar"} {
		if got := linuxDotPlaybackPeriodFrames(target); got != periodSize {
			t.Errorf("%q playback period = %d, want %d", target, got, periodSize)
		}
	}
	for _, target := range []string{"checkers", "cronos", "crown", "athena", "rook", ""} {
		if got := linuxDotPlaybackPeriodFrames(target); got != alsaPeriodSize {
			t.Errorf("%q fallback playback period = %d, want %d", target, got, alsaPeriodSize)
		}
	}
}

func TestPumpUsesSelectedHardwarePeriod(t *testing.T) {
	for _, test := range []struct {
		name          string
		writeFrames   int
		dotCorrection bool
		wantWrites    []int
	}{
		{"corrected linux dot", periodSize, true, []int{driftOutputFrames * 4}},
		{"uncorrected linux dot fallback", periodSize, false, []int{periodBytes}},
		{"legacy fallback", alsaPeriodSize, false, []int{periodBytes / 2, periodBytes / 2}},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := &recordingPlaybackSession{}
			p := &PcmSpeaker{
				session:            session,
				target:             "biscuit",
				alsaWriteFrames:    test.writeFrames,
				dotDriftCorrection: test.dotCorrection,
			}
			if err := p.pump(make([]byte, periodBytes)); err != nil {
				t.Fatalf("pump: %v", err)
			}
			if len(session.writes) != len(test.wantWrites) {
				t.Fatalf("writes = %v, want %v", session.writes, test.wantWrites)
			}
			for i := range test.wantWrites {
				if session.writes[i] != test.wantWrites[i] {
					t.Fatalf("writes = %v, want %v", session.writes, test.wantWrites)
				}
			}
		})
	}
}

func TestCorrectedPlaybackConsumesAFullSendspinTimeline(t *testing.T) {
	for _, test := range []struct {
		name       string
		target     string
		corrected  bool
		wantFrames int
	}{
		{"corrected Biscuit", "biscuit", true, driftOutputFrames},
		{"corrected Radar", "radar", true, driftOutputFrames},
		{"corrected Rook", "rook", false, driftOutputFrames},
		{"uncorrected fallback", "biscuit", false, periodSize},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := &recordingMusicSource{}
			p := &PcmSpeaker{
				target:             test.target,
				alsaWriteFrames:    periodSize,
				dotDriftCorrection: test.corrected,
				srcBuf:             make([]byte, periodBytes),
				srcTimelineBuf:     make([]byte, driftOutputFrames*4),
			}
			out := p.fillMusicSource(source, time.Now(), 0)
			if source.frames != test.wantFrames {
				t.Fatalf("source filled %d frames, want %d", source.frames, test.wantFrames)
			}
			if len(out) != periodBytes {
				t.Fatalf("mixer bytes = %d, want %d", len(out), periodBytes)
			}
		})
	}
}
