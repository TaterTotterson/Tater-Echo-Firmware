package mixer

import (
	"errors"
	"reflect"
	"testing"
)

type fake struct {
	sets     [][]string
	byteSets map[string][]byte
	values   map[string]string
	fail     bool
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

func (f *fake) SetBytes(name string, values []byte) error {
	if f.fail {
		return errors.New("no such control")
	}
	if f.byteSets == nil {
		f.byteSets = make(map[string][]byte)
	}
	f.byteSets[name] = append([]byte(nil), values...)
	return nil
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

func TestSetBytesUsesTheAtomicByteBackend(t *testing.T) {
	f := &fake{}
	Use(f)
	defer Use(unavailable{})
	want := []byte{128, 0, 1, 247}
	if err := SetBytes("biquad coefficients", want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.byteSets["biquad coefficients"], want) {
		t.Fatalf("byte control = %v, want %v", f.byteSets["biquad coefficients"], want)
	}
	if err := SetBytes("biquad coefficients", nil); err == nil {
		t.Fatal("empty byte control write was accepted")
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

func TestADCMuteStateRequiresEveryControlToAgree(t *testing.T) {
	values := map[string]string{
		"ADC_A Left Mute": "On", "ADC_A Right Mute": "On",
		"ADC_B Left Mute": "On", "ADC_B Right Mute": "On",
		"ADC_C Left Mute": "On", "ADC_C Right Mute": "On",
		"ADC_D Left Mute": "On", "ADC_D Right Mute": "On",
	}
	Use(&fake{values: values})
	ConfigureTarget("biscuit")
	defer Use(unavailable{})

	muted, consistent, snapshot, err := ADCMuteState()
	if err != nil || !consistent || !muted {
		t.Fatalf("all muted = muted %v consistent %v err %v", muted, consistent, err)
	}
	if len(snapshot) != 8 {
		t.Fatalf("snapshot has %d controls, want 8", len(snapshot))
	}

	values["ADC_D Right Mute"] = "Off"
	muted, consistent, _, err = ADCMuteState()
	if err != nil {
		t.Fatal(err)
	}
	if consistent {
		t.Fatalf("mixed controls reported consistent (muted=%v)", muted)
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

func TestRadarEnablesBothSpeakerAmplifierGates(t *testing.T) {
	f := &fake{}
	Use(f)
	ConfigureTarget("radar")
	defer func() { ConfigureTarget("biscuit"); Use(unavailable{}) }()
	if err := SetSpeakerEnabled(true); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"Speaker_Amp_Switch", "On"},
		{"Ext_Speaker_Amp_Switch", "On"},
	}
	if !reflect.DeepEqual(f.sets, want) {
		t.Fatalf("radar amp writes = %v, want %v", f.sets, want)
	}
}

func TestRookMixerUsesTwoADCsAndActiveHighAmp(t *testing.T) {
	f := &fake{}
	Use(f)
	ConfigureTarget("rook")
	defer func() { ConfigureTarget("biscuit"); Use(unavailable{}) }()
	if err := SetSpeakerEnabled(true); err != nil {
		t.Fatal(err)
	}
	if failed := SetADCMute(true); failed != 0 {
		t.Fatalf("SetADCMute failed %d writes", failed)
	}
	want := [][]string{
		{"Ext_Speaker_Amp_Switch", "On"},
		{"ADC_A Left Mute", "1"}, {"ADC_A Right Mute", "1"},
		{"ADC_B Left Mute", "1"}, {"ADC_B Right Mute", "1"},
	}
	if !reflect.DeepEqual(f.sets, want) {
		t.Fatalf("rook mixer writes = %v, want %v", f.sets, want)
	}
}
