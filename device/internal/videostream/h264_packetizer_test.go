package videostream

import (
	"bytes"
	"testing"
)

func TestH264PacketizerPreservesPrefixAcrossChunks(t *testing.T) {
	prefix := []byte{0, 0, 0, 1, 0x67, 1, 2, 3}
	first := []byte{0, 0, 1, 0x09, 0xf0, 0, 0, 1, 0x65, 4, 5}
	second := []byte{0, 0, 0, 1, 0x09, 0xf0, 0, 0, 1, 0x41, 6, 7}
	third := []byte{0, 0, 1, 0x09, 0xf0, 0, 0, 1, 0x41, 8, 9}
	stream := append(append(append([]byte{}, prefix...), first...), second...)
	stream = append(stream, third...)
	packetizer, err := newH264AccessUnitPacketizer(25)
	if err != nil {
		t.Fatal(err)
	}
	type packet struct {
		pts  int64
		data []byte
	}
	var got []packet
	receive := func(pts int64, data []byte) error {
		got = append(got, packet{pts: pts, data: append([]byte(nil), data...)})
		return nil
	}
	for offset := 0; offset < len(stream); {
		end := min(offset+2, len(stream))
		if err := packetizer.Push(stream[offset:end], receive); err != nil {
			t.Fatal(err)
		}
		offset = end
	}
	if err := packetizer.Close(receive); err != nil {
		t.Fatal(err)
	}
	want := [][]byte{append(append([]byte{}, prefix...), first...), second, third}
	if len(got) != len(want) {
		t.Fatalf("received %d access units, want %d", len(got), len(want))
	}
	for index := range want {
		if !bytes.Equal(got[index].data, want[index]) {
			t.Fatalf("access unit %d changed", index)
		}
		if got[index].pts != int64(index)*40_000 {
			t.Fatalf("access unit %d PTS = %d", index, got[index].pts)
		}
	}
}

func TestH264PacketizerRejectsMissingAndOversizedAUDs(t *testing.T) {
	packetizer, _ := newH264AccessUnitPacketizer(30)
	if err := packetizer.Push([]byte{0, 0, 1, 0x65}, func(int64, []byte) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := packetizer.Close(func(int64, []byte) error { return nil }); err == nil {
		t.Fatal("stream without an AUD was accepted")
	}
	packetizer, _ = newH264AccessUnitPacketizer(30)
	oversized := make([]byte, vdecMaximumAccessUnitBytes+1)
	copy(oversized, []byte{0, 0, 1, 0x09, 0xf0})
	if err := packetizer.Push(oversized, func(int64, []byte) error { return nil }); err == nil {
		t.Fatal("oversized access unit was accepted")
	}
}

func TestH264PacketizerRejectsConsecutiveEmptyAUDs(t *testing.T) {
	packetizer, _ := newH264AccessUnitPacketizer(30)
	doubleAUD := []byte{0, 0, 0, 1, 0x09, 0xf0, 0, 0, 1, 0x09, 0xf0, 0, 0, 1, 0x65, 1}
	if err := packetizer.Push(doubleAUD, func(int64, []byte) error { return nil }); err == nil {
		t.Fatal("consecutive AUD-only access unit was accepted")
	}
}
