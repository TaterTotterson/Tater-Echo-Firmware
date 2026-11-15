//go:build linux

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/gpucompositor"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/linuxinput"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

func matrixTestRenderer(t *testing.T) (*renderer, *image.RGBA, time.Time) {
	t.Helper()
	r, canvas, now := gpuTestRenderer(t)
	r.state.Settings = show.ScreenSettingDefaults()
	r.state.Settings["screen_text_style"] = "matrix"
	r.state.DisplayEdgeGlow = false
	r.state.Phase = "listening"
	r.state.Conversation = &show.Conversation{TurnID: "matrix-turn", Phase: "listening", UserText: "Turn on the kitchen lights"}
	return r, canvas, now
}

func TestMatrixChoiceInAppearanceCyclesAndSendsOnlyLocalPreference(t *testing.T) {
	r, _, _ := gpuTestRenderer(t)
	r.settingsOpen, r.settingsPage = true, "appearance"
	_, rows := r.settingRows()
	if len(rows) > 5 {
		t.Fatal("appearance options no longer fit on one Show screen")
	}
	index := -1
	for at, row := range rows {
		if row.key == "screen_text_style" {
			index = at
			if row.kind != "choice" || row.options != "smooth|matrix" {
				t.Fatal("invalid conversation style control")
			}
		}
	}
	if index < 0 || r.settingValue("screen_text_style") != "smooth" {
		t.Fatal("missing Matrix option or legacy default")
	}
	for _, expected := range []string{"matrix", "smooth"} {
		var wire bytes.Buffer
		client := &socketClient{writer: bufio.NewWriter(&wire)}
		bounds := settingsRowBounds(960, 480, index, false)
		r.handleSettingsTouch(client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: bounds.Max.X - 40, Y: bounds.Min.Y + bounds.Dy()/2}, 960, 480)
		var command show.Command
		if err := json.Unmarshal(bytes.TrimSpace(wire.Bytes()), &command); err != nil {
			t.Fatal(err)
		}
		if command.Action != "screen.settings.set" || len(command.Settings) != 1 || command.Settings["screen_text_style"] != expected || r.settingValue("screen_text_style") != expected {
			t.Fatalf("incorrect style preference request: %+v", command)
		}
	}
}

func TestMatrixSymbolsResolveIndependentlyAndMonotonically(t *testing.T) {
	const value = "Matrixcharacters"
	seed := matrixWordSeed("turn", value, 0, time.Unix(1800000000, 0))
	sawOutOfOrder := false
	for step := 0; step <= 100; step++ {
		progress := float64(step) / 100
		unresolvedBefore := false
		for index, letter := range value {
			resolved := matrixRuneResolved(letter, index, seed, progress)
			if progress == 0 && resolved || progress == 1 && !resolved {
				t.Fatal("Matrix animation does not start scrambled and end with real text")
			}
			if resolved && unresolvedBefore {
				sawOutOfOrder = true
			}
			unresolvedBefore = unresolvedBefore || !resolved
			if resolved && !matrixRuneResolved(letter, index, seed, progress+.01) {
				t.Fatal("a resolved letter scrambled again")
			}
		}
	}
	if !sawOutOfOrder {
		t.Fatal("letters only settled in left-to-right order")
	}
	if seed == matrixWordSeed("another-turn", value, 0, time.Unix(1800000000, 0)) || seed == matrixWordSeed("turn", value, 1, time.Unix(1800000000, 0)) {
		t.Fatal("turns or repeated words shared the same pattern")
	}
}

