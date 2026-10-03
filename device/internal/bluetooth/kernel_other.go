//go:build !linux || android

package bluetooth

import "os"

func openKernelTransport() (*os.File, bool, error) { return nil, false, nil }
