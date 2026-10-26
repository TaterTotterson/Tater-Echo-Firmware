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
		Script:  filepath.Join(dir, "tater-usb-ethernet"),
		MChange: filepath.Join(dir, "mchange"),
		State:   filepath.Join(dir, "state"),
	}
	if err := os.WriteFile(paths.Script, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.MChange, []byte("Active:1 host:0 force:2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &Manager{Target: target, Paths: paths}
}

func TestSupportIsBiscuitAndPlatformGated(t *testing.T) {
	manager := testManager(t, "biscuit")
	if !manager.Prepared() || !manager.Supported() {
		t.Fatal("complete Biscuit platform should be prepared and supported")
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
		if target == "biscuit" && !capability.qualified() {
			t.Errorf("Biscuit must remain the qualified baseline: %#v", capability)
		}
		if target != "biscuit" && capability.qualified() {
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
	manager := testManager(t, "checkers")
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

func TestUnqualifiedDiagnosticsExplainTheHardwareGate(t *testing.T) {
	manager := testManager(t, "athena")
	got := manager.Diagnostics()
	if got["prepared"] != true || got["supported"] != false || got["qualification"] != "kernel-work" ||
		got["backend"] != "mediatek-mtu3-role-switch-candidate" ||
		got["detection"] != "passive-until-kernel-host-mode" {
		t.Fatalf("unexpected Athena diagnostics: %#v", got)
	}
	if reason, _ := got["qualification_reason"].(string); reason == "" {
		t.Fatalf("Athena diagnostics omitted the qualification blocker: %#v", got)
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
