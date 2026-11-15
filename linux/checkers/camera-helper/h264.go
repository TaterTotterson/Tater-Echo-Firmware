package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

const (
	vencWidth               = 1280
	vencHeight              = 720
	vencMaxPacket           = 4 * 1024 * 1024
	vencHelperHash          = "94dd2cc2edcc54b1938dff536ea44099ff3a1f7d22923dc6b137d9792279c4b4"
	vencComponentHash       = "46ec0c93f5fc5b6e6512125397d35236c5be5759dbdb285bd29c4c4714e181fe"
	cronosVencHelperHash    = "29bf2acbd3d2d2e0c69af6111d9a15d5c32404a0a9a6f1be5d4581d819d7cecc"
	cronosVencComponentHash = "161020e59faad3b1d250dcc3db168ce9109c08c8b2f02bc736109356f7ed5db4"
	cronosCodecTree         = "8564d383faba23c32545037f62a7ab9ddddf227a61790470dbc883e6eb871f0a"
	crownVencHelperHash     = "6b0ea3d6d44f9f75b266b3cad352f2924c60f1aab096254df6c58d1548b3131c"
	rookVencHelperHash      = "6dbecd9c9d673af97a2c860bc095a406ab967ae7ca0d2240f2af87fb00c56bf5"
)

type encoderProfile struct {
	target, id, helper, component, tree string
	width, height                       int
}

func encoderProfileForTarget(target string) (encoderProfile, bool) {
	switch target {
	case "checkers":
		return encoderProfile{target, "checkers-mt8163-venc-stock-1", vencHelperHash, vencComponentHash, "490a3c771d1c6f979828e60b93c363ec42403ca9bd3c98b12f8c5cd0761f2cc4", 1280, 720}, true
	case "cronos":
		return encoderProfile{target, "cronos-mt8163-venc-stock-1", cronosVencHelperHash, cronosVencComponentHash, cronosCodecTree, 1280, 960}, true
	case "crown":
		return encoderProfile{target, "crown-mt8163-venc-stock-1", crownVencHelperHash, cronosVencComponentHash, cronosCodecTree, 1280, 720}, true
	case "rook":
		return encoderProfile{target, "rook-mt8163-venc-stock-1", rookVencHelperHash, cronosVencComponentHash, cronosCodecTree, 640, 480}, true
	case "athena":
		// Only the generated-pixel-qualified NV12 profile, never an untested
		// full-resolution profile or the older MT8163 runtime.
		return encoderProfile{target, "athena-mt8183-ap-venc-1", athenaVencHelperHash, "852f82e3dd4d05d1b63435b258811f89b6f5b6466ff1d52cf577e85108cb5ec5", "de1daf2574d4d4d4d3c5ba8605852f782a9f170d35934888290e1d52d9a91db3", 640, 480}, true
	default:
		return encoderProfile{}, false
	}
}

func encoderCaptureProfile(target, mode string) (encoderProfile, bool) {
	profile, ok := encoderProfileForTarget(target)
	if !ok || (mode != "" && mode != "balanced") {
		return encoderProfile{}, false
	}
	if mode == "balanced" {
		if target == "rook" {
			// Native VGA is already the qualified 4:3 balanced wire format.
			// Keep existing Show receivers compatible without a new envelope.
			profile.width, profile.height = 640, 480
		} else if target == "cronos" || target == "athena" {
			profile.width, profile.height = 640, 480
		} else {
			profile.width, profile.height = 768, 432
		}
	}
	return profile, true
}

var vencOwner = make(chan struct{}, 1)

func cameraCapabilities(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_, _, _, err := encoderPaths()
	profile, _ := encoderProfileForTarget(layout.Board)
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(response).Encode(map[string]any{"camera_h264": err == nil, "camera_h264_version": 1, "width": profile.width, "height": profile.height, "max_fps": 30})
}

// Encoder diagnostics are retained only as a bounded failure detail. Vendor
// libraries may log frequently, and must never grow a camera log unboundedly.
type encoderLog struct {
	sync.Mutex
	value []byte
}

func (b *encoderLog) Write(p []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	const limit = 4096
	b.value = append(b.value, p...)
	if len(b.value) > limit {
		b.value = append([]byte(nil), b.value[len(b.value)-limit:]...)
	}
	return len(p), nil
}

