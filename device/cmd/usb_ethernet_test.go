package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// swapUSBEthernetPaths points the flag, script and mode-file paths at a temp
// directory and restores them at cleanup.
func swapUSBEthernetPaths(t *testing.T) (flag, script, mchange string) {
	t.Helper()
	dir := t.TempDir()
	old := usbEthernetPaths
	flag = filepath.Join(dir, "net-mode")
	script = filepath.Join(dir, "techo5-eth")
	mchange = filepath.Join(dir, "mchange")
	usbEthernetPaths = struct{ flag, script, mchange string }{flag, script, mchange}
	t.Cleanup(func() { usbEthernetPaths = old })
	return flag, script, mchange
}

// writeScript installs a stand-in techo5-eth that records each run in marker.
func writeScript(t *testing.T, script, marker string) {
	t.Helper()
	body := "#!/bin/sh\n: > " + marker + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
}

func TestUSBEthernetDisabledByDefault(t *testing.T) {
	flag, script, _ := swapUSBEthernetPaths(t)
	writeScript(t, script, filepath.Join(t.TempDir(), "marker"))
	applied := map[string]any{}
	if err := applyUSBEthernetSetting(map[string]any{"usb_ethernet_enabled": false}, applied); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := os.Stat(flag); !os.IsNotExist(err) {
		t.Fatalf("flag file should not exist, got %v", err)
	}
	if applied["usb_ethernet_enabled"] != false {
		t.Fatalf("applied usb_ethernet_enabled = %v, want false", applied["usb_ethernet_enabled"])
	}
}

func TestUSBEthernetKeyAbsentIsNoOp(t *testing.T) {
	flag, _, _ := swapUSBEthernetPaths(t)
	if err := applyUSBEthernetSetting(map[string]any{"volume_percent": 40.0}, map[string]any{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := os.Stat(flag); !os.IsNotExist(err) {
		t.Fatalf("flag file should not exist, got %v", err)
	}
}

func TestUSBEthernetEnableWritesFlagAndRunsScript(t *testing.T) {
	flag, script, _ := swapUSBEthernetPaths(t)
	marker := filepath.Join(t.TempDir(), "ran")
	writeScript(t, script, marker)
	applied := map[string]any{}
	if err := applyUSBEthernetSetting(map[string]any{"usb_ethernet_enabled": true}, applied); err != nil {
		t.Fatalf("apply: %v", err)
	}
	data, err := os.ReadFile(flag)
	if err != nil {
		t.Fatalf("read flag: %v", err)
	}
	if string(data) != usbEthernetWired+"\n" {
		t.Fatalf("flag = %q, want %q", data, usbEthernetWired+"\n")
	}
	if applied["usb_ethernet_enabled"] != true {
		t.Fatalf("applied usb_ethernet_enabled = %v, want true", applied["usb_ethernet_enabled"])
	}
	waitForFile(t, marker)
}

func TestUSBEthernetEnableIsIdempotent(t *testing.T) {
	flag, script, _ := swapUSBEthernetPaths(t)
	marker := filepath.Join(t.TempDir(), "ran")
	writeScript(t, script, marker)
	if err := os.WriteFile(flag, []byte(usbEthernetWired+"\n"), 0o644); err != nil {
		t.Fatalf("pre-write flag: %v", err)
	}
	if err := applyUSBEthernetSetting(map[string]any{"usb_ethernet_enabled": true}, map[string]any{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("script must not run when the flag is unchanged, got %v", err)
	}
}

func TestUSBEthernetDisableRemovesFlagAndRestoresDeviceMode(t *testing.T) {
	flag, _, mchange := swapUSBEthernetPaths(t)
	if err := os.WriteFile(flag, []byte(usbEthernetWired+"\n"), 0o644); err != nil {
		t.Fatalf("pre-write flag: %v", err)
	}
	// force:1 = host mode active, the state that needs the revert.
	if err := os.WriteFile(mchange, []byte("Active:1 host:1 suspend:0 force:1\n"), 0o644); err != nil {
		t.Fatalf("pre-write mchange: %v", err)
	}
	if err := applyUSBEthernetSetting(map[string]any{"usb_ethernet_enabled": false}, map[string]any{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := os.Stat(flag); !os.IsNotExist(err) {
		t.Fatalf("flag file should be removed, got %v", err)
	}
	data, err := os.ReadFile(mchange)
	if err != nil {
		t.Fatalf("read mchange: %v", err)
	}
	if !strings.Contains(string(data), "device") {
		t.Fatalf("mchange = %q, want a device-mode revert", data)
	}
}

func TestUSBEthernetDisableLeavesDeviceModeAlone(t *testing.T) {
	_, _, mchange := swapUSBEthernetPaths(t)
	// force:2 = already in device mode; writing to it would be wrong.
	content := "Active:1 host:0 suspend:0 force:2\n"
	if err := os.WriteFile(mchange, []byte(content), 0o644); err != nil {
		t.Fatalf("pre-write mchange: %v", err)
	}
	if err := applyUSBEthernetSetting(map[string]any{"usb_ethernet_enabled": false}, map[string]any{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	data, err := os.ReadFile(mchange)
	if err != nil {
		t.Fatalf("read mchange: %v", err)
	}
	if string(data) != content {
		t.Fatalf("mchange = %q, want unchanged %q", data, content)
	}
}

func TestUSBEthernetUnavailableDeviceErrors(t *testing.T) {
	dir := t.TempDir()
	old := usbEthernetPaths
	usbEthernetPaths = struct{ flag, script, mchange string }{
		filepath.Join(dir, "absent", "net-mode"),
		filepath.Join(dir, "techo5-eth"),
		filepath.Join(dir, "mchange"),
	}
	t.Cleanup(func() { usbEthernetPaths = old })
	if err := applyUSBEthernetSetting(map[string]any{"usb_ethernet_enabled": true}, map[string]any{}); err == nil {
		t.Fatal("apply must error where /data/tater-linux is missing")
	}
}