func TestMatrixRetainsAtlasAndFinishesWithExactOriginalGlyphs(t *testing.T) {
	r, canvas, now := matrixTestRenderer(t)
	r.state.Conversation.UserText = "Café déjà vu"
	initial, ok := r.prepareGPUScene(canvas, now, true, true)
	if !ok || initial.effects.Text == nil || len(initial.effects.Text.Pixels) == 0 {
		t.Fatal("Matrix glyph variants were not uploaded")
	}
	atlas := r.gpuScene.words.atlas
	changed := false
	middle, _ := r.prepareGPUScene(canvas, now.Add(220*time.Millisecond), true, true)
	if r.gpuScene.words.atlas != atlas || len(middle.effects.Text.Pixels) != 0 || len(middle.source) != 0 {
		t.Fatal("a Matrix animation tick rerasterized/reuploaded pixels")
	}
	for index, quad := range middle.effects.Text.Quads {
		changed = changed || quad.Atlas != initial.effects.Text.Quads[index].Atlas
		if quad.Y != initial.effects.Text.Quads[index].Y || quad.X != initial.effects.Text.Quads[index].X {
			t.Fatal("symbol changes moved the text baselines")
		}
	}
	if !changed {
		t.Fatal("the GPU kept displaying the original symbols")
	}
	final, _ := r.prepareGPUScene(canvas, now.Add(700*time.Millisecond), true, true)
	if !final.present {
		t.Fatal("Matrix failed to submit its final readable frame")
	}
	for index, entry := range r.gpuScene.words.current {
		quad := final.effects.Text.Quads[index]
		if quad.Atlas != entry.atlas || quad.Opacity != 1 {
			t.Fatal("final Matrix text is scrambled or translucent")
		}
		expected := image.NewRGBA(image.Rect(0, 0, entry.bounds.Dx(), entry.bounds.Dy()))
		r.text(expected, -entry.bounds.Min.X, -entry.bounds.Min.Y, entry.word.value, entry.word.size, entry.word.bold, entry.word.ink)
		actual := image.NewRGBA(expected.Bounds())
		draw.Draw(actual, actual.Bounds(), atlas, entry.atlas.Min, draw.Src)
		if !bytes.Equal(actual.Pix, expected.Pix) {
			t.Fatalf("settled Unicode word %q differs from the ordinary font renderer", entry.word.value)
		}
	}
	settled, _ := r.prepareGPUScene(canvas, now.Add(800*time.Millisecond), true, true)
	if settled.present || r.gpuScene.words.atlas != atlas {
		t.Fatal("settled Matrix text keeps drawing without glow or other motion")
	}
}

func TestMatrixStreamingAndThinkingDoNotRestartSettledOrInFlightWords(t *testing.T) {
	r, canvas, now := matrixTestRenderer(t)
	r.prepareGPUScene(canvas, now, true, true)
	r.prepareGPUScene(canvas, now.Add(220*time.Millisecond), true, true)
	previous := append([]animatedConversationWord(nil), r.gpuScene.words.current...)
	r.state.Conversation.UserText += " please"
	updated, _ := r.prepareGPUScene(canvas, now.Add(240*time.Millisecond), true, true)
	if len(updated.source) != 0 {
		t.Fatal("streaming Matrix words restarted the background transition")
	}
	for index, old := range previous {
		next := r.gpuScene.words.current[index]
		if next.matrixSeed != old.matrixSeed || next.matrixStarted != old.matrixStarted || next.matrixFrom < old.matrixProgress(now.Add(220*time.Millisecond)) {
			t.Fatal("a streamed append restarted or rewound an existing word")
		}
	}
	r.prepareGPUScene(canvas, now.Add(time.Second), true, true)
	r.state.Conversation.Phase, r.state.Phase = "thinking", "thinking"
	r.prepareGPUScene(canvas, now.Add(1100*time.Millisecond), true, true)
	for _, entry := range r.gpuScene.words.current {
		if len(entry.matrixAtlases) != 0 {
			t.Fatal("thinking reshuffled already settled STT")
		}
	}
	r.state.Conversation.Phase, r.state.Phase = "replying", "speaking"
	r.state.Conversation.AssistantText = "The lights are now on"
	reply, _ := r.prepareGPUScene(canvas, now.Add(1200*time.Millisecond), true, true)
	if reply.effects.Text == nil || len(r.gpuScene.words.current[0].matrixAtlases) < 2 || len(r.gpuScene.words.outgoing) != 0 {
		t.Fatal("reply text did not get a fresh reveal, or retained stale STT glyphs")
	}
	oldSeed := r.gpuScene.words.current[0].matrixSeed
	r.state.Conversation.TurnID = "next-turn"
	r.prepareGPUScene(canvas, now.Add(1300*time.Millisecond), true, true)
	if r.gpuScene.words.current[0].matrixSeed == oldSeed || len(r.gpuScene.words.outgoing) != 0 {
		t.Fatal("a new turn reused the previous reveal")
	}
}

