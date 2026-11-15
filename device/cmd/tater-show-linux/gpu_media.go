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
)

// The reviewed v3 glyph-atlas protocol also supports solid strips and labels.
// Keep one bounded atlas, update timestamps once a second, and send only small
// quad rectangles for progress/spectrum on intervening frames. No helper or
// proprietary-library changes are required.
type gpuOverlayState struct {
	key                      string
	atlas                    *image.RGBA
	elapsedWidth, totalWidth int
}

func (r *renderer) gpuMediaOverlay(now time.Time, surface string) *gpucompositor.TextFrame {
	state := &r.gpuScene.overlay
	ink := r.menuPalette().primary
	width, height := r.showSize()
	left, right, y, thickness, baseline := 24, width-24, height-10, 4, 0
	var progress, duration int64
	var bars image.Rectangle
	timeSize := 17
	spectrum := false
	switch {
	case strings.HasPrefix(surface, "notification:"):
		if r.state.Notification.Priority == "critical" {
			ink.R, ink.G, ink.B = 255, 92, 92
		}
		if r.state.Notification.ExpiresAtUnixMS <= 0 {
			return nil
		}
		progress = int64(notificationProgress(r.state.Notification, r.currentTaterUnixMS(now)) * 100000)
		duration = 100000
		left, right, y, thickness = popupNotificationProgressGeometry(width, height)
	case strings.HasPrefix(surface, "music:"):
		_, content, levels := r.fullscreenMusicLayout(width, height)
		bars = levels
		left, right, y, thickness, baseline = content.Min.X, content.Max.X, levels.Min.Y-34, 6, levels.Min.Y-9
		progress, duration = mediaProgress(r.state.Media, now)
		spectrum = true
	case strings.HasPrefix(surface, "tube:"):
		_, content := r.taterTubeLayout(width, height)
		left, right = content.Min.X, content.Max.X
		y, thickness, timeSize, baseline = r.taterTubeProgressGeometry(content)
		progress, duration = taterTubeProgress(r.state.TaterTube, now)
	default:
		return nil
	}
	if r.spotScreen {
		spectrum = false
		if strings.HasPrefix(surface, "notification:") {
			left, right, y, thickness = 150, 330, 419, 5
		} else {
			left, right, y, thickness, baseline, timeSize = 110, 370, 381, 5, 406, 16
		}
	}
	elapsed, total := "", ""
	if duration > 0 && baseline > 0 {
		elapsed, total = formatMediaTime(progress), formatMediaTime(duration)
	}
	key := fmt.Sprintf("%s|%s|%s|%v|%d", surface, elapsed, total, ink, timeSize)
	changed := state.atlas == nil || state.key != key || r.gpuScene.textKind != "overlay"
	if changed {
		if state.atlas == nil {
			state.atlas = image.NewRGBA(image.Rect(0, 0, gpuAtlasDimension(width), 128))
		}
		draw.Draw(state.atlas, state.atlas.Bounds(), image.Transparent, image.Point{}, draw.Src)
		draw.Draw(state.atlas, image.Rect(0, 0, width, 12), image.NewUniform(ink), image.Point{}, draw.Src)
		draw.Draw(state.atlas, image.Rect(0, 80, 20, 128), image.NewUniform(ink), image.Point{}, draw.Src)
		r.text(state.atlas, 0, 50, elapsed, timeSize, false, workspaceText)
		r.text(state.atlas, 256, 50, total, timeSize, false, workspaceMuted)
		state.elapsedWidth = textWidth(r.faces.regular[timeSize], elapsed) + 2
		state.totalWidth = textWidth(r.faces.regular[timeSize], total) + 2
		state.key = key
	}
	frame := &gpucompositor.TextFrame{Width: state.atlas.Rect.Dx(), Height: 128}
	if changed {
		frame.Pixels = state.atlas.Pix
	}
	r.gpuScene.textKind = "overlay"
	if duration > 0 {
		filled := int(math.Round(float64(right-left) * math.Max(0, math.Min(1, float64(progress)/float64(duration)))))
		if filled > 0 {
			frame.Quads = append(frame.Quads, gpucompositor.TextQuad{Atlas: image.Rect(0, 0, filled, thickness), X: float32(left), Y: float32(y - thickness/2), Opacity: 1})
		}
		if baseline > 0 {
			frame.Quads = append(frame.Quads,
				gpucompositor.TextQuad{Atlas: image.Rect(0, 20, state.elapsedWidth, 58), X: float32(left), Y: float32(baseline - 30), Opacity: 1},
				gpucompositor.TextQuad{Atlas: image.Rect(256, 20, 256+state.totalWidth, 58), X: float32(right - state.totalWidth), Y: float32(baseline - 30), Opacity: 1})
		}
	}
	if spectrum {
		r.updateMusicLevels(now, now.Sub(r.animationStart).Seconds())
		const count = 24
		gap := 6
		width := (bars.Dx() - (count-1)*gap) / count
		for index := 0; index < count; index++ {
			level := r.workspaceSpectrumLevel(index, now.Sub(r.animationStart).Seconds())
			if strings.EqualFold(r.state.Media.PlaybackState, "paused") {
				level = .04
			}
			height := max(2, min(48, int(level*48)))
			frame.Quads = append(frame.Quads, gpucompositor.TextQuad{Atlas: image.Rect(0, 80, min(20, width), 80+height), X: float32(bars.Min.X + index*(width+gap)), Y: float32(bars.Max.Y - height), Opacity: 1})
		}
	}
	return frame
}

func (r *renderer) gpuWorkspaceKey(now time.Time, surface string) string {
	theme := fmt.Sprintf("%s|%t|%t", r.state.DisplayTheme, r.state.DisplayEdgeGlow, r.state.Muted)
	switch {
	case strings.HasPrefix(surface, "sat-chat-directory:"):
		// The ordinary home cache deliberately omits Sat Chat. Its retained
		// GPU directory must track both local paging and live participant data.
		return theme + "|" + r.satChatRenderKey(now, accentForTheme(r.state.Phase, r.state.DisplayTheme))
	case strings.HasPrefix(surface, "notification:"):
		n := r.state.Notification
		return fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%t|%t|%s", theme, n.Kind, n.Label, n.Title, n.CameraName, n.Description, n.Source, r.notificationImage != nil, n.Priority == "critical", r.workspaceNotificationStamp(now))
	case strings.HasPrefix(surface, "music:"):
		return theme + "|" + mediaCacheIdentity(r.state.Media) + fmt.Sprint(r.mediaImage != nil)
	case strings.HasPrefix(surface, "tube:"):
		return theme + "|" + taterTubeIdentity(r.state.TaterTube) + fmt.Sprint(r.taterTubeImage != nil)
	default:
		if r.spotScreen {
			return r.spotFrameCacheKey(now, accentForTheme(r.state.Phase, r.state.DisplayTheme))
		}
		return r.checkersCacheKey(now, accentForTheme(r.state.Phase, r.state.DisplayTheme))
	}
}
