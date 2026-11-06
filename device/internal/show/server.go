// Package show exposes the small loopback-only protocol used by the Tater
// Show Linux renderer. The native daemon remains the source of truth for audio
// and Tater state; the renderer handles the screen and touch commands only.
package show

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
// renderer can distinguish no media from a title that happens to be blank.
type Media struct {
	Active                  bool      `json:"active"`
	PlaybackState           string    `json:"playback_state,omitempty"`
	GroupName               string    `json:"group_name,omitempty"`
	Title                   string    `json:"title,omitempty"`
	Artist                  string    `json:"artist,omitempty"`
	AlbumArtist             string    `json:"album_artist,omitempty"`
	Album                   string    `json:"album,omitempty"`
	ArtworkURL              string    `json:"artwork_url,omitempty"`
	PrimaryColor            [3]uint8  `json:"primary_color,omitempty"`
	AccentColor             [3]uint8  `json:"accent_color,omitempty"`
	ProgressMS              int64     `json:"progress_ms,omitempty"`
	DurationMS              int64     `json:"duration_ms,omitempty"`
	PlaybackSpeed           int       `json:"playback_speed,omitempty"`
	ProgressUpdatedAtUnixMS int64     `json:"progress_updated_at_unix_ms,omitempty"`
	Loudness                float64   `json:"loudness,omitempty"`
	Peak                    float64   `json:"peak,omitempty"`
	PeakSequence            uint64    `json:"peak_sequence,omitempty"`
	Spectrum                []float64 `json:"spectrum,omitempty"`
	BeatSequence            uint64    `json:"beat_sequence,omitempty"`
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
	Daylight              string `json:"daylight,omitempty"`
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
	Label           string `json:"label,omitempty"`
	Priority        string `json:"priority,omitempty"`
	Title           string `json:"title,omitempty"`
	CameraName      string `json:"camera_name,omitempty"`
	Description     string `json:"description,omitempty"`
	Source          string `json:"source,omitempty"`
	ImageFit        string `json:"image_fit,omitempty"`
	ImageURL        string `json:"image_url,omitempty"`
	CreatedAtUnixMS int64  `json:"created_at_unix_ms,omitempty"`
	ExpiresAtUnixMS int64  `json:"expires_at_unix_ms,omitempty"`
	TTLMS           int64  `json:"ttl_ms,omitempty"`
}

// Timer is the compact countdown selected by the native timer manager for the
// local screen. DeadlineUnixMS lets the renderer animate smoothly without forcing
// the daemon to broadcast a snapshot every second.
type Timer struct {
	Active             bool   `json:"active"`
	Ringing            bool   `json:"ringing"`
	Count              int    `json:"count"`
	RingingCount       int    `json:"ringing_count"`
	ID                 string `json:"id,omitempty"`
	Name               string `json:"name,omitempty"`
	Label              string `json:"label,omitempty"`
	OriginalDurationMS int    `json:"original_duration_ms,omitempty"`
	RemainingMS        int    `json:"remaining_ms,omitempty"`
	DeadlineUnixMS     int64  `json:"deadline_unix_ms,omitempty"`
}

type SatChatParticipant struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Room  string `json:"room,omitempty"`
	Board string `json:"board,omitempty"`
	Audio bool   `json:"audio"`
	Video bool   `json:"video"`
	Busy  bool   `json:"busy"`
}

type SatChatCall struct {
	ID               string `json:"id,omitempty"`
	State            string `json:"state,omitempty"`
	Mode             string `json:"mode,omitempty"`
	Caller           string `json:"caller,omitempty"`
	CallerName       string `json:"caller_name,omitempty"`
	Target           string `json:"target,omitempty"`
	TargetName       string `json:"target_name,omitempty"`
	Reason           string `json:"reason,omitempty"`
	VideoSendProfile string `json:"video_send_profile,omitempty"`
}

type SatChat struct {
	Self          string               `json:"self,omitempty"`
	Participants  []SatChatParticipant `json:"participants,omitempty"`
	Call          *SatChatCall         `json:"call,omitempty"`
	VideoFrameURL string               `json:"video_frame_url,omitempty"`
}

type Camera struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Room      string `json:"room,omitempty"`
	Doorbell  bool   `json:"doorbell"`
	Talkback  bool   `json:"talkback"`
	Available bool   `json:"available"`
}

