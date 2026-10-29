//go:build linux

// Package videostream runs the small, pinned FFmpeg decoder shipped with
// Tater Linux. It deliberately exposes only HTTP(S) and RTSP(S) video input
// plus raw RGBA output for the native renderer.
package videostream

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	decoder32       = "/usr/local/bin/tater-ffmpeg"
	techo5Decoder32 = "/usr/local/bin/techo5-ffmpeg"
	techo5Decoder64 = "/usr/local/bin/techo5-ffmpeg64"
	maximumURLBytes = 4096
	maximumPixels   = 1920 * 1088
)

// Request describes one live camera stream and its already-computed display
// bounds. Width and Height must be positive, even values.
type Request struct {
	URL           string
	Width, Height int
}

// MediaRequest describes provider-neutral fullscreen playback. It uses the
// same pinned decoder and resource limits as cameras, but emits audio and has
// ordinary end-of-file semantics instead of the camera controller's retry
// loop.
type MediaRequest struct {
	URL             string
	Width, Height   int
	FPS             int
	StartPositionMS int64
	VideoOnly       bool
}

// DecoderPath chooses the decoder installed beside an OTA renderer first,
// except on Athena's 64-bit kernel where the platform's static ARM64 decoder
// supports the broader codec set.
func DecoderPath() string {
	if path := strings.TrimSpace(os.Getenv("TATER_FFMPEG")); path != "" {
		return path
	}
	if kernelMachine() == "aarch64" && fileExists(techo5Decoder64) {
		return techo5Decoder64
	}
	if executable, err := os.Executable(); err == nil {
		path := filepath.Join(filepath.Dir(executable), "tater-ffmpeg")
		if fileExists(path) {
			if err := makeSlottedDecoderTraversable(path); err == nil {
				return path
			}
		}
	}
	if fileExists(decoder32) {
		return decoder32
	}
	return techo5Decoder32
}

func makeSlottedDecoderTraversable(decoder string) error {
	directory := filepath.Clean(filepath.Dir(decoder))
	slots := filepath.Clean("/data/tater-linux/app/slots")
	if filepath.Dir(directory) != slots {
		return nil
	}
	switch filepath.Base(directory) {
	case "a", "b":
		// The renderer is privileged, but FFmpeg is deliberately launched as
		// techo5-video. Older OTA installers made the slot root-only, so repair
		// that directory before dropping privileges for the decoder process.
		return os.Chmod(directory, 0o755)
	}
	return nil
}

// Installed reports whether this image or the current OTA slot carries the
// pinned decoder.
func Installed() bool { return fileExists(DecoderPath()) }

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func kernelMachine() string {
	var value unix.Utsname
	if unix.Uname(&value) != nil {
		return ""
	}
	return unix.ByteSliceToString(value.Machine[:])
}

