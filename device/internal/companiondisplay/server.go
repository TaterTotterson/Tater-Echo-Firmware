// Package companiondisplay exposes a small, LAN-local presentation protocol.
// Media and notification systems discover screen-capable Tater Echoes directly
// without routing display state through Tater or Home Assistant.
package companiondisplay

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"
)

const (
	DefaultPort       = 8930
	ServiceType       = "_tater-display._tcp"
	ProtocolVersion   = 1
	MaxArtworkBytes   = 4 << 20
	maxRequestBytes   = MaxArtworkBytes*4/3 + 64*1024
	defaultLease      = 90 * time.Second
	minimumLease      = 15 * time.Second
	maximumLease      = 2 * time.Minute
	defaultNoticeTTL  = 90 * time.Second
	minimumNoticeTTL  = 5 * time.Second
	maximumNoticeTTL  = 10 * time.Minute
	discoveryProbe    = "tater.display.discover"
	discoveryAnnounce = "tater.display.announce"
)

// Config describes the display advertised on the local network.
type Config struct {
	DeviceID        string
	Name            string
	Room            string
	Board           string
	FirmwareVersion string
	Port            int
	Advertise       bool
	Update          func(NowPlaying, []byte, string)
	Notify          func(Notification, []byte, string)
}

// NowPlaying is the generic companion presentation accepted from a media
// system. SourceName is the friendly player name rendered on the screen.
type NowPlaying struct {
	Active                  bool   `json:"active"`
	SourceID                string `json:"source_id"`
	SourceName              string `json:"source_name"`
	EventID                 string `json:"event_id"`
	PlaybackState           string `json:"playback_state"`
	MediaType               string `json:"media_type"`
	Title                   string `json:"title"`
	SeriesTitle             string `json:"series_title"`
	Season                  int    `json:"season"`
	Episode                 int    `json:"episode"`
	Subtitle                string `json:"subtitle"`
	ProgressMS              int64  `json:"progress_ms"`
	DurationMS              int64  `json:"duration_ms"`
	PlaybackSpeed           int    `json:"playback_speed"`
	ProgressUpdatedAtUnixMS int64  `json:"progress_updated_at_unix_ms"`
	LeaseMS                 int64  `json:"lease_ms"`
	ExpiresAtUnixMS         int64  `json:"expires_at_unix_ms"`
	Revision                int64  `json:"revision"`
	ImageDataBase64         string `json:"image_data_b64,omitempty"`
	ImageContentType        string `json:"image_content_type,omitempty"`
}

// Notification is a short-lived generic presentation from any trusted-LAN
// client. It deliberately carries display data rather than an Awareness- or
// Tater-specific event so other systems can use the same screen capability.
type Notification struct {
	SourceID            string `json:"source_id"`
	EventID             string `json:"event_id"`
	Kind                string `json:"kind,omitempty"`
	DisplayLabel        string `json:"display_label,omitempty"`
	Priority            string `json:"priority,omitempty"`
	Title               string `json:"title,omitempty"`
	CameraName          string `json:"camera_name,omitempty"`
	Description         string `json:"description,omitempty"`
	Source              string `json:"source,omitempty"`
	ImageFit            string `json:"image_fit,omitempty"`
	CreatedAtUnixMS     int64  `json:"created_at_unix_ms,omitempty"`
	ExpiresAtUnixMS     int64  `json:"expires_at_unix_ms,omitempty"`
	TTLMS               int64  `json:"ttl_ms,omitempty"`
	RememberAsLastEvent bool   `json:"remember_as_last_event,omitempty"`
	ImageDataBase64     string `json:"image_data_b64,omitempty"`
	ImageContentType    string `json:"image_content_type,omitempty"`
}

type discoveryMessage struct {
	Type            string         `json:"type"`
	ProtocolVersion int            `json:"protocol_version"`
	RequestID       string         `json:"request_id,omitempty"`
	DeviceID        string         `json:"device_id,omitempty"`
	Name            string         `json:"name,omitempty"`
	Room            string         `json:"room,omitempty"`
	Board           string         `json:"board,omitempty"`
	FirmwareVersion string         `json:"firmware_version,omitempty"`
	Port            int            `json:"port,omitempty"`
	Path            string         `json:"path,omitempty"`
	Capabilities    map[string]any `json:"capabilities,omitempty"`
}

