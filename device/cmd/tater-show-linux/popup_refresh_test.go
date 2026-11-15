//go:build linux

package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/gpucompositor"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

func popupFixture(t *testing.T, target string) (*renderer, *image.RGBA, time.Time) {
	t.Helper()
	r, canvas, now := gpuTestRenderer(t)
	if target == "crown" || target == "athena" {
		canvas = image.NewRGBA(image.Rect(0, 0, 1280, 800))
	}
	r.target, r.screenWidth, r.screenHeight = target, canvas.Rect.Dx(), canvas.Rect.Dy()
	r.animationStart = now
	r.state.DisplayTheme, r.state.Settings = "tater", show.ScreenSettingDefaults()
	return r, canvas, now
}

func TestPopupArtworkUsesEntireScreenWithoutCroppingOrTransparencyHoles(t *testing.T) {
	for _, target := range []string{"checkers", "cronos", "crown", "athena"} {
		t.Run(target, func(t *testing.T) {
			_, canvas, _ := popupFixture(t, target)
			for _, size := range []image.Point{image.Pt(160, 90), image.Pt(90, 160), image.Pt(100, 100)} {
				source := cameraFixture(size)
				drawPopupArtwork(canvas, source)
				picture := containBounds(canvas.Rect, size)
				for _, edge := range []struct {
					point image.Point
					ink   color.RGBA
				}{
					{image.Pt(picture.Min.X+3, picture.Min.Y+picture.Dy()/2), color.RGBA{200, 20, 20, 255}},
					{image.Pt(picture.Max.X-4, picture.Min.Y+picture.Dy()/2), color.RGBA{20, 200, 20, 255}},
					{image.Pt(picture.Min.X+picture.Dx()/2, picture.Min.Y+3), color.RGBA{20, 20, 200, 255}},
					{image.Pt(picture.Min.X+picture.Dx()/2, picture.Max.Y-4), color.RGBA{200, 200, 20, 255}},
				} {
					if got := canvas.RGBAAt(edge.point.X, edge.point.Y); got != edge.ink {
						t.Fatalf("full-screen %v edge %v was cropped: %v", size, edge.point, got)
					}
				}
			}
			drawPopupArtwork(canvas, taterPrimaryLogo)
			for index := 3; index < len(canvas.Pix); index += 4 {
				if canvas.Pix[index] != 255 {
					t.Fatal("default transparent Tater art left a nonopaque screen")
				}
			}
		})
	}
}

func TestPopupNotificationRetainsArtworkAndUsesMatchingProgressGeometry(t *testing.T) {
	for _, target := range []string{"checkers", "cronos", "crown", "athena"} {
		t.Run(target, func(t *testing.T) {
			r, canvas, now := popupFixture(t, target)
			r.prepareGPUScene(canvas, now, true, true)
			r.state.Notification = &show.Notification{ID: "note", Source: "Awareness Core", Title: "Garage door is open", CreatedAtUnixMS: now.UnixMilli(), ExpiresAtUnixMS: now.Add(time.Minute).UnixMilli(), TTLMS: 60000}
			first, ok := r.prepareGPUScene(canvas, now.Add(time.Second), true, true)
			if !ok || first.effects.Blend != 0 || len(first.source) == 0 || first.effects.Text == nil {
				t.Fatal("notification did not enter as a retained GPU popup")
			}
			base := bytes.Clone(r.gpuScene.layer.Pix)
			next, _ := r.prepareGPUScene(canvas, now.Add(1100*time.Millisecond), true, true)
			if len(next.source) != 0 || len(next.effects.Text.Pixels) != 0 || !bytes.Equal(base, r.gpuScene.layer.Pix) {
				t.Fatal("notification animation repainted its full-screen image")
			}
			left, _, y, thickness := popupNotificationProgressGeometry(canvas.Rect.Dx(), canvas.Rect.Dy())
			quad := next.effects.Text.Quads[0]
			if int(quad.X) != left || int(quad.Y) != y-thickness/2 || !popupNotificationProgressBounds(canvas.Rect.Dx(), canvas.Rect.Dy()).In(canvas.Rect) {
				t.Fatal("GPU progress and CPU dirty rectangle disagree")
			}
			r.state.Notification.Priority = "critical"
			critical, _ := r.prepareGPUScene(canvas, now.Add(1200*time.Millisecond), true, true)
			if critical.effects.Blend != 1 {
				t.Fatal("critical notification waited for a decorative animation")
			}
		})
	}
}

