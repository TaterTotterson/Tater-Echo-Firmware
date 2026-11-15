package gpucompositor

import (
	"encoding/binary"
	"errors"
	"image"
	"io"
	"math"
)

// Effects animate retained text/layout without retransmitting its pixels.
// Blend crossfades from the last completed GPU frame captured on an upload.
type Effects struct {
	Blend, Seconds, Level float32
	Red, Green, Blue      float32
	Rim, Ambient, Dots    bool
	Text                  *TextFrame
	Video                 *VideoFrame
}

const MaxTextQuads = 96

// TextFrame retains a glyph atlas and moves/fades individual words. Pixels
// are sent only when glyphs change; quads describe the current animation.
type TextFrame struct {
	Pixels        []byte
	Width, Height int
	Quads         []TextQuad
}

type TextQuad struct {
	Atlas         image.Rectangle
	X, Y, Opacity float32
}

// VideoFrame uploads a source-sized decoded picture and lets the GPU scale it
// directly into the landscape workspace. Pixels may be omitted to retain the
// previous video texture while only the floating control atlas changes.
type VideoFrame struct {
	Pixels        []byte
	Width, Height int
	Source        image.Rectangle
	Destination   image.Rectangle
	CornerRadius  int
}

func writeSceneFrame(writer io.Writer, source []byte, effects Effects) error {
	return writeSceneFrameForGeometry(writer, source, effects, defaultGeometry())
}

func writeSceneFrameForGeometry(writer io.Writer, source []byte, effects Effects, geometry Geometry) error {
	if effects.Video != nil {
		if len(source) != 0 || effects.Text == nil {
			return errors.New("invalid GPU video scene")
		}
		return writeVideoSceneFrameForGeometry(writer, effects.Video, effects.Text, geometry)
	}
	if writer == nil || (len(source) != 0 && len(source) != geometry.SourceBytes()) {
		return errors.New("invalid GPU scene source")
	}
	values := [...]float32{effects.Blend, effects.Seconds, effects.Level, effects.Red, effects.Green, effects.Blue}
	for index, value := range values {
		maximum := float32(1)
		if index == 1 {
			maximum = 1024
		}
		if !(value >= 0 && value <= maximum) {
			return errors.New("invalid GPU scene effect")
		}
	}
	var header [40]byte
	copy(header[:], "TGS2")
	binary.LittleEndian.PutUint32(header[4:], uint32(len(source)))
	for index, value := range values {
		binary.LittleEndian.PutUint32(header[8+index*4:], math.Float32bits(value))
	}
	var features uint32
	if effects.Rim {
		features |= 1
	}
	if effects.Ambient {
		features |= 2
	}
	if effects.Dots {
		features |= 4
	}
	binary.LittleEndian.PutUint32(header[32:], features)
	if effects.Text != nil {
		return writeTextSceneFrameForGeometry(writer, source, header[:], effects.Text, geometry)
	}
	if err := writeAll(writer, header[:]); err != nil {
		return err
	}
	return writeAll(writer, source)
}

func writeVideoSceneFrame(writer io.Writer, video *VideoFrame, text *TextFrame) error {
	return writeVideoSceneFrameForGeometry(writer, video, text, defaultGeometry())
}

