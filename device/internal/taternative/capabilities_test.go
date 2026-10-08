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
	for _, key := range []string{"openwakeword", "wake_detector_selection", "dual_wake_confirmation"} {
		if got[key] != true {
			t.Errorf("biscuit %s = %#v, want true", key, got[key])
		}
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
	if got["screen_weather"] != true {
		t.Fatal("checkers did not advertise its Environment Core weather surface")
	}
	if got["screen_notifications"] != true {
		t.Fatal("checkers did not advertise its temporary display notification surface")
	}
	if got["camera_snapshot"] != true || got["camera_snapshot_version"] != 1 {
		t.Fatal("checkers did not advertise its on-demand Room Vision camera")
	}
	if got["ota"] != true {
		t.Fatal("checkers did not advertise OTA support")
	}
	if got["ble_advertisements"] != true || got["ble_advertisements_version"] != 1 {
		t.Fatal("checkers did not advertise BLE presence scanning")
	}
	if got["microphone"] != true || got["speaker"] != true {
		t.Fatal("checkers lost shared satellite capabilities")
	}
}

func TestCapabilitiesForTargetDescribesRookScreenAndCamera(t *testing.T) {
	got := CapabilitiesForTarget("rook")
	for _, key := range []string{"screen", "touchscreen", "screen_weather", "screen_notifications", "ble_advertisements", "ota"} {
		if got[key] != true {
			t.Errorf("rook %s = %#v, want true", key, got[key])
		}
	}
	for _, key := range []string{"led_ring"} {
		if got[key] != false {
			t.Errorf("rook %s = %#v, want false", key, got[key])
		}
	}
	if got["camera_snapshot"] != true || got["camera_snapshot_version"] != 1 {
		t.Fatal("rook did not advertise its on-demand Room Vision camera")
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
