//go:build linux

package main

import (
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

var screenSettingsMu sync.Mutex

func initialScreenSettings(volume int) map[string]any {
	values := show.ScreenSettingDefaults()
	values["volume_percent"] = volume
	return values
}

func screenSettingsPath() string {
	if path := os.Getenv("TATER_SCREEN_SETTINGS_PATH"); path != "" {
		return path
	}
	return "/data/local/etc/tater/screen-settings.json"
}

func screenDeviceAddress() string {
	addresses, _ := net.InterfaceAddrs()
	for _, address := range addresses {
		if network, ok := address.(*net.IPNet); ok && !network.IP.IsLoopback() && network.IP.To4() != nil {
			return network.IP.String()
		}
	}
	return "Unavailable"
}

func updateScreenPreferences(screen *show.Server, values map[string]any) {
	if screen == nil {
		return
	}
	screenSettingsMu.Lock()
	defer screenSettingsMu.Unlock()
	selected := show.CopyScreenSettings(values)
	if len(selected) == 0 {
		return
	}
	screen.Update(func(snapshot *show.Snapshot) {
		if snapshot.Settings == nil {
			snapshot.Settings = show.ScreenSettingDefaults()
		}
		for key, value := range selected {
			snapshot.Settings[key] = value
		}
		snapshot.SettingsError = ""
		if value, ok := selected["volume_percent"]; ok {
			snapshot.VolumePercent = int(nativeNumber(value, float64(snapshot.VolumePercent)))
		}
		if value, ok := selected["display_theme"]; ok {
			snapshot.DisplayTheme = normalizeDisplayTheme(value)
		}
		if value, ok := selected["display_edge_glow"]; ok {
			snapshot.DisplayEdgeGlow = nativeBool(value, true)
		}
		if value, ok := selected["display_wallpaper_interval_minutes"]; ok {
			snapshot.WallpaperIntervalMinutes = normalizeWallpaperIntervalMinutes(value)
		}
		applyScreenSettings(snapshot, selected)
	})
	if err := show.SaveScreenSettings(screenSettingsPath(), screen.Snapshot().Settings); err != nil {
		screen.Update(func(snapshot *show.Snapshot) { snapshot.SettingsError = "Applied, but could not save preferences" })
	}
}

func applyLocalScreenCommand(screen *show.Server, values map[string]any, apply func(map[string]any) error) error {
	validated, err := show.ValidateScreenSettings(values)
	if err == nil && apply == nil {
		err = fmt.Errorf("settings are not ready")
	}
	if err == nil {
		err = apply(validated)
	}
	if screen != nil && err != nil {
		screen.Update(func(snapshot *show.Snapshot) { snapshot.SettingsError = err.Error() })
	}
	return err
}

func settingsChangeWake(values map[string]any) bool {
	for key := range values {
		if strings.HasPrefix(key, "wake_") && !strings.HasPrefix(key, "wake_sound") || strings.HasPrefix(key, "oww_") || strings.HasPrefix(key, "nww_") || key == "close_miss_threshold" {
			return true
		}
	}
	return false
}

func settingsChangeCapture(values map[string]any) bool {
	if settingsChangeWake(values) {
		return true
	}
	for key := range values {
		if strings.HasPrefix(key, "aec_") || key == "mic_gain_db" || key == "beamforming_enabled" || key == "vad_threshold" {
			return true
		}
	}
	return false
}

func settingsChangeOutput(values map[string]any) bool {
	for key := range values {
		if strings.HasPrefix(key, "eq") || strings.HasPrefix(key, "bass") || strings.HasPrefix(key, "limiter") {
			return true
		}
	}
	return false
}

var screenDaemonStarted = time.Now()