func writeVideoSceneFrameForGeometry(writer io.Writer, video *VideoFrame, text *TextFrame, geometry Geometry) error {
	if writer == nil || video == nil || text == nil || video.Width < 16 || video.Height < 16 ||
		video.Width > 1920 || video.Height > 1088 || video.Width&1 != 0 || video.Height&1 != 0 ||
		(len(video.Pixels) != 0 && len(video.Pixels) != video.Width*video.Height*4) ||
		video.Source.Empty() || !video.Source.In(image.Rect(0, 0, video.Width, video.Height)) ||
		video.Destination.Empty() || !video.Destination.In(image.Rect(0, 0, geometry.SourceWidth, geometry.SourceHeight)) ||
		video.CornerRadius < 0 || video.CornerRadius > min(video.Destination.Dx(), video.Destination.Dy())/2 ||
		text.Width < 1 || text.Width > geometry.atlasWidth() || text.Height < 1 || text.Height > 1024 ||
		(len(text.Pixels) != 0 && len(text.Pixels) != text.Width*text.Height*4) || len(text.Quads) > MaxTextQuads {
		return errors.New("invalid GPU video scene")
	}
	quads, err := encodeTextQuads(text)
	if err != nil {
		return err
	}
	var header [72]byte
	copy(header[:], "TGV4")
	values := []int{len(video.Pixels), video.Width, video.Height,
		video.Source.Min.X, video.Source.Min.Y, video.Source.Dx(), video.Source.Dy(), video.Destination.Min.X,
		video.Destination.Min.Y, video.Destination.Dx(), video.Destination.Dy(), video.CornerRadius,
		len(text.Pixels), text.Width, text.Height, len(text.Quads)}
	for index, value := range values {
		binary.LittleEndian.PutUint32(header[4+index*4:], uint32(value))
	}
	for _, payload := range [][]byte{header[:], video.Pixels, text.Pixels, quads} {
		if err := writeAll(writer, payload); err != nil {
			return err
		}
	}
	return nil
}

func writeTextSceneFrame(writer io.Writer, source, scene []byte, text *TextFrame) error {
	return writeTextSceneFrameForGeometry(writer, source, scene, text, defaultGeometry())
}

func writeTextSceneFrameForGeometry(writer io.Writer, source, scene []byte, text *TextFrame, geometry Geometry) error {
	if text.Width < 1 || text.Width > geometry.atlasWidth() || text.Height < 1 || text.Height > 1024 ||
		(len(text.Pixels) != 0 && len(text.Pixels) != text.Width*text.Height*4) || len(text.Quads) > MaxTextQuads {
		return errors.New("invalid GPU text atlas")
	}
	quads, err := encodeTextQuads(text)
	if err != nil {
		return err
	}
	var header [56]byte
	copy(header[:], scene)
	copy(header[:4], "TGS3")
	binary.LittleEndian.PutUint32(header[36:], uint32(len(text.Pixels)))
	binary.LittleEndian.PutUint32(header[40:], uint32(text.Width))
	binary.LittleEndian.PutUint32(header[44:], uint32(text.Height))
	binary.LittleEndian.PutUint32(header[48:], uint32(len(text.Quads)))
	for _, payload := range [][]byte{header[:], source, text.Pixels, quads} {
		if err := writeAll(writer, payload); err != nil {
			return err
		}
	}
	return nil
}

func encodeTextQuads(text *TextFrame) ([]byte, error) {
	quads := make([]byte, len(text.Quads)*20)
	for index, quad := range text.Quads {
		bounds := image.Rect(0, 0, text.Width, text.Height)
		if quad.Atlas.Empty() || !quad.Atlas.In(bounds) || !(quad.X >= -1024 && quad.X <= 2048) ||
			!(quad.Y >= -1024 && quad.Y <= 2048) || !(quad.Opacity >= 0 && quad.Opacity <= 1) {
			return nil, errors.New("invalid GPU text quad")
		}
		value := quads[index*20:]
		for axis, number := range []int{quad.Atlas.Min.X, quad.Atlas.Min.Y, quad.Atlas.Dx(), quad.Atlas.Dy()} {
			binary.LittleEndian.PutUint16(value[axis*2:], uint16(number))
		}
		for axis, number := range []float32{quad.X, quad.Y, quad.Opacity} {
			binary.LittleEndian.PutUint32(value[8+axis*4:], math.Float32bits(number))
		}
	}
	return quads, nil
}