// CheckURL accepts only the network schemes compiled into the decoder. Local
// loopback is rejected because the stream URL is supplied by a remote Tater
// integration, not by the on-device loopback UI.
func CheckURL(raw string) (*url.URL, error) {
	if raw == "" || len(raw) > maximumURLBytes {
		return nil, errors.New("video stream address is empty or too long")
	}
	for _, char := range raw {
		if char <= ' ' || char == 0x7f {
			return nil, errors.New("video stream address contains whitespace or control characters")
		}
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Opaque != "" || parsed.Host == "" {
		return nil, errors.New("video stream address is invalid")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "rtsp" && parsed.Scheme != "rtsps" {
		return nil, fmt.Errorf("video stream scheme %q is not supported", parsed.Scheme)
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return nil, errors.New("video stream address names the device itself")
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()) {
		return nil, errors.New("video stream address names the device itself")
	}
	return parsed, nil
}

// RedactedURL is safe for logs: camera credentials, query strings, fragments,
// and Home Assistant's HLS token are removed.
func RedactedURL(parsed *url.URL) string {
	if parsed == nil {
		return ""
	}
	copy := *parsed
	copy.User = nil
	copy.RawQuery = ""
	copy.Fragment = ""
	if copy.Scheme == "rtsp" || copy.Scheme == "rtsps" {
		copy.Path = "/REDACTED"
		return copy.String()
	}
	parts := strings.Split(copy.Path, "/")
	for index := range parts {
		if index > 0 && parts[index-1] == "hls" {
			parts[index] = "REDACTED"
			break
		}
	}
	copy.Path = strings.Join(parts, "/")
	return copy.String()
}

func threads(height int) int {
	switch {
	case height <= 540:
		return 2
	case height <= 720:
		return 3
	default:
		return 4
	}
}

// Args returns the bounded live-stream decode command. The output is exactly
// Width*Height*4 bytes per RGBA frame on standard output.
func Args(request Request) ([]string, error) {
	parsed, err := CheckURL(request.URL)
	if err != nil {
		return nil, err
	}
	pixels := int64(request.Width) * int64(request.Height)
	if request.Width <= 0 || request.Height <= 0 || request.Width&1 != 0 || request.Height&1 != 0 ||
		pixels <= 0 || pixels > maximumPixels {
		return nil, fmt.Errorf("invalid video stream frame size %dx%d", request.Width, request.Height)
	}
	filter := fmt.Sprintf(
		"scale=%d:%d:force_original_aspect_ratio=decrease:flags=fast_bilinear,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:black,fps=12",
		request.Width, request.Height, request.Width, request.Height,
	)
	protocols := "http,https,tcp,tls,hls,crypto"
	if parsed.Scheme == "rtsp" || parsed.Scheme == "rtsps" {
		protocols = "tcp,tls,udp,rtp,srtp,crypto"
	}
	args := []string{
		"-hide_banner", "-nostdin", "-loglevel", "error", "-max_pixels", strconv.Itoa(maximumPixels),
		"-protocol_whitelist", protocols,
	}
	if parsed.Scheme == "rtsp" || parsed.Scheme == "rtsps" {
		// Protect's official RTSPS endpoint is TCP based. Keeping media on the
		// same reliable transport also avoids opening a broad UDP receive range
		// on the satellite even though the reduced decoder supports RTP/UDP.
		args = append(args, "-rtsp_transport", "tcp", "-timeout", "15000000")
		if parsed.Scheme == "rtsps" {
			// Protect appliances normally use a locally issued certificate for
			// their LAN-only RTSPS endpoint. The stream URL is already supplied
			// over the authenticated Tater control channel; allow that local
			// certificate while retaining TLS encryption on the camera link.
			args = append(args, "-tls_verify", "0")
		}
	} else {
		args = append(args,
			"-rw_timeout", "15000000",
			"-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "4",
		)
	}
	if parsed.Scheme == "https" {
		args = append(args, "-tls_verify", "1")
		if fileExists("/etc/ssl/certs/ca-certificates.crt") {
			args = append(args, "-ca_file", "/etc/ssl/certs/ca-certificates.crt")
		}
	}
	if strings.HasSuffix(strings.ToLower(parsed.Path), ".m3u8") {
		args = append(args, "-live_start_index", "-1")
	}
	args = append(args,
		"-threads", strconv.Itoa(threads(request.Height)), "-re", "-fflags", "nobuffer", "-i", parsed.String(),
		"-map", "0:v:0", "-an", "-sn", "-dn", "-vf", filter, "-fps_mode", "passthrough",
		"-f", "rawvideo", "-pix_fmt", "rgba", "pipe:1",
	)
	return args, nil
}

// MediaArgs returns a bounded two-output command: RGBA video on stdout and
// mono 48 kHz S16LE PCM on file descriptor 3. Mono PCM is the native music
// plane wire format; the speaker duplicates it to the hardware's stereo I2S
// path and applies normal Tater ducking and output processing.
func MediaArgs(request MediaRequest) ([]string, error) {
	parsed, err := CheckURL(request.URL)
	if err != nil {
		return nil, err
	}
	pixels := int64(request.Width) * int64(request.Height)
	if request.Width <= 0 || request.Height <= 0 || request.Width&1 != 0 || request.Height&1 != 0 ||
		pixels <= 0 || pixels > maximumPixels {
		return nil, fmt.Errorf("invalid video frame size %dx%d", request.Width, request.Height)
	}
	if request.FPS != 24 && request.FPS != 30 {
		request.FPS = 30
	}
	if request.StartPositionMS < 0 {
		request.StartPositionMS = 0
	}
	filter := fmt.Sprintf(
		"scale=%d:%d:force_original_aspect_ratio=decrease:flags=fast_bilinear,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:black,fps=%d",
		request.Width, request.Height, request.Width, request.Height, request.FPS,
	)
	protocols := "http,https,tcp,tls,hls,crypto"
	if parsed.Scheme == "rtsp" || parsed.Scheme == "rtsps" {
		protocols = "tcp,tls,udp,rtp,srtp,crypto"
	}
	args := []string{
		"-hide_banner", "-nostdin", "-loglevel", "error", "-max_pixels", strconv.Itoa(maximumPixels),
		"-protocol_whitelist", protocols,
	}
	if parsed.Scheme == "rtsp" || parsed.Scheme == "rtsps" {
		args = append(args, "-rtsp_transport", "tcp", "-timeout", "15000000")
		if parsed.Scheme == "rtsps" {
			args = append(args, "-tls_verify", "0")
		}
	} else {
		args = append(args,
			"-rw_timeout", "15000000",
			"-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "4",
		)
	}
	if parsed.Scheme == "https" {
		args = append(args, "-tls_verify", "1")
		if fileExists("/etc/ssl/certs/ca-certificates.crt") {
			args = append(args, "-ca_file", "/etc/ssl/certs/ca-certificates.crt")
		}
	}
	if strings.HasSuffix(strings.ToLower(parsed.Path), ".m3u8") {
		args = append(args, "-live_start_index", "-1")
	}
	args = append(args, "-threads", strconv.Itoa(threads(request.Height)), "-re")
	if request.StartPositionMS > 0 {
		args = append(args, "-ss", strconv.FormatFloat(float64(request.StartPositionMS)/1000, 'f', 3, 64))
	}
	args = append(args, "-i", parsed.String(),
		"-map", "0:v:0", "-an", "-sn", "-dn", "-vf", filter, "-fps_mode", "passthrough",
		"-f", "rawvideo", "-pix_fmt", "rgba", "pipe:1",
	)
	if !request.VideoOnly {
		args = append(args,
			"-map", "0:a:0?", "-vn", "-sn", "-dn", "-ac", "1", "-ar", "48000", "-f", "s16le", "pipe:3",
		)
	}
	return args, nil
}

type lineTail struct {
	mu       sync.Mutex
	reported bool
}

func (tail *lineTail) Write(value []byte) (int, error) {
	tail.mu.Lock()
	defer tail.mu.Unlock()
	if strings.TrimSpace(string(value)) != "" {
		tail.reported = true
	}
	return len(value), nil
}

func (tail *lineTail) Error() error {
	tail.mu.Lock()
	defer tail.mu.Unlock()
	if !tail.reported {
		return nil
	}
	// FFmpeg error lines can repeat the complete input URL, including a camera
	// password or Home Assistant HLS token. Keep that ephemeral credential out
	// of the persistent renderer log.
	return errors.New("video decoder reported an error")
}

func decoderCredential() (*syscall.Credential, error) {
	for _, name := range []string{"techo5-video", "nobody"} {
		account, err := user.Lookup(name)
		if err != nil {
			continue
		}
		uid, uidErr := strconv.ParseUint(account.Uid, 10, 32)
		gid, gidErr := strconv.ParseUint(account.Gid, 10, 32)
		if uidErr != nil || gidErr != nil || uid == 0 || gid == 0 {
			continue
		}
		credential := &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{3003}}
		if group, groupErr := user.LookupGroup("inet"); groupErr == nil {
			if groupID, parseErr := strconv.ParseUint(group.Gid, 10, 32); parseErr == nil {
				credential.Groups = []uint32{uint32(groupID)}
			}
		}
		return credential, nil
	}
	return nil, errors.New("no unprivileged video decoder user is available")
}

// Read starts a low-priority, resource-bounded decoder and calls receive for
// each complete frame. The callback must not retain or mutate the image after
// it returns; Read reuses the same frame buffer to avoid display-sized garbage
// on every frame.
func Read(ctx context.Context, request Request, receive func(*image.RGBA) error) error {
	args, err := Args(request)
	if err != nil {
		return err
	}
	decoder := DecoderPath()
	if !fileExists(decoder) {
		return fmt.Errorf("video decoder is not installed at %s", decoder)
	}
	credential, err := decoderCredential()
	if err != nil {
		return err
	}
	script := `echo 1000 > /proc/self/oom_score_adj; ulimit -v 655360 && ulimit -f 0 && ulimit -c 0 && ulimit -n 64 && exec nice -n 5 "$0" "$@"`
	argv := append([]string{"-c", script, decoder}, args...)
	command := exec.CommandContext(ctx, "/bin/sh", argv...)
	command.Dir = "/"
	command.Env = []string{"PATH=/usr/bin:/bin", "HOME=/"}
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
		return err
	}
	tail := &lineTail{}
	command.Stderr = tail
	if err := command.Start(); err != nil {
		return err
	}
	frame := image.NewRGBA(image.Rect(0, 0, request.Width, request.Height))
	for {
		if _, err := io.ReadFull(output, frame.Pix); err != nil {
			waitErr := command.Wait()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if detail := tail.Error(); detail != nil {
				return detail
			}
			if waitErr != nil {
				return waitErr
			}
			return err
		}
		if err := receive(frame); err != nil {
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			_ = command.Wait()
			return err
		}
	}
}

