//go:build linux

package gpucompositor

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestAthenaMesaIdentityAndTargetIsolation(t *testing.T) {
	value, err := os.ReadFile("testdata/athena-mesa-runtime.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseMesaManifest(value); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{append(append([]byte{}, value...), '{', '}'), []byte(strings.Replace(string(value), athenaRuntimeID, crownRuntimeID, 1)), []byte(`{}`)} {
		if _, err := parseMesaManifest(bad); err == nil {
			t.Fatal("accepted changed Mesa metadata")
		}
	}
	if validateManifestForTarget(value, "crown") == nil {
		t.Fatal("Android path accepted native Mesa")
	}
	t.Setenv("TATER_FIRMWARE_TARGET", "athena")
	t.Setenv("TATER_GPU_ENABLE", "1")
	t.Setenv("TATER_GPU_ROOT", "")
	c, enabled := configFromEnvironment()
	if !enabled || c.root != "/data/tater-mesa" {
		t.Fatal(c, enabled)
	}
	program, arguments := c.invocation("--capabilities")
	if program != "/data/tater-mesa/lib/ld-musl-armhf.so.1" || len(arguments) != 2 || arguments[0] != c.helper {
		t.Fatal(program, arguments)
	}
	for _, item := range c.environment() {
		if strings.HasPrefix(item, "LD_PRELOAD=") || strings.HasPrefix(item, "ANDROID_") {
			t.Fatal(item)
		}
	}
	if !slices.Contains(c.environment(), "MESA_LOADER_DRIVER_OVERRIDE=panfrost") {
		t.Fatal(c.environment())
	}
	g, ok := GeometryForTarget("athena")
	if !ok || g.SourceWidth != 1280 || g.SourceHeight != 800 || g.PanelWidth != 800 || g.PanelHeight != 1280 {
		t.Fatal(g)
	}
	t.Setenv("TATER_GPU_ENABLE", "0")
	if _, enabled := configFromEnvironment(); enabled {
		t.Fatal("ignored CPU override")
	}
}
