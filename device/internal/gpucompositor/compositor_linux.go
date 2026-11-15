//go:build linux

// Package gpucompositor connects the Checkers UI to a version-matched open
// Mali-T720 pbuffer helper and owner-local libraries. Go remains the
// sole framebuffer owner and falls back to the CPU path on any preflight or
// stream failure.
package gpucompositor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/hardwarebundle"
)

const (
	defaultRoot                = "/data/tater-gpu"
	manifestMaximumBytes       = 64 * 1024
	expectedRuntimeID          = "checkers-mt8163-mali-pbuffer-1"
	expectedTreeSHA256         = "bc75d30a7be167b827e89b1d7629ec5dc4b528c99106d0b1e9beed02b4815561"
	expectedHelperSHA256       = "a646a0ad7fb44efb2855a3d64ab5411096356b3516c288805e166acef532fd76"
	expectedSceneHelperSHA256  = "706358262178bbb6260c468741a1dbcb3262d131f4c7c524ea97cfb99b06cb7e"
	expectedTextHelperSHA256   = "3dc9b5403f85d627f275739e6fa4ed86825cbfbde0a3edb7e79313f12b59f2f0"
	legacyPreparedHelperSHA256 = "28d0dcb884d486c9ae3cfaaec127f9e4ff2b28fba9eabfc9e755d3e1cf19de1f"
	expectedLinkerSHA256       = "b9c6e172a16c5e1337cca905bc3457392e877ffc3b040e250929a5ca56afdaaa"
	expectedMaliSHA256         = "dcb3eac3dd0f220124b39c5a44b0e8a4d5caeb475fe9465db45d8f5f9a75f04d"
	expectedCompatSHA256       = "e98b5822d5e3e2ba5cc88da3a3f0aca0b9309af7f59b35c2d8ea42fdaec4f216"
	expectedRuntimeFiles       = 34
	expectedRuntimeBytes       = int64(37_506_624)
	cronosRuntimeID            = "cronos-mt8163-mali-pbuffer-1"
	cronosHelperSHA256         = "ce71abcc445a903b37113aecfdb0514835e00def9555220a5dbae29e71806f0b"
	crownRuntimeID             = "crown-mt8163-mali-pbuffer-1"
	crownHelperSHA256          = "93d741156d90f2bfb000552abd17a56870ad11f0723aaa8e3dcdd3eb4d022de3"
	rookRuntimeID              = "rook-mt8163-mali-pbuffer-1"
	rookHelperSHA256           = "0b69363ff3856a13343def35e90a92f363dfcc8ac880cf21b7b36730061c84b6"
)

type runtimeManifest struct {
	Schema       int    `json:"schema"`
	RuntimeID    string `json:"runtime_id"`
	TreeSHA256   string `json:"tree_sha256"`
	FileCount    int    `json:"file_count"`
	RuntimeBytes int64  `json:"runtime_bytes"`
	HelperSHA256 string `json:"helper_sha256"`
}

type capabilities struct {
	Adapter      string `json:"adapter"`
	Version      int    `json:"version"`
	SourceWidth  int    `json:"source_width"`
	SourceHeight int    `json:"source_height"`
	PanelWidth   int    `json:"panel_width"`
	PanelHeight  int    `json:"panel_height"`
	Output       string `json:"output"`
	SceneVersion int    `json:"scene_version"`
}

type config struct {
	root, helper, linker, mali, compat, manifest string
	target                                       string
}

func configFromEnvironment() (config, bool) {
	target := strings.ToLower(strings.TrimSpace(os.Getenv("TATER_FIRMWARE_TARGET")))
	if _, qualified := GeometryForTarget(target); os.Getenv("TATER_GPU_ENABLE") != "1" || !qualified {
		return config{}, false
	}
	root := strings.TrimSpace(os.Getenv("TATER_GPU_ROOT"))
	if root == "" {
		root = defaultRoot
		if target == "athena" {
			root = "/data/tater-mesa"
			if _, err := os.Stat(root + "/runtime.json"); os.IsNotExist(err) {
				if _, err := os.Stat("/usr/lib/tater-mesa/runtime.json"); err == nil {
					root = "/usr/lib/tater-mesa"
				}
			}
		}
	}
	return config{
		target: target,
		root:   root, helper: hardwarebundle.Helper("tater-hw-gpu"), linker: root + "/linker",
		mali: root + "/lib/libGLES_mali.so", compat: root + "/lib/libbionic28compat.so",
		manifest: root + "/runtime.json",
	}, true
}

