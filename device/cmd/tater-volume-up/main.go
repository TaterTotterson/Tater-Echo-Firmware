//go:build linux

// tater-volume-up is a static initramfs helper. Exit 0 means continuously held,
// 1 means idle/released, and 2 means unavailable. It never writes to a device.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/earlykey"
)

func main() {
	held, err := earlykey.Held(earlykey.VolumeUp, time.Now, time.Sleep)
	if err != nil {
		fmt.Fprintln(os.Stderr, "early Volume Up:", err)
		os.Exit(2)
	}
	if !held {
		os.Exit(1)
	}
}
