package videostream

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	vdecProtocolVersion        = 2
	vdecStreamHeaderBytes      = 32
	vdecRecordHeaderBytes      = 16
	vdecInputRecordHeaderBytes = 12
	vdecMaximumAccessUnitBytes = 4 * 1024 * 1024
	vdecMaximumWidth           = 1920
	vdecMaximumHeight          = 1088
	vdecOutputFlagEOS          = 1
)

type vdecStreamHeader struct {
	Width, Height                int
	FrameBytes                   int
	FPSNumerator, FPSDenominator int
}

func writeAll(writer io.Writer, value []byte) error {
	for len(value) != 0 {
		written, err := writer.Write(value)
		if written > 0 {
			value = value[written:]
		}
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

// writeVDECAccessUnit writes one protocol-v2 Annex-B access unit. ptsUS is the
// presentation timestamp carried through OMX and returned beside its picture.
func writeVDECAccessUnit(writer io.Writer, ptsUS int64, accessUnit []byte) error {
	if writer == nil || ptsUS < 0 || len(accessUnit) == 0 || len(accessUnit) > vdecMaximumAccessUnitBytes {
		return errors.New("invalid hardware-decoder access unit")
	}
	header := make([]byte, vdecInputRecordHeaderBytes)
	binary.LittleEndian.PutUint32(header, uint32(len(accessUnit)))
	binary.LittleEndian.PutUint64(header[4:], uint64(ptsUS))
	if err := writeAll(writer, header); err != nil {
		return err
	}
	return writeAll(writer, accessUnit)
}

func writeVDECInputEOS(writer io.Writer) error {
	if writer == nil {
		return errors.New("hardware-decoder input is unavailable")
	}
	return writeAll(writer, make([]byte, vdecInputRecordHeaderBytes))
}

func readVDECStreamHeader(reader io.Reader) (vdecStreamHeader, error) {
	var result vdecStreamHeader
	wire := make([]byte, vdecStreamHeaderBytes)
	if reader == nil {
		return result, errors.New("hardware-decoder output is unavailable")
	}
	if _, err := io.ReadFull(reader, wire); err != nil {
		return result, err
	}
	result = vdecStreamHeader{
		Width:          int(binary.LittleEndian.Uint32(wire[8:12])),
		Height:         int(binary.LittleEndian.Uint32(wire[12:16])),
		FrameBytes:     int(binary.LittleEndian.Uint32(wire[16:20])),
		FPSNumerator:   int(binary.LittleEndian.Uint32(wire[24:28])),
		FPSDenominator: int(binary.LittleEndian.Uint32(wire[28:32])),
	}
	if string(wire[:4]) != "TVO2" || binary.LittleEndian.Uint32(wire[4:8]) != vdecStreamHeaderBytes ||
		string(wire[20:24]) != "RGBA" || result.Width < 16 || result.Width > vdecMaximumWidth ||
		result.Height < 16 || result.Height > vdecMaximumHeight || result.Width&1 != 0 || result.Height&1 != 0 ||
		result.FrameBytes != result.Width*result.Height*4 || result.FPSNumerator < 1 || result.FPSNumerator > 120 ||
		result.FPSDenominator != 1 {
		return vdecStreamHeader{}, errors.New("invalid hardware-decoder stream header")
	}
	return result, nil
}

// readVDECFrame reads one framed RGBA picture into destination. A clean EOS
// record returns eos=true without reading or changing destination.
func readVDECFrame(reader io.Reader, stream vdecStreamHeader, destination []byte) (ptsUS int64, eos bool, err error) {
	header := make([]byte, vdecRecordHeaderBytes)
	if reader == nil {
		return 0, false, errors.New("hardware-decoder output is unavailable")
	}
	if _, err := io.ReadFull(reader, header); err != nil {
		return 0, false, err
	}
	payloadBytes := int(binary.LittleEndian.Uint32(header[:4]))
	flags := binary.LittleEndian.Uint32(header[4:8])
	encodedPTS := binary.LittleEndian.Uint64(header[8:16])
	if encodedPTS > uint64(^uint64(0)>>1) {
		return 0, false, errors.New("invalid hardware-decoder timestamp")
	}
	ptsUS = int64(encodedPTS)
	if payloadBytes == 0 {
		if flags != vdecOutputFlagEOS {
			return 0, false, errors.New("invalid hardware-decoder terminator")
		}
		return ptsUS, true, nil
	}
	if flags != 0 || payloadBytes != stream.FrameBytes || len(destination) != stream.FrameBytes {
		return 0, false, fmt.Errorf("invalid hardware-decoder frame contract: bytes=%d flags=%d", payloadBytes, flags)
	}
	if _, err := io.ReadFull(reader, destination); err != nil {
		return 0, false, err
	}
	return ptsUS, false, nil
}
