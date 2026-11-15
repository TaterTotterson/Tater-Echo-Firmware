//go:build linux

package main

import (
	"context"
	"fmt"
	"image"
	"log"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/videostream"
)

func satChatH264Identity(chat *show.SatChat, size image.Point) string {
	if chat == nil || chat.Call == nil || chat.Call.State != "active" || chat.Call.Mode != "video" || chat.VideoStreamURL == "" || size.X <= 0 || size.Y <= 0 {
		return ""
	}
	return fmt.Sprintf("%s\x00%s\x00%dx%d", chat.Call.ID, chat.VideoStreamURL, size.X, size.Y)
}

func (c *cameraStreamController) UpdateSatChat(parent context.Context, chat *show.SatChat, size image.Point) bool {
	identity := satChatH264Identity(chat, size)
	if identity == c.identity {
		return false
	}
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	c.identity = identity
	if identity == "" {
		return true
	}
	ctx, cancel := context.WithCancel(parent)
	c.cancel = cancel
	go func() {
		pictures := newLatestDecodedVideo(c.recycle)
		work, stopWork := context.WithCancel(ctx)
		workerDone := make(chan struct{})
		go func() {
			defer close(workerDone)
			var sequence uint64
			for {
				select {
				case <-work.Done():
					return
				case source := <-pictures.frames:
					sequence++
					// The decoder buffer has already been copied into the shared
					// latest-picture pool. Keep its source geometry and let GPU v4
					// scale it once during composition. CPU fallback still fits the
					// source in drawWorkspaceVideoPicture.
					c.publish(cameraStreamResult{identity: identity, sequence: sequence, image: source})
				}
			}
		}()
		defer func() { stopWork(); <-workerDone; pictures.discard() }()
		for ctx.Err() == nil {
			err := videostream.ReadSatChatH264(ctx, chat.VideoStreamURL, func(source *image.RGBA) error {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				pictures.publish(source)
				return nil
			})
			if ctx.Err() != nil {
				return
			}
			log.Printf("[sat-chat-h264] decoder stopped: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}()
	return true
}

func (r *renderer) resetSatChatH264(c *cameraStreamController) {
	c.Release(r.satChatStreamImage)
	r.satChatStreamImage = nil
	r.satChatVideoID, r.satChatVideoShown, r.satChatVideoDrawn = "", "", ""
	r.satChatVideoImage = nil
}

func (r *renderer) applySatChatH264(c *cameraStreamController, result cameraStreamResult) {
	if result.identity == "" || result.identity != c.identity || satChatH264Identity(r.state.SatChat, r.satChatVideoBounds().Size()) != result.identity || result.image == nil {
		c.Release(result.image)
		return
	}
	c.Release(r.satChatStreamImage)
	r.satChatStreamImage = result.image
	r.satChatVideoImage = result.image
	r.satChatVideoShown = fmt.Sprintf("h264:%s:%d", result.identity, result.sequence)
}
