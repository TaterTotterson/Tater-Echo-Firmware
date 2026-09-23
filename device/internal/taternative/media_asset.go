package taternative

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/hajimehoshi/go-mp3"
)

const (
	mediaPrebufferFrames = playbackRate * 2
	maxDecodedMediaBytes = 256 * 1024 * 1024
)

// mediaAsset is an append-only PCM spool. The network decoder writes small
// blocks to disk while playback reads already prepared frames with ReadAt.
// Only the current block and the speaker queue live in RAM, so a long MP3 no
// longer expands into a song-sized []byte on this memory-constrained device.
type mediaAsset struct {
	file   *os.File
	path   string
	cancel context.CancelFunc
	done   chan struct{}

	mu     sync.Mutex
	frames int64
	err    error
	notify chan struct{}
	closed bool
	once   sync.Once
}

func newMediaAsset(parent context.Context, directory string) (*mediaAsset, context.Context, error) {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		fallback := filepath.Join(os.TempDir(), "tater-echo-media")
		if fallbackErr := os.MkdirAll(fallback, 0o755); fallbackErr != nil {
			return nil, nil, fmt.Errorf("create media cache: %w", err)
		}
		directory = fallback
	}
	file, err := os.CreateTemp(directory, ".media-*.pcm")
	if err != nil {
		return nil, nil, fmt.Errorf("create media spool: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	return &mediaAsset{
		file: file, path: file.Name(), cancel: cancel, done: make(chan struct{}),
		notify: make(chan struct{}),
	}, ctx, nil
}

func (a *mediaAsset) publish(frames int64, err error, done bool) {
	a.mu.Lock()
	if frames > a.frames {
		a.frames = frames
	}
	if err != nil && a.err == nil && !errors.Is(err, context.Canceled) {
		a.err = err
	}
	close(a.notify)
	a.notify = make(chan struct{})
	a.mu.Unlock()
	if done {
		close(a.done)
	}
}

func (a *mediaAsset) snapshot() (frames int64, done bool, err error, notify <-chan struct{}) {
	a.mu.Lock()
	defer a.mu.Unlock()
	select {
	case <-a.done:
		done = true
	default:
	}
	return a.frames, done, a.err, a.notify
}

func (a *mediaAsset) waitFrames(ctx context.Context, minimum int64) (int64, error) {
	for {
		frames, done, err, notify := a.snapshot()
		if frames >= minimum || done {
			if frames == 0 && err != nil {
				return 0, err
			}
			return frames, nil
		}
		select {
		case <-ctx.Done():
			return frames, ctx.Err()
		case <-notify:
		}
	}
}

// readFrames waits for a complete decoder block unless the source has ended.
// waited is true only when playback caught the decoder, which is the actual
// rebuffer condition reported to Tater (not ordinary disk reads).
func (a *mediaAsset) readFrames(
	ctx context.Context,
	cursor *int64,
	count int,
	loop bool,
) (samples []int16, ended bool, waited bool, err error) {
	if count <= 0 {
		return nil, false, false, nil
	}
	for {
		frames, done, decodeErr, notify := a.snapshot()
		if *cursor >= frames {
			if done {
				if decodeErr != nil {
					return nil, true, waited, decodeErr
				}
				if loop && frames > 0 {
					*cursor = 0
					continue
				}
				return nil, true, waited, nil
			}
			waited = true
			select {
			case <-ctx.Done():
				return nil, false, waited, ctx.Err()
			case <-notify:
				continue
			}
		}

		available := frames - *cursor
		if available < int64(count) && !done {
			waited = true
			select {
			case <-ctx.Done():
				return nil, false, waited, ctx.Err()
			case <-notify:
				continue
			}
		}
		readCount := int64(count)
		if available < readCount {
			readCount = available
		}
		raw := make([]byte, int(readCount)*2)
		n, readErr := a.file.ReadAt(raw, *cursor*2)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, false, waited, readErr
		}
		n -= n % 2
		raw = raw[:n]
		samples = make([]int16, len(raw)/2)
		for index := range samples {
			samples[index] = int16(binary.LittleEndian.Uint16(raw[index*2:]))
		}
		*cursor += int64(len(samples))
		return samples, done && *cursor >= frames, waited, nil
	}
}

func (a *mediaAsset) Close() {
	a.once.Do(func() {
		a.cancel()
		<-a.done
		a.mu.Lock()
		a.closed = true
		a.mu.Unlock()
		_ = a.file.Close()
		_ = os.Remove(a.path)
	})
}

type boundedReader struct {
	reader io.Reader
	read   int64
	limit  int64
}

func (r *boundedReader) Read(p []byte) (int, error) {
	remaining := r.limit + 1 - r.read
	if remaining <= 0 {
		return 0, fmt.Errorf("audio exceeds %d bytes", r.limit)
	}
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := r.reader.Read(p)
	r.read += int64(n)
	if r.read > r.limit {
		return n, fmt.Errorf("audio exceeds %d bytes", r.limit)
	}
	return n, err
}

