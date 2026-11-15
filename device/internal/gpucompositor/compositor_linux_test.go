//go:build linux

package gpucompositor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestAllowlistedRuntimeManifest(t *testing.T) {
	manifest, err := json.Marshal(runtimeManifest{
		Schema: 1, RuntimeID: expectedRuntimeID, TreeSHA256: expectedTreeSHA256,
		FileCount: expectedRuntimeFiles, RuntimeBytes: expectedRuntimeBytes,
		HelperSHA256: expectedHelperSHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateManifest(manifest); err != nil {
		t.Fatal(err)
	}
	if err := validateManifest(append(manifest, []byte("{}")...)); err == nil {
		t.Fatal("trailing manifest data accepted")
	}
}

func TestPrivateLibraryOnlyManifestDoesNotPinSharedHelper(t *testing.T) {
	manifest := runtimeManifest{Schema: 2, RuntimeID: expectedRuntimeID, TreeSHA256: expectedTreeSHA256,
		FileCount: expectedRuntimeFiles, RuntimeBytes: expectedRuntimeBytes}
	value, _ := json.Marshal(manifest)
	if err := validateManifest(value); err != nil {
		t.Fatal(err)
	}
	manifest.HelperSHA256 = expectedHelperSHA256
	value, _ = json.Marshal(manifest)
	if validateManifest(value) == nil {
		t.Fatal("library-only manifest accepted a mutable shared helper")
	}
}

func TestOriginalPreparedCheckersManifestRemainsCompatible(t *testing.T) {
	manifest := runtimeManifest{Schema: 1, RuntimeID: expectedRuntimeID, TreeSHA256: expectedTreeSHA256,
		FileCount: expectedRuntimeFiles, RuntimeBytes: expectedRuntimeBytes, HelperSHA256: legacyPreparedHelperSHA256}
	value, _ := json.Marshal(manifest)
	if err := validateManifestForTarget(value, "checkers"); err != nil {
		t.Fatalf("rejected original pinned runtime metadata: %v", err)
	}
	if validateManifestForTarget(value, "cronos") == nil {
		t.Fatal("accepted Checkers runtime metadata for Cronos")
	}
	manifest.HelperSHA256 = "08d0dcb884d486c9ae3cfaaec127f9e4ff2b28fba9eabfc9e755d3e1cf19de1f"
	value, _ = json.Marshal(manifest)
	if validateManifest(value) == nil {
		t.Fatal("accepted unknown legacy helper metadata")
	}
}

func TestCapabilitiesAreExact(t *testing.T) {
	valid := []byte(`{"adapter":"tater-gpu-rotate","version":1,"source_width":960,"source_height":480,"panel_width":480,"panel_height":960,"output":"bgra"}`)
	if err := validateCapabilities(valid); err != nil {
		t.Fatal(err)
	}
	if err := validateCapabilities([]byte(`{"adapter":"tater-gpu-rotate","version":1,"source_width":960,"source_height":480,"panel_width":480,"panel_height":960,"output":"rgba"}`)); err == nil {
		t.Fatal("wrong output format accepted")
	}
	for _, version := range []int{2, 3, 4} {
		value := []byte(fmt.Sprintf(`{"adapter":"tater-gpu-rotate","version":1,"source_width":960,"source_height":480,"panel_width":480,"panel_height":960,"output":"bgra","scene_version":%d}`, version))
		if err := validateCapabilities(value); err != nil {
			t.Fatalf("scene version %d rejected: %v", version, err)
		}
	}
	if err := validateCapabilities([]byte(`{"adapter":"tater-gpu-rotate","version":1,"source_width":960,"source_height":480,"panel_width":480,"panel_height":960,"output":"bgra","scene_version":5}`)); err == nil {
		t.Fatal("unknown GPU scene protocol accepted")
	}
}

func TestCronosRuntimeCannotCrossTargetManifestOrHelperGates(t *testing.T) {
	manifest := runtimeManifest{Schema: 2, RuntimeID: cronosRuntimeID, TreeSHA256: expectedTreeSHA256,
		FileCount: expectedRuntimeFiles, RuntimeBytes: expectedRuntimeBytes}
	value, _ := json.Marshal(manifest)
	if err := validateManifestForTarget(value, "cronos"); err != nil {
		t.Fatal(err)
	}
	if validateManifest(value) == nil || validateManifestForTarget(value, "crown") == nil {
		t.Fatal("Cronos runtime accepted by another target")
	}
	manifest.Schema, manifest.HelperSHA256 = 1, expectedTextHelperSHA256
	value, _ = json.Marshal(manifest)
	if validateManifestForTarget(value, "cronos") == nil {
		t.Fatal("Checkers helper accepted in a Cronos manifest")
	}
	manifest.HelperSHA256 = cronosHelperSHA256
	value, _ = json.Marshal(manifest)
	if err := validateManifestForTarget(value, "cronos"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TATER_GPU_ENABLE", "1")
	for _, target := range []string{"checkers", "cronos", "crown", "athena", "rook", "", "cronos-extra"} {
		t.Setenv("TATER_FIRMWARE_TARGET", target)
		configuration, enabled := configFromEnvironment()
		want := target == "checkers" || target == "cronos" || target == "crown" || target == "athena" || target == "rook"
		if enabled != want || (enabled && configuration.target != target) {
			t.Fatalf("wrong GPU target gate: target=%q enabled=%t", target, enabled)
		}
	}
}

func TestStalledSceneHelperTimesOutAndIsReaped(t *testing.T) {
	inputReader, inputWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer inputReader.Close()
	outputReader, outputWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer outputWriter.Close()
	command := exec.Command("/bin/sh", "-c", "sleep 30")
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	compositor := &Compositor{geometry: defaultGeometry(), command: command, input: inputWriter, output: outputReader, stderr: &tailBuffer{}, scenes: true}
	defer compositor.Close()
	started := time.Now()
	if err := compositor.CompositeScene(nil, make([]byte, PanelBytes), Effects{Blend: 1}); err == nil {
		t.Fatal("stalled helper returned a frame")
	}
	if time.Since(started) > 3*time.Second || !compositor.closed || command.ProcessState == nil {
		t.Fatal("stalled helper was not closed/reaped promptly for CPU fallback")
	}
}
