//go:build cgo && (darwin || linux || android)

package microwakeword

/*
#cgo CFLAGS: -I${SRCDIR}
#cgo LDFLAGS: -ldl

#include "native_shim.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"unsafe"
)

const MinimumNativeArenaSize = 64 * 1024

// NativeRuntime is one dlopen'd libtater_microwakeword. Runtime handles are
// cached for process lifetime because unloading a C++ runtime while any engine
// might still exist is more dangerous than retaining one small library handle.
type NativeRuntime struct {
	runtime C.em_mww_runtime
	version string
}

var (
	nativeRuntimeMu sync.Mutex
	nativeRuntimes  = map[string]*NativeRuntime{}
)

// OpenNativeRuntime loads and ABI-checks a native runtime shared library.
func OpenNativeRuntime(path string) (*NativeRuntime, error) {
	if path == "" {
		return nil, errors.New("microwakeword: native runtime path is required")
	}
	nativeRuntimeMu.Lock()
	defer nativeRuntimeMu.Unlock()
	if runtime := nativeRuntimes[path]; runtime != nil {
		return runtime, nil
	}

	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	runtime := &NativeRuntime{}
	if err := nativeError(C.em_mww_open(cPath, &runtime.runtime)); err != nil {
		return nil, fmt.Errorf("microwakeword: load native runtime %s: %w", path, err)
	}
	runtime.version = C.GoString(C.em_mww_version(&runtime.runtime))
	nativeRuntimes[path] = runtime
	return runtime, nil
}

// Version returns the native runtime build version.
func (r *NativeRuntime) Version() string { return r.version }

// NewEngine creates a model instance. The native side copies model before the
// call returns, so the caller may release or reuse the byte slice immediately.
func (r *NativeRuntime) NewEngine(model []byte, settings RuntimeSettings) (Engine, error) {
	if len(model) == 0 {
		return nil, errors.New("microwakeword: model is empty")
	}
	arenaSize := settings.TensorArenaSize
	if arenaSize < MinimumNativeArenaSize {
		arenaSize = MinimumNativeArenaSize
	}
	frontend := settings.Frontend
	config := C.tater_mww_config{
		sample_rate:          C.int32_t(frontend.SampleRate),
		feature_duration_ms:  C.int32_t(frontend.FeatureDurationMS),
		feature_step_ms:      C.int32_t(frontend.FeatureStepMS),
		feature_size:         C.int32_t(frontend.FeatureSize),
		input_feature_frames: C.int32_t(frontend.InputFeatureFrames),
		lower_band_limit:     C.float(frontend.LowerBandLimit),
		upper_band_limit:     C.float(frontend.UpperBandLimit),
		tensor_arena_size:    C.size_t(arenaSize),
	}
	var handle unsafe.Pointer
	if err := nativeError(C.em_mww_create(
		&r.runtime,
		(*C.uint8_t)(unsafe.Pointer(&model[0])),
		C.size_t(len(model)),
		&config,
		&handle,
	)); err != nil {
		return nil, fmt.Errorf("microwakeword: create native engine: %w", err)
	}
	engine := &nativeEngine{runtime: r, handle: handle}
	engine.info = C.GoString(C.em_mww_info(&r.runtime, handle))
	engine.arenaUsed = int(C.em_mww_arena_used(&r.runtime, handle))
	engine.inputStride = int(C.em_mww_input_stride(&r.runtime, handle))
	return engine, nil
}

// OpenNativeEngine loads modelPath and creates an engine from runtimePath.
func OpenNativeEngine(runtimePath, modelPath string, settings RuntimeSettings) (Engine, error) {
	runtime, err := OpenNativeRuntime(runtimePath)
	if err != nil {
		return nil, err
	}
	model, err := os.ReadFile(modelPath)
	if err != nil {
		return nil, fmt.Errorf("microwakeword: read model %s: %w", modelPath, err)
	}
	return runtime.NewEngine(model, settings)
}

type nativeEngine struct {
	runtime     *NativeRuntime
	handle      unsafe.Pointer
	info        string
	arenaUsed   int
	inputStride int
}

func (e *nativeEngine) PushPCM(samples []int16) ([]float32, error) {
	if e.handle == nil {
		return nil, errors.New("microwakeword: native engine is closed")
	}
	if len(samples) == 0 {
		return nil, nil
	}
	// One microfrontend frame can arrive per 10ms (160 samples). The model
	// currently consumes two at a time, but sizing for one per frame also covers
	// buffered overlap at the start of a call without relying on that detail.
	capacity := len(samples)/160 + 4
	scores := make([]float32, capacity)
	var count C.size_t
	if err := nativeError(C.em_mww_process(
		&e.runtime.runtime,
		e.handle,
		(*C.int16_t)(unsafe.Pointer(&samples[0])),
		C.size_t(len(samples)),
		(*C.float)(unsafe.Pointer(&scores[0])),
		C.size_t(len(scores)),
		&count,
	)); err != nil {
		return nil, fmt.Errorf("microwakeword: process PCM: %w", err)
	}
	return scores[:int(count)], nil
}

func (e *nativeEngine) Reset() error {
	if e.handle == nil {
		return errors.New("microwakeword: native engine is closed")
	}
	if err := nativeError(C.em_mww_reset(&e.runtime.runtime, e.handle)); err != nil {
		return fmt.Errorf("microwakeword: reset native engine: %w", err)
	}
	return nil
}

func (e *nativeEngine) Close() error {
	if e.handle != nil {
		C.em_mww_destroy(&e.runtime.runtime, e.handle)
		e.handle = nil
	}
	return nil
}

func (e *nativeEngine) Info() string { return e.info }

// ArenaUsed reports the bytes actually reserved by TFLM after tensor
// allocation. It is useful for setting a safe production arena with headroom.
func (e *nativeEngine) ArenaUsed() int { return e.arenaUsed }

// InputStride reports the feature-frame count read from the model input.
func (e *nativeEngine) InputStride() int { return e.inputStride }

func nativeError(message *C.char) error {
	if message == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(message))
	return errors.New(C.GoString(message))
}
