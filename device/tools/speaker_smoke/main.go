//go:build server

// speaker_smoke performs a short, bounded audible test through the same
// PcmSpeaker path used by Tater replies. It is a development tool, not part of
// a release image.
package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/mixer"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/speaker"
)

const (
	sampleRate   = 48000
	periodFrames = 2048
	logicalMax   = 127
)

func main() {
	target := flag.String("target", "", "firmware target (must be checkers for this bring-up test)")
	level := flag.Int("level", 127, "logical codec level, 1..127; waveform amplitude remains bounded")
	duration := flag.Duration("duration", 500*time.Millisecond, "tone duration, 100ms..2s")
	frequency := flag.Float64("frequency", 660, "tone frequency in hertz, 100..2000")
	flag.Parse()

	if *target != "checkers" {
		log.Fatal("refusing audible test: pass -target checkers explicitly")
	}
	if os.Geteuid() != 0 {
		log.Fatal("speaker smoke test must run as root")
	}
	if *level < 1 || *level > logicalMax {
		log.Fatal("level must be between 1 and 127")
	}
	if *duration < 100*time.Millisecond || *duration > 2*time.Second {
		log.Fatal("duration must be between 100ms and 2s")
	}
	if *frequency < 100 || *frequency > 2000 {
		log.Fatal("frequency must be between 100Hz and 2000Hz")
	}

	mixer.ConfigureTarget(*target)
	output, err := speaker.NewPcmSpeakerForTarget(*target, nil, nil)
	if err != nil {
		log.Fatalf("open speaker: %v", err)
	}
	defer output.Close()
	if err := mixer.SetPlaybackLevel(*level, logicalMax); err != nil {
		log.Fatalf("set safe playback level: %v", err)
	}

	periods := int(math.Ceil(duration.Seconds() * sampleRate / periodFrames))
	totalFrames := periods * periodFrames
	for period := 0; period < periods; period++ {
		pcm := make([]byte, periodFrames*2)
		for frame := 0; frame < periodFrames; frame++ {
			index := period*periodFrames + frame
			envelope := fade(index, totalFrames, sampleRate/50) // 20ms ends
			sample := int16(math.Round(1600 * envelope * math.Sin(2*math.Pi**frequency*float64(index)/sampleRate)))
			binary.LittleEndian.PutUint16(pcm[frame*2:], uint16(sample))
		}
		if err := output.PumpPeriod(pcm); err != nil {
			log.Fatalf("queue tone period: %v", err)
		}
	}
	output.EndStream()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := output.WaitVoiceIdle(ctx); err != nil {
		log.Fatalf("wait for audible drain: %v", err)
	}
	fmt.Printf("speaker smoke complete: %gHz, %s, level %d/%d\n", *frequency, *duration, *level, logicalMax)
}
