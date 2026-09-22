//go:build !cgo || (!darwin && !linux && !android)

package microwakeword

const MinimumNativeArenaSize = 64 * 1024

type NativeRuntime struct{}

func OpenNativeRuntime(string) (*NativeRuntime, error) {
	return nil, ErrNativeRuntimeUnavailable
}

func (r *NativeRuntime) Version() string { return "" }

func (r *NativeRuntime) NewEngine([]byte, RuntimeSettings) (Engine, error) {
	return nil, ErrNativeRuntimeUnavailable
}

func OpenNativeEngine(string, string, RuntimeSettings) (Engine, error) {
	return nil, ErrNativeRuntimeUnavailable
}
