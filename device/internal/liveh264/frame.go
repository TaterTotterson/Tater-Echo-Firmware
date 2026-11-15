// Package liveh264 defines the bounded, timestamped Sat Chat H.264 wire
// contract. Sequence gaps require a fresh independently decodable keyframe;
// predictive frames must never use JPEG's arbitrary-drop semantics.
package liveh264

import (
	"encoding/binary"
	"errors"
	"io"
)

const Prefix = "TSCV2"
const HeaderBytes = 41
const MaxPayload = 1024 * 1024

type Frame struct {
	Width, Height, FPS, Sequence uint32
	StreamID                     uint64
	PTSUS                        int64
	Keyframe                     bool
	Data                         []byte
}

func QualifiedGeometry(width, height uint32) bool {
	return (width == 1280 && (height == 720 || height == 960)) ||
		(width == 768 && height == 432) || (width == 640 && height == 480)
}

func (f Frame) valid() bool {
	return QualifiedGeometry(f.Width, f.Height) && f.FPS == 30 && f.StreamID != 0 &&
		f.PTSUS >= 0 && len(f.Data) >= 5 && len(f.Data) <= MaxPayload &&
		f.Data[0] == 0 && f.Data[1] == 0 && (f.Data[2] == 1 || f.Data[2] == 0 && f.Data[3] == 1)
}

func Encode(f Frame) []byte {
	if !f.valid() {
		return nil
	}
	wire := make([]byte, HeaderBytes+len(f.Data))
	copy(wire, Prefix)
	binary.LittleEndian.PutUint32(wire[5:], f.Width)
	binary.LittleEndian.PutUint32(wire[9:], f.Height)
	binary.LittleEndian.PutUint32(wire[13:], f.FPS)
	binary.LittleEndian.PutUint32(wire[17:], f.Sequence)
	if f.Keyframe {
		binary.LittleEndian.PutUint32(wire[21:], 1)
	}
	binary.LittleEndian.PutUint64(wire[25:], uint64(f.PTSUS))
	binary.LittleEndian.PutUint64(wire[33:], f.StreamID)
	copy(wire[HeaderBytes:], f.Data)
	return wire
}

// Decode aliases wire; callers retaining a message must own its backing bytes.
func Decode(wire []byte) (Frame, error) {
	if len(wire) < HeaderBytes+5 || len(wire) > HeaderBytes+MaxPayload || string(wire[:5]) != Prefix {
		return Frame{}, errors.New("invalid H.264 call envelope")
	}
	flags := binary.LittleEndian.Uint32(wire[21:])
	f := Frame{Width: binary.LittleEndian.Uint32(wire[5:]), Height: binary.LittleEndian.Uint32(wire[9:]),
		FPS: binary.LittleEndian.Uint32(wire[13:]), Sequence: binary.LittleEndian.Uint32(wire[17:]),
		PTSUS: int64(binary.LittleEndian.Uint64(wire[25:])), StreamID: binary.LittleEndian.Uint64(wire[33:]),
		Keyframe: flags == 1, Data: wire[HeaderBytes:]}
	if flags > 1 || !f.valid() {
		return Frame{}, errors.New("invalid H.264 call geometry or timing")
	}
	return f, nil
}

func Write(w io.Writer, wire []byte) error {
	if _, err := Decode(wire); err != nil {
		return err
	}
	var header [4]byte
	binary.LittleEndian.PutUint32(header[:], uint32(len(wire)))
	for _, value := range [][]byte{header[:], wire} {
		for len(value) > 0 {
			n, err := w.Write(value)
			if n > 0 {
				value = value[n:]
			}
			if err != nil {
				return err
			}
			if n == 0 {
				return io.ErrShortWrite
			}
		}
	}
	return nil
}

func Read(r io.Reader) (Frame, error) {
	var h [4]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return Frame{}, err
	}
	size := binary.LittleEndian.Uint32(h[:])
	if size < HeaderBytes+5 || size > HeaderBytes+MaxPayload {
		return Frame{}, errors.New("invalid H.264 call packet size")
	}
	wire := make([]byte, int(size))
	if _, err := io.ReadFull(r, wire); err != nil {
		return Frame{}, err
	}
	return Decode(wire)
}

type Gate struct {
	stream        uint64
	sequence      uint32
	pts           int64
	width, height uint32
	synced        bool
}

// Accept returns whether to decode, and whether the old decoder must be
// discarded. A restart or any missing picture waits for a self-contained IDR.
func (g *Gate) Accept(f Frame) (accept, reset bool) {
	if !f.valid() {
		return false, false
	}
	if g.synced && f.StreamID == g.stream && f.Sequence <= g.sequence {
		return false, false
	}
	reset = g.synced && (f.StreamID != g.stream || f.Sequence != g.sequence+1 || f.PTSUS <= g.pts || f.Width != g.width || f.Height != g.height)
	if reset {
		g.synced = false
	}
	if !g.synced && !f.Keyframe {
		return false, reset
	}
	if !g.synced {
		reset = true
	}
	g.stream, g.sequence, g.pts, g.synced = f.StreamID, f.Sequence, f.PTSUS, true
	g.width, g.height = f.Width, f.Height
	return true, reset
}
