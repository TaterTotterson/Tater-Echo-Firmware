//go:build !dot && !spot && !panel

package camera

import (
	"context"
	"encoding/binary"
	"fmt"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"image/color"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestTaterLiveToneMatchesPlatformWithinOneLSB(t *testing.T) {
	rng := rand.New(rand.NewSource(29))
	for n := 0; n < 50; n++ {
		black := rng.Intn(64)
		level := tone{gainR: 0.5 + rng.Float64()*1.5, gainB: 0.5 + rng.Float64()*1.5, black: black, white: black + 256 + rng.Intn(1700), gamma: 0.37 + rng.Float64()*0.19}
		expected, got := level.tables(), level.taterLiveTables()
		for ch := 0; ch < 3; ch++ {
			for v := 0; v < 2048; v++ {
				delta := int(expected[ch][v]) - int(got[ch][v])
				if delta < -1 || delta > 1 {
					t.Fatalf("tone %d channel %d value %d difference %d", n, ch, v, delta)
				}
			}
		}
	}
}

func TestTaterLiveExposureOnlyAffectsQualifiedActiveCameras(t *testing.T) {
	saved := layout.Board
	defer func() { layout.Board = saved; taterLiveVideoUsers.Store(0) }()
	for _, target := range []string{"checkers", "cronos", "crown", "athena", "rook", ""} {
		layout.Board = target
		taterLiveVideoUsers.Store(0)
		mid, shutter, live := taterVideoExposureLimits()
		if live || mid != aeMidGain || shutter != aeMaxShut {
			t.Fatal("changed still exposure")
		}
		taterLiveVideoUsers.Store(1)
		mid, shutter, live = taterVideoExposureLimits()
		qualified := target == "checkers" || target == "cronos" || target == "crown"
		if qualified && (!live || mid != aeMaxGain || shutter != aeFrame) {
			t.Fatal("live exposure not bounded to sensor frame")
		}
		if !qualified && (live || mid != aeMidGain || shutter != aeMaxShut) {
			t.Fatal("changed unqualified target exposure")
		}
	}
}

func TestMotionExposureLeaseRestoresNormalPolicy(t *testing.T) {
	saved := layout.Board
	layout.Board = "checkers"
	t.Cleanup(func() { layout.Board = saved; taterLiveVideoUsers.Store(0) })
	c := offDevice(t, func(stop, stopped chan struct{}) { defer close(stopped); <-stop })
	first, err := c.AcquireTaterLiveVideo()
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.AcquireTaterH264()
	if err != nil {
		t.Fatal(err)
	}
	if taterLiveVideoUsers.Load() != 2 {
		t.Fatal("leases not shared across live codecs")
	}
	first()
	first()
	if taterLiveVideoUsers.Load() != 1 {
		t.Fatal("double release changed active exposure")
	}
	second()
	if taterLiveVideoUsers.Load() != 0 {
		t.Fatal("live exposure leaked after close")
	}
	if mid, shutter, live := taterVideoExposureLimits(); live || mid != aeMidGain || shutter != aeMaxShut {
		t.Fatal("normal still exposure not restored")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.TaterStop(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestMotionExposureFailedAcquireDoesNotLeaveLivePolicy(t *testing.T) {
	saved := nodes
	nodes = []string{t.TempDir() + "/absent-camera-node"}
	t.Cleanup(func() { nodes = saved; taterLiveVideoUsers.Store(0) })
	c := &Camera{}
	if release, err := c.AcquireTaterLiveVideo(); err == nil || release != nil {
		t.Fatal("accepted unavailable camera")
	}
	if taterLiveVideoUsers.Load() != 0 {
		t.Fatal("failed acquire leaked live exposure")
	}
}

func TestSteadySceneDoesNotSuppressExposureModeTransition(t *testing.T) {
	for _, held := range []bool{false, true} {
		if taterHoldExposure(held, true) {
			t.Fatal("stable scene suppressed mode transition")
		}
		if taterHoldExposure(held, false) != held {
			t.Fatal("changed normal exposure hold")
		}
	}
}

func TestTaterStopWaitsForReverseTeardown(t *testing.T) {
	stoppedHardware := make(chan struct{})
	c := offDevice(t, func(stop, stopped chan struct{}) {
		defer close(stopped)
		<-stop
		time.Sleep(5 * time.Millisecond)
		close(stoppedHardware)
	})
	release, err := c.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.TaterStop(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stoppedHardware:
	default:
		t.Fatal("returned before sensor teardown")
	}
}

func TestTaterSyntheticHardwareCapture(t *testing.T) {
	helper := os.Getenv("TATER_VENC_SYNTHETIC_HELPER")
	if helper == "" {
		t.Skip("opt-in synthetic hardware test")
	}
	cmd := exec.Command(helper, "--stream-bayer10", "/tmp/tater-venc-test/libMtkOmxVenc.so", "--target", "checkers", "bounded-private-buffers", "1280", "720", "30", "-")
	cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH=/data/tater-vdec/lib")
	cmd.Stderr = io.Discard
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	done := make(chan error, 1)
	started := time.Now()
	var generate, prepare, send time.Duration
	const count = 60
	go func() {
		defer input.Close()
		raw := make([]byte, frameBytes)
		var reuse []byte
		for n := 0; n < count; n++ {
			stage := time.Now()
			for i := range raw {
				raw[i] = byte(i*17 + i/97 + n*3)
			}
			generate += time.Since(stage)
			stage = time.Now()
			reuse = (&Frame{raw: raw}).TaterBayer10(reuse)
			prepare += time.Since(stage)
			if len(reuse) == 0 {
				done <- fmt.Errorf("unsupported Bayer frame")
				return
			}
			var h [16]byte
			binary.LittleEndian.PutUint32(h[:], uint32(len(reuse)))
			binary.LittleEndian.PutUint64(h[8:], uint64(n)*1000000/30)
			stage = time.Now()
			if _, err := input.Write(h[:]); err != nil {
				done <- err
				return
			}
			if _, err := input.Write(reuse); err != nil {
				done <- err
				return
			}
			send += time.Since(stage)
		}
		done <- nil
	}()
	var h [32]byte
	if _, err := io.ReadFull(output, h[:]); err != nil {
		t.Fatal(err)
	}
	if string(h[:4]) != "TVE1" {
		t.Fatalf("header %q", h[:4])
	}
	pictures, total := 0, 0
	for {
		var record [16]byte
		if _, err := io.ReadFull(output, record[:]); err != nil {
			t.Fatal(err)
		}
		size, flags := binary.LittleEndian.Uint32(record[:]), binary.LittleEndian.Uint32(record[4:])
		if size > 4*1024*1024 {
			t.Fatal("oversized output")
		}
		if _, err := io.CopyN(io.Discard, output, int64(size)); err != nil {
			t.Fatal(err)
		}
		if size > 0 && flags&2 == 0 {
			pictures++
		}
		total += int(size)
		if flags&1 != 0 {
			break
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if pictures != count {
		t.Fatalf("pictures=%d want %d", pictures, count)
	}
	fmt.Printf("SYNTHETIC_HARDWARE_CAPTURE pictures=%d bytes=%d elapsed=%s fps=%.2f\n", pictures, total, time.Since(started), float64(pictures)/time.Since(started).Seconds())
	fmt.Printf("CAPTURE_STAGES generate=%s prepare=%s send_convert_encode=%s\n", generate/count, prepare/count, send/count)
}

// Explicitly opt-in: synthetic raw Bayer only; never acquires the camera.
func TestTaterSyntheticCaptureTimings(t *testing.T) {
	if os.Getenv("TATER_CAMERA_SYNTHETIC_BENCH") != "1" {
		t.Skip("opt-in synthetic benchmark")
	}
	raw := make([]byte, frameBytes)
	for i := range raw {
		raw[i] = byte(i*17 + i/97)
	}
	const count = 30
	var develop, convert time.Duration
	var checksum uint64
	for n := 0; n < count; n++ {
		start := time.Now()
		frame := (&Frame{raw: raw}).Full()
		develop += time.Since(start)
		start = time.Now()
		for y := 0; y < frame.Rect.Dy(); y++ {
			row := frame.Pix[y*frame.Stride:]
			for x := 0; x < frame.Rect.Dx(); x++ {
				p := x * 4
				yy, cb, cr := color.RGBToYCbCr(row[p], row[p+1], row[p+2])
				checksum += uint64(yy) + uint64(cb) + uint64(cr)
			}
		}
		convert += time.Since(start)
	}
	fmt.Printf("SYNTHETIC_CAPTURE width=%d height=%d count=%d develop_avg=%s rgb_yuv_avg=%s checksum=%d\n", sensorW, sensorH, count, develop/count, convert/count, checksum)
}