func TestPopupThinkingDotsKeepTextAndAtlasRetainedOnEveryShow(t *testing.T) {
	for _, target := range []string{"checkers", "cronos", "crown", "athena"} {
		for _, style := range []string{"smooth", "matrix"} {
			t.Run(target+"/"+style, func(t *testing.T) {
				r, canvas, now := popupFixture(t, target)
				r.state.Settings["screen_text_style"] = style
				r.state.Phase = "thinking"
				r.state.Conversation = &show.Conversation{TurnID: "dots", Phase: "thinking", UserText: "What’s the weather like tomorrow?"}
				first, ok := r.prepareGPUScene(canvas, now, true, true)
				if !ok || first.effects.Dots || first.effects.Text == nil || len(first.effects.Text.Pixels) == 0 {
					t.Fatal("modern dots did not use the shared retained GPU atlas")
				}
				atlas := r.gpuScene.words.atlas
				for _, offset := range []time.Duration{time.Second, 1300 * time.Millisecond, 20 * time.Second} {
					next, _ := r.prepareGPUScene(canvas, now.Add(offset), true, true)
					text := next.effects.Text
					if !next.present || len(next.source) != 0 || text == nil || len(text.Pixels) != 0 || r.gpuScene.words.atlas != atlas || len(text.Quads) > gpucompositor.MaxTextQuads || text.Height > 1024 {
						t.Fatal("a dot tick repainted text, uploaded pixels, or exceeded its bounds")
					}
					for _, quad := range text.Quads {
						if !quad.Atlas.In(atlas.Rect) {
							t.Fatal("dot or word rectangle escaped the shared atlas")
						}
					}
				}
				a, _ := r.gpuConversationText(now.Add(time.Second), "thinking")
				b, _ := r.gpuConversationText(now.Add(1300*time.Millisecond), "thinking")
				if a.Quads[len(a.Quads)-1] == b.Quads[len(b.Quads)-1] {
					t.Fatal("dots stopped breathing")
				}
				r.state.Conversation.Phase, r.state.Phase = "replying", "speaking"
				r.state.Conversation.AssistantText = "Clear skies tomorrow."
				r.prepareGPUScene(canvas, now.Add(21*time.Second), true, true)
				reply, _ := r.gpuConversationText(now.Add(22*time.Second), "replying")
				for _, quad := range reply.Quads {
					if quad.Atlas.Min.Y >= r.gpuScene.words.textBounds.Max.Y {
						t.Fatal("thinking sprites leaked into reply text")
					}
				}
			})
		}
	}
}

