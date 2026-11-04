package led

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	pkgled "github.com/TaterTotterson/Tater-Echo-Firmware/pkg/led"
)

func TestWaitForBootRingStopWaitsForAcknowledgement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tater-led-boot-stopped")
	go func() {
		time.Sleep(10 * time.Millisecond)
		_ = os.WriteFile(path, []byte("1\n"), 0o644)
	}()
	if !waitForBootRingStop(path, 200*time.Millisecond) {
		t.Fatal("boot-ring handoff did not observe the stop acknowledgement")
	}
}

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
