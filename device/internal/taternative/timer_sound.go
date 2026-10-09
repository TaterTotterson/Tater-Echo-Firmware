package taternative

import (
	_ "embed"
	"sync"
)

// This is the same deterministic zen timer-finished chime embedded by the
// ESP32 Tater Native firmware. Keeping it in the Echo binary makes timers work
// offline and keeps the sound consistent across the satellite families.
//
//go:embed timer_sounds/zen_timer_alarm.wav
var embeddedZenTimerSoundWAV []byte

var (
	timerSoundOnce sync.Once
	timerSoundPCM  []byte
)

func timerSound() []byte {
	timerSoundOnce.Do(func() {
		pcm, err := decodeWAV(embeddedZenTimerSoundWAV)
		if err == nil && len(pcm) > 0 {
			timerSoundPCM = pcm
			return
		}
		// The checked-in asset is validated by tests. Retain the old generated
		// tone as a defensive fallback so a damaged development build can still
		// announce a finished timer.
		timerSoundPCM = legacyTimerTone()
	})
	return timerSoundPCM
}