// ReadMedia decodes one finite or live media session. Audio is drained in a
// sibling goroutine so neither output can back-pressure the other and disturb
// A/V timing. Cancelling ctx kills the complete FFmpeg process group.
func ReadMedia(ctx context.Context, request MediaRequest, audio io.Writer, receive func(*image.RGBA) error) error {
	args, err := MediaArgs(request)
	if err != nil {
		return err
	}
	decoder := DecoderPath()
	if !fileExists(decoder) {
		return fmt.Errorf("video decoder is not installed at %s", decoder)
	}
	credential, err := decoderCredential()
	if err != nil {
		return err
	}
	audioRead, audioWrite, err := os.Pipe()
	if err != nil {
		return err
	}
	defer audioRead.Close()
	script := `echo 1000 > /proc/self/oom_score_adj; ulimit -v 655360 && ulimit -f 0 && ulimit -c 0 && ulimit -n 64 && exec nice -n 5 "$0" "$@"`
	argv := append([]string{"-c", script, decoder}, args...)
	command := exec.CommandContext(ctx, "/bin/sh", argv...)
	command.Dir = "/"
	command.Env = []string{"PATH=/usr/bin:/bin", "HOME=/"}
	command.ExtraFiles = []*os.File{audioWrite}
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
		audioWrite.Close()
		return err
	}
	tail := &lineTail{}
	command.Stderr = tail
	if err := command.Start(); err != nil {
		audioWrite.Close()
		return err
	}
	_ = audioWrite.Close()
	audioDone := make(chan error, 1)
	go func() {
		var copyErr error
		if audio == nil {
			_, copyErr = io.Copy(io.Discard, audioRead)
		} else {
			_, copyErr = io.Copy(audio, audioRead)
		}
		audioDone <- copyErr
	}()
	waitAudio := func() error {
		select {
		case copyErr := <-audioDone:
			return copyErr
		case <-time.After(2 * time.Second):
			_ = audioRead.Close()
			return errors.New("video audio pipe did not close")
		}
	}
	frame := image.NewRGBA(image.Rect(0, 0, request.Width, request.Height))
	for {
		if _, err := io.ReadFull(output, frame.Pix); err != nil {
			waitErr := command.Wait()
			copyErr := waitAudio()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if detail := tail.Error(); detail != nil {
				return detail
			}
			if waitErr != nil {
				return waitErr
			}
			if copyErr != nil {
				return copyErr
			}
			return err
		}
		if err := receive(frame); err != nil {
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			_ = command.Wait()
			_ = waitAudio()
			return err
		}
	}
}
