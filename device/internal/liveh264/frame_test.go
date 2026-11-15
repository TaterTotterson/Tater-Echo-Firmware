package liveh264

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func fixture(seq uint32, key bool) Frame {
	return Frame{Width: 1280, Height: 720, FPS: 30, Sequence: seq, StreamID: 123, PTSUS: int64(seq) * 33333, Keyframe: key, Data: []byte{0, 0, 0, 1, 0x65, 0x88}}
}
func TestWireRoundTripAndBounds(t *testing.T) {
	f := fixture(2, true)
	var stream bytes.Buffer
	if err := Write(&stream, Encode(f)); err != nil {
		t.Fatal(err)
	}
	got, err := Read(&stream)
	if err != nil || got.Sequence != f.Sequence || got.StreamID != f.StreamID || got.PTSUS != f.PTSUS || !got.Keyframe || !bytes.Equal(got.Data, f.Data) {
		t.Fatalf("round trip: %#v %v", got, err)
	}
	for _, offset := range []int{5, 9, 13, 21, 32, 33} {
		bad := Encode(f)
		if offset == 33 {
			for n := 33; n < 41; n++ {
				bad[n] = 0
			}
		} else {
			bad[offset] = 255
		}
		if _, err := Decode(bad); err == nil {
			t.Fatalf("accepted corrupt field %d", offset)
		}
	}
	var size [4]byte
	binary.LittleEndian.PutUint32(size[:], MaxPayload+HeaderBytes+1)
	if _, err := Read(bytes.NewReader(size[:])); err == nil {
		t.Fatal("accepted oversized input")
	}
}
func TestGapAndEpochWaitForKeyframe(t *testing.T) {
	var gate Gate
	for _, row := range []struct {
		frame         Frame
		accept, reset bool
	}{
		{fixture(0, false), false, false}, {fixture(1, true), true, true},
		{fixture(2, false), true, false}, {fixture(2, false), false, false},
		{fixture(4, false), false, true}, {fixture(5, false), false, false},
		{fixture(6, true), true, true}, {fixture(7, false), true, false},
	} {
		a, r := gate.Accept(row.frame)
		if a != row.accept || r != row.reset {
			t.Fatalf("sequence=%d accept=%t reset=%t", row.frame.Sequence, a, r)
		}
	}
	f := fixture(0, false)
	f.StreamID = 456
	if a, r := gate.Accept(f); a || !r {
		t.Fatal("epoch did not reset")
	}
	f.Keyframe = true
	if a, r := gate.Accept(f); !a || !r {
		t.Fatal("new epoch keyframe rejected")
	}
}

func TestCronosFourByThreeAndGeometryChange(t *testing.T) {
	f := fixture(0, true)
	f.Height = 960
	if got, err := Decode(Encode(f)); err != nil || got.Height != 960 {
		t.Fatal("Cronos geometry rejected", err)
	}
	var gate Gate
	gate.Accept(fixture(0, true))
	f.Sequence, f.PTSUS, f.Keyframe = 1, 33333, false
	if accept, reset := gate.Accept(f); accept || !reset {
		t.Fatal("geometry change must wait for IDR")
	}
	f.Sequence, f.PTSUS, f.Keyframe = 2, 66666, true
	if accept, reset := gate.Accept(f); !accept || !reset {
		t.Fatal("new geometry IDR did not restart decoder")
	}
	for _, height := range []uint32{480, 721, 962, 1200} {
		f.Height = height
		if Encode(f) != nil {
			t.Fatal("unqualified geometry accepted", height)
		}
	}
}

func TestBalancedGeometryIsExplicitAndPreservesPTS(t *testing.T) {
	for _, size := range [][2]uint32{{768, 432}, {640, 480}} {
		f := fixture(1, true)
		f.Width, f.Height = size[0], size[1]
		got, err := Decode(Encode(f))
		if err != nil || got.Width != f.Width || got.Height != f.Height || got.PTSUS != f.PTSUS {
			t.Fatal(got, err)
		}
		var gate Gate
		gate.Accept(fixture(0, true))
		f.Keyframe = false
		if accept, reset := gate.Accept(f); accept || !reset {
			t.Fatal("balanced geometry switch did not wait for a keyframe")
		}
	}
	for _, size := range [][2]uint32{{640, 360}, {768, 480}, {640, 432}, {320, 240}} {
		if QualifiedGeometry(size[0], size[1]) {
			t.Fatal("accepted unqualified geometry", size)
		}
	}
}
