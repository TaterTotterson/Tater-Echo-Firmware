//go:build linux

package main

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
	"golang.org/x/image/font"
)

var (
	conversationBackdrop   = color.RGBA{5, 5, 5, 255}
	conversationText       = color.RGBA{244, 241, 237, 255}
	conversationDimText    = color.RGBA{132, 132, 137, 255}
	conversationGlowLayers sync.Map
)

const (
	conversationFadeDuration            = 320 * time.Millisecond
	conversationPlaceholderFadeDuration = 280 * time.Millisecond
)

type conversationGlowCacheKey struct {
	width  int
	height int
	spot   bool
	accent color.RGBA
}

func conversationPhase(conversation *show.Conversation) string {
	if conversation == nil {
		return ""
	}
	phase := strings.ToLower(strings.TrimSpace(conversation.Phase))
	switch phase {
	case "listening", "thinking", "replying":
		return phase
	default:
		return ""
	}
}

func conversationAnimationIdentity(conversation *show.Conversation) string {
	if conversation == nil {
		return ""
	}
	if conversation.TurnID != "" {
		return conversation.TurnID
	}
	return conversationPhase(conversation)
}

func (r *renderer) noteConversationTextTransition(previous, next *show.Conversation, now time.Time) {
	if next == nil || previous == nil || conversationAnimationIdentity(next) != conversationAnimationIdentity(previous) {
		r.conversationTextStarted = time.Time{}
		r.conversationTextTurn = ""
		return
	}
	if strings.TrimSpace(previous.UserText) == "" && strings.TrimSpace(next.UserText) != "" {
		r.conversationTextStarted = now
		r.conversationTextTurn = conversationAnimationIdentity(next)
	}
}

func (r *renderer) conversationPlaceholderFade(now time.Time) (float64, bool) {
	conversation := r.state.Conversation
	if conversation == nil || strings.TrimSpace(conversation.UserText) == "" || r.conversationTextStarted.IsZero() ||
		r.conversationTextTurn != conversationAnimationIdentity(conversation) {
		return 1, false
	}
	elapsed := now.Sub(r.conversationTextStarted)
	if elapsed < 0 || elapsed >= conversationPlaceholderFadeDuration {
		return 1, false
	}
	progress := math.Max(0, math.Min(1, elapsed.Seconds()/conversationPlaceholderFadeDuration.Seconds()))
	return progress * progress * (3 - 2*progress), true
}

func conversationFadeInk(ink color.RGBA, opacity float64) color.RGBA {
	opacity = math.Max(0, math.Min(1, opacity))
	ink.A = uint8(math.Round(float64(ink.A) * opacity))
	return ink
}

func (r *renderer) conversationActive() bool {
	conversation := r.state.Conversation
	phase := conversationVisualPhase(conversation, r.state.Phase)
	return conversation != nil && phase != ""
}

func conversationVisualPhase(conversation *show.Conversation, devicePhase string) string {
	phase := conversationPhase(conversation)
	if phase == "thinking" && strings.EqualFold(strings.TrimSpace(devicePhase), "tool_call") {
		return "working"
	}
	return phase
}

func conversationAccent(phase, theme string) color.RGBA {
	switch phase {
	case "listening":
		return accentForTheme("listening", theme)
	case "thinking":
		return accentForTheme("thinking", theme)
	case "working":
		return accentForTheme("tool_call", theme)
	default:
		return accentForTheme("speaking", theme)
	}
}

func conversationStageLabel(phase string) string {
	switch phase {
	case "listening":
		return "LISTENING"
	case "thinking":
		return "THINKING"
	case "working":
		return "WORKING"
	case "replying":
		return "REPLYING"
	default:
		return ""
	}
}

func flowingConversationLines(value string, face font.Face, maxWidth, maxLines int) []string {
	lines := wrapTextToWidth(strings.TrimSpace(value), face, maxWidth)
	if len(lines) <= maxLines {
		return lines
	}
	lines = append([]string(nil), lines[len(lines)-maxLines:]...)
	lines[0] = fitTextToWidth("… "+strings.TrimSpace(lines[0]), face, maxWidth)
	return lines
}

