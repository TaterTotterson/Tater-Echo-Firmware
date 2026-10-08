package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/aec"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/beamformer"
	"github.com/gorilla/websocket"
)

func TestBeamLockOnSpeechRequestReplacesImmediateLock(t *testing.T) {
	d := &DataClient{}
	d.RequestBeamLock()
	if got := atomic.LoadInt32(&d.beamReq); got != beamReqLock {
		t.Fatalf("immediate beam request = %d, want %d", got, beamReqLock)
	}
	d.RequestBeamLockOnSpeech()
	if got := atomic.LoadInt32(&d.beamReq); got != beamReqLockOnSpeech {
		t.Fatalf("continued-chat beam request = %d, want %d", got, beamReqLockOnSpeech)
	}
	d.RequestBeamUnlock()
	if got := atomic.LoadInt32(&d.beamReq); got != beamReqUnlock {
		t.Fatalf("unlock beam request = %d, want %d", got, beamReqUnlock)
	}
}

func TestNativeListeningStateAlwaysUsesSpeechLock(t *testing.T) {
	d := &DataClient{}
	d.ApplyNativeBeamState("listening")
	if got := atomic.LoadInt32(&d.beamReq); got != beamReqLockOnSpeech {
		t.Fatalf("native listening request = %d, want speech lock %d", got, beamReqLockOnSpeech)
	}

	d.ApplyNativeBeamState("idle")
	if got := atomic.LoadInt32(&d.beamReq); got != beamReqUnlock {
		t.Fatalf("native idle request = %d, want unlock %d", got, beamReqUnlock)
	}
}

func TestNativeWakeCarriesWinningBeamIntoListening(t *testing.T) {
	d := NewDataClient("winner-test", nil, nil, aec.New())
	d.wakeLaneDir[1].Store(4)
	if !d.ClaimWakeLane(1, time.Now()) {
		t.Fatal("first wake lane was not accepted")
	}
	d.ApplyNativeBeamState("listening")
	if got := atomic.LoadInt32(&d.beamReq); got != beamReqLock {
		t.Fatalf("winning wake request = %d, want exact lock %d", got, beamReqLock)
	}
	d.lockWakeOrBest(true)
	if got := d.beam.(*beamformer.Beamformer).LockedAngle(); got != 210 {
		t.Fatalf("winning wake angle = %.0f, want 210", got)
	}
}

func TestEveryTargetUsesOneContinuousWakeStream(t *testing.T) {
	if got := NewDataClientForTarget("biscuit", nil, nil, aec.New(), "biscuit").WakeScorerCount(); got != 1 {
		t.Fatalf("Biscuit wake scorer count = %d, want 1 stable stream", got)
	}
	checkers := NewDataClientForTarget("checkers", nil, nil, aec.New(), "checkers")
	if got := checkers.WakeScorerCount(); got != 1 {
		t.Fatalf("Checkers wake scorer count = %d, want 1 stable stream", got)
	}
	if !checkers.ClaimWakeLane(0, time.Now()) {
		t.Fatal("Checkers first beam crossing was not accepted")
	}
	checkers.ApplyNativeBeamState("listening")
	if got := atomic.LoadInt32(&checkers.beamReq); got != beamReqLock {
		t.Fatalf("Checkers listening request = %d, want winning-beam lock %d", got, beamReqLock)
	}
}

func TestAdjacentWakeBeamCrossingsCollapseToOneClaim(t *testing.T) {
	d := NewDataClient("claim-test", nil, nil, aec.New())
	d.wakeLaneDir[0].Store(2)
	d.wakeLaneDir[1].Store(3)
	now := time.Now()
	if !d.ClaimWakeLane(0, now) {
		t.Fatal("first candidate crossing was rejected")
	}
	if d.ClaimWakeLane(1, now) {
		t.Fatal("adjacent candidate crossing escaped shared refractory")
	}
	if got := d.wakeWinnerDir.Load(); got != 2 {
		t.Fatalf("winner direction = %d, want first lane direction 2", got)
	}
}

func TestWakeLaneClaimUsesDirectionAtCaptureTime(t *testing.T) {
	d := NewDataClient("assignment-test", nil, nil, aec.New())
	first := time.Now()
	d.assignWakeLane(0, 1, first)
	d.assignWakeLane(0, 4, first.Add(time.Second))
	if !d.ClaimWakeLane(0, first.Add(500*time.Millisecond)) {
		t.Fatal("historical candidate crossing was rejected")
	}
	if got := d.wakeWinnerDir.Load(); got != 1 {
		t.Fatalf("historical winner direction = %d, want 1", got)
	}
}

