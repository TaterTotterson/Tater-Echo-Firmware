//go:build linux

package main

import (
	"embed"
	"fmt"
	"image"
	"io/fs"
	"strings"
	"sync"
)

// The built-in scenes are stored at each display's native resolution.
// Keeping the JPEGs compressed in the executable avoids extra rootfs files;
// only the one scene used today is decoded into memory.
//
//go:embed assets/wallpapers/checkers/*.jpg assets/wallpapers/crown/*.jpg assets/weather/*.png
var wallpaperFiles embed.FS

var (
	weatherArtOnce sync.Once
	weatherArtSet  map[string]image.Image
)

var defaultWallpaperNames = [...]string{
	"mountains",
	"forest",
	"ocean",
	"desert",
	"waterfall",
	"night",
	"alpine-sunset",
	"misty-lake",
	"desert-dusk",
}

func defaultWallpaperCount() int {
	return len(defaultWallpaperNames)
}

func readDefaultWallpaper(target string, index int) (image.Image, string, error) {
	if len(defaultWallpaperNames) == 0 {
		return nil, "", fmt.Errorf("no built-in wallpapers")
	}
	if index < 0 {
		index = -index
	}
	index %= len(defaultWallpaperNames)
	family := "checkers"
	if target == "crown" || target == "athena" || target == "rook" {
		family = "crown"
	}
	identity := family + "/" + defaultWallpaperNames[index]
	file, err := wallpaperFiles.Open("assets/wallpapers/" + identity + ".jpg")
	if err != nil {
		return nil, identity, err
	}
	defer file.Close()
	decoded, _, err := image.Decode(file)
	if err != nil {
		return nil, identity, err
	}
	return decoded, identity, nil
}

func embeddedWallpaperFiles() ([]string, error) {
	return fs.Glob(wallpaperFiles, "assets/wallpapers/*/*.jpg")
}

func weatherArt(kind string) image.Image {
	weatherArtOnce.Do(func() {
		weatherArtSet = make(map[string]image.Image, 9)
		for _, name := range []string{"sun", "moon", "partly", "partly-night", "cloud", "rain", "storm", "snow", "fog"} {
			file, err := wallpaperFiles.Open("assets/weather/" + name + ".png")
			if err != nil {
				continue
			}
			decoded, _, decodeErr := image.Decode(file)
			_ = file.Close()
			if decodeErr == nil {
				weatherArtSet[name] = decoded
			}
		}
	})
	token := strings.ToLower(strings.TrimSpace(kind))
	switch {
	case strings.Contains(token, "partly") && strings.Contains(token, "night"):
		token = "partly-night"
	case strings.Contains(token, "partly"):
		token = "partly"
	case token == "moon" || strings.Contains(token, "night") || strings.Contains(token, "clear-night"):
		token = "moon"
	case token == "sun" || token == "sunny" || token == "clear" || strings.Contains(token, "clear-day"):
		token = "sun"
	case strings.Contains(token, "thunder") || strings.Contains(token, "storm") || strings.Contains(token, "lightning"):
		token = "storm"
	case strings.Contains(token, "snow") || strings.Contains(token, "sleet") || strings.Contains(token, "ice"):
		token = "snow"
	case strings.Contains(token, "rain") || strings.Contains(token, "shower") || strings.Contains(token, "drizzle"):
		token = "rain"
	case strings.Contains(token, "fog") || strings.Contains(token, "mist") || strings.Contains(token, "haze"):
		token = "fog"
	case strings.Contains(token, "cloud") || strings.Contains(token, "overcast"):
		token = "cloud"
	default:
		return nil
	}
	return weatherArtSet[token]
}
