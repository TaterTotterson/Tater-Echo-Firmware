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
	"strings"
	"sync"
	"time"
)

const (
	ProtocolVersion = 1
	DefaultAddress  = "127.0.0.1:43821"
	maxCommandBytes = 16 * 1024
)

// Media is the currently visible media item. Empty fields are omitted so the
// APK can distinguish no media from a title that happens to be blank.
type Media struct {
	Title  string `json:"title,omitempty"`
	Artist string `json:"artist,omitempty"`
	Album  string `json:"album,omitempty"`
}

// Snapshot is the complete screen state. Complete snapshots make reconnects
// deterministic: the APK never has to replay a missed transition.
type Snapshot struct {
	Protocol         int      `json:"protocol"`
	Type             string   `json:"type"`
	Phase            string   `json:"phase"`
	Connected        bool     `json:"connected"`
	DeviceName       string   `json:"device_name"`
	Room             string   `json:"room"`
	Message          string   `json:"message,omitempty"`
	Muted            bool     `json:"muted"`
	VolumePercent    int      `json:"volume_percent"`
	AudioLevel       float64  `json:"audio_level"`
	DirectionDegrees *float64 `json:"direction_degrees,omitempty"`
	TimerActive      bool     `json:"timer_active"`
	Media            *Media   `json:"media,omitempty"`
	UpdatedAtUnixMS  int64    `json:"updated_at_unix_ms"`
}

// Command is a bounded request from the local screen. Only actions explicitly
// handled by cmd/server.go have an effect.
type Command struct {
	Protocol int    `json:"protocol"`
	Type     string `json:"type"`
	Action   string `json:"action"`
	Value    *int   `json:"value,omitempty"`
}

type client struct {
	conn net.Conn
	mu   sync.Mutex
}

// Server owns the local screen state and fans complete snapshots to every
// connected renderer. It binds loopback only; no screen control is exposed to
// the LAN.
type Server struct {
	address   string
	onCommand func(Command)

	mu       sync.RWMutex
	state    Snapshot
	clients  map[*client]struct{}
	listener net.Listener
	closed   bool
	notify   chan struct{}
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
	return &Server{
		address: address, onCommand: onCommand, state: initial,
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
	s.mu.Lock()
	s.listener = listener
	s.mu.Unlock()

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
	clients := make([]*client, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.clients = make(map[*client]struct{})
	s.mu.Unlock()
	if listener != nil {
		_ = listener.Close()
	}
	for _, c := range clients {
		_ = c.conn.Close()
	}
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
	case "idle", "listening", "thinking", "speaking", "intercom", "music", "error", "setup", "offline":
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
