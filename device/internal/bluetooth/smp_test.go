package bluetooth

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func reverseHex(t *testing.T, value string) []byte {
	t.Helper()
	raw, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	for left, right := 0, len(raw)-1; left < right; left, right = left+1, right-1 {
		raw[left], raw[right] = raw[right], raw[left]
	}
	return raw
}

func TestS1BluetoothSpecificationVector(t *testing.T) {
	// Core Vol 3 Part H 2.2.4. Inputs are reversed here because SMP carries
	// 128-bit integers least-significant octet first.
	tk := make([]byte, 16)
	r1 := reverseHex(t, "000f0e0d0c0b0a091122334455667788")
	r2 := reverseHex(t, "010203040506070899aabbccddeeff00")
	got, err := smpS1(tk, r1, r2)
	if err != nil {
		t.Fatal(err)
	}
	want := reverseHex(t, "9a1fe1f0e8b0f49b5b4216ae796da062")
	if !equal16(got[:], want) {
		t.Fatalf("s1 = %x, want %x", got, want)
	}
}

func TestC1BluetoothSpecificationVector(t *testing.T) {
	tk := make([]byte, 16)
	random := reverseHex(t, "5783d52156ad6f0e6388274ec6702ee0")
	preq := reverseHexVariable(t, "07071000000101")
	pres := reverseHexVariable(t, "05000800000302")
	ia := reverseHexVariable(t, "a1a2a3a4a5a6")
	ra := reverseHexVariable(t, "b1b2b3b4b5b6")
	got, err := smpC1(tk, random, pres, preq, 1, ia, 0, ra)
	if err != nil {
		t.Fatal(err)
	}
	want := reverseHex(t, "1e1e3fef878988ead2a74dc5bef13b86")
	if !equal16(got[:], want) {
		t.Fatalf("c1 = %x, want %x", got, want)
	}
}

func reverseHexVariable(t *testing.T, value string) []byte {
	t.Helper()
	raw, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	for left, right := 0, len(raw)-1; left < right; left, right = left+1, right-1 {
		raw[left], raw[right] = raw[right], raw[left]
	}
	return raw
}

func TestPairRequiresExactlySixDigits(t *testing.T) {
	for _, pin := range []string{"", "12345", "1234567", "12x456"} {
		if _, err := pairingTK(pin); err == nil {
			t.Fatalf("accepted %q", pin)
		}
	}
	if tk, err := pairingTK("000123"); err != nil || tk[0] != 123 || tk[1] != 0 {
		t.Fatalf("TK %x, %v", tk, err)
	}
}

func TestPairEncryptsPersistsBondAndReconnects(t *testing.T) {
	f := newFakeCtl()
	p := f.peer(peerA)
	p.pairPIN = "123456"
	path := filepath.Join(t.TempDir(), "ble", "bonds.json")
	m := session(t, f, func(s *Scanner) {
		s.Conns().SetOwnAddress(StaticRandomAddr("pair-test"))
		if err := s.Conns().SetBondStore(path); err != nil {
			t.Fatal(err)
		}
	}).Conns()
	if _, err := m.Connect(peerA, 1); err != nil {
		t.Fatal(err)
	}
	info, err := m.Pair(peerA, p.pairPIN)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Bonded || !info.Encrypted || !info.Authenticated {
		t.Fatalf("pair info %+v", info)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), p.pairPIN) {
		t.Fatal("bond file contains the PIN")
	}
	if mode := (func() os.FileMode { st, _ := os.Stat(path); return st.Mode().Perm() })(); mode != 0o600 {
		t.Fatalf("bond mode %o", mode)
	}
	if err := m.Disconnect(peerA); err != nil {
		t.Fatal(err)
	}
	// Connect automatically restores encryption from the saved bond without
	// asking for the passkey again.
	if _, err := m.Connect(peerA, 1); err != nil {
		t.Fatal(err)
	}
	if c := m.byAddr(peerA); c == nil || !c.encrypted {
		t.Fatal("reconnected link did not restore encryption")
	}
	if err := m.Disconnect(peerA); err != nil {
		t.Fatal(err)
	}
	if err := m.ForgetBond(peerA); err != nil {
		t.Fatal(err)
	}
	forgotten, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(forgotten), peerA) {
		t.Fatalf("forgotten bond remains on disk: %s", forgotten)
	}
	if _, err := m.Connect(peerA, 1); err != nil {
		t.Fatal(err)
	}
	if c := m.byAddr(peerA); c == nil || c.encrypted {
		t.Fatal("forgotten peer unexpectedly restored encryption")
	}
}
