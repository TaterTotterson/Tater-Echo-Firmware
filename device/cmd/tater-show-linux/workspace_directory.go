//go:build linux

package main

import (
	"fmt"
	"image"
	"log"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/linuxinput"
)

type directoryInteraction struct {
	pressedKind, pressedID string
	pendingKind, pendingID string
	started                time.Time
	messageKind, message   string
	awaitingKind           string
	awaitingSince          time.Time
}

func (r *renderer) directoryCacheKey() string {
	d := &r.directoryUI
	return fmt.Sprintf("%q|%q|%q|%q|%q|%q", d.pressedKind, d.pressedID, d.pendingKind, d.pendingID, d.messageKind, d.message)
}

func (r *renderer) directoryRowAt(point image.Point) (kind, id string) {
	if !r.state.Connected || r.menuOpen || r.settingsVisible(time.Now()) || r.conversationActive() || r.notificationActive(time.Now()) || r.displayVideoVisible() {
		return "", ""
	}
	width, height := r.showSize()
	if r.cameraOpen {
		if r.state.Cameras == nil || r.state.Cameras.SelectedID != "" {
			return "", ""
		}
		items := r.state.Cameras.Items
		perPage := cameraPageSize(width, height, r.spotScreen)
		page := max(0, min(r.cameraPage, satChatPageCount(len(items), perPage)-1))
		for index := 0; index < perPage && page*perPage+index < len(items); index++ {
			item := items[page*perPage+index]
			if item.Available && point.In(cameraCardBounds(width, height, index, r.spotScreen)) {
				return "camera", item.ID
			}
		}
	} else if r.satChatVisible() && r.state.SatChat != nil && r.state.SatChat.Call == nil {
		items := r.state.SatChat.Participants
		perPage := r.satChatPageSize(width, height)
		page := r.satChatPageFor(len(items), perPage)
		for index := 0; index < perPage && page*perPage+index < len(items); index++ {
			bounds := satChatDirectoryLayoutFor(width, height).participantBounds(index)
			if r.spotScreen {
				bounds = image.Rect(90, 128+index*48, 390, 176+index*48)
			}
			item := items[page*perPage+index]
			if !item.Busy && point.In(bounds) {
				return "sat-chat", item.ID
			}
		}
	}
	return "", ""
}

func (r *renderer) noteDirectoryTouch(event linuxinput.TouchEvent) {
	d := &r.directoryUI
	if event.Kind == linuxinput.Down {
		d.pressedKind, d.pressedID = r.directoryRowAt(image.Pt(event.X, event.Y))
	} else if r.touchMoved {
		d.pressedKind, d.pressedID = "", ""
	}
}

func (r *renderer) beginDirectoryRequest(kind, id string, now time.Time) bool {
	d := &r.directoryUI
	if kind == "sat-chat" && r.state.Muted {
		d.messageKind, d.message = kind, "Unmute the microphone to start a call"
		return false
	}
	if d.pendingKind == kind && (kind == "sat-chat" || d.pendingID == id) {
		return false
	}
	d.pendingKind, d.pendingID, d.started = kind, id, now
	d.messageKind, d.message, d.awaitingKind = "", "", ""
	log.Printf("[directory] %s requested", kind)
	return true
}

func (r *renderer) updateDirectoryInteraction(now time.Time) {
	d := &r.directoryUI
	if d.pendingKind != "" {
		acknowledged := d.pendingKind == "camera" && r.state.Cameras != nil && r.state.Cameras.SelectedID == d.pendingID ||
			d.pendingKind == "sat-chat" && r.state.SatChat != nil && r.state.SatChat.Call != nil
		if acknowledged {
			log.Printf("[directory] %s host state after %s", d.pendingKind, now.Sub(d.started).Round(time.Millisecond))
			d.awaitingKind, d.awaitingSince = d.pendingKind, d.started
			if d.pendingKind == "sat-chat" && !r.satChatVideoActive() {
				d.awaitingKind = ""
			}
			d.pendingKind, d.pendingID = "", ""
		} else if !r.state.Connected || now.Sub(d.started) >= 12*time.Second {
			d.messageKind, d.message = d.pendingKind, "No response yet. Tap to try again."
			d.pendingKind, d.pendingID = "", ""
		} else if d.pendingKind == "camera" && !r.cameraOpen || d.pendingKind == "sat-chat" && !r.satChatOpen {
			d.pendingKind, d.pendingID = "", ""
		}
	}
	if d.awaitingKind != "" && now.Sub(d.awaitingSince) >= 30*time.Second {
		d.awaitingKind = ""
	}
}

func (r *renderer) noteDirectoryVideoPresented(now time.Time) {
	d := &r.directoryUI
	if d.awaitingKind == "camera" && r.cameraVideoActive() && r.cameraFrameImage != nil ||
		d.awaitingKind == "sat-chat" && r.satChatVideoActive() && r.satChatVideoImage != nil {
		log.Printf("[directory] %s first picture displayed after %s", d.awaitingKind, now.Sub(d.awaitingSince).Round(time.Millisecond))
		d.awaitingKind = ""
	}
}

func (r *renderer) directorySubtitle(kind, fallback string) string {
	d := &r.directoryUI
	if d.messageKind == kind && d.message != "" {
		return d.message
	}
	if d.pendingKind == kind {
		if kind == "camera" {
			return "Opening camera…"
		}
		return "Starting call…"
	}
	return fallback
}

func (r *renderer) directoryRowPending(kind, id string) bool {
	return r.directoryUI.pendingKind == kind && r.directoryUI.pendingID == id
}

func (r *renderer) drawDirectoryRowFeedback(canvas *image.RGBA, bounds image.Rectangle, kind, id string) {
	d := &r.directoryUI
	if d.pressedKind == kind && d.pressedID == id || r.directoryRowPending(kind, id) {
		ink := r.menuPalette().primary
		drawMenuRoundedRect(canvas, bounds.Inset(2), 10, mix(workspacePanel, ink, .16))
		drawMenuRoundedOutline(canvas, bounds.Inset(2), 10, 2, ink)
	}
}
