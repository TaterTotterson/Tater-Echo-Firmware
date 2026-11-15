//go:build linux

package main

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/gpucompositor"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/linuxinput"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/linuxscreen"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

func TestCronosUsesSharedGPUWorkspaceWithoutEnablingOtherTargets(t *testing.T) {
	for _, target := range []string{"checkers", "cronos", "crown", "athena", "rook"} {
		r, canvas, now := gpuTestRenderer(t)
		r.target = target
		r.menuOpen = true
		_, enabled := r.prepareGPUScene(canvas, now, true, true)
		if enabled != (target == "checkers" || target == "cronos") {
			t.Fatalf("wrong retained scene gate for %s: %t", target, enabled)
		}
	}
}

func TestGPUSatChatPagingUploadsOnEveryTap(t *testing.T) {
	for _, effect := range []string{"fade", "slide", "disabled"} {
		t.Run(effect, func(t *testing.T) {
			r, canvas, now := gpuTestRenderer(t)
			r.state.Settings = show.ScreenSettingDefaults()
			r.state.Settings["screen_popup_effect"] = effect
			r.menuOpen = true
			r.state.SatChat = &show.SatChat{}
			for index := 0; index < 10; index++ {
				r.state.SatChat.Participants = append(r.state.SatChat.Participants, show.SatChatParticipant{
					ID: fmt.Sprint(index), Name: fmt.Sprintf("Satellite %02d", index+1), Audio: true,
				})
			}
			r.prepareGPUScene(canvas, now, true, true)
			r.menuOpen, r.satChatOpen = false, true
			now = now.Add(time.Second)
			r.prepareGPUScene(canvas, now, true, true)
			previous := bytes.Clone(r.gpuScene.layer.Pix)
			client := &socketClient{} // Paging must remain local: no command is sent.
			// Include reversals during an unfinished animation, followed by a
			// settled transition; neither may wait for the next host snapshot.
			for index, page := range []int{1, 2, 1, 0, 1, 2, 3, 2} {
				bounds := satChatNextBounds(960, 480)
				if page < r.satChatPage {
					bounds = satChatPreviousBounds(960, 480)
				}
				x, y := bounds.Min.X+bounds.Dx()/2, bounds.Min.Y+bounds.Dy()/2
				handleQueuedTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Down, X: x, Y: y})
				handleQueuedTouch(r, client, linuxinput.TouchEvent{Kind: linuxinput.Up, X: x, Y: y})
				if r.satChatPage != page {
					t.Fatalf("tap %d selected page %d, want %d", index, r.satChatPage, page)
				}
				now = now.Add(40 * time.Millisecond)
				frame, ok := r.prepareGPUScene(canvas, now, true, true)
				if !ok || !frame.present || bytes.Equal(previous, r.gpuScene.layer.Pix) {
					t.Fatalf("tap %d did not redraw page %d immediately", index, page)
				}
				upload := frame.source
				if r.gpuScene.sliding && frame.effects.Text != nil {
					upload = frame.effects.Text.Pixels
				}
				if !bytes.Equal(upload, r.gpuScene.layer.Pix) {
					t.Fatalf("tap %d redrew page %d but did not upload its pixels", index, page)
				}
				previous = bytes.Clone(r.gpuScene.layer.Pix)
				if index%3 == 0 {
					now = now.Add(250 * time.Millisecond)
					r.prepareGPUScene(canvas, now, true, true)
				}
			}
			now = now.Add(250 * time.Millisecond)
			r.prepareGPUScene(canvas, now, true, true)
			settled, _ := r.prepareGPUScene(canvas, now.Add(time.Millisecond), true, true)
			if settled.present || len(settled.source) != 0 {
				t.Fatal("unchanged directory must stop uploading after the transition")
			}
		})
	}
}

