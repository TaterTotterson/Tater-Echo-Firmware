package tinyapi

// #include <stdio.h>
// #include <stdlib.h>
// #include <tinyalsa/asoundlib.h>
import "C"
import (
	"errors"
	"fmt"
	"github.com/Binozo/GoTinyAlsa/pkg/pcm"
	"time"
	"unsafe"
)

type PcmDevice struct {
	pcmDevice *C.struct_pcm
	Config    pcm.Config
}

func (d *PcmDevice) FrameBytesSize() int {
	return int(C.pcm_frames_to_bytes(d.pcmDevice, C.pcm_get_buffer_size(d.pcmDevice)))
}

func (d *PcmDevice) BytesPerFrame() int {
	return int(C.pcm_frames_to_bytes(d.pcmDevice, 1))
}

func (d *PcmDevice) BitsPerSample() uint16 {
	return d.Config.Format.BitsPerSample()
}

func (d *PcmDevice) GetError() error {
	message := d.getErrorMsg()
	if message == "" {
		// Check if device is ready
		if !d.IsReady() {
			return errors.New("device is not ready")
		}
	}
	return errors.New(message)
}

func (d *PcmDevice) getErrorMsg() string {
	p := C.pcm_get_error(d.pcmDevice)
	return C.GoString(p)
}

func (d *PcmDevice) IsReady() bool {
	return C.pcm_is_ready(d.pcmDevice) == 1
}

func (d *PcmDevice) WaitUntilReady(timeout time.Duration) error {
	result := int(C.pcm_wait(d.pcmDevice, C.int(timeout.Milliseconds())))
	if result == 1 {
		// Frame became available
		return nil
	} else if result == 0 {
		// Timeout occurred
		return errors.New("timeout")
	} else {
		// Result is below zero => error
		// Parse errno.h error
		// Reference: https://www2.hs-fulda.de/~klingebiel/c-stdlib/sys.errno.h.htm
		// Reference: https://github.com/tinyalsa/tinyalsa/blob/f78ed25aced2dfea743867b8205a787bfb091340/src/pcm.c#L1475C15-L1475C15
		switch result {
		case -5:
			return errors.New("I/O error")
		case -32:
			return errors.New("broken pipe")
		case -92:
			return errors.New("unstable sleeping")
		case -19:
			return errors.New("device not found")
		}
		return errors.New(fmt.Sprintf("pcm error: %d (%s)", result, d.GetError()))
	}
}

func (d *PcmDevice) ReadFrames(buffer []byte, size int) error {
	if size == 0 {
		return nil
	}
	if size < 0 || size > len(buffer) {
		return fmt.Errorf("invalid PCM read size %d for %d-byte buffer", size, len(buffer))
	}
	bytesPerFrame := d.BytesPerFrame()
	if bytesPerFrame <= 0 || size%bytesPerFrame != 0 {
		return fmt.Errorf("PCM read size %d is not a whole number of %d-byte frames", size, bytesPerFrame)
	}

	return d.readFrames(buffer, size, bytesPerFrame)
}

func (d *PcmDevice) WriteFrames(buffer []byte, size int) error {
	framesWritten := C.uint(C.pcm_write(d.pcmDevice, unsafe.Pointer(&buffer[0]), C.uint(size)))
	if framesWritten != 0 {
		// Error occurred
		return errors.New(fmt.Sprintf("couldn't write frames: %s", d.GetError()))
	}
	return nil
}

func (d *PcmDevice) Close() {
	C.pcm_close(d.pcmDevice)
}
