package show

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/liveh264"
)

func TestCapturePacingIsRequestedBeforeEncoding(t *testing.T) {
	for _, rate := range []int{15, 30} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/stream.h264" || r.URL.Query().Get("max_fps") != fmt.Sprint(rate) {
				t.Error("wrong capture request", r.URL)
			}
			w.Header().Set("Content-Type", "application/vnd.tater.h264")
			_, _ = w.Write(append(captureFixture(), capturePacket(0, 1, 0, nil)...))
		}))
		if err := StreamCameraH264Paced(context.Background(), strings.TrimPrefix(server.URL, "http://"), rate, nil); err != nil {
			t.Fatal(err)
		}
		server.Close()
	}
	if err := StreamCameraH264Paced(context.Background(), "invalid", 60, nil); err == nil {
		t.Fatal("accepted unqualified rate")
	}
}

func TestBalancedCaptureExplicitlyRequestsProfile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("profile") != "balanced" || r.URL.Query().Get("max_fps") != "15" {
			t.Error("wrong balanced request", r.URL)
		}
		w.Header().Set("Content-Type", "application/vnd.tater.h264")
		h := captureFixture()
		binary.LittleEndian.PutUint32(h[8:], 640)
		binary.LittleEndian.PutUint32(h[12:], 480)
		_, _ = w.Write(append(h, capturePacket(0, 1, 0, nil)...))
	}))
	defer server.Close()
	if err := StreamCameraH264Profile(context.Background(), strings.TrimPrefix(server.URL, "http://"), 15, "balanced", nil); err != nil {
		t.Fatal(err)
	}
	if StreamCameraH264Profile(context.Background(), "invalid", 15, "arbitrary", nil) == nil {
		t.Fatal("unqualified profile accepted")
	}
}

func captureFixture() []byte {
	h := make([]byte, 32)
	copy(h, "TVE1")
	binary.LittleEndian.PutUint32(h[4:], 32)
	binary.LittleEndian.PutUint32(h[8:], 1280)
	binary.LittleEndian.PutUint32(h[12:], 720)
	copy(h[16:], "H264")
	binary.LittleEndian.PutUint32(h[20:], 30)
	binary.LittleEndian.PutUint32(h[24:], 1)
	binary.LittleEndian.PutUint32(h[28:], 4*1024*1024)
	return h
}
func capturePacket(size, flags uint32, pts uint64, payload []byte) []byte {
	h := make([]byte, 16)
	binary.LittleEndian.PutUint32(h, size)
	binary.LittleEndian.PutUint32(h[4:], flags)
	binary.LittleEndian.PutUint64(h[8:], pts)
	return append(h, payload...)
}

func TestCaptureCronosPreservesFourByThreeGeometry(t *testing.T) {
	wire := captureFixture()
	binary.LittleEndian.PutUint32(wire[12:], 960)
	config := []byte{0, 0, 0, 1, 0x67, 0x42, 0, 0, 0, 1, 0x68, 0x88}
	idr := []byte{0, 0, 0, 1, 0x65, 0x88}
	wire = append(wire, capturePacket(uint32(len(config)), 2, 0, config)...)
	wire = append(wire, capturePacket(uint32(len(idr)), 4, 1234, idr)...)
	wire = append(wire, capturePacket(0, 1, 0, nil)...)
	count := 0
	if err := readCameraH264(context.Background(), bytes.NewReader(wire), func(f liveh264.Frame) {
		count++
		if f.Width != 1280 || f.Height != 960 || f.PTSUS != 1234 {
			t.Fatal("Cronos frame was cropped or retimed", f)
		}
	}); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}
func TestCaptureH264PrependsCurrentConfigToEveryKeyframe(t *testing.T) {
	config := []byte{0, 0, 0, 1, 0x67, 0x42, 0, 0, 0, 1, 0x68, 0x88}
	idr := []byte{0, 0, 0, 1, 0x65, 0x88}
	wire := captureFixture()
	for n := 0; n < 3; n++ {
		wire = append(wire, capturePacket(uint32(len(config)), 2, 0, config)...)
		wire = append(wire, capturePacket(uint32(len(idr)), 4, uint64(n)*1000000, idr)...)
	}
	wire = append(wire, capturePacket(0, 1, 0, nil)...)
	var frames []liveh264.Frame
	if err := readCameraH264(context.Background(), bytes.NewReader(wire), func(f liveh264.Frame) { frames = append(frames, f) }); err != nil {
		t.Fatal(err)
	}
	if len(frames) != 3 {
		t.Fatalf("frames %d", len(frames))
	}
	for n, f := range frames {
		if !bytes.Equal(f.Data, append(append([]byte(nil), config...), idr...)) || !f.Keyframe || f.Sequence != uint32(n) || f.PTSUS != int64(n)*1000000 || f.StreamID != frames[0].StreamID {
			t.Fatalf("frame %#v", f)
		}
	}
}
func TestCaptureRejectsMalformedPacketsAndBareKeyframes(t *testing.T) {
	for _, packet := range [][]byte{capturePacket(1024*1024+1, 0, 0, nil), capturePacket(0, 0, 0, nil), capturePacket(6, 4, 0, []byte{0, 0, 0, 1, 0x65, 0x88})} {
		if err := readCameraH264(context.Background(), bytes.NewReader(append(captureFixture(), packet...)), nil); err == nil {
			t.Fatal("accepted invalid packet")
		}
	}
}
