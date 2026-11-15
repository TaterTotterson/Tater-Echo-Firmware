//go:build linux

package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
)

// Live views use a stable decoder canvas underneath retained controls.
// Source aspect, not this rectangle, determines the actual picture size.
func workspaceVideoViewport(width, height int, spot, controls bool) image.Rectangle {
	return image.Rect(0, 0, width&^1, height&^1)
}

// Frames are scaled once in the decoder/JPEG worker, then copied here. Only
// the small rounded corners are blended; no full-frame alpha mask or UI
// rasterization is needed during playback. Alpha denotes generated padding,
// never inferred black bars in a dark scene.
func drawWorkspaceVideoPicture(canvas *image.RGBA, viewport image.Rectangle, source image.Image) image.Rectangle {
	viewport = viewport.Intersect(canvas.Rect)
	draw.Draw(canvas, viewport, image.NewUniform(workspaceBackground), image.Point{}, draw.Src)
	bounds := viewport
	round := canvas.Rect.Size() == image.Pt(480, 480)
	if source != nil {
		content := cameraImageContentBounds(source)
		bounds = containBounds(viewport, content.Size())
		if round {
			bounds = rookContainedArtwork(content.Size(), 228)
		} else if source.Bounds().Size() == viewport.Size() {
			bounds = content.Sub(source.Bounds().Min).Add(viewport.Min)
		}
		if rgba, ok := source.(*image.RGBA); ok {
			source = rgba.SubImage(content)
		}
		if source.Bounds().Size() != bounds.Size() {
			source = prepareCoverImage(source, bounds.Size())
		}
	}
	radius := max(12, min(22, bounds.Dy()/12))
	if source == nil {
		drawMenuRoundedRect(canvas, bounds, radius, workspaceControl)
	} else {
		drawMenuRoundedImage(canvas, bounds, source, radius, workspaceBackground)
	}
	drawMenuRoundedOutline(canvas, bounds, radius, 1, workspaceBorder)
	if round {
		maskOutsideSpotCircle(canvas, rookPanelRadius)
	}
	return bounds
}

func (r *renderer) drawWorkspaceSatChatVideo(canvas *image.RGBA, width, height int) image.Rectangle {
	viewport := r.satChatVideoContentBounds(width, height)
	r.drawSatChatVideoFrame(canvas, width, height)
	return viewport
}

func (r *renderer) drawWorkspaceSatChatControls(canvas *image.RGBA, width, height int) {
	viewport := r.satChatVideoContentBounds(width, height)
	chat := r.state.SatChat
	if chat == nil || chat.Call == nil {
		return
	}
	call := chat.Call
	name := firstNonEmpty(call.TargetName, call.CallerName, "Satellite")
	if call.Target == chat.Self {
		name = firstNonEmpty(call.CallerName, "Satellite")
	}
	if r.spotScreen {
		r.drawRookHeader(canvas, name, "Video + audio", false)
		if r.satChatVideoImage == nil {
			r.centeredText(canvas, 240, 246, "Waiting for camera", 20, false, workspaceMuted)
		}
		r.drawRookEndCall(canvas)
		return
	}
	label := "Sat Chat · " + name
	size, baseline := 24, 52
	if height >= 600 {
		size, baseline = 34, 76
	}
	if r.spotScreen {
		size, baseline = 18, 101
	}
	label = fitTextToWidth(label, r.faces.regular[size], viewport.Dx()-40)
	if !r.spotScreen {
		r.drawWorkspaceVideoLabelBackground(canvas, label, size, baseline)
	}
	r.centeredText(canvas, width/2, baseline, label, size, false, workspaceText)
	if r.satChatVideoImage == nil {
		size := 20
		if r.spotScreen {
			size = 14
		}
		r.centeredText(canvas, width/2, viewport.Min.Y+viewport.Dy()/2, "Waiting for camera", size, false, workspaceMuted)
	}
	end := r.satChatEndBounds(width, height)
	drawMenuRoundedRect(canvas, end, 12, color.RGBA{120, 38, 40, 255})
	size = 18
	if r.spotScreen {
		size = 16
	}
	r.centeredText(canvas, width/2, end.Min.Y+end.Dy()/2+size/3, "End Sat Chat", size, false, workspaceText)
}

