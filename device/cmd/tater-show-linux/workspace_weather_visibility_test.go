//go:build linux

package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

func TestWorkspaceHomeConditionsFollowWeatherVisibility(t *testing.T) {
	for _, test := range []struct {
		name, temperature, want string
	}{
		{"not selected", "", "Feels like 66°|Room humidity 42%|Wind NW 5 mph"},
		{"blank", " \t", "Feels like 66°|Room humidity 42%|Wind NW 5 mph"},
		{"selected", "68°", "Sunny|Feels like 66°|Room humidity 42%|Wind NW 5 mph"},
		{"zero degrees", "0°", "Sunny|Feels like 66°|Room humidity 42%|Wind NW 5 mph"},
	} {
		t.Run(test.name, func(t *testing.T) {
			weather := &show.Weather{TemperatureText: test.temperature, Condition: "Sunny",
				FeelsLikeText: "66°", IndoorHumidityText: "42%", WindText: "NW 5 mph"}
			if got := strings.Join(workspaceHomeReadings(weather), "|"); got != test.want {
				t.Fatalf("footer = %q, want %q", got, test.want)
			}
		})
	}
	if len(workspaceHomeReadings(&show.Weather{Condition: "Sunny"})) != 0 {
		t.Fatal("unselected weather leaves a condition-only footer")
	}
}

func TestGPUHomeClearsConditionWhenWeatherIsRemoved(t *testing.T) {
	for _, target := range []string{"checkers", "cronos"} {
		t.Run(target, func(t *testing.T) {
			r, canvas, now := gpuTestRenderer(t)
			r.target = target
			r.state.Weather = &show.Weather{TemperatureText: "68°", Condition: "Sunny", ConditionKind: "sun",
				IndoorTemperatureText: "72°", IndoorHumidityText: "42%"}
			if first, ok := r.prepareGPUScene(canvas, now, true); !ok || len(first.source) == 0 {
				t.Fatal("initial weather home was not uploaded")
			}
			// The host may retain condition metadata while the outdoor slot is
			// disabled. Both the weather block and condition footer must clear.
			r.state.Weather.TemperatureText = ""
			next, ok := r.prepareGPUScene(canvas, now.Add(time.Millisecond), true)
			if !ok || !next.present || len(next.source) == 0 {
				t.Fatal("removing weather did not replace the retained GPU home")
			}
			got := append([]byte(nil), next.source...)
			clean, cleanCanvas, _ := gpuTestRenderer(t)
			clean.target, clean.state = target, r.state
			weather := *r.state.Weather
			weather.Condition, weather.ConditionKind = "", ""
			clean.state.Weather = &weather
			want, ok := clean.prepareGPUScene(cleanCanvas, now.Add(time.Millisecond), true)
			if !ok || !bytes.Equal(got, want.source) {
				t.Fatal("hidden weather still draws condition pixels")
			}
		})
	}
}
