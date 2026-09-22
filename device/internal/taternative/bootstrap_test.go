package taternative

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadBootstrap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native.json")
	if err := os.WriteFile(path, []byte(`{"url":" http://tater.local:8501 ","token":" 123456 ","room":" Kitchen "}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadBootstrap(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.URL != "http://tater.local:8501" || cfg.Token != "123456" || cfg.Room != "Kitchen" {
		t.Fatalf("unexpected bootstrap: %+v", cfg)
	}
}

func TestMissingBootstrapKeepsLegacyMode(t *testing.T) {
	cfg, err := LoadBootstrap(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil || cfg.URL != "" {
		t.Fatalf("missing bootstrap = %+v, %v", cfg, err)
	}
}
