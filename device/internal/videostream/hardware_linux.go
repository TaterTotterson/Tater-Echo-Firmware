//go:build linux

package videostream

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/hardwarebundle"
)

const (
	defaultVDECComponent     = "/data/tater-vdec/lib/libMtkOmxVdecEx.so"
	defaultVDECLibraryPath   = "/data/tater-vdec/lib:/system/lib:/vendor/lib"
	defaultVDECManifest      = "/data/tater-vdec/runtime.json"
	hardwareAudioBufferBytes = 1024 * 1024
	hardwareFrameLagUS       = 250_000
	vdecManifestMaxBytes     = 4096

	expectedVDECRuntimeID     = "checkers-mt8163-vdec-stock-1"
	expectedVDECTreeSHA256    = "490a3c771d1c6f979828e60b93c363ec42403ca9bd3c98b12f8c5cd0761f2cc4"
	expectedVDECHelperSHA256  = "f8e1ffb6051f4476771db8392e9a2f511a54461161b35f37f7ee647f293eb805"
	neonVDECHelperSHA256      = "56091ad1146815d30be313be9e08cb671640033756de14f294f04ab04ef6ce91"
	expectedVDECComponentHash = "1c3345fd1725df98d0da548c6eaadde172d011a2d09ff79de233920cb5cbc441"
	expectedVDECGrallocHash   = "9f1bc1cae72091973cae340654c47b78e22834d9ba3ca63d37a8ab08e2840a29"
	cronosVDECHelperSHA256    = "b6f9cf50e7a05aec9cf3a0cfaaee9277d0e16d8c81eda61662c6dc0331577f2d"
	cronosVDECTreeSHA256      = "8564d383faba23c32545037f62a7ab9ddddf227a61790470dbc883e6eb871f0a"
	cronosVDECComponentHash   = "812cd2a56b50e994fee60cdb92d3b0c82ff339d25b55c55d31ac9233f7b6ee89"
	crownVDECHelperSHA256     = "abb64de71075441c32f43a7c8336bacc6364a34958f50028df5884ca2cc84d30"
	rookVDECHelperSHA256      = "c103fcf7ed2e492ead281ed3c86847f502417abbbc2b2e2387b8001e20046cbd"
)

type hardwareDecoderConfig struct {
	helper, component, gralloc, libraryPath, manifest string
	target                                            string
}

func environmentValue(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func hardwareDecoderConfigFromEnvironment() (hardwareDecoderConfig, bool) {
	target := strings.ToLower(strings.TrimSpace(os.Getenv("TATER_FIRMWARE_TARGET")))
	if target == "athena" && os.Getenv("TATER_VDEC_ENABLE") == "1" && os.Getenv("TATER_ATHENA_AP_ENABLE") == "1" {
		return athenaAPDecoderConfig(), true
	}
	if os.Getenv("TATER_VDEC_ENABLE") != "1" || (target != "checkers" && target != "cronos" && target != "crown" && target != "rook") {
		return hardwareDecoderConfig{}, false
	}
	config := hardwareDecoderConfig{
		helper:      environmentValue("TATER_VDEC_HELPER", hardwarebundle.Helper("tater-hw-vdec")),
		component:   environmentValue("TATER_VDEC_COMPONENT", defaultVDECComponent),
		gralloc:     environmentValue("TATER_VDEC_GRALLOC", "/data/tater-vdec/lib/gralloc.mt8163.mali.so"),
		libraryPath: environmentValue("TATER_VDEC_LIBRARY_PATH", defaultVDECLibraryPath),
		manifest:    environmentValue("TATER_VDEC_RUNTIME_MANIFEST", defaultVDECManifest),
		target:      target,
	}
	if target == "cronos" || target == "crown" || target == "rook" {
		// No Android 5 GraphicBuffer ABI and no unqualified /vendor fallback.
		config.libraryPath = environmentValue("TATER_VDEC_LIBRARY_PATH", "/data/tater-vdec/lib")
	}
	return config, true
}

func (config hardwareDecoderConfig) environment() []string {
	environment := []string{
		"PATH=/system/bin:/usr/bin:/bin",
		"HOME=/",
		"ANDROID_ROOT=/system",
		"ANDROID_DATA=/data",
		"LD_LIBRARY_PATH=" + config.libraryPath,
		"TATER_VDEC_GRALLOC=" + config.gralloc,
	}
	if config.target == "cronos" || config.target == "crown" || config.target == "rook" {
		environment = append(environment, "LD_PRELOAD=/data/tater-vdec/lib/libbionic28compat.so")
	}
	return environment
}

func deviceNodeExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode()&os.ModeDevice != 0
}

