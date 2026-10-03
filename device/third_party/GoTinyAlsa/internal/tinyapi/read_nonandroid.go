//go:build !android

package tinyapi

// #include <tinyalsa/asoundlib.h>
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// Checkers' Linux userspace has tinyalsa 2.x. Its frame API completes short
// reads, while pcm_read can return before the Checkers capture DMA advances.
// Production Checkers uses arecord for capture, but retain the correct Linux
// implementation for any other caller of GoTinyAlsa.
func (d *PcmDevice) readFrames(buffer []byte, size, bytesPerFrame int) error {
	remaining := size / bytesPerFrame
	offset := 0
	for remaining > 0 {
		read := int(C.pcm_readi(d.pcmDevice, unsafe.Pointer(&buffer[offset]), C.uint(remaining)))
		if read < 0 {
			return fmt.Errorf("couldn't read frames: %s", d.GetError())
		}
		if read == 0 {
			return errors.New("couldn't read frames: zero-length PCM read")
		}
		offset += read * bytesPerFrame
		remaining -= read
	}
	return nil
}
