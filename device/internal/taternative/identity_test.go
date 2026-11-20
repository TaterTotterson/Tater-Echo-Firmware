package taternative

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func identityClient(t *testing.T) (*Client, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "native.json")
	if err := os.WriteFile(path, []byte(`{"url":"ws://tater.test","token":"pairing","token_path":"/saved/token","device_name":"Office Echo","room":"Office","future_setting":{"enabled":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := New(Config{URL: "ws://tater.test", DeviceID: "echo-stable", DeviceName: "Office Echo", Room: "Office", BootstrapPath: path}, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.cancel)
	c.connected.Store(true)
	return c, path
}

func identityReply(t *testing.T, c *Client, values map[string]any) Envelope {
	t.Helper()
	c.handle(Envelope{Type: "identity.set", ID: "identity-request", Payload: values})
	frame := <-c.out
	var result Envelope
	if err := json.Unmarshal(frame.data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Type != "identity.result" || result.Payload["reply_to"] != "identity-request" {
		t.Fatalf("uncorrelated reply: %#v", result)
	}
	return result
}

func TestIdentityPersistsBothFieldsWithoutTouchingPairing(t *testing.T) {
	c, path := identityClient(t)
	called := false
	c.hooks.IdentityChanged = func(name, room string) { called = name == "Kitchen Écho" && room == "" }
	result := identityReply(t, c, map[string]any{"device_name": " Kitchen Écho ", "room": ""})
	if result.Payload["ok"] != true || !called {
		t.Fatalf("save failed: %#v", result)
	}
	if name, room := c.Identity(); name != "Kitchen Écho" || room != "" {
		t.Fatalf("identity = %q %q", name, room)
	}
	if c.cfg.DeviceID != "echo-stable" || !c.connected.Load() {
		t.Fatal("rename changed connection identity")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if saved["token"] != "pairing" || saved["token_path"] != "/saved/token" || saved["url"] != "ws://tater.test" || saved["future_setting"] == nil {
		t.Fatalf("connection fields changed: %#v", saved)
	}
	restored, err := LoadBootstrap(path)
	if err != nil || restored.DeviceName != "Kitchen Écho" || restored.Room != "" || !restored.IdentityUpdated {
		t.Fatalf("restore = %#v, %v", restored, err)
	}
}

func TestIdentityRejectsInvalidInputAndStorageFailure(t *testing.T) {
	for _, value := range []any{nil, 42, "", "   ", "bad\nname", "bad\x00name", strings.Repeat("é", 32)} {
		t.Run("invalid", func(t *testing.T) {
			c, path := identityClient(t)
			before, _ := os.ReadFile(path)
			result := identityReply(t, c, map[string]any{"device_name": value, "room": "Kitchen"})
			after, _ := os.ReadFile(path)
			name, room := c.Identity()
			if result.Payload["ok"] != false || string(before) != string(after) || name != "Office Echo" || room != "Office" {
				t.Fatal("invalid rename changed identity")
			}
		})
	}
	c, path := identityClient(t)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	result := identityReply(t, c, map[string]any{"device_name": "Kitchen", "room": "Kitchen"})
	if name, _ := c.Identity(); result.Payload["ok"] != false || name != "Office Echo" {
		t.Fatal("failed persistence changed live name")
	}
}

func TestIdentityCreatesBootstrapForEnvironmentOnlyLaunch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "native.json")
	c, err := New(Config{URL: "ws://tater.test", Token: "env-token", TokenPath: "/saved/device-token", DeviceID: "stable-id", DeviceName: "Office", BootstrapPath: path}, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.cancel)
	c.connected.Store(true)
	result := identityReply(t, c, map[string]any{"device_name": "Kitchen", "room": "Kitchen"})
	if result.Payload["ok"] != true {
		t.Fatalf("environment-only save failed: %#v", result)
	}
	saved, err := LoadBootstrap(path)
	if err != nil || saved.URL != "ws://tater.test" || saved.Token != "env-token" || saved.TokenPath != "/saved/device-token" || saved.DeviceName != "Kitchen" || saved.Room != "Kitchen" {
		t.Fatalf("bootstrap did not retain the connection: %#v, %v", saved, err)
	}
}
