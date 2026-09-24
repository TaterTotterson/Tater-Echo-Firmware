package server

import (
	"math"
	"testing"

	"github.com/TaterTotterson/Tater-Echo-Firmware/pkg/led"
)

func TestSpinFrame(t *testing.T) {
	spec := AnimSpec{
		Pattern: "spin",
		Colors:  [][3]uint8{{0, 200, 0}, {0, 60, 0}},
	}
	frame := animFrame(spec, 3)
	if frame[3].G != 200 {
		t.Fatalf("head not at pos 3: %+v", frame[3])
	}
	if frame[2].G != 60 {
		t.Fatalf("trail not at pos 2: %+v", frame[2])
	}
	for i, l := range frame {
		if i == 3 || i == 2 {
			continue
		}
		if l.R != 0 || l.G != 0 || l.B != 0 {
			t.Fatalf("LED %d not dark: %+v", i, l)
		}
		if l.ID != i {
			t.Fatalf("LED %d has wrong ID %d", i, l.ID)
		}
	}
	// Wraparound: head at 0 puts trail at 11.
	frame = animFrame(spec, 0)
	if frame[0].G != 200 || frame[11].G != 60 {
		t.Fatalf("wraparound wrong: head=%+v trail=%+v", frame[0], frame[11])
	}
}

func TestRotateFrame(t *testing.T) {
	palette := make([][3]uint8, 12)
	for i := range palette {
		palette[i] = [3]uint8{uint8(i), 0, 0}
	}
	spec := AnimSpec{Pattern: "rotate", Colors: palette}
	// pos=0 is the palette 1:1; pos=1 shifts every colour one LED clockwise.
	frame := animFrame(spec, 0)
	for i := range frame {
		if frame[i].R != uint8(i) {
			t.Fatalf("pos 0: LED %d = %d, want %d", i, frame[i].R, i)
		}
	}
	frame = animFrame(spec, 1)
	if frame[1].R != 0 || frame[0].R != 11 {
		t.Fatalf("pos 1 rotation wrong: led0=%d led1=%d", frame[0].R, frame[1].R)
	}
}

func TestRotateFrameEmptyPalette(t *testing.T) {
	frame := animFrame(AnimSpec{Pattern: "rotate"}, 5)
	for i, l := range frame {
		if l.R != 0 || l.G != 0 || l.B != 0 {
			t.Fatalf("LED %d not dark on empty palette: %+v", i, l)
		}
	}
}

func TestPaletteFrame(t *testing.T) {
	// Single colour fills the ring.
	frame := paletteFrame([][3]uint8{{10, 20, 30}})
	for i, l := range frame {
		if l.R != 10 || l.G != 20 || l.B != 30 {
			t.Fatalf("LED %d wrong: %+v", i, l)
		}
	}
	// Short multi-colour list leaves the rest dark.
	frame = paletteFrame([][3]uint8{{1, 0, 0}, {2, 0, 0}})
	if frame[0].R != 1 || frame[1].R != 2 || frame[2].R != 0 {
		t.Fatalf("partial palette wrong: %+v", frame[:3])
	}
}

func TestResolveMeterDefaultsAndClamps(t *testing.T) {
	// Absent fields fall back to the shipped curve.
	a, d, f, g, r, c := resolveMeter(AnimSpec{Pattern: "meter"})
	if a != meterDefaults.attack || d != meterDefaults.decay ||
		f != meterDefaults.floor || g != meterDefaults.gamma ||
		r != meterDefaults.ref || c != meterDefaults.curve {
		t.Fatalf("defaults not applied: %v %v %v %v %v %v", a, d, f, g, r, c)
	}

	// floor=0 must survive: it is a legitimate value (fully dark at
	// silence), which is the whole reason these fields are pointers.
	zero := 0.0
	_, _, f0, _, _, _ := resolveMeter(AnimSpec{Floor: &zero})
	if f0 != 0.0 {
		t.Fatalf("floor 0 not honoured, got %v", f0)
	}

	// Out-of-range values clamp rather than producing a dead ring: a
	// config push must not be able to break the display.
	huge, neg := 99.0, -5.0
	at, dc, fl, gm, rf, cv := resolveMeter(AnimSpec{
		Attack: &huge, Decay: &neg, Floor: &huge,
		Gamma: &neg, Ref: &neg, Curve: &huge,
	})
	if at != 1.0 || dc != 0.02 || fl != 0.6 || gm != 1.0 || rf != 0.02 || cv != 2.0 {
		t.Fatalf("clamping wrong: %v %v %v %v %v %v", at, dc, fl, gm, rf, cv)
	}
}

