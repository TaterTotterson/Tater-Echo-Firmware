package show

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"
)

func TestSnapshotIsBoundedAndNormalized(t *testing.T) {
	s := New("127.0.0.1:0", Snapshot{Phase: "mystery", VolumePercent: 150, AudioLevel: -2}, nil)
	got := s.Snapshot()
	if got.Protocol != ProtocolVersion || got.Type != "snapshot" {
		t.Fatalf("protocol envelope = %#v", got)
	}
	if got.Phase != "idle" || got.VolumePercent != 100 || got.AudioLevel != 0 {
		t.Fatalf("normalized snapshot = %#v", got)
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