func (p *LocalPlayer) startMediaAsset(ctx context.Context, rawURL, channel string) (*mediaAsset, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create audio request: %w", err)
	}
	response, err := p.HTTP.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download audio: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, fmt.Errorf("download audio: HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maxAudioDownload {
		response.Body.Close()
		return nil, fmt.Errorf("audio exceeds %d bytes", maxAudioDownload)
	}
	asset, assetCtx, err := newMediaAsset(ctx, p.mediaCacheDir)
	if err != nil {
		response.Body.Close()
		return nil, err
	}
	contentType := strings.ToLower(strings.Split(response.Header.Get("Content-Type"), ";")[0])
	go func() {
		defer response.Body.Close()
		reader := bufio.NewReader(&boundedReader{reader: response.Body, limit: maxAudioDownload})
		decodeErr := decodeMediaStream(assetCtx, asset, reader, contentType, rawURL, channel)
		frames, _, _, _ := asset.snapshot()
		asset.publish(frames, decodeErr, true)
	}()
	return asset, nil
}

func decodeMediaStream(
	ctx context.Context,
	asset *mediaAsset,
	reader *bufio.Reader,
	contentType, rawURL, channel string,
) error {
	header, _ := reader.Peek(12)
	if len(header) >= 12 && string(header[:4]) == "RIFF" && string(header[8:12]) == "WAVE" {
		return decodeWAVStream(ctx, asset, reader, channel)
	}
	extension := ""
	if parsed, err := url.Parse(rawURL); err == nil {
		extension = strings.ToLower(filepath.Ext(parsed.Path))
	}
	if contentType == "audio/mpeg" || contentType == "audio/mp3" || extension == ".mp3" || (len(header) >= 3 && string(header[:3]) == "ID3") {
		decoder, err := mp3.NewDecoder(reader)
		if err != nil {
			return fmt.Errorf("decode MP3: %w", err)
		}
		return convertPCMStream(ctx, asset, decoder, 2, decoder.SampleRate(), channel, -1)
	}
	return fmt.Errorf("unsupported audio type %q", contentType)
}

func decodeWAVStream(ctx context.Context, asset *mediaAsset, reader *bufio.Reader, channel string) error {
	header := make([]byte, 12)
	if _, err := io.ReadFull(reader, header); err != nil {
		return fmt.Errorf("read WAV header: %w", err)
	}
	var format, channels, rate, bits int
	for {
		chunkHeader := make([]byte, 8)
		if _, err := io.ReadFull(reader, chunkHeader); err != nil {
			return fmt.Errorf("read WAV chunk: %w", err)
		}
		name := string(chunkHeader[:4])
		size := int64(binary.LittleEndian.Uint32(chunkHeader[4:]))
		if size < 0 || size > maxAudioDownload {
			return errors.New("invalid WAV chunk length")
		}
		if name == "fmt " {
			chunk := make([]byte, size)
			if _, err := io.ReadFull(reader, chunk); err != nil {
				return fmt.Errorf("read WAV fmt chunk: %w", err)
			}
			if len(chunk) < 16 {
				return errors.New("short WAV fmt chunk")
			}
			format = int(binary.LittleEndian.Uint16(chunk[0:2]))
			channels = int(binary.LittleEndian.Uint16(chunk[2:4]))
			rate = int(binary.LittleEndian.Uint32(chunk[4:8]))
			bits = int(binary.LittleEndian.Uint16(chunk[14:16]))
		} else if name == "data" {
			if format != 1 || bits != 16 || channels < 1 || channels > 8 || rate < 8000 || rate > 192000 {
				return fmt.Errorf("unsupported WAV format=%d channels=%d rate=%d bits=%d", format, channels, rate, bits)
			}
			return convertPCMStream(ctx, asset, io.LimitReader(reader, size), channels, rate, channel, size)
		} else {
			if _, err := io.CopyN(io.Discard, reader, size); err != nil {
				return fmt.Errorf("skip WAV chunk: %w", err)
			}
		}
		if size%2 != 0 {
			if _, err := reader.ReadByte(); err != nil {
				return fmt.Errorf("read WAV padding: %w", err)
			}
		}
	}
}

type streamingResampler struct {
	asset      *mediaAsset
	sourceRate int64
	targetRate int64
	nextPos    int64
	inputCount int64
	output     []byte
	frames     int64
	last       int16
	haveLast   bool
}

func (r *streamingResampler) add(sample int16) error {
	index := r.inputCount
	r.inputCount++
	if !r.haveLast {
		r.last = sample
		r.haveLast = true
		return r.emit(sample)
	}
	boundary := index * r.targetRate
	for r.nextPos <= boundary {
		leftBoundary := (index - 1) * r.targetRate
		fraction := float64(r.nextPos-leftBoundary) / float64(r.targetRate)
		if fraction < 0 {
			fraction = 0
		} else if fraction > 1 {
			fraction = 1
		}
		value := float64(r.last) + fraction*float64(int(sample)-int(r.last))
		if err := r.emit(int16(math.Round(value))); err != nil {
			return err
		}
		r.nextPos += r.sourceRate
	}
	r.last = sample
	return nil
}

