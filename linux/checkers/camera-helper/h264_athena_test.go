package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAthenaEncoderManifestRejectsCrossTargetAndChangedIdentity(t *testing.T) {
	for _, encoder := range []bool{false, true} {
		id, tree, count, size := "athena-mt8183-ap-vdec-1", "efcfe0d53924554af190c7e9f8068f190e1af3620c0346efceed3a81890b23bc", 24, 6042836
		if encoder {
			id, tree, count, size = "athena-mt8183-ap-venc-1", "de1daf2574d4d4d4d3c5ba8605852f782a9f170d35934888290e1d52d9a91db3", 55, 8510652
		}
		m := map[string]any{"schema": 1, "target": "athena", "runtime_id": id, "experimental": true, "tree_sha256": tree, "file_count": count, "runtime_bytes": size}
		value, _ := json.Marshal(m)
		if err := validateAthenaEncoderManifest(value, encoder); err != nil {
			t.Fatal(err)
		}
		if validateAthenaEncoderManifest(value, !encoder) == nil {
			t.Fatal("accepted other engine manifest")
		}
		for _, invalid := range [][]byte{
			append(append([]byte(nil), value...), []byte(` {}`)...),
			bytes.Replace(value, []byte(`"athena"`), []byte(`"cronos"`), 1),
			bytes.Replace(value, []byte(`true`), []byte(`false`), 1),
			bytes.Replace(value, []byte(tree), []byte("wrong"), 1),
			bytes.Replace(value, []byte(`"schema":1`), []byte(`"schema":2`), 1),
			append([]byte(`{"unknown":true,`), value[1:]...),
		} {
			if validateAthenaEncoderManifest(invalid, encoder) == nil {
				t.Fatal("accepted changed manifest")
			}
		}
	}
	if len(athenaEncoderFiles) != 79 {
		t.Fatal("dependency closure incomplete")
	}
}

func TestAthenaEncoderRequiresSeparateOptInAndOrdinaryFiles(t *testing.T) {
	t.Setenv("TATER_ATHENA_AP_ENABLE", "")
	if _, _, _, err := athenaEncoderPaths(); err == nil {
		t.Fatal("enabled without AP opt-in")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("wrong binary"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, link, dir, filepath.Join(dir, "missing")} {
		if checkedAthenaEncoderFile(path, athenaVencHelperHash) == nil {
			t.Fatal("accepted unqualified dependency")
		}
	}
}

func TestAthenaEncoderUsesOnlyQualifiedNV12Contract(t *testing.T) {
	for _, mode := range []string{"", "balanced"} {
		p, ok := encoderCaptureProfile("athena", mode)
		if !ok || p.width != 640 || p.height != 480 {
			t.Fatal("untested Athena geometry")
		}
		want := []string{"--stream-nv12-h264-v1", "component", "640", "480", "30"}
		if !reflect.DeepEqual(encoderProcessArguments(p, "component"), want) {
			t.Fatal("incorrect AP invocation")
		}
	}
	for _, mode := range []string{"full", "1280x960", "anything"} {
		if _, ok := encoderCaptureProfile("athena", mode); ok {
			t.Fatal("untested capture profile")
		}
	}
	p, _ := encoderCaptureProfile("cronos", "balanced")
	if got := encoderProcessArguments(p, "component"); got[0] != "--stream-bayer10" || got[3] != "cronos" {
		t.Fatal("changed legacy encoder contract")
	}
}
