package codec

import (
	"strings"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/mixer"
)

// CaptureHighPassOffloaded accepts only the versioned driver interface's
// physically verified, complete stereo-ADC profile. Stock/older kernels and
// every other target keep the existing software filter. A name or a requested
// boot parameter alone is not evidence that coefficients reached the hardware.
func CaptureHighPassOffloaded(target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	if target != "biscuit" && target != "radar" {
		return false
	}
	values, err := mixer.GetIntegers("Tater ADC HPF Status")
	return err == nil && len(values) == 4 &&
		values[0] == 1 && values[1] == 80 && values[2] == 255 && values[3] == 16000
}
