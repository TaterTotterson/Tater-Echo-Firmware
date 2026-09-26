package taternative

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/wifi"
)

const (
	DefaultSetupListen   = ":80"
	DefaultEmOSWiFiPath  = "/data/emos/wpa.conf"
	DefaultSetupMarker   = "/data/local/etc/tater/setup_enabled"
	DefaultDeviceToken   = "/data/local/etc/tater/device_token"
	defaultSetupDevice   = "Tater Echo"
	maxSetupRequestBytes = 4096
)

// SetupPortalOptions makes the first-boot portal testable without granting a
// host test permission to write under /data or signal PID 1.
type SetupPortalOptions struct {
	Listen        string
	WiFiPath      string
	BootstrapPath string
	TokenPath     string
	MarkerPath    string
	// ApplyWiFi replaces the emOS config-file write on platforms such as
	// Checkers where Android owns the station supplicant. It runs only after
	// the success page has reached the phone, because joining the selected
	// network necessarily tears down the setup connection.
	ApplyWiFi    func(ssid []byte, password string) error
	Restart      func()
	RestartDelay time.Duration
}

func (o SetupPortalOptions) defaults() SetupPortalOptions {
	if strings.TrimSpace(o.Listen) == "" {
		o.Listen = DefaultSetupListen
	}
	if strings.TrimSpace(o.WiFiPath) == "" {
		o.WiFiPath = DefaultEmOSWiFiPath
	}
	if strings.TrimSpace(o.BootstrapPath) == "" {
		o.BootstrapPath = DefaultBootstrapPath
	}
	if strings.TrimSpace(o.TokenPath) == "" {
		o.TokenPath = DefaultDeviceToken
	}
	if strings.TrimSpace(o.MarkerPath) == "" {
		o.MarkerPath = DefaultSetupMarker
	}
	if o.RestartDelay <= 0 {
		o.RestartDelay = 1200 * time.Millisecond
	}
	if o.Restart == nil {
		o.Restart = func() {
			// emOS PID 1 catches SIGTERM, stops its supervised services,
			// syncs, remounts /data read-only, and only then reboots.
			_ = syscall.Kill(1, syscall.SIGTERM)
		}
	}
	return o
}

type setupPageData struct {
	Server string
	Name   string
	Room   string
	Error  string
}

