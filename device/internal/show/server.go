// Package show exposes the small loopback-only protocol used by the Tater
// Show APK. The native daemon remains the source of truth for audio and Tater
// state; the APK is a renderer and command surface, never an audio service.
package show

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	ProtocolVersion     = 1
	DefaultAddress      = "127.0.0.1:43821"
	DefaultImageAddress = "127.0.0.1:43822"
	maxCommandBytes     = 16 * 1024
)

// Media is the currently visible media item. Empty fields are omitted so the
// APK can distinguish no media from a title that happens to be blank.
type Media struct {
	Title  string `json:"title,omitempty"`
	Artist string `json:"artist,omitempty"`
	Album  string `json:"album,omitempty"`
}

// Weather is the compact Environment Core summary rendered by screen targets.
// It deliberately contains display-ready values only; provider credentials and
// the full sensor feed never leave Tater.
type Weather struct {
	TemperatureText       string `json:"temperature_text,omitempty"`
	TemperatureUnit       string `json:"temperature_unit,omitempty"`
	IndoorTemperatureText string `json:"indoor_temperature_text,omitempty"`
	IndoorHumidityText    string `json:"indoor_humidity_text,omitempty"`
	Condition             string `json:"condition,omitempty"`
	ConditionKind         string `json:"condition_kind,omitempty"`
	FeelsLikeText         string `json:"feels_like_text,omitempty"`
	FeelsLikeRelation     string `json:"feels_like_relation,omitempty"`
	HumidityText          string `json:"humidity_text,omitempty"`
	WindText              string `json:"wind_text,omitempty"`
	RainText              string `json:"rain_text,omitempty"`
	LightningText         string `json:"lightning_text,omitempty"`
	Source                string `json:"source,omitempty"`
	Stale                 bool   `json:"stale,omitempty"`
}

// Notification is a temporary right-side visual supplied by Tater's display
// event bus. The image URL points only at this daemon's loopback image server.
type Notification struct {
	ID              string `json:"id,omitempty"`
	Kind            string `json:"kind,omitempty"`
	Priority        string `json:"priority,omitempty"`
	Title           string `json:"title,omitempty"`
	CameraName      string `json:"camera_name,omitempty"`
	Description     string `json:"description,omitempty"`
	ImageURL        string `json:"image_url,omitempty"`
	ExpiresAtUnixMS int64  `json:"expires_at_unix_ms,omitempty"`
}

// Snapshot is the complete screen state. Complete snapshots make reconnects
// deterministic: the APK never has to replay a missed transition.
type Snapshot struct {
	Protocol         int           `json:"protocol"`
	Type             string        `json:"type"`
	Phase            string        `json:"phase"`
	Connected        bool          `json:"connected"`
	DeviceName       string        `json:"device_name"`
	Room             string        `json:"room"`
	Message          string        `json:"message,omitempty"`
	ToolName         string        `json:"tool_name,omitempty"`
	ToolMessage      string        `json:"tool_message,omitempty"`
	Muted            bool          `json:"muted"`
	VolumePercent    int           `json:"volume_percent"`
	AudioLevel       float64       `json:"audio_level"`
	DirectionDegrees *float64      `json:"direction_degrees,omitempty"`
	TimerActive      bool          `json:"timer_active"`
	Media            *Media        `json:"media,omitempty"`
	Weather          *Weather      `json:"weather,omitempty"`
	Notification     *Notification `json:"notification,omitempty"`
	TaterTimeUnixMS  int64         `json:"tater_time_unix_ms,omitempty"`
	TaterUTCOffset   int           `json:"tater_utc_offset_seconds,omitempty"`
	TaterTimezone    string        `json:"tater_timezone,omitempty"`
	UpdatedAtUnixMS  int64         `json:"updated_at_unix_ms"`
}

