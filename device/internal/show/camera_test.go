package show

import (
	"context"
	"net/http"
	"net/http/httptest"
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