func TestGPUSatChatDirectoryTracksParticipantContents(t *testing.T) {
	r, canvas, now := gpuTestRenderer(t)
	r.satChatOpen = true
	r.state.SatChat = &show.SatChat{Participants: []show.SatChatParticipant{{ID: "office", Name: "Office", Audio: true}}}
	r.prepareGPUScene(canvas, now, true, true)
	for _, edit := range []func(*show.SatChatParticipant){
		func(p *show.SatChatParticipant) { p.Name = "Kitchen" },
		func(p *show.SatChatParticipant) { p.Busy = true },
		func(p *show.SatChatParticipant) { p.Video = true },
		func(p *show.SatChatParticipant) { p.Room = "Downstairs" },
	} {
		previous := bytes.Clone(r.gpuScene.layer.Pix)
		edit(&r.state.SatChat.Participants[0])
		now = now.Add(time.Millisecond)
		frame, _ := r.prepareGPUScene(canvas, now, true, true)
		if !frame.present || len(frame.source) == 0 || bytes.Equal(previous, r.gpuScene.layer.Pix) {
			t.Fatal("changed participant details did not upload immediately")
		}
		// Re-decoded but identical host state must not invalidate the GPU cache.
		r.state.SatChat = &show.SatChat{Participants: append([]show.SatChatParticipant(nil), r.state.SatChat.Participants...)}
		unchanged, _ := r.prepareGPUScene(canvas, now.Add(time.Millisecond), true, true)
		if unchanged.present || len(unchanged.source) != 0 {
			t.Fatal("identical participant snapshot invalidated retained pixels")
		}
	}
	r.satChatPage = 9
	r.state.SatChat.Participants = nil
	cleared, _ := r.prepareGPUScene(canvas, now.Add(2*time.Millisecond), true, true)
	if !cleared.present || len(cleared.source) == 0 || r.satChatPage != 0 || r.gpuScene.surface != "sat-chat-directory:0" {
		t.Fatal("empty directory left stale participants visible")
	}
	r.prepareGPUScene(canvas, now.Add(300*time.Millisecond), true, true)
	settled, _ := r.prepareGPUScene(canvas, now.Add(301*time.Millisecond), true, true)
	if settled.present || len(settled.source) != 0 {
		t.Fatal("clamped page invalidated the cache again on the following frame")
	}
}

func TestGPUSurfaceChangeAlwaysUploadsSource(t *testing.T) {
	r, canvas, now := gpuTestRenderer(t)
	r.prepareGPUScene(canvas, now, true, true)
	for _, open := range []bool{true, false} {
		r.satChatOpen = open
		now = now.Add(time.Second)
		frame, _ := r.prepareGPUScene(canvas, now, true, true)
		if !frame.present || len(frame.source) == 0 {
			t.Fatalf("surface switch (Sat Chat open=%t) retained the previous view's source", open)
		}
	}
}

func gpuTestRenderer(t *testing.T) (*renderer, *image.RGBA, time.Time) {
	t.Helper()
	faces, err := newFaceSet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1800000000, 0)
	return &renderer{faces: faces, target: "checkers", screenWidth: 960, screenHeight: 480,
		state: show.Snapshot{Connected: true, Phase: "idle", DisplayEdgeGlow: true},
	}, image.NewRGBA(image.Rect(0, 0, 960, 480)), now
}

func TestGPUMenuFadeReusesLayersAndHandlesRapidTaps(t *testing.T) {
	r, canvas, now := gpuTestRenderer(t)
	first, ok := r.prepareGPUScene(canvas, now, true)
	if !ok || !first.present || len(first.source) == 0 || first.effects.Blend != 1 {
		t.Fatal("first scene must upload immediately")
	}
	static, ok := r.prepareGPUScene(canvas, now.Add(time.Second), true)
	if !ok || static.present || len(static.source) != 0 {
		t.Fatal("settled home must not draw or upload another frame")
	}
	r.menuOpen = true
	opened, _ := r.prepareGPUScene(canvas, now.Add(2*time.Second), true)
	if len(opened.source) == 0 || opened.effects.Blend != 0 {
		t.Fatal("menu must fade from the last displayed frame")
	}
	middle, _ := r.prepareGPUScene(canvas, now.Add(2110*time.Millisecond), true)
	if len(middle.source) != 0 || middle.effects.Blend < .49 || middle.effects.Blend > .51 {
		t.Fatalf("menu midpoint should only send uniforms: %+v", middle.effects)
	}
	r.menuOpen = false
	closed, _ := r.prepareGPUScene(canvas, now.Add(2120*time.Millisecond), true)
	if len(closed.source) == 0 || closed.effects.Blend != 0 {
		t.Fatal("interrupted close must snapshot and fade from the current GPU frame")
	}
}