// Server owns the HTTP endpoint plus mDNS and active UDP discovery.
type Server struct {
	cfg Config

	mu           sync.Mutex
	owner        string
	ownerExpires time.Time

	httpServer *http.Server
	listener   net.Listener
	discovery  *net.UDPConn
	mdns       *zeroconf.Server
}

// New validates and normalizes a Companion Display server.
func New(cfg Config) (*Server, error) {
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, fmt.Errorf("companion display port %d is invalid", cfg.Port)
	}
	cfg.DeviceID = clean(cfg.DeviceID, 200)
	cfg.Name = clean(cfg.Name, 200)
	cfg.Room = clean(cfg.Room, 200)
	cfg.Board = clean(cfg.Board, 80)
	cfg.FirmwareVersion = clean(cfg.FirmwareVersion, 80)
	if cfg.DeviceID == "" {
		return nil, errors.New("companion display device ID is required")
	}
	if cfg.Name == "" {
		cfg.Name = "Tater Echo " + tail(cfg.DeviceID, 4)
	}
	if cfg.Update == nil {
		return nil, errors.New("companion display update callback is required")
	}
	return &Server{cfg: cfg}, nil
}

// Start listens on TCP and UDP, advertises over mDNS, and stops with ctx.
func (s *Server) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", ":"+strconv.Itoa(s.cfg.Port))
	if err != nil {
		return err
	}
	s.listener = listener

	discovery, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: s.cfg.Port})
	if err != nil {
		listener.Close()
		return fmt.Errorf("companion display discovery: %w", err)
	}
	s.discovery = discovery

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/capabilities", s.handleCapabilities)
	mux.HandleFunc("/v1/now-playing", s.handleNowPlaying)
	mux.HandleFunc("/v1/notifications", s.handleNotification)
	s.httpServer = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	if s.cfg.Advertise {
		txt := []string{
			"path=/v1", "id=" + s.cfg.DeviceID, "name=" + s.cfg.Name,
			"room=" + s.cfg.Room, "board=" + s.cfg.Board,
			"protocol=" + strconv.Itoa(ProtocolVersion), "now_playing=1",
		}
		if s.cfg.Notify != nil {
			txt = append(txt, "notifications=1")
		}
		// A DNS-SD instance is one DNS label and therefore cannot exceed 63
		// octets. Device IDs may be much longer, so keep the stable tail here;
		// the complete ID remains available in TXT and /v1/capabilities.
		instance := "tater-display-" + tail(s.cfg.DeviceID, 40)
		s.mdns, err = zeroconf.Register(instance, ServiceType, "local.", s.cfg.Port, txt, nil)
		if err != nil {
			// Active UDP discovery remains available in containerized and segmented
			// networks where an mDNS responder cannot bind successfully.
			log.Printf("[companion-display] mDNS unavailable: %v", err)
		}
	}

	go s.runDiscovery(ctx)
	go func() {
		<-ctx.Done()
		s.Close()
	}()
	log.Printf("[companion-display] %q listening on :%d", s.cfg.Name, s.cfg.Port)
	err = s.httpServer.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) || ctx.Err() != nil {
		return nil
	}
	return err
}

// Close releases every listener. It is safe to call more than once.
func (s *Server) Close() {
	if s.mdns != nil {
		s.mdns.Shutdown()
	}
	if s.discovery != nil {
		s.discovery.Close()
	}
	if s.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = s.httpServer.Shutdown(ctx)
		cancel()
	}
}

func (s *Server) capabilities() map[string]any {
	features := map[string]any{
		"now_playing": 1, "progress": true, "lease": true,
		"artwork_upload":    []string{"image/jpeg", "image/png", "image/webp"},
		"max_artwork_bytes": MaxArtworkBytes,
	}
	if s.cfg.Notify != nil {
		features["notifications"] = 1
	}
	return map[string]any{
		"protocol_version": ProtocolVersion,
		"device": map[string]any{
			"id": s.cfg.DeviceID, "name": s.cfg.Name, "room": s.cfg.Room,
			"board": s.cfg.Board, "firmware_version": s.cfg.FirmwareVersion,
		},
		"capabilities": features,
	}
}

func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.capabilities())
}

