package show

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestScreenDiagnosticsCommandRoundTrips(t *testing.T) {
	want := Command{
		Protocol: ProtocolVersion, Type: "command", Action: "screen.diagnostics",
		Diagnostics: &ScreenDiagnostics{LayoutTarget: "crown", Width: 1280, Height: 800, FPS: 29.8, RenderAvgMS: 4.2, PresentAvgMS: 18.1, UpdatedAtMS: 1234},
	}
	body, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got Command
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Diagnostics == nil || got.Diagnostics.LayoutTarget != want.Diagnostics.LayoutTarget || got.Diagnostics.Width != want.Diagnostics.Width || got.Diagnostics.Height != want.Diagnostics.Height || got.Diagnostics.FPS != want.Diagnostics.FPS || got.Diagnostics.PresentAvgMS != want.Diagnostics.PresentAvgMS {
		t.Fatalf("screen diagnostics command = %#v, want %#v", got, want)
	}
}

func TestSnapshotIsBoundedAndNormalized(t *testing.T) {
	target := 72.0
	current := 71.0
	s := New("127.0.0.1:0", Snapshot{
		Phase: "mystery", VolumePercent: 150, AudioLevel: -2,
		WallpaperIntervalMinutes: 360,
		Weather:                  &Weather{TemperatureText: "74°", Condition: "Partly cloudy", Daylight: "night"},
		Climate:                  &Climate{Available: true, RoomID: "living_room", RoomName: "Living Room", CurrentTemperature: &current, TargetTemperature: &target, TemperatureUnit: "F", CanSetTemperature: true, RoomTemperatures: []RoomTemperature{{RoomName: "Kitchen", Temperature: 73, TemperatureUnit: "F"}}},
		Timer:                    &Timer{Active: true, ID: "tea", Label: "Tea", Count: 1, RemainingMS: 120000},
		TaterTimeUnixMS:          1_799_999_999_000, TaterUTCOffset: -18000, TaterTimezone: "CST",
	}, nil)
	got := s.Snapshot()
	if got.Protocol != ProtocolVersion || got.Type != "snapshot" {
		t.Fatalf("protocol envelope = %#v", got)
	}
	if got.Phase != "idle" || got.VolumePercent != 100 || got.AudioLevel != 0 {
		t.Fatalf("normalized snapshot = %#v", got)
	}
	if got.WallpaperIntervalMinutes != 360 {
		t.Fatalf("wallpaper interval = %d, want 360", got.WallpaperIntervalMinutes)
	}
	if got.Weather == nil || got.Weather.TemperatureText != "74°" || got.Weather.Condition != "Partly cloudy" || got.Weather.Daylight != "night" {
		t.Fatalf("weather snapshot = %#v", got.Weather)
	}
	if got.Climate == nil || !got.Climate.Available || got.Climate.RoomID != "living_room" || got.Climate.TargetTemperature == nil || *got.Climate.TargetTemperature != 72 || len(got.Climate.RoomTemperatures) != 1 {
		t.Fatalf("climate snapshot = %#v", got.Climate)
	}
	if got.Timer == nil || got.Timer.ID != "tea" || got.Timer.RemainingMS != 120000 {
		t.Fatalf("timer snapshot = %#v", got.Timer)
	}
	if got.TaterTimeUnixMS != 1_799_999_999_000 || got.TaterUTCOffset != -18000 || got.TaterTimezone != "CST" {
		t.Fatalf("Tater clock = %#v", got)
	}
}

func TestSnapshotPreservesToolCallPresentation(t *testing.T) {
	s := New("127.0.0.1:0", Snapshot{
		Phase: "tool_call", Connected: true, DeviceName: "Kitchen Show",
		ToolName: "room_vision", ToolMessage: "I’m taking a quick look now.",
	}, nil)
	got := s.Snapshot()
	if got.Phase != "tool_call" {
		t.Fatalf("phase = %q, want tool_call", got.Phase)
	}
	if got.ToolName != "room_vision" || got.ToolMessage != "I’m taking a quick look now." {
		t.Fatalf("tool presentation = %#v", got)
	}
}

