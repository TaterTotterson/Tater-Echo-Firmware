package tinyapi

// #cgo LDFLAGS: -ldl -ltinyalsa
// #include <stdio.h>
// #include <stdlib.h>
// #include <tinyalsa/asoundlib.h>
//
// static void pcm_config_set_thresholds(struct pcm_config *config,
//                                       unsigned long start,
//                                       unsigned long stop,
//                                       unsigned long silence) {
//     config->start_threshold = start;
//     config->stop_threshold = stop;
//     config->silence_threshold = silence;
// }
import "C"
import (
	"github.com/Binozo/GoTinyAlsa/pkg/pcm"
	"unsafe"
)

const PCM_IN = C.PCM_IN
const PCM_OUT = C.PCM_OUT

const (
	PCM_FORMAT_INVALID = iota - 1
	/** Signed 16-bit, little endian */
	PCM_FORMAT_S16_LE
	/** Signed, 32-bit, little endian */
	PCM_FORMAT_S32_LE
	/** Signed, 8-bit */
	PCM_FORMAT_S8
	/** Signed, 24-bit (32-bit in memory), little endian */
	PCM_FORMAT_S24_LE
	/** Signed, 24-bit, little endian */
	PCM_FORMAT_S24_3LE

	/* End of compatibility section. */

	/** Signed, 16-bit, big endian */
	PCM_FORMAT_S16_BE
	/** Signed, 24-bit (32-bit in memory), big endian */
	PCM_FORMAT_S24_BE
	/** Signed, 24-bit, big endian */
	PCM_FORMAT_S24_3BE
	/** Signed, 32-bit, big endian */
	PCM_FORMAT_S32_BE
	/** 32-bit float, little endian */
	PCM_FORMAT_FLOAT_LE
	/** 32-bit float, big endian */
	PCM_FORMAT_FLOAT_BE
	/** Max of the enumeration list, not an actual format. */
	PCM_FORMAT_MAX
)

var Formats = map[int]string{
	0:  "S8",
	1:  "U8",
	2:  "S16_LE",
	3:  "S16_BE",
	4:  "U16_LE",
	5:  "U16_BE",
	6:  "S24_LE",
	7:  "S24_BE",
	8:  "U24_LE",
	9:  "U24_BE",
	10: "S32_LE",
	11: "S32_BE",
	12: "U32_LE",
	13: "U32_BE",
	14: "FLOAT_LE",
	15: "FLOAT_BE",
	16: "FLOAT64_LE",
	17: "FLOAT64_BE",
	18: "IEC958_SUBFRAME_LE",
	19: "IEC958_SUBFRAME_BE",
	20: "MU_LAW",
	21: "A_LAW",
	22: "IMA_ADPCM",
	23: "MPEG",
	24: "GSM",

	31: "SPECIAL",
	32: "S24_3LE",
	33: "S24_3BE",
	34: "U24_3LE",
	35: "U24_3BE",
	36: "S20_3LE",
	37: "S20_3BE",
	38: "U20_3LE",
	39: "U20_3BE",
	40: "S18_3LE",
	41: "S18_3BE",
	42: "U18_3LE",
	43: "U18_3BE",
}

func PcmOpen(cardNr int, deviceNr int, openFlags int, config pcm.Config) (PcmDevice, error) {
	var internalConfig *C.struct_pcm_config = new(C.struct_pcm_config)
	internalConfig.channels = C.uint(config.Channels)
	internalConfig.rate = C.uint(config.SampleRate)
	internalConfig.period_size = C.uint(config.PeriodSize)
	internalConfig.period_count = C.uint(config.PeriodCount)

	var format C.enum_pcm_format = C.enum_pcm_format(config.Format)
	internalConfig.format = format

	// tinyalsa changed these fields from unsigned long to unsigned int. Let C
	// perform the checked narrowing so this wrapper builds against both Fire
	// OS's older headers and current Alpine without binding cgo to one field
	// spelling.
	C.pcm_config_set_thresholds(internalConfig,
		C.ulong(config.StartThreshold),
		C.ulong(config.StopThreshold),
		C.ulong(config.SilenceThreshold))

	// Open device
	var pcmDevice *C.struct_pcm = C.pcm_open(C.uint(cardNr), C.uint(deviceNr), C.uint(openFlags), internalConfig)
	device := PcmDevice{
		pcmDevice: pcmDevice,
		Config:    config,
	}

	// Check if device is ready
	if !device.IsReady() {
		errorMsg := device.getErrorMsg()
		if errorMsg == "" {
			// No real error occurred. Continue as normal
			return device, nil
		}
		return PcmDevice{}, device.GetError()
	}
	return device, nil
}

func GetParams(cardNr int, deviceNr int) (pcm.Info, pcm.Info) {
	var inInfo pcm.Info
	var outInfo pcm.Info
	for flag := 1; flag <= 2; flag++ {
		info := pcm.Info{}
		var params *C.struct_pcm_params
		if flag == 1 {
			params = C.pcm_params_get(C.uint(cardNr), C.uint(deviceNr), PCM_IN)
		} else if flag == 2 {
			params = C.pcm_params_get(C.uint(cardNr), C.uint(deviceNr), PCM_OUT)
		}
		if params == nil {
			// Device is either busy or doesn't exist
			continue
		}
		access := C.pcm_params_get_mask(params, C.PCM_PARAM_ACCESS)
		info.Access = uint(access.bits[0])

		format := C.pcm_params_get_mask(params, C.PCM_PARAM_FORMAT)
		bitCount := int(unsafe.Sizeof(int(format.bits[0])) * 8)
		info.Format0 = uint(format.bits[0])
		info.Format1 = uint(format.bits[1])
		info.FormatNames = make([]string, 0)
		for i := 0; i < 2; i++ {
			for j := 0; j < bitCount; j++ {
				// Check if format name exists
				if format.bits[i]&(1<<j) != 0 {
					name := Formats[j+i*bitCount]
					info.FormatNames = append(info.FormatNames, name)
				}
			}
		}

		subFormat := C.pcm_params_get_mask(params, C.PCM_PARAM_SUBFORMAT)
		info.Subformat = uint(subFormat.bits[0])

		info.RateMin = uint(C.pcm_params_get_min(params, C.PCM_PARAM_RATE))
		info.RateMax = uint(C.pcm_params_get_max(params, C.PCM_PARAM_RATE))

		info.ChannelsMin = uint(C.pcm_params_get_min(params, C.PCM_PARAM_CHANNELS))
		info.ChannelsMax = uint(C.pcm_params_get_max(params, C.PCM_PARAM_CHANNELS))

		info.SampleBitsMin = uint(C.pcm_params_get_min(params, C.PCM_PARAM_SAMPLE_BITS))
		info.SampleBitsMax = uint(C.pcm_params_get_max(params, C.PCM_PARAM_SAMPLE_BITS))

		info.PeriodSizeMin = uint(C.pcm_params_get_min(params, C.PCM_PARAM_PERIOD_SIZE))
		info.PeriodSizeMax = uint(C.pcm_params_get_max(params, C.PCM_PARAM_PERIOD_SIZE))

		info.PeriodCountMin = uint(C.pcm_params_get_min(params, C.PCM_PARAM_PERIODS))
		info.PeriodCountMax = uint(C.pcm_params_get_max(params, C.PCM_PARAM_PERIODS))

		C.pcm_params_free(params)

		if flag == 1 {
			inInfo = info
		} else if flag == 2 {
			outInfo = info
		}
	}
	return inInfo, outInfo
}
