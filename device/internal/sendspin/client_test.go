package sendspin

import (
	"encoding/binary"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestPresentationRolesAreScopedToEchoHardware(t *testing.T) {
	screen, err := New(Config{StorePath: filepath.Join(t.TempDir(), "screen.json"), Screen: true})
	if err != nil {
		t.Fatal(err)
	}
	hello := screen.hello()
	for _, role := range []string{rolePlayer, roleMetadata, roleArtwork, roleColor, roleVisualizer} {
		if !containsString(hello.SupportedRoles, role) {
			t.Fatalf("screen hello lacks %s: %v", role, hello.SupportedRoles)
		}
	}
	if hello.ArtworkSupport == nil || len(hello.ArtworkSupport.Channels) != 1 ||
		hello.ArtworkSupport.Channels[0].Format != "jpeg" {
		t.Fatalf("screen artwork support = %#v", hello.ArtworkSupport)
	}
	if hello.VisualizerSupport == nil || hello.VisualizerSupport.Spectrum == nil ||
		hello.VisualizerSupport.Spectrum.DisplayBins != 12 || hello.VisualizerSupport.RateMax != 20 ||
		hello.VisualizerSupport.BufferCapacity != 64*1024 {
		t.Fatalf("screen visualizer support = %#v", hello.VisualizerSupport)
	}

	biscuit, err := New(Config{StorePath: filepath.Join(t.TempDir(), "biscuit.json"), Visuals: true})
	if err != nil {
		t.Fatal(err)
	}
	hello = biscuit.hello()
	if containsString(hello.SupportedRoles, roleMetadata) || containsString(hello.SupportedRoles, roleArtwork) {
		t.Fatalf("Biscuit requested screen-only roles: %v", hello.SupportedRoles)
	}
	if !containsString(hello.SupportedRoles, roleColor) || !containsString(hello.SupportedRoles, roleVisualizer) {
		t.Fatalf("Biscuit lacks music visual roles: %v", hello.SupportedRoles)
	}
	if hello.VisualizerSupport == nil || hello.VisualizerSupport.Spectrum == nil ||
		hello.VisualizerSupport.Spectrum.DisplayBins != 12 || hello.VisualizerSupport.RateMax != 10 ||
		hello.VisualizerSupport.BufferCapacity != 16*1024 {
		t.Fatalf("Biscuit reduced visualizer support = %#v", hello.VisualizerSupport)
	}
}

func TestNowPlayingMergesStateAndAppliesDueVisualizerFrames(t *testing.T) {
	c, err := New(Config{StorePath: filepath.Join(t.TempDir(), "state.json"), Screen: true})
	if err != nil {
		t.Fatal(err)
	}
	c.updateGroupPresentation(groupUpdate{PlaybackState: "playing", GroupName: "Everywhere"})
	c.applyMetadata(json.RawMessage(`{
		"timestamp":10,"title":"All Together Now","artist":"The Taters","album":"Garden Songs",
		"progress":{"track_progress":12000,"track_duration":180000,"playback_speed":1000}
	}`))
	c.applyColor(json.RawMessage(`{"timestamp":10,"primary":[12,34,56],"accent":[210,90,30]}`))
	c.applyArtwork([]byte{1, 2, 3}, "image/jpeg")

	frame := make([]byte, 10)
	binary.BigEndian.PutUint64(frame[:8], uint64(nowUs()))
	binary.BigEndian.PutUint16(frame[8:], 32768)
	c.queueVisualizer(msgVisualizerLoudness, frame, 0, nowUs()-1)
	got := c.NowPlaying()
	if !got.Active || got.GroupName != "Everywhere" || got.Title != "All Together Now" ||
		got.ProgressMS < 12000 || got.ProgressMS > 12010 || got.DurationMS != 180000 ||
		got.PrimaryColor != ([3]uint8{12, 34, 56}) {
		t.Fatalf("now playing = %#v", got)
	}
	artwork, contentType := c.NowPlayingArtwork()
	if got.Loudness < .49 || got.Loudness > .51 || string(artwork) != string([]byte{1, 2, 3}) || contentType != "image/jpeg" {
		t.Fatalf("presentation media/visualizer = %#v", got)
	}
	visualizer := c.Status().Visualizer
	if visualizer.Received != 1 || visualizer.Applied != 1 || visualizer.QueueDepth != 0 || visualizer.Revision != 1 {
		t.Fatalf("visualizer diagnostics = %#v, want one received and applied frame", visualizer)
	}
	c.mu.Lock()
	c.presentation.progressUpdatedAt = time.Now().Add(-2 * time.Second)
	c.mu.Unlock()
	advanced := c.NowPlaying().ProgressMS
	if advanced < 13_999 || advanced > 14_010 {
		t.Fatalf("playing progress did not advance from its metadata timestamp: %d", advanced)
	}
	artwork[0] = 9
	if stored, _ := c.NowPlayingArtwork(); stored[0] != 1 {
		t.Fatal("NowPlaying exposed mutable artwork storage")
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// One volume (Wil, 2026-09-30): with the device owning it, a server's volume
// command moves the DEVICE's volume and the synced music itself plays at
// unity, so the level is never applied twice.
func TestServerVolumeMovesTheDeviceVolume(t *testing.T) {
	c, err := New(Config{StorePath: filepath.Join(t.TempDir(), "s.json")})
	if err != nil {
		t.Fatal(err)
	}
	var got []int
	c.OnVolume(func(v int) { got = append(got, v) })

	set := c.store.settings()
	set.Volume = 40
	c.applySettings(set)
	if len(got) != 1 || got[0] != 40 {
		t.Fatalf("device told %v, want [40]", got)
	}
	if c.player.gainTarget != unity {
		t.Errorf("synced music attenuated by the server volume as well: gain %d", c.player.gainTarget)
	}

	// The device echoing its new level back is not a change.
	c.SetVolume(40)
	if c.store.settings().Volume != 40 || len(got) != 1 {
		t.Error("the echo of a server's own command looped")
	}
	// A button press is: it becomes what the server is told.
	c.SetVolume(55)
	if c.store.settings().Volume != 55 {
		t.Error("device volume not recorded for the server")
	}

	// Mute stays the player's own: the Echo's mute button is the mic.
	set = c.store.settings()
	set.Muted = true
	c.applySettings(set)
	if c.player.gainTarget != 0 {
		t.Error("server mute did not silence synced music")
	}
}

// Without the device owning volume (the interop harness), the server volume
// is a gain on synced music, as before.
func TestWithoutADeviceVolumeTheServerVolumeIsAGain(t *testing.T) {
	c, _ := New(Config{StorePath: filepath.Join(t.TempDir(), "s.json")})
	set := c.store.settings()
	set.Volume = 50
	c.applySettings(set)
	if c.player.gainTarget != volumeGain(50, false) {
		t.Errorf("gain %d, want %d", c.player.gainTarget, volumeGain(50, false))
	}
}

// The Echo's scale is 0..127 (unity at 127); HA maps it proportionally and
// so must this, so one volume reads one number everywhere.
func TestVolumeMappingRoundTrips(t *testing.T) {
	for pct := 0; pct <= 100; pct++ {
		if got := LevelToPercent(PercentToLevel(pct, 127), 127); got != pct {
			t.Errorf("%d%% -> %d -> %d%%", pct, PercentToLevel(pct, 127), got)
		}
	}
	if PercentToLevel(100, 127) != 127 || LevelToPercent(127, 127) != 100 || PercentToLevel(0, 127) != 0 {
		t.Error("endpoints")
	}
}
