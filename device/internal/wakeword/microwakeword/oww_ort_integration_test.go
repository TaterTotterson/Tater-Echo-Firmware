//go:build linux && cgo && onnxruntime

package microwakeword

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This test is opt-in because ordinary host tests do not carry the target
// runtime. The Linux ARMv7 build invokes it under QEMU before packaging either
// display satellite, proving the musl library can load all three OWW models.
func TestLinuxARMv7ORTModels(t *testing.T) {
	dir := os.Getenv("TATER_ORT_SMOKE_DIR")
	if dir == "" {
		t.Skip("TATER_ORT_SMOKE_DIR is not set")
	}
	classifier := filepath.Join(dir, defaultOWWONNXFilename)
	engine, err := newORTOWWEngine(dir, classifier, OWWSettings{Threshold: 0.5, Patience: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if !strings.Contains(engine.Info(), "xnnpack=true") {
		t.Fatalf("unexpected runtime configuration: %s", engine.Info())
	}
	scores, err := engine.PushPCM(make([]int16, 17*owwChunkSamples))
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) == 0 {
		t.Fatal("ONNX streaming pipeline produced no score after warm-up")
	}
}