type vdecCapabilities struct {
	Adapter        string `json:"adapter"`
	Version        int    `json:"version"`
	Codec          string `json:"codec"`
	StreamVersions []int  `json:"stream_versions"`
	StreamV2Output string `json:"stream_v2_output"`
	MaxWidth       int    `json:"max_width"`
	MaxHeight      int    `json:"max_height"`
}

type vdecRuntimeManifest struct {
	Schema       int    `json:"schema"`
	RuntimeID    string `json:"runtime_id"`
	TreeSHA256   string `json:"tree_sha256"`
	FileCount    int    `json:"file_count"`
	RuntimeBytes int64  `json:"runtime_bytes"`
	HelperSHA256 string `json:"helper_sha256"`
}

func allowlistedVDECHelper(hash string) bool {
	return hash == expectedVDECHelperSHA256 || hash == neonVDECHelperSHA256
}

func validateVDECRuntimeManifest(value []byte) error {
	return validateVDECRuntimeManifestForTarget(value, "checkers")
}

func validateVDECRuntimeManifestForTarget(value []byte, target string) error {
	var manifest vdecRuntimeManifest
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return errors.New("hardware decoder runtime manifest is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("hardware decoder runtime manifest has trailing data")
	}
	validHelper := (manifest.Schema == 1 && allowlistedVDECHelper(manifest.HelperSHA256)) ||
		(manifest.Schema == 2 && manifest.HelperSHA256 == "")
	if target == "cronos" || target == "crown" || target == "rook" {
		if manifest.Schema != 2 || manifest.HelperSHA256 != "" ||
			manifest.RuntimeID != target+"-mt8163-vdec-stock-1" || manifest.TreeSHA256 != cronosVDECTreeSHA256 ||
			manifest.FileCount != 66 || manifest.RuntimeBytes != 41_926_684 {
			return errors.New("Cronos decoder runtime identity is not allowlisted")
		}
		return nil
	}
	if target != "checkers" || !validHelper || manifest.RuntimeID != expectedVDECRuntimeID ||
		manifest.TreeSHA256 != expectedVDECTreeSHA256 || manifest.FileCount != 76 ||
		manifest.RuntimeBytes != 12_839_369 {
		return errors.New("hardware decoder runtime identity is not allowlisted")
	}
	return nil
}

func boundedFile(path string, maximum int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	value, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(value)) > maximum {
		return nil, errors.New("file exceeds its boundary")
	}
	return value, nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", digest.Sum(nil)), nil
}

func validateVDECRuntime(config hardwareDecoderConfig) error {
	if config.target == "athena" {
		return validateAthenaVDECRuntime(config)
	}
	manifest, err := boundedFile(config.manifest, vdecManifestMaxBytes)
	if err != nil {
		return fmt.Errorf("hardware decoder runtime manifest is unavailable: %w", err)
	}
	target := config.target
	if target == "" {
		target = "checkers"
	}
	if err := validateVDECRuntimeManifestForTarget(manifest, target); err != nil {
		return err
	}
	checks := map[string]string{
		config.helper: neonVDECHelperSHA256, config.component: expectedVDECComponentHash, config.gralloc: expectedVDECGrallocHash,
	}
	if target == "cronos" || target == "crown" || target == "rook" {
		checks = map[string]string{
			config.helper: cronosVDECHelperSHA256, config.component: cronosVDECComponentHash,
			"/data/tater-vdec/linker":                   "b9c6e172a16c5e1337cca905bc3457392e877ffc3b040e250929a5ca56afdaaa",
			"/data/tater-vdec/lib/libbionic28compat.so": "e98b5822d5e3e2ba5cc88da3a3f0aca0b9309af7f59b35c2d8ea42fdaec4f216",
			"/data/tater-vdec/lib/libion_ulit.so":       "6790909777094d628fa4337c5a4928d1bf3559cd2ce4ca6200d5507611d9f713",
			"/data/tater-vdec/lib/libvcodecdrv.so":      "6a9b8be27634d37a451a05cb359797289254d8250573973b8fc064f4ff07989e",
			"/data/tater-vdec/lib/libvcodec_utility.so": "7f46d588d62a552ec00954d0ca23662390071150a6cef7c04f814ebfd6548313",
		}
		if target == "crown" {
			checks[config.helper] = crownVDECHelperSHA256
		} else if target == "rook" {
			checks[config.helper] = rookVDECHelperSHA256
		}
	}
	for path, expected := range checks {
		actual, hashErr := fileSHA256(path)
		if hashErr != nil || actual != expected {
			return fmt.Errorf("hardware decoder runtime file is not allowlisted: %s", path)
		}
	}
	return nil
}

