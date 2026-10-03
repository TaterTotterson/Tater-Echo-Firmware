package tinypcm

// #include <stdio.h>
// #include <stdlib.h>
// #include <tinyalsa/asoundlib.h>
import "C"

type Format C.enum_pcm_format

func (f *Format) BitsPerSample() uint16 {
	var format C.enum_pcm_format = C.enum_pcm_format(*f)
	return uint16(C.pcm_format_to_bits(format))
}
