package main

import (
	"context"
	"log"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/companiondisplay"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

func startCompanionDisplay(ctx context.Context, screen *show.Server, deviceID, name, room, board, version string) {
	if screen == nil {
		return
	}
	service, err := companiondisplay.New(companiondisplay.Config{
		DeviceID: deviceID, Name: name, Room: room, Board: board, FirmwareVersion: version,
		Advertise: true,
		Update: func(update companiondisplay.NowPlaying, artwork []byte, contentType string) {
			if !update.Active {
				screen.SetTaterTube(nil, nil, "")
				return
			}
			screen.SetTaterTube(&show.TaterTubeMedia{
				Active: true, PlayerID: update.SourceID, PlayerName: update.SourceName,
				EventID: update.EventID, PlaybackState: update.PlaybackState,
				MediaType: update.MediaType, Title: update.Title, SeriesTitle: update.SeriesTitle,
				Season: update.Season, Episode: update.Episode, Subtitle: update.Subtitle,
				ProgressMS: update.ProgressMS, DurationMS: update.DurationMS,
				PlaybackSpeed: update.PlaybackSpeed, ProgressUpdatedAtUnixMS: update.ProgressUpdatedAtUnixMS,
				ExpiresAtUnixMS: update.ExpiresAtUnixMS, Revision: update.Revision,
			}, artwork, contentType)
		},
		Notify: func(update companiondisplay.Notification, image []byte, contentType string) {
			notification := &show.Notification{
				ID: update.SourceID + ":" + update.EventID, Kind: update.Kind,
				Label: update.DisplayLabel, Priority: update.Priority, Title: update.Title,
				CameraName: update.CameraName, Description: update.Description,
				Source: update.Source, ImageFit: update.ImageFit,
				CreatedAtUnixMS: update.CreatedAtUnixMS, ExpiresAtUnixMS: update.ExpiresAtUnixMS,
				TTLMS: update.TTLMS,
			}
			screen.SetNotification(notification, image, contentType)
			if update.RememberAsLastEvent {
				screen.SetLastEvent(notification, image, contentType)
			}
		},
	})
	if err != nil {
		log.Printf("[companion-display] unavailable: %v", err)
		return
	}
	go func() {
		if err := service.Start(ctx); err != nil && ctx.Err() == nil {
			log.Printf("[companion-display] stopped: %v", err)
		}
	}()
}
