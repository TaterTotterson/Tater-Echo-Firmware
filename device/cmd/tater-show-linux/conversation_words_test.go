//go:build linux

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

func TestStreamingWordsSlideInWithoutMovingRetainedText(t *testing.T) {
	r, canvas, now := gpuTestRenderer(t)
	r.state.Phase = "listening"
	r.state.Conversation = &show.Conversation{TurnID: "words", Phase: "listening", UserText: "Turn on"}
	initial, ok := r.prepareGPUScene(canvas, now, true, true)
	if !ok || initial.effects.Text == nil || len(initial.effects.Text.Pixels) == 0 {
		t.Fatal("initial conversation did not upload a glyph atlas")
	}
	settled, _ := r.prepareGPUScene(canvas, now.Add(200*time.Millisecond), true, true)
	if len(settled.effects.Text.Quads) != 2 || len(settled.source) != 0 || len(settled.effects.Text.Pixels) != 0 {
		t.Fatal("retained words should animate without retransmitting text/background pixels")
	}
	r.state.Conversation.UserText = "Turn on the lights"
	added, _ := r.prepareGPUScene(canvas, now.Add(210*time.Millisecond), true, true)
	if len(added.source) != 0 || added.effects.Blend != 1 {
		t.Fatal("a streamed word restarted the whole-screen crossfade")
	}
	moving, _ := r.prepareGPUScene(canvas, now.Add(270*time.Millisecond), true, true)
	if len(moving.effects.Text.Quads) != 4 {
		t.Fatal("new words were not animated independently")
	}
	for index := 0; index < 2; index++ {
		before, after := settled.effects.Text.Quads[index], moving.effects.Text.Quads[index]
		if before.X != after.X || before.Y != after.Y || after.Opacity != 1 {
			t.Fatalf("retained word %d moved or faded: before=%+v after=%+v", index, before, after)
		}
	}
	word := r.gpuScene.words.current[3]
	x, y, alpha := word.at(now.Add(270 * time.Millisecond))
	if x != float64(word.word.x) || y <= float64(word.word.y) || alpha <= 0 || alpha >= 1 {
		t.Fatalf("new word did not rise and fade: x=%f y=%f alpha=%f", x, y, alpha)
	}
}

func TestWordScrollKeepsRepeatedWordsAndBoundsLongTokens(t *testing.T) {
	r, canvas, now := gpuTestRenderer(t)
	r.updateConversationWords([]conversationWord{
		{value: "go", x: 20, y: 200, size: 34}, {value: "go", x: 70, y: 200, size: 34},
	}, "scroll", now)
	r.updateConversationWords([]conversationWord{
		{value: "go", x: 20, y: 158, size: 34}, {value: "go", x: 70, y: 158, size: 34},
	}, "scroll", now.Add(time.Second))
	if len(r.gpuScene.words.outgoing) != 0 {
		t.Fatal("repeated retained words were mistaken for replacements")
	}
	_, movingY, alpha := r.gpuScene.words.current[1].at(now.Add(1090 * time.Millisecond))
	if movingY >= 200 || movingY <= 158 || alpha != 1 {
		t.Fatal("line scrolling did not preserve opaque words while moving smoothly")
	}
	r.state.Phase = "listening"
	r.state.Conversation = &show.Conversation{TurnID: "long", Phase: "listening", UserText: strings.Repeat("long_token", 200)}
	frame, _ := r.prepareGPUScene(canvas, now.Add(2*time.Second), true, true)
	if frame.effects.Text == nil || r.gpuScene.words.current[0].atlas.Max.X > frame.effects.Text.Width {
		t.Fatal("long token escaped its glyph atlas")
	}
}

func TestPartialCorrectionFadesOutAndRapidUpdatesKeepTheirProgress(t *testing.T) {
	r, canvas, now := gpuTestRenderer(t)
	r.state.Phase = "listening"
	r.state.Conversation = &show.Conversation{TurnID: "words", Phase: "listening", UserText: "Turn on the kitc"}
	r.prepareGPUScene(canvas, now, true, true)
	r.prepareGPUScene(canvas, now.Add(200*time.Millisecond), true, true)
	r.state.Conversation.UserText = "Turn on the kitchen"
	r.prepareGPUScene(canvas, now.Add(210*time.Millisecond), true, true)
	if len(r.gpuScene.words.outgoing) != 1 || r.gpuScene.words.outgoing[0].word.value != "kitc" {
		t.Fatal("corrected partial text was not retained for its fade-out")
	}
	_, oldY, oldAlpha := r.gpuScene.words.outgoing[0].at(now.Add(270 * time.Millisecond))
	if oldY >= float64(r.gpuScene.words.outgoing[0].word.y) || oldAlpha <= 0 || oldAlpha >= 1 {
		t.Fatal("replaced word did not slide up and fade away")
	}
	r.state.Conversation.UserText = "Turn on the kitchen lights"
	r.prepareGPUScene(canvas, now.Add(270*time.Millisecond), true, true)
	if !r.gpuScene.words.current[3].started.Equal(now.Add(210 * time.Millisecond)) {
		t.Fatal("new streaming content restarted an earlier word's animation")
	}
	r.prepareGPUScene(canvas, now.Add(time.Second), true, true)
	frame, _ := r.gpuScene.words.frame(now.Add(time.Second))
	if len(frame.Quads) != 5 {
		t.Fatal("old partial word survived after its fade-out")
	}
}

func TestWordAnimationPresentsFinalOpaqueTextWhenGlowIsDisabled(t *testing.T) {
	r, canvas, now := gpuTestRenderer(t)
	r.state.DisplayEdgeGlow = false
	r.state.Phase = "listening"
	r.state.Conversation = &show.Conversation{TurnID: "words", Phase: "listening", UserText: "Hello Tater"}
	r.prepareGPUScene(canvas, now, true, true)
	r.prepareGPUScene(canvas, now.Add(150*time.Millisecond), true, true)
	final, _ := r.prepareGPUScene(canvas, now.Add(200*time.Millisecond), true, true)
	if !final.present || len(final.effects.Text.Quads) != 2 || final.effects.Text.Quads[1].Opacity != 1 {
		t.Fatal("word animation did not submit its final fully opaque frame")
	}
	settled, _ := r.prepareGPUScene(canvas, now.Add(250*time.Millisecond), true, true)
	if settled.present {
		t.Fatal("settled text with no glow should stop rendering")
	}
}
