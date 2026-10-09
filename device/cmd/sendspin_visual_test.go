package main

import (
	"testing"
	"time"
)

func TestSendspinScreenPollMatchesDisplayCadence(t *testing.T) {
	if sendspinScreenPollInterval != time.Second/30 {
		t.Fatalf("screen poll interval = %s, want %s", sendspinScreenPollInterval, time.Second/30)
	}
	if sendspinPollInterval != 100*time.Millisecond {
		t.Fatalf("non-screen poll interval = %s, want 100ms", sendspinPollInterval)
	}
}

func TestShouldShowSendspinMusicVisual(t *testing.T) {
	tests := []struct {
		name                  string
		target, state         string
		active, down, ringing bool
		want                  bool
	}{
		{name: "biscuit idle playback", target: "biscuit", state: "idle", active: true, want: true},
		{name: "biscuit playing state", target: "BISCUIT", state: "playing", active: true, want: true},
		{name: "radar ring", target: "RADAR", state: "idle", active: true, want: true},
		{name: "stopped", target: "biscuit", state: "idle", want: false},
		{name: "reply owns ring", target: "biscuit", state: "speaking", active: true, want: false},
		{name: "timer owns ring", target: "biscuit", state: "idle", active: true, ringing: true, want: false},
		{name: "link owns ring", target: "biscuit", state: "idle", active: true, down: true, want: false},
		{name: "other echo", target: "checkers", state: "idle", active: true, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := shouldShowSendspinMusicVisual(test.target, test.active, test.down, test.ringing, test.state)
			if got != test.want {
				t.Fatalf("shouldShowSendspinMusicVisual() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestVoiceStatesAcceptNoAnimation(t *testing.T) {
	nativeVisuals.Lock()
	previousListening := nativeVisuals.listening
	previousThinking := nativeVisuals.thinking
	previousTool := nativeVisuals.tool
	previousReplying := nativeVisuals.replying
	nativeVisuals.listening = "off"
	nativeVisuals.thinking = "off"
	nativeVisuals.tool = "off"
	nativeVisuals.replying = "off"
	nativeVisuals.Unlock()
	t.Cleanup(func() {
		nativeVisuals.Lock()
		nativeVisuals.listening = previousListening
		nativeVisuals.thinking = previousThinking
		nativeVisuals.tool = previousTool
		nativeVisuals.replying = previousReplying
		nativeVisuals.Unlock()
	})

	for _, state := range []string{"listening", "thinking", "tool_call", "speaking"} {
		t.Run(state, func(t *testing.T) {
			if pattern := nativeStateAnimation(state).Pattern; pattern != "off" {
				t.Fatalf("nativeStateAnimation(%q) pattern = %q, want off", state, pattern)
			}
		})
	}
}

func TestSendspinMusicKeepsSelectedEffectAndUsesTrackPalette(t *testing.T) {
	nativeVisuals.Lock()
	previousMusic := nativeVisuals.music
	previousBrightness := nativeVisuals.brightness
	nativeVisuals.music = "music_wave"
	nativeVisuals.brightness = 80
	nativeVisuals.Unlock()
	t.Cleanup(func() {
		nativeVisuals.Lock()
		nativeVisuals.music = previousMusic
		nativeVisuals.brightness = previousBrightness
		nativeVisuals.Unlock()
	})

	spec := nativeMusicAnimation([3]uint8{12, 18, 24}, [3]uint8{30, 210, 72})
	if spec.Pattern != "music_wave" || !spec.Music {
		t.Fatalf("music spec = %#v, want selected effect with Sendspin visualizer input", spec)
	}
	if len(spec.Colors) != 1 || spec.Colors[0][1] <= spec.Colors[0][0] || spec.Colors[0][1] <= spec.Colors[0][2] {
		t.Fatalf("music spec did not use track palette: %#v", spec.Colors)
	}
}