func TestWakeLaneClaimSnapshotsWinningBeamThroughCallback(t *testing.T) {
	d := NewDataClient("winner-audio-test", nil, nil, aec.New())
	base := time.Now()
	d.recordWakeLaneAudio(0, base, []byte{1, 0})
	d.recordWakeLaneAudio(1, base, []byte{7, 0})
	d.recordWakeLaneAudio(1, base.Add(80*time.Millisecond), []byte{8, 0})
	d.recordWakeLaneAudio(1, base.Add(160*time.Millisecond), []byte{9, 0})
	d.wakeLaneDir[1].Store(1)
	if !d.ClaimWakeLane(1, base.Add(80*time.Millisecond)) {
		t.Fatal("winning wake lane was not accepted")
	}
	confirmation := d.CopyWinningWakeAudio()
	if len(confirmation) != 3 || confirmation[0][0] != 7 {
		t.Fatalf("confirmation pre-roll = %v, want a copy of lane 1", confirmation)
	}
	confirmation[0][0] = 99
	d.recordWakeLaneAudio(1, base.Add(240*time.Millisecond), []byte{10, 0})
	d.ExtendWinningWakeAudio(1, base.Add(80*time.Millisecond))
	frames := d.TakeWinningWakeAudio()
	if len(frames) != 4 || frames[0][0] != 7 || frames[1][0] != 8 || frames[2][0] != 9 || frames[3][0] != 10 {
		t.Fatalf("winning pre-roll = %v, want the unmodified lane 1 snapshot plus confirmation-delay audio", frames)
	}
	if again := d.TakeWinningWakeAudio(); len(again) != 0 {
		t.Fatalf("winning pre-roll was not one-shot: %v", again)
	}
}

func TestContinuousWakeAudioSurvivesDirectionChange(t *testing.T) {
	d := NewDataClient("continuous-wake-test", nil, nil, aec.New())
	base := time.Now()
	d.assignWakeLane(0, 1, base)
	d.recordWakeLaneAudio(0, base, []byte{1, 0})
	d.assignWakeLane(0, 4, base.Add(80*time.Millisecond))
	d.recordWakeLaneAudio(0, base.Add(80*time.Millisecond), []byte{2, 0})
	if !d.ClaimWakeLane(0, base.Add(40*time.Millisecond)) {
		t.Fatal("continuous wake stream was not accepted")
	}
	frames := d.TakeWinningWakeAudio()
	if len(frames) != 2 || frames[0][0] != 1 || frames[1][0] != 2 {
		t.Fatalf("wake pre-roll was split by a direction update: %v", frames)
	}
}

func TestDOAActivityDoesNotRequireStrictSpeechGate(t *testing.T) {
	const gain = 15.85 // +24dB default
	if !doaActivity(0.002, gain, false) {
		t.Fatal("quiet acoustic activity did not start DOA below the strict speech threshold")
	}
	if doaActivity(0.001, gain, false) {
		t.Fatal("sub-threshold room noise started DOA")
	}
	if !doaActivity(0, gain, true) {
		t.Fatal("strict speech decision did not start DOA")
	}
}

// fanoutMic is a minimal mic.Subscribable: a background pump broadcasts raw
// 9ch S24_3LE periods to every subscriber until closed, mimicking
// PcmMicrophone's fan-out (including the drop-when-full behaviour).
type fanoutMic struct {
	mu     sync.Mutex
	subs   []chan []byte
	stopCh chan struct{}
}

func newFanoutMic() *fanoutMic {
	m := &fanoutMic{stopCh: make(chan struct{})}
	// One 512-frame period of 9ch S24_3LE (the minimum Process() analyses),
	// non-zero so the beamformer smoothers see real energy.
	raw := make([]byte, 512*9*3)
	for i := range raw {
		raw[i] = byte(i % 251)
	}
	go func() {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-m.stopCh:
				return
			case <-ticker.C:
				m.mu.Lock()
				for _, ch := range m.subs {
					select {
					case ch <- raw:
					default:
					}
				}
				m.mu.Unlock()
			}
		}
	}()
	return m
}

func (m *fanoutMic) Subscribe() chan []byte {
	ch := make(chan []byte, 32)
	m.mu.Lock()
	m.subs = append(m.subs, ch)
	m.mu.Unlock()
	return ch
}

func (m *fanoutMic) Unsubscribe(ch chan []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.subs {
		if s == ch {
			m.subs = append(m.subs[:i], m.subs[i+1:]...)
			close(ch)
			return
		}
	}
}

func (m *fanoutMic) close() { close(m.stopCh) }