func (r *streamingResampler) emit(sample int16) error {
	// The first source sample represents output position zero. Move the next
	// requested position only after writing it.
	if r.frames == 0 && r.nextPos == 0 {
		r.nextPos = r.sourceRate
	}
	r.output = binary.LittleEndian.AppendUint16(r.output, uint16(sample))
	r.frames++
	if len(r.output) >= 32*1024 {
		return r.flush()
	}
	return nil
}

func (r *streamingResampler) finish() error {
	if !r.haveLast {
		return errors.New("decoded audio is empty")
	}
	targetCount := (r.inputCount*r.targetRate + r.sourceRate - 1) / r.sourceRate
	for r.frames < targetCount {
		if err := r.emit(r.last); err != nil {
			return err
		}
	}
	return r.flush()
}

func (r *streamingResampler) flush() error {
	if len(r.output) == 0 {
		return nil
	}
	if r.frames*2 > maxDecodedMediaBytes {
		return fmt.Errorf("decoded audio exceeds %d bytes", maxDecodedMediaBytes)
	}
	offset := (r.frames * 2) - int64(len(r.output))
	if _, err := r.asset.file.WriteAt(r.output, offset); err != nil {
		return fmt.Errorf("write media spool: %w", err)
	}
	r.output = r.output[:0]
	r.asset.publish(r.frames, nil, false)
	return nil
}

func convertPCMStream(
	ctx context.Context,
	asset *mediaAsset,
	reader io.Reader,
	channels, sourceRate int,
	channel string,
	expectedBytes int64,
) error {
	if channels < 1 || sourceRate <= 0 {
		return errors.New("invalid PCM stream format")
	}
	resampler := &streamingResampler{
		asset: asset, sourceRate: int64(sourceRate), targetRate: playbackRate,
		output: make([]byte, 0, 32*1024),
	}
	frameBytes := channels * 2
	buffer := make([]byte, 32*1024+frameBytes)
	pending := 0
	var consumed int64
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		n, readErr := reader.Read(buffer[pending:])
		consumed += int64(n)
		total := pending + n
		usable := total - total%frameBytes
		for offset := 0; offset < usable; offset += frameBytes {
			var sample int16
			if channel == "left" || channel == "right" {
				selected := 0
				if channel == "right" && channels > 1 {
					selected = 1
				}
				sample = int16(binary.LittleEndian.Uint16(buffer[offset+selected*2:]))
			} else {
				var sum int64
				for index := 0; index < channels; index++ {
					sum += int64(int16(binary.LittleEndian.Uint16(buffer[offset+index*2:])))
				}
				sample = int16(sum / int64(channels))
			}
			if err := resampler.add(sample); err != nil {
				return err
			}
		}
		pending = copy(buffer, buffer[usable:total])
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return fmt.Errorf("decode audio: %w", readErr)
			}
			break
		}
	}
	if expectedBytes >= 0 && consumed < expectedBytes {
		return io.ErrUnexpectedEOF
	}
	if pending != 0 {
		return errors.New("decoded PCM ended mid-frame")
	}
	return resampler.finish()
}

// resampleMediaBlock consumes a very small source-rate difference into one
// fixed output block. correction > 0 advances the source (catch up), while a
// negative value consumes fewer source frames (fall back), both without a
// dropped/repeated discontinuity.
func resampleMediaBlock(input []int16, outputFrames int) []byte {
	if len(input) == 0 || outputFrames <= 0 {
		return nil
	}
	out := make([]byte, outputFrames*2)
	if len(input) == 1 {
		for index := 0; index < outputFrames; index++ {
			binary.LittleEndian.PutUint16(out[index*2:], uint16(input[0]))
		}
		return out
	}
	for index := 0; index < outputFrames; index++ {
		position := float64(index) * float64(len(input)-1) / float64(maxInt(outputFrames-1, 1))
		left := int(position)
		right := left + 1
		if right >= len(input) {
			right = len(input) - 1
		}
		fraction := position - float64(left)
		value := float64(input[left]) + fraction*float64(int(input[right])-int(input[left]))
		binary.LittleEndian.PutUint16(out[index*2:], uint16(int16(math.Round(value))))
	}
	return out
}

type mediaSlew struct {
	pending       int64
	interval      int64
	untilNextStep int64
}

func (s *mediaSlew) replace(correction int64, settleFrames int64) {
	s.pending = correction
	if correction == 0 {
		s.interval = 0
		s.untilNextStep = 0
		return
	}
	abs := correction
	if abs < 0 {
		abs = -abs
	}
	if settleFrames <= 0 {
		settleFrames = playbackRate
	}
	s.interval = maxInt64(1, settleFrames/abs)
	s.untilNextStep = s.interval
}

func (s *mediaSlew) next(outputFrames int64) int64 {
	if s.pending == 0 || outputFrames <= 0 {
		return 0
	}
	var result int64
	remaining := outputFrames
	for s.pending != 0 && remaining >= s.untilNextStep {
		remaining -= s.untilNextStep
		if s.pending > 0 {
			result++
			s.pending--
		} else {
			result--
			s.pending++
		}
		s.untilNextStep = s.interval
	}
	if s.pending != 0 {
		s.untilNextStep -= remaining
	}
	return result
}
