//go:build linux

package videostream

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

type blockingAudioWriter struct {
	started chan struct{}
	release chan struct{}
}

func (writer *blockingAudioWriter) Write(value []byte) (int, error) {
	select {
	case <-writer.started:
	default:
		close(writer.started)
	}
	<-writer.release
	return len(value), nil
}

func TestHardwareDecoderRequiresExplicitOptIn(t *testing.T) {
	t.Setenv("TATER_VDEC_ENABLE", "")
	t.Setenv("TATER_FIRMWARE_TARGET", "checkers")
	if _, enabled := hardwareDecoderConfigFromEnvironment(); enabled {
		t.Fatal("hardware decoder enabled without an explicit opt-in")
	}
	t.Setenv("TATER_VDEC_ENABLE", "1")
	t.Setenv("TATER_VDEC_HELPER", "/test/tater-vdec")
	t.Setenv("TATER_VDEC_COMPONENT", "/test/libMtkOmxVdecEx.so")
	t.Setenv("TATER_VDEC_GRALLOC", "/test/gralloc.mt8163.mali.so")
	t.Setenv("TATER_VDEC_LIBRARY_PATH", "/test/lib")
	t.Setenv("TATER_VDEC_RUNTIME_MANIFEST", "/test/runtime.json")
	config, enabled := hardwareDecoderConfigFromEnvironment()
	if !enabled || config.helper != "/test/tater-vdec" || config.component != "/test/libMtkOmxVdecEx.so" || config.gralloc != "/test/gralloc.mt8163.mali.so" || config.libraryPath != "/test/lib" || config.manifest != "/test/runtime.json" {
		t.Fatalf("hardware decoder config = %#v, enabled=%t", config, enabled)
	}
}

func TestHardwareDecoderRejectsOtherTargets(t *testing.T) {
	t.Setenv("TATER_VDEC_ENABLE", "1")
	for _, target := range []string{"", "crown-extra", "athena", "rook-extra"} {
		t.Setenv("TATER_FIRMWARE_TARGET", target)
		if _, enabled := hardwareDecoderConfigFromEnvironment(); enabled {
			t.Fatalf("hardware decoder enabled for target %q", target)
		}
	}
}

func TestCronosDecoderHasSeparateRuntimeAndLinearABI(t *testing.T) {
	t.Setenv("TATER_VDEC_ENABLE", "1")
	t.Setenv("TATER_FIRMWARE_TARGET", "cronos")
	t.Setenv("TATER_VDEC_LIBRARY_PATH", "")
	config, enabled := hardwareDecoderConfigFromEnvironment()
	if !enabled || config.target != "cronos" || config.libraryPath != "/data/tater-vdec/lib" {
		t.Fatal("Cronos runtime was not isolated", config)
	}
	if !strings.Contains(strings.Join(config.environment(), "\n"), "LD_PRELOAD=/data/tater-vdec/lib/libbionic28compat.so") {
		t.Fatal("missing Cronos Bionic compatibility preflight")
	}
	manifest := vdecRuntimeManifest{Schema: 2, RuntimeID: "cronos-mt8163-vdec-stock-1",
		TreeSHA256: cronosVDECTreeSHA256, FileCount: 66, RuntimeBytes: 41_926_684}
	value, _ := json.Marshal(manifest)
	if err := validateVDECRuntimeManifestForTarget(value, "cronos"); err != nil {
		t.Fatal(err)
	}
	if validateVDECRuntimeManifest(value) == nil {
		t.Fatal("Cronos runtime accepted as Checkers")
	}
	for _, field := range []string{"identity", "count", "bytes", "helper"} {
		bad := manifest
		switch field {
		case "identity":
			bad.RuntimeID = expectedVDECRuntimeID
		case "count":
			bad.FileCount--
		case "bytes":
			bad.RuntimeBytes--
		case "helper":
			bad.HelperSHA256 = cronosVDECHelperSHA256
		}
		value, _ := json.Marshal(bad)
		if validateVDECRuntimeManifestForTarget(value, "cronos") == nil {
			t.Fatal("unqualified Cronos manifest accepted", field)
		}
	}
}

