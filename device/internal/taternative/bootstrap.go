package taternative

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

const DefaultBootstrapPath = "/data/local/etc/tater/native.json"

// Bootstrap is the small persistent configuration needed before the Echo can
// receive live settings from Tater. Token may initially be a six-digit pairing
// code; the permanent device token returned by hello.ack is stored separately.
type Bootstrap struct {
	URL        string `json:"url"`
	Token      string `json:"token,omitempty"`
	TokenPath  string `json:"token_path,omitempty"`
	DeviceName string `json:"device_name,omitempty"`
	Room       string `json:"room,omitempty"`
}

// LoadBootstrap reads a strict single-object bootstrap file. A missing file
// is not an error because legacy EchoMuse mode is the default.
func LoadBootstrap(filename string) (Bootstrap, error) {
	if strings.TrimSpace(filename) == "" {
		filename = DefaultBootstrapPath
	}
	raw, err := os.ReadFile(filename)
	if errors.Is(err, os.ErrNotExist) {
		return Bootstrap{}, nil
	}
	if err != nil {
		return Bootstrap{}, fmt.Errorf("read Tater native config: %w", err)
	}
	var cfg Bootstrap
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Bootstrap{}, fmt.Errorf("decode Tater native config: %w", err)
	}
	cfg.URL = strings.TrimSpace(cfg.URL)
	cfg.Token = strings.TrimSpace(cfg.Token)
	cfg.TokenPath = strings.TrimSpace(cfg.TokenPath)
	cfg.DeviceName = strings.TrimSpace(cfg.DeviceName)
	cfg.Room = strings.TrimSpace(cfg.Room)
	return cfg, nil
}
