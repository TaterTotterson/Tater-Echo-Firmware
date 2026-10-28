package usbethernet

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func testManager(t *testing.T, target string) *Manager {
	t.Helper()
	dir := t.TempDir()
	paths := Paths{
		Script:   filepath.Join(dir, "tater-usb-ethernet"),
		MChange:  filepath.Join(dir, "mchange"),
		RoleMode: filepath.Join(dir, "role-mode"),
		State:    filepath.Join(dir, "state"),
	}
	if err := os.WriteFile(paths.Script, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.MChange, []byte("Active:1 host:0 force:2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.RoleMode, []byte("ufp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &Manager{Target: target, Paths: paths}
}

func TestSupportIsQualifiedAndPlatformGated(t *testing.T) {
	manager := testManager(t, "biscuit")
	if !manager.Prepared() || !manager.Supported() {
		t.Fatal("complete Biscuit platform should be prepared and supported")
	}
	manager.Target = "crown"
	if !manager.Prepared() || !manager.Supported() {
		t.Fatal("qualified Crown hardware-OTG path should be prepared and supported")
	}
	manager.Target = "checkers"
	if !manager.Prepared() || !manager.Supported() {
		t.Fatal("qualified Checkers hardware-OTG path should be prepared and supported")
	}
	manager.Target = "rook"
	if !manager.Prepared() || !manager.Supported() {
		t.Fatal("qualified Rook MediaTek role path should be prepared and supported")
	}
	manager.Target = "radar"
	if !manager.Prepared() || manager.Supported() {
		t.Fatal("Radar should be prepared without inheriting Biscuit role qualification")
	}
	manager.Target = "biscuit"
	if err := os.Remove(manager.Paths.MChange); err != nil {
		t.Fatal(err)
	}
	if manager.Supported() {
		t.Fatal("daemon-only OTA without kernel support must be unavailable")
	}
	manager = testManager(t, "crown")
	if err := os.Remove(manager.Paths.RoleMode); err != nil {
		t.Fatal(err)
	}
	if manager.Supported() {
		t.Fatal("Crown must fail closed without its verified hardware role path")
	}
	manager = testManager(t, "checkers")
	if err := os.Remove(manager.Paths.RoleMode); err != nil {
		t.Fatal(err)
	}
	if manager.Supported() {
		t.Fatal("Checkers must fail closed without its verified hardware role path")
	}
	manager = testManager(t, "rook")
	if err := os.Remove(manager.Paths.MChange); err != nil {
		t.Fatal(err)
	}
	if manager.Supported() {
		t.Fatal("Rook must fail closed without MediaTek role control")
	}
}

func TestMissingControllerIsNotPrepared(t *testing.T) {
	manager := testManager(t, "checkers")
	if !manager.Prepared() {
		t.Fatal("known target with an installed controller should be prepared")
	}
	if err := os.Remove(manager.Paths.Script); err != nil {
		t.Fatal(err)
	}
	if manager.Prepared() || manager.State().Phase != "unavailable" {
		t.Fatalf("missing controller did not fail closed: %#v", manager.Diagnostics())
	}
}

func TestCapabilityCatalogCoversEverySatelliteAndFailsClosed(t *testing.T) {
	capabilities := Capabilities()
	got := make([]string, 0, len(capabilities))
	for target := range capabilities {
		got = append(got, target)
	}
	sort.Strings(got)
	want := []string{"athena", "biscuit", "checkers", "crown", "radar", "rook"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("capability targets = %v, want %v", got, want)
	}

	for target, capability := range capabilities {
		if capability.Target != target || capability.Backend == "" || capability.Detection == "" ||
			capability.Status == "" || capability.Port == "" || capability.Power == "" ||
			capability.Recovery == "" || capability.Reason == "" {
			t.Errorf("incomplete %s capability: %#v", target, capability)
		}
		qualified := target == "biscuit" || target == "checkers" || target == "crown" || target == "rook"
		if qualified && !capability.qualified() {
			t.Errorf("hardware-qualified target %s lost qualification: %#v", target, capability)
		}
		if !qualified && capability.qualified() {
			t.Errorf("unvalidated target %s became qualified: %#v", target, capability)
		}
	}

	unknown := CapabilityForTarget("future-echo")
	if unknown.Status != "unknown" || unknown.Backend != "none" || unknown.qualified() {
		t.Fatalf("unknown target did not fail closed: %#v", unknown)
	}
}

func TestTaterHostnameAliasUsesTheSameCapability(t *testing.T) {
	plain := CapabilityForTarget("biscuit")
	aliased := CapabilityForTarget("  TATER-BISCUIT\n")
	if !reflect.DeepEqual(plain, aliased) {
		t.Fatalf("aliased capability = %#v, want %#v", aliased, plain)
	}
}

func TestPreparedControllerStartsInPendingAutomaticState(t *testing.T) {
	manager := testManager(t, "radar")
	got := manager.Diagnostics()
	if got["prepared"] != true || got["automatic"] != true || got["supported"] != false ||
		got["active"] != false || got["state"] != "pending" {
		t.Fatalf("unexpected prepared diagnostics: %#v", got)
	}
}

func TestDiagnosticsSeparateQualificationAndActiveState(t *testing.T) {
	manager := testManager(t, "biscuit")
	if err := os.WriteFile(manager.Paths.State, []byte("active eth0 192.168.1.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := manager.Diagnostics()
	if got["supported"] != true || got["automatic"] != true || got["active"] != true ||
		got["interface"] != "eth0" || got["address"] != "192.168.1.9" ||
		got["target"] != "biscuit" || got["backend"] != "mediatek-mchange" ||
		got["detection"] != "bounded-active-probe" || got["qualification"] != "qualified" {
		t.Fatalf("unexpected diagnostics: %#v", got)
	}
}

func TestShowDiagnosticsReportQualifiedHardwareOTG(t *testing.T) {
	for _, target := range []string{"checkers", "crown"} {
		t.Run(target, func(t *testing.T) {
			manager := testManager(t, target)
			if err := os.WriteFile(manager.Paths.State, []byte("active eth0 10.4.20.159\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			got := manager.Diagnostics()
			if got["supported"] != true || got["active"] != true || got["qualification"] != "qualified" ||
				got["backend"] != "mediatek-musb-hardware-otg" || got["detection"] != "hardware-id-passive" ||
				got["interface"] != "eth0" || got["address"] != "10.4.20.159" {
				t.Fatalf("unexpected %s diagnostics: %#v", target, got)
			}
		})
	}
}

func TestRookDiagnosticsReportQualifiedMChange(t *testing.T) {
	manager := testManager(t, "rook")
	if err := os.WriteFile(manager.Paths.State, []byte("active eth0 10.4.20.159\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := manager.Diagnostics()
	if got["supported"] != true || got["active"] != true || got["qualification"] != "qualified" ||
		got["backend"] != "mediatek-mchange" || got["detection"] != "bounded-active-probe" {
		t.Fatalf("unexpected Rook diagnostics: %#v", got)
	}
}

func TestUnqualifiedDiagnosticsExplainTheHardwareGate(t *testing.T) {
	tests := []struct {
		target    string
		backend   string
		detection string
	}{
		{target: "athena", backend: "mediatek-mtu3-gpio-otg", detection: "hardware-id-passive"},
		{target: "radar", backend: "mediatek-mchange-candidate", detection: "passive-until-role-qualified"},
	}
	for _, test := range tests {
		t.Run(test.target, func(t *testing.T) {
			manager := testManager(t, test.target)
			got := manager.Diagnostics()
			if got["prepared"] != true || got["supported"] != false || got["qualification"] != "hardware-validation" ||
				got["backend"] != test.backend || got["detection"] != test.detection {
				t.Fatalf("unexpected %s diagnostics: %#v", test.target, got)
			}
			if reason, _ := got["qualification_reason"].(string); reason == "" {
				t.Fatalf("%s diagnostics omitted the qualification blocker: %#v", test.target, got)
			}
		})
	}
}

func TestFallbackDiagnosticsReportReasonNotInterface(t *testing.T) {
	manager := testManager(t, "biscuit")
	if err := os.WriteFile(manager.Paths.State, []byte("fallback no-adapter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := manager.Diagnostics()
	if got["state"] != "fallback" || got["reason"] != "no-adapter" {
		t.Fatalf("unexpected fallback diagnostics: %#v", got)
	}
	if _, exists := got["interface"]; exists {
		t.Fatalf("fallback reason was exposed as an interface: %#v", got)
	}
}

func TestWaitingDiagnosticsReportWhyWifiIsStillActive(t *testing.T) {
	manager := testManager(t, "biscuit")
	if err := os.WriteFile(manager.Paths.State, []byte("waiting no-adapter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := manager.Diagnostics()
	if got["state"] != "waiting" || got["reason"] != "no-adapter" || got["active"] != false {
		t.Fatalf("unexpected waiting diagnostics: %#v", got)
	}
}
