//go:build !linux || android

package bluetooth

import "errors"

// NewBlueZGATTManager is unavailable where there is no system BlueZ daemon.
func NewBlueZGATTManager(func(bool)) (GATTManager, error) {
	return nil, errors.New("ble: BlueZ GATT is unavailable on this platform")
}
