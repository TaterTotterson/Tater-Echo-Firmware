package main

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/platform"
)

const sshControlPath = "/usr/local/sbin/tater-ssh-control"

var (
	sshBaseOS     = platform.Base
	runSSHControl = func(input string) ([]byte, error) {
		command := exec.Command(sshControlPath, "configure")
		command.Stdin = strings.NewReader(input)
		return command.CombinedOutput()
	}
)

func sshSettingBool(value any) (bool, error) {
	switch normalized := strings.ToLower(strings.TrimSpace(fmt.Sprint(value))); normalized {
	case "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off", "":
		return false, nil
	default:
		return false, fmt.Errorf("ssh_enabled must be true or false")
	}
}

func validSSHPassword(password string) bool {
	if len(password) < 12 || len(password) > 64 {
		return false
	}
	for _, character := range password {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune("._@%+=-", character) {
			continue
		}
		return false
	}
	return true
}

// applySSHSettings applies Tater's per-satellite switch. The password is sent
// to the root-only helper on stdin, then deliberately removed from the
// settings acknowledgement so it cannot enter satellite state or logs.
func applySSHSettings(values, applied map[string]any) error {
	enabledValue, hasEnabled := values["ssh_enabled"]
	_, hasPassword := values["ssh_password"]
	if !hasEnabled && !hasPassword {
		return nil
	}
	delete(applied, "ssh_password")
	delete(applied, "ssh_username")
	if !hasEnabled {
		delete(applied, "ssh_enabled")
		return fmt.Errorf("ssh_enabled is required when applying SSH credentials")
	}
	enabled, err := sshSettingBool(enabledValue)
	if err != nil {
		delete(applied, "ssh_enabled")
		return err
	}
	password := ""
	if rawPassword, present := values["ssh_password"]; present && rawPassword != nil {
		password = strings.TrimSpace(fmt.Sprint(rawPassword))
	}
	if enabled && !validSSHPassword(password) {
		delete(applied, "ssh_enabled")
		return fmt.Errorf("ssh_password must be 12-64 characters using letters, numbers, or ._@%%+=-")
	}
	if sshBaseOS() != platform.TaterLinux {
		delete(applied, "ssh_enabled")
		return nil
	}
	output, err := runSSHControl(fmt.Sprintf("%t\n%s\n", enabled, password))
	if err != nil {
		delete(applied, "ssh_enabled")
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("apply SSH setting: %s", message)
	}
	applied["ssh_enabled"] = enabled
	applied["ssh_username"] = "root"
	return nil
}
