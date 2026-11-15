//go:build linux

package main

import (
	"image"
	"image/color"
	"math"
	"strings"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/gpucompositor"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
	"golang.org/x/image/font"
)

const conversationWordDuration = 180 * time.Millisecond

type conversationWord struct {
	value string
	x, y  int
	size  int
	bold  bool
	ink   color.RGBA
}

func (r *renderer) captureConversationWords(value string, centerX, baseline, width, lines, size int, ink color.RGBA, highlight bool) {
	wrapped := flowingConversationLines(value, r.faces.regular[size], width, lines)
	for lineIndex, line := range wrapped {
		words := strings.Fields(line)
		prefix := ""
		for wordIndex, value := range words {
			shown := value
			if font.MeasureString(r.faces.regular[size], shown).Ceil() > width {
				shown = fitTextToWidth(shown, r.faces.regular[size], width)
			}
			word := conversationWord{value: shown, x: centerX - width/2 + font.MeasureString(r.faces.regular[size], prefix).Round(),
				y: baseline + lineIndex*(size+max(8, size/4)), size: size, ink: ink}
			if highlight && lineIndex == len(wrapped)-1 && wordIndex == len(words)-1 {
				word.bold = true
				if len(words) > 1 {
					word.ink = mix(ink, color.RGBA{255, 226, 203, ink.A}, .28)
				}
			}
			*r.gpuTextCapture = append(*r.gpuTextCapture, word)
			prefix += value + " "
		}
	}
}

type animatedConversationWord struct {
	word                    conversationWord
	fromX, fromY, fromAlpha float64
	toX, toY, toAlpha       float64
	started                 time.Time
	duration                time.Duration
	atlas, bounds           image.Rectangle
	matrixStarted           time.Time
	matrixSeed              uint64
	matrixAtlases           []image.Rectangle
	matrixFrom              float64
}

func (word animatedConversationWord) at(now time.Time) (x, y, alpha float64) {
	progress := 1.0
	if word.duration > 0 {
		progress = math.Max(0, math.Min(1, float64(now.Sub(word.started))/float64(word.duration)))
	}
	// A short ease-out keeps up with speech while gently settling into place.
	eased := 1 - math.Pow(1-progress, 3)
	return word.fromX + (word.toX-word.fromX)*eased,
		word.fromY + (word.toY-word.fromY)*eased,
		word.fromAlpha + (word.toAlpha-word.fromAlpha)*eased
}

type conversationWordState struct {
	current, outgoing []animatedConversationWord
	atlas             *image.RGBA
	context           string
	upload            bool
	style             string
	staticAtlas       bool
	textBounds        image.Rectangle
	dotSprites        [thinkingDotSteps]image.Rectangle
}

// Matching the retained sequence keeps already-visible words steady, including
// repeated words, partial revisions, and the upward scroll when lines overflow.
func matchConversationWords(previous []animatedConversationWord, next []conversationWord) map[int]int {
	stride := len(next) + 1
	lcs := make([]int, (len(previous)+1)*stride)
	for old := len(previous) - 1; old >= 0; old-- {
		for new := len(next) - 1; new >= 0; new-- {
			if previous[old].word.value == next[new].value {
				lcs[old*stride+new] = 1 + lcs[(old+1)*stride+new+1]
			} else {
				lcs[old*stride+new] = max(lcs[(old+1)*stride+new], lcs[old*stride+new+1])
			}
		}
	}
	matches := make(map[int]int)
	for old, new := 0, 0; old < len(previous) && new < len(next); {
		if previous[old].word.value == next[new].value {
			matches[new] = old
			old++
			new++
		} else if lcs[(old+1)*stride+new] > lcs[old*stride+new+1] {
			old++
		} else {
			new++
		}
	}
	return matches
}

