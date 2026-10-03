package taternative

import (
	"crypto/rand"
	"encoding/hex"
)

func newMessageID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// crypto/rand failure is not actionable here; a stable non-empty ID is
		// still enough for a request the server does not need to correlate.
		return "tater-native"
	}
	return hex.EncodeToString(raw[:])
}