// TestMeterCurveIsVisiblyVaried is the regression guard for the reported
// "too subtle to distinguish from a solid ring" bug. It asserts the shipped
// curve produces a wide PERCEPTUAL swing across ordinary speech levels —
// the old curve (sqrt(rms/0.35), floor .15, no gamma) managed only ~23%.
func TestMeterCurveIsVisiblyVaried(t *testing.T) {
	_, _, floor, gamma, ref, curve := resolveMeter(AnimSpec{Pattern: "meter"})
	span := 1.0 - floor

	// Perceived lightness of a painted duty cycle b is ~b^(1/gamma), so
	// with the gamma encoding the perceptual value is just floor+span*env.
	perceived := func(rms float64) float64 {
		env := math.Pow(math.Min(1, rms/ref), curve)
		b := math.Pow(floor+span*env, gamma)
		return math.Pow(b, 1.0/gamma)
	}

	quiet, loud := perceived(0.02), perceived(0.20)
	if got := loud - quiet; got < 0.55 {
		t.Fatalf("perceptual swing across speech only %.2f, want >=0.55 "+
			"(quiet=%.2f loud=%.2f) — meter will read as a solid ring", got, quiet, loud)
	}
	// Silence must still be visibly lit: the ring belongs to the turn.
	if s := perceived(0.0); s < 0.03 {
		t.Fatalf("silence too dark (%.3f) — ring reads as off mid-response", s)
	}
	// And full scale must not clip below full brightness.
	if p := perceived(1.0); p < 0.99 {
		t.Fatalf("peak not full brightness: %.3f", p)
	}
}

func TestNativeAnimationFramesCoverEverySelectableEffect(t *testing.T) {
	animations := []string{
		"sparkle", "ping_pong", "voice_ring", "spinner", "orbit", "pulse",
		"breathe", "comet", "dual_comet", "scanner", "ripple", "heartbeat",
		"theater", "wave", "shimmer", "twinkle", "equalizer",
	}
	for _, animation := range animations {
		t.Run(animation, func(t *testing.T) {
			state := nativeAnimState{}
			frame := nativeAnimationFrame(animation, 9, [3]uint8{200, 80, 20}, 0.08, 4, &state)
			if len(frame) != 12 {
				t.Fatalf("frame length = %d, want 12", len(frame))
			}
			lit := false
			for i, pixel := range frame {
				if pixel.ID != i {
					t.Fatalf("pixel %d has ID %d", i, pixel.ID)
				}
				lit = lit || pixel.R != 0 || pixel.G != 0 || pixel.B != 0
			}
			if !lit {
				t.Fatal("effect rendered a completely dark ring")
			}
		})
	}
}

