package gpucompositor

import (
	"bytes"
	"encoding/binary"
	"image"
	"math"
	"testing"
)

func TestRetainedSceneCommandAndUpload(t *testing.T) {
	var output bytes.Buffer
	effects := Effects{Blend: .5, Seconds: 12, Level: .7, Red: 1, Rim: true, Ambient: true}
	if err := writeSceneFrame(&output, nil, effects); err != nil {
		t.Fatal(err)
	}
	header := output.Bytes()
	if len(header) != 40 || string(header[:4]) != "TGS2" || binary.LittleEndian.Uint32(header[4:]) != 0 ||
		math.Float32frombits(binary.LittleEndian.Uint32(header[8:])) != .5 || binary.LittleEndian.Uint32(header[32:]) != 3 {
		t.Fatalf("invalid retained scene command: %x", header)
	}
	output.Reset()
	source := bytes.Repeat([]byte{1, 2, 3, 255}, SourceWidth*SourceHeight)
	if err := writeSceneFrame(&output, source, effects); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 40+SourceBytes || !bytes.Equal(output.Bytes()[40:], source) {
		t.Fatal("scene upload did not preserve source bytes")
	}
}

func TestTextSceneEncodesRetainedAtlasAndQuads(t *testing.T) {
	var output bytes.Buffer
	text := &TextFrame{Width: 16, Height: 8, Pixels: make([]byte, 16*8*4),
		Quads: []TextQuad{{Atlas: image.Rect(1, 2, 9, 6), X: 20.5, Y: 30.25, Opacity: .5}}}
	if err := writeSceneFrame(&output, nil, Effects{Blend: 1, Text: text}); err != nil {
		t.Fatal(err)
	}
	value := output.Bytes()
	if string(value[:4]) != "TGS3" || len(value) != 56+16*8*4+20 ||
		binary.LittleEndian.Uint32(value[36:]) != 16*8*4 || binary.LittleEndian.Uint32(value[48:]) != 1 {
		t.Fatal("invalid text stream header")
	}
	quad := value[56+16*8*4:]
	if binary.LittleEndian.Uint16(quad[4:]) != 8 || math.Float32frombits(binary.LittleEndian.Uint32(quad[8:])) != 20.5 {
		t.Fatal("text quad geometry did not survive encoding")
	}
	text.Pixels = nil
	output.Reset()
	if err := writeSceneFrame(&output, nil, Effects{Blend: 1, Text: text}); err != nil || output.Len() != 56+20 {
		t.Fatal("retained glyph frame retransmitted the atlas")
	}
	text.Quads[0].Atlas.Max.X = 17
	output.Reset()
	if err := writeSceneFrame(&output, nil, Effects{Blend: 1, Text: text}); err == nil || output.Len() != 0 {
		t.Fatal("out-of-atlas glyph was not rejected before writing")
	}
}

func TestVideoSceneEncodesSourceGeometryAndRetainedControls(t *testing.T) {
	var output bytes.Buffer
	video := &VideoFrame{Width: 640, Height: 480, Pixels: make([]byte, 640*480*4),
		Source: image.Rect(0, 40, 640, 440), Destination: image.Rect(160, 0, 800, 400), CornerRadius: 20}
	text := &TextFrame{Width: 64, Height: 32, Pixels: make([]byte, 64*32*4),
		Quads: []TextQuad{{Atlas: image.Rect(2, 4, 18, 20), X: 40, Y: 30, Opacity: 1}}}
	if err := writeSceneFrame(&output, nil, Effects{Video: video, Text: text}); err != nil {
		t.Fatal(err)
	}
	value := output.Bytes()
	if string(value[:4]) != "TGV4" || binary.LittleEndian.Uint32(value[4:]) != 640*480*4 ||
		binary.LittleEndian.Uint32(value[8:]) != 640 || binary.LittleEndian.Uint32(value[16:]) != 0 ||
		binary.LittleEndian.Uint32(value[20:]) != 40 || binary.LittleEndian.Uint32(value[32:]) != 160 ||
		binary.LittleEndian.Uint32(value[44:]) != 400 || binary.LittleEndian.Uint32(value[48:]) != 20 ||
		len(value) != 72+640*480*4+64*32*4+20 {
		t.Fatal("invalid GPU video stream frame")
	}
	video.Pixels, text.Pixels = nil, nil
	output.Reset()
	if err := writeSceneFrame(&output, nil, Effects{Video: video, Text: text}); err != nil || output.Len() != 72+20 {
		t.Fatal("retained GPU video scene retransmitted pixels")
	}
	video.Destination.Max.X = SourceWidth + 1
	output.Reset()
	if err := writeSceneFrame(&output, nil, Effects{Video: video, Text: text}); err == nil || output.Len() != 0 {
		t.Fatal("out-of-bounds GPU video destination was accepted")
	}
}

func TestRetainedSceneRejectsMalformedInputBeforeWriting(t *testing.T) {
	for _, effects := range []Effects{
		{Blend: float32(math.NaN())}, {Blend: 2}, {Seconds: float32(math.Inf(1))},
		{Seconds: -1}, {Level: -1}, {Red: 2},
	} {
		var output bytes.Buffer
		if err := writeSceneFrame(&output, nil, effects); err == nil || output.Len() != 0 {
			t.Fatalf("malformed scene reached helper: %+v", effects)
		}
	}
	var output bytes.Buffer
	if err := writeSceneFrame(&output, []byte{1}, Effects{}); err == nil {
		t.Fatal("partial source frame accepted")
	}
}
