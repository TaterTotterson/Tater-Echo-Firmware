package codec

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/mixer"
)

type captureGainFake struct {
	values       map[string][]int
	writes       []string
	reads        map[string]int
	writeHook    func(string, []int) error
	readHook     func(string, []int) ([]int, error)
	preflightBad bool
}

func newCaptureGainFake() *captureGainFake {
	f := &captureGainFake{values: map[string][]int{}, reads: map[string]int{}}
	for _, adc := range []string{"A", "B", "C", "D"} {
		f.values["ADC_"+adc+" MICPGA Volume Ctrl"] = []int{0, 0}
		f.values["ADC_"+adc+" Digital Volume Control"] = []int{88, 88}
	}
	return f
}

func (f *captureGainFake) Set(name string, values []string) error {
	if len(f.writes) == 0 && len(f.reads) != 8 {
		f.preflightBad = true
	}
	f.writes = append(f.writes, name)
	next := make([]int, 2)
	for i := range next {
		s := values[0]
		if len(values) > 1 {
			s = values[i]
		}
		var err error
		next[i], err = strconv.Atoi(s)
		if err != nil {
			return err
		}
	}
	if f.writeHook != nil {
		if err := f.writeHook(name, next); err != nil {
			return err
		}
	}
	f.values[name] = next
	return nil
}

func (f *captureGainFake) Get(string) (string, error) {
	return "", errors.New("first-channel reads cannot verify stereo gain")
}

func (f *captureGainFake) GetIntegers(name string) ([]int, error) {
	f.reads[name]++
	values, ok := f.values[name]
	if !ok {
		return nil, errors.New("missing control")
	}
	values = append([]int(nil), values...)
	if f.readHook != nil {
		return f.readHook(name, values)
	}
	return values, nil
}

func useCaptureGainFake(t *testing.T) *captureGainFake {
	t.Helper()
	captureGainOnce, captureGainErr = sync.Once{}, nil
	f := newCaptureGainFake()
	mixer.Use(f)
	t.Cleanup(func() {
		captureGainOnce, captureGainErr = sync.Once{}, nil
		mixer.Use(&codecMixerFake{})
	})
	return f
}

func TestCaptureGainVerifiesBothChannelsOnEveryADC(t *testing.T) {
	for _, target := range []string{"biscuit", "radar"} {
		t.Run(target, func(t *testing.T) {
			f := useCaptureGainFake(t)
			if err := EnsureCaptureGain(target); err != nil {
				t.Fatal(err)
			}
			if f.preflightBad || len(f.writes) != 4 {
				t.Fatalf("gain writes=%v preflightBad=%v", f.writes, f.preflightBad)
			}
			for name, values := range f.values {
				want := []int{80, 80}
				if strings.Contains(name, "Digital") {
					want = []int{88, 88}
				}
				if !reflect.DeepEqual(values, want) || f.reads[name] != 2 {
					t.Fatalf("%s=%v reads=%d, want %v and pre/post readback", name, values, f.reads[name], want)
				}
			}
			if err := EnsureCaptureGain(target); err != nil || len(f.writes) != 4 {
				t.Fatalf("gain reapplied: writes=%v err=%v", f.writes, err)
			}
		})
	}
}

func TestCaptureGainDoesNotChangeOtherBoards(t *testing.T) {
	for _, target := range []string{"checkers", "cronos", "crown", "athena", "rook", "", "unknown"} {
		f := useCaptureGainFake(t)
		if err := EnsureCaptureGain(target); err != nil || len(f.reads) != 0 || len(f.writes) != 0 {
			t.Fatalf("%q touched the gain controls: %v", target, err)
		}
	}
}

func TestCaptureGainPreflightRefusesUnexpectedHardwareWithoutWrites(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*captureGainFake)
	}{
		{"missing", func(f *captureGainFake) { delete(f.values, "ADC_D MICPGA Volume Ctrl") }},
		{"not stereo", func(f *captureGainFake) { f.values["ADC_C MICPGA Volume Ctrl"] = []int{0} }},
		{"PGA out of range", func(f *captureGainFake) { f.values["ADC_B MICPGA Volume Ctrl"] = []int{0, 81} }},
		{"PGA read failed", func(f *captureGainFake) { f.values["ADC_A MICPGA Volume Ctrl"] = []int{-5, -5} }},
		{"digital right not unity", func(f *captureGainFake) { f.values["ADC_D Digital Volume Control"] = []int{88, 68} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := useCaptureGainFake(t)
			test.edit(f)
			if err := EnsureCaptureGain("radar"); err == nil || len(f.writes) != 0 {
				t.Fatalf("unexpected hardware accepted: writes=%v err=%v", f.writes, err)
			}
			// A later speaker/microphone init must not swallow the failure.
			if err := EnsureCaptureGain("radar"); err == nil || len(f.writes) != 0 {
				t.Fatal("startup failure was forgotten")
			}
		})
	}
}

func TestCaptureGainRestoresPartialWritesAndReadbackFailures(t *testing.T) {
	for _, failure := range []string{"partial write", "wrong right channel", "digital changed", "read error"} {
		t.Run(failure, func(t *testing.T) {
			f := useCaptureGainFake(t)
			// Preserve exact original pairs, not an assumed all-zero baseline.
			for _, adc := range []string{"A", "B", "C", "D"} {
				f.values["ADC_"+adc+" MICPGA Volume Ctrl"] = []int{6, 8}
			}
			switch failure {
			case "partial write":
				f.writeHook = func(name string, next []int) error {
					if name == "ADC_C MICPGA Volume Ctrl" && next[0] == 80 {
						f.values[name][0] = 80 // first half succeeded, second failed
						return errors.New("second-channel write failed")
					}
					return nil
				}
			default:
				f.readHook = func(name string, values []int) ([]int, error) {
					if f.reads[name] == 2 && name == "ADC_B MICPGA Volume Ctrl" {
						if failure == "wrong right channel" {
							values[1] = 0
						} else if failure == "read error" {
							return nil, errors.New("ALSA read failed")
						}
					}
					if failure == "digital changed" && f.reads[name] == 2 && name == "ADC_D Digital Volume Control" {
						values[1] = 68
					}
					return values, nil
				}
			}
			if err := EnsureCaptureGain("biscuit"); err == nil {
				t.Fatal("gain failure did not stop startup")
			}
			for _, adc := range []string{"A", "B", "C", "D"} {
				name := "ADC_" + adc + " MICPGA Volume Ctrl"
				if !reflect.DeepEqual(f.values[name], []int{6, 8}) {
					t.Fatalf("%s not restored: %v", name, f.values[name])
				}
			}
		})
	}
}

func TestCaptureGainReportsFailedRollback(t *testing.T) {
	f := useCaptureGainFake(t)
	f.writeHook = func(name string, next []int) error {
		if name == "ADC_B MICPGA Volume Ctrl" || next[0] == 0 {
			return fmt.Errorf("persistent I/O failure: %s", name)
		}
		return nil
	}
	err := EnsureCaptureGain("biscuit")
	if err == nil || !strings.Contains(err.Error(), "ADC_A MICPGA") {
		t.Fatalf("rollback failure was hidden: %v", err)
	}
	// One A write, one failed B write and three restore attempts for each.
	if len(f.writes) != 8 {
		t.Fatalf("rollback retries=%v", f.writes)
	}
}