func checkedEncoderFile(path, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 8*1024*1024 {
		return errors.New("invalid encoder runtime file")
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if fmt.Sprintf("%x", h.Sum(nil)) != expected {
		return errors.New("encoder runtime identity is not qualified")
	}
	return nil
}

func encoderPaths() (helper, component, libraries string, err error) {
	profile, qualified := encoderProfileForTarget(layout.Board)
	if !qualified || os.Getenv("TATER_VENC_ENABLE") != "1" {
		return "", "", "", errors.New("hardware capture is not enabled for this target")
	}
	if profile.target == "athena" {
		return athenaEncoderPaths()
	}
	root := strings.TrimSpace(os.Getenv("TATER_VENC_RUNTIME"))
	if root == "" {
		root = "/data/tater-venc"
	}
	manifest, readErr := readEncoderManifest(filepath.Join(root, "runtime.json"))
	if readErr != nil || validateEncoderManifestForTarget(manifest, profile.target) != nil {
		return "", "", "", errors.New("qualified encoder manifest unavailable")
	}
	executable, executableErr := os.Executable()
	if executableErr != nil {
		return "", "", "", executableErr
	}
	// Camera and its helper follow the same system/application slot. Never
	// fall through to a newer shared /data helper after a rollback.
	helper, component, libraries = filepath.Join(filepath.Dir(executable), "tater-hw-venc"), filepath.Join(root, "libMtkOmxVenc.so"), "/data/tater-vdec/lib"
	checks := map[string]string{
		helper: profile.helper, component: profile.component,
		filepath.Join(libraries, "libvcodecdrv.so"):      "cfe53dae4fde4ef786a570e10c935e5b6bee2834914770b79c83002b756c8cd8",
		filepath.Join(libraries, "libvcodec_utility.so"): "cef2ec6ede502ceb3eb75e86e3d169f970427bda67eaaee34a8f6f277e070d84",
	}
	if profile.target == "cronos" || profile.target == "crown" || profile.target == "rook" {
		checks[filepath.Join(libraries, "libvcodecdrv.so")] = "6a9b8be27634d37a451a05cb359797289254d8250573973b8fc064f4ff07989e"
		checks[filepath.Join(libraries, "libvcodec_utility.so")] = "7f46d588d62a552ec00954d0ca23662390071150a6cef7c04f814ebfd6548313"
		checks[filepath.Join(libraries, "libion_ulit.so")] = "6790909777094d628fa4337c5a4928d1bf3559cd2ce4ca6200d5507611d9f713"
		checks[filepath.Join(libraries, "libbionic28compat.so")] = "e98b5822d5e3e2ba5cc88da3a3f0aca0b9309af7f59b35c2d8ea42fdaec4f216"
	}
	for path, expected := range checks {
		if err = checkedEncoderFile(path, expected); err != nil {
			return
		}
	}
	return
}

func readEncoderManifest(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 4096 {
		return nil, errors.New("invalid encoder manifest file")
	}
	return io.ReadAll(io.LimitReader(f, 4097))
}

func validateEncoderManifest(value []byte) error {
	return validateEncoderManifestForTarget(value, "checkers")
}

func validateEncoderManifestForTarget(value []byte, target string) error {
	profile, qualified := encoderProfileForTarget(target)
	if !qualified {
		return errors.New("unqualified encoder target")
	}
	var m struct {
		Schema    int    `json:"schema"`
		ID        string `json:"runtime_id"`
		Helper    string `json:"helper_sha256"`
		Component string `json:"component_sha256"`
		Base      string `json:"base_tree_sha256"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(value)))
	decoder.DisallowUnknownFields()
	if len(value) > 4096 || decoder.Decode(&m) != nil {
		return errors.New("encoder manifest identity is not qualified")
	}
	var trailing any
	// Schema 1 described a formerly shared helper alongside the private
	// libraries. Application slots now own and independently hash-check their
	// helper in encoderPaths. Keep known historical metadata usable without
	// changing private libraries or breaking rollback; never load that old
	// shared executable or accept an arbitrary manifest helper identity.
	legacyHelper := "5431a4fb2b32378313307b4550e71842df23fb155acf04e3a816662312df2e7d"
	if target == "cronos" {
		legacyHelper = "920bf4a08373566478c8913e5ab6e3cf161cce835137ae513164bada2fc52709"
	}
	validHelper := (m.Schema == 1 && (m.Helper == profile.helper || m.Helper == legacyHelper)) || (m.Schema == 2 && m.Helper == "")
	if decoder.Decode(&trailing) != io.EOF || !validHelper || m.ID != profile.id ||
		m.Component != profile.component || m.Base != profile.tree {
		return errors.New("encoder manifest identity is not qualified")
	}
	return nil
}

func validateEncoderHeader(h []byte) error {
	return validateEncoderHeaderForTarget(h, "checkers")
}

func validateEncoderHeaderForTarget(h []byte, target string) error {
	profile, qualified := encoderProfileForTarget(target)
	return validateEncoderHeaderForProfile(h, profile, qualified)
}

func validateEncoderHeaderForProfile(h []byte, profile encoderProfile, qualified bool) error {
	if !qualified {
		return errors.New("unqualified encoder target")
	}
	if len(h) != 32 || string(h[:4]) != "TVE1" || binary.LittleEndian.Uint32(h[4:]) != 32 ||
		binary.LittleEndian.Uint32(h[8:]) != uint32(profile.width) || binary.LittleEndian.Uint32(h[12:]) != uint32(profile.height) ||
		string(h[16:20]) != "H264" || binary.LittleEndian.Uint32(h[20:]) != 30 ||
		binary.LittleEndian.Uint32(h[24:]) != 1 || binary.LittleEndian.Uint32(h[28:]) != vencMaxPacket {
		return errors.New("invalid hardware capture stream header")
	}
	return nil
}

func readEncoderPacket(reader io.Reader) (header [16]byte, payload []byte, err error) {
	if _, err = io.ReadFull(reader, header[:]); err != nil {
		return
	}
	size, flags := binary.LittleEndian.Uint32(header[:]), binary.LittleEndian.Uint32(header[4:])
	if size > vencMaxPacket || flags & ^uint32(7) != 0 || (size == 0 && flags&1 == 0) {
		err = errors.New("invalid hardware capture packet")
		return
	}
	payload = make([]byte, int(size))
	_, err = io.ReadFull(reader, payload)
	return
}

type encoderPacket struct {
	header  [16]byte
	payload []byte
}

func readEncoderStartup(reader io.Reader) ([]encoderPacket, error) {
	var packets []encoderPacket
	configBytes := 0
	for len(packets) < 128 {
		header, payload, err := readEncoderPacket(reader)
		if err != nil {
			return nil, err
		}
		flags := binary.LittleEndian.Uint32(header[4:])
		if flags&1 != 0 {
			return nil, errors.New("encoder ended before camera picture")
		}
		packets = append(packets, encoderPacket{header, payload})
		if flags&2 == 0 {
			return packets, nil
		}
		configBytes += len(payload)
		if configBytes > 64*1024 {
			return nil, errors.New("oversized encoder startup configuration")
		}
	}
	return nil, errors.New("encoder did not supply a camera picture")
}

func streamH264(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	format := request.URL.Query().Get("format")
	if format != "" && format != "annexb" {
		http.Error(response, "unsupported format", http.StatusBadRequest)
		return
	}
	captureFPS, rateErr := h264CaptureFPS(request.URL.Query().Get("max_fps"))
	if rateErr != nil {
		http.Error(response, "unsupported capture cadence", http.StatusBadRequest)
		return
	}
	profile, qualified := encoderCaptureProfile(layout.Board, request.URL.Query().Get("profile"))
	if !qualified {
		http.Error(response, "unsupported capture profile", http.StatusBadRequest)
		return
	}
	helper, component, libraries, err := encoderPaths()
	if err != nil {
		http.Error(response, "qualified hardware encoder unavailable", http.StatusServiceUnavailable)
		return
	}
	select {
	case vencOwner <- struct{}{}:
		defer func() { <-vencOwner }()
	default:
		http.Error(response, "hardware encoder busy", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithCancel(request.Context())
	defer cancel()
	device := camera.Get()
	release, err := device.AcquireTaterH264()
	if err != nil {
		http.Error(response, "camera unavailable", http.StatusServiceUnavailable)
		return
	}
	defer release()
	frames := make(chan *camera.Frame, 1)
	var sourceFrames atomic.Uint64
	var preparedFrames, prepareNS atomic.Uint64
	stopListening := device.Frames.Listen(func(frame *camera.Frame) {
		sourceFrames.Add(1)
		select {
		case frames <- frame:
			return
		default:
		}
		select {
		case <-frames:
		default:
		}
		select {
		case frames <- frame:
		default:
		}
	})
	defer stopListening()
	workerContext, stopWorker := context.WithCancel(context.WithoutCancel(ctx))
	defer stopWorker()
	command := exec.CommandContext(workerContext, helper, encoderProcessArguments(profile, component)...)
	command.Env = []string{"PATH=/usr/bin:/bin", "ANDROID_ROOT=/system", "ANDROID_DATA=/data", "LD_LIBRARY_PATH=" + libraries}
	if profile.target == "cronos" || profile.target == "crown" || profile.target == "rook" {
		command.Env = append(command.Env, "LD_PRELOAD="+filepath.Join(libraries, "libbionic28compat.so"))
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }
	command.WaitDelay = time.Second
	var diagnostics encoderLog
	command.Stderr = &diagnostics
	input, err := command.StdinPipe()
	if err != nil {
		http.Error(response, "encoder input unavailable", http.StatusServiceUnavailable)
		return
	}
	output, err := command.StdoutPipe()
	if err != nil {
		input.Close()
		http.Error(response, "encoder output unavailable", http.StatusServiceUnavailable)
		return
	}
	if err = command.Start(); err != nil {
		input.Close()
		output.Close()
		http.Error(response, "encoder startup failed", http.StatusServiceUnavailable)
		return
	}
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		defer input.Close()
		inputHealthy := true
		defer func() {
			if profile.target == "athena" && inputHealthy {
				// The narrow AP protocol uses an explicit zero record, not
				// unframed EOF. Let the codec drain and release hardware first.
				if pipe, ok := input.(*os.File); ok {
					_ = pipe.SetWriteDeadline(time.Now().Add(time.Second))
				}
				_, _ = input.Write(make([]byte, 16))
			}
		}()
		var reuse []byte
		var development camera.TaterEncoderInput
		var first time.Time
		var lastTone time.Time
		var previous int64 = -1
		var lastIDRRequest int64 = -1000000
		var cadence h264CaptureCadence
		if captureFPS < 30 {
			cadence.interval = time.Second / time.Duration(captureFPS)
		}
		for {
			select {
			case <-ctx.Done():
				return
			case frame := <-frames:
				if frame == nil {
					continue
				}
				if !cadence.accept(frame.At) {
					continue
				}
				prepareStarted := time.Now()
				refreshTone := lastTone.IsZero() || frame.At.Sub(lastTone) >= 100*time.Millisecond
				reuse = development.Prepare(frame, reuse, refreshTone)
				if refreshTone {
					lastTone = frame.At
				}
				prepareNS.Add(uint64(time.Since(prepareStarted)))
				preparedFrames.Add(1)
				if len(reuse) == 0 {
					continue
				}
				if first.IsZero() {
					first = frame.At
				}
				pts := frame.At.Sub(first).Microseconds()
				if pts <= previous {
					continue
				}
				previous = pts
				var record [16]byte
				binary.LittleEndian.PutUint32(record[:], uint32(len(reuse)))
				if pts-lastIDRRequest >= 1000000 {
					binary.LittleEndian.PutUint32(record[4:], 1)
					lastIDRRequest = pts
				}
				binary.LittleEndian.PutUint64(record[8:], uint64(pts))
				if pipe, ok := input.(*os.File); ok {
					_ = pipe.SetWriteDeadline(time.Now().Add(2 * time.Second))
				}
				if _, err := input.Write(record[:]); err != nil {
					inputHealthy = false
					cancel()
					return
				}
				if _, err := input.Write(reuse); err != nil {
					inputHealthy = false
					cancel()
					return
				}
			}
		}
	}()
	defer func() {
		cancel()
		// A previous read timeout must not prevent draining EOS during cleanup.
		if pipe, ok := output.(*os.File); ok {
			_ = pipe.SetReadDeadline(time.Time{})
		}
		forced, err := finishEncoder(command, input, output, writerDone, stopWorker, 2*time.Second)
		if forced {
			log.Printf("[camera-h264] encoder cleanup exceeded deadline; forced termination")
		} else if err != nil {
			// Do not log vendor output: it can contain private buffer addresses.
			log.Printf("[camera-h264] encoder stopped: %v", err)
		} else {
			log.Printf("[camera-h264] encoder completed clean shutdown")
		}
	}()
	// Pipe deadlines bound both startup and a mid-stream hardware stall.
	pipe, ok := output.(*os.File)
	if !ok {
		http.Error(response, "encoder pipe unavailable", http.StatusServiceUnavailable)
		return
	}
	_ = pipe.SetReadDeadline(time.Now().Add(8 * time.Second))
	var header [32]byte
	if _, err = io.ReadFull(output, header[:]); err != nil || validateEncoderHeaderForProfile(header[:], profile, true) != nil {
		http.Error(response, "encoder handshake failed", http.StatusServiceUnavailable)
		return
	}
	// Wait for the first picture before committing HTTP success. Missing
	// sensor frames and a privacy-held sensor therefore fail without a fake stream.
	pending, err := readEncoderStartup(output)
	if err != nil {
		http.Error(response, "camera capture unavailable", http.StatusServiceUnavailable)
		return
	}
	record, payload := pending[0].header, pending[0].payload
	if format == "annexb" {
		response.Header().Set("Content-Type", "video/h264")
	} else {
		response.Header().Set("Content-Type", "application/vnd.tater.h264")
	}
	response.Header().Set("Cache-Control", "no-store")
	control := http.NewResponseController(response)
	started, count, bytes := time.Now(), 0, 0
	var lastPicturePTS int64 = -1
	var gapTotalUS, gapMaxUS int64
	gapCount := 0
	if format != "annexb" {
		if _, err = response.Write(header[:]); err != nil {
			return
		}
	}
	for {
		_ = control.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if format != "annexb" {
			if _, err = response.Write(record[:]); err != nil {
				return
			}
		}
		if _, err = response.Write(payload); err != nil {
			return
		}
		if err = control.Flush(); err != nil {
			return
		}
		flags := binary.LittleEndian.Uint32(record[4:])
		if flags&1 != 0 {
			return
		}
		if flags&2 == 0 {
			count++
			pts := int64(binary.LittleEndian.Uint64(record[8:]))
			if lastPicturePTS >= 0 && pts > lastPicturePTS {
				gap := pts - lastPicturePTS
				gapTotalUS += gap
				gapMaxUS = max(gapMaxUS, gap)
				gapCount++
			}
			lastPicturePTS = pts
		}
		bytes += len(payload)
		if elapsed := time.Since(started); elapsed >= 10*time.Second {
			prepared := preparedFrames.Swap(0)
			ns := prepareNS.Swap(0)
			log.Printf("[camera-h264] %dx%d source=%.1f fps encoded=%.1f fps %.2f Mbit/s prepare=%s source_gap_avg/max=%s/%s", profile.width, profile.height, float64(sourceFrames.Swap(0))/elapsed.Seconds(), float64(count)/elapsed.Seconds(), float64(bytes)*8/elapsed.Seconds()/1e6, time.Duration(ns/max(prepared, 1)).Round(time.Microsecond), (time.Duration(gapTotalUS/int64(max(gapCount, 1))) * time.Microsecond).Round(time.Millisecond), (time.Duration(gapMaxUS) * time.Microsecond).Round(time.Millisecond))
			started, count, bytes = time.Now(), 0, 0
			gapTotalUS, gapMaxUS, gapCount = 0, 0, 0
		}
		_ = pipe.SetReadDeadline(time.Now().Add(8 * time.Second))
		if len(pending) > 1 {
			pending = pending[1:]
			record, payload = pending[0].header, pending[0].payload
			continue
		}
		pending = nil
		record, payload, err = readEncoderPacket(output)
		if err != nil {
			return
		}
	}
}
