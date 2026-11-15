//go:build linux

package main

import (
	"image"
	"math"
	"time"
	"unicode"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

const (
	matrixRevealDuration = 480 * time.Millisecond
	matrixAtlasSteps     = 8
	matrixSymbols        = "0123456789#$%&*+-=<>?/\\[]{}|:;!"
)

func matrixMix(value uint64) uint64 {
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	return value ^ (value >> 31)
}

// A private deterministic seed avoids global RNG contention and frame-to-frame
// rerasterization. Repeated words and separate turns get independent patterns.
func matrixWordSeed(context, value string, index int, now time.Time) uint64 {
	seed := uint64(14695981039346656037)
	for _, text := range []string{context, "\x00", value} {
		for index := 0; index < len(text); index++ {
			seed = (seed ^ uint64(text[index])) * 1099511628211
		}
	}
	return matrixMix(seed ^ uint64(index)*0x9e3779b97f4a7c15 ^ uint64(now.UnixNano()))
}

func (word animatedConversationWord) matrixProgress(now time.Time) float64 {
	if word.matrixStarted.IsZero() {
		return 1
	}
	return math.Max(0, math.Min(1, float64(now.Sub(word.matrixStarted))/float64(matrixRevealDuration)))
}

func (word animatedConversationWord) matrixStep(progress float64) int {
	last := len(word.matrixAtlases) - 1
	if progress >= 1 || word.matrixFrom >= 1 {
		return max(0, last)
	}
	return max(0, min(last, int((progress-word.matrixFrom)/(1-word.matrixFrom)*float64(last))))
}

func matrixRuneResolved(value rune, index int, seed uint64, progress float64) bool {
	if progress >= 1 || unicode.IsSpace(value) || unicode.IsMark(value) || unicode.Is(unicode.Cf, value) {
		return true
	}
	// Each position locks independently: a later letter can finish before the
	// first. Once resolved it never becomes a symbol again in the same word.
	threshold := .12 + .88*float64(matrixMix(seed^uint64(index+1)*0x9e3779b97f4a7c15)&0xffff)/65535
	return progress >= threshold
}

func matrixSymbol(face font.Face, advance fixed.Int26_6, index int, seed uint64, progress float64) rune {
	start := int(matrixMix(seed^uint64(index+1)*0xd6e8feb86659fd93^uint64(progress*32)) % uint64(len(matrixSymbols)))
	for offset := 0; offset < len(matrixSymbols); offset++ {
		value := rune(matrixSymbols[(start+offset)%len(matrixSymbols)])
		if width, ok := face.GlyphAdvance(value); ok && width <= advance {
			return value
		}
	}
	return '.'
}

func (r *renderer) matrixWordFace(word conversationWord) font.Face {
	if word.bold {
		return r.faces.bold[word.size]
	}
	return r.faces.regular[word.size]
}

type matrixAtlasEntry struct {
	word    *animatedConversationWord
	current bool
}

// Keep one quad per word. A small set of glyph variants is uploaded once;
// ordinary animation ticks only select atlas rectangles on the existing GPU
// protocol. The atlas and quad budgets do not grow with animation duration.
func packMatrixAtlas(entries []matrixAtlasEntry, width, steps int) (int, bool) {
	x, y, rowHeight := 0, 0, 0
	for _, item := range entries {
		entry := item.word
		entry.matrixAtlases = nil
		count := 1
		if item.current && entry.matrixFrom < 1 && steps > 0 {
			count += steps
			entry.matrixAtlases = make([]image.Rectangle, count)
		}
		if entry.bounds.Dx() > width {
			return 0, false
		}
		for step := 0; step < count; step++ {
			if x+entry.bounds.Dx() > width {
				x, y, rowHeight = 0, y+rowHeight, 0
			}
			if y+entry.bounds.Dy() > conversationAtlasTextLimit {
				return 0, false
			}
			entry.atlas = image.Rect(x, y, x+entry.bounds.Dx(), y+entry.bounds.Dy())
			if count > 1 {
				entry.matrixAtlases[step] = entry.atlas
			}
			x += entry.bounds.Dx()
			rowHeight = max(rowHeight, entry.bounds.Dy())
		}
	}
	return max(64, ((y+rowHeight+63)/64)*64), true
}

func (r *renderer) buildMatrixConversationAtlas(state *conversationWordState, now time.Time) {
	entries := make([]matrixAtlasEntry, 0, len(state.current)+len(state.outgoing))
	for index := range state.current {
		entries = append(entries, matrixAtlasEntry{&state.current[index], true})
	}
	for index := range state.outgoing {
		entries = append(entries, matrixAtlasEntry{&state.outgoing[index], false})
	}
	for _, item := range entries {
		entry := item.word
		face := r.matrixWordFace(entry.word)
		bounds, advance := font.BoundString(face, entry.word.value)
		metrics := face.Metrics()
		entry.bounds = image.Rect(min(-2, bounds.Min.X.Floor()-2), min(-metrics.Ascent.Ceil()-2, bounds.Min.Y.Floor()-2),
			max(advance.Ceil()+2, bounds.Max.X.Ceil()+2), max(metrics.Descent.Ceil()+2, bounds.Max.Y.Ceil()+2))
		// Start regenerated variants at today's progress, never at zero. This
		// prevents a streamed append or a smaller atlas budget undoing letters.
		entry.matrixFrom = entry.matrixProgress(now)
	}
	width, height := 1024, 0
	if sourceWidth, _ := r.showSize(); sourceWidth > 1024 {
		width = 2048
	}
	packed := false
	for steps := matrixAtlasSteps; steps > 0; steps-- {
		if height, packed = packMatrixAtlas(entries, width, steps); packed {
			break
		}
	}
	if !packed {
		// Outgoing revisions may be discarded before any live text. Fewer
		// variants reduce upload size; they never reduce visible text content.
		state.outgoing = nil
		entries = entries[:len(state.current)]
		for steps := matrixAtlasSteps; steps >= 0; steps-- {
			if height, packed = packMatrixAtlas(entries, width, steps); packed {
				break
			}
		}
	}
	state.staticAtlas, state.upload = !packed, true
	if !packed {
		// Defensive fallback for pathological input: retain legible final text
		// in one screen-sized quad instead of exceeding the helper's limits.
		w, h := r.showSize()
		state.atlas = image.NewRGBA(image.Rect(0, 0, w, h))
		for index := range state.current {
			entry := &state.current[index]
			entry.matrixStarted, entry.matrixFrom = time.Time{}, 1
			r.text(state.atlas, entry.word.x, entry.word.y, entry.word.value, entry.word.size, entry.word.bold, entry.word.ink)
		}
		return
	}
	state.atlas = image.NewRGBA(image.Rect(0, 0, width, height))
	for _, item := range entries {
		entry := item.word
		if len(entry.matrixAtlases) == 0 {
			if item.current {
				// A budget fallback that displays final text is final: later
				// updates must never turn those readable letters into symbols.
				entry.matrixStarted, entry.matrixFrom = time.Time{}, 1
			}
			r.text(state.atlas, entry.atlas.Min.X-entry.bounds.Min.X, entry.atlas.Min.Y-entry.bounds.Min.Y,
				entry.word.value, entry.word.size, entry.word.bold, entry.word.ink)
			continue
		}
		for step, tile := range entry.matrixAtlases {
			progress := entry.matrixFrom + (1-entry.matrixFrom)*float64(step)/float64(len(entry.matrixAtlases)-1)
			if step == len(entry.matrixAtlases)-1 {
				progress = 1
			}
			r.drawMatrixWord(state.atlas, tile, *entry, progress)
		}
	}
}

func (r *renderer) drawMatrixWord(canvas *image.RGBA, tile image.Rectangle, entry animatedConversationWord, progress float64) {
	word := entry.word
	x, y := tile.Min.X-entry.bounds.Min.X, tile.Min.Y-entry.bounds.Min.Y
	if progress >= 1 {
		// Exactly the ordinary font renderer, including kerning and Unicode.
		r.text(canvas, x, y, word.value, word.size, word.bold, word.ink)
		return
	}
	face := r.matrixWordFace(word)
	ink := image.NewUniform(word.ink)
	symbolInk := image.NewUniform(mix(word.ink, r.menuPalette().primary, .6))
	drawer := font.Drawer{Dst: canvas.SubImage(tile).(*image.RGBA), Face: face}
	position := fixed.I(x)
	var previous rune
	for index, value := range []rune(word.value) {
		if index > 0 {
			position += face.Kern(previous, value)
		}
		advance, _ := face.GlyphAdvance(value)
		drawer.Dot, drawer.Src = fixed.Point26_6{X: position, Y: fixed.I(y)}, ink
		shown := value
		if !matrixRuneResolved(value, index, entry.matrixSeed, progress) && advance > 0 {
			shown = matrixSymbol(face, advance, index, entry.matrixSeed, progress)
			width, _ := face.GlyphAdvance(shown)
			drawer.Dot.X += (advance - width) / 2
			drawer.Src = symbolInk
		}
		drawer.DrawString(string(shown))
		position += advance
		previous = value
	}
}
