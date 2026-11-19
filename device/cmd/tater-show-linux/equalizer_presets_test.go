//go:build linux

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/linuxinput"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

func equalizerFixture(t *testing.T, target string) (*renderer, *image.RGBA) {
	t.Helper()
	r, _, _ := gpuTestRenderer(t)
	r.target, r.spotScreen = target, target == "rook"
	switch target {
	case "crown", "athena":
		r.screenWidth, r.screenHeight = 1280, 800
	case "rook":
		r.screenWidth, r.screenHeight = 480, 480
	}
	r.settingsOpen, r.settingsPage = true, "equalizer"
	r.state.Settings = show.ScreenSettingDefaults()
	w, h := r.showSize()
	return r, image.NewRGBA(image.Rect(0, 0, w, h))
}

func TestEqualizerPresetSelectionAndManualEditsAcrossAllScreens(t *testing.T) {
	for _, target := range []string{"checkers", "cronos", "crown", "athena", "rook"} {
		t.Run(target, func(t *testing.T) {
			r, canvas := equalizerFixture(t, target)
			w, h := canvas.Rect.Dx(), canvas.Rect.Dy()
			box := equalizerPresetBounds(w, h)
			if r.spotScreen {
				box = rookEqualizerPresetBounds()
			}
			if r.equalizerPresetID() != "warm" {
				t.Fatal("new device does not start with Warm selected")
			}
			for index, option := range equalizerPresetOptions() {
				button := equalizerPresetButton(box, index)
				if !button.In(canvas.Rect) || button.Dx() < 60 || button.Dy() < 40 {
					t.Fatal("preset is not a visible touch target", button)
				}
				if r.spotScreen {
					for _, point := range []image.Point{button.Min, button.Max, image.Pt(button.Min.X, button.Max.Y), image.Pt(button.Max.X, button.Min.Y)} {
						if math.Hypot(float64(point.X-240), float64(point.Y-240)) > rookPanelRadius {
							t.Fatal("preset button is clipped by the round panel", button)
						}
					}
				}
				before := append([]float64(nil), r.state.Settings["eq_bands"].([]float64)...)
				var wire bytes.Buffer
				client := &socketClient{writer: bufio.NewWriter(&wire)}
				event := linuxinput.TouchEvent{Kind: linuxinput.Down, X: button.Min.X + button.Dx()/2, Y: button.Min.Y + button.Dy()/2}
				r.handleSettingsTouch(client, event, w, h)
				if wire.Len() != 0 {
					t.Fatal("preset applied before release")
				}
				event.Kind = linuxinput.Up
				r.handleSettingsTouch(client, event, w, h)
				var command show.Command
				if err := json.Unmarshal(bytes.TrimSpace(wire.Bytes()), &command); err != nil || command.Settings["eq_preset"] != option.ID || r.equalizerPresetID() != option.ID {
					t.Fatal("preset tap did not update UI and native request", option.ID, command, err)
				}
				if option.ID == "manual" {
					if len(command.Settings) != 1 || !reflect.DeepEqual(before, r.state.Settings["eq_bands"]) {
						t.Fatal("Manual changed the current curve")
					}
				} else if len(command.Settings) != 3 || !reflect.DeepEqual(r.state.Settings["eq_bands"], option.Bands[:]) || command.Settings["eq_loudness"] != false {
					t.Fatal("preset did not commit all eight bands and boost atomically", command)
				}
				for key := range command.Settings {
					if key != "eq_preset" && key != "eq_bands" && key != "eq_loudness" {
						t.Fatal("preset changed a non-EQ setting", key)
					}
				}
				r.drawWorkspaceSettings(canvas)
			}
			// Legacy user curves stay Manual, including an all-zero saved curve.
			r.state.Settings = map[string]any{"eq_bands": make([]float64, 8), "eq_loudness": false}
			if r.equalizerPresetID() != "manual" {
				t.Fatal("legacy saved EQ was auto-assigned a named preset")
			}
			r.commitSettings(nil, map[string]any{"eq_preset": "warm"})
			track := equalizerTrackBounds(w, h, 0)
			if r.spotScreen {
				track = rookEqualizerTrack(0)
			}
			event := linuxinput.TouchEvent{Kind: linuxinput.Down, X: track.Min.X, Y: track.Min.Y}
			r.handleSettingsTouch(nil, event, w, h)
			if r.equalizerPresetID() != "manual" || r.state.Settings["eq_preset"] != "warm" {
				t.Fatal("drag did not preview Manual without an early commit")
			}
			event.Kind = linuxinput.Up
			r.handleSettingsTouch(nil, event, w, h)
			if r.state.Settings["eq_preset"] != "manual" || settingFloat(r.settingValue("eq_band_0")) != 12 {
				t.Fatal("fader release did not persist Manual with the edited band")
			}
		})
	}
}

func TestEqualizerPresetNativePreviews(t *testing.T) {
	directory := os.Getenv("TATER_EQ_PREVIEWS")
	if directory == "" {
		t.Skip("set TATER_EQ_PREVIEWS to render production EQ screens")
	}
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"checkers", "cronos", "crown", "athena", "rook"} {
		for _, id := range []string{"warm", "manual"} {
			r, canvas := equalizerFixture(t, target)
			if id == "manual" {
				r.state.Settings["eq_preset"] = "manual"
				r.state.Settings["eq_bands"] = []float64{1, 2, 0, -1, 0, 2, 1, 0}
			}
			pages := 1
			if r.spotScreen {
				pages = 2
			}
			for page := 0; page < pages; page++ {
				r.settingsIndex = page
				r.drawWorkspaceSettings(canvas)
				file, err := os.Create(filepath.Join(directory, fmt.Sprintf("eq-%s-%s-%d.png", target, id, page+1)))
				if err != nil {
					t.Fatal(err)
				}
				err, closeErr := png.Encode(file, canvas), file.Close()
				if err != nil || closeErr != nil {
					t.Fatal(err, closeErr)
				}
			}
		}
	}
}
