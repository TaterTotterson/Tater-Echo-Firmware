package show

import (
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
)

func TestCaptureCameraSnapshotReturnsBoundedImage(t *testing.T) {
	jpeg := []byte("\xff\xd8fresh-show-frame\xff\xd9")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/snapshot" {
			http.NotFound(w, request)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(jpeg)
	}))
	defer server.Close()

	result, err := CaptureCameraSnapshot(context.Background(), strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Image) != string(jpeg) || result.ContentType != "image/jpeg" {
		t.Fatalf("snapshot = %#v", result)
	}
}

func TestStreamCameraVideoDeliversMultipartFrames(t *testing.T) {
	frames := [][]byte{
		{0xff, 0xd8, 0x01, 0xff, 0xd9},
		{0xff, 0xd8, 0x02, 0xff, 0xd9},
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/stream" {
			http.NotFound(writer, request)
			return
		}
		stream := multipart.NewWriter(writer)
		if err := stream.SetBoundary("camera-test"); err != nil {
			t.Fatal(err)
		}
		writer.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=camera-test")
		for _, frame := range frames {
			header := textproto.MIMEHeader{}
			header.Set("Content-Type", "image/jpeg")
			header.Set("Content-Length", fmt.Sprint(len(frame)))
			part, err := stream.CreatePart(header)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = part.Write(frame)
		}
		_ = stream.Close()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var got [][]byte
	err := StreamCameraVideo(ctx, strings.TrimPrefix(server.URL, "http://"), func(frame []byte) {
		got = append(got, append([]byte(nil), frame...))
		if len(got) == len(frames) {
			cancel()
		}
	})
	if err != context.Canceled {
		t.Fatalf("stream error = %v, want context cancellation", err)
	}
	if len(got) != len(frames) || string(got[0]) != string(frames[0]) || string(got[1]) != string(frames[1]) {
		t.Fatalf("stream frames = %x, want %x", got, frames)
	}
}

func TestStreamCameraVideoProfileRequestsEchoLowProfile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/stream" || request.URL.Query().Get("profile") != "echo-low" {
			t.Fatalf("stream request URL = %q, want /stream?profile=echo-low", request.URL.String())
		}
		stream := multipart.NewWriter(writer)
		_ = stream.SetBoundary("camera-test")
		writer.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=camera-test")
		part, _ := stream.CreatePart(textproto.MIMEHeader{"Content-Type": {"image/jpeg"}})
		_, _ = part.Write([]byte{0xff, 0xd8, 0xff, 0xd9})
		_ = stream.Close()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	err := StreamCameraVideoProfile(ctx, strings.TrimPrefix(server.URL, "http://"), "echo-low", func([]byte) { cancel() })
	if err != context.Canceled {
		t.Fatalf("stream error = %v, want context cancellation", err)
	}
}

func TestCaptureCameraSnapshotRejectsNonImage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("not an image"))
	}))
	defer server.Close()

	if _, err := CaptureCameraSnapshot(context.Background(), strings.TrimPrefix(server.URL, "http://")); err == nil {
		t.Fatal("non-image response was accepted")
	}
}

func TestCaptureCameraVideoFrameRequestsVideoProfile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/snapshot" || request.URL.Query().Get("profile") != "video" {
			t.Fatalf("video request URL = %q, want /snapshot?profile=video", request.URL.String())
		}
		writer.Header().Set("Content-Type", "image/jpeg")
		_, _ = writer.Write([]byte{0xff, 0xd8, 0xff, 0xd9})
	}))
	defer server.Close()

	result, err := CaptureCameraVideoFrame(context.Background(), strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Image) != 4 || result.ContentType != "image/jpeg" {
		t.Fatalf("video frame = %#v", result)
	}
}

func TestCaptureCameraVideoFrameProfileRequestsEchoLowProfile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/snapshot" || request.URL.Query().Get("profile") != "echo-low" {
			t.Fatalf("video request URL = %q, want /snapshot?profile=echo-low", request.URL.String())
		}
		writer.Header().Set("Content-Type", "image/jpeg")
		_, _ = writer.Write([]byte{0xff, 0xd8, 0xff, 0xd9})
	}))
	defer server.Close()

	if _, err := CaptureCameraVideoFrameProfile(context.Background(), strings.TrimPrefix(server.URL, "http://"), "echo-low"); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureCameraVideoFrameProfileRequestsEchoBalancedProfile(t *testing.T) {
	var requested string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requested = request.URL.Query().Get("profile")
		response.Header().Set("Content-Type", "image/jpeg")
		_, _ = response.Write([]byte{0xff, 0xd8, 0xff, 0xd9})
	}))
	defer server.Close()
	if _, err := CaptureCameraVideoFrameProfile(context.Background(), strings.TrimPrefix(server.URL, "http://"), "echo-balanced"); err != nil {
		t.Fatal(err)
	}
	if requested != "echo-balanced" {
		t.Fatalf("camera profile = %q, want echo-balanced", requested)
	}
}
