//go:build !linux && !android

package microwakeword

import "fmt"

func pinCurrentThread(cpu int) error {
	return fmt.Errorf("CPU affinity is unavailable on this platform")
}
