package microwakeword

import "errors"

// ErrNativeRuntimeUnavailable is returned on platforms that cannot use the
// dlopen/cgo native runtime boundary.
var ErrNativeRuntimeUnavailable = errors.New("microwakeword: native runtime requires cgo and dlopen")

// Engine is the boundary implemented by the native TFLite Micro runtime.
//
// PushPCM accepts arbitrary-sized chunks of mono 16 kHz S16 PCM and returns
// zero or more model probabilities in chronological order. The implementation
// owns microfrontend overlap and TFLM resource-variable state. It is called
// from a dedicated inference goroutine and is not required to be safe for
// concurrent use.
type Engine interface {
	PushPCM(samples []int16) ([]float32, error)
	Reset() error
	Close() error
	Info() string
}
