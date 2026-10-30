//go:build server

package speaker

import (
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
	frames int
}

func (*recordingMusicSource) Active() bool { return true }

func (s *recordingMusicSource) Fill(out []byte, _ time.Time, _ time.Duration) bool {
	s.frames = len(out) / 4
	return true
}

func TestLinuxDotTargetsUseTheSharedDirectPlaybackPath(t *testing.T) {
	for _, target := range []string{"biscuit", "radar", " BISCUIT ", "Radar"} {
		if !isLinuxDotPlaybackTarget(target) {
			t.Errorf("%q was not recognized as a Linux Dot playback target", target)
		}
	}
	for _, target := range []string{"checkers", "crown", "athena", "rook", ""} {
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
	for _, target := range []string{"checkers", "crown", "athena", "rook", ""} {
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
