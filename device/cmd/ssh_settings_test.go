package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/platform"
)

func withSSHSettingTestHooks(t *testing.T, runner func(string) ([]byte, error)) {
	t.Helper()
	oldBaseOS := sshBaseOS
	oldRunner := runSSHControl
	sshBaseOS = func() string { return platform.TaterLinux }
	runSSHControl = runner
	t.Cleanup(func() {
		sshBaseOS = oldBaseOS
		runSSHControl = oldRunner
	})
}

func TestApplySSHSettingsEnablesWithoutEchoingPassword(t *testing.T) {
	password := "tater-k7pm-4xq2"
	var input string
	withSSHSettingTestHooks(t, func(value string) ([]byte, error) {
		input = value
		return []byte("enabled username=root\n"), nil
	})
	values := map[string]any{"ssh_enabled": true, "ssh_password": password}
	applied := map[string]any{"ssh_enabled": true, "ssh_password": password}

	if err := applySSHSettings(values, applied); err != nil {
		t.Fatal(err)
	}
	if input != "true\n"+password+"\n" {
		t.Fatalf("control input = %q", input)
	}
	if applied["ssh_enabled"] != true || applied["ssh_username"] != "root" {
		t.Fatalf("applied = %#v", applied)
	}
	if _, present := applied["ssh_password"]; present {
		t.Fatal("settings acknowledgement exposed the SSH password")
	}
}

func TestApplySSHSettingsDisablesWithoutPassword(t *testing.T) {
	var input string
	withSSHSettingTestHooks(t, func(value string) ([]byte, error) {
		input = value
		return []byte("disabled\n"), nil
	})
	applied := map[string]any{"ssh_enabled": false}

	if err := applySSHSettings(map[string]any{"ssh_enabled": false}, applied); err != nil {
		t.Fatal(err)
	}
	if input != "false\n\n" || applied["ssh_enabled"] != false {
		t.Fatalf("input = %q applied = %#v", input, applied)
	}
}

func TestApplySSHSettingsRejectsUnsafeCredentials(t *testing.T) {
	withSSHSettingTestHooks(t, func(string) ([]byte, error) {
		t.Fatal("unsafe password reached the control helper")
		return nil, nil
	})
	for _, password := range []string{"short", "tater-password with space", "tater-password:colon"} {
		applied := map[string]any{"ssh_enabled": true, "ssh_password": password}
		err := applySSHSettings(map[string]any{"ssh_enabled": true, "ssh_password": password}, applied)
		if err == nil || !strings.Contains(err.Error(), "ssh_password") {
			t.Fatalf("password %q error = %v", password, err)
		}
		if _, present := applied["ssh_password"]; present {
			t.Fatalf("password %q remained in acknowledgement", password)
		}
	}
}

func TestApplySSHSettingsReturnsSanitizedHelperError(t *testing.T) {
	withSSHSettingTestHooks(t, func(string) ([]byte, error) {
		return []byte("tater-ssh-control: cannot update mode\n"), errors.New("exit status 1")
	})
	applied := map[string]any{"ssh_enabled": true, "ssh_password": "tater-k7pm-4xq2"}
	err := applySSHSettings(
		map[string]any{"ssh_enabled": true, "ssh_password": "tater-k7pm-4xq2"},
		applied,
	)
	if err == nil || !strings.Contains(err.Error(), "cannot update mode") {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "k7pm") {
		t.Fatal("helper error exposed the password")
	}
}
