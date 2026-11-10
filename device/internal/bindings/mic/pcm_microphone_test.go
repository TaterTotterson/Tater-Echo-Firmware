//go:build server

package mic

import (
	"testing"

	pkgmic "github.com/TaterTotterson/Tater-Echo-Firmware/pkg/mic"
)

func TestSlowCaptureSubscriberExposesSequenceGap(t *testing.T) {
	p := &PcmMicrophone{}
	ch := p.SubscribeCapture()
	defer p.UnsubscribeCapture(ch)
	// Saturate just this subscriber; the producer must keep moving.
	for sequence := uint64(1); sequence <= uint64(cap(ch)); sequence++ {
		if drops := p.publishCapture(pkgmic.Capture{Sequence: sequence, Generation: 1}); drops != 0 {
			t.Fatal("premature drop")
		}
	}
	missing := uint64(cap(ch) + 1)
	if drops := p.publishCapture(pkgmic.Capture{Sequence: missing, Generation: 1}); drops != 1 {
		t.Fatalf("drops=%d", drops)
	}
	for len(ch) > 0 {
		<-ch
	}
	p.publishCapture(pkgmic.Capture{Sequence: missing + 1, Generation: 1})
	if next := <-ch; next.Sequence != missing+1 {
		t.Fatalf("drop was hidden: %+v", next)
	}
}
