//go:build server && bench

package speaker

import (
	"encoding/binary"
	"log"
	"time"
)

// writeLoopMeter reports how late the ALSA write loop gets, once a minute, for
// sizing the hardware buffer tier: the worst gap between writes (time the
// buffer drained unrefilled) and the loop's own work between them. Two
// time.Now per period. Bench builds only.
type writeLoopMeter struct {
	last, workDone, window time.Time
	maxGap, maxWork        time.Duration
	periods                int
	audioActive            bool
	audioPeak              int
	audioNearLimit         int
	audioAtLimit           int
	audioSamples           int
}

func (m *writeLoopMeter) beforeWrite() { m.workDone = time.Now() }

// observeAudio records only aggregate levels, never PCM. It distinguishes
// clipping before the DAC from a later kernel or analog playback artifact.
func (m *writeLoopMeter) observeAudio(pcm []byte, active bool) {
	if !active {
		if m.audioActive {
			log.Printf("[speaker] audio peak: max=%d/32768 near_limit=%d at_limit=%d samples=%d",
				m.audioPeak, m.audioNearLimit, m.audioAtLimit, m.audioSamples)
			m.audioPeak, m.audioNearLimit, m.audioAtLimit, m.audioSamples = 0, 0, 0, 0
		}
		m.audioActive = false
		return
	}
	m.audioActive = true
	for off := 0; off+1 < len(pcm); off += 2 {
		value := int(int16(binary.LittleEndian.Uint16(pcm[off:])))
		if value < 0 {
			value = -value
		}
		if value > m.audioPeak {
			m.audioPeak = value
		}
		if value >= 32000 {
			m.audioNearLimit++
		}
		if value >= 32767 {
			m.audioAtLimit++
		}
		m.audioSamples++
	}
}

func (m *writeLoopMeter) afterWrite() {
	now := time.Now()
	if m.window.IsZero() {
		m.window = now
	}
	if !m.last.IsZero() {
		if g := now.Sub(m.last); g > m.maxGap {
			m.maxGap = g
		}
		if w := m.workDone.Sub(m.last); w > m.maxWork {
			m.maxWork = w
		}
		m.periods++
	}
	m.last = now
	if now.Sub(m.window) >= time.Minute {
		log.Printf("[speaker] write loop: max gap %.1fms, max work %.1fms over %d periods (mix period %.1fms)",
			float64(m.maxGap.Microseconds())/1000, float64(m.maxWork.Microseconds())/1000, m.periods,
			float64(periodSize)*1000/48000)
		m.window, m.maxGap, m.maxWork, m.periods = now, 0, 0, 0
	}
}