// Command is a bounded request from the local screen. Only actions explicitly
// handled by cmd/server.go have an effect.
type Command struct {
	Protocol    int                `json:"protocol"`
	Type        string             `json:"type"`
	Action      string             `json:"action"`
	Value       *int               `json:"value,omitempty"`
	AppVersion  string             `json:"app_version,omitempty"`
	Adverts     []BLEAdvertisement `json:"adverts,omitempty"`
	AdvertsSeen uint64             `json:"adverts_seen,omitempty"`
	UniqueAddrs int                `json:"unique_addrs,omitempty"`
	BLEScanning bool               `json:"scanning,omitempty"`
	BLEError    string             `json:"error,omitempty"`
}

// BLEAdvertisement is the Android scanner's hardware-neutral relay shape.
// Data is lowercase AD-payload hex to keep the screen protocol textual and
// bounded; the native daemon validates and decodes it before forwarding.
type BLEAdvertisement struct {
	Address     string `json:"address"`
	AddressType int    `json:"address_type"`
	EventType   int    `json:"event_type"`
	RSSI        int    `json:"rssi"`
	Data        string `json:"data"`
}

type client struct {
	conn net.Conn
	mu   sync.Mutex
}

// Server owns the local screen state and fans complete snapshots to every
// connected renderer. It binds loopback only; no screen control is exposed to
// the LAN.
type Server struct {
	address      string
	imageAddress string
	onCommand    func(Command)

	mu                       sync.RWMutex
	state                    Snapshot
	clients                  map[*client]struct{}
	listener                 net.Listener
	imageListener            net.Listener
	imageServer              *http.Server
	notificationImage        []byte
	notificationImageType    string
	notificationImageVersion uint64
	closed                   bool
	notify                   chan struct{}
}

func New(address string, initial Snapshot, onCommand func(Command)) *Server {
	if strings.TrimSpace(address) == "" {
		address = DefaultAddress
	}
	initial.Protocol = ProtocolVersion
	initial.Type = "snapshot"
	initial.Phase = normalizePhase(initial.Phase)
	initial.VolumePercent = clamp(initial.VolumePercent, 0, 100)
	initial.AudioLevel = clampFloat(initial.AudioLevel, 0, 1)
	initial.UpdatedAtUnixMS = time.Now().UnixMilli()
	imageAddress := DefaultImageAddress
	if strings.HasSuffix(address, ":0") {
		imageAddress = "127.0.0.1:0"
	}
	return &Server{
		address: address, imageAddress: imageAddress, onCommand: onCommand, state: initial,
		clients: make(map[*client]struct{}), notify: make(chan struct{}, 1),
	}
}

// Run serves until ctx is cancelled. A failure to bind is returned so the
// firmware logs it, but it never prevents the voice satellite from starting.
func (s *Server) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.address, err)
	}
	imageListener, err := net.Listen("tcp", s.imageAddress)
	if err != nil {
		_ = listener.Close()
		return fmt.Errorf("listen on %s: %w", s.imageAddress, err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/notification-image", s.serveNotificationImage)
	imageServer := &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second}
	s.mu.Lock()
	s.listener = listener
	s.imageListener = imageListener
	s.imageServer = imageServer
	s.mu.Unlock()
	go func() {
		if err := imageServer.Serve(imageListener); err != nil && !errors.Is(err, http.ErrServerClosed) && ctx.Err() == nil {
			log.Printf("[show] notification image server stopped: %v", err)
		}
	}()

	go func() {
		<-ctx.Done()
		s.Close()
	}()
	go s.broadcastLoop(ctx)
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept screen connection: %w", err)
		}
		c := &client{conn: conn}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = conn.Close()
			continue
		}
		s.clients[c] = struct{}{}
		state := s.state
		s.mu.Unlock()
		if err := writeSnapshot(c, state); err != nil {
			s.remove(c)
			continue
		}
		go s.readCommands(c)
	}
}

// Address returns the bound address. It is useful for the integration test
// where port 0 asks the kernel to choose a free port.
func (s *Server) Address() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

func (s *Server) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	listener := s.listener
	imageListener := s.imageListener
	imageServer := s.imageServer
	clients := make([]*client, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.clients = make(map[*client]struct{})
	s.mu.Unlock()
	if listener != nil {
		_ = listener.Close()
	}
	if imageServer != nil {
		_ = imageServer.Close()
	} else if imageListener != nil {
		_ = imageListener.Close()
	}
	for _, c := range clients {
		_ = c.conn.Close()
	}
}

