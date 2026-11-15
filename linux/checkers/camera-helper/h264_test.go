package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"io"
	"os"
	"strings"
	"testing"
)

func TestEncoderManifestPinsEveryRuntimeIdentity(t *testing.T) {
	valid := []byte(`{"schema":1,"runtime_id":"checkers-mt8163-venc-stock-1","helper_sha256":"` + vencHelperHash + `","component_sha256":"` + vencComponentHash + `","base_tree_sha256":"490a3c771d1c6f979828e60b93c363ec42403ca9bd3c98b12f8c5cd0761f2cc4"}`)
	if err := validateEncoderManifest(valid); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]byte{
		bytes.Replace(valid, []byte(vencHelperHash), []byte(strings.Repeat("0", 64)), 1),
		bytes.Replace(valid, []byte(vencComponentHash), []byte(strings.Repeat("0", 64)), 1),
		bytes.Replace(valid, []byte(`"schema":1`), []byte(`"schema":2`), 1),
		append(append([]byte(nil), valid...), []byte(` {}`)...),
		[]byte(strings.Repeat(" ", 4097)),
	} {
		if validateEncoderManifest(invalid) == nil {
			t.Fatal("accepted unqualified manifest")
		}
	}
	path := t.TempDir() + "/manifest"
	if err := os.WriteFile(path, valid, 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := readEncoderManifest(path); err != nil || !bytes.Equal(got, valid) {
		t.Fatal("failed bounded manifest read", err)
	}
	if err := os.WriteFile(path, make([]byte, 4097), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readEncoderManifest(path); err == nil {
		t.Fatal("read oversized manifest")
	}
	if err := checkedEncoderFile(path, vencHelperHash); err == nil {
		t.Fatal("accepted wrong runtime identity")
	}
}

func TestEncoderLibraryOnlyManifest(t *testing.T) {
	valid := []byte(`{"schema":2,"runtime_id":"checkers-mt8163-venc-stock-1","component_sha256":"` + vencComponentHash + `","base_tree_sha256":"490a3c771d1c6f979828e60b93c363ec42403ca9bd3c98b12f8c5cd0761f2cc4"}`)
	if err := validateEncoderManifest(valid); err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(valid, []byte(`"schema":2`), []byte(`"schema":2,"unknown":true`), 1)
	if validateEncoderManifest(unknown) == nil {
		t.Fatal("accepted unknown private manifest field")
	}
}

func TestCheckersLineageEncoderHasIsolatedRuntime(t *testing.T) {
	t.Setenv("TATER_CHECKERS_LINEAGE_ENABLE", "1")
	profile, ok := encoderProfileForTarget("checkers")
	if !ok || profile.id != "checkers-mt8163-venc-lineage-1" || profile.helper != checkersLineageVencHash || profile.component != cronosVencComponentHash || profile.tree != cronosCodecTree {
		t.Fatal("wrong retained-Lineage encoder profile", profile)
	}
	valid := []byte(`{"schema":2,"runtime_id":"` + profile.id + `","component_sha256":"` + profile.component + `","base_tree_sha256":"` + profile.tree + `"}`)
	if err := validateEncoderManifestForTarget(valid, "checkers"); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"cronos", "crown", "rook", "athena"} {
		if validateEncoderManifestForTarget(valid, target) == nil {
			t.Fatal("accepted Checkers runtime for another device", target)
		}
	}
	if validateEncoderManifestForTarget(bytes.Replace(valid, []byte(`"schema":2`), []byte(`"schema":1`), 1), "checkers") == nil {
		t.Fatal("accepted old runtime metadata")
	}
	t.Setenv("TATER_CHECKERS_LINEAGE_ENABLE", "")
	if validateEncoderManifestForTarget(valid, "checkers") == nil {
		t.Fatal("accepted new runtime in the old FireOS profile")
	}
}

