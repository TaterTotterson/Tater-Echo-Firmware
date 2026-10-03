//go:build cgo && (darwin || linux || android)

package microwakeword

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestNativeEngineIntegration(t *testing.T) {
	library := os.Getenv("TATER_MWW_LIBRARY")
	modelPath := os.Getenv("TATER_MWW_MODEL")
	if library == "" || modelPath == "" {
		t.Skip("set TATER_MWW_LIBRARY and TATER_MWW_MODEL to run native integration")
	}

	manifest := mustManifest(t)
	model, err := os.ReadFile(modelPath)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := OpenNativeRuntime(library)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(runtime.Version(), "tflm.2747abd") {
		t.Fatalf("unexpected runtime version %q", runtime.Version())
	}
	if _, err := runtime.NewEngine([]byte("not a flatbuffer"), manifest.RuntimeConfig()); err == nil {
		t.Fatal("invalid model unexpectedly created a native engine")
	}
	engine, err := runtime.NewEngine(model, manifest.RuntimeConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if !strings.Contains(engine.Info(), "stride=2") {
		t.Fatalf("unexpected engine info %q", engine.Info())
	}

	pcm := make([]int16, 32000)
	first, err := engine.PushPCM(pcm)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) == 0 {
		t.Fatal("two seconds of PCM produced no scores")
	}
	for i, score := range first {
		if score < 0 || score > 1 {
			t.Fatalf("score %d = %f, want [0,1]", i, score)
		}
	}
	if err := engine.Reset(); err != nil {
		t.Fatal(err)
	}
	second, err := engine.PushPCM(pcm)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(first, second) {
		t.Fatal("reset did not restore deterministic frontend/model state")
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.PushPCM(pcm); err == nil {
		t.Fatal("PushPCM on a closed engine unexpectedly succeeded")
	}
	if err := engine.Reset(); err == nil {
		t.Fatal("Reset on a closed engine unexpectedly succeeded")
	}
}

func TestOpenNativeRuntimeRejectsMissingLibrary(t *testing.T) {
	_, err := OpenNativeRuntime(t.TempDir() + "/missing-runtime.so")
	if err == nil || !strings.Contains(err.Error(), "load native runtime") {
		t.Fatalf("OpenNativeRuntime error = %v", err)
	}
}