func TestPopupVideoRetainsControlsAndPictureIndependently(t *testing.T) {
	for _, target := range []string{"checkers", "cronos", "crown", "athena"} {
		t.Run(target, func(t *testing.T) {
			r, canvas, now := popupFixture(t, target)
			r.displayVideoImage = cameraFixture(image.Pt(640, 360))
			r.displayVideoFrame = "one"
			r.state.Video = &show.Video{Active: true, SessionID: "movie", State: "paused", Title: "Alpine at dusk", Subtitle: "Quiet moments in the mountains", DurationMS: 1_720_000, PositionMS: 728_000}
			first, ok := r.prepareGPUScene(canvas, now, true, true, true)
			if !ok || first.effects.Video == nil || len(first.effects.Video.Pixels) == 0 || len(first.effects.Text.Pixels) == 0 || len(first.effects.Text.Quads) != 1 || first.effects.Text.Quads[0].Opacity != 0 {
				t.Fatal("video did not upload separate picture and fading control textures")
			}
			middle, _ := r.prepareGPUScene(canvas, now.Add(popupFadeDuration/2), true, true, true)
			if len(middle.effects.Video.Pixels) != 0 || len(middle.effects.Text.Pixels) != 0 || middle.effects.Text.Quads[0].Opacity < .49 || middle.effects.Text.Quads[0].Opacity > .51 {
				t.Fatal("paused control fade retransmitted pixels or missed its midpoint")
			}
			r.prepareGPUScene(canvas, now.Add(400*time.Millisecond), true, true, true)
			settled, _ := r.prepareGPUScene(canvas, now.Add(time.Second), true, true, true)
			if settled.present {
				t.Fatal("settled pause screen continued making GPU jobs")
			}
			r.state.Video.Subtitle = "Updated subtitle"
			metadata, _ := r.prepareGPUScene(canvas, now.Add(1100*time.Millisecond), true, true, true)
			if len(metadata.effects.Video.Pixels) != 0 || len(metadata.effects.Text.Pixels) == 0 || metadata.effects.Text.Quads[0].Opacity != 1 {
				t.Fatal("metadata failed to update independently of the retained picture")
			}
			r.state.Video.State = "playing"
			playing, _ := r.prepareGPUScene(canvas, now.Add(1200*time.Millisecond), true, true, true)
			if len(playing.effects.Text.Quads) != 0 || len(playing.effects.Video.Pixels) != 0 {
				t.Fatal("resume retained permanent controls or reuploaded the picture")
			}
			atlas := r.playbackControls.layer
			r.displayVideoFrame = "two"
			r.displayVideoPositionMS += 33
			next, _ := r.prepareGPUScene(canvas, now.Add(1250*time.Millisecond), true, true, true)
			if len(next.effects.Video.Pixels) == 0 || len(next.effects.Text.Pixels) != 0 || r.playbackControls.layer != atlas {
				t.Fatal("new video frame rebuilt the invisible control atlas")
			}
			r.state.Muted = true
			muted, _ := r.prepareGPUScene(canvas, now.Add(1300*time.Millisecond), true, true, true)
			if len(muted.effects.Video.Pixels) != 0 || len(muted.effects.Text.Pixels) == 0 || muted.effects.Text.Quads[0].Opacity != 1 {
				t.Fatal("mute indicator was delayed or repainted the picture")
			}
			if _, ok := r.prepareGPUScene(canvas, now, false, true, true); ok {
				t.Fatal("disabled GPU did not use software fallback")
			}
			r.render(canvas, now)
			if len(r.presentRegions) == 0 {
				t.Fatal("software fallback left a stale GPU image")
			}
		})
	}
}

func TestPopupVideoLoadingErrorAndUnsupportedHelpersRemainSafe(t *testing.T) {
	r, canvas, now := popupFixture(t, "athena")
	r.state.Video = &show.Video{Active: true, SessionID: "load", State: "loading", Title: strings.Repeat("Long title ", 20)}
	if _, ok := r.prepareGPUScene(canvas, now, true, false, false); ok {
		t.Fatal("helper without atlas support entered the video overlay path")
	}
	first, ok := r.prepareGPUScene(canvas, now, true, true, true)
	if !ok || first.effects.Video != nil || len(first.source) != len(canvas.Pix) || first.effects.Text == nil {
		t.Fatal("loading without a picture did not use a safe retained placeholder")
	}
	r.state.Video.Error = "This stream is unavailable."
	errFrame, _ := r.prepareGPUScene(canvas, now.Add(10*time.Millisecond), true, true, true)
	if errFrame.effects.Text.Quads[0].Opacity != 1 {
		t.Fatal("error controls waited behind an entrance animation")
	}
	for _, dimensions := range []image.Point{image.Pt(960, 480), image.Pt(1280, 800)} {
		play, stop := popupVideoButtonBounds(dimensions.X, dimensions.Y)
		if !play.In(image.Rectangle{Max: dimensions}) || !stop.In(image.Rectangle{Max: dimensions}) || !play.Intersect(stop).Empty() || play.Dy() < 44 {
			t.Fatal("video buttons overlap or escape the native screen")
		}
	}
}

func TestPopupCompanionKeepsEpisodeCasingAndDeduplicatesMetadata(t *testing.T) {
	media := &show.TaterTubeMedia{SeriesTitle: "The Quiet Wild", Title: "Alpine at dusk", Subtitle: "S2 E4 · Alpine at dusk · The Quiet Wild", Season: 2, Episode: 4}
	if got := strings.Join(taterTubeDetailParts(media), " · "); got != "S2 E4 · Alpine at dusk" {
		t.Fatalf("companion metadata = %q", got)
	}
}