func validateVDECCapabilities(value []byte) error {
	var capabilities vdecCapabilities
	if len(value) > 16*1024 || json.Unmarshal(value, &capabilities) != nil {
		return errors.New("hardware decoder returned invalid capabilities")
	}
	hasV2 := false
	for _, version := range capabilities.StreamVersions {
		hasV2 = hasV2 || version == vdecProtocolVersion
	}
	if capabilities.Adapter != "tater-vdec" || capabilities.Version < vdecProtocolVersion ||
		capabilities.Codec != "h264" || !hasV2 ||
		capabilities.StreamV2Output != "TVO2-header-and-timestamped-rgba" ||
		capabilities.MaxWidth != vdecMaximumWidth || capabilities.MaxHeight != vdecMaximumHeight {
		return errors.New("hardware decoder does not satisfy the protocol-v2 contract")
	}
	return nil
}

func ffmpegSupportsHardwareH264(muxers, bitstreamFilters []byte) bool {
	muxerText := string(muxers)
	filterText := "\n" + string(bitstreamFilters) + "\n"
	return strings.Contains(muxerText, " h264 ") &&
		strings.Contains(muxerText, " framecrc ") &&
		strings.Contains(filterText, "\nh264_mp4toannexb\n") &&
		strings.Contains(filterText, "\nh264_metadata\n")
}

func boundedCommandOutput(ctx context.Context, path string, environment []string, credential *syscall.Credential, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, path, arguments...)
	command.Dir = "/"
	command.Env = environment
	if credential != nil {
		command.SysProcAttr = &syscall.SysProcAttr{Credential: credential, Pdeathsig: syscall.SIGKILL}
	}
	output := &boundedBuffer{remaining: 64 * 1024}
	command.Stdout = output
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// boundedBuffer rejects unexpected unbounded diagnostic output without first
// retaining the bytes it is supposed to reject.
type boundedBuffer struct {
	buffer    bytes.Buffer
	remaining int
}

func (writer *boundedBuffer) Write(value []byte) (int, error) {
	if len(value) > writer.remaining {
		return 0, errors.New("capability output exceeds its boundary")
	}
	writer.remaining -= len(value)
	return writer.buffer.Write(value)
}

func (writer *boundedBuffer) Bytes() []byte { return writer.buffer.Bytes() }

func preflightHardwareDecoder(ctx context.Context, config hardwareDecoderConfig, decoder string) error {
	if config.target == "athena" {
		return errors.New("Athena AP decoder is live-only; general media remains on the qualified software path")
	}
	if !fileExists(config.helper) || !fileExists(config.component) {
		return errors.New("hardware decoder runtime is not installed")
	}
	if err := validateVDECRuntime(config); err != nil {
		return err
	}
	if !deviceNodeExists("/dev/Vcodec") || !deviceNodeExists("/dev/ion") {
		return errors.New("hardware decoder device nodes are unavailable")
	}
	preflightContext, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	capabilities, err := boundedCommandOutput(preflightContext, config.helper, config.environment(), nil, "--capabilities")
	if err != nil {
		return fmt.Errorf("hardware decoder capability check failed: %w", err)
	}
	if err := validateVDECCapabilities(capabilities); err != nil {
		return err
	}
	credential, err := decoderCredential()
	if err != nil {
		return err
	}
	decoderEnvironment := []string{"PATH=/usr/bin:/bin", "HOME=/"}
	muxers, err := boundedCommandOutput(preflightContext, decoder, decoderEnvironment, credential, "-hide_banner", "-muxers")
	if err != nil {
		return fmt.Errorf("video decoder muxer check failed: %w", err)
	}
	filters, err := boundedCommandOutput(preflightContext, decoder, decoderEnvironment, credential, "-hide_banner", "-bsfs")
	if err != nil || !ffmpegSupportsHardwareH264(muxers, filters) {
		return errors.New("video decoder lacks the bounded H.264 copy features")
	}
	return nil
}

