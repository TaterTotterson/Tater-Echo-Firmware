package buttons

import "testing"

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
