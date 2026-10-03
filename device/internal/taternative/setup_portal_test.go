package taternative

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/wifi"
)

func setupTestOptions(t *testing.T) (SetupPortalOptions, string) {
	t.Helper()
	dir := t.TempDir()
	restarted := make(chan struct{}, 1)
	return SetupPortalOptions{
		WiFiPath:      filepath.Join(dir, "emos", "wpa.conf"),
		BootstrapPath: filepath.Join(dir, "tater", "native.json"),
		TokenPath:     filepath.Join(dir, "tater", "device_token"),
		MarkerPath:    filepath.Join(dir, "tater", "setup_enabled"),
		RestartDelay:  time.Nanosecond,
		Restart:       func() { restarted <- struct{}{} },
	}, dir
}

func TestSetupPortalSavesWiFiAndNativeConfig(t *testing.T) {
	opts, _ := setupTestOptions(t)
	if err := os.MkdirAll(filepath.Dir(opts.TokenPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(opts.TokenPath, []byte("old-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"ssid":     {`Kim's "WiFi"`},
		"password": {`pa"ss\word`},
		"server":   {"http://10.4.20.210:8501"},
		"token":    {"123 456"},
		"name":     {"Family Echo"},
		"room":     {"Family Room"},
	}
	req := httptest.NewRequest(http.MethodPost, "/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	NewSetupPortalHandler(opts).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("save returned %d: %s", rec.Code, rec.Body.String())
	}
	wpa, err := os.ReadFile(opts.WiFiPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"ctrl_interface=/data/emos/sockets",
		`ssid="Kim's "WiFi""`,
		`psk="pa"ss\word"`,
	} {
		if !strings.Contains(string(wpa), want) {
			t.Fatalf("WiFi config missing %q:\n%s", want, wpa)
		}
	}
	var cfg Bootstrap
	raw, err := os.ReadFile(opts.BootstrapPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.URL != "ws://10.4.20.210:8501/api/tater/satellite/v1/ws" ||
		cfg.Token != "123456" || cfg.DeviceName != "Family Echo" || cfg.Room != "Family Room" {
		t.Fatalf("unexpected native config: %+v", cfg)
	}
	if _, err := os.Stat(opts.TokenPath); !os.IsNotExist(err) {
		t.Fatalf("old permanent token still exists: %v", err)
	}
	for _, path := range []string{opts.WiFiPath, opts.BootstrapPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o, want 600", path, info.Mode().Perm())
		}
	}
}

func TestSetupPortalRejectsInvalidValuesWithoutSaving(t *testing.T) {
	opts, _ := setupTestOptions(t)
	for _, form := range []url.Values{
		{"ssid": {"Home"}, "password": {"short"}, "server": {"tater.local:8501"}, "token": {"123456"}},
		{"ssid": {"Home"}, "password": {"password"}, "server": {"file:///tmp/no"}, "token": {"123456"}},
		{"ssid": {"Home"}, "password": {"password"}, "server": {"tater.local:8501"}, "token": {""}},
	} {
		req := httptest.NewRequest(http.MethodPost, "/save", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		NewSetupPortalHandler(opts).ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid form returned %d", rec.Code)
		}
		if _, err := os.Stat(opts.WiFiPath); !os.IsNotExist(err) {
			t.Fatalf("invalid form wrote WiFi config: %v", err)
		}
	}
}

func TestSetupPortalServesEveryCaptiveProbe(t *testing.T) {
	opts, _ := setupTestOptions(t)
	if err := os.MkdirAll(filepath.Dir(opts.BootstrapPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(opts.BootstrapPath, []byte(`{"url":"http://tater.local:8501","device_name":"<Echo>"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/generate_204", "/hotspot-detect.html", "/ncsi.txt", "/anything"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		NewSetupPortalHandler(opts).ServeHTTP(rec, req)
		body, _ := io.ReadAll(rec.Result().Body)
		if rec.Code != http.StatusOK || !strings.Contains(string(body), "Echo Satellite Setup") {
			t.Fatalf("GET %s = %d, body %q", path, rec.Code, body)
		}
		if strings.Contains(string(body), "<Echo>") {
			t.Fatal("device name was not HTML escaped")
		}
	}
}

func TestCheckersSetupPortalShowsItsNetworkAndSavesLinuxWiFi(t *testing.T) {
	opts, _ := setupTestOptions(t)
	opts.DeviceLabel = "Echo Show 5"
	opts.SetupSSID = "Tater-Setup-D4B7"
	opts.SetupAddress = "192.168.4.1"
	opts.WiFiConfig = wifi.LinuxProvisioningConfig
	opts.BootstrapLast = true
	page := httptest.NewRecorder()
	NewSetupPortalHandler(opts).ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/", nil))
	for _, want := range []string{"Echo Show 5", "Tater-Setup-D4B7", "192.168.4.1"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("setup page missing %q", want)
		}
	}
	form := url.Values{"ssid": {"Home"}, "password": {"test-password"}, "server": {"http://10.4.20.210:8501"}, "token": {"123456"}}
	req := httptest.NewRequest(http.MethodPost, "/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	NewSetupPortalHandler(opts).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("save returned %d: %s", rec.Code, rec.Body.String())
	}
	config, err := os.ReadFile(opts.WiFiPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), "ctrl_interface=/run/wpa") {
		t.Fatalf("wrong station control socket: %s", config)
	}
}

func TestCheckersSetupDoesNotMarkReadyIfWiFiWriteFails(t *testing.T) {
	opts, dir := setupTestOptions(t)
	opts.BootstrapLast = true
	opts.WiFiConfig = wifi.LinuxProvisioningConfig
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, []byte("file, not directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts.WiFiPath = filepath.Join(blocked, "wpa.conf")
	form := url.Values{"ssid": {"Home"}, "password": {"test-password"}, "server": {"http://10.4.20.210:8501"}, "token": {"123456"}}
	req := httptest.NewRequest(http.MethodPost, "/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	NewSetupPortalHandler(opts).ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("save returned %d", rec.Code)
	}
	if _, err := os.Stat(opts.BootstrapPath); !os.IsNotExist(err) {
		t.Fatalf("failed setup marked device ready: %v", err)
	}
}
