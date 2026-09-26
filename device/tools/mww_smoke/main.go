//go:build server

// mww_smoke runs the production Checkers microphone and microWakeWord path
// without connecting to Tater or retaining captured audio.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/aec"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/mic"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/mixer"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/client"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/wakeword/microwakeword"
)

func main() {
	target := flag.String("target", "", "firmware target (must be checkers for this bring-up test)")
	duration := flag.Duration("duration", 60*time.Second, "listening duration, 5s..2m")
	flag.Parse()

	if *target != "checkers" {
		log.Fatal("refusing microphone test: pass -target checkers explicitly")
	}
	if os.Geteuid() != 0 {
		log.Fatal("microWakeWord smoke test must run as root")
	}
	if *duration < 5*time.Second || *duration > 2*time.Minute {
		log.Fatal("duration must be between 5s and 2m")
	}

	mixer.ConfigureTarget(*target)
	microphone, err := mic.NewMicrophoneForTarget(*target)
	if err != nil {
		log.Fatalf("open microphone: %v", err)
	}

	canceller := aec.NewForTarget(*target) // disabled: no far-end playback in this test
	data := client.NewDataClientForTarget("mww-smoke", microphone, nil, canceller, *target)
	var levels struct {
		sync.Mutex
		chunks  uint64
		samples uint64
		sumSq   float64
		maxRMS  float64
		clipped uint64
	}
	data.OnPCM(func(pcm []byte) {
		var sum float64
		var clipped uint64
		samples := len(pcm) / 2
		for index := 0; index < samples; index++ {
			value := int16(binary.LittleEndian.Uint16(pcm[index*2:]))
			normalized := float64(value) / 32768
			sum += normalized * normalized
			if value >= 32760 || value <= -32760 {
				clipped++
			}
		}
		rms := 0.0
		if samples > 0 {
			rms = math.Sqrt(sum / float64(samples))
		}
		levels.Lock()
		levels.chunks++
		levels.samples += uint64(samples)
		levels.sumSq += sum
		if rms > levels.maxRMS {
			levels.maxRMS = rms
		}
		levels.clipped += clipped
		levels.Unlock()
	})
	crossings := make(chan float32, 8)
	scorer, err := microwakeword.OpenShadowTunedWithHooks("hey_tater", microwakeword.ScorerOverrides{
		Sensitivity: "normal",
		Environment: "balanced",
	}, microwakeword.ShadowHooks{
		Cross: func(score float32, _ time.Time) {
			log.Printf("[mww-smoke] WAKE DETECTED score=%.3f", score)
			select {
			case crossings <- score:
			default:
			}
		},
		CloseMiss: func(score float32, _ time.Time) {
			log.Printf("[mww-smoke] close miss score=%.3f", score)
		},
	})
	if err != nil {
		log.Fatalf("open microWakeWord: %v", err)
	}
	defer scorer.Close()
	data.SetMWWShadowScorer(scorer)
	data.StartLocalMic()

	fmt.Printf("microWakeWord ready: %s\n", scorer.Info())
	fmt.Printf("LISTENING for %s — say Hey Tater three times\n", *duration)
	timer := time.NewTimer(*duration)
	defer timer.Stop()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stop)
	count := 0
loop:
	for {
		select {
		case <-crossings:
			count++
		case <-timer.C:
			break loop
		case <-stop:
			break loop
		}
	}
	data.StopMic()
	time.Sleep(250 * time.Millisecond) // let the mic goroutine observe stopCh
	stats := scorer.Drain()
	levels.Lock()
	averageRMS := 0.0
	if levels.samples > 0 {
		averageRMS = math.Sqrt(levels.sumSq / float64(levels.samples))
	}
	levelChunks, levelMax, levelClipped := levels.chunks, levels.maxRMS, levels.clipped
	levels.Unlock()
	fmt.Printf("RESULT detections=%d chunks=%d scores=%d max_raw=%.3f max_window=%.3f threshold=%.3f drops=%d errors=%d pcm_chunks=%d pcm_rms_avg=%.5f pcm_rms_max=%.5f clipped=%d\n",
		count, stats.Chunks, stats.Scores, stats.MaxRawScore, stats.MaxScore,
		stats.Threshold, stats.Drops, stats.Errors, levelChunks, averageRMS, levelMax, levelClipped)
}
