package taternative

import (
	"encoding/binary"
	"testing"
)

func TestDecodeWAVDownmixesAndResamples(t *testing.T) {
	// Two stereo frames at 24 kHz become four mono frames at 48 kHz.
	data := make([]byte, 8)
	binary.LittleEndian.PutUint16(data[0:], uint16(int16(1000)))
	binary.LittleEndian.PutUint16(data[2:], uint16(int16(3000)))
	negative := int16(-1000)
	binary.LittleEndian.PutUint16(data[4:], uint16(negative))
	binary.LittleEndian.PutUint16(data[6:], uint16(int16(1000)))
	wav := make([]byte, 44+len(data))
	copy(wav[0:], "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 2)
	binary.LittleEndian.PutUint32(wav[24:], 24000)
	binary.LittleEndian.PutUint32(wav[28:], 24000*4)
	binary.LittleEndian.PutUint16(wav[32:], 4)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], uint32(len(data)))
	copy(wav[44:], data)
	pcm, err := decodeAudio(wav, "audio/wav", "http://tater/test.wav")
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm) != 8 {
		t.Fatalf("decoded bytes = %d, want 8", len(pcm))
	}
	if got := int16(binary.LittleEndian.Uint16(pcm)); got != 2000 {
		t.Fatalf("first downmixed sample = %d, want 2000", got)
	}
}
