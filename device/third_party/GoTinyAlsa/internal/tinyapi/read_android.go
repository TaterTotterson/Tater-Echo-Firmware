package tinyapi

// #include <tinyalsa/asoundlib.h>
import "C"

import (
	"fmt"
	"unsafe"
)

// Biscuit's emOS carries an older libtinyalsa.so without pcm_readi. Its
// pcm_read byte-count API is the capture path proven by the v0.2.6 firmware.
// Keep this in an Android-only file: even an unused pcm_readi reference stops
// the dynamic linker before main can run or the OTA supervisor can test audio.
func (d *PcmDevice) readFrames(buffer []byte, size, _ int) error {
	if result := int(C.pcm_read(d.pcmDevice, unsafe.Pointer(&buffer[0]), C.uint(size))); result != 0 {
		return fmt.Errorf("couldn't read PCM: %s (code %d)", d.GetError(), result)
	}
	return nil
}