func TestMatrixAtlasAndQuadsStayBoundedForLongRapidRevisions(t *testing.T) {
	for _, target := range []string{"checkers", "cronos", "crown", "athena"} {
		t.Run(target, func(t *testing.T) {
			r, _, now := matrixTestRenderer(t)
			r.target = target
			if target == "crown" || target == "athena" {
				r.screenWidth, r.screenHeight = 1280, 800
			}
			width, height := r.showSize()
			canvas := image.NewRGBA(image.Rect(0, 0, width, height))
			maximumWidth := 1024
			if width > 1024 {
				maximumWidth = 2048
			}
			for revision := 0; revision < 6; revision++ {
				r.state.Conversation.UserText = strings.Repeat(fmt.Sprintf("i go %d café ", revision), 100)
				frame, _ := r.prepareGPUScene(canvas, now.Add(time.Duration(revision)*30*time.Millisecond), true, true)
				text := frame.effects.Text
				if text == nil || text.Width > maximumWidth || text.Height > 1024 || len(text.Quads) > gpucompositor.MaxTextQuads {
					t.Fatal("rapid revisions exceeded the existing GPU protocol budget")
				}
				for _, quad := range text.Quads {
					if !quad.Atlas.In(image.Rect(0, 0, text.Width, text.Height)) {
						t.Fatal("a Matrix glyph lies outside the uploaded atlas")
					}
				}
				bodySize, maxWidth, maxLines := 34, width*46/100, 4
				if height >= 600 {
					bodySize, maxWidth, maxLines = 48, width*54/100, 5
				}
				want := strings.Fields(strings.Join(flowingConversationLines(r.state.Conversation.UserText, r.faces.regular[bodySize], maxWidth, maxLines), " "))
				var actual []string
				for _, entry := range r.gpuScene.words.current {
					actual = append(actual, strings.Fields(entry.word.value)...)
				}
				if strings.Join(actual, " ") != strings.Join(want, " ") {
					t.Fatal("atlas budgeting dropped visible conversation text")
				}
			}
		})
	}
}

func TestMatrixStyleSwitchAndCPUFallbackKeepLegacyBehavior(t *testing.T) {
	r, canvas, now := matrixTestRenderer(t)
	r.state.Settings["screen_text_style"] = "smooth"
	r.prepareGPUScene(canvas, now, true, true)
	for _, entry := range r.gpuScene.words.current {
		if !entry.matrixStarted.IsZero() || len(entry.matrixAtlases) != 0 {
			t.Fatal("Smooth unexpectedly used the Matrix effect")
		}
	}
	r.prepareGPUScene(canvas, now.Add(time.Second), true, true)
	r.state.Settings["screen_text_style"] = "matrix"
	r.prepareGPUScene(canvas, now.Add(1100*time.Millisecond), true, true)
	if len(r.gpuScene.words.current[0].matrixAtlases) < 2 {
		t.Fatal("changing the style failed to invalidate the retained text")
	}
	a, b := image.NewRGBA(canvas.Bounds()), image.NewRGBA(canvas.Bounds())
	r.drawShowConversation(a, 1, color.RGBA{}, false)
	r.state.Settings["screen_text_style"] = "smooth"
	r.drawShowConversation(b, 1, color.RGBA{}, false)
	if !bytes.Equal(a.Pix, b.Pix) {
		t.Fatal("a GPU-only effect changed the safe software rendering fallback")
	}
}

func TestMatrixPathologicalTokenFallsBackWithoutRestartingOrOversizedAtlas(t *testing.T) {
	r, _, now := matrixTestRenderer(t)
	words := []conversationWord{{value: strings.Repeat("W", 400), x: 20, y: 200, size: 34, ink: conversationText}}
	r.updateConversationWords(words, "oversize", now)
	state := &r.gpuScene.words
	frame, moving := state.frame(now)
	if !state.staticAtlas || frame.Width > 1024 || frame.Height > 1024 || len(frame.Quads) != 1 || frame.Quads[0].Atlas != image.Rect(0, 0, 960, 480) || moving {
		t.Fatal("pathological input exceeded the atlas limit instead of using readable static text")
	}
	if !state.current[0].matrixStarted.IsZero() {
		t.Fatal("budget fallback can re-scramble text that was already displayed in full")
	}
	atlas := state.atlas
	frame, moving = state.frame(now.Add(time.Second))
	if moving || len(frame.Pixels) != 0 || state.atlas != atlas {
		t.Fatal("static fallback continues rebuilding or uploading pixels")
	}
}