type gatedAudioWriter struct {
	mu          sync.Mutex
	destination io.Writer
	buffer      bytes.Buffer
	committed   bool
	limit       int
	flushDone   chan struct{}
	flushErr    error
}

// playbackClock is intentionally optional. A generic io.Writer only proves
// that PCM was accepted by the next queue; it does not prove that the speaker
// has played it. Screen playback wraps the audio writer with the clock
// reported by the daemon's ALSA consumer. Probes and other callers without a
// real playback clock simply skip late-frame dropping.
type playbackClock interface {
	PlaybackPositionUS() (int64, bool)
}

func newGatedAudioWriter(destination io.Writer) *gatedAudioWriter {
	if destination == nil {
		destination = io.Discard
	}
	return &gatedAudioWriter{destination: destination, limit: hardwareAudioBufferBytes}
}

func (writer *gatedAudioWriter) playbackPTSUS() (int64, bool) {
	clock, ok := writer.destination.(playbackClock)
	if !ok {
		return 0, false
	}
	return clock.PlaybackPositionUS()
}

func (writer *gatedAudioWriter) Write(value []byte) (int, error) {
	writer.mu.Lock()
	if !writer.committed {
		defer writer.mu.Unlock()
		if len(value) > writer.limit-writer.buffer.Len() {
			return 0, errors.New("hardware decoder startup audio exceeds its boundary")
		}
		return writer.buffer.Write(value)
	}
	done, destination := writer.flushDone, writer.destination
	writer.mu.Unlock()
	<-done
	writer.mu.Lock()
	flushErr := writer.flushErr
	writer.mu.Unlock()
	if flushErr != nil {
		return 0, flushErr
	}
	return destination.Write(value)
}

func (writer *gatedAudioWriter) Commit() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.committed {
		return nil
	}
	writer.committed = true
	writer.flushDone = make(chan struct{})
	if writer.buffer.Len() == 0 {
		close(writer.flushDone)
		return nil
	}
	prefix := append([]byte(nil), writer.buffer.Bytes()...)
	writer.buffer.Reset()
	go func() {
		err := writeAll(writer.destination, prefix)
		writer.mu.Lock()
		writer.flushErr = err
		close(writer.flushDone)
		writer.mu.Unlock()
	}()
	return nil
}

func (writer *gatedAudioWriter) Wait() error {
	writer.mu.Lock()
	if !writer.committed {
		writer.mu.Unlock()
		return nil
	}
	done := writer.flushDone
	writer.mu.Unlock()
	<-done
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.flushErr
}

type hardwareHelper struct {
	command *exec.Cmd
	input   io.WriteCloser
	output  io.ReadCloser
	tail    *lineTail
}

func startHardwareHelper(ctx context.Context, config hardwareDecoderConfig, request MediaRequest) (*hardwareHelper, error) {
	arguments := []string{
		"--stream-h264-rgba-v2", config.component,
		strconv.Itoa(request.Width), strconv.Itoa(request.Height), strconv.Itoa(request.FPS),
	}
	command := exec.CommandContext(ctx, config.helper, arguments...)
	command.Dir = "/"
	command.Env = config.environment()
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	command.WaitDelay = 2 * time.Second
	input, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := command.StdoutPipe()
	if err != nil {
		_ = input.Close()
		return nil, err
	}
	// The helper receives only framed H.264 bytes and fixed local paths, never
	// the authenticated media URL, so its bounded diagnostic text is safe to
	// retain. FFmpeg stderr remains opaque because it can repeat URL secrets.
	tail := &lineTail{source: "hardware decoder helper", safeText: true}
	command.Stderr = tail
	if err := command.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		return nil, err
	}
	return &hardwareHelper{command: command, input: input, output: output, tail: tail}, nil
}

