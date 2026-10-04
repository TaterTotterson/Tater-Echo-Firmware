//go:build linux

package linuxinput

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"testing"
)

func TestRookProtocolATouchUsesNativeCoordinates(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "rook-touch-")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	write := func(kind, code uint16, value int32) {
		buffer := make([]byte, inputEventBytes)
		binary.LittleEndian.PutUint16(buffer[inputEventType:], kind)
		binary.LittleEndian.PutUint16(buffer[inputEventCode:], code)
		binary.LittleEndian.PutUint32(buffer[inputEventValue:], uint32(value))
		if _, err := file.Write(buffer); err != nil {
			t.Fatal(err)
		}
	}
	write(evAbs, absMTPositionX, 120)
	write(evAbs, absMTPositionY, 180)
	write(evKey, btnTouch, 1)
	write(evSyn, synReport, 0)
	write(evAbs, absMTPositionX, 130)
	write(evAbs, absMTPositionY, 190)
	write(evSyn, synReport, 0)
	write(evKey, btnTouch, 0)
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	device := &Device{file: file, path: file.Name(), rawWidth: 480, rawHeight: 480, protocolA: true}
	events := make(chan TouchEvent, 4)
	if err := device.runProtocolA(context.Background(), events); !errors.Is(err, io.EOF) {
		t.Fatalf("reading recorded touch = %v, want EOF", err)
	}
	want := []TouchEvent{{Kind: Down, X: 120, Y: 180}, {Kind: Move, X: 130, Y: 190}, {Kind: Up, X: 130, Y: 190}}
	for index, expected := range want {
		select {
		case got := <-events:
			if got != expected {
				t.Errorf("event %d = %+v, want %+v", index, got, expected)
			}
		default:
			t.Fatalf("missing event %d", index)
		}
	}
}

func TestCheckersTouchStillRotates(t *testing.T) {
	device := &Device{rawWidth: 480, rawHeight: 960}
	x, y := device.frame(&position{x: 120, y: 180, seenX: true, seenY: true})
	if x != 180 || y != 359 {
		t.Fatalf("Checkers touch = (%d,%d), want (180,359)", x, y)
	}
}
