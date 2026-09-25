package bluetooth

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	enrollmentAPKPath   = "/data/local/tmp/tater-ble-enrollment.apk"
	enrollmentComponent = "com.tatertotterson.bleenroll/.EnrollmentService"
	androidSystemPath   = "/system/bin:/system/xbin:/vendor/bin:/sbin"
)

var (
	enrollmentIDPattern   = regexp.MustCompile(`^[0-9a-fA-F-]{8,64}$`)
	enrollmentNamePattern = regexp.MustCompile(`[^A-Za-z0-9 ._-]+`)
	bluetoothAddress      = regexp.MustCompile(`(?i)^(?:[0-9a-f]{2}:){5}[0-9a-f]{2}$`)
	hexKeyPattern         = regexp.MustCompile(`(?i)^[0-9a-f]+$`)
)

type EnrollmentEvent func(messageType string, payload map[string]any)

// Enrollment temporarily hands /dev/stpbt from the raw observer to Android's
// BLE stack. The embedded helper exposes a Heart Rate GATT peripheral while
// this root process consumes the peer IRK from Bluedroid's private bond file.
type Enrollment struct {
	scanner *Scanner
	onEvent EnrollmentEvent

	mu       sync.Mutex
	activeID string
	status   string
	cancel   context.CancelFunc
}

func NewEnrollment(scanner *Scanner, onEvent EnrollmentEvent) *Enrollment {
	return &Enrollment{scanner: scanner, onEvent: onEvent, status: "idle"}
}

func EnrollmentSupported() bool { return len(embeddedEnrollmentAPK()) > 0 }

func (e *Enrollment) Status() map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	return map[string]any{
		"supported":     EnrollmentSupported(),
		"active":        e.activeID != "",
		"enrollment_id": e.activeID,
		"status":        e.status,
	}
}

func (e *Enrollment) Active() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.activeID != ""
}

func (e *Enrollment) Start(payload map[string]any) error {
	if !EnrollmentSupported() {
		return errors.New("BLE enrollment helper is not included in this firmware")
	}
	id := strings.TrimSpace(stringValue(payload["enrollment_id"]))
	name := strings.TrimSpace(stringValue(payload["display_name"]))
	name = enrollmentNamePattern.ReplaceAllString(name, "")
	if !enrollmentIDPattern.MatchString(id) || name == "" {
		return errors.New("invalid BLE enrollment request")
	}
	if len(name) > 28 {
		name = name[:28]
	}
	timeout := intValue(payload["timeout_s"], 90)
	if timeout < 30 {
		timeout = 30
	} else if timeout > 180 {
		timeout = 180
	}

	e.mu.Lock()
	if e.activeID != "" {
		e.mu.Unlock()
		return errors.New("BLE enrollment is already active")
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.activeID, e.status, e.cancel = id, "starting", cancel
	e.mu.Unlock()
	go e.run(ctx, id, name, time.Duration(timeout)*time.Second)
	return nil
}

func (e *Enrollment) Cancel(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.activeID == "" || (id != "" && id != e.activeID) {
		return
	}
	if e.cancel != nil {
		e.cancel()
	}
}

func (e *Enrollment) emit(messageType string, payload map[string]any) {
	if e.onEvent != nil {
		e.onEvent(messageType, payload)
	}
}

func (e *Enrollment) setStatus(id, status, detail string) {
	e.mu.Lock()
	if e.activeID == id {
		e.status = status
	}
	e.mu.Unlock()
	payload := map[string]any{"enrollment_id": id, "status": status}
	if detail != "" {
		payload["error"] = detail
	}
	e.emit("ble.enrollment.status", payload)
}

func shell(command string) (string, error) {
	cmd := exec.Command("/system/bin/sh", "-c", command)
	cmd.Env = androidShellEnvironment(os.Environ())
	output, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

func androidShellEnvironment(base []string) []string {
	environment := make([]string, 0, len(base)+1)
	for _, value := range base {
		if !strings.HasPrefix(value, "PATH=") {
			environment = append(environment, value)
		}
	}
	return append(environment, "PATH="+androidSystemPath)
}

func (e *Enrollment) run(ctx context.Context, id, name string, timeout time.Duration) {
	peerAddress := ""
	completed := false
	e.emit("ble.enrollment.status", map[string]any{"enrollment_id": id, "status": "starting"})
	defer func() {
		e.cleanup(peerAddress)
		e.mu.Lock()
		if e.activeID == id {
			e.activeID = ""
			e.status = "idle"
			e.cancel = nil
		}
		e.mu.Unlock()
		if completed {
			log.Println("[ble] enrollment complete; temporary Android bond removed")
		}
	}()

	e.scanner.SetEnabled(false)
	if err := os.MkdirAll(filepath.Dir(enrollmentAPKPath), 0o755); err != nil {
		e.fail(id, err)
		return
	}
	if err := os.WriteFile(enrollmentAPKPath, embeddedEnrollmentAPK(), 0o644); err != nil {
		e.fail(id, err)
		return
	}
	_, _ = shell("pm uninstall com.tatertotterson.bleenroll")
	if output, err := shell("pm install -r " + enrollmentAPKPath); err != nil {
		e.fail(id, fmt.Errorf("install BLE helper: %w (%s)", err, output))
		return
	}
	baseline := readBluetoothIdentityKeys()
	commands := []string{
		"pm enable " + enrollmentComponent,
		"pm enable com.android.bluetooth",
		"settings put global bluetooth_on 1",
		"svc bluetooth enable",
	}
	for _, command := range commands {
		if output, err := shell(command); err != nil {
			e.fail(id, fmt.Errorf("prepare Android Bluetooth: %w (%s)", err, output))
			return
		}
	}
	startCommand := fmt.Sprintf(
		"am startservice -n %s --es action start --es display_name %s --ei timeout_s %d",
		enrollmentComponent,
		strconv.Quote(name),
		int(timeout/time.Second),
	)
	if output, err := shell(startCommand); err != nil {
		e.fail(id, fmt.Errorf("start BLE helper: %w (%s)", err, output))
		return
	}
	e.setStatus(id, "advertising", "")

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			e.setStatus(id, "cancelled", "")
			return
		case <-deadline.C:
			e.setStatus(id, "timed_out", "No device paired before the enrollment window closed.")
			return
		case <-ticker.C:
			address, irk := newBluetoothIdentityKey(baseline, readBluetoothIdentityKeys())
			if irk == "" {
				continue
			}
			peerAddress = address
			e.emit("ble.enrollment.result", map[string]any{
				"enrollment_id": id,
				"ok":            true,
				"irk":           irk,
			})
			irk = ""
			completed = true
			return
		}
	}
}

