//go:build linux

package main

import (
	"fmt"
	"image"
	"image/draw"
	"math"
	"strings"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/gpucompositor"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

type gpuSceneState struct {
	layer        *image.RGBA
	key, surface string
	started      time.Time
	duration     time.Duration
	lastFrame    time.Time
	level        float64
	blend        float32
	active       bool
	baseKey      string
	words        conversationWordState
	wordsMoving  bool
	textKind     string
	overlay      gpuOverlayState
	slideBase    *image.RGBA
	sliding      bool
	muted        bool
	videoFrame   string
	videoImage   bool
	videoAtlas   *image.RGBA
	videoOverlay bool
	videoFadeAt  time.Time
}

type gpuSceneFrame struct {
	source  []byte
	effects gpucompositor.Effects
	present bool
}

func (r *renderer) resetGPUScene() {
	if r.gpuScene.active {
		r.invalidateFrameCanvases()
	}
	// Keep the allocation; a temporary volume overlay/video need not allocate
	// another full screen when it closes.
	layer := r.gpuScene.layer
	r.gpuScene = gpuSceneState{layer: layer}
}

func (r *renderer) gpuSurface(now time.Time) string {
	if !r.state.Connected || r.state.Phase == "setup" || r.nightClockVisible(now) ||
		now.Before(r.volumeOverlayUntil) {
		return ""
	}
	if r.displayVideoVisible() {
		return "display-video:" + r.state.Video.SessionID
	}
	if r.conversationActive() {
		return "conversation:" + conversationAnimationIdentity(r.state.Conversation)
	}
	if r.satChatVisible() {
		if r.state.SatChat != nil && r.state.SatChat.Call != nil {
			if r.satChatVideoActive() {
				return "sat-chat-video:" + r.state.SatChat.Call.ID
			}
			return ""
		}
		total := 0
		if r.state.SatChat != nil {
			total = len(r.state.SatChat.Participants)
		}
		width, height := r.showSize()
		return fmt.Sprintf("sat-chat-directory:%d", r.satChatPageFor(total, r.satChatPageSize(width, height)))
	}
	if r.notificationActive(now) {
		return "notification:" + notificationIdentity(r.state.Notification)
	}
	if r.state.Timer != nil && r.state.Timer.Active {
		return ""
	}
	if r.cameraPopupActive(now) {
		return "camera-video:" + r.state.Cameras.SelectedID
	}
	if r.settingsOpen {
		return fmt.Sprintf("settings:%s:%d", r.settingsPage, r.settingsIndex)
	}
	if r.climateOpen {
		return fmt.Sprintf("climate:%d:%t", r.climatePage, r.rookClimateRooms)
	}
	if r.cameraOpen {
		if r.state.Cameras != nil && r.state.Cameras.SelectedID != "" {
			return "camera-video:" + r.state.Cameras.SelectedID
		}
		return fmt.Sprintf("camera-directory:%d", r.cameraPage)
	}
	if r.menuOpen {
		return "menu"
	}
	if r.state.Media != nil && r.state.Media.Active {
		return "music:" + mediaIdentity(r.state.Media)
	}
	if r.taterTubeActive(now) {
		return "tube:" + firstNonEmpty(r.state.TaterTube.EventID, r.state.TaterTube.PlayerID) + "|" + r.state.TaterTube.Title + "|" + r.state.TaterTube.SeriesTitle
	}
	return "home"
}

// prepareGPUScene rasterizes text and controls only when their visible contents
// change. Subsequent frames send a 40-byte animation command instead of another
// 1.8 MB canvas. The helper snapshots the completed GPU image on each upload,
// allowing rapid STT revisions and taps to interrupt a fade without a jump.
func (r *renderer) prepareGPUScene(canvas *image.RGBA, now time.Time, enabled bool, animatedText ...bool) (gpuSceneFrame, bool) {
	r.updateDirectoryInteraction(now)
	var frame gpuSceneFrame
	surface := r.gpuSurface(now)
	dynamic := strings.HasPrefix(surface, "notification:") || strings.HasPrefix(surface, "music:") || strings.HasPrefix(surface, "tube:")
	canAtlas := len(animatedText) > 0 && animatedText[0]
	canVideo := len(animatedText) > 1 && animatedText[1]
	if dynamic && !canAtlas {
		r.resetGPUScene()
		return frame, false
	}
	geometry, qualified := gpucompositor.GeometryForTarget(r.target)
	if !enabled || !qualified || canvas.Rect != image.Rect(0, 0, geometry.SourceWidth, geometry.SourceHeight) || surface == "" {
		r.resetGPUScene()
		return frame, false
	}
	if strings.HasPrefix(surface, "sat-chat-video:") || strings.HasPrefix(surface, "camera-video:") || strings.HasPrefix(surface, "display-video:") {
		if !canAtlas {
			r.resetGPUScene()
			return frame, false
		}
		return r.prepareGPUVideoScene(canvas, now, surface, canVideo), true
	}
	scene := &r.gpuScene
	accent := accentForTheme(r.state.Phase, r.state.DisplayTheme)
	phase := conversationVisualPhase(r.state.Conversation, r.state.Phase)
	conversation := strings.HasPrefix(surface, "conversation:")
	wordAnimation := conversation && len(animatedText) > 0 && animatedText[0]
	textStyle := "smooth"
	if conversation {
		textStyle = show.NormalizeTextStyle(r.settingValue("screen_text_style"))
	}
	if wordAnimation && scene.textKind != "conversation" {
		scene.words = conversationWordState{}
		scene.textKind = "conversation"
	}
	if conversation {
		accent = r.conversationInk(phase)
	}
	key := ""
	baseKey := ""
	if conversation {
		// Revision/final flags do not change pixels; neither should restart a
		// fade. Keep the key tied to the text actually drawn on screen.
		key = fmt.Sprintf("%q|%q|%q|%q|%q|%t|%t|%t|%s", surface, phase,
			r.state.Conversation.UserText, r.state.Conversation.AssistantText,
			r.state.DisplayTheme, r.state.DisplayEdgeGlow, r.state.Muted, wordAnimation, textStyle)
		if wordAnimation {
			header := ""
			if phase == "replying" {
				header = r.state.Conversation.UserText
			}
			baseKey = fmt.Sprintf("%q|%q|%q|%q|%t|%t", surface, phase, header, r.state.DisplayTheme, r.state.DisplayEdgeGlow, r.state.Muted)
		}
	} else {
		// A surface change must also replace the retained GPU source, even
		// when the two views happen to share the same workspace state key.
		key = surface + "|" + r.gpuWorkspaceKey(now, surface)
	}
	if baseKey == "" {
		baseKey = key
	}
	changed := !scene.active || key != scene.key || surface != scene.surface
	if changed {
		previousSurface := scene.surface
		wasActive := scene.active
		mutedChanged := wasActive && scene.muted != r.state.Muted
		canSlide := wasActive && scene.textKind != "video" && !mutedChanged && scene.blend >= 1 && !scene.sliding && canAtlas && !conversation && !dynamic &&
			surface != previousSurface && show.NormalizePopupEffect(r.settingValue("screen_popup_effect")) == "slide"
		if scene.textKind == "video" {
			scene.textKind = ""
		}
		if canSlide {
			if scene.slideBase == nil {
				scene.slideBase = image.NewRGBA(canvas.Rect)
			}
			draw.Draw(scene.slideBase, canvas.Rect, scene.layer, image.Point{}, draw.Src)
		}
		scene.sliding = canSlide
		if scene.layer == nil || scene.layer.Rect != canvas.Rect {
			scene.layer = image.NewRGBA(canvas.Rect)
		}
		r.gpuSceneBuilding = true
		var words []conversationWord
		if wordAnimation {
			r.gpuTextCapture = &words
		}
		if r.spotScreen {
			r.renderSpotFrame(scene.layer, now, 0, accent, false)
		} else {
			r.renderCheckers(scene.layer, now, canvas.Rect.Dx(), canvas.Rect.Dy(), 0, accent, false)
		}
		r.gpuSceneBuilding = false
		r.gpuTextCapture = nil
		if wordAnimation {
			context := surface + ":user"
			if phase == "replying" {
				context = surface + ":reply"
			}
			context += ":" + textStyle
			r.updateConversationWords(words, context, now)
		}
		if !wasActive || baseKey != scene.baseKey {
			scene.duration = 0
			if wasActive && surface != previousSurface {
				scene.duration = 220 * time.Millisecond
				if dynamic {
					scene.duration = popupFadeDuration
				}
			} else if wasActive && conversation {
				scene.duration = 100 * time.Millisecond
			}
			if !conversation && show.NormalizePopupEffect(r.settingValue("screen_popup_effect")) == "disabled" {
				scene.duration = 0
			}
			if mutedChanged {
				scene.duration = 0
			}
			if r.state.Notification != nil && r.state.Notification.Priority == "critical" && r.notificationActive(now) {
				scene.duration = 0
			}
			scene.started = now
			frame.source = scene.layer.Pix
			if canSlide {
				frame.source = scene.slideBase.Pix
			}
		}
		scene.key, scene.baseKey, scene.surface, scene.active = key, baseKey, surface, true
		scene.muted = r.state.Muted
	}
	progress := 1.0
	if scene.duration > 0 {
		progress = math.Max(0, math.Min(1, float64(now.Sub(scene.started))/float64(scene.duration)))
		progress = progress * progress * (3 - 2*progress)
	}
	rim := r.state.DisplayEdgeGlow && voiceEdgeActive(r.state.Phase)
	if conversation {
		rim = r.state.DisplayEdgeGlow
	}
	level := .34
	if r.state.Phase == "speaking" {
		targetLevel := math.Pow(math.Min(1, math.Max(0, r.state.AudioLevel)/.22), .65)
		dt := 1.0 / 30
		if !scene.lastFrame.IsZero() {
			dt = math.Max(0, math.Min(.1, now.Sub(scene.lastFrame).Seconds()))
		}
		tau := .09
		if targetLevel > scene.level {
			tau = .036
		}
		scene.level += (targetLevel - scene.level) * (1 - math.Exp(-dt/tau))
		level = scene.level
	} else {
		scene.level = 0
	}
	scene.lastFrame = now
	frame.effects = gpucompositor.Effects{
		Blend: float32(progress), Seconds: float32(math.Mod(float64(now.UnixMilli())/1000, 600)),
		Level: float32(level), Red: float32(accent.R) / 255, Green: float32(accent.G) / 255, Blue: float32(accent.B) / 255,
		Rim: rim, Ambient: conversation && r.state.DisplayEdgeGlow,
		Dots: conversation && !wordAnimation && (phase == "thinking" || phase == "working"),
	}
	textMoving := false
	if wordAnimation {
		frame.effects.Text, textMoving = r.gpuConversationText(now, phase)
	}
	if scene.sliding {
		frame.effects.Blend = 1
		frame.effects.Rim, frame.effects.Ambient, frame.effects.Dots = false, false, false
		frame.effects.Text = &gpucompositor.TextFrame{Width: canvas.Rect.Dx(), Height: canvas.Rect.Dy(), Quads: []gpucompositor.TextQuad{{Atlas: canvas.Rect, Y: float32(28 * (1 - progress)), Opacity: float32(progress)}}}
		if changed {
			frame.effects.Text.Pixels = scene.layer.Pix
		}
		scene.textKind = "slide"
		if progress >= 1 {
			frame.source = scene.layer.Pix
			frame.effects.Text = nil
			scene.sliding = false
			scene.duration = 0
		}
		scene.blend = float32(progress)
		frame.present = true
		return frame, true
	}
	if dynamic {
		frame.effects.Text = r.gpuMediaOverlay(now, surface)
	}
	// Settled static screens make no GPU job or framebuffer write. Audio glow
	// and thinking indicators retain the display's animation cadence.
	if !changed && progress >= 1 && scene.blend >= 1 && !rim && !frame.effects.Dots && !textMoving && !scene.wordsMoving && !dynamic {
		r.presentRegions = nil
		return frame, true
	}
	scene.blend = frame.effects.Blend
	scene.wordsMoving = textMoving
	frame.present = true
	return frame, true
}
