//go:build linux

package earlykey

import "testing"

func TestInputABI(t *testing.T) {
	if inputRequest(0x18) != 0x80804518 || inputRequest(0x21) != 0x80804521 {
		t.Fatal("incorrect evdev ioctl ABI")
	}
	var bits [bitmapBytes]byte
	bits[115/8] = 1 << (115 % 8)
	if !keySet(bits[:], 115) || keySet(bits[:], 114) {
		t.Fatal("incorrect volume key bitmap")
	}
}

func TestOnlyPhysicalBoardKeysCanRequestRecovery(t *testing.T) {
	for _, name := range []string{"keys\n", "gpio-keys\n"} {
		if !physicalVolumeInput(name) {
			t.Fatalf("board input %q rejected", name)
		}
	}
	for _, name := range []string{"", "USB Keyboard", "mtk-kpd", "ACCDET"} {
		if physicalVolumeInput(name) {
			t.Fatalf("non-volume input %q accepted", name)
		}
	}
}
