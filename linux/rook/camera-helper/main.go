// This adapter is copied into the pinned TECHO5 module and built with the
// spot tag. TECHO5's Rook GC0312 driver owns the hardware-specific capture.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"image/jpeg"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
)

const address = "127.0.0.1:43823"
const muteStatePath = "/data/local/etc/echomuse/state.json"

func main() {
	log.SetOutput(os.Stdout)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		log.Fatalf("camera listen: %v", err)
	}
	server := &http.Server{ReadHeaderTimeout: 2 * time.Second}
	http.HandleFunc("/snapshot", snapshot)
	go func() {
		<-ctx.Done()
		shutdown, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("Tater Rook camera ready on %s", address)
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func snapshot(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	muted, err := currentMuteState()
	if err != nil {
		http.Error(response, "camera mute state unavailable", http.StatusServiceUnavailable)
		return
	}
	if muted {
		http.Error(response, "camera unavailable while muted", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 8*time.Second)
	defer cancel()
	frame, err := camera.Get().Snapshot(ctx)
	if err != nil {
		http.Error(response, err.Error(), http.StatusServiceUnavailable)
		return
	}
	// Rook has no hardware camera shutter. A mute pressed during capture must
	// still prevent the completed frame from leaving this process.
	muted, err = currentMuteState()
	if err != nil {
		http.Error(response, "camera mute state unavailable", http.StatusServiceUnavailable)
		return
	}
	if muted {
		http.Error(response, "camera unavailable while muted", http.StatusForbidden)
		return
	}
	response.Header().Set("Content-Type", "image/jpeg")
	response.Header().Set("Cache-Control", "no-store")
	if err := jpeg.Encode(response, frame.Full(), &jpeg.Options{Quality: 88}); err != nil {
		log.Printf("camera JPEG: %v", err)
	}
}

func currentMuteState() (bool, error) {
	stateBytes, err := os.ReadFile(muteStatePath)
	if err != nil {
		return false, err
	}
	return parseMuteState(stateBytes)
}

func parseMuteState(data []byte) (bool, error) {
	var state struct {
		Muted *bool `json:"muted"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return false, err
	}
	if state.Muted == nil {
		return false, errors.New("mute state missing")
	}
	return *state.Muted, nil
}