// drawConversationFlowLines keeps every line on a fixed baseline and uses a
// fixed type size. Streaming words can extend or wrap without repeatedly
// recentering and rescaling the entire paragraph.
func (r *renderer) drawConversationFlowLines(
	canvas *image.RGBA,
	value string,
	centerX, firstBaseline, maxWidth, maxLines, size int,
	ink color.RGBA,
	highlightNewest bool,
) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	lines := flowingConversationLines(value, r.faces.regular[size], maxWidth, maxLines)
	lineHeight := size + max(8, size/4)
	left := centerX - maxWidth/2
	for index, lineValue := range lines {
		baseline := firstBaseline + index*lineHeight
		if highlightNewest && index == len(lines)-1 {
			r.drawConversationHighlightedLine(canvas, left, baseline, lineValue, size, ink)
			continue
		}
		r.text(canvas, left, baseline, lineValue, size, false, ink)
	}
}

func (r *renderer) drawConversationUserText(
	canvas *image.RGBA,
	centerX, firstBaseline, maxWidth, maxLines, size int,
	ink color.RGBA,
	motion bool,
) {
	conversation := r.state.Conversation
	text := ""
	if conversation != nil {
		text = strings.TrimSpace(conversation.UserText)
	}
	if text == "" {
		r.centeredText(canvas, centerX, firstBaseline, "I’m listening", size, false, ink)
		return
	}

	progress, transitioning := 1.0, false
	if motion {
		progress, transitioning = r.conversationPlaceholderFade(time.Now())
	}
	r.drawConversationFlowLines(canvas, text, centerX, firstBaseline, maxWidth, maxLines, size, conversationFadeInk(ink, progress), true)
	if transitioning {
		r.centeredText(canvas, centerX, firstBaseline, "I’m listening", size, false, conversationFadeInk(ink, 1-progress))
	}
}

func (r *renderer) drawConversationHighlightedLine(
	canvas *image.RGBA,
	left, baseline int,
	value string,
	size int,
	ink color.RGBA,
) {
	words := strings.Fields(value)
	if len(words) < 2 {
		r.text(canvas, left, baseline, value, size, true, ink)
		return
	}
	last := words[len(words)-1]
	prefix := strings.TrimSpace(strings.TrimSuffix(value, last))
	if prefix != "" {
		prefix += " "
	}
	prefixWidth := font.MeasureString(r.faces.regular[size], prefix).Round()
	if prefix != "" {
		r.text(canvas, left, baseline, prefix, size, false, ink)
	}
	warm := mix(ink, color.RGBA{255, 226, 203, ink.A}, .28)
	warm.A = ink.A
	r.text(canvas, left+prefixWidth, baseline, last, size, true, warm)
}

func (r *renderer) drawConversationDots(canvas *image.RGBA, centerX, centerY int, seconds float64, accent color.RGBA, scale int) {
	for index := 0; index < 3; index++ {
		pulse := .5 + .5*math.Sin(seconds*3.0-float64(index)*.72)
		radius := max(5, scale/2) + int(math.Round(pulse*2))
		halo := accent
		halo.A = uint8(18 + pulse*24)
		circle(canvas, centerX+(index-1)*scale*3, centerY, radius+11, halo)
		glow := accent
		glow.A = uint8(48 + pulse*52)
		circle(canvas, centerX+(index-1)*scale*3, centerY, radius+4, glow)
		ink := accent
		ink.A = uint8(145 + pulse*105)
		circle(canvas, centerX+(index-1)*scale*3, centerY, radius, ink)
	}
}

func conversationGaussian(value, center, spread float64) float64 {
	if spread <= 0 {
		return 0
	}
	delta := (value - center) / spread
	return math.Exp(-delta * delta)
}