// One bounded transparent control image is shared by camera/call views. It is
// rebuilt only when visible controls change, never for a new decoded picture.
// GPU v3 retains this as its atlas; CPU fallback blends only the small regions.
type workspaceVideoControlCache struct {
	key     string
	layer   *image.RGBA
	regions []image.Rectangle
}

func (r *renderer) workspaceVideoControls(bounds image.Rectangle, kind string) *workspaceVideoControlCache {
	key := fmt.Sprintf("%s|%v|%s|%t", kind, bounds, r.state.DisplayTheme, r.state.Muted)
	waiting, message := false, ""
	if kind == "camera" {
		key += "|" + r.cameraVideoRenderKey(r.menuPalette().primary)
		waiting = r.cameraFrameImage == nil
		if r.state.Cameras != nil {
			message = r.state.Cameras.Error
		}
	} else {
		if r.state.SatChat != nil {
			key += "|" + r.state.SatChat.Self + "|" + stableCacheValue(r.state.SatChat.Call)
		}
		waiting = r.satChatVideoImage == nil
	}
	key += fmt.Sprintf("|%t", waiting)
	cache := &r.videoControls
	if cache.layer != nil && cache.key == key {
		return cache
	}
	if cache.layer == nil || cache.layer.Rect != bounds {
		cache.layer = image.NewRGBA(bounds)
	} else {
		clear(cache.layer.Pix)
	}
	width, height := bounds.Dx(), bounds.Dy()
	headerBottom := 96
	if height >= 600 {
		headerBottom = 120
	}
	cache.regions = []image.Rectangle{image.Rect(12, 12, width-12, headerBottom)}
	if r.spotScreen {
		cache.regions = []image.Rectangle{image.Rect(60, 66, 420, 132)}
	}
	if kind == "camera" {
		r.drawWorkspaceCameraControls(cache.layer, r.spotScreen)
		if r.spotScreen {
			cache.regions = append(cache.regions, r.cameraCloseBounds(width, height, true, true))
		}
		if r.state.Cameras != nil && r.state.Cameras.SelectedTalkback {
			cache.regions = append(cache.regions, cameraTalkBounds(width, height, r.spotScreen))
		}
	} else {
		r.drawWorkspaceSatChatControls(cache.layer, width, height)
		cache.regions = append(cache.regions, r.satChatEndBounds(width, height))
	}
	if waiting {
		cache.regions = append(cache.regions, image.Rect(12, height/2-30, width-12, height/2+30))
	}
	if message != "" && !r.spotScreen {
		box := image.Rect(28, headerBottom+8, width-28, headerBottom+48)
		drawMenuRoundedRect(cache.layer, box, 8, workspacePanel)
		r.centeredText(cache.layer, width/2, box.Min.Y+26, fitTextToWidth(message, r.faces.regular[14], box.Dx()-24), 14, false, workspaceText)
		cache.regions = append(cache.regions, box)
	}
	if r.state.Muted {
		box := image.Rect(16, height-60, 164, height-24)
		if r.spotScreen {
			box = image.Rect(166, 140, 314, 176)
		}
		drawMenuRoundedRect(cache.layer, box, 12, color.RGBA{101, 27, 38, 235})
		r.text(cache.layer, box.Min.X+19, box.Min.Y+25, "MIC MUTED", 16, true, color.RGBA{255, 191, 197, 255})
		cache.regions = append(cache.regions, box)
	}
	cache.key = key
	return cache
}

func (r *renderer) drawWorkspaceVideoControls(canvas *image.RGBA, kind string) {
	cache := r.workspaceVideoControls(canvas.Rect, kind)
	for _, region := range cache.regions {
		draw.Draw(canvas, region, cache.layer, region.Min, draw.Over)
	}
}

func (r *renderer) drawWorkspaceVideoLabelBackground(canvas *image.RGBA, label string, size, baseline int) {
	width := textWidth(r.faces.regular[size], label) + 32
	x := (canvas.Rect.Dx() - width) / 2
	drawMenuRoundedRect(canvas, image.Rect(x, baseline-size-9, x+width, baseline+13), 12, color.RGBA{17, 18, 19, 210})
}