func (value config) environment() []string {
	if value.target == "athena" {
		return []string{"PATH=/usr/bin:/bin", "HOME=/tmp", "EGL_PLATFORM=surfaceless",
			"LD_LIBRARY_PATH=" + value.root + "/lib", "LIBGL_DRIVERS_PATH=" + value.root + "/dri",
			"MESA_LOADER_DRIVER_OVERRIDE=panfrost", "MESA_SHADER_CACHE_DISABLE=true"}
	}
	return []string{
		"PATH=/system/bin:/usr/bin:/bin", "HOME=/", "ANDROID_ROOT=/system", "ANDROID_DATA=/data",
		"LD_LIBRARY_PATH=" + value.root + "/lib", "LD_PRELOAD=" + value.compat,
	}
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

func validateManifest(value []byte) error {
	return validateManifestForTarget(value, "checkers")
}

func validateManifestForTarget(value []byte, target string) error {
	runtimeID := expectedRuntimeID
	if target == "cronos" {
		runtimeID = cronosRuntimeID
	} else if target == "crown" {
		runtimeID = crownRuntimeID
	} else if target == "rook" {
		runtimeID = rookRuntimeID
	} else if target != "checkers" {
		return errors.New("GPU target is not qualified")
	}
	var manifest runtimeManifest
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return errors.New("GPU runtime manifest is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("GPU runtime manifest has trailing data")
	}
	validHelper := (manifest.Schema == 1 && ((target == "checkers" && allowedHelper(manifest.HelperSHA256)) ||
		(target == "cronos" && manifest.HelperSHA256 == cronosHelperSHA256))) ||
		(manifest.Schema == 2 && manifest.HelperSHA256 == "")
	if !validHelper || manifest.RuntimeID != runtimeID ||
		manifest.TreeSHA256 != expectedTreeSHA256 || manifest.FileCount != expectedRuntimeFiles ||
		manifest.RuntimeBytes != expectedRuntimeBytes {
		return errors.New("GPU runtime identity is not allowlisted")
	}
	return nil
}

func allowedHelper(digest string) bool {
	// Schema-1 private runtime metadata can name the original prepared helper.
	// This does not allow executing it: validateRuntime separately requires
	// the current firmware-owned helper's exact hash beside the renderer.
	return digest == expectedHelperSHA256 || digest == expectedSceneHelperSHA256 ||
		digest == expectedTextHelperSHA256 || digest == legacyPreparedHelperSHA256
}

func validateRuntime(value config) error {
	if value.target == "athena" {
		return validateMesaRuntime(value)
	}
	manifest, err := boundedFile(value.manifest, manifestMaximumBytes)
	if err != nil {
		return fmt.Errorf("GPU runtime manifest is unavailable: %w", err)
	}
	if err := validateManifestForTarget(manifest, value.target); err != nil {
		return err
	}
	for path, expected := range map[string]string{
		value.linker: expectedLinkerSHA256,
		value.mali:   expectedMaliSHA256, value.compat: expectedCompatSHA256,
	} {
		actual, hashErr := fileSHA256(path)
		if hashErr != nil || actual != expected {
			return fmt.Errorf("GPU runtime file is not allowlisted: %s", path)
		}
	}
	// Legacy runtime manifests describe a shared helper too. Only their fixed
	// library identity matters now: the owning firmware pins its own helper.
	helperHash := expectedTextHelperSHA256
	if value.target == "cronos" {
		helperHash = cronosHelperSHA256
	} else if value.target == "crown" {
		helperHash = crownHelperSHA256
	} else if value.target == "rook" {
		helperHash = rookHelperSHA256
	}
	if actual, err := fileSHA256(value.helper); err != nil || actual != helperHash {
		return errors.New("GPU helper does not match this firmware")
	}
	info, err := os.Stat("/dev/mali0")
	if err != nil || info.Mode()&os.ModeDevice == 0 {
		return errors.New("GPU device node is unavailable")
	}
	return nil
}

func validateCapabilities(value []byte) error {
	return validateCapabilitiesForTarget(value, "checkers")
}

func validateCapabilitiesForTarget(value []byte, target string) error {
	geometry, qualified := GeometryForTarget(target)
	var result capabilities
	if !qualified || len(value) > 4096 || json.Unmarshal(value, &result) != nil ||
		result.Adapter != "tater-gpu-rotate" || result.Version != 1 ||
		result.SourceWidth != geometry.SourceWidth || result.SourceHeight != geometry.SourceHeight ||
		result.PanelWidth != geometry.PanelWidth || result.PanelHeight != geometry.PanelHeight || result.Output != "bgra" {
		return errors.New("GPU compositor does not satisfy the protocol-v1 contract")
	}
	if result.SceneVersion != 0 && result.SceneVersion != 2 && result.SceneVersion != 3 && result.SceneVersion != 4 {
		return errors.New("unsupported GPU scene protocol")
	}
	return nil
}

type tailBuffer struct {
	mu    sync.Mutex
	value []byte
}

func (buffer *tailBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	const maximum = 8192
	buffer.value = append(buffer.value, value...)
	if len(buffer.value) > maximum {
		buffer.value = append([]byte(nil), buffer.value[len(buffer.value)-maximum:]...)
	}
	return len(value), nil
}

func (buffer *tailBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return strings.TrimSpace(string(buffer.value))
}

type boundedBuffer struct {
	value     bytes.Buffer
	remaining int
}

func (buffer *boundedBuffer) Write(value []byte) (int, error) {
	if len(value) > buffer.remaining {
		return 0, errors.New("output exceeds its boundary")
	}
	buffer.remaining -= len(value)
	return buffer.value.Write(value)
}

func (buffer *boundedBuffer) Bytes() []byte { return buffer.value.Bytes() }

type Compositor struct {
	geometry Geometry
	command  *exec.Cmd
	input    io.WriteCloser
	output   io.ReadCloser
	stderr   *tailBuffer
	closed   bool
	scenes   bool
	text     bool
	video    bool
}

// Open starts the compositor only when the reviewed owner-local libraries are
// explicitly enabled for a qualified target. A disabled runtime returns (nil, nil).
func Open() (*Compositor, error) {
	configuration, enabled := configFromEnvironment()
	if !enabled {
		return nil, nil
	}
	if err := validateRuntime(configuration); err != nil {
		return nil, err
	}
	checkContext, cancelCheck := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelCheck()
	program, arguments := configuration.invocation("--capabilities")
	check := exec.CommandContext(checkContext, program, arguments...)
	check.Dir = "/"
	check.Env = configuration.environment()
	check.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	capabilityOutput := &boundedBuffer{remaining: 4096}
	check.Stdout = capabilityOutput
	check.Stderr = io.Discard
	if err := check.Run(); err != nil {
		return nil, fmt.Errorf("GPU capability check failed: %w", err)
	}
	if err := validateCapabilitiesForTarget(capabilityOutput.Bytes(), configuration.target); err != nil {
		return nil, err
	}

	program, arguments = configuration.invocation("--stream-rgba-rotate-v1", "--target", configuration.target, "--acknowledge-gpu-job", "pbuffer-rotate-readback", "run")
	command := exec.Command(program, arguments...)
	command.Dir = "/"
	command.Env = configuration.environment()
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
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
	tail := &tailBuffer{}
	command.Stderr = tail
	if err := command.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		return nil, err
	}
	geometry, _ := GeometryForTarget(configuration.target)
	result := &Compositor{geometry: geometry, command: command, input: input, output: output, stderr: tail}
	var capability capabilities
	_ = json.Unmarshal(capabilityOutput.Bytes(), &capability)
	result.scenes = capability.SceneVersion >= 2
	result.text = capability.SceneVersion >= 3
	result.video = capability.SceneVersion >= 4
	result.deadline(5 * time.Second)
	if err := readStreamHeaderForGeometry(output, geometry); err != nil {
		_ = result.Close()
		return nil, fmt.Errorf("GPU compositor startup failed: %w", err)
	}
	return result, nil
}

