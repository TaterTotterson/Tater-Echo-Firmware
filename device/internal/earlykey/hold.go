// Package earlykey implements the read-only held-key check used by the Dot
// initramfs. It deliberately knows nothing about partitions or recovery writes.
package earlykey

import "time"

const (
	Settle = 2 * time.Second
	Hold   = 3 * time.Second
	Poll   = 100 * time.Millisecond
)

// Held waits a bounded time for the input driver, then requires a continuous
// hold. An idle key returns immediately; a release never becomes a new hold.
func Held(read func() (bool, error), now func() time.Time, sleep func(time.Duration)) (bool, error) {
	deadline := now().Add(Settle)
	for {
		pressed, err := read()
		if err == nil {
			if !pressed {
				return false, nil
			}
			break
		}
		if !now().Before(deadline) {
			return false, err
		}
		sleep(Poll)
	}
	deadline = now().Add(Hold)
	for now().Before(deadline) {
		sleep(Poll)
		pressed, err := read()
		if err != nil || !pressed {
			return false, err
		}
	}
	return true, nil
}
