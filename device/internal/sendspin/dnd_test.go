package sendspin

import (
	"path/filepath"
	"testing"
	"time"
)

func TestBlockedPlayerDropsAudioAndPresentation(t *testing.T) {
	client, err := New(Config{StorePath: filepath.Join(t.TempDir(), "sendspin.json")})
	if err != nil {
		t.Fatal(err)
	}
	client.player.push(nowUs(), make([]int16, 960))
	client.updateGroupPresentation(groupUpdate{PlaybackState: "playing", GroupName: "Bedroom"})
	if !client.Active() || !client.NowPlaying().Active {
		t.Fatal("test player did not become active")
	}

	client.SetBlocked(true)
	if client.Active() {
		t.Fatal("blocked player remained active")
	}
	if client.Fill(make([]byte, 3840), time.Now(), 0) {
		t.Fatal("blocked player supplied audio")
	}
	if now := client.NowPlaying(); now.Active || now.Title != "" {
		t.Fatalf("blocked player exposed presentation: %#v", now)
	}

	client.SetBlocked(false)
	if client.blocked.Load() {
		t.Fatal("player remained blocked")
	}
}