// drawConversationAmbientGlow gives the live conversation surface the warm,
// dimensional edge light from the UI concept. It is deliberately separate
// from the regular voice-state rim: the broad falloff establishes the visual
// surface, while drawConversationRim adds the crisp/reactive outer edge.
func (r *renderer) drawConversationAmbientGlow(canvas *image.RGBA, _ float64, accent color.RGBA) {
	if !r.state.DisplayEdgeGlow {
		draw.Draw(canvas, canvas.Bounds(), image.NewUniform(conversationBackdrop), image.Point{}, draw.Src)
		return
	}
	key := conversationGlowCacheKey{
		width: canvas.Rect.Dx(), height: canvas.Rect.Dy(), spot: r.spotScreen, accent: accent,
	}
	if cached, ok := conversationGlowLayers.Load(key); ok {
		draw.Draw(canvas, canvas.Bounds(), cached.(*image.RGBA), image.Point{}, draw.Src)
		return
	}

	// The broad edge light contains hundreds of thousands of Gaussian and
	// power calculations on Show-sized framebuffers. Its shape changes only
	// imperceptibly, so build it once per target/theme and keep the dots, text,
	// and audio-reactive rim as the live per-frame animation.
	layer := image.NewRGBA(canvas.Bounds())
	draw.Draw(layer, layer.Bounds(), image.NewUniform(conversationBackdrop), image.Point{}, draw.Src)
	if r.spotScreen {
		r.drawSpotConversationAmbientGlow(layer, 0, accent)
	} else {
		r.drawShowConversationAmbientGlow(layer, 0, accent)
	}
	conversationGlowLayers.Store(key, layer)
	draw.Draw(canvas, canvas.Bounds(), layer, image.Point{}, draw.Src)
}

func (r *renderer) drawSpotConversationAmbientGlow(canvas *image.RGBA, seconds float64, accent color.RGBA) {
	width, height := canvas.Rect.Dx(), canvas.Rect.Dy()
	cx, cy := width/2, height/2
	outer := float64(min(cx, cy) - 3)
	depth := math.Max(34, outer*.19)
	pulse := .94 + .06*math.Sin(seconds*.85)
	ink := mix(accent, color.RGBA{255, 218, 180, 255}, .05)

	for y := 0; y < height; y++ {
		dy := float64(y - cy)
		for x := 0; x < width; x++ {
			dx := float64(x - cx)
			radius := math.Hypot(dx, dy)
			inside := outer - radius
			if inside < 0 || inside > depth {
				continue
			}
			angle := math.Atan2(dy, dx)
			// Uneven pools of light keep the ring organic without making it
			// look like a spinning progress indicator.
			hotspot := .40 + .24*(.5+.5*math.Sin(angle*3.0+seconds*.18)) +
				.22*(.5+.5*math.Sin(angle*5.0-seconds*.13))
			falloff := math.Pow(1-inside/depth, 2.15)
			alpha := (15 + 72*hotspot) * falloff * pulse
			ink.A = uint8(math.Min(108, alpha))
			blendPixel(canvas, x, y, ink)
		}
	}
}

func (r *renderer) drawShowConversationAmbientGlow(canvas *image.RGBA, seconds float64, accent color.RGBA) {
	width, height := canvas.Rect.Dx(), canvas.Rect.Dy()
	depth := max(36, min(64, height/7))
	pulse := .94 + .06*math.Sin(seconds*.85)
	ink := mix(accent, color.RGBA{255, 218, 180, 255}, .05)

	for y := 0; y < height; y++ {
		normalY := float64(y) / float64(max(1, height-1))
		for x := 0; x < width; x++ {
			normalX := float64(x) / float64(max(1, width-1))
			left, right := x, width-1-x
			top, bottom := y, height-1-y
			distance := min(min(left, right), min(top, bottom))
			if distance >= depth {
				continue
			}

			// Each side gets a broad baseline plus a few brighter pools. The
			// centers drift only a little, so the screen feels alive and not busy.
			hotspot := .36
			switch distance {
			case top:
				hotspot += .56 * conversationGaussian(normalX, .53+.025*math.Sin(seconds*.19), .19)
			case bottom:
				hotspot += .48*conversationGaussian(normalX, .31, .17) + .36*conversationGaussian(normalX, .78, .20)
			case left:
				hotspot += .48 * conversationGaussian(normalY, .72+.025*math.Sin(seconds*.16), .20)
			default:
				hotspot += .58 * conversationGaussian(normalY, .23+.02*math.Sin(seconds*.21), .20)
			}
			falloff := math.Pow(1-float64(distance)/float64(depth), 2.2)
			alpha := (14 + 70*hotspot) * falloff * pulse
			ink.A = uint8(math.Min(112, alpha))
			blendPixel(canvas, x, y, ink)
		}
	}
}

