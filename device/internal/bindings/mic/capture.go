package mic

import (
	"errors"
	"io"
	"sync/atomic"
	"time"

	pkgmic "github.com/TaterTotterson/Tater-Echo-Firmware/pkg/mic"
)

var errCaptureStalled = errors.New("microphone capture stopped making sample progress")

// readCaptureBatches watches complete batches, so a producer dripping partial
// bytes cannot keep a deaf microphone alive. interrupt must unblock Read (the
// live backend kills arecord). No extra reader goroutine survives a restart.
func readCaptureBatches(reader io.Reader, batchBytes int, output chan<- pkgmic.Capture,
	startupTimeout, progressTimeout time.Duration, interrupt func()) error {
	var stalled atomic.Bool
	watchdog := time.AfterFunc(startupTimeout, func() {
		stalled.Store(true)
		interrupt()
	})
	defer watchdog.Stop()
	for {
		buffer := make([]byte, batchBytes)
		if _, err := io.ReadFull(reader, buffer); err != nil {
			if stalled.Load() {
				return errCaptureStalled
			}
			return err
		}
		if stalled.Load() {
			return errCaptureStalled
		}
		watchdog.Reset(progressTimeout)
		batch := pkgmic.Capture{PCM: buffer, CapturedAt: time.Now()}
		timer := time.NewTimer(progressTimeout)
		select {
		case output <- batch:
			timer.Stop()
		case <-timer.C:
			return errors.New("microphone capture consumer stopped")
		}
	}
}
