//go:build !cgo || (!darwin && !linux && !android)

package microwakeword

const MinimumNativeArenaSize = 64 * 1024

type NativeRuntime struct{}

type OWWSettings struct {
	Threshold float32
	Patience  int
}

type OWWResult struct {
	Accepted    bool
	Score       float32
	Consecutive int
}

type OWWEngine interface {
	Engine
	Confirm([]int16) (OWWResult, error)
}

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
