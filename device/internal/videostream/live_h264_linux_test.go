//go:build linux

package videostream

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestLiveH264DecoderResetDrainsBeforeKilling(t *testing.T) {
	for _, test := range []struct {
		name             string
		script           string
		cancelled, stall bool
	}{
		{"graceful-reset", "dd bs=12 count=1 >/dev/null 2>/dev/null", false, false},
		{"graceful-call-ended", "cat >/dev/null", true, false},
		{"stalled", "sleep 30", false, true},
		{"stalled-call-ended", "sleep 30", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			command := exec.CommandContext(ctx, "/bin/sh", "-c", test.script)
			command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }
			input, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			output, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan hardwareOutputResult, 1)
			go func() { _, err := io.Copy(io.Discard, output); done <- hardwareOutputResult{err: err} }()
			call, endCall := context.WithCancel(context.Background())
			defer endCall()
			if test.cancelled {
				endCall()
			}
			started := time.Now()
			stopLiveH264Decoder(call, &hardwareHelper{command: command, input: input, output: output}, cancel, done)
			if command.ProcessState == nil || command.ProcessState.Success() == test.stall || time.Since(started) > 4*time.Second {
				t.Fatal("reset failed graceful-exit or bounded-kill contract")
			}
		})
	}
}

func TestSatChatH264AcceptsOnlyLocalRelayURLs(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1:43822/sat-chat-h264?stream=123", "http://[::1]:43822/sat-chat-h264?stream=123"} {
		if _, err := satChatH264URL(raw); err != nil {
			t.Fatalf("rejected local relay: %v", err)
		}
	}
	for _, raw := range []string{
		"http://localhost:43822/sat-chat-h264?stream=123", "http://192.168.1.2:43822/sat-chat-h264?stream=123",
		"https://127.0.0.1:43822/sat-chat-h264?stream=123", "http://user@127.0.0.1:43822/sat-chat-h264?stream=123",
		"http://127.0.0.1:43822/other?stream=123", "http://127.0.0.1:43822/sat-chat-h264?stream=0",
		"http://127.0.0.1:0/sat-chat-h264?stream=123", "http://127.0.0.1/sat-chat-h264?stream=123",
		"http://127.0.0.1:43822/sat-chat-h264?stream=123#fragment", "http://127.0.0.1:43822/sat-chat-h264?stream=123&extra=1",
	} {
		if _, err := satChatH264URL(raw); err == nil {
			t.Fatalf("accepted invalid relay %q", raw)
		}
	}
}

func TestSatChatH264DoesNotFollowRedirects(t *testing.T) {
	var redirected atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Store(true) }))
	defer target.Close()
	var reached atomic.Bool
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Store(true)
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer relay.Close()
	if err := ReadSatChatH264(context.Background(), relay.URL+"/sat-chat-h264?stream=123", nil); err == nil {
		t.Fatal("accepted redirect")
	}
	if !reached.Load() || redirected.Load() {
		t.Fatal("did not reach loopback relay, or followed its redirect")
	}
}

func TestSatChatH264BoundsResponseStartupWithoutLimitingCallLength(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // Connected but never supplies HTTP headers.
	}))
	defer relay.Close()
	started := time.Now()
	if err := ReadSatChatH264(context.Background(), relay.URL+"/sat-chat-h264?stream=123", nil); err == nil {
		t.Fatal("accepted a stalled local relay")
	}
	if time.Since(started) > 4*time.Second {
		t.Fatal("response startup was not bounded")
	}
}

func TestLiveH264WatchdogExpiresAndProgressKeepsItAlive(t *testing.T) {
	t.Run("stalled", func(t *testing.T) {
		expired := make(chan struct{}, 1)
		_, stop := watchLiveH264Pictures(context.Background(), 40*time.Millisecond, func() { expired <- struct{}{} })
		defer stop()
		select {
		case <-expired:
		case <-time.After(time.Second):
			t.Fatal("picture stall did not expire")
		}
	})
	t.Run("progress-and-stop", func(t *testing.T) {
		expired := make(chan struct{}, 1)
		progress, stop := watchLiveH264Pictures(context.Background(), 160*time.Millisecond, func() { expired <- struct{}{} })
		defer stop()
		for n := 0; n < 5; n++ {
			time.Sleep(50 * time.Millisecond)
			progress()
		}
		stop()
		select {
		case <-expired:
			t.Fatal("active or cancelled picture watchdog expired")
		case <-time.After(200 * time.Millisecond):
		}
	})
}
