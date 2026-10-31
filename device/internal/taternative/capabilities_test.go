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
	if got["ble_gatt"] != true || got["ble_gatt_pairing"] != true {
		t.Fatalf("biscuit active GATT capabilities = %#v", got)
	}
	for _, key := range []string{"openwakeword", "nanowakeword", "wake_detector_selection", "dual_wake_confirmation", "wake_model_tuning_overrides"} {
		if got[key] != true {
			t.Errorf("biscuit %s = %#v, want true", key, got[key])
		}
	}
	architectures, ok := got["nanowakeword_architectures"].([]string)
	if !ok || len(architectures) != 2 || architectures[0] != "cnn" || architectures[1] != "gru" {
		t.Fatalf("biscuit NanoWakeWord architectures = %#v, want [cnn gru]", got["nanowakeword_architectures"])
	}
}

func TestCapabilitiesForTargetKeepsRadarRing(t *testing.T) {
	got := CapabilitiesForTarget("radar")
	if got["led_ring"] != true || got["microphone"] != true || got["speaker"] != true {
		t.Fatalf("radar shared voice/ring capabilities = %#v", got)
	}
	if _, ok := got["screen"]; ok {
		t.Fatal("radar unexpectedly advertises a screen")
	}
	if got["ble_gatt"] != true || got["ota"] != true {
		t.Fatalf("radar native capabilities = %#v", got)
	}
	if got["output_chain"] != true {
		t.Fatalf("radar output_chain = %#v, want true", got["output_chain"])
	}
}

func TestSupportedEchoTargetsAdvertiseOutputChain(t *testing.T) {
	for _, target := range []string{"biscuit", "radar", "checkers", "crown", "athena", "rook"} {
		if got := CapabilitiesForTarget(target)["output_chain"]; got != true {
			t.Errorf("%s output_chain = %#v, want true", target, got)
		}
	}
	if got := CapabilitiesForTarget("future-echo")["output_chain"]; got != nil {
		t.Fatalf("unknown target output_chain = %#v, want absent", got)
	}
}

func TestSupportedEchoTargetsAdvertiseManagedSSH(t *testing.T) {
	for _, target := range []string{"biscuit", "radar", "checkers", "crown", "athena", "rook"} {
		got := CapabilitiesForTarget(target)
		if got["ssh_management"] != true || got["ssh_management_version"] != 1 {
			t.Errorf("%s managed SSH capabilities = %#v", target, got)
		}
	}
	if got := CapabilitiesForTarget("future-echo")["ssh_management"]; got != nil {
		t.Fatalf("unknown target ssh_management = %#v, want absent", got)
	}
}

func TestSupportedEchoTargetsAdvertiseSystemOTA(t *testing.T) {
	for _, target := range []string{"biscuit", "radar", "checkers", "crown", "athena", "rook"} {
		if got := CapabilitiesForTarget(target)["system_ota_version"]; got != 1 {
			t.Errorf("%s system_ota_version = %#v, want 1", target, got)
		}
	}
	if got := CapabilitiesForTarget("future-echo")["system_ota_version"]; got != nil {
		t.Fatalf("unknown target system_ota_version = %#v, want absent", got)
	}
}

func TestCapabilitiesForTargetDescribesCrownScreenAndCamera(t *testing.T) {
	got := CapabilitiesForTarget("crown")
	for _, key := range []string{
		"screen", "touchscreen", "screen_weather", "screen_climate", "screen_notifications",
		"screen_last_event", "camera_snapshot", "ble_advertisements", "ble_gatt", "ota",
	} {
		if got[key] != true {
			t.Errorf("crown %s = %#v, want true", key, got[key])
		}
	}
	if got["led_ring"] != false {
		t.Fatalf("crown led_ring = %#v, want false", got["led_ring"])
	}
}

func TestCapabilitiesForTargetDescribesAthenaScreenAndCamera(t *testing.T) {
	got := CapabilitiesForTarget("athena")
	for _, key := range []string{
		"screen", "touchscreen", "screen_weather", "screen_climate", "screen_notifications",
		"screen_last_event", "camera_snapshot", "ble_advertisements", "ble_gatt", "ota",
	} {
		if got[key] != true {
			t.Errorf("athena %s = %#v, want true", key, got[key])
		}
	}
	if got["led_ring"] != false {
		t.Fatalf("athena led_ring = %#v, want false", got["led_ring"])
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
	if got["screen_climate"] != true {
		t.Fatal("checkers did not advertise its integration-neutral climate surface")
	}
	if got["screen_notifications"] != true {
		t.Fatal("checkers did not advertise its temporary display notification surface")
	}
	if got["screen_notifications_version"] != 2 {
		t.Fatalf("checkers screen notification protocol = %#v", got["screen_notifications_version"])
	}
	if got["camera_snapshot"] != true || got["camera_snapshot_version"] != 1 {
		t.Fatal("checkers did not advertise its on-demand Room Vision camera")
	}
	if got["screen_camera_stream"] != true || got["screen_camera_stream_version"] != 1 {
		t.Fatal("checkers did not advertise native camera stream decoding")
	}
	if got["ota"] != true {
		t.Fatal("checkers did not advertise OTA support")
	}
	if got["ota_bundle_components_version"] != 2 {
		t.Fatal("checkers did not advertise staged component OTA continuation")
	}
	if got["ble_advertisements"] != true || got["ble_advertisements_version"] != 1 {
		t.Fatal("checkers did not advertise BLE presence scanning")
	}
	if got["ble_gatt"] != true || got["ble_gatt_pairing"] != true {
		t.Fatal("checkers did not advertise its exclusive raw-controller GATT backend")
	}
	if got["microphone"] != true || got["speaker"] != true {
		t.Fatal("checkers lost shared satellite capabilities")
	}
}

func TestCapabilitiesForTargetDescribesRookScreenAndCamera(t *testing.T) {
	got := CapabilitiesForTarget("rook")
	for _, key := range []string{"screen", "touchscreen", "screen_weather", "screen_climate", "screen_notifications", "ble_advertisements", "ota"} {
		if got[key] != true {
			t.Errorf("rook %s = %#v, want true", key, got[key])
		}
	}
	for _, key := range []string{"led_ring"} {
		if got[key] != false {
			t.Errorf("rook %s = %#v, want false", key, got[key])
		}
	}
	if got["ble_gatt"] != true || got["ble_gatt_pairing"] != true {
		t.Fatal("rook did not advertise its BlueZ active-GATT backend")
	}
	if got["camera_snapshot"] != true || got["camera_snapshot_version"] != 1 {
		t.Fatal("rook did not advertise its on-demand Room Vision camera")
	}
}

func TestScreenTargetsAdvertiseDisplayPowerManagement(t *testing.T) {
	for _, target := range []string{"checkers", "crown", "athena", "rook"} {
		got := CapabilitiesForTarget(target)
		if got["screen_power_management_version"] != 1 || got["screen_brightness"] != true || got["screen_auto_brightness"] != true || got["screen_night_mode"] != true || got["screen_red_clock"] != true || got["screen_clock_format"] != true {
			t.Errorf("%s display power capabilities = %#v", target, got)
		}
		if got["camera_stream"] != true || got["camera_stream_version"] != 1 {
			t.Errorf("%s native camera stream capabilities = %#v", target, got)
		}
	}
	if got := CapabilitiesForTarget("biscuit")["screen_power_management_version"]; got != nil {
		t.Fatalf("screenless target advertised display power management: %#v", got)
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
