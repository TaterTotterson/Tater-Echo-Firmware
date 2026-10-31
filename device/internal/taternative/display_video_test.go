package taternative

import "testing"

func TestDecodeDisplayVideoBoundsPlaybackProfile(t *testing.T) {
	got := decodeDisplayVideo(map[string]any{
		"active": true, "session_id": "movie-1", "url": "https://media.invalid/movie.m3u8",
		"title": "Movie", "state": "PAUSED", "position_ms": -25.0, "duration_ms": 90_000.0,
		"fps": 60.0, "loop": true, "server_seek": true,
	})
	if !got.Active || got.SessionID != "movie-1" || got.State != "paused" || got.PositionMS != 0 || got.DurationMS != 90_000 {
		t.Fatalf("decoded video = %#v", got)
	}
	if got.FPS != 30 {
		t.Fatalf("unsupported fps became %d, want safe 30 fps", got.FPS)
	}
	if !got.ServerSeek {
		t.Fatal("server-managed seek was not preserved")
	}
}

func TestScreenTargetsAdvertiseFullscreenVideo(t *testing.T) {
	for _, target := range []string{"checkers", "crown", "athena", "rook"} {
		got := CapabilitiesForTarget(target)
		if got["screen_video_playback"] != true || got["screen_video_playback_version"] != 1 {
			t.Fatalf("%s fullscreen video capabilities = %#v", target, got)
		}
	}
	if got := CapabilitiesForTarget("rook")["screen_video_transcode_profile"]; got != "echo_rook_480p" {
		t.Fatalf("Rook profile = %#v", got)
	}
	if got := CapabilitiesForTarget("checkers")["screen_video_transcode_profile"]; got != "echo_checkers_480p" {
		t.Fatalf("Checkers profile = %#v", got)
	}
	if got := CapabilitiesForTarget("athena")["screen_video_transcode_profile"]; got != "echo_show_720p" {
		t.Fatalf("Athena profile = %#v", got)
	}
	if got := CapabilitiesForTarget("biscuit")["screen_video_playback"]; got != nil {
		t.Fatalf("screenless Biscuit advertised fullscreen video: %#v", got)
	}
}