func TestPopupThinkingThemesReuseSpritesAndKeepLongTextAboveDots(t *testing.T) {
	for _, target := range []string{"checkers", "cronos", "crown", "athena"} {
		for _, theme := range []string{"tater", "ocean", "violet", "forest", "sunset"} {
			r, canvas, now := popupFixture(t, target)
			r.state.DisplayTheme = theme
			r.state.Conversation = &show.Conversation{TurnID: "long", Phase: "thinking", UserText: strings.Repeat("Long transcript text with more words. ", 20)}
			r.state.Phase = "thinking"
			r.prepareGPUScene(canvas, now, true, true)
			for _, word := range r.gpuScene.words.current {
				if word.word.y+word.bounds.Max.Y >= canvas.Rect.Dy()*72/100-thinkingDotTile/2 {
					t.Fatalf("%s/%s long transcript overlaps the thinking dots", target, theme)
				}
			}
			width, _ := r.showSize()
			ink := r.menuPalette().primary
			before, after := thinkingDotSprites(width, ink), thinkingDotSprites(width, ink)
			if before[0] != after[0] || before[thinkingDotSteps-1].RGBAAt(24, 24) != ink || r.conversationInk("thinking") != ink {
				t.Fatal("theme did not preserve the cached sprite artwork and primary accent")
			}
			if r.conversationInk("listening") != conversationAccent("listening", theme) {
				t.Fatal("popup refresh changed listening-state colors")
			}
		}
	}
}

// Local synthetic previews only: no live pictures, device, or network access.
func TestPopupRefreshOfflinePreviews(t *testing.T) {
	directory := os.Getenv("TATER_POPUP_PREVIEW_DIR")
	if directory == "" {
		t.Skip("set TATER_POPUP_PREVIEW_DIR for native popup previews")
	}
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"checkers", "cronos", "crown", "athena"} {
		for _, view := range []string{"notification", "notification-default", "video-paused", "video-loading", "now-playing", "thinking", "thinking-matrix"} {
			r, canvas, now := popupFixture(t, target)
			art, _, err := readDefaultWallpaper(target, 6) // alpine-sunset
			if err != nil {
				t.Fatal(err)
			}
			switch view {
			case "notification", "notification-default":
				r.state.Notification = &show.Notification{ID: "preview", Source: "Awareness Core", Label: "Home notification", Title: "The garage door is still open.", Description: "It has been open for 10 minutes.", CreatedAtUnixMS: now.UnixMilli(), ExpiresAtUnixMS: now.Add(time.Minute).UnixMilli(), TTLMS: 60000}
				if view == "notification" {
					r.notificationImage = art
				}
				r.drawWorkspaceNotification(canvas, now, true)
			case "video-paused", "video-loading":
				r.state.Video = &show.Video{Active: true, SessionID: "preview", State: "paused", Title: "Alpine at dusk", Subtitle: "Quiet moments in the mountains", PositionMS: 728000, DurationMS: 1720000}
				if view == "video-paused" {
					r.displayVideoImage = prepareContainImage(art, canvas.Rect.Size()).(*image.RGBA)
				} else {
					r.state.Video.State = "loading"
				}
				r.renderDisplayVideo(canvas)
			case "now-playing":
				r.taterTubeImage = art
				r.state.TaterTube = &show.TaterTubeMedia{Active: true, EventID: "preview", SeriesTitle: "The Quiet Wild", Title: "Alpine at dusk", Season: 2, Episode: 4, PlayerName: "Family Room TV", ProgressMS: 1397000, DurationMS: 3242000}
				r.drawWorkspaceTaterTube(canvas, now, true)
			case "thinking", "thinking-matrix":
				r.state.Phase = "thinking"
				r.state.Conversation = &show.Conversation{TurnID: "preview", Phase: "thinking", UserText: "What’s the weather like tomorrow?"}
				if view == "thinking-matrix" {
					r.state.Settings["screen_text_style"] = "matrix"
				}
				r.prepareGPUScene(canvas, now, true, true)
				frame, _ := r.prepareGPUScene(canvas, now.Add(time.Second), true, true)
				canvas = compositeConversationFrame(r, frame)
			}
			file, err := os.Create(filepath.Join(directory, target+"-"+view+".png"))
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(file, canvas)
			_ = file.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}
