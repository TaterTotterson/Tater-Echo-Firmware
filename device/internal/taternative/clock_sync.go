package taternative

import (
	"math"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/clock"
)

// syncClockFromServer applies Tater's authenticated envelope timestamp when
// the Echo's wall clock is materially wrong. The step function is injected so
// the decision and unit conversion are testable without changing a test host.
func syncClockFromServer(serverUnixSeconds float64, deviceNow time.Time, step func(int64) error) (bool, error) {
	const maxUnixSeconds = float64((1<<63)-1) / 1000
	if step == nil || serverUnixSeconds <= 0 || serverUnixSeconds > maxUnixSeconds ||
		math.IsNaN(serverUnixSeconds) || math.IsInf(serverUnixSeconds, 0) {
		return false, nil
	}
	serverUnixMS := int64(math.Round(serverUnixSeconds * 1000))
	if !clock.ShouldStep(deviceNow, serverUnixMS) {
		return false, nil
	}
	return true, step(serverUnixMS)
}
