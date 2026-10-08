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
	if requests != 6 {
		t.Fatalf("network requests = %d, want two manifest/model/bundle probes", requests)
	}
	if _, err := InstallPackageURLRevision(context.Background(), server.URL+"/wake.json", "revision-2"); err != nil {
		t.Fatal(err)
	}
	if requests != 7 {
		t.Fatalf("cached revision should only re-probe its optional bundle: %d", requests)
	}
}

func TestInstallPackageURLInstallsVerifiedOWWCompanion(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TATER_MWW_DIR", dir)
	manifestBody, err := os.ReadFile(filepath.Join("models", "hey_tater.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestBody, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["model"] = "wake.tflite"
	manifestBody, _ = json.Marshal(manifest)
	modelBody := []byte{8, 0, 0, 0, 'T', 'F', 'L', '3', 1, 2, 3, 4}
	metadataBody := []byte("{\"format\":\"openwakeword\"}\n")
	classifierBody := []byte{0x08, 0x07, 'o', 'n', 'n', 'x'}
	bundle := WakeBundle{
		SchemaVersion: 1,
		Type:          "tater_wake_word_bundle",
		WakeWord:      "hey tater",
		Key:           "hey_tater",
		MicroWakeWord: BundleMicroWakeWord{
			Manifest: "wake.json", Model: "wake.tflite",
			ManifestSHA256: companionDigest(manifestBody),
			ModelSHA256:    companionDigest(modelBody),
		},
		OpenWakeWord: BundleOpenWakeWord{
			Metadata: "wake.oww.json", MetadataSHA256: companionDigest(metadataBody),
			RecommendedThreshold: 0.93, RecommendedPatience: 3,
			Artifacts: map[string]BundleArtifact{
				"onnx": {File: "wake.oww.onnx", SHA256: companionDigest(classifierBody), SizeBytes: int64(len(classifierBody))},
			},
		},
	}
	bundleBody, _ := json.Marshal(bundle)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		files := map[string][]byte{
			"/wake.json":             manifestBody,
			"/wake.tflite":           modelBody,
			"/wake.wake-bundle.json": bundleBody,
			"/wake.oww.json":         metadataBody,
			"/wake.oww.onnx":         classifierBody,
		}
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	defer server.Close()

	packageName, err := InstallPackageURL(context.Background(), server.URL+"/wake.json")
	if err != nil {
		t.Fatal(err)
	}
	prefix, _ := companionPrefix(packageName)
	for _, name := range []string{
		prefix + CompanionBundleSuffix,
		prefix + CompanionMetadataSuffix,
		prefix + CompanionONNXModelSuffix,
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("companion file %s: %v", name, err)
		}
	}
	if got := CompanionInstallError(packageName); got != "" {
		t.Fatalf("companion install error = %q", got)
	}
	if !CompanionReady(packageName) {
		t.Fatal("verified MWW package did not expose its paired OWW companion")
	}
	if paired, err := PairedCompanionPackage(packageName); err != nil || paired != packageName {
		t.Fatalf("paired package = %q, %v", paired, err)
	}
	if _, err := InstallPackageURL(context.Background(), server.URL+"/wake.json"); err != nil {
		t.Fatal(err)
	}
	if requests != 5 {
		t.Fatalf("network requests = %d, want five initial bundle files and a fully cached second install", requests)
	}
}

func TestInstallCompanionURLRevisionDoesNotRequireInstalledMWW(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TATER_MWW_DIR", dir)
	metadataBody := []byte("{\"format\":\"openwakeword\"}\n")
	classifierBody := []byte{0x08, 0x07, 'o', 'n', 'n', 'x'}
	bundle := WakeBundle{
		SchemaVersion: 1, Type: "tater_wake_word_bundle", WakeWord: "jojo", Key: "jojo",
		MicroWakeWord: BundleMicroWakeWord{
			Manifest: "jojo.json", Model: "jojo.tflite",
			ManifestSHA256: companionDigest([]byte("manifest")), ModelSHA256: companionDigest([]byte("model")),
		},
		OpenWakeWord: BundleOpenWakeWord{
			Metadata: "jojo.oww.json", MetadataSHA256: companionDigest(metadataBody),
			RecommendedThreshold: 0.92, RecommendedPatience: 3,
			Artifacts: map[string]BundleArtifact{
				"onnx": {File: "jojo.oww.onnx", SHA256: companionDigest(classifierBody), SizeBytes: int64(len(classifierBody))},
			},
		},
	}
	bundleBody, _ := json.Marshal(bundle)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		files := map[string][]byte{
			"/jojo.wake-bundle.json": bundleBody,
			"/jojo.oww.json":         metadataBody,
			"/jojo.oww.onnx":         classifierBody,
		}
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	defer server.Close()
	packageName, err := InstallCompanionURLRevision(context.Background(), server.URL+"/jojo.wake-bundle.json", "rev-1")
	if err != nil {
		t.Fatal(err)
	}
	if cachedPackageValid(filepath.Join(dir, packageName+".json"), filepath.Join(dir, packageName+".tflite")) {
		t.Fatal("OWW-only install unexpectedly created an MWW package")
	}
	if !cachedCompanionValid(packageName) {
		t.Fatal("OWW-only companion was not installed as a verified cache entry")
	}
	if _, err := PairedCompanionPackage(packageName); err == nil {
		t.Fatal("OWW-only package was accepted as a paired MWW + OWW package")
	}
}