func TestGPUConversationUpdatesTextWithoutFollowingPlaybackWhileListening(t *testing.T) {
	r, canvas, now := gpuTestRenderer(t)
	r.state.Phase = "listening"
	r.state.Conversation = &show.Conversation{TurnID: "turn-1", Phase: "listening"}
	first, _ := r.prepareGPUScene(canvas, now, true)
	r.state.AudioLevel = 1
	r.state.Conversation.Revision++
	next, _ := r.prepareGPUScene(canvas, now.Add(time.Second/30), true)
	if len(next.source) != 0 || next.effects.Level != first.effects.Level || !next.effects.Rim {
		t.Fatal("identical transcript should retain pixels and listening must ignore speaker level")
	}
	r.state.Conversation.UserText = "Turn on the lights"
	text, _ := r.prepareGPUScene(canvas, now.Add(100*time.Millisecond), true)
	if len(text.source) == 0 || text.effects.Blend != 0 {
		t.Fatal("new STT text must upload once and crossfade")
	}
	r.state.Conversation.Phase = "thinking"
	r.state.Phase = "thinking"
	thinking, _ := r.prepareGPUScene(canvas, now.Add(200*time.Millisecond), true)
	if !thinking.effects.Dots || !thinking.effects.Ambient {
		t.Fatal("thinking must animate GPU dots and ambient edge light")
	}
}

func TestLeavingGPUSceneRestoresCPUAndRejectsOtherTargets(t *testing.T) {
	r, canvas, now := gpuTestRenderer(t)
	r.prepareGPUScene(canvas, now, true)
	r.checkersCanvas = canvas
	if _, ok := r.prepareGPUScene(canvas, now, false); ok || r.gpuScene.active || r.checkersCanvas != nil {
		t.Fatal("GPU failure must invalidate the CPU canvas before fallback")
	}
	r.target = "crown"
	if _, ok := r.prepareGPUScene(canvas, now, true); ok {
		t.Fatal("an unqualified target selected the Checkers GPU runtime")
	}
}

func TestGPUFadePresentsFinalFrameAndReloadsWallpaper(t *testing.T) {
	r, canvas, now := gpuTestRenderer(t)
	r.prepareGPUScene(canvas, now, true)
	r.menuOpen = true
	r.prepareGPUScene(canvas, now.Add(time.Second), true)
	r.prepareGPUScene(canvas, now.Add(1200*time.Millisecond), true)
	end, _ := r.prepareGPUScene(canvas, now.Add(1230*time.Millisecond), true)
	if !end.present || end.effects.Blend != 1 {
		t.Fatal("fade must present its final fully opaque frame")
	}
	settled, _ := r.prepareGPUScene(canvas, now.Add(1260*time.Millisecond), true)
	if settled.present {
		t.Fatal("settled menu should stop rendering")
	}
	r.wallpaperID = "new-wallpaper"
	r.prepareGPUScene(canvas, now.Add(1300*time.Millisecond), true)
	r.applyScreenImage(screenImageResult{kind: wallpaperScreenImage, identity: r.wallpaperID, image: image.NewRGBA(canvas.Rect)})
	reloaded, _ := r.prepareGPUScene(canvas, now.Add(1330*time.Millisecond), true)
	if len(reloaded.source) == 0 {
		t.Fatal("completed image download did not invalidate retained GPU pixels")
	}
}

