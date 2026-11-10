package mic

import (
	"errors"
	"io"
	"testing"
	"time"

	pkgmic "github.com/TaterTotterson/Tater-Echo-Firmware/pkg/mic"
)

func TestCaptureWatchdogInterruptsPartialAndStalledReads(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "silent", true: "partial"}[partial], func(t *testing.T) {
			reader, writer := io.Pipe()
			defer writer.Close()
			defer reader.Close()
			output := make(chan pkgmic.Capture, 1)
			done := make(chan error, 1)
			go func() {
				done <- readCaptureBatches(reader, 16, output, 30*time.Millisecond, 30*time.Millisecond, func() { reader.Close() })
			}()
			if partial {
				if _, err := writer.Write([]byte{1, 2}); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				if !errors.Is(err, errCaptureStalled) {
					t.Fatalf("error=%v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("watchdog left reader blocked")
			}
			if len(output) != 0 {
				t.Fatal("incomplete PCM escaped capture")
			}
		})
	}
}

func TestCaptureWatchdogRestartsDeadlineAfterCompleteBatch(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	output := make(chan pkgmic.Capture, 1)
	done := make(chan error, 1)
	go func() {
		done <- readCaptureBatches(reader, 4, output, time.Second, 30*time.Millisecond, func() { reader.Close() })
	}()
	before := time.Now()
	if _, err := writer.Write([]byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	capture := <-output
	if capture.CapturedAt.Before(before) || len(capture.PCM) != 4 {
		t.Fatalf("capture=%+v", capture)
	}
	select {
	case err := <-done:
		if !errors.Is(err, errCaptureStalled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("progress deadline did not replace startup deadline")
	}
}
