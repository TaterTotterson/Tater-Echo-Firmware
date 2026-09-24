package taternative

import "testing"

func TestCapabilitiesForTargetKeepsBiscuitRing(t *testing.T) {
	got := CapabilitiesForTarget("biscuit")
	if got["led_ring"] != true {
		t.Fatalf("biscuit led_ring = %#v", got["led_ring"])
	}
	if _, ok := got["screen"]; ok {
		t.Fatal("biscuit unexpectedly advertises a screen")
	}
	if got["ble_advertisements"] != true || got["ble_advertisements_version"] != 1 {
		t.Fatalf("biscuit BLE capabilities = %#v", got)
	}
}

func TestCapabilitiesForTargetDescribesCheckersScreen(t *testing.T) {
	got := CapabilitiesForTarget("checkers")
	if got["led_ring"] != false {
		t.Fatalf("checkers led_ring = %#v", got["led_ring"])
	}
	if got["screen"] != true || got["touchscreen"] != true || got["screen_protocol"] != 1 {
		t.Fatalf("checkers screen capabilities = %#v", got)
	}
	if got["ota"] != false {
		t.Fatal("checkers advertised OTA before coordinated APK/native rollback exists")
	}
	if got["ble_advertisements"] != false {
		t.Fatal("checkers advertised Biscuit BLE scanning before hardware bring-up")
	}
	if _, ok := got["ble_advertisements_version"]; ok {
		t.Fatal("checkers advertised a BLE protocol version without BLE support")
	}
	if got["microphone"] != true || got["speaker"] != true {
		t.Fatal("checkers lost shared satellite capabilities")
	}
}

func TestCapabilitiesForTargetReturnsIndependentMaps(t *testing.T) {
	checkers := CapabilitiesForTarget("checkers")
	biscuit := CapabilitiesForTarget("biscuit")
	checkers["microphone"] = false
	if biscuit["microphone"] != true {
		t.Fatal("target capability maps share mutable state")
	}
}