type hardwareOutputResult struct {
	frames, presented, dropped int
	err                        error
}

func readHardwareOutput(reader io.Reader, request MediaRequest, audio *gatedAudioWriter, firstFrame func(), receive func(*image.RGBA) error) hardwareOutputResult {
	return readHardwareOutputPTS(reader, request, audio, firstFrame, func(frame *image.RGBA, _ int64) error {
		return receive(frame)
	})
}

func readHardwareOutputPTS(reader io.Reader, request MediaRequest, audio *gatedAudioWriter, firstFrame func(), receive func(*image.RGBA, int64) error) hardwareOutputResult {
	stream, err := readVDECStreamHeader(reader)
	if err != nil {
		return hardwareOutputResult{err: err}
	}
	if stream.Width != request.Width || stream.Height != request.Height || stream.FPSNumerator != request.FPS {
		return hardwareOutputResult{err: errors.New("hardware decoder negotiated unexpected media geometry")}
	}
	frame := image.NewRGBA(image.Rect(0, 0, stream.Width, stream.Height))
	result := hardwareOutputResult{}
	var firstPTS, lastPTS int64
	for {
		ptsUS, eos, err := readVDECFrame(reader, stream, frame.Pix)
		if err != nil {
			result.err = err
			return result
		}
		if eos {
			return result
		}
		result.frames++
		if os.Getenv("TATER_VDEC_TRACE") == "1" && (result.frames <= 12 || result.frames%300 == 0) {
			audioPTS, clockOK := audio.playbackPTSUS()
			log.Printf("[video] hardware output frame=%d pts_us=%d audio_us=%d clock=%t", result.frames, ptsUS, audioPTS, clockOK)
		}
		if result.frames == 1 {
			firstPTS, lastPTS = ptsUS, ptsUS
			if err := audio.Commit(); err != nil {
				result.err = err
				return result
			}
			if firstFrame != nil {
				firstFrame()
			}
		} else {
			if ptsUS < firstPTS || ptsUS < lastPTS {
				result.err = errors.New("hardware decoder returned non-monotonic presentation timestamps")
				return result
			}
			lastPTS = ptsUS
			if audioPTS, ok := audio.playbackPTSUS(); ok && audioPTS-(ptsUS-firstPTS) > hardwareFrameLagUS {
				result.dropped++
				continue
			}
		}
		if err := receive(frame, ptsUS); err != nil {
			result.err = err
			return result
		}
		result.presented++
	}
}

func startHardwareFFmpeg(ctx context.Context, decoder string, arguments []string) (*exec.Cmd, io.ReadCloser, *os.File, *os.File, *lineTail, error) {
	credential, err := decoderCredential()
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	audioRead, audioWrite, err := os.Pipe()
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	metadataRead, metadataWrite, err := os.Pipe()
	if err != nil {
		_ = audioRead.Close()
		_ = audioWrite.Close()
		return nil, nil, nil, nil, nil, err
	}
	script := `echo 1000 > /proc/self/oom_score_adj; ulimit -v 655360 && ulimit -f 0 && ulimit -c 0 && ulimit -n 64 && exec nice -n 5 "$0" "$@"`
	argv := append([]string{"-c", script, decoder}, arguments...)
	command := exec.CommandContext(ctx, "/bin/sh", argv...)
	command.Dir = "/"
	command.Env = []string{"PATH=/usr/bin:/bin", "HOME=/"}
	command.ExtraFiles = []*os.File{audioWrite, metadataWrite}
	command.SysProcAttr = &syscall.SysProcAttr{Credential: credential, Setpgid: true, Pdeathsig: syscall.SIGKILL}
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	command.WaitDelay = 2 * time.Second
	output, err := command.StdoutPipe()
	if err != nil {
		_ = audioRead.Close()
		_ = audioWrite.Close()
		_ = metadataRead.Close()
		_ = metadataWrite.Close()
		return nil, nil, nil, nil, nil, err
	}
	tail := &lineTail{source: "video demuxer"}
	command.Stderr = tail
	if err := command.Start(); err != nil {
		_ = audioRead.Close()
		_ = audioWrite.Close()
		_ = metadataRead.Close()
		_ = metadataWrite.Close()
		return nil, nil, nil, nil, nil, err
	}
	_ = audioWrite.Close()
	_ = metadataWrite.Close()
	return command, output, audioRead, metadataRead, tail, nil
}

