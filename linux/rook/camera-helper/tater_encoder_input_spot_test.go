//go:build spot

package camera

import (
	"bytes"
	"testing"
)

func TestRookEncoderInputOwnsItsFrameAndRetainsColorTables(t *testing.T) {
	f := &Frame{raw: make([]byte, 640*480), toned: true, tone: tone{gainR: 1, gainB: 1, white: 255}}
	for i := range f.raw {
		f.raw[i] = byte(i)
	}
	var input TaterEncoderInput
	first := input.Prepare(f, nil, true)
	if len(first) != 3072+640*480 || !bytes.Equal(first[3072:], f.raw) {
		t.Fatal("invalid frame contract")
	}
	tables := bytes.Clone(first[:3072])
	f.raw[0] ^= 255
	if first[3072] == f.raw[0] {
		t.Fatal("encoder shares mutable camera storage")
	}
	second := input.Prepare(f, first, false)
	if &second[0] != &first[0] || !bytes.Equal(second[:3072], tables) || second[3072] != f.raw[0] {
		t.Fatal("reuse changed color tables or frame ownership")
	}
	if input.Prepare(&Frame{}, first, true) != nil {
		t.Fatal("empty camera frame accepted")
	}
}
