package bluetooth

import (
	"encoding/hex"
	"fmt"
	"strings"
)

const gattCCCD = 0x2902

func uuidFromString(value string) (UUID, error) {
	text := strings.ToLower(strings.TrimSpace(value))
	compact := strings.ReplaceAll(text, "-", "")
	if len(compact) != 32 {
		return nil, fmt.Errorf("ble: invalid UUID %q", value)
	}
	raw, err := hex.DecodeString(compact)
	if err != nil {
		return nil, fmt.Errorf("ble: invalid UUID %q", value)
	}
	// BlueZ always presents canonical 128-bit UUIDs. Preserve the compact ATT
	// 16-bit form for values in the Bluetooth base UUID.
	if strings.HasPrefix(text, "0000") && strings.HasSuffix(text, "-0000-1000-8000-00805f9b34fb") {
		return UUID16(uint16(raw[2])<<8 | uint16(raw[3])), nil
	}
	for left, right := 0, len(raw)-1; left < right; left, right = left+1, right-1 {
		raw[left], raw[right] = raw[right], raw[left]
	}
	return UUID(raw), nil
}

func bluezCharacteristicProperties(flags []string) byte {
	var properties byte
	for _, flag := range flags {
		switch strings.ToLower(strings.TrimSpace(flag)) {
		case "read":
			properties |= 0x02
		case "write-without-response":
			properties |= 0x04
		case "write":
			properties |= 0x08
		case "notify":
			properties |= 0x10
		case "indicate":
			properties |= 0x20
		}
	}
	return properties
}
