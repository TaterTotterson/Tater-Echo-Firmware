package videostream

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestVDECInputProtocolCarriesTimestampAndEOS(t *testing.T) {
	var wire bytes.Buffer
	accessUnit := []byte{0, 0, 0, 1, 9, 0xf0, 0, 0, 1, 0x65}
	if err := writeVDECAccessUnit(&wire, 33_333, accessUnit); err != nil {
		t.Fatal(err)
	}
	if err := writeVDECInputEOS(&wire); err != nil {
		t.Fatal(err)
	}
	encoded := wire.Bytes()
	if got := binary.LittleEndian.Uint32(encoded[:4]); got != uint32(len(accessUnit)) {
		t.Fatalf("access-unit bytes = %d", got)
	}
	if got := binary.LittleEndian.Uint64(encoded[4:12]); got != 33_333 {
		t.Fatalf("PTS = %d", got)
	}
	if !bytes.Equal(encoded[12:12+len(accessUnit)], accessUnit) {
		t.Fatal("access unit changed on the wire")
	}
	if !bytes.Equal(encoded[12+len(accessUnit):], make([]byte, vdecInputRecordHeaderBytes)) {
		t.Fatal("input EOS record is not all zero")
	}
}

func TestVDECOutputProtocolReadsNegotiatedFrames(t *testing.T) {
	const width, height = 16, 16
	frameBytes := width * height * 4
	var wire bytes.Buffer
	header := make([]byte, vdecStreamHeaderBytes)
	copy(header, "TVO2")
	binary.LittleEndian.PutUint32(header[4:], vdecStreamHeaderBytes)
	binary.LittleEndian.PutUint32(header[8:], width)
	binary.LittleEndian.PutUint32(header[12:], height)
	binary.LittleEndian.PutUint32(header[16:], uint32(frameBytes))
	copy(header[20:], "RGBA")
	binary.LittleEndian.PutUint32(header[24:], 30)
	binary.LittleEndian.PutUint32(header[28:], 1)
	wire.Write(header)
	record := make([]byte, vdecRecordHeaderBytes)
	binary.LittleEndian.PutUint32(record, uint32(frameBytes))
	binary.LittleEndian.PutUint64(record[8:], 66_666)
	wire.Write(record)
	wire.Write(bytes.Repeat([]byte{0x5a}, frameBytes))
	binary.LittleEndian.PutUint32(record, 0)
	binary.LittleEndian.PutUint32(record[4:], vdecOutputFlagEOS)
	binary.LittleEndian.PutUint64(record[8:], 0)
	wire.Write(record)

	stream, err := readVDECStreamHeader(&wire)
	if err != nil {
		t.Fatal(err)
	}
	if stream.Width != width || stream.Height != height || stream.FrameBytes != frameBytes {
		t.Fatalf("unexpected negotiated stream: %#v", stream)
	}
	pixels := make([]byte, frameBytes)
	pts, eos, err := readVDECFrame(&wire, stream, pixels)
	if err != nil || eos || pts != 66_666 || !bytes.Equal(pixels, bytes.Repeat([]byte{0x5a}, frameBytes)) {
		t.Fatalf("frame pts=%d eos=%v err=%v", pts, eos, err)
	}
	if _, eos, err = readVDECFrame(&wire, stream, pixels); err != nil || !eos {
		t.Fatalf("EOS eos=%v err=%v", eos, err)
	}
}

func TestVDECProtocolRejectsMalformedContracts(t *testing.T) {
	if err := writeVDECAccessUnit(&bytes.Buffer{}, -1, []byte{1}); err == nil {
		t.Fatal("negative input timestamp was accepted")
	}
	if err := writeVDECAccessUnit(&bytes.Buffer{}, 0, make([]byte, vdecMaximumAccessUnitBytes+1)); err == nil {
		t.Fatal("oversized access unit was accepted")
	}
	if _, err := readVDECStreamHeader(strings.NewReader(strings.Repeat("\x00", vdecStreamHeaderBytes))); err == nil {
		t.Fatal("invalid output header was accepted")
	}
}