type Cameras struct {
	Available        bool     `json:"available"`
	Items            []Camera `json:"cameras,omitempty"`
	SelectedID       string   `json:"selected_id,omitempty"`
	SelectedName     string   `json:"selected_name,omitempty"`
	SelectedRoom     string   `json:"selected_room,omitempty"`
	SelectedTalkback bool     `json:"selected_talkback"`
	Loading          bool     `json:"loading"`
	Error            string   `json:"error,omitempty"`
	FrameURL         string   `json:"frame_url,omitempty"`
	StreamURL        string   `json:"stream_url,omitempty"`
	StreamActive     bool     `json:"stream_active"`
}

// Video is one provider-neutral fullscreen playback session. The stream URL
// is short-lived and remains on the authenticated Tater -> satellite control
// path; the renderer consumes it directly and returns decoded PCM over the
// loopback-only audio endpoint below.
type Video struct {
	Active     bool   `json:"active"`
	SessionID  string `json:"session_id,omitempty"`
	URL        string `json:"url,omitempty"`
	Title      string `json:"title,omitempty"`
	Subtitle   string `json:"subtitle,omitempty"`
	State      string `json:"state,omitempty"`
	PositionMS int64  `json:"position_ms,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	FPS        int    `json:"fps,omitempty"`
	Loop       bool   `json:"loop"`
	Live       bool   `json:"live"`
	ServerSeek bool   `json:"server_seek"`
	Error      string `json:"error,omitempty"`
}

type RoomTemperature struct {
	RoomID          string  `json:"room_id,omitempty"`
	RoomName        string  `json:"room_name,omitempty"`
	Temperature     float64 `json:"temperature"`
	TemperatureUnit string  `json:"temperature_unit,omitempty"`
}

type Climate struct {
	Available          bool              `json:"available"`
	RoomID             string            `json:"room_id,omitempty"`
	RoomName           string            `json:"room_name,omitempty"`
	CurrentTemperature *float64          `json:"current_temperature,omitempty"`
	TargetTemperature  *float64          `json:"target_temperature,omitempty"`
	TemperatureUnit    string            `json:"temperature_unit,omitempty"`
	CurrentHumidity    *float64          `json:"current_humidity,omitempty"`
	HVACMode           string            `json:"hvac_mode,omitempty"`
	HVACAction         string            `json:"hvac_action,omitempty"`
	AvailableHVACModes []string          `json:"available_hvac_modes,omitempty"`
	CanSetTemperature  bool              `json:"can_set_temperature"`
	CanSetMode         bool              `json:"can_set_mode"`
	MinimumTemperature float64           `json:"minimum_temperature,omitempty"`
	MaximumTemperature float64           `json:"maximum_temperature,omitempty"`
	TemperatureStep    float64           `json:"temperature_step,omitempty"`
	RoomTemperatures   []RoomTemperature `json:"room_temperatures,omitempty"`
}

// Conversation is the display-only live text for one voice turn. Tater sends
// complete snapshots so partial STT revisions, streamed LLM output, reconnects,
// and non-streaming engines all share the same renderer contract.
type Conversation struct {
	TurnID         string `json:"turn_id,omitempty"`
	Phase          string `json:"phase,omitempty"`
	UserText       string `json:"user_text,omitempty"`
	UserFinal      bool   `json:"user_final"`
	AssistantText  string `json:"assistant_text,omitempty"`
	AssistantFinal bool   `json:"assistant_final"`
	Revision       int64  `json:"revision,omitempty"`
}

// Snapshot is the complete screen state. Complete snapshots make reconnects
// deterministic: the renderer never has to replay a missed transition.
type Snapshot struct {
	Protocol                    int           `json:"protocol"`
	Type                        string        `json:"type"`
	Phase                       string        `json:"phase"`
	Connected                   bool          `json:"connected"`
	DeviceName                  string        `json:"device_name"`
	AssistantName               string        `json:"assistant_name,omitempty"`
	Room                        string        `json:"room"`
	DisplayTheme                string        `json:"display_theme,omitempty"`
	DisplayEdgeGlow             bool          `json:"display_edge_glow"`
	WallpaperURLs               []string      `json:"wallpaper_urls,omitempty"`
	WallpaperIntervalMinutes    int           `json:"wallpaper_interval_minutes,omitempty"`
	ScreenBrightness            int           `json:"screen_brightness"`
	ScreenAutoBrightness        bool          `json:"screen_auto_brightness_enabled"`
	ScreenAutoBrightnessMinimum int           `json:"screen_auto_brightness_minimum"`
	ScreenNightMode             bool          `json:"screen_night_mode_enabled"`
	ScreenNightBrightness       int           `json:"screen_night_brightness"`
	ScreenNightStart            string        `json:"screen_night_start,omitempty"`
	ScreenNightEnd              string        `json:"screen_night_end,omitempty"`
	ScreenNightDisplay          string        `json:"screen_night_display,omitempty"`
	ScreenClockFormat           string        `json:"screen_clock_format,omitempty"`
	Message                     string        `json:"message,omitempty"`
	ToolName                    string        `json:"tool_name,omitempty"`
	ToolMessage                 string        `json:"tool_message,omitempty"`
	Conversation                *Conversation `json:"conversation,omitempty"`
	Muted                       bool          `json:"muted"`
	VolumePercent               int           `json:"volume_percent"`
	AudioLevel                  float64       `json:"audio_level"`
	DirectionDegrees            *float64      `json:"direction_degrees,omitempty"`
	TimerActive                 bool          `json:"timer_active"`
	Timer                       *Timer        `json:"timer,omitempty"`
	Media                       *Media        `json:"media,omitempty"`
	Weather                     *Weather      `json:"weather,omitempty"`
	Climate                     *Climate      `json:"climate,omitempty"`
	Notification                *Notification `json:"notification,omitempty"`
	LastEvent                   *Notification `json:"last_event,omitempty"`
	SatChat                     *SatChat      `json:"sat_chat,omitempty"`
	Cameras                     *Cameras      `json:"cameras,omitempty"`
	Video                       *Video        `json:"video,omitempty"`
	TaterTimeUnixMS             int64         `json:"tater_time_unix_ms,omitempty"`
	TaterUTCOffset              int           `json:"tater_utc_offset_seconds,omitempty"`
	TaterTimezone               string        `json:"tater_timezone,omitempty"`
	UpdatedAtUnixMS             int64         `json:"updated_at_unix_ms"`
}

// Command is a bounded request from the local screen. Only actions explicitly
// handled by cmd/server.go have an effect.
type Command struct {
	Protocol    int                `json:"protocol"`
	Type        string             `json:"type"`
	Action      string             `json:"action"`
	Target      string             `json:"target,omitempty"`
	Mode        string             `json:"mode,omitempty"`
	CallID      string             `json:"call_id,omitempty"`
	Value       *float64           `json:"value,omitempty"`
	Unit        string             `json:"unit,omitempty"`
	SessionID   string             `json:"session_id,omitempty"`
	PositionMS  int64              `json:"position_ms,omitempty"`
	Error       string             `json:"error,omitempty"`
	Diagnostics *ScreenDiagnostics `json:"diagnostics,omitempty"`
}

// ScreenDiagnostics is a low-rate performance sample from the native display
// process. It is sent over the existing loopback socket and exposed in the
// satellite status so display performance can be diagnosed without SSH.
type ScreenDiagnostics struct {
	LayoutTarget           string   `json:"layout_target,omitempty"`
	Width                  int      `json:"width,omitempty"`
	Height                 int      `json:"height,omitempty"`
	FPS                    float64  `json:"fps"`
	RenderAvgMS            float64  `json:"render_avg_ms"`
	RenderMaxMS            float64  `json:"render_max_ms"`
	PresentAvgMS           float64  `json:"present_avg_ms"`
	PresentMaxMS           float64  `json:"present_max_ms"`
	RotateAvgMS            float64  `json:"rotate_avg_ms"`
	PanAvgMS               float64  `json:"pan_avg_ms"`
	TouchDowns             uint64   `json:"touch_downs"`
	TouchMoves             uint64   `json:"touch_moves"`
	TouchUps               uint64   `json:"touch_ups"`
	LastTouchX             int      `json:"last_touch_x"`
	LastTouchY             int      `json:"last_touch_y"`
	LastTouchAtMS          int64    `json:"last_touch_at_ms"`
	BacklightPercent       int      `json:"backlight_percent"`
	BacklightTargetPercent int      `json:"backlight_target_percent"`
	AutoBrightness         bool     `json:"auto_brightness"`
	NightMode              bool     `json:"night_mode"`
	NightDisplay           string   `json:"night_display,omitempty"`
	AmbientLightAvailable  bool     `json:"ambient_light_available"`
	AmbientLightSensor     string   `json:"ambient_light_sensor,omitempty"`
	AmbientLightError      string   `json:"ambient_light_error,omitempty"`
	AmbientLux             *float64 `json:"ambient_lux,omitempty"`
	UpdatedAtMS            int64    `json:"updated_at_ms"`
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
	onVideoAudio func(context.Context, string, string, io.Reader) error

	mu                       sync.RWMutex
	state                    Snapshot
	clients                  map[*client]struct{}
	listener                 net.Listener
	imageListener            net.Listener
	imageServer              *http.Server
	notificationImage        []byte
	notificationImageType    string
	notificationImageVersion uint64
	lastEventImage           []byte
	lastEventImageType       string
	lastEventImageVersion    uint64
	mediaImage               []byte
	mediaImageType           string
	mediaImageVersion        uint64
	satChatVideoImage        []byte
	satChatVideoVersion      uint64
	cameraFrameImage         []byte
	cameraFrameVersion       uint64
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

// SetVideoAudioHandler installs the only path from decoded fullscreen-video
// audio into the daemon. The HTTP listener is loopback-only, and the handler
// additionally validates the active session before any bytes reach ALSA.
func (s *Server) SetVideoAudioHandler(handler func(context.Context, string, string, io.Reader) error) {
	s.mu.Lock()
	s.onVideoAudio = handler
	s.mu.Unlock()
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
	mux.HandleFunc("/last-event-image", s.serveLastEventImage)
	mux.HandleFunc("/media-image", s.serveMediaImage)
	mux.HandleFunc("/sat-chat-video", s.serveSatChatVideo)
	mux.HandleFunc("/camera-frame", s.serveCameraFrame)
	mux.HandleFunc("/video-audio", s.serveVideoAudio)
	imageServer := &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second}
	s.mu.Lock()
	s.listener = listener
	s.imageListener = imageListener
	s.imageServer = imageServer
	if s.state.Notification != nil && len(s.notificationImage) > 0 && s.state.Notification.ImageURL == "" {
		next := *s.state.Notification
		s.notificationImageVersion++
		next.ImageURL = fmt.Sprintf("http://%s/notification-image?v=%d", imageListener.Addr().String(), s.notificationImageVersion)
		s.state.Notification = &next
	}
	if s.state.LastEvent != nil && len(s.lastEventImage) > 0 && s.state.LastEvent.ImageURL == "" {
		next := *s.state.LastEvent
		s.lastEventImageVersion++
		next.ImageURL = fmt.Sprintf("http://%s/last-event-image?v=%d", imageListener.Addr().String(), s.lastEventImageVersion)
		s.state.LastEvent = &next
	}
	if s.state.Media != nil && len(s.mediaImage) > 0 && s.state.Media.ArtworkURL == "" {
		next := *s.state.Media
		s.mediaImageVersion++
		next.ArtworkURL = fmt.Sprintf("http://%s/media-image?v=%d", imageListener.Addr().String(), s.mediaImageVersion)
		s.state.Media = &next
	}
	if s.state.Cameras != nil && len(s.cameraFrameImage) > 0 && s.state.Cameras.FrameURL == "" {
		next := *s.state.Cameras
		s.cameraFrameVersion++
		next.FrameURL = fmt.Sprintf("http://%s/camera-frame?v=%d", imageListener.Addr().String(), s.cameraFrameVersion)
		s.state.Cameras = &next
	}
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

// SetMedia updates Sendspin's compact now-playing state. Artwork is served on
// loopback instead of being embedded in every snapshot, just like awareness
// images, so the visualizer may update at display cadence without copying a
// cover image into every frame.
func (s *Server) SetMedia(media *Media, image []byte, contentType string) {
	s.mu.Lock()
	if media == nil {
		s.state.Media = nil
		s.mediaImage = nil
		s.mediaImageType = ""
	} else {
		next := *media
		next.Spectrum = append([]float64(nil), media.Spectrum...)
		if len(image) > 0 {
			s.mediaImage = append(s.mediaImage[:0], image...)
			s.mediaImageType = strings.TrimSpace(contentType)
			if !strings.HasPrefix(s.mediaImageType, "image/") {
				s.mediaImageType = "image/jpeg"
			}
			s.mediaImageVersion++
			if s.imageListener != nil {
				next.ArtworkURL = fmt.Sprintf(
					"http://%s/media-image?v=%d",
					s.imageListener.Addr().String(),
					s.mediaImageVersion,
				)
			}
		} else {
			s.mediaImage = nil
			s.mediaImageType = ""
			next.ArtworkURL = ""
		}
		s.state.Media = &next
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

// SetSatChatVideo publishes the newest remote call frame over the existing
// loopback image side channel. Only its changing URL enters screen snapshots,
// keeping large JPEGs out of the renderer control socket.
func (s *Server) SetSatChatVideo(image []byte) {
	s.mu.Lock()
	if s.state.SatChat == nil {
		s.state.SatChat = &SatChat{}
	}
	if len(image) == 0 {
		s.satChatVideoImage = nil
		s.state.SatChat.VideoFrameURL = ""
	} else {
		s.satChatVideoImage = append(s.satChatVideoImage[:0], image...)
		s.satChatVideoVersion++
		if s.imageListener != nil {
			s.state.SatChat.VideoFrameURL = fmt.Sprintf(
				"http://%s/sat-chat-video?v=%d",
				s.imageListener.Addr().String(),
				s.satChatVideoVersion,
			)
		}
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

// SetCameraFrame publishes the newest integration-camera frame over loopback.
// Like Sat Chat video, only the changing URL enters the small snapshot stream.
func (s *Server) SetCameraFrame(image []byte) {
	s.mu.Lock()
	if s.state.Cameras == nil {
		s.state.Cameras = &Cameras{}
	}
	next := *s.state.Cameras
	if len(image) == 0 {
		s.cameraFrameImage = nil
		next.FrameURL = ""
	} else {
		s.cameraFrameImage = append(s.cameraFrameImage[:0], image...)
		s.cameraFrameVersion++
		if s.imageListener != nil {
			next.FrameURL = fmt.Sprintf("http://%s/camera-frame?v=%d", s.imageListener.Addr().String(), s.cameraFrameVersion)
		}
		next.Loading = false
		next.Error = ""
	}
	s.state.Cameras = &next
	s.state.Protocol = ProtocolVersion
	s.state.Type = "snapshot"
	s.state.UpdatedAtUnixMS = time.Now().UnixMilli()
	s.mu.Unlock()
	select {
	case s.notify <- struct{}{}:
	default:
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
// large camera frame to the renderer.
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

// SetLastEvent retains the newest display event independently of the temporary
// notification TTL. Crown uses it as the idle content in its lower-right pane.
func (s *Server) SetLastEvent(event *Notification, image []byte, contentType string) {
	s.mu.Lock()
	if event == nil {
		s.state.LastEvent = nil
		s.lastEventImage = nil
		s.lastEventImageType = ""
	} else {
		next := *event
		// Last Event is intentionally durable; popup expiry applies only to
		// Snapshot.Notification.
		next.ExpiresAtUnixMS = 0
		if len(image) > 0 {
			s.lastEventImage = append(s.lastEventImage[:0], image...)
			s.lastEventImageType = strings.TrimSpace(contentType)
			if !strings.HasPrefix(s.lastEventImageType, "image/") {
				s.lastEventImageType = "image/jpeg"
			}
			s.lastEventImageVersion++
			if s.imageListener != nil {
				next.ImageURL = fmt.Sprintf(
					"http://%s/last-event-image?v=%d",
					s.imageListener.Addr().String(),
					s.lastEventImageVersion,
				)
			}
		} else {
			s.lastEventImage = nil
			s.lastEventImageType = ""
			next.ImageURL = ""
		}
		s.state.LastEvent = &next
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

func (s *Server) serveLastEventImage(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	image := append([]byte(nil), s.lastEventImage...)
	contentType := s.lastEventImageType
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

func (s *Server) serveMediaImage(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	image := append([]byte(nil), s.mediaImage...)
	contentType := s.mediaImageType
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

func (s *Server) serveSatChatVideo(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	image := append([]byte(nil), s.satChatVideoImage...)
	s.mu.RUnlock()
	if len(image) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.Header().Set("Content-Type", "image/jpeg")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(image)
}

func (s *Server) serveCameraFrame(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	image := append([]byte(nil), s.cameraFrameImage...)
	s.mu.RUnlock()
	if len(image) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.Header().Set("Content-Type", "image/jpeg")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(image)
}

func (s *Server) serveVideoAudio(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session"))
	streamID := strings.TrimSpace(r.URL.Query().Get("stream"))
	if sessionID == "" || streamID == "" || len(sessionID) > 128 || len(streamID) > 128 {
		http.Error(w, "invalid video audio session", http.StatusBadRequest)
		return
	}
	s.mu.RLock()
	video := s.state.Video
	handler := s.onVideoAudio
	valid := video != nil && video.Active && video.SessionID == sessionID &&
		(strings.EqualFold(video.State, "playing") || strings.EqualFold(video.State, "loading"))
	s.mu.RUnlock()
	if !valid || handler == nil {
		http.Error(w, "video session is not active", http.StatusConflict)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if err := handler(r.Context(), sessionID, streamID, r.Body); err != nil && r.Context().Err() == nil {
		log.Printf("[show] video audio stopped: %v", err)
		http.Error(w, "video audio stopped", http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Update atomically changes the complete state and schedules a broadcast.
// Scheduling is non-blocking because audio and direction updates originate on
// real-time capture/playback goroutines which must never wait on the renderer.
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
