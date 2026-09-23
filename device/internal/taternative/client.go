package taternative

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/clock"
	"github.com/gorilla/websocket"
)

const (
	defaultReconnect = 2 * time.Second
	defaultHeartbeat = 5 * time.Second
	writeTimeout     = 10 * time.Second
	readTimeout      = 45 * time.Second
	preRollChunks    = 25 // 2 seconds at the firmware's 80 ms PCM boundary
	outgoingCapacity = 512
)

// Config identifies one Echo to Tater.
type Config struct {
	URL             string
	Token           string
	TokenPath       string
	DeviceID        string
	HardwareID      string
	DeviceName      string
	Board           string
	FirmwareTarget  string
	FirmwareVersion string
	Room            string
	Reconnect       time.Duration
	Heartbeat       time.Duration
	Capabilities    map[string]any
}

// PlayRequest is a one-shot voice/TTS playback command.
type PlayRequest struct {
	URL                  string
	TTSKind              string
	StateAfter           string
	ContinueConversation bool
	ConversationID       string
	Ducking              map[string]any
}

// OverlayRequest is a synchronized foreground/TTS layer rendered over an
// already active persistent media session.
type OverlayRequest struct {
	OverlayID             string
	GroupID               string
	URL                   string
	Kind                  string
	VolumePercent         int
	DuckingTargetPercent  int
	DuckingAttack         time.Duration
	DuckingRelease        time.Duration
	StopMediaWhenFinished bool
	BackgroundFadeOut     time.Duration
	StartAtUS             int64
	ContinueConversation  bool
	ConversationID        string
}

// SceneRequest is the standalone foreground/background scene command. New
// Tater controllers normally compose this from a buffered media session plus
// an overlay, but the direct command remains supported for compatibility.
type SceneRequest struct {
	SceneID                 string
	ForegroundURL           string
	ForegroundKind          string
	ForegroundVolumePercent int
	BackgroundURL           string
	BackgroundVolumePercent int
	BackgroundLoop          bool
	DuckingTargetPercent    int
	DuckingAttack           time.Duration
	DuckingRelease          time.Duration
	BackgroundFadeOut       time.Duration
}

// MediaRequest is a persistent music session.
type MediaRequest struct {
	SessionID       string
	GroupID         string
	URL             string
	Channel         string
	VolumePercent   int
	StartPositionMS int
	Loop            bool
	ContentType     string
	Title           string
	Artist          string
	Album           string
}

// MediaPreparation describes a locally decoded session that is ready for a
// synchronized commit. Echo playback is mono at the speaker, but Channel
// selects which side of a stereo source is rendered by this member.
type MediaPreparation struct {
	BufferedFrames      int
	SampleRateHz        int
	OutputLatencyFrames int
}

// MediaPlaybackEvent carries the synchronized session clock and playhead
// reports emitted by LocalPlayer after a prepared session is committed.
type MediaPlaybackEvent struct {
	Kind                     string
	SessionID                string
	GroupID                  string
	Channel                  string
	SampleRateHz             int
	ScheduledStartUS         int64
	ActualStartUS            int64
	SourceFrames             int64
	RenderedFrames           int64
	OutputFrames             int64
	BufferedFrames           int
	OutputLatencyFrames      int
	CorrectionFrames         int
	UnderrunEvents           int
	OverlayUnderrunEvents    int
	BackgroundUnderrunEvents int
	ForegroundUnderrunEvents int
	Rebuffering              bool
	RejoinCount              int
	RejoinFrames             int64
	SatelliteTimeUS          int64
}

// OTARequest describes one A/B firmware update offered by Tater.
type OTARequest struct {
	URL       string
	SHA256    string
	SizeBytes int64
}

// Hooks bind protocol commands to the Echo hardware. Blocking playback and
// OTA callbacks are launched outside the WebSocket reader.
type Hooks struct {
	Connected     func(selector string)
	Disconnected  func(error)
	State         func(state string, payload map[string]any)
	Settings      func(values map[string]any) (map[string]any, error)
	Status        func() map[string]any
	PlayWakeSound func() bool
	PlayVoice     func(context.Context, PlayRequest) error
	PlayOverlay   func(context.Context, OverlayRequest, func()) error
	PlayScene     func(context.Context, SceneRequest) error
	StopVoice     func()
	StartMedia    func(context.Context, MediaRequest) error
	PrepareMedia  func(context.Context, MediaRequest) (MediaPreparation, error)
	CommitMedia   func(context.Context, string, int64, func(MediaPlaybackEvent)) (<-chan error, error)
	AdjustMedia   func(sessionID string, correctionFrames int, mode string, settle time.Duration) error
	StopMedia     func(sessionID string)
	PauseMedia    func(sessionID string)
	ResumeMedia   func(sessionID string)
	VolumeMedia   func(sessionID string, percent int)
	TimerAlarm    func(active bool, timer Timer)
	SetupReset    func() error
	OTA           func(context.Context, OTARequest, func(status string, progress int, message string)) error
}