func readHardwareMedia(ctx context.Context, config hardwareDecoderConfig, request MediaRequest, audio io.Writer, receive func(*image.RGBA) error) (int, error) {
	arguments, err := HardwareH264MediaArgs(request)
	if err != nil {
		return 0, err
	}
	if request.FPS != 24 && request.FPS != 30 {
		request.FPS = 30
	}
	decoder := DecoderPath()
	if err := preflightHardwareDecoder(ctx, config, decoder); err != nil {
		return 0, err
	}
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	ffmpeg, video, audioRead, metadataRead, ffmpegTail, err := startHardwareFFmpeg(runContext, decoder, arguments)
	if err != nil {
		return 0, err
	}
	defer video.Close()
	defer audioRead.Close()
	defer metadataRead.Close()
	audioGate := newGatedAudioWriter(audio)
	audioDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(audioGate, audioRead)
		if copyErr == nil {
			copyErr = audioGate.Wait()
		}
		audioDone <- copyErr
		if copyErr != nil {
			cancel()
		}
	}()

	var helper *hardwareHelper
	helperRequest := request
	var outputDone chan hardwareOutputResult
	var handshakeDone chan struct{}
	traceInputs := 0
	feed := func(ptsUS int64, accessUnit []byte) error {
		if os.Getenv("TATER_VDEC_TRACE") == "1" && traceInputs < 12 {
			log.Printf("[video] hardware input pts_us=%d bytes=%d", ptsUS, len(accessUnit))
			traceInputs++
		}
		if helper == nil {
			width, height, parseErr := h264DimensionsFromAccessUnit(accessUnit)
			if parseErr != nil {
				return parseErr
			}
			if width != request.Width || height != request.Height {
				if !request.AllowSourceGeometry {
					return fmt.Errorf("H.264 source is %dx%d, not display geometry %dx%d", width, height, request.Width, request.Height)
				}
				pixels := int64(width) * int64(height)
				if width < 16 || height < 16 || width&1 != 0 || height&1 != 0 || pixels > maximumPixels {
					return fmt.Errorf("H.264 source geometry %dx%d is outside the hardware boundary", width, height)
				}
				helperRequest.Width, helperRequest.Height = width, height
			}
			helper, parseErr = startHardwareHelper(runContext, config, helperRequest)
			if parseErr != nil {
				return parseErr
			}
			outputDone = make(chan hardwareOutputResult, 1)
			handshakeDone = make(chan struct{})
			var handshakeOnce sync.Once
			go func() {
				result := readHardwareOutput(helper.output, helperRequest, audioGate, func() {
					handshakeOnce.Do(func() { close(handshakeDone) })
				}, receive)
				outputDone <- result
				if result.err != nil {
					cancel()
				}
			}()
			go func() {
				select {
				case <-handshakeDone:
				case <-time.After(10 * time.Second):
					cancel()
				case <-runContext.Done():
				}
			}()
		}
		return writeVDECAccessUnit(helper.input, ptsUS, accessUnit)
	}

	videoReader := newAsyncBoundedReader(runContext, video)
	metadataPackets := make(chan h264PacketMetadata, 16)
	metadataDone := make(chan error, 1)
	go func() {
		expectedWidth, expectedHeight := request.Width, request.Height
		if request.AllowSourceGeometry {
			expectedWidth, expectedHeight = 0, 0
		}
		metadataErr := readFrameCRCMetadata(metadataRead, expectedWidth, expectedHeight, func(metadata h264PacketMetadata) error {
			select {
			case metadataPackets <- metadata:
				return nil
			case <-runContext.Done():
				return runContext.Err()
			}
		})
		close(metadataPackets)
		metadataDone <- metadataErr
		if metadataErr != nil {
			cancel()
		}
	}()
	var inputErr error
	for metadata := range metadataPackets {
		accessUnit := make([]byte, metadata.Bytes)
		if _, err := io.ReadFull(videoReader, accessUnit); err != nil {
			inputErr = err
			cancel()
			break
		}
		if len(h264AUDOffsets(accessUnit)) != 1 || !h264AccessUnitHasVCL(accessUnit) {
			inputErr = errors.New("hardware-decoder H.264 packet is not one complete access unit")
			cancel()
			break
		}
		if err := feed(metadata.PTSUS, accessUnit); err != nil {
			inputErr = err
			cancel()
			break
		}
	}
	metadataErr := <-metadataDone
	if inputErr == nil {
		inputErr = metadataErr
	}
	if inputErr == nil {
		var trailing [1]byte
		count, trailingErr := videoReader.Read(trailing[:])
		if count != 0 || (trailingErr != nil && !errors.Is(trailingErr, io.EOF)) {
			inputErr = errors.New("hardware-decoder H.264 stream does not match its packet metadata")
		}
	}
	if inputErr == nil && helper != nil {
		inputErr = writeVDECInputEOS(helper.input)
	}
	if helper != nil {
		_ = helper.input.Close()
	}
	if inputErr != nil {
		cancel()
	}
	ffmpegErr := ffmpeg.Wait()
	if helper == nil {
		cancel()
	}
	var outputResult hardwareOutputResult
	var helperErr error
	if helper != nil {
		helperErr = helper.command.Wait()
		outputResult = <-outputDone
		_ = helper.output.Close()
	}
	var audioErr error
	select {
	case audioErr = <-audioDone:
	case <-time.After(2 * time.Second):
		_ = audioRead.Close()
		audioErr = errors.New("hardware video audio pipe did not close")
	}
	var helperDetail error
	// The owner-local MediaTek runtime emits linker warnings, component
	// discovery JSON, and vendor informational lines on stderr during every
	// successful invocation. The v2 output terminator plus a zero process exit
	// is the success contract; stderr becomes diagnostic only when the helper
	// itself exits nonzero.
	if helper != nil && helperErr != nil {
		helperDetail = helper.tail.Error()
	}
	if ctx.Err() != nil {
		for _, detail := range []error{outputResult.err, helperDetail, ffmpegTail.Error(), inputErr, ffmpegErr, helperErr, audioErr} {
			if detail != nil && !errors.Is(detail, context.Canceled) && !errors.Is(detail, ctx.Err()) {
				return outputResult.frames, fmt.Errorf("%w: %v", ctx.Err(), detail)
			}
		}
		return outputResult.frames, ctx.Err()
	}
	for _, candidate := range []error{inputErr, outputResult.err, ffmpegTail.Error(), helperDetail, ffmpegErr, helperErr, audioErr} {
		if candidate != nil {
			return outputResult.frames, candidate
		}
	}
	if helper == nil {
		return 0, errors.New("video decoder produced no H.264 access unit")
	}
	if outputResult.frames == 0 {
		return 0, errors.New("hardware decoder produced no video frame")
	}
	if outputResult.dropped != 0 {
		log.Printf("[video] hardware decoder dropped %d late frame(s) from %d decoded", outputResult.dropped, outputResult.frames)
	}
	return outputResult.frames, nil
}

// ReadMedia decodes one finite or live media session. Hardware H.264 remains
// an explicit development opt-in; any preflight or startup failure before the
// first frame restarts through the established software decoder without
// leaking buffered audio from the failed attempt.
func ReadMedia(ctx context.Context, request MediaRequest, audio io.Writer, receive func(*image.RGBA) error) error {
	if config, enabled := hardwareDecoderConfigFromEnvironment(); enabled {
		frames, err := readHardwareMedia(ctx, config, request, audio, receive)
		if err == nil || frames != 0 || ctx.Err() != nil {
			return err
		}
		if os.Getenv("TATER_VDEC_REQUIRE") == "1" {
			return err
		}
		log.Printf("[video] hardware H.264 startup failed; using software decode: %v", err)
	}
	return readSoftwareMedia(ctx, request, audio, receive)
}