func (e *Enrollment) fail(id string, err error) {
	e.emit("ble.enrollment.result", map[string]any{
		"enrollment_id": id,
		"ok":            false,
		"error":         err.Error(),
	})
}

func (e *Enrollment) cleanup(peerAddress string) {
	if peerAddress != "" && bluetoothAddress.MatchString(peerAddress) {
		_, _ = shell(fmt.Sprintf(
			"am startservice -n %s --es action stop --es address %s",
			enrollmentComponent,
			peerAddress,
		))
		time.Sleep(200 * time.Millisecond)
	} else {
		_, _ = shell("am force-stop com.tatertotterson.bleenroll")
	}
	_, _ = shell("svc bluetooth disable")
	_, _ = shell("settings put global bluetooth_on 0")
	_, _ = shell("pm disable " + enrollmentComponent)
	_, _ = shell("pm disable com.android.bluetooth")
	_ = os.Remove(enrollmentAPKPath)
	e.scanner.SetEnabled(true)
}

func bluetoothConfigPath() string {
	for _, path := range []string{
		"/data/misc/bluedroid/bt_config.conf",
		"/data/misc/bluetooth/bt_config.conf",
	} {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func readBluetoothIdentityKeys() map[string]string {
	path := bluetoothConfigPath()
	if path == "" {
		return map[string]string{}
	}
	file, err := os.Open(path)
	if err != nil {
		return map[string]string{}
	}
	defer file.Close()
	return parseBluetoothIdentityKeys(file)
}

func parseBluetoothIdentityKeys(file *os.File) map[string]string {
	return scanBluetoothIdentityKeys(bufio.NewScanner(file))
}

func scanBluetoothIdentityKeys(scanner *bufio.Scanner) map[string]string {
	keys := make(map[string]string)
	section := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) != "LE_KEY_PID" || !bluetoothAddress.MatchString(section) {
			continue
		}
		value := strings.TrimSpace(parts[1])
		if len(value) < 32 || len(value)%2 != 0 || !hexKeyPattern.MatchString(value) {
			continue
		}
		keys[strings.ToUpper(section)] = strings.ToLower(value)
	}
	return keys
}

func newBluetoothIdentityKey(before, after map[string]string) (string, string) {
	for address, value := range after {
		if before[address] == value || len(value) < 32 {
			continue
		}
		// tBTM_LE_PID_KEYS stores the peer IRK as its first 16 bytes.
		return address, value[:32]
	}
	return "", ""
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func intValue(value any, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(stringValue(value)))
	if err != nil {
		return fallback
	}
	return parsed
}
