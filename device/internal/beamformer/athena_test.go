package beamformer

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestAthenaFrontEndDecimatesFourS32Channels(t *testing.T) {
	front := NewAthena()
	raw := make([]byte, athenaRatio*athenaChannels*4*80)
	for frame := 0; frame < len(raw)/(athenaChannels*4); frame++ {
		for channel := 0; channel < athenaChannels; channel++ {
			binary.LittleEndian.PutUint32(raw[(frame*athenaChannels+channel)*4:], uint32(int32(4096<<16)))
		}
	}
	mono, angle := front.Process(raw, 0, 1)
	if len(mono) != 80*2 || angle != -1 {
		t.Fatalf("Athena output = %d bytes, angle %v", len(mono), angle)
	}
	got := int16(binary.LittleEndian.Uint16(mono[len(mono)-2:]))
	want := 4096 * math.Pow(10, athenaMicBoost/20)
	if math.Abs(float64(got)-want) > 2 {
		t.Fatalf("settled sample = %d, want %.0f", got, want)
	}
	if front.EchoRefInto(raw, nil) != nil {
		t.Fatal("Athena must retain the software echo reference until paired capture is implemented")
	}
}

func TestAthenaSelectedByTarget(t *testing.T) {
	if _, ok := NewForTarget(" ATHENA ").(*AthenaFrontEnd); !ok {
		t.Fatalf("Athena target selected %T", NewForTarget("athena"))
	}
}

func TestAthenaAdaptiveBeamAlignsUnknownChannelOrder(t *testing.T) {
	front := NewAthena()
	raw := athenaDelayedRaw(1600, [athenaChannels]int{0, 1, 3, 4},
		[athenaChannels]bool{true, true, true, true})

	omni, _ := front.Process(raw, 0, 1)
	diag := front.Diagnostics()
	if !diag.Calibrated || diag.HealthyMicChannels != athenaChannels {
		t.Fatalf("Athena alignment did not acquire: %+v", diag)
	}
	if diag.DelaySamples < 3.2 || diag.DelaySamples > 4.8 {
		t.Fatalf("Athena delay span = %.2f samples, want about 4", diag.DelaySamples)
	}
	if diag.Spatial < 0.80 {
		t.Fatalf("Athena correlation = %.3f, want a confident coherent wave", diag.Spatial)
	}
	direction, ok := front.WakeDirection()
	if !ok || direction != athenaCoherentPath {
		t.Fatalf("Athena wake direction = %d, %v", direction, ok)
	}
	beam := front.WakeBeam(raw, direction, 1)
	if beam.Direction != athenaCoherentPath || len(beam.PCM) != len(omni) {
		t.Fatalf("Athena wake beam = path %d, %d bytes; want path %d, %d bytes",
			beam.Direction, len(beam.PCM), athenaCoherentPath, len(omni))
	}
	if got, base := pcmRMS(beam.PCM), pcmRMS(omni); got < base*1.45 {
		t.Fatalf("aligned wake RMS %.1f did not improve delayed omni RMS %.1f", got, base)
	}
}

func TestAthenaWinningAdaptiveBeamCarriesIntoTurn(t *testing.T) {
	front := NewAthena()
	raw := athenaDelayedRaw(1600, [athenaChannels]int{4, 0, 3, 1},
		[athenaChannels]bool{true, true, true, true})
	front.Process(raw, 0, 1)
	front.LockWakeDirection(athenaCoherentPath, true)

	locked, _ := front.Process(raw, 0, 1)
	if front.OutputChannel() != athenaCoherentPath || !front.steeringLocked {
		t.Fatalf("Athena turn did not retain wake beam: path=%d locked=%v",
			front.OutputChannel(), front.steeringLocked)
	}
	if pcmRMS(locked) == 0 {
		t.Fatal("Athena locked beam is silent")
	}

	front.Unlock()
	front.Process(raw, 0, 1)
	if front.OutputChannel() != athenaOmniPath || front.steeringLocked {
		t.Fatalf("Athena unlock did not restore omni: path=%d locked=%v",
			front.OutputChannel(), front.steeringLocked)
	}
}

func TestAthenaExcludesFailedMicrophoneAndFallsBackSafely(t *testing.T) {
	three := NewAthena()
	raw := athenaDelayedRaw(1600, [athenaChannels]int{0, 2, 4, 0},
		[athenaChannels]bool{true, true, true, false})
	mono, _ := three.Process(raw, 0, 1)
	if diag := three.Diagnostics(); !diag.Calibrated || diag.HealthyMicChannels != 3 {
		t.Fatalf("Athena three-mic degraded mode did not acquire: %+v", diag)
	}
	if pcmRMS(mono) == 0 {
		t.Fatal("Athena three-mic degraded output is silent")
	}

	one := NewAthena()
	raw = athenaDelayedRaw(1600, [athenaChannels]int{},
		[athenaChannels]bool{true, false, false, false})
	mono, _ = one.Process(raw, 0, 1)
	if direction, ok := one.WakeDirection(); ok {
		t.Fatalf("Athena one-mic fallback advertised beam %d", direction)
	}
	if diag := one.Diagnostics(); diag.Calibrated || diag.HealthyMicChannels != 1 {
		t.Fatalf("Athena one-mic fallback diagnostics = %+v", diag)
	}
	if one.OutputChannel() != athenaOmniPath || pcmRMS(mono) == 0 {
		t.Fatalf("Athena one-mic fallback path=%d rms=%.1f", one.OutputChannel(), pcmRMS(mono))
	}
}