func TestLegacyRuntimeManifestDoesNotPinTheApplicationHelper(t *testing.T) {
	for target, legacy := range map[string]string{
		"checkers": "5431a4fb2b32378313307b4550e71842df23fb155acf04e3a816662312df2e7d",
		"cronos":   "920bf4a08373566478c8913e5ab6e3cf161cce835137ae513164bada2fc52709",
	} {
		profile, _ := encoderProfileForTarget(target)
		manifest := []byte(`{"schema":1,"runtime_id":"` + profile.id + `","helper_sha256":"` + legacy + `","component_sha256":"` + profile.component + `","base_tree_sha256":"` + profile.tree + `"}`)
		if err := validateEncoderManifestForTarget(manifest, target); err != nil {
			t.Errorf("%s: known library runtime rejected after application helper upgrade: %v", target, err)
		}
		for _, invalid := range [][]byte{
			bytes.Replace(manifest, []byte(legacy), []byte(strings.Repeat("0", 64)), 1),
			bytes.Replace(manifest, []byte(profile.component), []byte(strings.Repeat("0", 64)), 1),
			bytes.Replace(manifest, []byte(profile.tree), []byte(strings.Repeat("0", 64)), 1),
			bytes.Replace(manifest, []byte(profile.id), []byte("unqualified-runtime"), 1),
		} {
			if validateEncoderManifestForTarget(invalid, target) == nil {
				t.Fatal("legacy metadata bypassed a runtime identity check", target)
			}
		}
		// Legacy metadata must not allow an arbitrary executable to pass the
		// separate, current-application helper hash check in encoderPaths.
		path := t.TempDir() + "/old-helper"
		if err := os.WriteFile(path, []byte("not the pinned application helper"), 0500); err != nil {
			t.Fatal(err)
		}
		if checkedEncoderFile(path, profile.helper) == nil {
			t.Fatal("manifest compatibility bypassed executable qualification")
		}
	}
}

func TestBalancedEncoderProfilesAreTargetOwnedAndExact(t *testing.T) {
	for target, size := range map[string][2]int{"checkers": {768, 432}, "cronos": {640, 480}, "crown": {768, 432}, "rook": {640, 480}} {
		p, ok := encoderCaptureProfile(target, "balanced")
		if !ok || p.width != size[0] || p.height != size[1] {
			t.Fatal(target, p)
		}
		h := make([]byte, 32)
		copy(h, "TVE1")
		copy(h[16:], "H264")
		for offset, value := range map[int]uint32{4: 32, 8: uint32(p.width), 12: uint32(p.height), 20: 30, 24: 1, 28: vencMaxPacket} {
			binary.LittleEndian.PutUint32(h[offset:], value)
		}
		if validateEncoderHeaderForProfile(h, p, ok) != nil || (target != "rook" && validateEncoderHeaderForTarget(h, target) == nil) {
			t.Fatal("balanced header confused with full-resolution profile")
		}
	}
	for _, target := range []string{"rook-extra", "crown-extra", ""} {
		if _, ok := encoderCaptureProfile(target, "balanced"); ok {
			t.Fatal("unqualified target", target)
		}
	}
	if _, ok := encoderCaptureProfile("cronos", "arbitrary"); ok {
		t.Fatal("unqualified mode")
	}
}

func TestCronosEncoderRejectsCheckersIdentityAndGeometry(t *testing.T) {
	valid := []byte(`{"schema":2,"runtime_id":"cronos-mt8163-venc-stock-1","component_sha256":"` + cronosVencComponentHash + `","base_tree_sha256":"` + cronosCodecTree + `"}`)
	if err := validateEncoderManifestForTarget(valid, "cronos"); err != nil {
		t.Fatal(err)
	}
	if validateEncoderManifest(valid) == nil {
		t.Fatal("Cronos component accepted for Checkers")
	}
	for _, target := range []string{"", "crown-extra", "rook-extra"} {
		if _, ok := encoderProfileForTarget(target); ok {
			t.Fatal("unqualified encoder target", target)
		}
	}
	h := make([]byte, 32)
	copy(h, "TVE1")
	copy(h[16:], "H264")
	for offset, value := range map[int]uint32{4: 32, 8: 1280, 12: 960, 20: 30, 24: 1, 28: vencMaxPacket} {
		binary.LittleEndian.PutUint32(h[offset:], value)
	}
	if err := validateEncoderHeaderForTarget(h, "cronos"); err != nil {
		t.Fatal(err)
	}
	if validateEncoderHeader(h) == nil {
		t.Fatal("4:3 camera header accepted for Checkers")
	}
}

func TestEncoderHeaderRequiresExactQualifiedGeometry(t *testing.T) {
	h := make([]byte, 32)
	copy(h, "TVE1")
	binary.LittleEndian.PutUint32(h[4:], 32)
	binary.LittleEndian.PutUint32(h[8:], 1280)
	binary.LittleEndian.PutUint32(h[12:], 720)
	copy(h[16:], "H264")
	binary.LittleEndian.PutUint32(h[20:], 30)
	binary.LittleEndian.PutUint32(h[24:], 1)
	binary.LittleEndian.PutUint32(h[28:], vencMaxPacket)
	if err := validateEncoderHeader(h); err != nil {
		t.Fatal(err)
	}
	for _, offset := range []int{0, 4, 8, 12, 16, 20, 24, 28} {
		invalid := append([]byte(nil), h...)
		invalid[offset] ^= 1
		if validateEncoderHeader(invalid) == nil {
			t.Fatalf("accepted corrupt field %d", offset)
		}
	}
	if validateEncoderHeader(h[:31]) == nil {
		t.Fatal("accepted truncated header")
	}
}