func (s *Server) handleNowPlaying(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		s.applyClear(w, r)
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		w.Header().Set("Allow", "POST, PUT, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	decoder := json.NewDecoder(r.Body)
	var update NowPlaying
	if err := decoder.Decode(&update); err != nil {
		http.Error(w, "invalid now-playing payload", http.StatusBadRequest)
		return
	}
	if err := ensureJSONEnd(decoder); err != nil {
		http.Error(w, "invalid now-playing payload", http.StatusBadRequest)
		return
	}
	update.SourceID = clean(update.SourceID, 200)
	update.SourceName = clean(update.SourceName, 200)
	update.EventID = clean(update.EventID, 300)
	update.PlaybackState = strings.ToLower(clean(update.PlaybackState, 30))
	update.MediaType = clean(update.MediaType, 50)
	update.Title = clean(update.Title, 500)
	update.SeriesTitle = clean(update.SeriesTitle, 500)
	update.Subtitle = clean(update.Subtitle, 500)
	if update.SourceID == "" {
		http.Error(w, "source_id is required", http.StatusBadRequest)
		return
	}
	if update.Active && update.Title == "" {
		http.Error(w, "title is required while active", http.StatusBadRequest)
		return
	}
	if update.PlaybackState != "playing" && update.PlaybackState != "paused" {
		update.PlaybackState = "playing"
	}
	update.ProgressMS = max(int64(0), update.ProgressMS)
	update.DurationMS = max(int64(0), update.DurationMS)
	if update.DurationMS > 0 {
		update.ProgressMS = min(update.ProgressMS, update.DurationMS)
	}
	update.Season = max(0, update.Season)
	update.Episode = max(0, update.Episode)
	update.PlaybackSpeed = max(0, update.PlaybackSpeed)

	now := time.Now()
	lease := time.Duration(update.LeaseMS) * time.Millisecond
	if lease == 0 {
		lease = defaultLease
	}
	lease = min(max(lease, minimumLease), maximumLease)
	update.ExpiresAtUnixMS = now.Add(lease).UnixMilli()
	update.ImageContentType = strings.ToLower(strings.TrimSpace(update.ImageContentType))
	var artwork []byte
	if update.ImageDataBase64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(update.ImageDataBase64)
		if err != nil || len(decoded) == 0 || len(decoded) > MaxArtworkBytes {
			http.Error(w, "invalid artwork", http.StatusBadRequest)
			return
		}
		if update.ImageContentType != "image/jpeg" && update.ImageContentType != "image/png" && update.ImageContentType != "image/webp" {
			http.Error(w, "unsupported artwork content type", http.StatusBadRequest)
			return
		}
		artwork = decoded
	}
	update.ImageDataBase64 = ""

	s.mu.Lock()
	if !update.Active && s.owner != "" && s.owner != update.SourceID && now.Before(s.ownerExpires) {
		s.mu.Unlock()
		http.Error(w, "another source owns the display", http.StatusConflict)
		return
	}
	if update.Active {
		s.owner, s.ownerExpires = update.SourceID, now.Add(lease)
	} else {
		s.owner, s.ownerExpires = "", time.Time{}
	}
	s.mu.Unlock()

	s.cfg.Update(update, artwork, update.ImageContentType)
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true, "expires_at_unix_ms": update.ExpiresAtUnixMS})
}

