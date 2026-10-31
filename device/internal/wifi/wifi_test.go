package wifi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/platform"
)

func onBase(t *testing.T, base, body string) {
	t.Helper()
	dir := t.TempDir()
	oldBase, oldAndroid, oldLinux, oldNet := baseOS, androidConfP, linuxConfP, linuxNetP
	oldChown := chownFile
	t.Cleanup(func() {
		baseOS, androidConfP, linuxConfP, linuxNetP = oldBase, oldAndroid, oldLinux, oldNet
		chownFile = oldChown
	})
	baseOS = func() string { return base }
	androidConfP = filepath.Join(dir, "android-wpa.conf")
	linuxConfP = filepath.Join(dir, "linux-wpa.conf")
	linuxNetP = filepath.Join(dir, "missing-tater-net")
	path := androidConfP
	if base == platform.TaterLinux {
		path = linuxConfP
	}
	if body != "" {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	chownFile = func(string, int, int) error { return nil }
}

func TestConfPathsArePlatformPrivate(t *testing.T) {
	onBase(t, platform.FireOS, "ctrl_interface=/data/misc/wifi/sockets\n")
	if read, write := confPaths(); read != androidConfP || write != androidConfP {
		t.Fatalf("Fire OS paths = %q, %q", read, write)
	}
	onBase(t, platform.TaterLinux, "ctrl_interface=/run/wpa\n")
	if read, write := confPaths(); read != linuxConfP || write != linuxConfP {
		t.Fatalf("Tater Linux paths = %q, %q", read, write)
	}
}

func TestSockDirReadsActiveConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, base, body, want string
	}{
		{"Fire OS DIR form", platform.FireOS, "ctrl_interface=DIR=/data/misc/wifi/sockets GROUP=wifi\n", "/data/misc/wifi/sockets"},
		{"Tater Linux", platform.TaterLinux, "update_config=0\nctrl_interface=/run/wpa\n", "/run/wpa"},
		{"last declaration wins", platform.TaterLinux, "ctrl_interface=/first\nctrl_interface=/run/wpa\n", "/run/wpa"},
		{"relative ignored", platform.TaterLinux, "ctrl_interface=wpa_ctrl\n", linuxSock},
	} {
		t.Run(tc.name, func(t *testing.T) {
			onBase(t, tc.base, tc.body)
			if got := sockDir(); got != tc.want {
				t.Fatalf("sockDir = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestComposeConfForTaterLinux(t *testing.T) {
	onBase(t, platform.TaterLinux, "ctrl_interface=/run/wpa\n")
	conf := composeConf([]byte("Home"), "12345678")
	for _, want := range []string{"ctrl_interface=/run/wpa", "update_config=0", `ssid="Home"`, `psk="12345678"`, "key_mgmt=WPA-PSK"} {
		if !strings.Contains(conf, want) {
			t.Errorf("configuration missing %q:\n%s", want, conf)
		}
	}
	for _, omitted := range []string{"driver_param=", "device_name=", "p2p_no_group_iface="} {
		if strings.Contains(conf, omitted) {
			t.Errorf("Linux configuration carries Android field %q", omitted)
		}
	}
}

func TestWriteConfUsesPrivateLinuxMode(t *testing.T) {
	onBase(t, platform.TaterLinux, "")
	if err := writeConf("network={\n}\n"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(linuxConfP)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %#o, want 0600", got)
	}
	if got := backupPath(); got != linuxConfP+".tater-bak" {
		t.Fatalf("backup path = %q", got)
	}
}

func TestSSIDFromWifiDumpUsesOnlyLiveWifiInfo(t *testing.T) {
	for _, tc := range []struct{ name, dump, want string }{
		{"connected", "rec[0]: SSID: stale state: COMPLETED\nmWifiInfo SSID: PhooeyFi, BSSID: da:b3:70:34:9d:ef\n", "PhooeyFi"},
		{"quoted", "mWifiInfo SSID: \"Kitchen WiFi\", BSSID: 00:11:22:33:44:55\n", "Kitchen WiFi"},
		{"disconnected", "rec[0]: SSID: PhooeyFi state: COMPLETED\nmWifiInfo SSID: , BSSID: 00:00:00:00:00:00\n", ""},
		{"unknown", "mWifiInfo SSID: <unknown ssid>, BSSID: 00:00:00:00:00:00\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(ssidFromWifiDump(tc.dump)); got != tc.want {
				t.Fatalf("ssidFromWifiDump = %q, want %q", got, tc.want)
			}
		})
	}
}