// SetNotification updates the temporary visual and stores its image outside
// the snapshot stream so real-time audio level updates never retransmit a
// large camera frame to the APK.
func (s *Server) SetNotification(notification *Notification, image []byte, contentType string) {
	s.mu.Lock()
	if notification == nil {
		s.state.Notification = nil
		s.notificationImage = nil
		s.notificationImageType = ""
	} else {
		next := *notification
		if len(image) > 0 {
			s.notificationImage = append(s.notificationImage[:0], image...)
			s.notificationImageType = strings.TrimSpace(contentType)
			if !strings.HasPrefix(s.notificationImageType, "image/") {
				s.notificationImageType = "image/jpeg"
			}
			s.notificationImageVersion++
			if s.imageListener != nil {
				next.ImageURL = fmt.Sprintf(
					"http://%s/notification-image?v=%d",
					s.imageListener.Addr().String(),
					s.notificationImageVersion,
				)
			}
		}
		s.state.Notification = &next
	}
	s.state.Protocol = ProtocolVersion
	s.state.Type = "snapshot"
	s.state.UpdatedAtUnixMS = time.Now().UnixMilli()
	s.mu.Unlock()
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *Server) serveNotificationImage(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	image := append([]byte(nil), s.notificationImage...)
	contentType := s.notificationImageType
	s.mu.RUnlock()
	if len(image) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(image)
}

// Update atomically changes the complete state and schedules a broadcast.
// Scheduling is non-blocking because audio and direction updates originate on
// real-time capture/playback goroutines which must never wait on the APK.
func (s *Server) Update(change func(*Snapshot)) {
	s.mu.Lock()
	change(&s.state)
	s.state.Protocol = ProtocolVersion
	s.state.Type = "snapshot"
	s.state.Phase = normalizePhase(s.state.Phase)
	s.state.VolumePercent = clamp(s.state.VolumePercent, 0, 100)
	s.state.AudioLevel = clampFloat(s.state.AudioLevel, 0, 1)
	s.state.UpdatedAtUnixMS = time.Now().UnixMilli()
	s.mu.Unlock()
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *Server) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

func (s *Server) readCommands(c *client) {
	defer s.remove(c)
	scanner := bufio.NewScanner(c.conn)
	scanner.Buffer(make([]byte, 1024), maxCommandBytes)
	for scanner.Scan() {
		var command Command
		if err := json.Unmarshal(scanner.Bytes(), &command); err != nil {
			log.Printf("[show] ignored malformed command: %v", err)
			continue
		}
		if command.Protocol != ProtocolVersion || command.Type != "command" {
			continue
		}
		command.Action = strings.TrimSpace(command.Action)
		if command.Action == "" {
			continue
		}
		if s.onCommand != nil {
			s.onCommand(command)
		}
	}
}

func (s *Server) broadcastLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.notify:
			s.mu.RLock()
			state := s.state
			clients := make([]*client, 0, len(s.clients))
			for c := range s.clients {
				clients = append(clients, c)
			}
			s.mu.RUnlock()
			for _, c := range clients {
				if err := writeSnapshot(c, state); err != nil {
					s.remove(c)
				}
			}
		}
	}
}

func (s *Server) remove(c *client) {
	s.mu.Lock()
	if _, ok := s.clients[c]; ok {
		delete(s.clients, c)
		_ = c.conn.Close()
	}
	s.mu.Unlock()
}

func writeSnapshot(c *client, state Snapshot) error {
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(250 * time.Millisecond))
	defer c.conn.SetWriteDeadline(time.Time{})
	if _, err := c.conn.Write(append(payload, '\n')); err != nil {
		return err
	}
	return nil
}

func normalizePhase(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "idle", "listening", "thinking", "tool_call", "speaking", "intercom", "music", "error", "setup", "offline":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "idle"
	}
}

func clamp(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func clampFloat(value, low, high float64) float64 {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}