var setupPage = template.Must(template.New("setup").Parse(`<!doctype html>
<html><head><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Tater Echo Setup</title><style>
:root{color-scheme:dark;--bg:#0b0f10;--panel:#141a1c;--line:#38464b;--text:#f4f1e9;--muted:#a8b3ad;--orange:#ff8a2a;--orange2:#ffc07f;--green:#39d4a0;--red:#ff6868}
*{box-sizing:border-box}body{margin:0;min-height:100vh;background:radial-gradient(circle at 50% -10%,rgba(255,138,42,.22),transparent 42%),linear-gradient(180deg,#111719,#090c0d);color:var(--text);font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
main{max-width:760px;margin:auto;padding:24px 16px}.shell{border:1px solid rgba(255,192,127,.18);background:rgba(14,18,20,.97);border-radius:18px;overflow:hidden;box-shadow:0 20px 70px rgba(0,0,0,.38)}
.top{padding:24px 22px 18px;border-bottom:1px solid rgba(255,255,255,.08);background:linear-gradient(135deg,rgba(255,138,42,.13),rgba(57,212,160,.08))}.brand{display:flex;align-items:center;gap:12px;margin-bottom:13px}.mark{display:grid;place-items:center;width:43px;height:43px;border-radius:12px;background:linear-gradient(135deg,var(--orange),var(--orange2));color:#1a0d03;font-weight:950}.kicker{color:var(--orange2);font-size:12px;font-weight:850;letter-spacing:.08em;text-transform:uppercase}
h1{font-size:28px;line-height:1.1;margin:0 0 8px}p{margin:0;color:var(--muted);line-height:1.45}.body{padding:22px}.status{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:10px;margin-bottom:20px}.chip{border:1px solid rgba(255,255,255,.08);background:rgba(0,0,0,.2);border-radius:12px;padding:11px 12px}.chip b{display:block;font-size:12px;color:var(--muted)}.chip span{display:block;margin-top:4px;font-weight:780;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
form{display:grid;gap:14px}.grid{display:grid;grid-template-columns:1fr 1fr;gap:14px}.field{display:grid;gap:7px}label{font-weight:760;font-size:13px}input{width:100%;border:1px solid var(--line);border-radius:12px;background:#0f1416;color:var(--text);padding:13px 12px;font-size:16px;outline:none}input:focus{border-color:var(--orange2);box-shadow:0 0 0 3px rgba(255,138,42,.18)}.hint{color:var(--muted);font-size:12px;line-height:1.35}
button{width:100%;border:0;border-radius:12px;background:linear-gradient(135deg,var(--orange),var(--orange2));color:#1d0e03;font-weight:900;padding:14px;font-size:16px;margin-top:4px}.foot,.error{margin-top:2px;padding:13px;border-radius:12px;font-size:13px;line-height:1.4}.foot{border:1px solid rgba(57,212,160,.18);background:rgba(57,212,160,.075);color:#c9f5e6}.error{border:1px solid rgba(255,104,104,.28);background:rgba(255,104,104,.09);color:#ffd1d1}
@media(max-width:640px){main{padding:10px}.shell{border-radius:0;border-left:0;border-right:0}.top,.body{padding:20px 16px}.grid,.status{grid-template-columns:1fr}h1{font-size:25px}}
</style></head><body><main><section class="shell">
<div class="top"><div class="brand"><div class="mark">T</div><div><div class="kicker">Tater Native</div><h1>Echo Satellite Setup</h1></div></div><p>Connect this Echo Dot to Wi-Fi and pair it directly with Tater.</p></div>
<div class="body"><div class="status"><div class="chip"><b>Setup network</b><span>Tater-Setup</span></div><div class="chip"><b>Setup address</b><span>192.168.4.1</span></div></div>
{{if .Error}}<div class="error">{{.Error}}</div>{{end}}
<form method="post" action="/save">
<div class="field"><label for="ssid">Wi-Fi SSID</label><input id="ssid" name="ssid" maxlength="32" autocomplete="off" autocapitalize="none" required><div class="hint">Your normal 2.4 GHz home Wi-Fi network.</div></div>
<div class="field"><label for="password">Wi-Fi password</label><input id="password" name="password" type="password" maxlength="64" autocomplete="current-password"><div class="hint">Leave blank only for an open network.</div></div>
<div class="field"><label for="server">Tater server</label><input id="server" name="server" value="{{.Server}}" placeholder="http://192.168.1.20:8501" maxlength="512" autocapitalize="none" required><div class="hint">Use the LAN address shown by Tater.</div></div>
<div class="grid"><div class="field"><label for="token">Pairing code</label><input id="token" name="token" inputmode="numeric" autocomplete="one-time-code" placeholder="123 456" maxlength="32" required><div class="hint">In Tater, open Satellites and choose Add Satellite first.</div></div>
<div class="field"><label for="room">Room</label><input id="room" name="room" value="{{.Room}}" maxlength="96" placeholder="Kitchen"></div></div>
<div class="field"><label for="name">Device name</label><input id="name" name="name" value="{{.Name}}" maxlength="96" placeholder="Kitchen Echo"><div class="hint">This is how the Echo appears in Tater.</div></div>
<button type="submit">Save and connect</button><div class="foot">After saving, this setup network disappears. Reconnect your phone or computer to normal Wi-Fi, then return to Tater.</div>
</form></div></section></main></body></html>`))

const setupSavedPage = `<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"><title>Tater Echo Saved</title><style>body{margin:0;min-height:100vh;display:grid;place-items:center;background:#0b0f10;color:#f4f1e9;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}.card{max-width:560px;margin:20px;padding:28px;border-radius:18px;border:1px solid #38464b;background:#141a1c;box-shadow:0 20px 70px #0008}.mark{font-size:42px}h1{margin:8px 0}p{color:#a8b3ad;line-height:1.5}.ok{color:#39d4a0;font-weight:800}</style></head><body><div class="card"><div class="mark">🥔</div><div class="ok">Settings saved</div><h1>Your Tater Echo is connecting</h1><p>The setup network will disappear while the Echo reboots. Reconnect to your normal Wi-Fi and return to Tater to finish pairing.</p></div></body></html>`

type setupPortal struct {
	opts SetupPortalOptions
	once sync.Once
}

// NewSetupPortalHandler returns the complete captive-portal handler. The same
// page answers arbitrary GET paths so Android, iOS and Windows connectivity
// probes all land on setup without relying on their redirect heuristics.
func NewSetupPortalHandler(opts SetupPortalOptions) http.Handler {
	return &setupPortal{opts: opts.defaults()}
}

