package gpucompositor

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestProtocolRoundTripContract(t *testing.T) {
	source := make([]byte, SourceBytes)
	var input bytes.Buffer
	if err := writeInputFrame(&input, source); err != nil {
		t.Fatal(err)
	}
	if got := input.Next(4); string(got) != "TGI1" {
		t.Fatalf("input magic = %q", got)
	}
	if got := binary.LittleEndian.Uint32(input.Next(4)); got != SourceBytes {
		t.Fatalf("input bytes = %d", got)
	}
	if input.Len() != SourceBytes {
		t.Fatalf("input payload = %d", input.Len())
	}

	stream := make([]byte, streamHeaderSize)
	copy(stream, "TGO1")
	binary.LittleEndian.PutUint32(stream[4:], PanelWidth)
	binary.LittleEndian.PutUint32(stream[8:], PanelHeight)
	binary.LittleEndian.PutUint32(stream[12:], PanelWidth*4)
	binary.LittleEndian.PutUint32(stream[16:], PanelBytes)
	if err := readStreamHeader(bytes.NewReader(stream)); err != nil {
		t.Fatal(err)
	}

	frame := make([]byte, frameHeaderSize+PanelBytes)
	copy(frame, "TGF1")
	binary.LittleEndian.PutUint32(frame[4:], PanelBytes)
	destination := make([]byte, PanelBytes)
	if err := readOutputFrame(bytes.NewReader(frame), destination); err != nil {
		t.Fatal(err)
	}
}

func TestProtocolRejectsMalformedOrUnboundedFrames(t *testing.T) {
	if err := writeInputFrame(&bytes.Buffer{}, make([]byte, SourceBytes-1)); err == nil {
		t.Fatal("short input accepted")
	}
	if err := readStreamHeader(strings.NewReader(strings.Repeat("x", streamHeaderSize))); err == nil {
		t.Fatal("invalid stream header accepted")
	}
	if err := readOutputFrame(strings.NewReader(strings.Repeat("x", frameHeaderSize)), make([]byte, PanelBytes)); err == nil {
		t.Fatal("invalid frame header accepted")
	}
}
