package usbethernet

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func testManager(t *testing.T, target string) (*Manager, *[][]string) {
	t.Helper()
	dir := t.TempDir()
	paths := Paths{
		Flag:    filepath.Join(dir, "data", "net-mode"),
		Script:  filepath.Join(dir, "tater-usb-ethernet"),
		MChange: filepath.Join(dir, "mchange"),
		State:   filepath.Join(dir, "state"),
	}
	if err := os.MkdirAll(filepath.Dir(paths.Flag), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Script, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.MChange, []byte("Active:1 host:0 force:2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commands := &[][]string{}
	manager := &Manager{Target: target, Paths: paths}
	manager.Start = func(path string, args ...string) error {
		*commands = append(*commands, append([]string{path}, args...))
		return nil
	}
	return manager, commands
}

func TestSupportIsBiscuitAndPlatformGated(t *testing.T) {
	manager, _ := testManager(t, "biscuit")
	if !manager.Supported() {
		t.Fatal("complete Biscuit platform should be supported")
	}
	manager.Target = "radar"
	if manager.Supported() {
		t.Fatal("Radar must not inherit Biscuit USB role handling")
	}
	manager.Target = "biscuit"
	if err := os.Remove(manager.Paths.MChange); err != nil {
		t.Fatal(err)
	}
	if manager.Supported() {
		t.Fatal("daemon-only OTA without kernel support must be unavailable")
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
		if capability.Target != target || capability.Backend == "" || capability.Status == "" ||
			capability.Port == "" || capability.Power == "" || capability.Recovery == "" || capability.Reason == "" {
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

func TestEnableWritesFlagAndStartsProbe(t *testing.T) {
	manager, commands := testManager(t, "biscuit")
	if err := manager.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if !manager.Enabled() {
		t.Fatal("wired preference was not persisted")
	}
	want := [][]string{{manager.Paths.Script}}
	if !reflect.DeepEqual(*commands, want) {
		t.Fatalf("commands = %#v, want %#v", *commands, want)
	}
}

func TestFailedStartRollsBackPreference(t *testing.T) {
	manager, _ := testManager(t, "biscuit")
	manager.Start = func(string, ...string) error { return errors.New("boom") }
	if err := manager.SetEnabled(true); err == nil {
		t.Fatal("start failure must be returned")
	}
	if manager.Enabled() {
		t.Fatal("failed start left a false enabled flag")
	}
}

func TestRepeatedEnableDoesNotReprobe(t *testing.T) {
	manager, commands := testManager(t, "biscuit")
	if err := manager.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.Paths.State, []byte("fallback no-lease\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if len(*commands) != 1 {
		t.Fatalf("settings reconciliation launched %d probes, want one", len(*commands))
	}
}

func TestDisableRemovesFlagAndRunsStop(t *testing.T) {
	manager, commands := testManager(t, "biscuit")
	if err := manager.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.Paths.State, []byte("active eth0 192.168.1.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if manager.Enabled() {
		t.Fatal("disable left the preference flag")
	}
	want := [][]string{{manager.Paths.Script}, {manager.Paths.Script, "stop"}}
	if !reflect.DeepEqual(*commands, want) {
		t.Fatalf("commands = %#v, want %#v", *commands, want)
	}
}

func TestUnsupportedEnableFailsWithoutPersisting(t *testing.T) {
	manager, commands := testManager(t, "crown")
	if err := manager.SetEnabled(true); err == nil {
		t.Fatal("unsupported target accepted wired mode")
	}
	if manager.Enabled() || len(*commands) != 0 {
		t.Fatalf("unsupported enable changed state: enabled=%v commands=%v", manager.Enabled(), *commands)
	}
}

func TestUnsupportedDisableDoesNotTouchUSBRole(t *testing.T) {
	manager, commands := testManager(t, "radar")
	if err := os.WriteFile(manager.Paths.MChange, []byte("Active:1 host:1 force:1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(manager.Paths.MChange)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "Active:1 host:1 force:1\n" || len(*commands) != 0 {
		t.Fatalf("unsupported disable changed USB state: mchange=%q commands=%v", got, *commands)
	}
}

func TestDiagnosticsSeparateDesiredAndActiveState(t *testing.T) {
	manager, _ := testManager(t, "biscuit")
	if err := manager.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.Paths.State, []byte("active eth0 192.168.1.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := manager.Diagnostics()
	if got["supported"] != true || got["enabled"] != true || got["active"] != true ||
		got["interface"] != "eth0" || got["address"] != "192.168.1.9" ||
		got["target"] != "biscuit" || got["backend"] != "mediatek-mchange" ||
		got["qualification"] != "qualified" {
		t.Fatalf("unexpected diagnostics: %#v", got)
	}
}

func TestUnqualifiedDiagnosticsExplainTheHardwareGate(t *testing.T) {
	manager, _ := testManager(t, "athena")
	got := manager.Diagnostics()
	if got["supported"] != false || got["qualification"] != "kernel-work" ||
		got["backend"] != "mediatek-mtu3-role-switch-candidate" {
		t.Fatalf("unexpected Athena diagnostics: %#v", got)
	}
	if reason, _ := got["qualification_reason"].(string); reason == "" {
		t.Fatalf("Athena diagnostics omitted the qualification blocker: %#v", got)
	}
}

func TestFallbackDiagnosticsReportReasonNotInterface(t *testing.T) {
	manager, _ := testManager(t, "biscuit")
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
	manager, _ := testManager(t, "biscuit")
	if err := os.WriteFile(manager.Paths.State, []byte("waiting no-adapter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := manager.Diagnostics()
	if got["state"] != "waiting" || got["reason"] != "no-adapter" || got["active"] != false {
		t.Fatalf("unexpected waiting diagnostics: %#v", got)
	}
}
