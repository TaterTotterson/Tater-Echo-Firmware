//go:build linux

package videostream

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/liveh264"
)

func HardwareH264Available(ctx context.Context) bool {
	return CheckHardwareH264(ctx) == nil
}

// CheckHardwareH264 explains a local decoder preflight failure without opening
// a codec session or including any remote URL or authentication material.
func CheckHardwareH264(ctx context.Context) error {
	config, enabled := hardwareDecoderConfigFromEnvironment()
	if !enabled {
		return errors.New("hardware H.264 decoder is not enabled for this target")
	}
	if err := validateVDECRuntime(config); err != nil {
		return err
	}
	// Legacy owner-local helpers remain valid for ordinary media playback,
	// but cannot keep up with simultaneous 720p capture. Do not negotiate a
	// live call until the exact sustained-test-qualified converter is present.
	expected := neonVDECHelperSHA256
	if config.target == "cronos" {
		expected = cronosVDECHelperSHA256
	} else if config.target == "crown" {
		expected = crownVDECHelperSHA256
	} else if config.target == "rook" {
		expected = rookVDECHelperSHA256
	} else if config.target == "athena" {
		expected = athenaVDECHelperSHA256
		if !deviceNodeExists("/dev/tater-athena-ap-vdec") {
			return errors.New("Athena AP decoder device node is unavailable")
		}
	}
	if hash, err := fileSHA256(config.helper); err != nil || hash != expected {
		return errors.New("hardware H.264 live helper hash is not qualified")
	}
	check, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	capabilities, err := boundedCommandOutput(check, config.helper, config.environment(), nil, []string{"--capabilities"}...)
	if err != nil {
		return fmt.Errorf("hardware H.264 helper capability check failed: %w", err)
	}
	return validateVDECCapabilitiesForTarget(capabilities, config.target)
}

// External media deliberately rejects loopback. Sat Chat uses the opposite
// trust boundary: only this locally generated relay endpoint is permitted.
func satChatH264URL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || len(raw) > maximumURLBytes || parsed.Scheme != "http" || parsed.Opaque != "" ||
		parsed.User != nil || parsed.Fragment != "" || parsed.Path != "/sat-chat-h264" || parsed.RawPath != "" ||
		(parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "::1") {
		return nil, errors.New("H.264 call stream must be loopback-only")
	}
	port, err := strconv.ParseUint(parsed.Port(), 10, 16)
	epoch, epochErr := strconv.ParseUint(parsed.Query().Get("stream"), 10, 64)
	if err != nil || port == 0 || epochErr != nil || epoch == 0 || len(parsed.Query()) != 1 {
		return nil, errors.New("invalid H.264 loopback endpoint")
	}
	return parsed, nil
}

func ReadSatChatH264(ctx context.Context, rawURL string, receive func(*image.RGBA) error) error {
	parsed, err := satChatH264URL(rawURL)
	if err != nil {
		return errors.New("H.264 call stream must be loopback-only")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return err
	}
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
		ResponseHeaderTimeout: 2 * time.Second,
		DisableKeepAlives:     true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "application/vnd.tater.sat-chat.h264" {
		return errors.New("invalid H.264 call stream response")
	}
	return ReadH264Packets(ctx, response.Body, receive)
}

// Let OMX release its surfaces and codec state normally before reopening it.
// Forced process death is the bounded fallback, not the normal reset lifecycle.
func stopLiveH264Decoder(ctx context.Context, helper *hardwareHelper, cancel context.CancelFunc, done <-chan hardwareOutputResult) {
	timeout := time.AfterFunc(1500*time.Millisecond, func() {
		cancel()
		helper.input.Close()
		helper.output.Close()
	})
	if ctx.Err() == nil {
		_ = writeVDECInputEOS(helper.input)
	}
	helper.input.Close()
	<-done
	_ = helper.command.Wait()
	timeout.Stop()
	cancel()
	helper.output.Close()
}

