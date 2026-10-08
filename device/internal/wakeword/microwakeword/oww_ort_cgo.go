//go:build cgo && (android || (linux && onnxruntime))

package microwakeword

/*
#cgo CFLAGS: -I${SRCDIR}/../../../build/onnxruntime/include -O2
#cgo LDFLAGS: -ldl

#include "ort_shim.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"
)

type owwORTRuntime struct {
	runtime C.tater_ort_runtime
	version string
}

type owwORTModel struct {
	model C.tater_ort_model
	name  string
	out   []float32
}

type owwORTNativeInferer struct {
	runtime       *owwORTRuntime
	melspec       *owwORTModel
	embedding     *owwORTModel
	classifier    *owwORTModel
	xnnpackActive bool
}

var (
	owwORTRuntimesMu sync.Mutex
	owwORTRuntimes   = map[string]*owwORTRuntime{}
)

func owwORTSupported() bool { return true }

func openOWWORTRuntime(path string) (*owwORTRuntime, error) {
	owwORTRuntimesMu.Lock()
	defer owwORTRuntimesMu.Unlock()
	if runtime := owwORTRuntimes[path]; runtime != nil {
		return runtime, nil
	}
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	runtime := &owwORTRuntime{}
	if err := owwORTError(C.tater_ort_open(cPath, &runtime.runtime)); err != nil {
		return nil, fmt.Errorf("microwakeword: open ONNX Runtime %s: %w", path, err)
	}
	runtime.version = C.GoString(C.tater_ort_version(&runtime.runtime))
	owwORTRuntimes[path] = runtime
	return runtime, nil
}

func (runtime *owwORTRuntime) load(path, name string) (*owwORTModel, error) {
	if path == "" {
		return nil, fmt.Errorf("microwakeword: no path for OWW %s model", name)
	}
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	model := &owwORTModel{name: name}
	if err := owwORTError(C.tater_ort_model_load(&runtime.runtime, cPath, &model.model)); err != nil {
		return nil, fmt.Errorf("microwakeword: load OWW %s model %s: %w", name, path, err)
	}
	return model, nil
}

func openOWWORTInferer(runtimePath, melspecPath, embeddingPath, classifierPath string) (owwORTInferer, error) {
	runtime, err := openOWWORTRuntime(runtimePath)
	if err != nil {
		return nil, err
	}
	inferer := &owwORTNativeInferer{runtime: runtime}
	if inferer.melspec, err = runtime.load(melspecPath, "melspectrogram"); err != nil {
		return nil, err
	}
	inferer.xnnpackActive = inferer.melspec.model.xnnpack != 0
	if inferer.embedding, err = runtime.load(embeddingPath, "embedding"); err != nil {
		_ = inferer.Close()
		return nil, err
	}
	if inferer.classifier, err = runtime.load(classifierPath, "classifier"); err != nil {
		_ = inferer.Close()
		return nil, err
	}
	return inferer, nil
}

func (inferer *owwORTNativeInferer) Info() string {
	return fmt.Sprintf("openWakeWord ONNX Runtime %s, xnnpack=%t",
		inferer.runtime.version, inferer.xnnpackActive)
}

func (inferer *owwORTNativeInferer) Close() error {
	for _, model := range []*owwORTModel{inferer.melspec, inferer.embedding, inferer.classifier} {
		if model != nil {
			C.tater_ort_model_free(&model.model)
		}
	}
	inferer.melspec, inferer.embedding, inferer.classifier = nil, nil, nil
	return nil
}

func (inferer *owwORTNativeInferer) Melspectrogram(samples []float32) ([]float32, int, error) {
	if inferer.melspec == nil {
		return nil, 0, errors.New("microwakeword: OWW melspectrogram model is closed")
	}
	if len(samples) == 0 {
		return nil, 0, errors.New("microwakeword: empty OWW melspectrogram input")
	}
	shape := [2]C.int64_t{1, C.int64_t(len(samples))}
	out, err := inferer.run(inferer.melspec, samples, shape[:])
	if err != nil {
		return nil, 0, err
	}
	if len(out)%owwMelBins != 0 {
		return nil, 0, fmt.Errorf("microwakeword: OWW melspectrogram produced %d values", len(out))
	}
	return out, len(out) / owwMelBins, nil
}

func (inferer *owwORTNativeInferer) Embed(window []float32) ([]float32, error) {
	if inferer.embedding == nil {
		return nil, errors.New("microwakeword: OWW embedding model is closed")
	}
	if len(window) != owwMelWindow*owwMelBins {
		return nil, fmt.Errorf("microwakeword: OWW embedding wants %d values, got %d",
			owwMelWindow*owwMelBins, len(window))
	}
	shape := [4]C.int64_t{1, owwMelWindow, owwMelBins, 1}
	out, err := inferer.run(inferer.embedding, window, shape[:])
	if err != nil {
		return nil, err
	}
	if len(out) != owwFeatureDim {
		return nil, fmt.Errorf("microwakeword: OWW embedding produced %d values, want %d",
			len(out), owwFeatureDim)
	}
	return out, nil
}

func (inferer *owwORTNativeInferer) Classify(features []float32) (float32, error) {
	if inferer.classifier == nil {
		return 0, errors.New("microwakeword: OWW classifier is closed")
	}
	if len(features) != owwFeatureWindow*owwFeatureDim {
		return 0, fmt.Errorf("microwakeword: OWW classifier wants %d values, got %d",
			owwFeatureWindow*owwFeatureDim, len(features))
	}
	shape := [3]C.int64_t{1, owwFeatureWindow, owwFeatureDim}
	out, err := inferer.run(inferer.classifier, features, shape[:])
	if err != nil {
		return 0, err
	}
	if len(out) != 1 {
		return 0, fmt.Errorf("microwakeword: OWW classifier produced %d values, want 1", len(out))
	}
	return out[0], nil
}

func (inferer *owwORTNativeInferer) run(model *owwORTModel, input []float32, shape []C.int64_t) ([]float32, error) {
	var output *C.float
	var count C.size_t
	err := owwORTError(C.tater_ort_model_run(
		&model.model,
		(*C.float)(unsafe.Pointer(&input[0])), C.size_t(len(input)),
		&shape[0], C.size_t(len(shape)), &output, &count,
	))
	if err != nil {
		return nil, fmt.Errorf("microwakeword: run OWW %s: %w", model.name, err)
	}
	if output == nil {
		return nil, fmt.Errorf("microwakeword: run OWW %s returned no output", model.name)
	}
	defer C.free(unsafe.Pointer(output))
	model.out = append(model.out[:0], unsafe.Slice((*float32)(unsafe.Pointer(output)), int(count))...)
	return model.out, nil
}

func owwORTError(message *C.char) error {
	if message == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(message))
	return errors.New(C.GoString(message))
}
