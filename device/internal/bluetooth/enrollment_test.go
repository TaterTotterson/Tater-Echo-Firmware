package bluetooth

import (
	"bufio"
	"strings"
	"testing"
)

func TestScanBluetoothIdentityKeysUsesPeerPIDIRK(t *testing.T) {
	input := `[Adapter]
Address=00:11:22:33:44:55
[AA:BB:CC:DD:EE:FF]
LE_KEY_PENC=ffffffffffffffffffffffffffffffff
LE_KEY_PID=00112233445566778899AABBCCDDEEFF01AABBCCDDEEFF
`
	got := scanBluetoothIdentityKeys(bufio.NewScanner(strings.NewReader(input)))
	address, irk := newBluetoothIdentityKey(nil, got)
	if address != "AA:BB:CC:DD:EE:FF" {
		t.Fatalf("address = %q", address)
	}
	if irk != "00112233445566778899aabbccddeeff" {
		t.Fatalf("irk = %q", irk)
	}
}

func TestNewBluetoothIdentityKeyIgnoresExistingBond(t *testing.T) {
	keys := map[string]string{"AA:BB:CC:DD:EE:FF": "00112233445566778899aabbccddeeff01"}
	address, irk := newBluetoothIdentityKey(keys, keys)
	if address != "" || irk != "" {
		t.Fatalf("unexpected identity %q %q", address, irk)
	}
}
