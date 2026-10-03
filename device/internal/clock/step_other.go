//go:build !linux

package clock

import "fmt"

// Step is present on development hosts so protocol tests can compile. Echo
// firmware is Linux and uses the CLOCK_REALTIME implementation.
func Step(int64) error {
	return fmt.Errorf("clock step is only supported on Linux")
}