func (r *renderer) drawConversationRim(canvas *image.RGBA, accent color.RGBA, replying bool) {
	if !r.state.DisplayEdgeGlow {
		return
	}
	if replying && r.state.Phase == "speaking" {
		if r.spotScreen {
			r.drawSpotReplyRim(canvas, accent)
		} else {
			r.drawCheckersReplyRim(canvas, accent)
		}
		return
	}
	if r.spotScreen {
		r.drawSpotStateRim(canvas, accent)
	} else {
		r.drawCheckersStateRim(canvas, accent)
	}
}

func (r *renderer) drawSpotConversation(canvas *image.RGBA, seconds float64, accent color.RGBA, motion bool) {
	conversation := r.state.Conversation
	phase := conversationVisualPhase(conversation, r.state.Phase)
	accent = conversationAccent(phase, r.state.DisplayTheme)
	r.drawConversationAmbientGlow(canvas, seconds, accent)
	labelInk := mix(accent, color.RGBA{255, 255, 255, 255}, .22)
	r.centeredText(canvas, 240, 138, conversationStageLabel(phase), 16, true, labelInk)

	switch phase {
	case "replying":
		r.drawConversationFlowLines(canvas, firstNonEmpty(conversation.AssistantText, "Replying"), 240, 202, 338, 4, 30, conversationText, true)
	default:
		ink := conversationText
		if phase == "thinking" || phase == "working" {
			ink = mix(conversationDimText, accent, .18)
		}
		r.drawConversationUserText(canvas, 240, 202, 338, 4, 30, ink, motion)
	}
	if motion {
		r.drawSpotConversationMotion(canvas, seconds, accent)
	}
	maskOutsideSpotCircle(canvas, 236)
}

func (r *renderer) drawSpotConversationMotion(canvas *image.RGBA, seconds float64, accent color.RGBA) {
	phase := conversationVisualPhase(r.state.Conversation, r.state.Phase)
	accent = conversationAccent(phase, r.state.DisplayTheme)
	if phase == "thinking" || phase == "working" {
		r.drawConversationDots(canvas, 240, 354, seconds, accent, 9)
	}
	r.drawConversationRim(canvas, accent, phase == "replying")
	drawConversationFadeIn(canvas, seconds)
}

