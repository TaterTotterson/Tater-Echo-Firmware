package sendspin

import (
	"encoding/binary"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestSessionDispatchesPresentationRoles(t *testing.T) {
	c, err := New(Config{StorePath: filepath.Join(t.TempDir(), "sendspin.json"), Screen: true})
	if err != nil {
		t.Fatal(err)
	}
	s := &session{
		c: c, filter: newTimeFilter(), activated: true,
		roles: []string{rolePlayer, roleMetadata, roleArtwork, roleColor, roleVisualizer},
	}
	c.admitted = s
	dispatch := func(messageType string, payload string) {
		t.Helper()
		if err := s.dispatch(envelope{Type: messageType, Payload: json.RawMessage(payload)}); err != nil {
			t.Fatalf("dispatch %s: %v", messageType, err)
		}
	}
	dispatch("group/update", `{"playback_state":"playing","group_name":"Kitchen"}`)
	dispatch("server/state", `{
		"metadata":{"timestamp":0,"title":"Garden Song","artist":"The Taters"},
		"color":{"timestamp":0,"primary":[20,80,140],"accent":[250,110,40]}
	}`)
	dispatch("stream/start", `{
		"artwork":{"channels":[{"source":"album","format":"jpeg","width":512,"height":512}]},
		"visualizer":{"types":["loudness","spectrum"],"rate_max":20,
			"spectrum":{"n_disp_bins":12,"scale":"mel","f_min":60,"f_max":16000}}
	}`)

	artwork := make([]byte, 12)
	artwork[0] = msgArtwork0
	copy(artwork[9:], []byte{1, 2, 3})
	s.onArtwork(artwork)
	loudness := make([]byte, 11)
	loudness[0] = msgVisualizerLoudness
	binary.BigEndian.PutUint64(loudness[1:9], uint64(nowUs()))
	binary.BigEndian.PutUint16(loudness[9:], 49151)
	s.onVisualizer(loudness)

	got := c.NowPlaying()
	if !got.Active || got.GroupName != "Kitchen" || got.Title != "Garden Song" ||
		got.PrimaryColor != ([3]uint8{20, 80, 140}) || got.Loudness < .74 || got.Loudness > .76 {
		t.Fatalf("presentation = %#v", got)
	}
	image, contentType := c.NowPlayingArtwork()
	if string(image) != string([]byte{1, 2, 3}) || contentType != "image/jpeg" {
		t.Fatalf("artwork = %x %q", image, contentType)
	}

	dispatch("stream/end", `{"roles":["artwork","visualizer"]}`)
	if image, _ := c.NowPlayingArtwork(); len(image) != 0 {
		t.Fatalf("artwork survived stream/end: %x", image)
	}
	if cleared := c.NowPlaying(); cleared.Loudness != 0 || len(cleared.Spectrum) != 0 {
		t.Fatalf("visualizer survived stream/end: %#v", cleared)
	}
}
