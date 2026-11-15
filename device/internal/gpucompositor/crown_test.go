package gpucompositor

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image"
	"testing"
)

func TestCrownProtocolIsGeometryBoundAndCannotCrossTargets(t *testing.T) {
	g, ok := GeometryForTarget("crown")
	if !ok || g != (Geometry{1280, 800, 800, 1280}) {
		t.Fatal(g, ok)
	}
	var out bytes.Buffer
	source := make([]byte, g.SourceBytes())
	if err := writeInputFrameForGeometry(&out, source, g); err != nil {
		t.Fatal(err)
	}
	if int(binary.LittleEndian.Uint32(out.Bytes()[4:])) != len(source) {
		t.Fatal("wrong upload size")
	}
	if writeInputFrame(&bytes.Buffer{}, source) == nil {
		t.Fatal("Crown frame accepted by Checkers protocol")
	}
	video := &VideoFrame{Width: 768, Height: 432, Pixels: make([]byte, 768*432*4), Source: image.Rect(0, 0, 768, 432), Destination: image.Rect(0, 40, 1280, 760), CornerRadius: 22}
	text := &TextFrame{Width: 2048, Height: 1024, Quads: []TextQuad{{Atlas: image.Rect(1100, 740, 1260, 790), X: 1100, Y: 740, Opacity: 1}}}
	if err := writeSceneFrameForGeometry(&bytes.Buffer{}, nil, Effects{Video: video, Text: text}, g); err != nil {
		t.Fatal(err)
	}
	if writeSceneFrame(&bytes.Buffer{}, nil, Effects{Video: video, Text: text}) == nil {
		t.Fatal("Crown video accepted at smaller geometry")
	}
	video.Destination.Max.X++
	if writeSceneFrameForGeometry(&bytes.Buffer{}, nil, Effects{Video: video, Text: text}, g) == nil {
		t.Fatal("out-of-bounds Crown destination")
	}
	header := make([]byte, streamHeaderSize)
	copy(header, "TGO1")
	for i, v := range []int{800, 1280, 3200, g.PanelBytes()} {
		binary.LittleEndian.PutUint32(header[4+i*4:], uint32(v))
	}
	if err := readStreamHeaderForGeometry(bytes.NewReader(header), g); err != nil {
		t.Fatal(err)
	}
	if readStreamHeader(bytes.NewReader(header)) == nil {
		t.Fatal("Crown stream accepted as Checkers")
	}
	manifest, _ := json.Marshal(runtimeManifest{Schema: 2, RuntimeID: crownRuntimeID, TreeSHA256: expectedTreeSHA256, FileCount: expectedRuntimeFiles, RuntimeBytes: expectedRuntimeBytes})
	if err := validateManifestForTarget(manifest, "crown"); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"checkers", "cronos", "athena", "rook"} {
		if validateManifestForTarget(manifest, target) == nil {
			t.Fatal("cross-target runtime accepted", target)
		}
	}
}