// dialTestWS stands up a WebSocket sink and returns a client conn to it.
func dialTestWS(t *testing.T) (*websocket.Conn, func()) {
	t.Helper()
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	url := "ws://" + strings.TrimPrefix(srv.URL, "http://")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		srv.Close()
		t.Fatalf("dial test ws: %v", err)
	}
	return conn, func() {
		conn.Close()
		srv.Close()
	}
}

// TestStreamRestartOverlapIsRaceFree drives the exact sequence the controller
// sends after every voice turn — StopMic immediately followed by StartMic —
// while mic data is flowing. The superseded streamMic goroutine can keep
// draining periods for a few iterations after its stopCh closes (select on a
// closed channel vs a ready mic channel picks randomly), so for a window the
// old and new goroutines run concurrently against the shared beamformer and
// AGC state. Run under -race: before pipeMu serialised the pipeline this
// reliably reported races on the beamformer's reused analysis buffers, and
// the old goroutine's deferred beam.Unlock could land after the new stream's
// Lock. Beam lock/unlock requests are mixed in to cover the mid-stream
// request path too.
func TestStreamRestartOverlapIsRaceFree(t *testing.T) {
	mic := newFanoutMic()
	defer mic.close()
	conn, cleanup := dialTestWS(t)
	defer cleanup()

	d := NewDataClient("race-test", mic, nil, aec.New())
	d.connMu.Lock()
	d.conn = conn
	d.connMu.Unlock()

	for i := 0; i < 100; i++ {
		lockMic := i%2 == 0 // alternate turn stream / wake stream
		d.StartMic(lockMic)
		d.RequestBeamLock()
		time.Sleep(2 * time.Millisecond) // let a couple of periods flow
		d.RequestBeamUnlock()
		d.StopMic()
		// No settling delay: the replacement StartMic in the next iteration
		// racing the superseded goroutine's drain is the scenario under test.
	}

	d.StopMic()
	// Give lingering goroutines time to exit so their deferred cleanup runs
	// (and the race detector observes it) before the test tears down.
	time.Sleep(100 * time.Millisecond)
}

// TestContextCancelReleasesMicStream reproduces the Office zombie-stream
// incident (2026-07-16): the control client cancels the data context on a
// control-WS reconnect while the data TCP path is still healthy. Before the
// ctx watcher in connect(), cancellation did nothing to an established
// connection — the old streamMic kept micActive forever and every
// mic_start on the replacement connection was refused ("already active"),
// leaving the device deaf to wake words. The fix must (a) close the
// connection so connect() returns promptly, and (b) release the mic stream
// so a StartMic against a new connection succeeds.
func TestContextCancelReleasesMicStream(t *testing.T) {
	mic := newFanoutMic()
	defer mic.close()

	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	// connect expects a full base URL ("ws://host:port") — a bare host:port
	// makes the dial fail instantly with a malformed-URL error, and this
	// test's poll loop would eat that silently until its deadline.
	addr := "ws://" + strings.TrimPrefix(srv.URL, "http://")

	d := NewDataClient("zombie-test", mic, nil, aec.New())

	ctx, cancel := context.WithCancel(context.Background())
	connectDone := make(chan error, 1)
	go func() { connectDone <- d.connect(ctx, addr) }()

	// Wait for connect to publish the conn, then start the wake stream on it.
	// Generous deadline: cold CI runners have missed 2s.
	deadline := time.Now().Add(10 * time.Second)
	for {
		d.connMu.Lock()
		ready := d.conn != nil
		d.connMu.Unlock()
		if ready {
			break
		}
		select {
		case err := <-connectDone:
			t.Fatalf("connect returned before publishing conn: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("connect never published conn")
		}
		time.Sleep(5 * time.Millisecond)
	}
	d.StartMic(false)

	// The control reconnect path: cancel the data context. connect() must
	// return promptly (not wait out a read deadline) and release the stream.
	cancel()
	select {
	case <-connectDone:
	case <-time.After(3 * time.Second):
		t.Fatal("connect did not return after context cancellation — established conn not torn down")
	}

	deadline = time.Now().Add(2 * time.Second)
	for {
		d.micMu.Lock()
		active := d.micActive
		d.micMu.Unlock()
		if !active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("mic stream still active after cancelled connection exited — zombie stream holds micActive")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// A replacement connection's StartMic must now succeed.
	conn2, cleanup2 := dialTestWS(t)
	defer cleanup2()
	d.connMu.Lock()
	d.conn = conn2
	d.connMu.Unlock()
	d.StartMic(false)
	d.micMu.Lock()
	restarted := d.micActive
	d.micMu.Unlock()
	if !restarted {
		t.Fatal("StartMic on replacement connection refused")
	}
	d.StopMic()
	time.Sleep(100 * time.Millisecond)
}