func (r *renderer) drawShowConversation(canvas *image.RGBA, seconds float64, accent color.RGBA, motion bool) {
	conversation := r.state.Conversation
	phase := conversationVisualPhase(conversation, r.state.Phase)
	accent = conversationAccent(phase, r.state.DisplayTheme)
	r.drawConversationAmbientGlow(canvas, seconds, accent)
	width := canvas.Rect.Dx()
	crown := r.isCrown()
	labelSize, labelBaseline := 15, 58
	maxWidth, maxLines := width*46/100, 4
	if crown {
		labelSize, labelBaseline = 20, 106
		maxWidth = width * 54 / 100
		maxLines = 5
	}
	labelInk := mix(accent, color.RGBA{255, 255, 255, 255}, .22)

	switch phase {
	case "replying":
		metaSize, metaTextSize, replySize := 11, 17, 32
		userLabelY, userTextY, assistantLabelY, replyFirstBaseline := 56, 82, 130, 184
		if crown {
			metaSize, metaTextSize, replySize = 15, 22, 42
			userLabelY, userTextY, assistantLabelY, replyFirstBaseline = 92, 127, 195, 270
		}
		r.centeredText(canvas, width/2, userLabelY, "YOU", metaSize, true, color.RGBA{171, 171, 176, 230})
		r.centeredText(canvas, width/2, userTextY, fitTextToWidth(conversation.UserText, r.faces.regular[metaTextSize], maxWidth), metaTextSize, false, color.RGBA{172, 172, 178, 220})
		r.centeredText(canvas, width/2, assistantLabelY, "TATER", metaSize, true, accent)
		r.drawConversationFlowLines(canvas, firstNonEmpty(conversation.AssistantText, "Replying"), width/2, replyFirstBaseline, maxWidth, maxLines, replySize, conversationText, true)
	default:
		r.centeredText(canvas, width/2, labelBaseline, conversationStageLabel(phase), labelSize, true, labelInk)
		bodySize, firstBaseline := 34, 194
		if crown {
			bodySize, firstBaseline = 48, 280
		}
		ink := conversationText
		if phase == "thinking" || phase == "working" {
			ink = mix(conversationDimText, accent, .18)
		}
		r.drawConversationUserText(canvas, width/2, firstBaseline, maxWidth, maxLines, bodySize, ink, motion)
	}
	if motion {
		r.drawShowConversationMotion(canvas, seconds, accent)
	}
}

func (r *renderer) drawShowConversationMotion(canvas *image.RGBA, seconds float64, accent color.RGBA) {
	width, height := canvas.Rect.Dx(), canvas.Rect.Dy()
	phase := conversationVisualPhase(r.state.Conversation, r.state.Phase)
	accent = conversationAccent(phase, r.state.DisplayTheme)
	if phase == "thinking" || phase == "working" {
		dotScale := 9
		if r.isCrown() {
			dotScale = 13
		}
		r.drawConversationDots(canvas, width/2, height*72/100, seconds, accent, dotScale)
	}
	r.drawConversationRim(canvas, accent, phase == "replying")
	drawConversationFadeIn(canvas, seconds)
}

func drawConversationFadeIn(canvas *image.RGBA, seconds float64) {
	duration := conversationFadeDuration.Seconds()
	if seconds >= duration {
		return
	}
	progress := math.Min(1, math.Max(0, seconds/duration))
	eased := progress * progress * (3 - 2*progress)
	alpha := uint8(math.Round(255 * (1 - eased)))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.NRGBA{A: alpha}), image.Point{}, draw.Over)
}

func conversationFadeNeedsFullFrame(seconds float64) bool {
	return seconds < conversationFadeDuration.Seconds()+1.0/30.0
}

func (r *renderer) showConversationMotionRegions(width, height int, seconds float64) []image.Rectangle {
	if conversationFadeNeedsFullFrame(seconds) {
		return []image.Rectangle{image.Rect(0, 0, width, height)}
	}
	if _, transitioning := r.conversationPlaceholderFade(time.Now()); transitioning {
		return []image.Rectangle{image.Rect(0, 0, width, height)}
	}
	regions := make([]image.Rectangle, 0, 5)
	phase := conversationVisualPhase(r.state.Conversation, r.state.Phase)
	if phase == "thinking" || phase == "working" {
		centerY := height * 72 / 100
		extentX, extentY := 64, 30
		if r.isCrown() {
			extentX, extentY = 88, 38
		}
		regions = append(regions, image.Rect(width/2-extentX, centerY-extentY, width/2+extentX, centerY+extentY))
	}
	if r.state.DisplayEdgeGlow {
		regions = append(regions,
			image.Rect(0, 0, width, 24), image.Rect(0, height-24, width, height),
			image.Rect(0, 24, 24, height-24), image.Rect(width-24, 24, width, height-24),
		)
	}
	return regions
}