type outbound struct {
	kind int
	data []byte
}

type queuedVoice struct {
	req   PlayRequest
	epoch uint64
}

type wakeVerification struct {
	enforce  bool
	wakeWord string
	score    float32
	timer    *time.Timer
}

// Client is a reconnecting Tater native satellite connection.
type Client struct {
	cfg   Config
	hooks Hooks
	url   string

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	connected atomic.Bool
	started   time.Time
	out       chan outbound

	connMu sync.Mutex
	conn   *websocket.Conn

	stateMu  sync.RWMutex
	state    string
	settings map[string]any
	selector string

	audioMu           sync.Mutex
	preRoll           [][]byte
	captureRoll       [][]byte
	wakePreRoll       [][]byte
	pendingAudio      [][]byte
	voicePending      bool
	voiceActive       bool
	voiceStopAfterAck bool
	audioDropped      atomic.Uint64
	wakeSuppressed    uint64

	verifyMu         sync.Mutex
	verifyNext       uint32
	verifyRequests   map[uint32]*wakeVerification
	verifyCompleted  uint64
	verifyRejections uint64
	verifyFailOpen   uint64
	verifyLastReason string

	trainerMu             sync.Mutex
	trainerHTTP           *http.Client
	trainerUploadRunning  bool
	trainerUploads        uint64
	trainerUploadFailures uint64
	trainerLastEvent      string
	trainerLastError      string
	trainerCloseTimer     *time.Timer
	trainerCloseScore     float32
	trainerCloseWord      string
	trainerLastClose      time.Time

	playMu               sync.Mutex
	voiceCancel          context.CancelFunc
	voiceGen             uint64
	voiceResponsePending bool
	pendingReopen        bool
	pendingConversation  string
	voiceQueue           chan queuedVoice
	voiceStop            chan uint64
	mediaCancel          context.CancelFunc
	mediaCtx             context.Context
	mediaGen             uint64
	mediaID              string
	mediaGroup           string
	mediaChannel         string
	overlayCancel        context.CancelFunc
	overlayGen           uint64
	overlayID            string
	overlayGroup         string
	sceneCancel          context.CancelFunc
	sceneGen             uint64
	sceneID              string

	timers    *TimerManager
	closeOnce sync.Once
}

// New validates cfg and builds a client. Run performs network I/O.
func New(cfg Config, hooks Hooks) (*Client, error) {
	normalized, err := NormalizeURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.DeviceID) == "" {
		return nil, errors.New("Tater native device ID is required")
	}
	if cfg.DeviceName == "" {
		cfg.DeviceName = cfg.DeviceID
	}
	if cfg.Board == "" {
		cfg.Board = "echo-dot"
	}
	if cfg.FirmwareTarget == "" {
		cfg.FirmwareTarget = "biscuit"
	}
	if cfg.FirmwareVersion == "" {
		cfg.FirmwareVersion = "unknown"
	}
	if cfg.Reconnect <= 0 {
		cfg.Reconnect = defaultReconnect
	}
	if cfg.Heartbeat <= 0 {
		cfg.Heartbeat = defaultHeartbeat
	}
	if cfg.Capabilities == nil {
		cfg.Capabilities = DefaultCapabilities()
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		cfg: cfg, hooks: hooks, url: normalized,
		ctx: ctx, cancel: cancel, done: make(chan struct{}),
		started: time.Now(), out: make(chan outbound, outgoingCapacity),
		state: "idle", settings: map[string]any{
			"barge_in_enabled": false,
			"continued_chat":   true,
		},
		preRoll:        make([][]byte, 0, preRollChunks),
		captureRoll:    make([][]byte, 0, trainerCaptureChunks),
		trainerHTTP:    &http.Client{Timeout: 15 * time.Second},
		verifyRequests: make(map[uint32]*wakeVerification),
		voiceQueue:     make(chan queuedVoice, 32),
		voiceStop:      make(chan uint64, 1),
	}
	c.timers = NewTimerManager(c.sendJSON, hooks.TimerAlarm)
	go c.voiceWorker()
	return c, nil
}

