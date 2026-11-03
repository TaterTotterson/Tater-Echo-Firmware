package led

import (
	"testing"

	pkgled "github.com/TaterTotterson/Tater-Echo-Firmware/pkg/led"
)

func TestCheckersUsesScreenOnlyController(t *testing.T) {
	ConfigureTarget("checkers")
	defer ConfigureTarget("biscuit")
	controller, err := NewDefaultController()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := controller.(discardController); !ok {
		t.Fatalf("controller = %T, want screen-only sink", controller)
	}
	if count, err := controller.GetNumLEDs(); err != nil || count != 12 {
		t.Fatalf("logical LED count = %d, %v", count, err)
	}
	if err := controller.SetLEDs(pkgled.Led{ID: 0, R: 255}); err != nil {
		t.Fatal(err)
	}
	if err := InitMuteButtonLED(); err != nil {
		t.Fatalf("screen target touched Biscuit mute GPIO: %v", err)
	}
}

func TestCronosUsesScreenOnlyController(t *testing.T) {
	ConfigureTarget("cronos")
	defer ConfigureTarget("biscuit")
	controller, err := NewDefaultController()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := controller.(discardController); !ok {
		t.Fatalf("controller = %T, want screen-only sink", controller)
	}
}

func TestCrownUsesScreenOnlyController(t *testing.T) {
	ConfigureTarget("crown")
	defer ConfigureTarget("biscuit")
	controller, err := NewDefaultController()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := controller.(discardController); !ok {
		t.Fatalf("controller = %T, want screen-only sink", controller)
	}
}

func TestAthenaUsesScreenOnlyController(t *testing.T) {
	ConfigureTarget("athena")
	defer ConfigureTarget("biscuit")
	controller, err := NewDefaultController()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := controller.(discardController); !ok {
		t.Fatalf("controller = %T, want screen-only sink", controller)
	}
}