func compositeConversationFrame(r *renderer, frame gpuSceneFrame) *image.RGBA {
	output := image.NewRGBA(r.gpuScene.layer.Bounds())
	draw.Draw(output, output.Bounds(), r.gpuScene.layer, image.Point{}, draw.Src)
	for _, quad := range frame.effects.Text.Quads {
		bounds := image.Rect(int(quad.X), int(quad.Y), int(quad.X)+quad.Atlas.Dx(), int(quad.Y)+quad.Atlas.Dy())
		draw.DrawMask(output, bounds, r.gpuScene.words.atlas, quad.Atlas.Min,
			image.NewUniform(color.Alpha{A: uint8(quad.Opacity * 255)}), image.Point{}, draw.Over)
	}
	return output
}

func TestSettledMatrixMatchesSmoothConversationPixels(t *testing.T) {
	for _, target := range []string{"checkers", "cronos", "crown", "athena"} {
		for _, phase := range []string{"listening", "thinking", "replying"} {
			t.Run(target+"/"+phase, func(t *testing.T) {
				var expected []byte
				for _, style := range []string{"smooth", "matrix"} {
					r, canvas, now := matrixTestRenderer(t)
					r.target = target
					if target == "crown" || target == "athena" {
						r.screenWidth, r.screenHeight = 1280, 800
						canvas = image.NewRGBA(image.Rect(0, 0, 1280, 800))
					}
					r.state.Settings["screen_text_style"] = style
					r.state.Conversation.Phase, r.state.Phase = phase, phase
					r.state.Conversation.UserText = "Could you turn on the kitchen lights?"
					r.state.Conversation.AssistantText = "The kitchen lights are on. Café lighting looks good!"
					r.prepareGPUScene(canvas, now, true, true)
					frame, _ := r.prepareGPUScene(canvas, now.Add(time.Second), true, true)
					pixels := compositeConversationFrame(r, frame).Pix
					if expected == nil {
						expected = pixels
					} else if !bytes.Equal(expected, pixels) {
						t.Fatal("settled Matrix changes the final text, kerning, layout or static labels")
					}
				}
			})
		}
	}
}

func BenchmarkMatrixConversationFrame(b *testing.B) {
	faces, err := newFaceSet()
	if err != nil {
		b.Fatal(err)
	}
	r := &renderer{faces: faces, target: "checkers", screenWidth: 960, screenHeight: 480,
		state: show.Snapshot{Settings: map[string]any{"screen_text_style": "matrix"}}}
	now := time.Unix(1800000000, 0)
	r.updateConversationWords([]conversationWord{{value: "kitchen", x: 200, y: 190, size: 34, ink: conversationText}}, "benchmark", now)
	r.gpuScene.words.frame(now) // upload only once
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		frame, _ := r.gpuScene.words.frame(now.Add(200 * time.Millisecond))
		if len(frame.Pixels) != 0 {
			b.Fatal("animation tick uploaded pixels")
		}
	}
}

func TestMatrixPreview(t *testing.T) {
	directory := os.Getenv("TATER_MATRIX_PREVIEW_DIR")
	if directory == "" {
		t.Skip("set TATER_MATRIX_PREVIEW_DIR for generated conversation previews")
	}
	for _, target := range []string{"checkers", "athena"} {
		r, canvas, now := matrixTestRenderer(t)
		r.target = target
		if target == "athena" {
			r.screenWidth, r.screenHeight = 1280, 800
			canvas = image.NewRGBA(image.Rect(0, 0, 1280, 800))
		}
		r.state.Conversation.UserText = "Could you turn on the kitchen lights?"
		for index, elapsed := range []time.Duration{0, 100, 220, 360, 700} {
			frame, _ := r.prepareGPUScene(canvas, now.Add(elapsed*time.Millisecond), true, true)
			output := compositeConversationFrame(r, frame)
			if index == 4 {
				for _, entry := range r.gpuScene.words.current {
					expected := image.NewRGBA(image.Rect(0, 0, entry.bounds.Dx(), entry.bounds.Dy()))
					r.text(expected, -entry.bounds.Min.X, -entry.bounds.Min.Y, entry.word.value, entry.word.size, entry.word.bold, entry.word.ink)
					actual := image.NewRGBA(expected.Bounds())
					draw.Draw(actual, actual.Bounds(), r.gpuScene.words.atlas, entry.atlas.Min, draw.Src)
					if !bytes.Equal(actual.Pix, expected.Pix) {
						t.Fatalf("%s final word %q tile is corrupt: atlas=%v bounds=%v", target, entry.word.value, entry.atlas, entry.bounds)
					}
				}
			}
			file, err := os.Create(filepath.Join(directory, fmt.Sprintf("matrix-%s-%d.png", target, index)))
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(file, output)
			file.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}
