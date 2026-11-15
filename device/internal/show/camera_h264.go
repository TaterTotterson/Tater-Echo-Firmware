package show

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/liveh264"
)

func CameraH264Available(ctx context.Context, address string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if address == "" {
		address = DefaultCameraAddress
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/capabilities", nil)
	if err != nil {
		return false
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	var capabilities struct {
		H264    bool `json:"camera_h264"`
		Version int  `json:"camera_h264_version"`
		Width   int  `json:"width"`
		Height  int  `json:"height"`
		FPS     int  `json:"max_fps"`
	}
	return response.StatusCode == 200 && json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&capabilities) == nil && capabilities.H264 && capabilities.Version == 1 && qualifiedCaptureGeometry(capabilities.Width, capabilities.Height) && capabilities.FPS == 30
}

func qualifiedCaptureGeometry(width, height int) bool {
	return liveh264.QualifiedGeometry(uint32(width), uint32(height))
}

// StreamCameraH264 preserves source capture timestamps and prefixes every IDR
// with parameter sets, so a dropped predictive picture can recover at the next
// keyframe without replaying a stale video backlog.
func StreamCameraH264(ctx context.Context, address string, receive func(liveh264.Frame)) error {
	return StreamCameraH264Paced(ctx, address, 30, receive)
}

// StreamCameraH264Paced limits sensor work before encoding, preserving native
// resolution and capture timestamps. Ordinary probes still request 30 FPS.
func StreamCameraH264Paced(ctx context.Context, address string, maxFPS int, receive func(liveh264.Frame)) error {
	return StreamCameraH264Profile(ctx, address, maxFPS, "", receive)
}

func StreamCameraH264Profile(ctx context.Context, address string, maxFPS int, profile string, receive func(liveh264.Frame)) error {
	if maxFPS != 15 && maxFPS != 30 {
		return errors.New("unqualified H.264 capture cadence")
	}
	if profile != "" && profile != "balanced" {
		return errors.New("unqualified H.264 capture profile")
	}
	if address == "" {
		address = DefaultCameraAddress
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://%s/stream.h264?max_fps=%d&profile=%s", address, maxFPS, profile), nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "application/vnd.tater.h264" {
		return errors.New("qualified H.264 capture unavailable")
	}
	return readCameraH264(ctx, response.Body, receive)
}

func readCameraH264(ctx context.Context, reader io.Reader, receive func(liveh264.Frame)) error {
	var header [32]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return err
	}
	width, height := int(binary.LittleEndian.Uint32(header[8:])), int(binary.LittleEndian.Uint32(header[12:]))
	if string(header[:4]) != "TVE1" || binary.LittleEndian.Uint32(header[4:]) != 32 ||
		!qualifiedCaptureGeometry(width, height) ||
		string(header[16:20]) != "H264" || binary.LittleEndian.Uint32(header[20:]) != 30 ||
		binary.LittleEndian.Uint32(header[24:]) != 1 || binary.LittleEndian.Uint32(header[28:]) != 4*1024*1024 {
		return errors.New("invalid capture encoder header")
	}
	var epoch [8]byte
	if _, err := rand.Read(epoch[:]); err != nil {
		return err
	}
	streamID := binary.LittleEndian.Uint64(epoch[:])
	if streamID == 0 {
		streamID = 1
	}
	var config []byte
	configBurst := false
	var sequence uint32
	for ctx.Err() == nil {
		var record [16]byte
		if _, err := io.ReadFull(reader, record[:]); err != nil {
			return err
		}
		size, flags := binary.LittleEndian.Uint32(record[:]), binary.LittleEndian.Uint32(record[4:])
		if size > liveh264.MaxPayload || flags & ^uint32(7) != 0 || (size == 0 && flags&1 == 0) {
			return errors.New("invalid capture encoder packet")
		}
		data := make([]byte, int(size))
		if _, err := io.ReadFull(reader, data); err != nil {
			return err
		}
		if flags&2 != 0 {
			if !configBurst {
				config = config[:0]
			}
			configBurst = true
			if len(config)+len(data) > 64*1024 {
				return errors.New("oversized H.264 parameter sets")
			}
			config = append(config, data...)
		} else if len(data) > 0 {
			configBurst = false
			key := flags&4 != 0
			if key {
				if len(config) == 0 {
					return errors.New("H.264 keyframe has no parameter sets")
				}
				data = append(append(make([]byte, 0, len(config)+len(data)), config...), data...)
			}
			frame := liveh264.Frame{Width: uint32(width), Height: uint32(height), FPS: 30, Sequence: sequence, StreamID: streamID, PTSUS: int64(binary.LittleEndian.Uint64(record[8:])), Keyframe: key, Data: data}
			if _, err := liveh264.Decode(liveh264.Encode(frame)); err != nil {
				return fmt.Errorf("invalid capture picture: %w", err)
			}
			sequence++
			if receive != nil {
				receive(frame)
			}
		}
		if flags&1 != 0 {
			return nil
		}
	}
	return ctx.Err()
}