func TestAthenaLockedBeamFallsBackWhenOnlyOneMicrophoneRemains(t *testing.T) {
	front := NewAthena()
	all := athenaDelayedRaw(1600, [athenaChannels]int{0, 1, 3, 4},
		[athenaChannels]bool{true, true, true, true})
	front.Process(all, 0, 1)
	front.LockWakeDirection(athenaCoherentPath, true)

	one := athenaDelayedRaw(1600, [athenaChannels]int{},
		[athenaChannels]bool{true, false, false, false})
	// The first batch contains the 2 ms tail already resident in the decimator;
	// the next one must expose the persistent channel loss and change AEC path.
	front.Process(one, 0, 1)
	mono, _ := front.Process(one, 0, 1)
	if front.OutputChannel() != athenaOmniPath {
		t.Fatalf("Athena retained coherent AEC path with one live microphone: %d", front.OutputChannel())
	}
	if pcmRMS(mono) == 0 {
		t.Fatal("Athena locked one-microphone fallback is silent")
	}
}

func TestAthenaIdleFloorCannotSteerAndExpiresPriorAlignment(t *testing.T) {
	front := NewAthena()
	loud := athenaDelayedRaw(1600, [athenaChannels]int{0, 1, 3, 4},
		[athenaChannels]bool{true, true, true, true})
	front.Process(loud, 0, 1)
	if _, ok := front.WakeDirection(); !ok {
		t.Fatal("Athena did not acquire the initial speech alignment")
	}

	quiet := athenaScaledRaw(loud, 200)
	// One transition batch still contains the 2 ms FIR tail from the loud
	// signal; the following hold window must then expire normally.
	for batch := 0; batch <= athenaAlignmentHold+1; batch++ {
		front.Process(quiet, 0, 1)
	}
	if direction, ok := front.WakeDirection(); ok {
		t.Fatalf("Athena idle floor retained stale beam %d", direction)
	}
	if diag := front.Diagnostics(); diag.Calibrated {
		t.Fatalf("Athena idle floor remained calibrated: %+v", diag)
	}

	fresh := NewAthena()
	fresh.Process(quiet, 0, 1)
	if _, ok := fresh.WakeDirection(); ok {
		t.Fatal("Athena idle floor acquired a new beam")
	}
}

func TestAthenaMeasuredLeftRightChannelGroupsReverseDelay(t *testing.T) {
	left := NewAthena()
	left.Process(athenaDelayedRaw(1600, [athenaChannels]int{0, 1, 1, 0},
		[athenaChannels]bool{true, true, true, true}), 0, 1)
	if left.arrival[1] < 0.6 || left.arrival[2] < 0.6 || math.Abs(left.arrival[3]) > 0.2 {
		t.Fatalf("Athena measured left signature = %+.2v, want channels 0/3 before 1/2", left.arrival)
	}

	right := NewAthena()
	right.Process(athenaDelayedRaw(1600, [athenaChannels]int{1, 0, 0, 1},
		[athenaChannels]bool{true, true, true, true}), 0, 1)
	if right.arrival[1] > -0.6 || right.arrival[2] > -0.6 || math.Abs(right.arrival[3]) > 0.2 {
		t.Fatalf("Athena measured right signature = %+.2v, want channels 1/2 before 0/3", right.arrival)
	}
}

// athenaDelayedRaw synthesizes a plane wave without assuming which converter
// channel occupies which physical position. Positive per-channel delays mean
// that microphone receives the same broadband signal later. Repeating each
// logical 16 kHz sample six times gives the production 96 kHz decimator a
// deterministic, band-limited-enough input while still exercising the real
// S32_LE conversion and FIR path.
func athenaDelayedRaw(logicalFrames int, delays [athenaChannels]int, live [athenaChannels]bool) []byte {
	const preRoll = 64
	source := make([]int32, logicalFrames+2*preRoll)
	seed := uint32(0x5a17c9e3)
	for i := range source {
		seed = seed*1664525 + 1013904223
		source[i] = int32(seed>>16) - 32768
	}
	raw := make([]byte, logicalFrames*athenaRatio*athenaFrameSize)
	for frame := 0; frame < logicalFrames*athenaRatio; frame++ {
		logical := frame / athenaRatio
		for channel := 0; channel < athenaChannels; channel++ {
			var sample int32
			if live[channel] {
				sample = source[preRoll+logical-delays[channel]] / 5
			}
			base := frame*athenaFrameSize + channel*4
			binary.LittleEndian.PutUint32(raw[base:], uint32(sample<<16))
		}
	}
	return raw
}

func athenaScaledRaw(raw []byte, divisor int32) []byte {
	result := make([]byte, len(raw))
	for offset := 0; offset+4 <= len(raw); offset += 4 {
		sample := int32(binary.LittleEndian.Uint32(raw[offset:])) / divisor
		binary.LittleEndian.PutUint32(result[offset:], uint32(sample))
	}
	return result
}
