package buttons

import (
	"reflect"
	"testing"
)

func TestMuteEventsKeepNormalReleaseBehavior(t *testing.T) {
	controller := &EvDevController{}
	var events []string
	controller.SetMuteEventCallback(func(down bool, heldMs int64) {
		if down {
			events = append(events, "press")
			return
		}
		if heldMs != 320 {
			t.Fatalf("mute hold = %dms, want 320ms", heldMs)
		}
		events = append(events, "release")
	})
	controller.SetMuteCallback(func() { events = append(events, "toggle") })
	controller.dispatchMuteEvent(true, 0)
	controller.dispatchMuteEvent(false, 320)
	if want := []string{"press", "release", "toggle"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("mute events = %v, want %v", events, want)
	}
}

func TestInputPathByNameUsesHandlerNotEnumerationOrder(t *testing.T) {
	data := []byte(`I: Bus=0019 Vendor=0001 Product=0001 Version=0100
N: Name="gpio-keys"
P: Phys=gpio-keys/input0
S: Sysfs=/devices/platform/gpio-keys/input/input6
H: Handlers=event12 dynamic_boost
B: EV=21

I: Bus=0019 Vendor=2454 Product=6500 Version=0010
N: Name="mtk-kpd"
H: Handlers=kbd event1
B: EV=3

I: Bus=0019 Vendor=0000 Product=0000 Version=0000
N: Name="gating"
H: Handlers=event0
B: EV=3
`)
	if got := inputPathByName(data, "gpio-keys"); got != "/dev/input/event12" {
		t.Fatalf("gpio-keys path = %q", got)
	}
	if got := inputPathByName(data, "mtk-kpd"); got != "/dev/input/event1" {
		t.Fatalf("mtk-kpd path = %q", got)
	}
	if got := inputPathByName(data, "gating"); got != "/dev/input/event0" {
		t.Fatalf("gating path = %q", got)
	}
	if got := inputPathByName(data, "missing"); got != "" {
		t.Fatalf("missing path = %q", got)
	}
}