// ReadH264Packets decodes bounded timestamped access units without FFmpeg or
// software transcoding. Packet loss discards the predictive decoder and waits
// for the next SPS/PPS-prefixed IDR. Cancellation always closes the source and
// reaps the isolated helper; neither URL handling nor voice audio enters it.
func ReadH264Packets(parent context.Context, source io.ReadCloser, receive func(*image.RGBA) error) (returnErr error) {
	defer source.Close()
	config, enabled := hardwareDecoderConfigFromEnvironment()
	if !enabled || !HardwareH264Available(parent) {
		return errors.New("qualified hardware H.264 call decoder unavailable")
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stopSource := context.AfterFunc(ctx, func() { _ = source.Close() })
	defer stopSource()
	queue := liveh264.NewQueue()
	readerDone := make(chan struct{})
	var readerErr error
	var newestPTS atomic.Int64
	var newestStream atomic.Uint64
	var newestSequence atomic.Uint32
	var newestAt atomic.Int64
	clockStart := time.Now()
	go func() {
		defer close(readerDone)
		defer queue.Close()
		for {
			frame, err := liveh264.Read(source)
			if err != nil {
				readerErr = err
				return
			}
			newestPTS.Store(frame.PTSUS)
			newestStream.Store(frame.StreamID)
			newestSequence.Store(frame.Sequence)
			newestAt.Store(time.Since(clockStart).Nanoseconds())
			queue.Push(frame)
		}
	}()
	defer func() {
		cancel()
		_ = source.Close()
		// Close must unblock a ReadCloser's reader; HTTP bodies and pipes do.
		<-readerDone
	}()
	var helper *hardwareHelper
	var stopHelper context.CancelFunc
	var outputDone chan hardwareOutputResult
	var discardOutput *atomic.Bool
	var stopCallClose func() bool
	decodeFailure := make(chan error, 1)
	pictureProgress, stopWatchdog := watchLiveH264Pictures(ctx, 8*time.Second, func() {
		select {
		case decodeFailure <- errors.New("H.264 call delivered no picture progress for 8 seconds"):
		default:
		}
		cancel()
	})
	defer stopWatchdog()
	stop := func() {
		if helper == nil {
			return
		}
		trace := os.Getenv("TATER_VDEC_TRACE") == "1"
		if trace {
			log.Printf("[sat-chat-h264] stopping previous decoder")
		}
		discardOutput.Store(true)
		if stopCallClose != nil {
			stopCallClose()
		}
		stopLiveH264Decoder(ctx, helper, stopHelper, outputDone)
		helper = nil
		if trace {
			log.Printf("[sat-chat-h264] previous decoder reaped")
		}
	}
	defer func() {
		stop()
		select {
		case failure := <-decodeFailure:
			if parent.Err() == nil {
				returnErr = errors.Join(returnErr, failure)
			}
		default:
		}
	}()
	var gate liveh264.Gate
	var sourceEpoch uint64
	var sourceReference time.Time
	var lastResetReport time.Time
	for {
		var frame liveh264.Frame
		var err error
		select {
		case <-ctx.Done():
			return ctx.Err()
		case next, ok := <-queue.Frames():
			if !ok {
				<-readerDone
				err = readerErr
			} else {
				frame = next
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) && helper != nil {
				if err := writeVDECInputEOS(helper.input); err != nil {
					return err
				}
				helper.input.Close()
				select {
				case result := <-outputDone:
					waitErr := helper.command.Wait()
					stopHelper()
					helper.output.Close()
					helper = nil
					if result.err != nil {
						return result.err
					}
					return waitErr
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(8 * time.Second):
					return errors.New("H.264 call decoder drain timed out")
				}
			}
			if parent.Err() != nil {
				return parent.Err()
			}
			return err
		}
		if os.Getenv("TATER_VDEC_TRACE") == "1" {
			if sourceReference.IsZero() || sourceEpoch != frame.StreamID {
				sourceEpoch = frame.StreamID
				sourceReference = time.Now().Add(-time.Duration(frame.PTSUS) * time.Microsecond)
			}
			if frame.Sequence%300 == 0 {
				log.Printf("[sat-chat-h264] input sequence=%d source_lag_ms=%d", frame.Sequence, (time.Since(sourceReference) - time.Duration(frame.PTSUS)*time.Microsecond).Milliseconds())
			}
		}
		accept, reset := gate.Accept(frame)
		if reset {
			if helper != nil && (lastResetReport.IsZero() || time.Since(lastResetReport) >= 10*time.Second) {
				lastResetReport = time.Now()
				log.Printf("[sat-chat-h264] prediction reset at sequence=%d queued=%d newest_sequence=%d", frame.Sequence, len(queue.Frames()), newestSequence.Load())
			}
			stop()
		}
		if !accept {
			continue
		}
		if helper == nil {
			if os.Getenv("TATER_VDEC_TRACE") == "1" {
				log.Printf("[sat-chat-h264] starting decoder at sequence=%d pts_us=%d", frame.Sequence, frame.PTSUS)
			}
			width, height, err := h264DimensionsFromAccessUnit(frame.Data)
			if err != nil || width != int(frame.Width) || height != int(frame.Height) {
				return errors.New("H.264 keyframe does not match negotiated capture geometry")
			}
			// Call cancellation closes input and drains output first. Propagating
			// it straight into CommandContext SIGKILL would bypass OMX cleanup.
			run, stopRun := context.WithCancel(context.WithoutCancel(ctx))
			request := MediaRequest{Width: width, Height: height, FPS: int(frame.FPS)}
			helper, err = startHardwareHelper(run, config, request)
			if err != nil {
				stopRun()
				return err
			}
			if os.Getenv("TATER_VDEC_TRACE") == "1" {
				log.Printf("[sat-chat-h264] decoder process started")
			}
			stopHelper = stopRun
			outputDone = make(chan hardwareOutputResult, 1)
			discard := &atomic.Bool{}
			discardOutput = discard
			currentHelper, currentDone := helper, outputDone
			decoderStream := frame.StreamID
			stopCallClose = context.AfterFunc(ctx, func() {
				discard.Store(true)
				_ = currentHelper.input.Close()
			})
			go func() {
				var reported time.Time
				result := readHardwareOutputPTS(currentHelper.output, request, newGatedAudioWriter(io.Discard), nil, func(image *image.RGBA, ptsUS int64) error {
					if run.Err() != nil {
						return run.Err()
					}
					if discard.Load() || ctx.Err() != nil {
						return nil
					}
					pictureProgress()
					if newestStream.Load() == decoderStream && (reported.IsZero() || time.Since(reported) >= 10*time.Second) {
						reported = time.Now()
						log.Printf("[sat-chat-h264] decode_backlog_ms=%d queued=%d latest_packet_age_ms=%d output_pts_ms=%d",
							max(0, newestPTS.Load()-ptsUS)/1000, len(queue.Frames()),
							(time.Since(clockStart) - time.Duration(newestAt.Load())).Milliseconds(), ptsUS/1000)
					}
					return receive(image)
				})
				if run.Err() == nil && ctx.Err() == nil && result.err != nil && !discard.Load() {
					failure := result.err
					if detail := currentHelper.tail.Error(); detail != nil {
						failure = fmt.Errorf("%w: %v", failure, detail)
					}
					select {
					case decodeFailure <- failure:
					default:
					}
					cancel()
				}
				currentDone <- result
			}()
		}
		if err := writeVDECAccessUnit(helper.input, frame.PTSUS, frame.Data); err != nil {
			return err
		}
	}
}

// A live call must recover from a helper/stream that stays open without
// producing pictures. The renderer already retries this path independently
// of call audio. Use elapsed monotonic time, not the wall clock or source PTS.
func watchLiveH264Pictures(parent context.Context, budget time.Duration, expired func()) (progress func(), stop context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	started := time.Now()
	var latest atomic.Int64
	go func() {
		ticker := time.NewTicker(budget / 4)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if time.Since(started)-time.Duration(latest.Load()) >= budget {
					if ctx.Err() == nil {
						expired()
					}
					return
				}
			}
		}
	}()
	return func() { latest.Store(time.Since(started).Nanoseconds()) }, cancel
}