func (p *setupPortal) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.URL.Path == "/save" {
		p.save(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p.render(w, setupPageData{})
}

func (p *setupPortal) pageDefaults() setupPageData {
	data := setupPageData{Name: defaultSetupDevice}
	if cfg, err := LoadBootstrap(p.opts.BootstrapPath); err == nil {
		data.Server = cfg.URL
		data.Name = cfg.DeviceName
		data.Room = cfg.Room
	}
	if strings.TrimSpace(data.Name) == "" {
		data.Name = defaultSetupDevice
	}
	return data
}

func (p *setupPortal) render(w http.ResponseWriter, data setupPageData) {
	defaults := p.pageDefaults()
	if data.Server == "" {
		data.Server = defaults.Server
	}
	if data.Name == "" {
		data.Name = defaults.Name
	}
	if data.Room == "" {
		data.Room = defaults.Room
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := setupPage.Execute(w, data); err != nil {
		log.Printf("[setup] render portal: %v", err)
	}
}

func (p *setupPortal) save(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSetupRequestBytes)
	if err := r.ParseForm(); err != nil {
		p.renderError(w, r, "The setup form was too large or unreadable.")
		return
	}
	ssid := r.FormValue("ssid")
	password := r.FormValue("password")
	server := strings.TrimSpace(r.FormValue("server"))
	token := normalizeSetupToken(r.FormValue("token"))
	name := strings.TrimSpace(r.FormValue("name"))
	room := strings.TrimSpace(r.FormValue("room"))
	if len(server) > 512 || len(token) > 256 || len(name) > 96 || len(room) > 96 {
		p.renderError(w, r, "One of the setup fields is too long.")
		return
	}
	if name == "" {
		name = defaultSetupDevice
	}
	if token == "" {
		p.renderError(w, r, "Enter the pairing code shown by Tater.")
		return
	}
	nativeURL, err := NormalizeURL(server)
	if err != nil {
		p.renderError(w, r, "Enter a valid Tater server address.")
		return
	}
	wpa, err := wifi.EmOSProvisioningConfig([]byte(ssid), password)
	if err != nil {
		p.renderError(w, r, err.Error()+".")
		return
	}
	bootstrap, err := json.MarshalIndent(Bootstrap{
		URL: nativeURL, Token: token, DeviceName: name, Room: room,
	}, "", "  ")
	if err != nil {
		http.Error(w, "Could not prepare settings", http.StatusInternalServerError)
		return
	}
	bootstrap = append(bootstrap, '\n')

	// Write the native half first and WiFi last. Setup remains active if the
	// first write fails, and a device can never reboot with WiFi marked ready
	// but no Tater address/token beside it.
	if err := writeSetupFile(p.opts.BootstrapPath, bootstrap, 0o600); err != nil {
		log.Printf("[setup] write native config: %v", err)
		http.Error(w, "Could not save Tater settings", http.StatusInternalServerError)
		return
	}
	if err := os.Remove(p.opts.TokenPath); err != nil && !os.IsNotExist(err) {
		log.Printf("[setup] remove old device token: %v", err)
		http.Error(w, "Could not reset the old pairing", http.StatusInternalServerError)
		return
	}
	if p.opts.ApplyWiFi == nil {
		if err := writeSetupFile(p.opts.WiFiPath, []byte(wpa), 0o600); err != nil {
			log.Printf("[setup] write WiFi config: %v", err)
			http.Error(w, "Could not save Wi-Fi settings", http.StatusInternalServerError)
			return
		}
	}
	if err := writeSetupFile(p.opts.MarkerPath, []byte("1\n"), 0o600); err != nil {
		log.Printf("[setup] refresh setup marker: %v", err)
	}
	syscall.Sync()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(setupSavedPage))

	p.once.Do(func() {
		go func() {
			time.Sleep(p.opts.RestartDelay)
			if p.opts.ApplyWiFi != nil {
				if err := p.opts.ApplyWiFi([]byte(ssid), password); err != nil {
					log.Printf("[setup] apply Wi-Fi settings: %v", err)
					return
				}
			}
			p.opts.Restart()
		}()
	})
}

func (p *setupPortal) renderError(w http.ResponseWriter, r *http.Request, message string) {
	w.WriteHeader(http.StatusBadRequest)
	p.render(w, setupPageData{
		Server: strings.TrimSpace(r.FormValue("server")),
		Name:   strings.TrimSpace(r.FormValue("name")),
		Room:   strings.TrimSpace(r.FormValue("room")),
		Error:  message,
	})
}

func normalizeSetupToken(raw string) string {
	value := strings.TrimSpace(raw)
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		if r == ' ' || r == '-' {
			return -1
		}
		return r
	}, value)
	if len(digits) == 6 {
		allDigits := true
		for _, r := range digits {
			allDigits = allDigits && r >= '0' && r <= '9'
		}
		if allDigits {
			return digits
		}
	}
	return value
}

func writeSetupFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("chmod %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s: %w", path, err)
	}
	return nil
}

// RunSetupPortal serves until emOS shuts the process down after a successful
// save. It is intentionally a command of the existing firmware binary, so the
// boot image only needs AP support and does not carry a second web stack.
func RunSetupPortal(opts SetupPortalOptions) error {
	opts = opts.defaults()
	log.Printf("[setup] captive portal listening on %s", opts.Listen)
	server := &http.Server{
		Addr:              opts.Listen,
		Handler:           NewSetupPortalHandler(opts),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8 * 1024,
	}
	return server.ListenAndServe()
}
