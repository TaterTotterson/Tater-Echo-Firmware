package videostream

import (
	"bytes"
	"encoding/hex"
	"testing"
)

type h264BitWriter struct {
	value []byte
	bit   int
}

func (writer *h264BitWriter) bits(value uint32, count int) {
	for index := count - 1; index >= 0; index-- {
		if writer.bit%8 == 0 {
			writer.value = append(writer.value, 0)
		}
		writer.value[len(writer.value)-1] |= byte(value>>index&1) << (7 - writer.bit%8)
		writer.bit++
	}
}

func (writer *h264BitWriter) ue(value uint32) {
	code := value + 1
	bits := 0
	for current := code; current != 0; current >>= 1 {
		bits++
	}
	writer.bits(0, bits-1)
	writer.bits(code, bits)
}

func testSPS(profile, widthMbs, heightMapUnits, frameMbsOnly, cropBottom uint32) []byte {
	var writer h264BitWriter
	writer.bits(profile, 8)
	writer.bits(0, 8)
	writer.bits(31, 8)
	writer.ue(0)
	if profile == 100 {
		writer.ue(1)
		writer.ue(0)
		writer.ue(0)
		writer.bits(0, 1)
		writer.bits(0, 1)
	}
	writer.ue(0)
	writer.ue(0)
	writer.ue(0)
	writer.ue(1)
	writer.bits(0, 1)
	writer.ue(widthMbs - 1)
	writer.ue(heightMapUnits - 1)
	writer.bits(frameMbsOnly, 1)
	if frameMbsOnly == 0 {
		writer.bits(0, 1)
	}
	writer.bits(1, 1)
	if cropBottom != 0 {
		writer.bits(1, 1)
		writer.ue(0)
		writer.ue(0)
		writer.ue(0)
		writer.ue(cropBottom)
	} else {
		writer.bits(0, 1)
	}
	writer.bits(0, 1)
	writer.bits(1, 1)
	return writer.value
}

func TestH264SPSParsesDisplayGeometry(t *testing.T) {
	for _, test := range []struct {
		name                string
		profile             uint32
		widthMbs, heightMbs uint32
		cropBottom          uint32
		width, height       int
	}{
		{name: "baseline-960x480", profile: 66, widthMbs: 60, heightMbs: 30, width: 960, height: 480},
		{name: "high-cropped-1080p", profile: 100, widthMbs: 120, heightMbs: 68, cropBottom: 4, width: 1920, height: 1080},
	} {
		t.Run(test.name, func(t *testing.T) {
			sps := testSPS(test.profile, test.widthMbs, test.heightMbs, 1, test.cropBottom)
			accessUnit := append([]byte{0, 0, 0, 1, 0x09, 0xf0, 0, 0, 1, 0x67}, sps...)
			accessUnit = append(accessUnit, 0, 0, 1, 0x65, 1, 2, 3)
			width, height, err := h264DimensionsFromAccessUnit(accessUnit)
			if err != nil {
				t.Fatal(err)
			}
			if width != test.width || height != test.height {
				t.Fatalf("geometry = %dx%d, want %dx%d", width, height, test.width, test.height)
			}
		})
	}
}

func TestH264SPSRejectsMissingAndOversizedGeometry(t *testing.T) {
	if _, _, err := h264DimensionsFromAccessUnit([]byte{0, 0, 1, 0x09, 0xf0, 0, 0, 1, 0x65, 1}); err == nil {
		t.Fatal("access unit without SPS was accepted")
	}
	sps := testSPS(66, 121, 68, 1, 0)
	accessUnit := append([]byte{0, 0, 1, 0x67}, sps...)
	accessUnit = append(accessUnit, 0, 0, 1, 0x65, 1)
	if _, _, err := h264DimensionsFromAccessUnit(accessUnit); err == nil {
		t.Fatal("oversized SPS was accepted")
	}
}

func TestH264SPSPictureOrderModeTwo(t *testing.T) {
	// Generated baseline x264 fixture: POC type 2 has no extra POC fields.
	sps, err := hex.DecodeString("42c01ed900a03db0110000030001000003003c8f162e48")
	if err != nil {
		t.Fatal(err)
	}
	width, height, err := parseH264SPS(sps)
	if err != nil || width != 640 || height != 480 {
		t.Fatalf("geometry = %dx%d, error = %v; want 640x480", width, height, err)
	}
}

func TestH264RBSPRemovesOnlyEmulationPreventionByte(t *testing.T) {
	encoded := []byte{1, 0, 0, 3, 0, 2, 0, 0, 3, 3, 4}
	want := []byte{1, 0, 0, 0, 2, 0, 0, 3, 4}
	if got := h264RBSP(encoded); !bytes.Equal(got, want) {
		t.Fatalf("RBSP = %x, want %x", got, want)
	}
}