func (r *renderer) updateConversationWords(words []conversationWord, context string, now time.Time) {
	state := &r.gpuScene.words
	style := show.NormalizeTextStyle(r.settingValue("screen_text_style"))
	if context != state.context || style != state.style {
		*state = conversationWordState{context: context, style: style}
	}
	// Tiny-word paragraphs can exceed the quad budget. Group neighboring words
	// on the same baseline rather than dropping any visible text.
	if len(words) > 64 {
		groupSize := (len(words) + 63) / 64
		grouped := make([]conversationWord, 0, 64)
		for index := 0; index < len(words); {
			word := words[index]
			index++
			for joined := 1; joined < groupSize && index < len(words) && words[index].y == word.y; joined++ {
				word.value += " " + words[index].value
				index++
			}
			grouped = append(grouped, word)
		}
		words = grouped
	}
	matches := matchConversationWords(state.current, words)
	used := make(map[int]bool)
	next := make([]animatedConversationWord, 0, len(words))
	for index, word := range words {
		entry := animatedConversationWord{word: word, fromX: float64(word.x), fromY: float64(word.y + 8),
			toX: float64(word.x), toY: float64(word.y), toAlpha: 1, started: now, duration: conversationWordDuration}
		if style == "matrix" {
			entry.matrixSeed = matrixWordSeed(context, word.value, index, now)
			entry.matrixStarted = now.Add(time.Duration(entry.matrixSeed%71) * time.Millisecond)
			entry.fromY, entry.fromAlpha, entry.duration = float64(word.y), 1, 0
		}
		if old, ok := matches[index]; ok {
			used[old] = true
			entry = state.current[old]
			if entry.word.x != word.x || entry.word.y != word.y {
				entry.fromX, entry.fromY, entry.fromAlpha = entry.at(now)
				entry.toX, entry.toY = float64(word.x), float64(word.y)
				entry.started, entry.duration = now, conversationWordDuration
			}
			entry.word = word
		}
		next = append(next, entry)
	}
	outgoing := state.outgoing[:0]
	for _, entry := range state.outgoing {
		if _, _, alpha := entry.at(now); alpha > .001 {
			outgoing = append(outgoing, entry)
		}
	}
	for old, entry := range state.current {
		if !used[old] {
			entry.fromX, entry.fromY, entry.fromAlpha = entry.at(now)
			entry.toX, entry.toY, entry.toAlpha = entry.fromX, entry.fromY-6, 0
			entry.started, entry.duration = now, 120*time.Millisecond
			outgoing = append(outgoing, entry)
		}
	}
	if available := gpucompositor.MaxTextQuads - 3 - len(next); len(outgoing) > available {
		outgoing = outgoing[len(outgoing)-available:]
	}
	state.current, state.outgoing = next, outgoing
	if style == "matrix" {
		r.buildMatrixConversationAtlas(state, now)
	} else {
		r.buildConversationAtlas(state)
	}
	r.appendThinkingDotAtlas(state)
}

func (r *renderer) buildConversationAtlas(state *conversationWordState) {
	var entries []*animatedConversationWord
	for index := range state.current {
		entries = append(entries, &state.current[index])
	}
	for index := range state.outgoing {
		entries = append(entries, &state.outgoing[index])
	}
	x, y, rowHeight := 0, 0, 0
	for _, entry := range entries {
		face := r.faces.regular[entry.word.size]
		if entry.word.bold {
			face = r.faces.bold[entry.word.size]
		}
		bounds, _ := font.BoundString(face, entry.word.value)
		entry.bounds = image.Rect(bounds.Min.X.Floor()-2, bounds.Min.Y.Floor()-2, bounds.Max.X.Ceil()+2, bounds.Max.Y.Ceil()+2)
		if x+entry.bounds.Dx() > 1024 {
			x, y, rowHeight = 0, y+rowHeight, 0
		}
		entry.atlas = image.Rect(x, y, x+entry.bounds.Dx(), y+entry.bounds.Dy())
		x += entry.bounds.Dx()
		rowHeight = max(rowHeight, entry.bounds.Dy())
	}
	height := max(64, ((y+rowHeight+63)/64)*64)
	state.staticAtlas = height > conversationAtlasTextLimit
	if state.staticAtlas {
		w, h := r.showSize()
		state.atlas = image.NewRGBA(image.Rect(0, 0, gpuAtlasDimension(w), h))
		for _, entry := range entries {
			r.text(state.atlas, entry.word.x, entry.word.y, entry.word.value, entry.word.size, entry.word.bold, entry.word.ink)
		}
		state.upload = true
		return
	}
	state.atlas = image.NewRGBA(image.Rect(0, 0, 1024, height))
	for _, entry := range entries {
		r.text(state.atlas, entry.atlas.Min.X-entry.bounds.Min.X, entry.atlas.Min.Y-entry.bounds.Min.Y,
			entry.word.value, entry.word.size, entry.word.bold, entry.word.ink)
	}
	state.upload = true
}

func (state *conversationWordState) frame(now time.Time) (*gpucompositor.TextFrame, bool) {
	if state.atlas == nil {
		return nil, false
	}
	frame := &gpucompositor.TextFrame{Width: state.atlas.Rect.Dx(), Height: state.atlas.Rect.Dy()}
	if state.upload {
		frame.Pixels = state.atlas.Pix
		state.upload = false
	}
	if state.staticAtlas {
		bounds := state.textBounds
		if bounds.Empty() {
			bounds = state.atlas.Bounds()
		}
		frame.Quads = []gpucompositor.TextQuad{{Atlas: bounds, Opacity: 1}}
		return frame, false
	}
	animating := false
	for _, entries := range [][]animatedConversationWord{state.outgoing, state.current} {
		for _, entry := range entries {
			x, y, alpha := entry.at(now)
			animating = animating || now.Before(entry.started.Add(entry.duration))
			atlas := entry.atlas
			if len(entry.matrixAtlases) > 0 {
				progress := entry.matrixProgress(now)
				animating = animating || progress < 1
				step := entry.matrixStep(progress)
				atlas = entry.matrixAtlases[step]
			}
			if alpha > .001 {
				frame.Quads = append(frame.Quads, gpucompositor.TextQuad{Atlas: atlas,
					X: float32(x + float64(entry.bounds.Min.X)), Y: float32(y + float64(entry.bounds.Min.Y)), Opacity: float32(alpha)})
			}
		}
	}
	return frame, animating
}
