//go:build linux || android

package microwakeword

import "golang.org/x/sys/unix"

func pinCurrentThread(cpu int) error {
	var set unix.CPUSet
	set.Zero()
	set.Set(cpu)
	return unix.SchedSetaffinity(0, &set)
}
