package videostream

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

const frameCRCFixture = `#extradata 0:       39, 0xa5ef09b3
#software: Lavf63.1.102
#tb 0: 1/15360
#media_type 0: video
#codec_id 0: h264
#dimensions 0: 960x480
#sar 0: 1/1
0,      -1024,          0,      512,    12747, 0x687c6ed1
0,       -512,       1024,      512,     6744, 0xf8e72b07, F=0x0
0,          0,        512,      512,     4564, 0xd332ed1c, F=0x0
`

func TestFrameCRCMetadataPreservesBFramePresentationTimestamps(t *testing.T) {
	var got []h264PacketMetadata
	err := readFrameCRCMetadata(strings.NewReader(frameCRCFixture), 960, 480, func(metadata h264PacketMetadata) error {
		got = append(got, metadata)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []h264PacketMetadata{
		{PTSUS: 0, Bytes: 12747},
		{PTSUS: 66_666, Bytes: 6744},
		{PTSUS: 33_333, Bytes: 4564},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("metadata = %#v, want %#v", got, want)
	}
}

func TestFrameCRCMetadataRejectsGeometryAndMissingHeaders(t *testing.T) {
	if err := readFrameCRCMetadata(strings.NewReader(frameCRCFixture), 1280, 720, func(h264PacketMetadata) error { return nil }); err == nil || !strings.Contains(err.Error(), "geometry") {
		t.Fatalf("geometry error = %v", err)
	}
	broken := "#tb 0: 1/30\n0, 0, 0, 1, 5, 0x0\n"
	if err := readFrameCRCMetadata(strings.NewReader(broken), 16, 16, func(h264PacketMetadata) error { return nil }); err == nil {
		t.Fatal("metadata without its semantic headers was accepted")
	}
}

func TestFrameCRCMetadataCanAcceptBoundedSourceGeometry(t *testing.T) {
	var packets int
	err := readFrameCRCMetadata(strings.NewReader(frameCRCFixture), 0, 0, func(h264PacketMetadata) error {
		packets++
		return nil
	})
	if err != nil || packets != 3 {
		t.Fatalf("unconstrained source geometry packets=%d err=%v", packets, err)
	}
	if err := readFrameCRCMetadata(strings.NewReader(frameCRCFixture), 0, 480, func(h264PacketMetadata) error { return nil }); err == nil {
		t.Fatal("half-constrained source geometry was accepted")
	}
}

func TestFrameCRCMetadataPropagatesReceiverFailure(t *testing.T) {
	want := errors.New("stop")
	err := readFrameCRCMetadata(strings.NewReader(frameCRCFixture), 960, 480, func(h264PacketMetadata) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestAsyncBoundedReaderPreservesBytes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := bytes.Repeat([]byte("timestamped-h264"), 10_000)
	got, err := io.ReadAll(newAsyncBoundedReader(ctx, bytes.NewReader(source)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, source) {
		t.Fatal("asynchronous reader changed the stream")
	}
}
