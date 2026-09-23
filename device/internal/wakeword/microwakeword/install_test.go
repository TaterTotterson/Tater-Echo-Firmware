package microwakeword

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

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
