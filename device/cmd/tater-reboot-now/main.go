//go:build linux

// tater-reboot-now performs the final kernel reboot after a transaction has
// made its own data durable. Checkers' vendor kernel can wedge in filesystem
// writeback or driver shutdown, so this helper supports narrow fsyncs and then
// uses the kernel's emergency-restart path instead of an orderly device walk.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func main() {
	// Factory provisioning may pass its few newly written files. Persist those
	// files and their containing directories without flushing the unrelated
	// rootfs slot store. Routine OTA has already synced its application slot.
	args := os.Args[1:]
	for len(args) > 0 {
		if args[0] == "--directory" {
			if len(args) < 2 {
				fail("--directory requires a path")
			}
			syncPath(args[1], false)
			args = args[2:]
			continue
		}
		syncPath(args[0], true)
		args = args[1:]
	}
	if trigger, err := os.OpenFile("/proc/sysrq-trigger", os.O_WRONLY, 0); err == nil {
		if _, err := trigger.WriteString("b"); err == nil {
			// A successful emergency restart does not normally return. If this
			// kernel queues it asynchronously, returning is still correct.
			_ = trigger.Close()
			return
		}
		_ = trigger.Close()
	}
	if err := syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART); err != nil {
		fail("reboot: %v", err)
	}
}

func syncPath(name string, syncParent bool) {
	file, err := os.Open(name)
	if err != nil {
		fail("open %s: %v", name, err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		fail("sync %s: %v", name, err)
	}
	if err := file.Close(); err != nil {
		fail("close %s: %v", name, err)
	}
	if !syncParent {
		return
	}
	directory, err := os.Open(filepath.Dir(name))
	if err != nil {
		fail("open directory for %s: %v", name, err)
	}
	if err := directory.Sync(); err != nil {
		directory.Close()
		fail("sync directory for %s: %v", name, err)
	}
	if err := directory.Close(); err != nil {
		fail("close directory for %s: %v", name, err)
	}
}

func fail(format string, values ...any) {
	fmt.Fprintf(os.Stderr, "tater-reboot-now: "+format+"\n", values...)
	os.Exit(1)
}
