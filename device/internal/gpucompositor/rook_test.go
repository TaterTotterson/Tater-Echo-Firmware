package gpucompositor

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestRookSquareGeometryAndPrivateRuntimeCannotCrossTargets(t *testing.T) {
	g, ok := GeometryForTarget("rook")
	if !ok || g != (Geometry{480, 480, 480, 480}) {
		t.Fatal(g, ok)
	}
	if err := writeInputFrameForGeometry(&bytes.Buffer{}, make([]byte, g.SourceBytes()), g); err != nil {
		t.Fatal(err)
	}
	if writeInputFrame(&bytes.Buffer{}, make([]byte, g.SourceBytes())) == nil {
		t.Fatal("Rook frame accepted as Checkers")
	}
	manifest, _ := json.Marshal(runtimeManifest{Schema: 2, RuntimeID: rookRuntimeID, TreeSHA256: expectedTreeSHA256, FileCount: expectedRuntimeFiles, RuntimeBytes: expectedRuntimeBytes})
	if err := validateManifestForTarget(manifest, "rook"); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"checkers", "cronos", "crown", "athena", "rook-extra"} {
		if validateManifestForTarget(manifest, target) == nil {
			t.Fatal("cross-target runtime accepted", target)
		}
	}
}
