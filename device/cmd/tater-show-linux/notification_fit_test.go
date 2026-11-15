//go:build linux

package main

import (
	"image"
	"image/color"
	"testing"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

func TestNotificationsShowWholeImageEvenForLegacyCoverSenders(t *testing.T) {
	for _, fit := range []string{"", "cover", "fill", "contain", "fit", "COVER"} {
		for _, size := range []image.Point{image.Pt(160, 90), image.Pt(90, 160), image.Pt(100, 100)} {
			item := &show.Notification{Kind: "awareness", Source: "Awareness Core", ImageFit: fit}
			if got := notificationImageFit(item, "cover"); got != "contain" {
				t.Fatalf("%q requested crop: %q", fit, got)
			}
			source := cameraFixture(size)
			canvas := image.NewRGBA(image.Rect(0, 0, 300, 200))
			art := image.Rect(10, 10, 290, 190)
			drawNotificationImage(canvas, art, source, notificationImageFit(item, "cover"))
			// drawContain rounds, rather than floors, its fitted dimensions.
			picture := containBounds(art, size)
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
					t.Fatalf("fit=%q size=%v edge %v was cropped: %v", fit, size, edge.point, got)
				}
			}
		}
	}
}

func TestNotificationContainPolicyKeepsDefaultTaterArtwork(t *testing.T) {
	if notificationImageFit(nil, "cover") != "contain" {
		t.Fatal("missing metadata selected crop")
	}
	art, fit := notificationArtwork(nil, notificationImageFit(&show.Notification{ImageFit: "cover"}, "cover"))
	if art != taterPrimaryLogo || fit != "contain" {
		t.Fatal("default Tater artwork changed")
	}
}