func TestHardwareDecoderRuntimeManifestIsExact(t *testing.T) {
	valid := []byte(`{"file_count":76,"helper_sha256":"f8e1ffb6051f4476771db8392e9a2f511a54461161b35f37f7ee647f293eb805","runtime_bytes":12839369,"runtime_id":"checkers-mt8163-vdec-stock-1","schema":1,"tree_sha256":"490a3c771d1c6f979828e60b93c363ec42403ca9bd3c98b12f8c5cd0761f2cc4"}`)
	if err := validateVDECRuntimeManifest(valid); err != nil {
		t.Fatal(err)
	}
	if err := validateVDECRuntimeManifest(bytes.Replace(valid, []byte(expectedVDECHelperSHA256), []byte(neonVDECHelperSHA256), 1)); err != nil {
		t.Fatalf("reviewed NEON helper manifest rejected: %v", err)
	}
	for _, invalid := range [][]byte{
		bytes.Replace(valid, []byte(expectedVDECTreeSHA256), []byte(strings.Repeat("0", 64)), 1),
		bytes.Replace(valid, []byte(expectedVDECHelperSHA256), []byte(strings.Repeat("0", 64)), 1),
		append(append([]byte(nil), valid...), []byte(` {}`)...),
		[]byte(`{"schema":1,"unknown":true}`),
	} {
		if err := validateVDECRuntimeManifest(invalid); err == nil {
			t.Fatalf("invalid runtime manifest accepted: %s", invalid)
		}
	}
}

func TestPrivateVDECLibraryOnlyManifest(t *testing.T) {
	manifest := vdecRuntimeManifest{Schema: 2, RuntimeID: expectedVDECRuntimeID,
		TreeSHA256: expectedVDECTreeSHA256, FileCount: 76, RuntimeBytes: 12_839_369}
	value, _ := json.Marshal(manifest)
	if err := validateVDECRuntimeManifest(value); err != nil {
		t.Fatal(err)
	}
	manifest.HelperSHA256 = neonVDECHelperSHA256
	value, _ = json.Marshal(manifest)
	if validateVDECRuntimeManifest(value) == nil {
		t.Fatal("library-only VDEC manifest accepted a shared helper")
	}
}

func TestBoundedRuntimeFileAndHash(t *testing.T) {
	path := t.TempDir() + "/runtime"
	if err := os.WriteFile(path, []byte("runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := boundedFile(path, 7)
	if err != nil || string(value) != "runtime" {
		t.Fatalf("bounded file = %q, %v", value, err)
	}
	if _, err := boundedFile(path, 6); err == nil {
		t.Fatal("oversized runtime marker was accepted")
	}
	hash, err := fileSHA256(path)
	if err != nil || hash != "d92c6a81b2ff50096bcda80885427d1f59a25b5f483f7055523504925d16ab23" {
		t.Fatalf("runtime hash = %q, %v", hash, err)
	}
}

func TestHardwareDecoderCapabilitiesAreExact(t *testing.T) {
	valid := []byte(`{"adapter":"tater-vdec","version":2,"codec":"h264","stream_versions":[1,2],"stream_v2_output":"TVO2-header-and-timestamped-rgba","max_width":1920,"max_height":1088}`)
	if err := validateVDECCapabilities(valid); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]byte{
		[]byte(`{"adapter":"other","version":2,"codec":"h264","stream_versions":[2],"stream_v2_output":"TVO2-header-and-timestamped-rgba","max_width":1920,"max_height":1088}`),
		[]byte(`{"adapter":"tater-vdec","version":1,"codec":"h264","stream_versions":[1],"max_width":1920,"max_height":1088}`),
		[]byte(`not-json`),
	} {
		if err := validateVDECCapabilities(invalid); err == nil {
			t.Fatalf("invalid capabilities accepted: %s", invalid)
		}
	}
}

func TestHardwareDecoderRequiresPinnedFFmpegFeatures(t *testing.T) {
	muxers := []byte(" E h264            raw H.264 video\n")
	muxers = append(muxers, []byte(" E framecrc        frame CRC testing\n")...)
	filters := []byte("h264_metadata\nh264_mp4toannexb\n")
	if !ffmpegSupportsHardwareH264(muxers, filters) {
		t.Fatal("complete FFmpeg feature set was rejected")
	}
	if ffmpegSupportsHardwareH264(muxers, []byte("h264_mp4toannexb\n")) {
		t.Fatal("incomplete FFmpeg feature set was accepted")
	}
	if ffmpegSupportsHardwareH264([]byte(" E h264 raw H.264 video\n"), filters) {
		t.Fatal("FFmpeg without packet timestamp metadata was accepted")
	}
}

func TestGatedAudioDoesNotLeakBeforeFirstFrame(t *testing.T) {
	var destination bytes.Buffer
	writer := newGatedAudioWriter(&destination)
	if _, err := writer.Write([]byte("before")); err != nil {
		t.Fatal(err)
	}
	if destination.Len() != 0 {
		t.Fatal("startup audio reached the destination before commit")
	}
	if err := writer.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("-after")); err != nil {
		t.Fatal(err)
	}
	if got := destination.String(); got != "before-after" {
		t.Fatalf("committed audio = %q", got)
	}
	limited := newGatedAudioWriter(nil)
	limited.limit = 3
	if _, err := limited.Write([]byte("four")); err == nil {
		t.Fatal("startup audio boundary was not enforced")
	}
}