const (
	SourceWidth      = 960
	SourceHeight     = 480
	PanelWidth       = 480
	PanelHeight      = 960
	SourceBytes      = SourceWidth * SourceHeight * 4
	PanelBytes       = PanelWidth * PanelHeight * 4
	inputHeaderSize  = 8
	streamHeaderSize = 20
	frameHeaderSize  = 8
)

// Geometry is selected from a fixed target allowlist; never from untrusted
// helper output or a process-global mutable screen size.
type Geometry struct {
	SourceWidth, SourceHeight, PanelWidth, PanelHeight int
}

func defaultGeometry() Geometry { return Geometry{SourceWidth, SourceHeight, PanelWidth, PanelHeight} }

func GeometryForTarget(target string) (Geometry, bool) {
	switch target {
	case "checkers", "cronos":
		return defaultGeometry(), true
	case "crown", "athena":
		return Geometry{1280, 800, 800, 1280}, true
	case "rook":
		return Geometry{480, 480, 480, 480}, true
	default:
		return Geometry{}, false
	}
}

func (g Geometry) SourceBytes() int { return g.SourceWidth * g.SourceHeight * 4 }
func (g Geometry) PanelBytes() int  { return g.PanelWidth * g.PanelHeight * 4 }
func (g Geometry) atlasWidth() int {
	if g.SourceWidth > 1024 {
		return 2048
	}
	return 1024
}

func writeAll(writer io.Writer, value []byte) error {
	for len(value) > 0 {
		written, err := writer.Write(value)
		if written > 0 {
			value = value[written:]
		}
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func writeInputFrame(writer io.Writer, source []byte) error {
	return writeInputFrameForGeometry(writer, source, defaultGeometry())
}

func writeInputFrameForGeometry(writer io.Writer, source []byte, geometry Geometry) error {
	if writer == nil || len(source) != geometry.SourceBytes() {
		return errors.New("invalid GPU compositor source frame")
	}
	header := make([]byte, inputHeaderSize)
	copy(header, "TGI1")
	binary.LittleEndian.PutUint32(header[4:], uint32(geometry.SourceBytes()))
	if err := writeAll(writer, header); err != nil {
		return err
	}
	return writeAll(writer, source)
}

func readStreamHeader(reader io.Reader) error {
	return readStreamHeaderForGeometry(reader, defaultGeometry())
}

func readStreamHeaderForGeometry(reader io.Reader, geometry Geometry) error {
	value := make([]byte, streamHeaderSize)
	if reader == nil {
		return errors.New("GPU compositor output is unavailable")
	}
	if _, err := io.ReadFull(reader, value); err != nil {
		return err
	}
	if string(value[:4]) != "TGO1" ||
		binary.LittleEndian.Uint32(value[4:8]) != uint32(geometry.PanelWidth) ||
		binary.LittleEndian.Uint32(value[8:12]) != uint32(geometry.PanelHeight) ||
		binary.LittleEndian.Uint32(value[12:16]) != uint32(geometry.PanelWidth*4) ||
		binary.LittleEndian.Uint32(value[16:20]) != uint32(geometry.PanelBytes()) {
		return errors.New("invalid GPU compositor stream contract")
	}
	return nil
}

func readOutputFrame(reader io.Reader, destination []byte) error {
	return readOutputFrameForGeometry(reader, destination, defaultGeometry())
}

func readOutputFrameForGeometry(reader io.Reader, destination []byte, geometry Geometry) error {
	header := make([]byte, frameHeaderSize)
	if reader == nil || len(destination) != geometry.PanelBytes() {
		return errors.New("invalid GPU compositor destination frame")
	}
	if _, err := io.ReadFull(reader, header); err != nil {
		return err
	}
	if string(header[:4]) != "TGF1" || binary.LittleEndian.Uint32(header[4:]) != uint32(geometry.PanelBytes()) {
		return errors.New("invalid GPU compositor frame contract")
	}
	_, err := io.ReadFull(reader, destination)
	return err
}