func (s *Server) handleNotification(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Notify == nil {
		http.Error(w, "notifications are unavailable", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		w.Header().Set("Allow", "POST, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	decoder := json.NewDecoder(r.Body)
	var notification Notification
	if err := decoder.Decode(&notification); err != nil {
		http.Error(w, "invalid notification payload", http.StatusBadRequest)
		return
	}
	if err := ensureJSONEnd(decoder); err != nil {
		http.Error(w, "invalid notification payload", http.StatusBadRequest)
		return
	}
	notification.SourceID = clean(notification.SourceID, 200)
	notification.EventID = clean(notification.EventID, 300)
	notification.Kind = strings.ToLower(clean(notification.Kind, 80))
	notification.DisplayLabel = clean(notification.DisplayLabel, 80)
	notification.Priority = strings.ToLower(clean(notification.Priority, 20))
	notification.Title = clean(notification.Title, 500)
	notification.CameraName = clean(notification.CameraName, 500)
	notification.Description = clean(notification.Description, 1200)
	notification.Source = clean(notification.Source, 100)
	notification.ImageFit = strings.ToLower(clean(notification.ImageFit, 20))
	if notification.SourceID == "" {
		http.Error(w, "source_id is required", http.StatusBadRequest)
		return
	}
	if notification.EventID == "" {
		http.Error(w, "event_id is required", http.StatusBadRequest)
		return
	}
	if notification.Title == "" && notification.Description == "" {
		http.Error(w, "title or description is required", http.StatusBadRequest)
		return
	}
	if notification.Kind == "" {
		notification.Kind = "notification"
	}
	switch notification.Priority {
	case "low", "normal", "high", "critical":
	default:
		notification.Priority = "normal"
	}
	switch notification.ImageFit {
	case "cover", "fill":
		notification.ImageFit = "cover"
	case "contain", "fit":
		notification.ImageFit = "contain"
	default:
		notification.ImageFit = ""
	}
	if notification.Source == "" {
		notification.Source = notification.SourceID
	}

	now := time.Now()
	if notification.CreatedAtUnixMS <= 0 || notification.CreatedAtUnixMS > now.Add(5*time.Minute).UnixMilli() {
		notification.CreatedAtUnixMS = now.UnixMilli()
	}
	ttl := time.Duration(notification.TTLMS) * time.Millisecond
	if ttl == 0 {
		ttl = defaultNoticeTTL
	}
	ttl = min(max(ttl, minimumNoticeTTL), maximumNoticeTTL)
	notification.TTLMS = ttl.Milliseconds()
	notification.ExpiresAtUnixMS = now.Add(ttl).UnixMilli()

	notification.ImageContentType = strings.ToLower(strings.TrimSpace(notification.ImageContentType))
	var image []byte
	if notification.ImageDataBase64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(notification.ImageDataBase64)
		if err != nil || len(decoded) == 0 || len(decoded) > MaxArtworkBytes {
			http.Error(w, "invalid notification image", http.StatusBadRequest)
			return
		}
		if notification.ImageContentType != "image/jpeg" && notification.ImageContentType != "image/png" && notification.ImageContentType != "image/webp" {
			http.Error(w, "unsupported notification image content type", http.StatusBadRequest)
			return
		}
		image = decoded
	}
	notification.ImageDataBase64 = ""
	s.cfg.Notify(notification, image, notification.ImageContentType)
	writeJSON(w, http.StatusOK, map[string]any{
		"accepted": true, "expires_at_unix_ms": notification.ExpiresAtUnixMS,
	})
}

func (s *Server) applyClear(w http.ResponseWriter, r *http.Request) {
	sourceID := clean(r.URL.Query().Get("source_id"), 200)
	if sourceID == "" {
		http.Error(w, "source_id is required", http.StatusBadRequest)
		return
	}
	now := time.Now()
	s.mu.Lock()
	if s.owner != "" && s.owner != sourceID && now.Before(s.ownerExpires) {
		s.mu.Unlock()
		http.Error(w, "another source owns the display", http.StatusConflict)
		return
	}
	s.owner, s.ownerExpires = "", time.Time{}
	s.mu.Unlock()
	s.cfg.Update(NowPlaying{SourceID: sourceID}, nil, "")
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true})
}

func (s *Server) runDiscovery(ctx context.Context) {
	buffer := make([]byte, 8192)
	for {
		_ = s.discovery.SetReadDeadline(time.Now().Add(time.Second))
		n, remote, err := s.discovery.ReadFromUDP(buffer)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				continue
			}
			continue
		}
		var probe discoveryMessage
		if json.Unmarshal(buffer[:n], &probe) != nil || probe.Type != discoveryProbe || probe.ProtocolVersion > ProtocolVersion {
			continue
		}
		announce := discoveryMessage{
			Type: discoveryAnnounce, ProtocolVersion: ProtocolVersion, RequestID: probe.RequestID,
			DeviceID: s.cfg.DeviceID, Name: s.cfg.Name, Room: s.cfg.Room, Board: s.cfg.Board,
			FirmwareVersion: s.cfg.FirmwareVersion, Port: s.cfg.Port, Path: "/v1",
			Capabilities: map[string]any{"now_playing": 1, "artwork_upload": true, "progress": true, "lease": true},
		}
		if s.cfg.Notify != nil {
			announce.Capabilities["notifications"] = 1
		}
		encoded, _ := json.Marshal(announce)
		_, _ = s.discovery.WriteToUDP(encoded, remote)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return errors.New("unexpected trailing JSON")
}

func clean(value string, limit int) string {
	value = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value))
	runes := []rune(value)
	if len(runes) > limit {
		value = string(runes[:limit])
	}
	return value
}

func tail(value string, count int) string {
	runes := []rune(value)
	if len(runes) <= count {
		return value
	}
	return string(runes[len(runes)-count:])
}