// Optional owner-device test: renders synthetic UI through Mali offscreen and
// writes previews only when explicitly requested. No framebuffer/input/audio
// device is opened by this test.
func TestGPUSceneHardwarePreview(t *testing.T) {
	directory := os.Getenv("TATER_GPU_TEST_PREVIEW_DIR")
	if directory == "" {
		t.Skip("set TATER_GPU_TEST_PREVIEW_DIR on Checkers for the offscreen test")
	}
	compositor, err := gpucompositor.Open()
	if err != nil {
		t.Fatal(err)
	}
	if compositor == nil || !compositor.SupportsScenes() {
		t.Fatal("retained GPU scene runtime unavailable")
	}
	defer compositor.Close()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	r, canvas, now := gpuTestRenderer(t)
	panel := make([]byte, gpucompositor.PanelBytes)
	steps := []struct {
		name string
		ms   int
		edit func()
	}{
		{"home", 0, func() {}},
		{"menu-start", 1000, func() { r.menuOpen = true }},
		{"menu-half", 1110, func() {}},
		{"menu", 1240, func() {}},
		{"listening-start", 2000, func() {
			r.menuOpen = false
			r.state.Phase = "listening"
			r.state.Conversation = &show.Conversation{TurnID: "preview", Phase: "listening"}
		}},
		{"listening", 2240, func() {}},
		{"stt-start", 2600, func() { r.state.Conversation.UserText = "Can you turn on" }},
		{"stt-half", 2650, func() {}},
		{"stt", 2720, func() {}},
		{"stt-append", 2730, func() { r.state.Conversation.UserText = "Can you turn on the kitchen lights?" }},
		{"stt-append-half", 2790, func() {}},
		{"stt-append-settled", 2940, func() {}},
		{"thinking", 3120, func() { r.state.Phase = "thinking"; r.state.Conversation.Phase = "thinking" }},
		{"thinking-settled", 3340, func() {}},
		{"reply", 3700, func() {
			r.state.Phase = "speaking"
			r.state.AudioLevel = .15
			r.state.Conversation.Phase = "replying"
			r.state.Conversation.AssistantText = "The kitchen lights are on."
		}},
		{"reply-settled", 3920, func() {}},
	}
	for _, step := range steps {
		step.edit()
		frame, ok := r.prepareGPUScene(canvas, now.Add(time.Duration(step.ms)*time.Millisecond), true, compositor.SupportsAnimatedText())
		if !ok || !frame.present {
			t.Fatalf("%s did not produce a GPU frame", step.name)
		}
		if err := compositor.CompositeScene(frame.source, panel, frame.effects); err != nil {
			t.Fatal(err)
		}
		preview := image.NewRGBA(canvas.Rect)
		for y := 0; y < 480; y++ {
			for x := 0; x < 960; x++ {
				from, to := (x*480+479-y)*4, (y*960+x)*4
				preview.Pix[to], preview.Pix[to+1], preview.Pix[to+2], preview.Pix[to+3] = panel[from+2], panel[from+1], panel[from], panel[from+3]
			}
		}
		file, err := os.Create(filepath.Join(directory, step.name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(file, preview)
		_ = file.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}

// Run only with the normal display owner stopped and an explicit target gate.
// The test presents synthetic text; it never sends commands to the daemon.
func TestGPUSceneHardwareDisplay(t *testing.T) {
	if os.Getenv("TATER_GPU_TEST_DISPLAY") != "checkers" {
		t.Skip("set TATER_GPU_TEST_DISPLAY=checkers with the normal renderer stopped")
	}
	display, err := linuxscreen.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer display.Close()
	if !display.SupportsGPUScenes() {
		t.Fatal("GPU scene display unavailable")
	}
	r, _, _ := gpuTestRenderer(t)
	r.state.Phase = "listening"
	r.state.Conversation = &show.Conversation{TurnID: "display-test", Phase: "listening"}
	const frames = 120
	started := time.Now()
	var rendering, presenting, slowest time.Duration
	for index := 0; index < frames; index++ {
		frameStart := time.Now()
		if index < 40 && index%5 == 0 {
			r.state.Conversation.UserText = fmt.Sprintf("Testing smooth on-screen speech, update %d", index/5+1)
		}
		if index == 40 {
			r.state.Phase, r.state.Conversation.Phase = "thinking", "thinking"
		}
		if index == 80 {
			r.state.Phase, r.state.Conversation.Phase = "speaking", "replying"
			r.state.Conversation.AssistantText = "The GPU display test is running."
			r.state.AudioLevel = .15
		}
		frame, ok := r.prepareGPUScene(display.Canvas(), frameStart, true, display.SupportsAnimatedText())
		if !ok || !frame.present {
			t.Fatal("animated scene did not submit a frame")
		}
		rendering += time.Since(frameStart)
		presentStart := time.Now()
		used, err := display.PresentGPUScene(frame.source, frame.effects)
		if err != nil || !used {
			t.Fatalf("GPU presentation failed: used=%t error=%v", used, err)
		}
		presenting += time.Since(presentStart)
		if len(frame.source) > 0 {
			r.gpuScene.started = time.Now()
		}
		if elapsed := time.Since(frameStart); elapsed > slowest {
			slowest = elapsed
		}
		if remaining := time.Second/30 - time.Since(frameStart); remaining > 0 {
			time.Sleep(remaining)
		}
	}
	elapsed := time.Since(started)
	fps := float64(frames) / elapsed.Seconds()
	t.Logf("presented=%d fps=%.2f render_avg=%s present_avg=%s slowest=%s", frames, fps,
		rendering/frames, presenting/frames, slowest)
	if fps < 24 {
		t.Fatalf("GPU UI missed its useful animation cadence: %.2f fps", fps)
	}
}
