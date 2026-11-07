package companiondisplay

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNowPlayingEndpointAcceptsPresentationAndArtwork(t *testing.T) {
	var got NowPlaying
	var artwork []byte
	var contentType string
	server, err := New(Config{DeviceID: "screen-1", Name: "Kitchen", Update: func(update NowPlaying, image []byte, imageType string) {
		got, artwork, contentType = update, append([]byte(nil), image...), imageType
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler := http.NewServeMux()
	handler.HandleFunc("/v1/now-playing", server.handleNowPlaying)
	picture := []byte("not-a-real-jpeg")
	payload := NowPlaying{
		Active: true, SourceID: "player-1", SourceName: "Living Room TV", EventID: "event-1",
		Title: "The Big Lebowski", PlaybackState: "playing", ProgressMS: 42000,
		DurationMS: 120000, PlaybackSpeed: 1, LeaseMS: 30000,
		ImageDataBase64: base64.StdEncoding.EncodeToString(picture), ImageContentType: "image/jpeg",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/v1/now-playing", bytes.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if got.Title != payload.Title || got.SourceName != payload.SourceName || !got.Active {
		t.Fatalf("update = %#v", got)
	}
	if got.ExpiresAtUnixMS <= time.Now().UnixMilli() || got.ImageDataBase64 != "" {
		t.Fatalf("normalized update = %#v", got)
	}
	if !bytes.Equal(artwork, picture) || contentType != "image/jpeg" {
		t.Fatalf("artwork = %q %q", artwork, contentType)
	}
}

func TestOldSourceCannotClearNewOwner(t *testing.T) {
	server, err := New(Config{DeviceID: "screen-1", Update: func(NowPlaying, []byte, string) {}})
	if err != nil {
		t.Fatal(err)
	}
	server.owner, server.ownerExpires = "new-player", time.Now().Add(time.Minute)
	req := httptest.NewRequest(http.MethodDelete, "/v1/now-playing?source_id=old-player", nil)
	response := httptest.NewRecorder()
	server.handleNowPlaying(response, req)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want conflict", response.Code)
	}
}

func TestNotificationEndpointAcceptsGenericCardAndImage(t *testing.T) {
	var got Notification
	var image []byte
	var contentType string
	server, err := New(Config{
		DeviceID: "screen-1",
		Update:   func(NowPlaying, []byte, string) {},
		Notify: func(notification Notification, imageBytes []byte, imageType string) {
			got, image, contentType = notification, append([]byte(nil), imageBytes...), imageType
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	picture := []byte("not-a-real-png")
	payload := Notification{
		SourceID: "awareness-core", EventID: "front-door-1", Kind: "notification",
		Title: "Front Door", Description: "A person was detected.", Source: "awareness_core",
		TTLMS: 30000, RememberAsLastEvent: true,
		ImageDataBase64: base64.StdEncoding.EncodeToString(picture), ImageContentType: "image/png",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1/notifications", bytes.NewReader(body))
	response := httptest.NewRecorder()
	server.handleNotification(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if got.SourceID != payload.SourceID || got.EventID != payload.EventID || !got.RememberAsLastEvent {
		t.Fatalf("notification = %#v", got)
	}
	if got.ExpiresAtUnixMS <= time.Now().UnixMilli() || got.TTLMS != 30000 || got.ImageDataBase64 != "" {
		t.Fatalf("normalized notification = %#v", got)
	}
	if !bytes.Equal(image, picture) || contentType != "image/png" {
		t.Fatalf("image = %q %q", image, contentType)
	}
}

func TestNotificationEndpointAcceptsMissingImageForRendererDefault(t *testing.T) {
	called := false
	server, err := New(Config{
		DeviceID: "screen-1",
		Update:   func(NowPlaying, []byte, string) {},
		Notify: func(notification Notification, image []byte, contentType string) {
			called = true
			if len(image) != 0 || contentType != "" || notification.Title != "Back Door" {
				t.Fatalf("notification = %#v, image = %q %q", notification, image, contentType)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"source_id":"automation","event_id":"door-1","title":"Back Door"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/notifications", bytes.NewReader(body))
	response := httptest.NewRecorder()
	server.handleNotification(response, req)
	if response.Code != http.StatusOK || !called {
		t.Fatalf("status = %d, called = %v, body = %s", response.Code, called, response.Body.String())
	}
}

func TestDiscoveryProbeReturnsCapabilities(t *testing.T) {
	updates := func(NowPlaying, []byte, string) {}
	server, err := New(Config{
		DeviceID: "screen-1", Name: "Kitchen", Room: "Kitchen", Board: "rook", Port: 8930,
		Update: updates, Notify: func(Notification, []byte, string) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	loopback := net.IPv4(127, 0, 0, 1)
	server.discovery, err = net.ListenUDP("udp4", &net.UDPAddr{IP: loopback, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer server.discovery.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.runDiscovery(ctx)

	client, err := net.ListenUDP("udp4", &net.UDPAddr{IP: loopback, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	probe, _ := json.Marshal(discoveryMessage{Type: discoveryProbe, ProtocolVersion: 1, RequestID: "request-1"})
	_, err = client.WriteToUDP(probe, server.discovery.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	buffer := make([]byte, 8192)
	n, _, err := client.ReadFromUDP(buffer)
	if err != nil {
		t.Fatal(err)
	}
	var announce discoveryMessage
	if json.Unmarshal(buffer[:n], &announce) != nil || announce.Type != discoveryAnnounce || announce.DeviceID != "screen-1" {
		t.Fatalf("announce = %#v", announce)
	}
	if announce.Capabilities["now_playing"] != float64(1) && announce.Capabilities["now_playing"] != 1 {
		t.Fatalf("capabilities = %#v", announce.Capabilities)
	}
	if announce.Capabilities["notifications"] != float64(1) && announce.Capabilities["notifications"] != 1 {
		t.Fatalf("notification capabilities = %#v", announce.Capabilities)
	}
}
