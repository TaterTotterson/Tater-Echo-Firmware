//go:build !cgo || (!android && (!linux || !onnxruntime))

package microwakeword

import "fmt"

func owwORTSupported() bool { return false }

func openOWWORTInferer(_, _, _, _ string) (owwORTInferer, error) {
	return nil, fmt.Errorf("microwakeword: ONNX Runtime OWW requires an Android or Linux CGO build")
}
