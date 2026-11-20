package taternative

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Identity returns the confirmed device name and room without changing its ID.
func (c *Client) Identity() (string, string) {
	c.identityMu.RLock()
	defer c.identityMu.RUnlock()
	return c.identityName, c.identityRoom
}

func identityText(value any, required bool) (string, error) {
	text, ok := value.(string)
	if !ok || !utf8.ValidString(text) {
		return "", fmt.Errorf("name and room must be UTF-8 text")
	}
	for _, character := range text {
		if character < 32 || character == 127 {
			return "", fmt.Errorf("name and room cannot contain control characters")
		}
	}
	text = strings.Trim(text, " ")
	if len(text) > 63 || (required && text == "") {
		return "", fmt.Errorf("name must be 1–63 bytes and room at most 63 bytes")
	}
	return text, nil
}

// SaveIdentity changes only identity fields, preserving connection credentials
// and unknown bootstrap fields. The rename commits both fields together.
func SaveIdentity(filename, name, room string, fallback Bootstrap) error {
	raw, err := os.ReadFile(filename)
	if errors.Is(err, os.ErrNotExist) {
		raw, err = json.Marshal(fallback)
	}
	if err != nil {
		return fmt.Errorf("read satellite configuration: %w", err)
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(raw, &config); err != nil || config == nil {
		return fmt.Errorf("invalid satellite configuration")
	}
	config["device_name"], _ = json.Marshal(name)
	config["room"], _ = json.Marshal(room)
	config["identity_updated"] = json.RawMessage("true")
	raw, err = json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
		return err
	}
	return atomicWrite(filename, append(raw, '\n'), 0o600)
}

func (c *Client) updateIdentity(requestID string, payload map[string]any) {
	result := map[string]any{"reply_to": requestID, "ok": false}
	name, err := identityText(payload["device_name"], true)
	var room string
	if err == nil {
		room, err = identityText(payload["room"], false)
	}
	if err == nil && c.cfg.BootstrapPath == "" {
		err = fmt.Errorf("persistent satellite configuration is unavailable")
	}
	if err == nil {
		err = SaveIdentity(c.cfg.BootstrapPath, name, room, Bootstrap{URL: c.cfg.URL, Token: c.cfg.Token, TokenPath: c.cfg.TokenPath})
	}
	if err != nil {
		result["error"] = err.Error()
	} else {
		c.identityMu.Lock()
		c.identityName, c.identityRoom = name, room
		c.identityMu.Unlock()
		if hook := c.hooks.IdentityChanged; hook != nil {
			hook(name, room)
		}
		result["ok"] = true
	}
	result["device_name"], result["room"] = c.Identity()
	c.sendJSON("identity.result", "", result)
}
