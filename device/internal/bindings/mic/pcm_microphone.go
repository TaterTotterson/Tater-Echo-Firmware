//go:build server

package mic

import (
	"context"
	"errors"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Binozo/GoTinyAlsa/pkg/pcm"
	"github.com/Binozo/GoTinyAlsa/pkg/tinyalsa"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/codec"
	pkgmic "github.com/TaterTotterson/Tater-Echo-Firmware/pkg/mic"
)

const biscuitCardNr = 0
const biscuitDeviceNr = 24

// rawTap receives every raw 9-channel batch, and is nil in release builds.
// Only rawtap_bench.go sets it (build tag bench): it records the mics to
// disk, which release firmware must be unable to do.
var rawTap func([]byte)

// PcmMicrophone opens the ALSA device once and fans out to multiple subscribers.
// Callers register via Listen(); each gets their own buffered channel.
type PcmMicrophone struct {
	device    *tinyalsa.AlsaDevice
	target    string
	mu        sync.Mutex
	subs      []chan []byte
	ready     chan struct{}
	readyOnce sync.Once
}

const (
	micStartupTimeout = 12 * time.Second
	micRestartDelay   = 500 * time.Millisecond
)

// NewMicrophone returns the pre-configured microphone alsa device and starts
// the permanent ALSA read loop.
func NewMicrophone() (*PcmMicrophone, error) {
	return NewMicrophoneForTarget("biscuit")
}

// NewMicrophoneForTarget opens the measured raw capture endpoint for a board.
// Checkers exposes one four-channel TLV320AIC3101 stream; its channel layout
// is normalized by the target audio front end, never by pretending it is
// Biscuit's nine-channel array.
func NewMicrophoneForTarget(target string) (*PcmMicrophone, error) {
	card, deviceNr, channels := biscuitCardNr, biscuitDeviceNr, 9
	if strings.EqualFold(strings.TrimSpace(target), "checkers") {
		card, deviceNr, channels = 0, 22, 4
	}
	device := tinyalsa.NewDevice(card, deviceNr, pcm.Config{
		Channels:    channels,
		SampleRate:  16000,
		PeriodSize:  512,
		PeriodCount: 5,
		Format:      tinyalsa.PCM_FORMAT_S24_3LE,
	})
	m := &PcmMicrophone{
		device: &device,
		target: target,
		ready:  make(chan struct{}),
	}
	if err := m.Init(); err != nil {
		return nil, err
	}
	return m, nil
}

// Init stops the mixer service (required to release the ALSA capture device)
// then starts the permanent background ALSA read loop.
func (p *PcmMicrophone) Init() error {
	// Biscuit's mixer daemon owns pcm24c. Checkers' direct TLV endpoint is
	// free while the Android framework is idle; stopping audioserver there
	// would destabilize the screen userspace and is neither needed nor safe.
	if !strings.EqualFold(strings.TrimSpace(p.target), "checkers") {
		cmd := exec.Command("stop", "mixer")
		if err := cmd.Run(); err != nil {
			log.Printf("mic: stop mixer: %v (continuing)", err)
		}
	}
	// Route the differential mic inputs into the ADCs before opening the PCM.
	// Without this the ADCs are powered down and capture returns the I2S bus's
	// own noise floor — with a perfectly healthy ALSA clock, which is what
	// makes it so hard to see. See the codec package.
	codec.EnsureRoutes(p.target)
	go p.readLoop()

	// Do not report the daemon as ready while the ALSA producer is wedged or
	// has already exited. One real capture period proves wake-word audio is
	// flowing; the boot supervisor can safely retry a failed startup.
	select {
	case <-p.ready:
		return nil
	case <-time.After(micStartupTimeout):
		return errors.New("microphone capture produced no audio within 12 seconds")
	}
}

