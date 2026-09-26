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

func TestSnapshotIsBoundedAndNormalized(t *testing.T) {
	s := New("127.0.0.1:0", Snapshot{
		Phase: "mystery", VolumePercent: 150, AudioLevel: -2,
		Weather:         &Weather{TemperatureText: "74°", Condition: "Partly cloudy"},
		TaterTimeUnixMS: 1_799_999_999_000, TaterUTCOffset: -18000, TaterTimezone: "CST",
	}, nil)
	got := s.Snapshot()
	if got.Protocol != ProtocolVersion || got.Type != "snapshot" {
		t.Fatalf("protocol envelope = %#v", got)
	}
	if got.Phase != "idle" || got.VolumePercent != 100 || got.AudioLevel != 0 {
		t.Fatalf("normalized snapshot = %#v", got)
	}
	if got.Weather == nil || got.Weather.TemperatureText != "74°" || got.Weather.Condition != "Partly cloudy" {
		t.Fatalf("weather snapshot = %#v", got.Weather)
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

	if _, err := conn.Write([]byte(`{"protocol":1,"type":"command","action":"volume.delta","value":8}` + "\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-commands:
		if got.Action != "volume.delta" || got.Value == nil || *got.Value != 8 {
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

func TestServerAcceptsBoundedBLEAndScreenVersionFields(t *testing.T) {
	commands := make(chan Command, 1)
	s := New("127.0.0.1:0", Snapshot{Phase: "idle"}, func(c Command) { commands <- c })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for s.Address() == "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	conn, err := net.DialTimeout("tcp", s.Address(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	if _, err := reader.ReadBytes('\n'); err != nil {
		t.Fatal(err)
	}
	wire := `{"protocol":1,"type":"command","action":"ble.advertisements",` +
		`"app_version":"v0.2.0","adverts_seen":7,"unique_addrs":2,"scanning":true,` +
		`"adverts":[{"address":"aa:bb:cc:dd:ee:ff","address_type":1,` +
		`"event_type":0,"rssi":-58,"data":"020106"}]}` + "\n"
	if _, err := conn.Write([]byte(wire)); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-commands:
		if got.AppVersion != "v0.2.0" || !got.BLEScanning || got.AdvertsSeen != 7 || got.UniqueAddrs != 2 {
			t.Fatalf("command metadata = %#v", got)
		}
		if len(got.Adverts) != 1 || got.Adverts[0].Data != "020106" || got.Adverts[0].RSSI != -58 {
			t.Fatalf("command adverts = %#v", got.Adverts)
		}
	case <-time.After(time.Second):
		t.Fatal("BLE command not delivered")
	}
}
