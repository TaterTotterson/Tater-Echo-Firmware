//go:build linux

package videostream

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAthenaAPDecoderRequiresSeparateOptIn(t *testing.T) {
	t.Setenv("TATER_FIRMWARE_TARGET", "athena")
	t.Setenv("TATER_VDEC_ENABLE", "1")
	t.Setenv("TATER_ATHENA_AP_ENABLE", "")
	if _, enabled := hardwareDecoderConfigFromEnvironment(); enabled {
		t.Fatal("experimental AP decoder enabled by a generic VDEC flag")
	}
	t.Setenv("TATER_ATHENA_AP_ENABLE", "1")
	t.Setenv("TATER_VDEC_LIBRARY_PATH", "/unqualified/lib")
	t.Setenv("TATER_VDEC_COMPONENT", "/unqualified/component")
	config, enabled := hardwareDecoderConfigFromEnvironment()
	if !enabled || config.target != "athena" || config.component != athenaCodecRoot+"/lib/libvcodecdrv.so" || config.libraryPath != athenaCodecRoot+"/lib" {
		t.Fatal("Athena runtime was not isolated", config, enabled)
	}
	if strings.Contains(strings.Join(config.environment(), "\n"), "LD_PRELOAD=") {
		t.Fatal("Athena inherited another target's compatibility preload")
	}
	t.Setenv("TATER_VDEC_ENABLE", "0")
	if _, enabled := hardwareDecoderConfigFromEnvironment(); enabled {
		t.Fatal("AP opt-in overrode a disabled decoder")
	}
}

func TestAthenaAPRuntimeManifestIsExact(t *testing.T) {
	manifest := athenaVDECManifest{Schema: 1, Target: "athena", Experimental: true,
		RuntimeID: "athena-mt8183-ap-vdec-1", TreeSHA256: athenaVDECTreeSHA256,
		FileCount: 24, RuntimeBytes: 6042836}
	value, _ := json.Marshal(manifest)
	if err := validateAthenaVDECManifest(value); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]byte{
		[]byte(strings.Replace(string(value), `"experimental":true`, `"experimental":false`, 1)),
		[]byte(strings.Replace(string(value), `"file_count":24`, `"file_count":23`, 1)),
		[]byte(strings.Replace(string(value), `"athena"`, `"cronos"`, 1)),
		[]byte(strings.Replace(string(value), `"runtime_bytes":6042836`, `"runtime_bytes":6042835`, 1)),
		append(append([]byte(nil), value...), []byte(` {}`)...),
		[]byte(`{"unknown":true}`),
	} {
		if validateAthenaVDECManifest(invalid) == nil {
			t.Fatal("unqualified Athena manifest accepted", string(invalid))
		}
	}
	if len(athenaVDECRuntimeFiles) != 24 {
		t.Fatal("incomplete runtime file allowlist")
	}
	for name, hash := range athenaVDECRuntimeFiles {
		if len(hash) != 64 || strings.Contains(name, "..") || strings.HasPrefix(name, "/") {
			t.Fatal("invalid runtime file pin", name)
		}
	}
}

func TestAthenaAPCapabilitiesStayLiveBaselineOnly(t *testing.T) {
	valid := []byte(`{"adapter":"tater-athena-ap-vdec","version":1,"codec":"h264","stream_versions":[2],"stream_v2_output":"rgba","max_width":1280,"max_height":960,"profiles":["baseline"],"single_slice_only":true,"live_only":true}`)
	if err := validateVDECCapabilitiesForTarget(valid, "athena"); err != nil {
		t.Fatal(err)
	}
	if validateVDECCapabilities(valid) == nil {
		t.Fatal("Athena's live-only helper accepted as a general media decoder")
	}
	for _, replacement := range [][2]string{
		{`"baseline"`, `"high"`}, {`"live_only":true`, `"live_only":false`},
		{`"single_slice_only":true`, `"single_slice_only":false`},
		{`"max_width":1280`, `"max_width":1920`}, {`"max_height":960`, `"max_height":1088`},
	} {
		if validateVDECCapabilitiesForTarget([]byte(strings.Replace(string(valid), replacement[0], replacement[1], 1)), "athena") == nil {
			t.Fatal("unqualified Athena capability expansion accepted", replacement)
		}
	}
	if err := preflightHardwareDecoder(context.Background(), hardwareDecoderConfig{target: "athena"}, ""); err == nil || !strings.Contains(err.Error(), "live-only") {
		t.Fatal("general media was not kept on its qualified path", err)
	}
}

func TestAthenaAPPinsOnlyOrdinaryBoundedFiles(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "runtime")
	if err := os.WriteFile(path, []byte("runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := fileSHA256(path)
	if err != nil || !athenaPinnedFile(path, hash) {
		t.Fatal("ordinary pinned file rejected", err)
	}
	link := filepath.Join(directory, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if athenaPinnedFile(link, hash) || athenaPinnedFile(directory, hash) || athenaOrdinaryFile(path, 6) {
		t.Fatal("symlink, directory or oversized input accepted")
	}
}
