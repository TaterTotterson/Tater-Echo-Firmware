package microwakeword

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureReleaseCompanionRuntimeInstallsAndCachesVerifiedAssets(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TATER_MWW_DIR", dir)
	runtimeBody := []byte("\x7fELFtarget-runtime")
	ortBody := []byte("\x7fELFpinned-onnx-runtime")
	melONNXBody := []byte("pinned-onnx-melspectrogram")
	embedONNXBody := []byte("pinned-onnx-embedding")
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		files := map[string][]byte{
			"/runtime":    runtimeBody,
			"/ort":        ortBody,
			"/mel-onnx":   melONNXBody,
			"/embed-onnx": embedONNXBody,
		}
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	defer server.Close()

	previousRuntimeSHA := ReleaseRuntimeSHA256
	previousORTSHA := ReleaseORTRuntimeSHA256
	previousRuntimeURL := releaseRuntimeURL
	previousORTURL := releaseORTRuntimeURL
	previousMelONNXURL, previousMelONNXSHA := openWakeWordMelspecONNXURL, openWakeWordMelspecONNXSHA
	previousEmbedONNXURL, previousEmbedONNXSHA := openWakeWordEmbedONNXURL, openWakeWordEmbedONNXSHA
	t.Cleanup(func() {
		ReleaseRuntimeSHA256 = previousRuntimeSHA
		ReleaseORTRuntimeSHA256 = previousORTSHA
		releaseRuntimeURL = previousRuntimeURL
		releaseORTRuntimeURL = previousORTURL
		openWakeWordMelspecONNXURL, openWakeWordMelspecONNXSHA = previousMelONNXURL, previousMelONNXSHA
		openWakeWordEmbedONNXURL, openWakeWordEmbedONNXSHA = previousEmbedONNXURL, previousEmbedONNXSHA
	})
	ReleaseRuntimeSHA256 = companionDigest(runtimeBody)
	ReleaseORTRuntimeSHA256 = companionDigest(ortBody)
	releaseRuntimeURL = func(_, _ string) string { return server.URL + "/runtime" }
	releaseORTRuntimeURL = func(_, _ string) string { return server.URL + "/ort" }
	openWakeWordMelspecONNXURL, openWakeWordMelspecONNXSHA = server.URL+"/mel-onnx", companionDigest(melONNXBody)
	openWakeWordEmbedONNXURL, openWakeWordEmbedONNXSHA = server.URL+"/embed-onnx", companionDigest(embedONNXBody)

	updated, err := EnsureReleaseCompanionRuntime(context.Background(), "checkers", "v2.2.0")
	if err != nil || !updated {
		t.Fatalf("updated=%v err=%v", updated, err)
	}
	for name, expected := range map[string][]byte{
		RuntimeFilename:               runtimeBody,
		OWWORTRuntimeFilename:         ortBody,
		OWWMelspectrogramONNXFilename: melONNXBody,
		OWWEmbeddingONNXFilename:      embedONNXBody,
	} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(body) != string(expected) {
			t.Fatalf("installed %s = %q, err=%v", name, body, err)
		}
	}
	updated, err = EnsureReleaseCompanionRuntime(context.Background(), "checkers", "v2.2.0")
	if err != nil || updated || requests != 4 {
		t.Fatalf("cached updated=%v requests=%d err=%v", updated, requests, err)
	}
}

func TestExpectedORTRuntimeSHAUsesReleaseTargetDigest(t *testing.T) {
	previous := ReleaseORTRuntimeSHA256
	t.Cleanup(func() { ReleaseORTRuntimeSHA256 = previous })
	ReleaseORTRuntimeSHA256 = companionDigest([]byte("linux-musl-armv7-runtime"))
	if got := expectedORTRuntimeSHA256(); got != ReleaseORTRuntimeSHA256 {
		t.Fatalf("runtime digest = %q, want release digest %q", got, ReleaseORTRuntimeSHA256)
	}
}

func TestEnsureReleaseCompanionRuntimeIgnoresDeveloperBuild(t *testing.T) {
	previous := ReleaseRuntimeSHA256
	t.Cleanup(func() { ReleaseRuntimeSHA256 = previous })
	ReleaseRuntimeSHA256 = ""
	updated, err := EnsureReleaseCompanionRuntime(context.Background(), "biscuit", "v0.0.0-dev")
	if err != nil || updated {
		t.Fatalf("developer bootstrap updated=%v err=%v", updated, err)
	}
}
