package codec

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"sync"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/mixer"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/micgain"
)

var (
	captureGainOnce sync.Once
	captureGainErr  error
)

// EnsureCaptureGain sets the Puffin microphone PGA before capture opens. The
// software path is unity, so a missing/mismatched control must stop startup,
// not quietly leave the microphones 40 dB too low. Other boards retain their
// existing gain staging. Both channels of all four ADCs must verify.
func EnsureCaptureGain(target string) error {
	if micgain.HardwareDB(target) == 0 {
		return nil
	}
	captureGainOnce.Do(func() {
		captureGainErr = applyCaptureGain(micgain.HardwareDB(target))
	})
	return captureGainErr
}

func stereoGain(name string, min, max int) ([]int, error) {
	values, err := mixer.GetIntegers(name)
	if err != nil {
		return nil, err
	}
	if len(values) != 2 {
		return nil, fmt.Errorf("codec: %q has %d values, expected stereo", name, len(values))
	}
	for _, v := range values {
		if v < min || v > max {
			return nil, fmt.Errorf("codec: %q has unexpected values %v", name, values)
		}
	}
	return values, nil
}

func applyCaptureGain(db int) error {
	// TLV320ADC3101 PGA: half-dB steps, 0..80. The ADC digital-volume
	// control's unity is 88 on this vendor driver, even though its advertised
	// maximum is incorrectly 64. Read/require unity; do NOT clamp it to that
	// maximum or write it through tinyalsa's range-checked scalar path.
	if db != 40 {
		return fmt.Errorf("codec: unqualified microphone hardware gain %d dB", db)
	}
	const digitalUnity = 88
	raw := db * 2
	var original [4][]int
	for i := range original {
		adc := string(rune('A' + i))
		if _, err := stereoGain("ADC_"+adc+" Digital Volume Control", digitalUnity, digitalUnity); err != nil {
			return err
		}
		values, err := stereoGain("ADC_"+adc+" MICPGA Volume Ctrl", 0, 80)
		if err != nil {
			return err
		}
		original[i] = values
	}

	// Snapshot every PGA before the first write. Even a failed stereo write
	// can have changed its first channel; include it in the rollback set.
	touched := 0
	rollback := func(cause error) error {
		for i := touched - 1; i >= 0; i-- {
			name := fmt.Sprintf("ADC_%c MICPGA Volume Ctrl", 'A'+i)
			var restoreErr error
			for attempt := 0; attempt < 3; attempt++ {
				restoreErr = mixer.Set(name, strconv.Itoa(original[i][0]), strconv.Itoa(original[i][1]))
				if restoreErr == nil {
					values, err := stereoGain(name, 0, 80)
					restoreErr = err
					if err == nil && (values[0] != original[i][0] || values[1] != original[i][1]) {
						restoreErr = fmt.Errorf("codec: %q rollback readback %v, expected %v", name, values, original[i])
					}
				}
				if restoreErr == nil {
					break
				}
			}
			cause = errors.Join(cause, restoreErr)
		}
		return cause
	}
	for i := range original {
		name := fmt.Sprintf("ADC_%c MICPGA Volume Ctrl", 'A'+i)
		touched = i + 1
		if err := mixer.Set(name, strconv.Itoa(raw)); err != nil {
			return rollback(err)
		}
	}
	for i := range original {
		if _, err := stereoGain(fmt.Sprintf("ADC_%c MICPGA Volume Ctrl", 'A'+i), raw, raw); err != nil {
			return rollback(err)
		}
		if _, err := stereoGain(fmt.Sprintf("ADC_%c Digital Volume Control", 'A'+i), digitalUnity, digitalUnity); err != nil {
			return rollback(err)
		}
	}
	log.Printf("[codec] four microphone PGAs verified at +%d dB; ADC digital gain unity; software boost disabled", db)
	return nil
}
