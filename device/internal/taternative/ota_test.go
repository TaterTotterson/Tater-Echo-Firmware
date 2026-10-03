package taternative

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestOTAInstallerWritesInactiveSlotAndFlipsSymlink(t *testing.T) {
	binary := append([]byte("\x7fELF"), make([]byte, 4096)...)
	digest := sha256.Sum256(binary)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(binary)
	}))
	defer server.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "server_a"), []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("server_a", filepath.Join(dir, "server")); err != nil {
		t.Fatal(err)
	}
	installer := NewOTAInstaller()
	installer.ActivePath = filepath.Join(dir, "server")
	if err := installer.Install(context.Background(), OTARequest{
		URL: server.URL, SHA256: hex.EncodeToString(digest[:]), SizeBytes: int64(len(binary)),
	}, nil); err != nil {
		t.Fatal(err)
	}
	link, err := os.Readlink(filepath.Join(dir, "server"))
	if err != nil || link != "server_b" {
		t.Fatalf("active link = %q, err=%v", link, err)
	}
	installed, err := os.ReadFile(filepath.Join(dir, "server_b"))
	if err != nil || string(installed) != string(binary) {
		t.Fatalf("inactive slot mismatch: bytes=%d err=%v", len(installed), err)
	}
}