func TestGatedAudioCommitDoesNotBlockVideoOutput(t *testing.T) {
	destination := &blockingAudioWriter{started: make(chan struct{}), release: make(chan struct{})}
	writer := newGatedAudioWriter(destination)
	_, _ = writer.Write([]byte("buffered-pcm"))
	if err := writer.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-destination.started:
	case <-time.After(time.Second):
		t.Fatal("audio flush did not start")
	}
	close(destination.release)
	if err := writer.Wait(); err != nil {
		t.Fatal(err)
	}
}

func testVDECOutputFrames(width, height, fps int, timestamps []int64, pixels []byte) []byte {
	var output bytes.Buffer
	header := make([]byte, vdecStreamHeaderBytes)
	copy(header, "TVO2")
	binary.LittleEndian.PutUint32(header[4:], vdecStreamHeaderBytes)
	binary.LittleEndian.PutUint32(header[8:], uint32(width))
	binary.LittleEndian.PutUint32(header[12:], uint32(height))
	binary.LittleEndian.PutUint32(header[16:], uint32(len(pixels)))
	copy(header[20:], "RGBA")
	binary.LittleEndian.PutUint32(header[24:], uint32(fps))
	binary.LittleEndian.PutUint32(header[28:], 1)
	output.Write(header)
	record := make([]byte, vdecRecordHeaderBytes)
	for _, timestamp := range timestamps {
		clear(record)
		binary.LittleEndian.PutUint32(record, uint32(len(pixels)))
		binary.LittleEndian.PutUint64(record[8:], uint64(timestamp))
		output.Write(record)
		output.Write(pixels)
	}
	clear(record)
	binary.LittleEndian.PutUint32(record, 0)
	binary.LittleEndian.PutUint32(record[4:], vdecOutputFlagEOS)
	output.Write(record)
	return output.Bytes()
}

func testVDECOutput(width, height, fps int, pixels []byte) []byte {
	return testVDECOutputFrames(width, height, fps, []int64{0}, pixels)
}

func TestHardwareOutputCommitsAudioWithFirstValidatedFrame(t *testing.T) {
	request := MediaRequest{Width: 16, Height: 16, FPS: 30}
	pixels := bytes.Repeat([]byte{1, 2, 3, 255}, request.Width*request.Height)
	var audio bytes.Buffer
	gate := newGatedAudioWriter(&audio)
	_, _ = gate.Write([]byte("pcm"))
	first := 0
	result := readHardwareOutput(bytes.NewReader(testVDECOutput(request.Width, request.Height, request.FPS, pixels)), request, gate, func() { first++ }, func(frame *image.RGBA) error {
		if !bytes.Equal(frame.Pix, pixels) {
			t.Fatal("hardware frame pixels changed")
		}
		return nil
	})
	if err := gate.Wait(); err != nil {
		t.Fatal(err)
	}
	if result.err != nil || result.frames != 1 || result.presented != 1 || first != 1 || audio.String() != "pcm" {
		t.Fatalf("hardware output result=%#v first=%d audio=%q", result, first, audio.String())
	}
}

