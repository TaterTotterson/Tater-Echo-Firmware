//go:build linux

package videostream

import (
	"strings"
	"testing"
)

func TestArgsBoundsNetworkDecode(t *testing.T) {
	args, err := Args(Request{URL: "https://homeassistant.invalid/api/hls/token/playlist.m3u8", Width: 896, Height: 672})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, expected := range []string{
		"-protocol_whitelist http,https,tcp,tls,hls,crypto",
		"-rw_timeout 15000000",
		"-threads 3",
		"-f rawvideo -pix_fmt rgba pipe:1",
		"scale=896:672:force_original_aspect_ratio=decrease",
		"fps=12",
		"-live_start_index -1",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("decoder arguments %q do not contain %q", joined, expected)
		}
	}
}

func TestArgsKeepsHLSOnlyOptionOffOtherHTTPInputs(t *testing.T) {
	args, err := Args(Request{URL: "http://camera.invalid/live.mp4", Width: 640, Height: 360})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(args, " "), "live_start_index") {
		t.Fatal("MP4 input received an HLS-only option")
	}
}

func TestArgsSupportsProtectRTSPSOverTCP(t *testing.T) {
	args, err := Args(Request{URL: "rtsps://protect.invalid:7441/private-token?enableSrtp", Width: 896, Height: 672})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, expected := range []string{
		"-protocol_whitelist tcp,tls,udp,rtp,srtp,crypto",
		"-rtsp_transport tcp -timeout 15000000",
		"-tls_verify 0",
		"rtsps://protect.invalid:7441/private-token?enableSrtp",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("decoder arguments %q do not contain %q", joined, expected)
		}
	}
	if strings.Contains(joined, "-rw_timeout ") || strings.Contains(joined, "-reconnect ") || strings.Contains(joined, "live_start_index") {
		t.Fatalf("RTSPS input received HTTP/HLS-only options: %q", joined)
	}
}

func TestArgsKeepsRTSPSCertificatePolicyOffPlainRTSP(t *testing.T) {
	args, err := Args(Request{URL: "rtsp://camera.invalid/live", Width: 640, Height: 360})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "-tls_verify") || strings.Contains(joined, "-rw_timeout") {
		t.Fatalf("plain RTSP input received TLS or HTTP-only options: %q", joined)
	}
}

func TestArgsRejectsUnsafeOrOversizedInput(t *testing.T) {
	for _, request := range []Request{
		{URL: "file:///etc/passwd", Width: 896, Height: 672},
		{URL: "http://127.0.0.1/private", Width: 896, Height: 672},
		{URL: "https://camera.invalid/live", Width: 895, Height: 672},
		{URL: "https://camera.invalid/live", Width: 3840, Height: 2160},
	} {
		if _, err := Args(request); err == nil {
			t.Fatalf("unsafe request was accepted: %#v", request)
		}
	}
}

func TestRedactedURLRemovesCredentialsAndHLSToken(t *testing.T) {
	parsed, err := CheckURL("https://user:secret@ha.invalid/api/hls/private-token/playlist.m3u8?auth=secret")
	if err != nil {
		t.Fatal(err)
	}
	redacted := RedactedURL(parsed)
	if strings.Contains(redacted, "secret") || strings.Contains(redacted, "private-token") || !strings.Contains(redacted, "/api/hls/REDACTED/") {
		t.Fatalf("redacted URL = %q", redacted)
	}
}

func TestRedactedURLRemovesProtectStreamBearer(t *testing.T) {
	parsed, err := CheckURL("rtsps://protect.invalid:7441/private-token?enableSrtp")
	if err != nil {
		t.Fatal(err)
	}
	redacted := RedactedURL(parsed)
	if strings.Contains(redacted, "private-token") || strings.Contains(redacted, "enableSrtp") || redacted != "rtsps://protect.invalid:7441/REDACTED" {
		t.Fatalf("redacted Protect URL = %q", redacted)
	}
}

func TestDecoderErrorDoesNotExposeStreamURL(t *testing.T) {
	var tail lineTail
	if _, err := tail.Write([]byte("https://ha.invalid/api/hls/private-token/playlist.m3u8: failed\n")); err != nil {
		t.Fatal(err)
	}
	err := tail.Error()
	if err == nil {
		t.Fatal("decoder output did not produce an error")
	}
	if strings.Contains(err.Error(), "private-token") || strings.Contains(err.Error(), "https://") {
		t.Fatalf("decoder error exposed the stream URL: %q", err)
	}
}