func TestEncoderPacketsAreBoundedAndPreserveTimestamp(t *testing.T) {
	var h [16]byte
	binary.LittleEndian.PutUint32(h[:], 4)
	binary.LittleEndian.PutUint32(h[4:], 4)
	binary.LittleEndian.PutUint64(h[8:], 123456)
	data := append(h[:], 0, 0, 1, 0x65)
	got, payload, err := readEncoderPacket(bytes.NewReader(data))
	if err != nil || got != h || !bytes.Equal(payload, data[16:]) {
		t.Fatalf("packet: %v", err)
	}
	if _, _, err := readEncoderPacket(bytes.NewReader(data[:18])); err != io.ErrUnexpectedEOF {
		t.Fatalf("short payload: %v", err)
	}
	for _, values := range [][2]uint32{{vencMaxPacket + 1, 0}, {0, 0}, {4, 8}} {
		binary.LittleEndian.PutUint32(h[:], values[0])
		binary.LittleEndian.PutUint32(h[4:], values[1])
		if _, _, err := readEncoderPacket(bytes.NewReader(h[:])); err == nil {
			t.Fatalf("accepted invalid packet %v", values)
		}
	}
	binary.LittleEndian.PutUint32(h[:], 0)
	binary.LittleEndian.PutUint32(h[4:], 1)
	if _, _, err := readEncoderPacket(bytes.NewReader(h[:])); err != nil {
		t.Fatalf("EOS: %v", err)
	}
}

func TestEncoderStartupRequiresAPictureBeforeHTTPCommit(t *testing.T) {
	packet := func(flags uint32, payload []byte) []byte {
		var header [16]byte
		binary.LittleEndian.PutUint32(header[:], uint32(len(payload)))
		binary.LittleEndian.PutUint32(header[4:], flags)
		return append(header[:], payload...)
	}
	config := packet(2, []byte{0, 0, 1, 0x67})
	picture := packet(4, []byte{0, 0, 1, 0x65})
	if packets, err := readEncoderStartup(bytes.NewReader(append(append([]byte(nil), config...), picture...))); err != nil || len(packets) != 2 {
		t.Fatalf("valid startup: %d packets, %v", len(packets), err)
	}
	for _, invalid := range [][]byte{config, packet(1, nil), bytes.Repeat(config, 128), packet(2, make([]byte, 64*1024+1))} {
		if _, err := readEncoderStartup(bytes.NewReader(invalid)); err == nil {
			t.Fatal("committed a stream without a camera picture")
		}
	}
}

type countedSnapshotFrame struct{ full, live, low, smooth int }

func (f *countedSnapshotFrame) Full() *image.RGBA {
	f.full++
	return image.NewRGBA(image.Rect(0, 0, 1280, 720))
}
func (f *countedSnapshotFrame) Image() *image.RGBA {
	f.live++
	return image.NewRGBA(image.Rect(0, 0, 640, 360))
}
func (f *countedSnapshotFrame) TaterEchoVideo(*image.RGBA) *image.RGBA {
	f.low++
	return image.NewRGBA(image.Rect(0, 0, 320, 180))
}
func (f *countedSnapshotFrame) TaterSmoothVideo(reuse *image.RGBA) *image.RGBA {
	f.smooth++
	if reuse != nil && reuse.Bounds() == image.Rect(0, 0, 480, 270) {
		return reuse
	}
	return image.NewRGBA(image.Rect(0, 0, 480, 270))
}

func TestSnapshotDoesNotDevelopUnusedFullResolutionImage(t *testing.T) {
	for _, profile := range []string{"video", "echo-low", "echo-balanced"} {
		frame := &countedSnapshotFrame{}
		snapshotImage(frame, profile)
		if frame.full != 0 {
			t.Fatalf("%s developed unused full image", profile)
		}
	}
	frame := &countedSnapshotFrame{}
	img, quality := snapshotImage(frame, "")
	if frame.full != 1 || frame.live != 0 || img.Rect.Size() != image.Pt(1280, 720) || quality != snapshotJPEGQuality {
		t.Fatal("default still snapshot lost native resolution")
	}
}