func TestHardwareOutputDropsFramesBehindDeliveredAudio(t *testing.T) {
	request := MediaRequest{Width: 16, Height: 16, FPS: 30}
	pixels := bytes.Repeat([]byte{1, 2, 3, 255}, request.Width*request.Height)
	clock := &testPlaybackClock{positionUS: 1_000_000, active: true}
	gate := newGatedAudioWriter(clock)
	if _, err := gate.Write(make([]byte, 96_000)); err != nil {
		t.Fatal(err)
	}
	if err := gate.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := gate.Wait(); err != nil {
		t.Fatal(err)
	}
	presented := 0
	result := readHardwareOutput(bytes.NewReader(testVDECOutputFrames(request.Width, request.Height, request.FPS, []int64{0, 100_000}, pixels)), request, gate, nil, func(*image.RGBA) error {
		presented++
		return nil
	})
	if result.err != nil || result.frames != 2 || result.presented != 1 || result.dropped != 1 || presented != 1 {
		t.Fatalf("late-frame result=%#v callbacks=%d", result, presented)
	}
}

type testPlaybackClock struct {
	positionUS int64
	active     bool
}

func (*testPlaybackClock) Write(value []byte) (int, error) { return len(value), nil }
func (clock *testPlaybackClock) PlaybackPositionUS() (int64, bool) {
	return clock.positionUS, clock.active
}

func TestHardwareOutputDoesNotTreatQueuedBytesAsPlayedAudio(t *testing.T) {
	request := MediaRequest{Width: 16, Height: 16, FPS: 30}
	pixels := bytes.Repeat([]byte{1, 2, 3, 255}, request.Width*request.Height)
	gate := newGatedAudioWriter(io.Discard)
	if _, err := gate.Write(make([]byte, 96_000)); err != nil {
		t.Fatal(err)
	}
	presented := 0
	result := readHardwareOutput(bytes.NewReader(testVDECOutputFrames(request.Width, request.Height, request.FPS, []int64{0, 100_000}, pixels)), request, gate, nil, func(*image.RGBA) error {
		presented++
		return nil
	})
	if result.err != nil || result.frames != 2 || result.presented != 2 || result.dropped != 0 || presented != 2 {
		t.Fatalf("queued-audio result=%#v callbacks=%d", result, presented)
	}
}

func TestHardwareOutputRejectsNonMonotonicPresentationTimestamps(t *testing.T) {
	request := MediaRequest{Width: 16, Height: 16, FPS: 30}
	pixels := bytes.Repeat([]byte{0xff}, request.Width*request.Height*4)
	result := readHardwareOutput(bytes.NewReader(testVDECOutputFrames(request.Width, request.Height, request.FPS, []int64{0, 66_666, 33_333}, pixels)), request, newGatedAudioWriter(nil), nil, func(*image.RGBA) error { return nil })
	if result.err == nil || !strings.Contains(result.err.Error(), "timestamps") || result.frames != 3 || result.presented != 2 {
		t.Fatalf("non-monotonic result=%#v", result)
	}
}

func TestHardwareOutputPTSIsPreservedForLiveBacklogMeasurement(t *testing.T) {
	request := MediaRequest{Width: 16, Height: 16, FPS: 30}
	pixels := make([]byte, 16*16*4)
	want := []int64{8000000, 8033333, 8099999}
	var timestamps []int64
	result := readHardwareOutputPTS(bytes.NewReader(testVDECOutputFrames(16, 16, 30, want, pixels)), request, newGatedAudioWriter(io.Discard), nil, func(_ *image.RGBA, pts int64) error {
		timestamps = append(timestamps, pts)
		return nil
	})
	if result.err != nil || len(timestamps) != len(want) {
		t.Fatal(result, timestamps)
	}
	for i, pts := range timestamps {
		if pts != want[i] {
			t.Fatal("timestamp changed", timestamps)
		}
	}
}

func TestHardwareOutputRejectsNegotiatedGeometryBeforeAudio(t *testing.T) {
	request := MediaRequest{Width: 16, Height: 16, FPS: 30}
	pixels := bytes.Repeat([]byte{0}, 32*16*4)
	var audio bytes.Buffer
	gate := newGatedAudioWriter(&audio)
	_, _ = gate.Write([]byte("must-not-leak"))
	result := readHardwareOutput(bytes.NewReader(testVDECOutput(32, 16, 30, pixels)), request, gate, nil, func(*image.RGBA) error { return nil })
	if result.err == nil || result.frames != 0 || audio.Len() != 0 || !strings.Contains(result.err.Error(), "geometry") {
		t.Fatalf("unexpected geometry result=%#v audio=%q", result, audio.String())
	}
}