func TestNativeAnimationsAdvanceAndVoiceRingReacts(t *testing.T) {
	for _, animation := range []string{"ping_pong", "spinner", "orbit", "comet", "scanner", "wave", "equalizer"} {
		state := nativeAnimState{}
		first := nativeAnimationFrame(animation, 0, [3]uint8{255, 90, 31}, 0, 0, &state)
		later := nativeAnimationFrame(animation, 5, [3]uint8{255, 90, 31}, 0, 0, &state)
		equal := true
		for i := range first {
			equal = equal && first[i] == later[i]
		}
		if equal {
			t.Errorf("%s did not advance", animation)
		}
	}

	darkState := nativeAnimState{}
	loudState := nativeAnimState{}
	quiet := nativeAnimationFrame("voice_ring", 3, [3]uint8{255, 90, 31}, 0, 2, &darkState)
	loud := nativeAnimationFrame("voice_ring", 3, [3]uint8{255, 90, 31}, 0.12, 2, &loudState)
	brightness := func(frame []led.Led) int {
		total := 0
		for _, pixel := range frame {
			total += int(pixel.R) + int(pixel.G) + int(pixel.B)
		}
		return total
	}
	if brightness(loud) <= brightness(quiet) {
		t.Fatalf("voice ring did not expand with audio: quiet=%d loud=%d", brightness(quiet), brightness(loud))
	}
}

func TestDirectionalFrameIsPointedAtDOA(t *testing.T) {
	var base [12]led.Led
	for i := range base {
		base[i] = led.Led{ID: i, R: 220, G: 50, B: 10}
	}
	frame := directionalFrame(base, 3)
	brightness := func(pixel led.Led) int {
		return int(pixel.R) + int(pixel.G) + int(pixel.B)
	}
	brightest := 0
	for i := 1; i < len(frame); i++ {
		if brightness(frame[i]) > brightness(frame[brightest]) {
			brightest = i
		}
	}
	if brightest != 3 {
		t.Fatalf("brightest LED = %d, want measured DOA LED 3", brightest)
	}
	if brightness(frame[3]) <= brightness(frame[2]) || brightness(frame[2]) <= brightness(frame[1]) {
		t.Fatalf("beam does not taper away from DOA: center=%d shoulder=%d tail=%d",
			brightness(frame[3]), brightness(frame[2]), brightness(frame[1]))
	}
	if brightness(frame[9])*8 >= brightness(frame[3]) {
		t.Fatalf("opposite side is too bright to read as directional: tip=%d opposite=%d",
			brightness(frame[3]), brightness(frame[9]))
	}

	wrapped := directionalFrame(base, 11.5)
	if brightness(wrapped[11]) != brightness(wrapped[0]) {
		t.Fatalf("beam does not wrap evenly across LED 11/0: led11=%d led0=%d",
			brightness(wrapped[11]), brightness(wrapped[0]))
	}
}

func TestDirectionMovesOnFirstSpeechWithoutTeleporting(t *testing.T) {
	s := &Server{}
	s.listeningLEDs = true
	s.SetDirectionObservation(330, true, true)
	start := s.directionPosition

	// A short utterance may produce only one microphone observation. It must
	// move the beam immediately, but the per-frame cap prevents teleporting
	// all the way across the ring on one noisy reading.
	s.SetDirectionObservation(90, true, true)
	first := s.directionPosition
	if first <= start || first >= 7 {
		t.Fatalf("first speech observation did not move smoothly: start=%.2f got=%.2f target=7", start, first)
	}

	// Sustained speech continues toward the target.
	s.SetDirectionObservation(90, true, true)
	if got := s.directionPosition; got <= first {
		t.Fatalf("sustained direction did not advance: first=%.2f got=%.2f", first, got)
	}
}

func TestDirectionSmoothingRunsAtDisplayRate(t *testing.T) {
	s := &Server{}
	s.listeningLEDs = true
	s.SetDirectionObservation(330, true, true) // LED 3
	s.SetDirectionObservation(90, true, true)  // LED 7

	previous := s.directionPosition
	for frame := 0; frame < 6; frame++ {
		s.renderDirectionFrame()
		current := s.directionPosition
		step := current - previous
		if step <= 0 || step > directionMaxStep+1e-9 {
			t.Fatalf("frame %d moved %.3f LEDs, want a smooth clockwise step in (0, %.2f]", frame, step, directionMaxStep)
		}
		previous = current
	}
	if previous >= 7 {
		t.Fatalf("display-rate smoothing jumped directly to target: got %.3f, target 7", previous)
	}
}

