package videostream

import "errors"

type h264AccessUnitPacketizer struct {
	buffer   []byte
	fps      int
	sequence int64
}

func newH264AccessUnitPacketizer(fps int) (*h264AccessUnitPacketizer, error) {
	if fps < 1 || fps > 120 {
		return nil, errors.New("invalid hardware-decoder frame rate")
	}
	return &h264AccessUnitPacketizer{fps: fps}, nil
}

func h264AUDOffsets(value []byte) []int {
	var offsets []int
	for offset := 0; offset+4 <= len(value); offset++ {
		prefix := 0
		switch {
		case offset+5 <= len(value) && value[offset] == 0 && value[offset+1] == 0 && value[offset+2] == 0 && value[offset+3] == 1:
			prefix = 4
		case value[offset] == 0 && value[offset+1] == 0 && value[offset+2] == 1:
			prefix = 3
		}
		if prefix != 0 && value[offset+prefix]&0x1f == 9 {
			offsets = append(offsets, offset)
			offset += prefix
		}
	}
	return offsets
}

func h264AccessUnitHasVCL(value []byte) bool {
	for offset := 0; offset+4 <= len(value); offset++ {
		prefix := 0
		switch {
		case offset+5 <= len(value) && value[offset] == 0 && value[offset+1] == 0 && value[offset+2] == 0 && value[offset+3] == 1:
			prefix = 4
		case value[offset] == 0 && value[offset+1] == 0 && value[offset+2] == 1:
			prefix = 3
		}
		if prefix != 0 {
			nalType := value[offset+prefix] & 0x1f
			if nalType >= 1 && nalType <= 5 {
				return true
			}
			offset += prefix
		}
	}
	return false
}

func (packetizer *h264AccessUnitPacketizer) emit(accessUnit []byte, receive func(int64, []byte) error) error {
	if len(accessUnit) == 0 || len(accessUnit) > vdecMaximumAccessUnitBytes || !h264AccessUnitHasVCL(accessUnit) {
		return errors.New("hardware-decoder access unit is invalid or exceeds its boundary")
	}
	ptsUS := packetizer.sequence * 1_000_000 / int64(packetizer.fps)
	if err := receive(ptsUS, accessUnit); err != nil {
		return err
	}
	packetizer.sequence++
	return nil
}

// Push accepts arbitrary elementary-stream chunks. FFmpeg inserts an AUD
// before every picture; the bytes before the first AUD (normally SPS/PPS) stay
// attached to the first access unit.
func (packetizer *h264AccessUnitPacketizer) Push(chunk []byte, receive func(int64, []byte) error) error {
	if packetizer == nil || receive == nil {
		return errors.New("hardware-decoder packetizer is unavailable")
	}
	if len(chunk) != 0 {
		packetizer.buffer = append(packetizer.buffer, chunk...)
	}
	for {
		offsets := h264AUDOffsets(packetizer.buffer)
		if len(offsets) < 2 {
			if len(packetizer.buffer) > vdecMaximumAccessUnitBytes {
				return errors.New("hardware-decoder access unit exceeds its boundary")
			}
			return nil
		}
		cut := offsets[1]
		if err := packetizer.emit(packetizer.buffer[:cut], receive); err != nil {
			return err
		}
		copy(packetizer.buffer, packetizer.buffer[cut:])
		packetizer.buffer = packetizer.buffer[:len(packetizer.buffer)-cut]
	}
}

func (packetizer *h264AccessUnitPacketizer) Close(receive func(int64, []byte) error) error {
	if packetizer == nil || receive == nil {
		return errors.New("hardware-decoder packetizer is unavailable")
	}
	if len(h264AUDOffsets(packetizer.buffer)) != 1 {
		return errors.New("hardware-decoder stream ended without a complete AUD-framed access unit")
	}
	if err := packetizer.emit(packetizer.buffer, receive); err != nil {
		return err
	}
	packetizer.buffer = nil
	return nil
}
