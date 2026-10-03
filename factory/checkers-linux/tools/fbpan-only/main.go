// fbpan-only isolates TECHO5's FBIOPAN_DISPLAY path without touching mapped
// framebuffer memory. It is a hardware diagnostic, not part of the firmware.
package main

import (
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"
)

const (
	fbioGetVScreenInfo = 0x4600
	fbioPanDisplay     = 0x4606
)

// fb_var_screeninfo, as used by TECHO5's screen and fbprobe packages.
type varInfo struct {
	Xres, Yres, XresVirtual, YresVirtual, Xoffset, Yoffset uint32
	BitsPerPixel, Grayscale                                uint32
	Red, Green, Blue, Transp                               [3]uint32
	Nonstd, Activate, Height, Width, AccelFlags            uint32
	Pixclock, LeftMargin, RightMargin, UpperMargin         uint32
	LowerMargin, HsyncLen, VsyncLen, Sync, Vmode, Rotate   uint32
	Colorspace                                             uint32
	Reserved                                               [4]uint32
}

func ioctl(fd uintptr, request uintptr, value *varInfo) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(unsafe.Pointer(value)))
	if errno != 0 {
		return errno
	}
	return nil
}

func main() {
	f, err := os.OpenFile("/dev/graphics/fb0", os.O_RDWR, 0)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	var v varInfo
	if err := ioctl(f.Fd(), fbioGetVScreenInfo, &v); err != nil {
		panic(err)
	}
	fmt.Printf("geometry=%dx%d virtual=%dx%d initial-y=%d activate=%d\n", v.Xres, v.Yres, v.XresVirtual, v.YresVirtual, v.Yoffset, v.Activate)
	pages := int(v.YresVirtual / v.Yres)
	for n := 0; n < 20; n++ {
		page := n % pages
		v.Xoffset, v.Yoffset = 0, uint32(page)*v.Yres
		started := time.Now()
		fmt.Printf("before pan %d page=%d y=%d\n", n, page, v.Yoffset)
		if err := ioctl(f.Fd(), fbioPanDisplay, &v); err != nil {
			fmt.Printf("pan %d failed: %v\n", n, err)
			os.Exit(1)
		}
		fmt.Printf("after pan %d took=%s\n", n, time.Since(started))
		time.Sleep(200 * time.Millisecond)
	}
}