// DefaultCapabilities is the honest Echo feature set implemented here.
func DefaultCapabilities() map[string]any {
	return map[string]any{
		"microphone": true, "speaker": true, "led_ring": true,
		"local_wake": true, "live_settings": true,
		"continued_chat_reopen": true, "barge_in": true,
		"tool_call_mode": true, "timers": true, "ota": true,
		"intercom": true, "setup_mode": true,
		"audio_ducking": true, "looping_background_audio": true,
		"persistent_media_sessions": true, "synchronized_media_sessions": true,
		"stereo_channel_selection": true, "media_playhead_telemetry": true,
		"media_render_clock": true, "media_output_latency_frames": echoOutputLatencyFrames,
		"media_drift_correction": true, "media_rate_slew": true,
		"media_underrun_recovery": true, "media_session_volume": true,
		"media_session_start_position": true, "media_sample_rate_hz": playbackRate,
		"tts_overlays": true, "synchronized_tts_overlays": true,
		"audio_scenes": true, "audio_scene_version": 1,
		"audio_session_version": 4,
		"settings":              true, "wake_verifier": true,
		"wake_sound": true, "wake_audio_capture": true,
	}
}

// Run reconnects until ctx is cancelled or Close is called.
func (c *Client) Run(ctx context.Context) error {
	defer close(c.done)
	for {
		if err := contextCause(ctx, c.ctx); err != nil {
			return err
		}
		err := c.runOnce(ctx)
		c.markDisconnected(err)
		if hook := c.hooks.Disconnected; hook != nil {
			hook(err)
		}
		timer := time.NewTimer(c.cfg.Reconnect)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-c.ctx.Done():
			timer.Stop()
			return c.ctx.Err()
		case <-timer.C:
		}
	}
}

func contextCause(a, b context.Context) error {
	select {
	case <-a.Done():
		return a.Err()
	default:
	}
	select {
	case <-b.Done():
		return b.Err()
	default:
		return nil
	}
}

func (c *Client) runOnce(parent context.Context) error {
	token := c.loadToken()
	header := http.Header{}
	if token != "" {
		header.Set("X-Tater-Token", token)
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.url, header)
	if err != nil {
		return fmt.Errorf("dial Tater: %w", err)
	}
	defer conn.Close()

	hello := map[string]any{
		"device_id": c.cfg.DeviceID, "hardware_id": c.cfg.HardwareID,
		"device_name": c.cfg.DeviceName, "board": c.cfg.Board,
		"firmware_target":  c.cfg.FirmwareTarget,
		"firmware_version": c.cfg.FirmwareVersion, "room": c.cfg.Room,
		"capabilities": c.cfg.Capabilities,
	}
	raw, _ := marshalEnvelope("hello", "", hello)
	conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	if err := conn.WriteMessage(websocket.TextMessage, raw); err != nil {
		return fmt.Errorf("send hello: %w", err)
	}
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	kind, first, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("read hello acknowledgement: %w", err)
	}
	if kind != websocket.TextMessage {
		return errors.New("Tater hello acknowledgement was not JSON")
	}
	var ack Envelope
	if err := json.Unmarshal(first, &ack); err != nil {
		return fmt.Errorf("decode hello acknowledgement: %w", err)
	}
	if ack.Type == "error" {
		return fmt.Errorf("Tater rejected satellite: %s", stringValue(ack.Payload["error"]))
	}
	if ack.Type != "hello.ack" || !boolValue(ack.Payload["ok"]) {
		return fmt.Errorf("expected hello.ack, received %q", ack.Type)
	}
	if changed, clockErr := syncClockFromServer(ack.TS, time.Now(), clock.Step); clockErr != nil {
		log.Printf("[clock] could not set the clock from Tater: %v", clockErr)
	} else if changed {
		log.Printf("[clock] stepped to %s (from Tater)", time.Now().Format(time.RFC3339))
	}
	if paired := stringValue(ack.Payload["device_token"]); paired != "" {
		c.cfg.Token = paired
		if err := c.saveToken(paired); err != nil {
			log.Printf("[tater-native] save paired token: %v", err)
		}
	}

	c.connMu.Lock()
	c.conn = conn
	c.connMu.Unlock()
	c.connected.Store(true)
	c.stateMu.Lock()
	c.selector = stringValue(ack.Payload["selector"])
	c.stateMu.Unlock()
	if hook := c.hooks.Connected; hook != nil {
		hook(c.Selector())
	}

	conn.SetReadDeadline(time.Now().Add(readTimeout))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(readTimeout))
	})
	errCh := make(chan error, 2)
	go func() { errCh <- c.writer(ctx, conn) }()
	go func() { errCh <- c.heartbeat(ctx) }()
	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		conn.SetReadDeadline(time.Now().Add(readTimeout))
		if kind != websocket.TextMessage {
			continue
		}
		var message Envelope
		if err := json.Unmarshal(data, &message); err != nil {
			log.Printf("[tater-native] invalid JSON ignored: %v", err)
			continue
		}
		c.handle(message)
		select {
		case err := <-errCh:
			return err
		default:
		}
	}
}

