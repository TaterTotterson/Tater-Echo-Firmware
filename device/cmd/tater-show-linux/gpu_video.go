//go:build linux

package main

import (
	"image"
	"image/draw"
	"math"
	"strings"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/gpucompositor"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

// The video source is uploaded only for a new decoded picture. Floating
// controls use the already-qualified v3 atlas/quads, retained across pictures;
// no full-frame CPU alpha pass or per-frame text rasterization is needed.
func (r *renderer) prepareGPUVideoScene(canvas *image.RGBA, now time.Time, surface string, directVideo bool) gpuSceneFrame {
	scene := &r.gpuScene
	kind, shown, source := "sat-chat", r.satChatVideoShown, r.satChatVideoImage
	if strings.HasPrefix(surface, "camera-video:") {
		kind, shown, source = "camera", r.cameraFrameShown, r.cameraFrameImage
	}
	if strings.HasPrefix(surface, "display-video:") {
		kind, shown, source = "playback", r.displayVideoFrame, nil
		if r.displayVideoImage != nil {
			source = r.displayVideoImage
		}
	}
	var controls *workspaceVideoControlCache
	if kind == "playback" {
		controls = r.popupVideoControls(canvas.Rect)
	} else {
		controls = r.workspaceVideoControls(canvas.Rect, kind)
	}
	entering := !scene.active || scene.surface != surface
	newPicture := entering || scene.videoFrame != shown || scene.videoImage != (source != nil)
	newControls := entering || scene.key != controls.key || scene.textKind != "video"
	overlay := kind == "playback" && r.popupVideoOverlayVisible()
	if kind == "playback" && ((entering || (!scene.videoOverlay && overlay)) && overlay) {
		scene.videoFadeAt = now
	}
	if !overlay || r.state.Muted || (r.state.Video != nil && r.state.Video.Error != "") || show.NormalizePopupEffect(r.settingValue("screen_popup_effect")) == "disabled" {
		scene.videoFadeAt = time.Time{}
	}
	opacity := 1.0
	if !scene.videoFadeAt.IsZero() {
		opacity = math.Max(0, math.Min(1, float64(now.Sub(scene.videoFadeAt))/float64(popupFadeDuration)))
		opacity = opacity * opacity * (3 - 2*opacity)
	}
	// Present one final opaque frame, then stop jobs while paused and settled.
	animating := overlay && !scene.videoFadeAt.IsZero() && now.Before(scene.videoFadeAt.Add(popupFadeDuration+time.Second/30))
	if !newPicture && !newControls && !animating {
		return gpuSceneFrame{}
	}
	frame := gpuSceneFrame{present: true, effects: gpucompositor.Effects{Blend: 1}}
	if newPicture && (!directVideo || source == nil) {
		if scene.layer == nil || scene.layer.Rect != canvas.Rect {
			scene.layer = image.NewRGBA(canvas.Rect)
		}
		drawWorkspaceVideoPicture(scene.layer, canvas.Rect, source)
		frame.source = scene.layer.Pix
	}
	// Power-of-two atlas coordinates remain exact in the existing helper's
	// mediump shader. A 960x480 atlas subtly filtered stationary text/chip edges.
	atlasWidth, atlasHeight := gpuAtlasDimension(canvas.Rect.Dx()), gpuAtlasDimension(canvas.Rect.Dy())
	text := &gpucompositor.TextFrame{Width: atlasWidth, Height: atlasHeight}
	if newControls {
		if scene.videoAtlas == nil {
			scene.videoAtlas = image.NewRGBA(image.Rect(0, 0, atlasWidth, atlasHeight))
		}
		draw.Draw(scene.videoAtlas, controls.layer.Rect, controls.layer, controls.layer.Rect.Min, draw.Src)
		text.Pixels = scene.videoAtlas.Pix
	}
	for _, region := range controls.regions {
		text.Quads = append(text.Quads, gpucompositor.TextQuad{Atlas: region,
			X: float32(region.Min.X), Y: float32(region.Min.Y), Opacity: float32(opacity)})
	}
	frame.effects.Text = text
	rgba, rgbaOK := source.(*image.RGBA)
	if directVideo && rgbaOK && rgba.Rect.Min == (image.Point{}) && rgba.Stride == rgba.Rect.Dx()*4 {
		content := cameraImageContentBounds(rgba)
		destination := containBounds(canvas.Rect, content.Size())
		if r.spotScreen {
			destination = rookContainedArtwork(content.Size(), 228)
		}
		video := &gpucompositor.VideoFrame{
			Width: rgba.Rect.Dx(), Height: rgba.Rect.Dy(), Source: content,
			Destination: destination, CornerRadius: max(12, min(22, destination.Dy()/12)),
		}
		if newPicture {
			video.Pixels = rgba.Pix
		}
		frame.source = nil
		frame.effects.Video = video
	}
	scene.active, scene.surface, scene.key, scene.textKind = true, surface, controls.key, "video"
	scene.videoFrame, scene.blend, scene.duration, scene.sliding = shown, 1, 0, false
	scene.videoImage = source != nil
	scene.videoOverlay = overlay
	scene.muted = r.state.Muted
	scene.baseKey = surface + "|" + controls.key
	if kind == "playback" {
		r.displayVideoDrawn = shown
	} else if kind == "camera" {
		r.cameraFrameDrawn = shown
	} else {
		r.satChatVideoDrawn = shown
	}
	return frame
}

func gpuAtlasDimension(size int) int {
	dimension := 1
	for dimension < size {
		dimension *= 2
	}
	return dimension
}
