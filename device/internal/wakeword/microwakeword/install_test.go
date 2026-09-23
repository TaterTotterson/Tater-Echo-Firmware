package microwakeword

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/clock"
)

func TestPackageHTTPClientUsesBuildTimeTLSFloor(t *testing.T) {
	previous := clock.BuildUnix
	t.Cleanup(func() { clock.BuildUnix = previous })
	// A build timestamp ahead of the test host simulates an Echo whose wall
	// clock is years behind the firmware it is running.
	built := time.Now().Add(time.Hour).Truncate(time.Second)
	clock.BuildUnix = strconv.FormatInt(built.Unix(), 10)

	client := packageHTTPClient(time.Second)
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.TLSClientConfig == nil || transport.TLSClientConfig.Time == nil {
		t.Fatal("wake-model HTTP client has no TLS verification clock")
	}
	if got := transport.TLSClientConfig.Time(); !got.Equal(built) {
		t.Fatalf("TLS verification time = %s, want build floor %s", got, built)
	}
}

func TestInstallPackageURLDownloadsValidatedAtomicPackage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TATER_MWW_DIR", dir)
	raw, err := os.ReadFile(filepath.Join("models", "hey_tater.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["model"] = "model.tflite"
	raw, _ = json.Marshal(manifest)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wake.json":
			_, _ = w.Write(raw)
		case "/model.tflite":
			_, _ = w.Write([]byte{8, 0, 0, 0, 'T', 'F', 'L', '3', 1, 2, 3, 4})
		default:
			http.NotFound(w, r)
		}
	}))
	packageName, err := InstallPackageURL(context.Background(), server.URL+"/wake.json")
	if err != nil {
		t.Fatal(err)
	}
	server.Close()
	installed, err := os.ReadFile(filepath.Join(dir, packageName+".json"))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseManifest(installed)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Model != packageName+".tflite" {
		t.Fatalf("installed model = %q", parsed.Model)
	}
	if _, err := InstallPackageURL(context.Background(), server.URL+"/wake.json"); err != nil {
		t.Fatalf("cached package required network: %v", err)
	}
}

func TestInstallPackageURLRevisionRefreshesStableURL(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TATER_MWW_DIR", dir)
	raw, err := os.ReadFile(filepath.Join("models", "hey_tater.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["model"] = "model.tflite"
	raw, _ = json.Marshal(manifest)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path == "/wake.json" {
			_, _ = w.Write(raw)
			return
		}
		if r.URL.Path == "/model.tflite" {
			_, _ = w.Write([]byte{8, 0, 0, 0, 'T', 'F', 'L', '3', byte(requests)})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	first, err := InstallPackageURLRevision(context.Background(), server.URL+"/wake.json", "revision-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := InstallPackageURLRevision(context.Background(), server.URL+"/wake.json", "revision-2")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("revision change reused package %q", first)
	}
	if requests != 4 {
		t.Fatalf("network requests = %d, want two manifest/model pairs", requests)
	}
	if _, err := InstallPackageURLRevision(context.Background(), server.URL+"/wake.json", "revision-2"); err != nil {
		t.Fatal(err)
	}
	if requests != 4 {
		t.Fatalf("cached revision performed another request: %d", requests)
	}
}