func (c *Client) writer(ctx context.Context, conn *websocket.Conn) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.ctx.Done():
			return c.ctx.Err()
		case frame := <-c.out:
			conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := conn.WriteMessage(frame.kind, frame.data); err != nil {
				conn.Close()
				return err
			}
		}
	}
}

func (c *Client) heartbeat(ctx context.Context) error {
	ticker := time.NewTicker(c.cfg.Heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.ctx.Done():
			return c.ctx.Err()
		case <-ticker.C:
			// Keep the connection alive even when Tater has no application
			// messages to send. The server's WebSocket ping handler replies with
			// a pong, which extends the read deadline installed in runOnce.
			c.enqueue(outbound{kind: websocket.PingMessage, data: []byte("tater")}, false)
			payload := map[string]any{
				"state": c.State(), "uptime_s": int(time.Since(c.started).Seconds()),
				"connected": true, "audio_tx_dropped": c.audioDropped.Load(),
			}
			payload["wake_verifier"] = c.wakeVerifierStatus()
			for key, value := range c.timers.Status() {
				payload[key] = value
			}
			if hook := c.hooks.Status; hook != nil {
				for key, value := range hook() {
					payload[key] = value
				}
			}
			c.sendJSON("status", "", payload)
		}
	}
}

func (c *Client) markDisconnected(error) {
	c.connected.Store(false)
	c.stopOverlay(false)
	c.stopScene(false)
	c.stopMedia("")
	c.stopVoice()
	c.connMu.Lock()
	c.conn = nil
	c.connMu.Unlock()
	c.audioMu.Lock()
	c.voicePending = false
	c.voiceActive = false
	c.voiceStopAfterAck = false
	c.wakePreRoll = nil
	c.pendingAudio = nil
	c.audioMu.Unlock()
	c.cancelWakeVerifications()
	c.cancelCloseMiss()
	c.drainOutgoing()
}

func (c *Client) drainOutgoing() {
	for {
		select {
		case <-c.out:
		default:
			return
		}
	}
}

func (c *Client) sendJSON(messageType, id string, payload map[string]any) bool {
	if !c.connected.Load() {
		return false
	}
	raw, err := marshalEnvelope(messageType, id, payload)
	if err != nil {
		return false
	}
	return c.enqueue(outbound{kind: websocket.TextMessage, data: raw}, false)
}

func (c *Client) sendBinary(data []byte) bool {
	if !c.connected.Load() || len(data) == 0 {
		return false
	}
	return c.enqueue(outbound{kind: websocket.BinaryMessage, data: append([]byte(nil), data...)}, true)
}

func (c *Client) enqueue(frame outbound, audio bool) bool {
	select {
	case c.out <- frame:
		return true
	default:
		if audio {
			c.audioDropped.Add(1)
			return false
		}
		// Preserve control progress under audio pressure by evicting one old
		// frame. A future audio discontinuity is better than losing stop/ack.
		select {
		case <-c.out:
		default:
		}
		select {
		case c.out <- frame:
			return true
		default:
			return false
		}
	}
}

func (c *Client) loadToken() string {
	if c.cfg.TokenPath != "" {
		if raw, err := os.ReadFile(c.cfg.TokenPath); err == nil {
			if token := strings.TrimSpace(string(raw)); token != "" {
				return token
			}
		}
	}
	return strings.TrimSpace(c.cfg.Token)
}

func (c *Client) saveToken(token string) error {
	if c.cfg.TokenPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(c.cfg.TokenPath), 0o700); err != nil {
		return err
	}
	tmp := c.cfg.TokenPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.TrimSpace(token)+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.cfg.TokenPath)
}

// Close stops reconnects and active local work.
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		c.cancel()
		c.connMu.Lock()
		if c.conn != nil {
			c.conn.Close()
		}
		c.connMu.Unlock()
		c.stopOverlay(false)
		c.stopScene(false)
		c.stopVoice()
		c.stopMedia("")
		c.cancelWakeVerifications()
		c.cancelCloseMiss()
		c.timers.Close()
	})
}

func (c *Client) Connected() bool { return c.connected.Load() }

func (c *Client) Selector() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.selector
}

func (c *Client) State() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.state
}

func (c *Client) setState(state string, payload map[string]any) {
	state = strings.ToLower(strings.TrimSpace(state))
	if state == "" {
		return
	}
	c.stateMu.Lock()
	c.state = state
	c.stateMu.Unlock()
	if hook := c.hooks.State; hook != nil {
		hook(state, payload)
	}
}

// ReportSettings publishes a physical/local settings change to Tater.
func (c *Client) ReportSettings(values map[string]any) {
	if len(values) == 0 {
		return
	}
	c.sendJSON("settings.changed", "", map[string]any{"ok": true, "settings": values})
}