func TestDirectionSmoothingUsesShortestWrap(t *testing.T) {
	clockwise := smoothRingPosition(11.8, 0.4)
	if clockwise <= 11.8 && clockwise >= 0.4 {
		t.Fatalf("11.8 -> 0.4 did not cross the wrap clockwise: %.3f", clockwise)
	}
	if d := ringDistance(clockwise, 11.8); d > directionMaxStep+1e-9 {
		t.Fatalf("wrap step %.3f exceeds cap %.2f", d, directionMaxStep)
	}

	counterClockwise := smoothRingPosition(0.2, 11.6)
	if counterClockwise >= 0.2 && counterClockwise <= 11.6 {
		t.Fatalf("0.2 -> 11.6 did not cross the wrap counter-clockwise: %.3f", counterClockwise)
	}
}

func TestReplyDirectionUsesAcousticTargetNotVisualLag(t *testing.T) {
	s := &Server{}
	s.listeningLEDs = true
	s.SetDirectionObservation(330, true, true) // LED 3
	s.SetDirectionObservation(90, true, true)  // target LED 7; visual only reaches 3.5
	if s.directionPosition >= 7 {
		t.Fatal("test setup no longer has visual lag")
	}
	s.endDirectionalListening()
	if got := s.directionLEDIndex(); got != 7 {
		t.Fatalf("reply direction = LED %d, want measured target LED 7", got)
	}
}

func TestDirectionalListeningWaitsForSpeech(t *testing.T) {
	s := &Server{}
	s.listeningLEDs = true

	s.SetDirectionObservation(330, false, false)
	if s.directionPositionKnown || s.directionSpeechStarted {
		t.Fatal("DOA started before speech instead of retaining the neutral listening glow")
	}

	// Lenient acoustic activity starts DOA even when the strict speech gate
	// has not asserted yet.
	s.SetDirectionObservation(330, true, false)
	if !s.directionPositionKnown || !s.directionSpeechStarted {
		t.Fatal("DOA did not start when speech was detected")
	}
	position := s.directionPosition
	s.SetDirectionObservation(90, false, false)
	if s.directionPosition != position {
		t.Fatalf("silence moved DOA from %.2f to %.2f", position, s.directionPosition)
	}
}

func TestReplyDirectionUsesLastSpeechNotTrailingNoise(t *testing.T) {
	s := &Server{}
	s.listeningLEDs = true

	// 330° maps to LED 3 on biscuit. Trailing no-speech observations may
	// update the live listening animation, but must not move the reply away
	// from the direction in which speech was actually heard.
	for i := 0; i < 6; i++ {
		s.SetDirectionObservation(330, true, true)
	}
	for i := 0; i < 6; i++ {
		s.SetDirectionObservation(90, true, false)
	}
	s.endDirectionalListening()
	if got := s.directionLEDIndex(); got != 3 {
		t.Fatalf("reply direction = LED %d, want last speech LED 3", got)
	}

	// Once listening ends, speaker echo and room noise are not allowed to
	// overwrite the direction remembered for the reply.
	s.SetDirectionObservation(90, true, false)
	if got := s.directionLEDIndex(); got != 3 {
		t.Fatalf("reply direction moved after listening: LED %d, want 3", got)
	}

	state := nativeAnimState{}
	frame := nativeAnimationFrame("voice_ring", 2, [3]uint8{220, 50, 10}, 0, s.directionLEDIndex(), &state)
	brightness := func(pixel led.Led) int {
		return int(pixel.R) + int(pixel.G) + int(pixel.B)
	}
	if brightness(frame[3]) <= brightness(frame[9]) {
		t.Fatalf("reply does not point at remembered DOA: center=%d opposite=%d", brightness(frame[3]), brightness(frame[9]))
	}
}
