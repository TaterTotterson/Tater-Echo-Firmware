package videostream

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

const (
	frameCRCMaximumLineBytes = 4096
	h264AsyncChunkBytes      = 64 * 1024
	h264AsyncChunkCount      = vdecMaximumAccessUnitBytes / h264AsyncChunkBytes
)

type h264PacketMetadata struct {
	PTSUS int64
	Bytes int
}

type frameCRCParser struct {
	timeBaseNumerator, timeBaseDenominator int64
	width, height                          int
	hasTimeBase, hasMedia, hasCodec        bool
	hasDimensions, hasFirstPTS             bool
	firstPTS                               int64
	records                                int
}

func frameCRCHeaderValue(line, name string) (string, bool) {
	prefix := "#" + name + " 0:"
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(line, prefix)), true
}

func (parser *frameCRCParser) parseHeader(line string) error {
	if value, ok := frameCRCHeaderValue(line, "tb"); ok {
		parts := strings.Split(value, "/")
		if len(parts) != 2 {
			return errors.New("invalid framecrc time base")
		}
		numerator, numeratorErr := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
		denominator, denominatorErr := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if numeratorErr != nil || denominatorErr != nil || numerator <= 0 || denominator <= 0 ||
			numerator > 1_000_000 || denominator > 1_000_000_000 {
			return errors.New("invalid framecrc time base")
		}
		parser.timeBaseNumerator, parser.timeBaseDenominator = numerator, denominator
		parser.hasTimeBase = true
		return nil
	}
	if value, ok := frameCRCHeaderValue(line, "media_type"); ok {
		if value != "video" {
			return errors.New("framecrc metadata is not video")
		}
		parser.hasMedia = true
		return nil
	}
	if value, ok := frameCRCHeaderValue(line, "codec_id"); ok {
		if value != "h264" {
			return errors.New("framecrc metadata is not H.264")
		}
		parser.hasCodec = true
		return nil
	}
	if value, ok := frameCRCHeaderValue(line, "dimensions"); ok {
		parts := strings.Split(value, "x")
		if len(parts) != 2 {
			return errors.New("invalid framecrc dimensions")
		}
		width, widthErr := strconv.Atoi(strings.TrimSpace(parts[0]))
		height, heightErr := strconv.Atoi(strings.TrimSpace(parts[1]))
		if widthErr != nil || heightErr != nil || width < 16 || height < 16 ||
			width > vdecMaximumWidth || height > vdecMaximumHeight || width&1 != 0 || height&1 != 0 {
			return errors.New("invalid framecrc dimensions")
		}
		parser.width, parser.height = width, height
		parser.hasDimensions = true
	}
	// Software identity, extradata size, and sample-aspect-ratio comments are
	// informational. Required semantic headers are checked before any record.
	return nil
}

func (parser *frameCRCParser) parseRecord(line string, expectedWidth, expectedHeight int) (h264PacketMetadata, error) {
	var result h264PacketMetadata
	if !parser.hasTimeBase || !parser.hasMedia || !parser.hasCodec || !parser.hasDimensions {
		return result, errors.New("framecrc packet arrived before its required headers")
	}
	if expectedWidth > 0 && expectedHeight > 0 &&
		(parser.width != expectedWidth || parser.height != expectedHeight) {
		return result, fmt.Errorf("H.264 source is %dx%d, not display geometry %dx%d", parser.width, parser.height, expectedWidth, expectedHeight)
	}
	fields := strings.Split(line, ",")
	if len(fields) < 6 {
		return result, errors.New("invalid framecrc packet record")
	}
	streamIndex, streamErr := strconv.Atoi(strings.TrimSpace(fields[0]))
	pts, ptsErr := strconv.ParseInt(strings.TrimSpace(fields[2]), 10, 64)
	packetBytes, bytesErr := strconv.Atoi(strings.TrimSpace(fields[4]))
	if streamErr != nil || streamIndex != 0 || ptsErr != nil || bytesErr != nil ||
		packetBytes <= 0 || packetBytes > vdecMaximumAccessUnitBytes {
		return result, errors.New("invalid framecrc packet record")
	}
	if !parser.hasFirstPTS {
		parser.firstPTS = pts
		parser.hasFirstPTS = true
	}
	delta := pts - parser.firstPTS
	if delta < 0 || parser.timeBaseNumerator > math.MaxInt64/1_000_000 {
		return result, errors.New("framecrc timestamp is outside its boundary")
	}
	scale := parser.timeBaseNumerator * 1_000_000
	if delta > math.MaxInt64/scale {
		return result, errors.New("framecrc timestamp is outside its boundary")
	}
	result = h264PacketMetadata{
		PTSUS: delta * scale / parser.timeBaseDenominator,
		Bytes: packetBytes,
	}
	parser.records++
	return result, nil
}

func readFrameCRCMetadata(reader io.Reader, expectedWidth, expectedHeight int, receive func(h264PacketMetadata) error) error {
	geometryValid := expectedWidth > 0 && expectedHeight > 0
	geometryUnconstrained := expectedWidth == 0 && expectedHeight == 0
	if reader == nil || receive == nil || (!geometryValid && !geometryUnconstrained) {
		return errors.New("framecrc metadata reader is unavailable")
	}
	parser := &frameCRCParser{}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 1024), frameCRCMaximumLineBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			if err := parser.parseHeader(line); err != nil {
				return err
			}
			continue
		}
		metadata, err := parser.parseRecord(line, expectedWidth, expectedHeight)
		if err != nil {
			return err
		}
		if err := receive(metadata); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if parser.records == 0 {
		return errors.New("framecrc metadata contained no H.264 packets")
	}
	return nil
}

type asyncReadChunk struct {
	value []byte
	err   error
}

// asyncBoundedReader drains FFmpeg's raw H.264 pipe concurrently with its
// metadata pipe. FFmpeg may write either output first; retaining at most one
// maximum-sized access unit prevents an output-order pipe deadlock without
// permitting unbounded buffering.
type asyncBoundedReader struct {
	chunks  <-chan asyncReadChunk
	current []byte
	ending  error
}

func newAsyncBoundedReader(ctx context.Context, source io.Reader) *asyncBoundedReader {
	chunks := make(chan asyncReadChunk, h264AsyncChunkCount)
	go func() {
		defer close(chunks)
		for {
			buffer := make([]byte, h264AsyncChunkBytes)
			count, err := source.Read(buffer)
			if count > 0 {
				select {
				case chunks <- asyncReadChunk{value: buffer[:count]}:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				select {
				case chunks <- asyncReadChunk{err: err}:
				case <-ctx.Done():
				}
				return
			}
			if count == 0 {
				select {
				case chunks <- asyncReadChunk{err: io.ErrNoProgress}:
				case <-ctx.Done():
				}
				return
			}
		}
	}()
	return &asyncBoundedReader{chunks: chunks}
}

func (reader *asyncBoundedReader) Read(destination []byte) (int, error) {
	for len(reader.current) == 0 {
		if reader.ending != nil {
			return 0, reader.ending
		}
		chunk, ok := <-reader.chunks
		if !ok {
			return 0, io.EOF
		}
		reader.current, reader.ending = chunk.value, chunk.err
	}
	count := copy(destination, reader.current)
	reader.current = reader.current[count:]
	return count, nil
}