func (compositor *Compositor) Composite(source, destination []byte) error {
	if compositor == nil || compositor.closed {
		return errors.New("GPU compositor is closed")
	}
	compositor.deadline(750 * time.Millisecond)
	if err := writeInputFrameForGeometry(compositor.input, source, compositor.geometry); err != nil {
		return compositor.fail(err)
	}
	if err := readOutputFrameForGeometry(compositor.output, destination, compositor.geometry); err != nil {
		return compositor.fail(err)
	}
	return nil
}

func (compositor *Compositor) SupportsScenes() bool {
	return compositor != nil && !compositor.closed && compositor.scenes
}

func (compositor *Compositor) SupportsAnimatedText() bool {
	return compositor.SupportsScenes() && compositor.text
}

func (compositor *Compositor) SupportsVideoScenes() bool {
	return compositor.SupportsAnimatedText() && compositor.video
}

func (compositor *Compositor) CompositeScene(source, destination []byte, effects Effects) error {
	if !compositor.SupportsScenes() {
		return errors.New("GPU retained scenes are unavailable")
	}
	if effects.Text != nil && !compositor.SupportsAnimatedText() {
		return errors.New("GPU word animation is unavailable")
	}
	if effects.Video != nil && !compositor.SupportsVideoScenes() {
		return errors.New("GPU video scenes are unavailable")
	}
	compositor.deadline(750 * time.Millisecond)
	if err := writeSceneFrameForGeometry(compositor.input, source, effects, compositor.geometry); err != nil {
		return compositor.fail(err)
	}
	if err := readOutputFrameForGeometry(compositor.output, destination, compositor.geometry); err != nil {
		return compositor.fail(err)
	}
	return nil
}

func (compositor *Compositor) deadline(timeout time.Duration) {
	until := time.Now().Add(timeout)
	for _, stream := range []any{compositor.input, compositor.output} {
		if file, ok := stream.(interface{ SetDeadline(time.Time) error }); ok {
			_ = file.SetDeadline(until)
		}
	}
}

func (compositor *Compositor) fail(reason error) error {
	detail := compositor.stderr.String()
	_ = compositor.Close()
	if detail != "" {
		return fmt.Errorf("%w: %s", reason, detail)
	}
	return reason
}

func (compositor *Compositor) Close() error {
	if compositor == nil || compositor.closed {
		return nil
	}
	compositor.closed = true
	_ = compositor.input.Close()
	_ = compositor.output.Close()
	if compositor.command.Process != nil {
		_ = syscall.Kill(-compositor.command.Process.Pid, syscall.SIGKILL)
	}
	return compositor.command.Wait()
}