func TestNotificationImageUsesLoopbackSideChannel(t *testing.T) {
	s := New("127.0.0.1:0", Snapshot{Phase: "idle"}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for s.Address() == "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.Address() == "" {
		t.Fatal("server did not bind")
	}

	image := []byte{0xff, 0xd8, 0xff, 0xd9}
	s.SetNotification(&Notification{
		ID: "event-1", CameraName: "Front Door", Description: "Package delivered",
		ExpiresAtUnixMS: time.Now().Add(time.Minute).UnixMilli(),
	}, image, "image/jpeg")
	snapshot := s.Snapshot()
	if snapshot.Notification == nil || snapshot.Notification.ImageURL == "" {
		t.Fatalf("notification snapshot = %#v", snapshot.Notification)
	}
	response, err := http.Get(snapshot.Notification.ImageURL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	got, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || string(got) != string(image) {
		t.Fatalf("image response status=%d body=%x", response.StatusCode, got)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not stop")
	}
}

func TestLastEventImageUsesIndependentLoopbackSideChannel(t *testing.T) {
	s := New("127.0.0.1:0", Snapshot{Phase: "idle"}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for s.Address() == "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.Address() == "" {
		t.Fatal("server did not bind")
	}

	lastImage := []byte{0xff, 0xd8, 0x11, 0xff, 0xd9}
	createdAt := time.Now().Add(-5 * time.Minute).UnixMilli()
	s.SetLastEvent(&Notification{
		ID: "last-1", CameraName: "Driveway", Description: "Vehicle detected",
		CreatedAtUnixMS: createdAt, ExpiresAtUnixMS: time.Now().Add(-time.Minute).UnixMilli(),
	}, lastImage, "image/jpeg")
	last := s.Snapshot().LastEvent
	if last == nil || last.ImageURL == "" || last.CreatedAtUnixMS != createdAt || last.ExpiresAtUnixMS != 0 {
		t.Fatalf("last event snapshot = %#v", last)
	}
	response, err := http.Get(last.ImageURL)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || string(got) != string(lastImage) {
		t.Fatalf("last-event image status=%d body=%x err=%v", response.StatusCode, got, err)
	}

	popupImage := []byte{0xff, 0xd8, 0x22, 0xff, 0xd9}
	s.SetNotification(&Notification{ID: "popup-2", CameraName: "Front Door"}, popupImage, "image/jpeg")
	current := s.Snapshot()
	if current.LastEvent == nil || current.LastEvent.ID != "last-1" || current.LastEvent.ImageURL != last.ImageURL {
		t.Fatalf("temporary notification changed retained last event: %#v", current.LastEvent)
	}
	response, err = http.Get(last.ImageURL)
	if err != nil {
		t.Fatal(err)
	}
	got, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(got) != string(lastImage) {
		t.Fatalf("last-event image was overwritten: body=%x err=%v", got, err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not stop")
	}
}

func TestMediaArtworkUsesLoopbackSideChannelAndClears(t *testing.T) {
	s := New("127.0.0.1:0", Snapshot{Phase: "idle"}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for s.Address() == "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.Address() == "" {
		t.Fatal("server did not bind")
	}

	art := []byte{0xff, 0xd8, 0xff, 0xd9}
	s.SetMedia(&Media{Active: true, Title: "Garden Song", Spectrum: []float64{.1, .7}}, art, "image/jpeg")
	media := s.Snapshot().Media
	if media == nil || media.ArtworkURL == "" || media.Title != "Garden Song" {
		t.Fatalf("media snapshot = %#v", media)
	}
	response, err := http.Get(media.ArtworkURL)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || string(got) != string(art) {
		t.Fatalf("media image status=%d body=%x err=%v", response.StatusCode, got, err)
	}

	s.SetMedia(nil, nil, "")
	if s.Snapshot().Media != nil {
		t.Fatal("media state did not clear")
	}
	response, err = http.Get(media.ArtworkURL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("cleared artwork status = %d, want 404", response.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not stop")
	}
}

func TestTaterTubeArtworkUsesLoopbackSideChannelAndSurvivesMetadataRefresh(t *testing.T) {
	s := New("127.0.0.1:0", Snapshot{Phase: "idle"}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for s.Address() == "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.Address() == "" {
		t.Fatal("server did not bind")
	}

	art := []byte{0xff, 0xd8, 0x44, 0xff, 0xd9}
	s.SetTaterTube(&TaterTubeMedia{Active: true, EventID: "event-1", Title: "The Big Lebowski"}, art, "image/jpeg")
	media := s.Snapshot().TaterTube
	if media == nil || media.ArtworkURL == "" {
		t.Fatalf("Tater Tube snapshot = %#v", media)
	}
	artworkURL := media.ArtworkURL
	s.SetTaterTube(&TaterTubeMedia{Active: true, EventID: "event-1", Title: "The Big Lebowski", ProgressMS: 42000}, nil, "")
	media = s.Snapshot().TaterTube
	if media == nil || media.ArtworkURL != artworkURL || media.ProgressMS != 42000 {
		t.Fatalf("metadata refresh lost artwork: %#v", media)
	}
	response, err := http.Get(media.ArtworkURL)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || string(got) != string(art) {
		t.Fatalf("Tater Tube image status=%d body=%x err=%v", response.StatusCode, got, err)
	}

	s.SetTaterTube(nil, nil, "")
	if s.Snapshot().TaterTube != nil {
		t.Fatal("Tater Tube state did not clear")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not stop")
	}
}

func TestSatChatVideoUsesLoopbackSideChannelAndClears(t *testing.T) {
	s := New("127.0.0.1:0", Snapshot{Phase: "idle", SatChat: &SatChat{}}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for s.Address() == "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.Address() == "" {
		t.Fatal("server did not bind")
	}

	frame := []byte{0xff, 0xd8, 0x56, 0x49, 0x44, 0xff, 0xd9}
	s.SetSatChatVideo(frame)
	videoURL := s.Snapshot().SatChat.VideoFrameURL
	if videoURL == "" {
		t.Fatal("Sat Chat video URL was not published")
	}
	response, err := http.Get(videoURL)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "image/jpeg" || string(got) != string(frame) {
		t.Fatalf("Sat Chat frame status=%d type=%q body=%x err=%v", response.StatusCode, response.Header.Get("Content-Type"), got, err)
	}

	s.SetSatChatVideo(nil)
	if got := s.Snapshot().SatChat.VideoFrameURL; got != "" {
		t.Fatalf("cleared Sat Chat video URL = %q", got)
	}
	response, err = http.Get(videoURL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("cleared Sat Chat frame status = %d, want 404", response.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not stop")
	}
}

func TestServerSendsCompleteUpdatesAndReceivesCommands(t *testing.T) {
	commands := make(chan Command, 1)
	s := New("127.0.0.1:0", Snapshot{Phase: "offline", DeviceName: "Test Show"}, func(c Command) {
		commands <- c
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for s.Address() == "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.Address() == "" {
		t.Fatal("server did not bind")
	}
	conn, err := net.DialTimeout("tcp", s.Address(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	read := func() Snapshot {
		t.Helper()
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		line, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		var value Snapshot
		if err := json.Unmarshal(line, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	if got := read(); got.DeviceName != "Test Show" || got.Phase != "offline" {
		t.Fatalf("initial snapshot = %#v", got)
	}
	s.Update(func(value *Snapshot) {
		value.Phase = "listening"
		value.Connected = true
		value.VolumePercent = 64
	})
	if got := read(); got.Phase != "listening" || !got.Connected || got.VolumePercent != 64 {
		t.Fatalf("updated snapshot = %#v", got)
	}

	if _, err := conn.Write([]byte(`{"protocol":1,"type":"command","action":"intercom.start"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-commands:
		if got.Action != "intercom.start" || got.Protocol != ProtocolVersion {
			t.Fatalf("command = %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("command not delivered")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not stop")
	}
}
