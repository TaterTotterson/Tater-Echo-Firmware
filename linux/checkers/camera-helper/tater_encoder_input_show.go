//go:build !spot && !dot && !panel

package camera

import "github.com/HuskerMinion/techo5/echod/internal/layout"

// Each platform owns its sensor-frame format; the shared HTTP/codec client
// does not need unrelated camera methods or a second capture implementation.
type TaterEncoderInput struct{ athena TaterAthenaVideo }

func (p *TaterEncoderInput) Prepare(f *Frame, reuse []byte, refreshTone bool) []byte {
	if layout.Board == "athena" {
		return p.athena.NV12(f, reuse, refreshTone)
	}
	return f.TaterLiveBayer10(reuse, refreshTone)
}
