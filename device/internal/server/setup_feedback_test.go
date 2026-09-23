package server

import (
	"testing"

	"github.com/TaterTotterson/Tater-Echo-Firmware/pkg/led"
)

type setupLEDController struct{ frame []led.Led }

func (c *setupLEDController) Init() error              { return nil }
func (c *setupLEDController) GetNumLEDs() (int, error) { return 12, nil }
func (c *setupLEDController) SetLEDs(values ...led.Led) error {
	c.frame = append([]led.Led(nil), values...)
	return nil
}

func TestSetupFeedbackOwnsRingWithoutOverwritingBase(t *testing.T) {
	lc := &setupLEDController{}
	s := &Server{ledController: lc, volume: &volumeController{}, mute: &muteController{}}
	s.SetLEDs([]led.Led{{ID: 0, B: 99}}, boolPtr(false))
	s.ShowSetupResetClicks(3, 5)
	if !s.setupFeedback.Load() || len(lc.frame) != 12 {
		t.Fatal("setup feedback did not own the ring")
	}
	if lc.frame[7].R != 255 || lc.frame[8].R == 255 {
		t.Fatalf("three-of-five progress did not light eight LEDs: %+v", lc.frame)
	}

	// A controller frame received during the gesture is retained, not painted.
	s.SetLEDs([]led.Led{{ID: 0, G: 77}}, boolPtr(false))
	if lc.frame[0].G == 77 {
		t.Fatal("controller frame painted over setup feedback")
	}
	s.ClearSetupResetFeedback()
	if s.setupFeedback.Load() || lc.frame[0].G != 77 {
		t.Fatalf("latest base frame was not restored: %+v", lc.frame[0])
	}
}

func TestSetupCountdownAndSuccessFrames(t *testing.T) {
	lc := &setupLEDController{}
	s := &Server{ledController: lc}
	s.ShowSetupResetCountdown(6, 12)
	if lc.frame[5].R != 255 || lc.frame[6].R == 255 {
		t.Fatal("countdown did not paint the requested half ring")
	}
	s.ShowSetupResetSuccess()
	for i, pixel := range lc.frame {
		if pixel.G != 220 {
			t.Fatalf("success LED %d = %+v", i, pixel)
		}
	}
}
