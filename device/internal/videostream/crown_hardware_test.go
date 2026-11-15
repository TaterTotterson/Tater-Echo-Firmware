//go:build linux

package videostream

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"image"
	"io"
	"os"
	"testing"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/liveh264"
)

func TestCrownDecoderRequiresItsOwnManifest(t *testing.T) {
	t.Setenv("TATER_VDEC_ENABLE", "1")
	t.Setenv("TATER_FIRMWARE_TARGET", "crown")
	t.Setenv("TATER_VDEC_LIBRARY_PATH", "")
	cfg, ok := hardwareDecoderConfigFromEnvironment()
	if !ok || cfg.libraryPath != "/data/tater-vdec/lib" {
		t.Fatal(cfg, ok)
	}
	manifest := vdecRuntimeManifest{Schema: 2, RuntimeID: "crown-mt8163-vdec-stock-1", TreeSHA256: cronosVDECTreeSHA256, FileCount: 66, RuntimeBytes: 41926684}
	data, _ := json.Marshal(manifest)
	if err := validateVDECRuntimeManifestForTarget(data, "crown"); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"checkers", "cronos", "athena", "rook"} {
		if validateVDECRuntimeManifestForTarget(data, target) == nil {
			t.Fatal("cross-target manifest accepted", target)
		}
	}
}

// Owner-local generated frame IDs only. No sensor, audio or framebuffer access.
func TestCrownHardwareGeneratedFrameOrder(t *testing.T) {
	path := os.Getenv("TATER_CROWN_GENERATED_VDI2")
	testMT8163GeneratedFrameOrder(t, "Crown", path)
}

func TestRookHardwareGeneratedFrameOrder(t *testing.T) {
	testMT8163GeneratedFrameOrder(t, "Rook", os.Getenv("TATER_ROOK_GENERATED_VDI2"))
}

func testMT8163GeneratedFrameOrder(t *testing.T, target, path string) {
	t.Helper()
	if path == "" {
		t.Skip("owner-local synthetic fixture required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if !HardwareH264Available(ctx) {
		t.Fatal("Crown hardware decoder preflight failed")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, writer := io.Pipe()
	defer reader.Close()
	go func() {
		defer writer.Close()
		ticker := time.NewTicker(time.Second / 15)
		defer ticker.Stop()
		for seq := uint32(0); seq < 120; seq++ {
			var header [12]byte
			if _, err := io.ReadFull(file, header[:]); err != nil {
				writer.CloseWithError(err)
				return
			}
			size := binary.LittleEndian.Uint32(header[:])
			if size < 5 || size > liveh264.MaxPayload {
				writer.CloseWithError(io.ErrUnexpectedEOF)
				return
			}
			data := make([]byte, size)
			if _, err := io.ReadFull(file, data); err != nil {
				writer.CloseWithError(err)
				return
			}
			frame := liveh264.Frame{Width: 640, Height: 480, FPS: 30, Sequence: seq, StreamID: 1, PTSUS: int64(binary.LittleEndian.Uint64(header[4:])), Keyframe: seq%15 == 0, Data: data}
			select {
			case <-ctx.Done():
				writer.CloseWithError(ctx.Err())
				return
			case <-ticker.C:
			}
			if err := liveh264.Write(writer, liveh264.Encode(frame)); err != nil {
				return
			}
		}
	}()
	count := 0
	err = ReadH264Packets(ctx, reader, func(frame *image.RGBA) error {
		for _, y := range []int{80, 240, 400} {
			marker := 0
			for bit := 0; bit < 8; bit++ {
				if frame.Pix[frame.PixOffset(bit*80+40, y)] > 128 {
					marker |= 1 << bit
				}
			}
			if marker != count {
				t.Errorf("backward/out-of-order generated frame: got %d want %d row %d", marker, count, y)
			}
		}
		count++
		return nil
	})
	if err != nil || count != 120 {
		t.Fatalf("hardware frames=%d error=%v", count, err)
	}
	t.Logf("120 ordered generated pictures through %s's production hardware receive path", target)
}
