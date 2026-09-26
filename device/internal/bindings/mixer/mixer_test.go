package mixer

import (
	"errors"
	"reflect"
	"testing"
)

type fake struct {
	sets   [][]string
	values map[string]string
	fail   bool
}

func (f *fake) Set(name string, v []string) error {
	if f.fail {
		return errors.New("no such control")
	}
	f.sets = append(f.sets, append([]string{name}, v...))
	return nil
}

func (f *fake) Get(name string) (string, error) {
	v, ok := f.values[name]
	if !ok {
		return "", errors.New("no such control")
	}
	return v, nil
}

func TestSetPassesNameAndValues(t *testing.T) {
	f := &fake{}
	Use(f)
	defer Use(unavailable{})
	if err := Set(PlaybackVolume, "100", "100"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"PCM Playback Volume", "100", "100"}}
	if !reflect.DeepEqual(f.sets, want) {
		t.Fatalf("got %v", f.sets)
	}
	if err := Set(SpeakerAmp); err == nil {
		t.Fatal("a write with no value must be refused")
	}
}

func TestFailuresAreReturnedEveryTime(t *testing.T) {
	Use(&fake{fail: true})
	defer Use(unavailable{})
	for i := 0; i < 3; i++ {
		if Set(SpeakerAmp, "On") == nil {
			t.Fatalf("attempt %d: error swallowed", i)
		}
	}
}

func TestHostBuildHasNoBackend(t *testing.T) {
	Use(unavailable{})
	if Set(SpeakerAmp, "On") == nil {
		t.Fatal("a build with no mixer must not report success")
	}
	if _, err := Get(SpeakerAmp); err == nil {
		t.Fatal("a build with no mixer must not report a value")
	}
}

func TestSpread(t *testing.T) {
	got, err := spread("x", []string{"7"}, 2)
	if err != nil || !reflect.DeepEqual(got, []string{"7", "7"}) {
		t.Fatalf("one value, two channels: %v %v", got, err)
	}
	if got, err = spread("x", []string{"1", "2"}, 2); err != nil || got[1] != "2" {
		t.Fatalf("one per channel: %v %v", got, err)
	}
	if _, err = spread("x", []string{"1", "2"}, 1); err == nil {
		t.Fatal("too many values must be refused")
	}
	if _, err = spread("x", []string{"1"}, 0); err == nil {
		t.Fatal("a control with no values must be refused")
	}
}

func TestBoolValue(t *testing.T) {
	for s, want := range map[string]int{"1": 1, "On": 1, "on": 1, "0": 0, "Off": 0, "off": 0} {
		if v, ok := boolValue(s); !ok || v != want {
			t.Errorf("%q = %d,%v", s, v, ok)
		}
	}
	for _, s := range []string{"", "2", "true", "ON "} {
		if _, ok := boolValue(s); ok {
			t.Errorf("%q accepted", s)
		}
	}
}

func TestCheckersMixerMapping(t *testing.T) {
	f := &fake{values: map[string]string{"DAC1 Playback Volume": "173"}}
	Use(f)
	ConfigureTarget("checkers")
	defer func() {
		ConfigureTarget("biscuit")
		Use(unavailable{})
	}()

	if err := SetPlaybackLevel(127, 127); err != nil {
		t.Fatal(err)
	}
	if err := SetSpeakerEnabled(true); err != nil {
		t.Fatal(err)
	}
	if failed := SetADCMute(true); failed != 0 {
		t.Fatalf("SetADCMute failed %d writes", failed)
	}
	want := [][]string{
		{"DAC1 Playback Volume", "173"},
		{"Ext_Speaker_Amp_Switch", "Off"},
		{"ADC_A Left Mute", "1"},
		{"ADC_A Right Mute", "1"},
	}
	if !reflect.DeepEqual(f.sets, want) {
		t.Fatalf("checkers writes = %v, want %v", f.sets, want)
	}
	if got, err := GetPlaybackLevel(127); err != nil || got != 127 {
		t.Fatalf("GetPlaybackLevel = %d, %v; want 127", got, err)
	}
}

func TestBiscuitSpeakerAmpIsActiveHigh(t *testing.T) {
	f := &fake{}
	Use(f)
	ConfigureTarget("biscuit")
	defer Use(unavailable{})
	if err := SetSpeakerEnabled(true); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.sets, [][]string{{"Ext_Speaker_Amp_Switch", "On"}}) {
		t.Fatalf("biscuit amp writes = %v", f.sets)
	}
}