// readLoop opens the ALSA device and reads periods forever, fanning each
// period out to all current subscribers. Runs for the lifetime of the process.
// When an ALSA producer ends, it is restarted while subscribers stay attached.
// GoTinyAlsa does not close its output channel when GetAudioStream returns, so
// ranging that channel would otherwise leave the wake stream silently deaf.
//
// Capture-loss telemetry (2026-07-10): the ALSA ring is only PeriodSize ×
// PeriodCount = 160ms deep, so any stall of this chain longer than that
// loses whole batches at the hardware with no error surfaced anywhere —
// discovered via the AEC reference governor tripping every ~20s on backlogs
// of exactly N×2560 samples. Two measurements below: per-batch arrival gaps
// (a gap ≫ the batch duration is an overrun in progress) and a ~1/min
// audio-vs-wall-clock ledger.
//
// THE LEDGER'S SIGN IS ITS WHOLE MEANING, and it reads both ways. Positive
// (wall ahead of audio) is audio the pipeline never got — overruns, the case
// this was built for. Negative is the capture clock running FAST, which is
// the ordinary state of this hardware and not a fault.
//
// Measured on SPJ over 11.8h, 2026-09-02: skew went -154ms to -14809ms, a
// steady **+345ppm** of the ALSA sample clock against CLOCK_MONOTONIC. The
// starting -154ms is just this loop counting the first batch's 160ms while
// wall starts at its arrival; everything after is the rate mismatch.
//
// It arrives in 160ms STEPS, roughly one every 7.7 minutes, and the earlier
// version of this comment said a rate mismatch would grow the deficit
// "smoothly rather than in stall-sized steps" — it does not, because
// delivery is quantised to whole 160ms batches, so the surplus banks in the
// ALSA ring until it is a whole batch and then lands at once. 160ms ÷
// 345ppm = 464s predicted against ~462s observed, which is what identifies
// it. So step-shaped growth does NOT distinguish overruns from drift; the
// SIGN does, and a device that is losing audio also raises stalls.
//
// Consequence for whoever reads a long uptime: seconds of accumulated
// negative skew are expected and mean nothing is wrong.
func (p *PcmMicrophone) readLoop() {
	rate := int64(p.device.DeviceConfig.SampleRate)
	bytesPerFrame := p.device.DeviceConfig.Channels * 3 // S24_3LE
	var (
		firstArrival time.Time
		lastArrival  time.Time
		lastReport   time.Time
		framesTotal  int64
		stalls       uint64
		subDrops     uint64
		restarts     uint64
	)

	for {
		stream := make(chan []byte, 16)
		ended := make(chan error, 1)
		go func() {
			ended <- p.device.GetAudioStream(p.device.DeviceConfig, stream)
		}()

	capture:
		for {
			var audio []byte
			select {
			case audio = <-stream:
				p.readyOnce.Do(func() { close(p.ready) })
			case err := <-ended:
				restarts++
				if restarts == 1 || restarts%10 == 0 {
					if err != nil {
						log.Printf("mic: ALSA producer ended: %v — restarting (attempt=%d)", err, restarts)
					} else {
						log.Printf("mic: ALSA producer ended without an error — restarting (attempt=%d)", restarts)
					}
				}
				time.Sleep(micRestartDelay)
				break capture
			}

			now := time.Now()
			frames := int64(len(audio) / bytesPerFrame)
			batchDur := time.Duration(frames) * time.Second / time.Duration(rate)
			if firstArrival.IsZero() {
				firstArrival, lastReport = now, now
			} else if gap := now.Sub(lastArrival); gap > 2*batchDur {
				stalls++
				log.Printf("[mic] capture stall: %dms between %dms batches — ~%dms lost to ALSA overrun (stalls=%d)",
					gap.Milliseconds(), batchDur.Milliseconds(),
					(gap - batchDur).Milliseconds(), stalls)
			}
			lastArrival = now
			framesTotal += frames
			if now.Sub(lastReport) >= time.Minute {
				wall := now.Sub(firstArrival)
				audioDur := time.Duration(framesTotal) * time.Second / time.Duration(rate)
				// Name the direction rather than leaving a signed number to be
				// read as loss either way — see readLoop's ledger note.
				skew := (wall - audioDur).Milliseconds()
				sense := "lost"
				if skew < 0 {
					sense = "capture fast"
				}
				log.Printf("[mic] clock: %.1fs audio over %.1fs wall (skew %+dms %s, stalls=%d, sub_drops=%d)",
					audioDur.Seconds(), wall.Seconds(), skew, sense, stalls, subDrops)
				lastReport = now
			}

			// GetAudioStream hands over a fresh slice per read (GoTinyAlsa #1),
			// so this can be passed on as-is. Copying here was too late: the
			// library reused one buffer, and a batch still queued in stream was
			// overwritten by the next read — repeated or torn audio (#607).
			buf := audio
			if rawTap != nil {
				rawTap(buf)
			}

			p.mu.Lock()
			for _, ch := range p.subs {
				select {
				case ch <- buf:
				default:
					// Subscriber too slow — drop this period rather than block
					subDrops++
					if subDrops == 1 || subDrops%64 == 0 {
						log.Printf("[mic] subscriber channel full — batch dropped (sub_drops=%d)", subDrops)
					}
				}
			}
			p.mu.Unlock()
		}
	}
}

// subscribe registers a new subscriber and returns its channel.
func (p *PcmMicrophone) Subscribe() chan []byte {
	ch := make(chan []byte, 32)
	p.mu.Lock()
	p.subs = append(p.subs, ch)
	p.mu.Unlock()
	return ch
}

// Unsubscribe removes and closes a subscriber channel. ALSA producer restarts
// leave subscribers attached, so the subscriber owner controls its lifetime.
func (p *PcmMicrophone) Unsubscribe(ch chan []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, s := range p.subs {
		if s == ch {
			p.subs = append(p.subs[:i], p.subs[i+1:]...)
			close(ch)
			return
		}
	}
	// Not found — it was already unsubscribed. Nothing to do.
}

// Listen subscribes to the permanent mic stream and calls callback for each
// period until ctx is cancelled. Satisfies the pkgmic.Microphone interface.
func (p *PcmMicrophone) Listen(callback pkgmic.AudioCallback, ctx context.Context) error {
	if callback == nil {
		return errors.New("callback can't be nil")
	}
	ch := p.Subscribe()
	defer p.Unsubscribe(ch)

	for {
		select {
		case <-ctx.Done():
			return nil
		case audio, ok := <-ch:
			if !ok {
				return nil
			}
			callback(audio)
		}
	}
}
