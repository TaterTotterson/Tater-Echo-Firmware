//go:build linux

package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/linuxinput"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
	"golang.org/x/image/font"
)

const spotHoldDelay = 350 * time.Millisecond

func spotTimerStopBounds() image.Rectangle { return image.Rect(156, 385, 324, 427) }

// The Spot's visible panel is round. Keep text and controls inside its safe
// circle; the square framebuffer corners are deliberately only background.
func (r *renderer) renderSpot(canvas *image.RGBA, now time.Time, seconds float64, accent color.RGBA) {
	r.renderSpotFrame(canvas, now, seconds, accent, true)
}

func (r *renderer) renderSpotFrame(canvas *image.RGBA, now time.Time, seconds float64, accent color.RGBA, motion bool) {
	speaking := r.state.Phase == "speaking"
	if !speaking {
		r.spotReplyGlow = 0
	}
	if r.state.Phase == "setup" {
		r.drawSpotSetup(canvas, seconds, accent)
		return
	}
	if !r.state.Connected && (r.state.Phase == "offline" || r.state.Phase == "connecting") {
		r.drawSpotConnecting(canvas, seconds)
		return
	}
	if r.satChatVisible() {
		palette := r.menuPalette()
		if wallpaper := r.activeWallpaper(now); wallpaper != nil {
			drawPreparedCover(canvas, canvas.Bounds(), wallpaper)
		} else {
			r.spotBackground(canvas, accent)
		}
		draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.NRGBA{A: 84}), image.Point{}, draw.Over)
		panel := cameraDirectoryLayoutFor(480, 480, true).panel
		drawMenuSuperellipseShadow(canvas, panel, 8)
		drawMenuSuperellipse(canvas, panel, menuBackdropInk)
		drawMenuSuperellipseOutline(canvas, panel, 2, menuAlpha(palette.primary, 230))
		r.drawSpotSatChat(canvas)
		maskOutsideSpotCircle(canvas, 236)
		return
	}

	legacyContext := true
	switch {
	case r.notificationActive(now):
		r.drawSpotLegacyBackdrop(canvas, now, accent)
		r.drawSpotNotification(canvas, now, seconds, accent)
	case r.state.Timer != nil && r.state.Timer.Active:
		r.drawSpotLegacyBackdrop(canvas, now, accent)
		r.drawSpotTimer(canvas, now, seconds, accent)
	case r.climateOpen:
		legacyContext = false
		r.drawSpotClimate(canvas, now, accent)
	case r.cameraOpen:
		legacyContext = false
		r.drawSpotCameraScreen(canvas, now, accent)
	case r.menuOpen:
		legacyContext = false
		r.drawSpotActionMenu(canvas, now, accent)
	case r.state.Media != nil && r.state.Media.Active:
		r.drawSpotLegacyBackdrop(canvas, now, accent)
		r.drawSpotNowPlaying(canvas, now, seconds, accent, motion)
	default:
		legacyContext = false
		r.drawSpotPhotoHome(canvas, now, accent)
	}
	if voiceEdgeActive(r.state.Phase) && r.state.DisplayEdgeGlow {
		if speaking {
			if motion {
				r.drawSpotReplyRim(canvas, accent)
			}
		} else {
			r.drawSpotStateRim(canvas, accent)
		}
	}
	if legacyContext && (motion || !speaking || !r.state.DisplayEdgeGlow) {
		r.drawSpotStatus(canvas)
	}
}

func (r *renderer) drawSpotLegacyBackdrop(canvas *image.RGBA, now time.Time, accent color.RGBA) {
	r.spotBackground(canvas, accent)
	local := r.localTime(now)
	r.centeredText(canvas, 240, 111, local.Format("3:04"), 82, false, color.White)
	r.centeredText(canvas, 240, 138, strings.ToUpper(local.Format("Mon · Jan 2")), 16, true, color.RGBA{167, 180, 199, 255})
	line(canvas, 125, 158, 355, 158, 1, color.RGBA{226, 154, 90, 48})
}

func (r *renderer) drawSpotPhotoHome(canvas *image.RGBA, now time.Time, accent color.RGBA) {
	if wallpaper := r.activeWallpaper(now); wallpaper != nil {
		drawPreparedCover(canvas, canvas.Bounds(), wallpaper)
	} else {
		fillGradient(canvas, color.RGBA{6, 11, 19, 255}, mix(color.RGBA{6, 11, 19, 255}, accent, .13))
	}
	drawSpotPhotoShade(canvas)

	local := r.localTime(now)
	weather := r.state.Weather
	timeText := local.Format("3:04")
	clockSize, temperatureSize, weatherIconSize := 74, 48, 42
	temperature, temperatureWidth := "", 0
	if weather != nil {
		temperature = strings.TrimSpace(weather.TemperatureText)
		if temperature != "" && weather.TemperatureUnit != "" && !strings.Contains(temperature, weather.TemperatureUnit) && !strings.Contains(temperature, "°") {
			temperature += weather.TemperatureUnit
		}
		temperatureWidth = font.MeasureString(r.faces.regular[temperatureSize], temperature).Round()
	}
	r.centeredText(canvas, 240, 237, timeText, clockSize, false, color.White)
	if temperature != "" {
		weatherGap := 9
		weatherLeft := (480 - weatherIconSize - weatherGap - temperatureWidth) / 2
		kind := weatherDisplayKind(weather, local)
		if art := weatherArt(kind); art != nil {
			drawContain(canvas, image.Rect(weatherLeft, 252, weatherLeft+weatherIconSize, 294), art)
		} else {
			r.drawWeatherIcon(canvas, weatherLeft+weatherIconSize/2, 273, 13, kind, 0, color.RGBA{255, 255, 255, 255})
		}
		r.text(canvas, weatherLeft+weatherIconSize+weatherGap, 291, temperature, temperatureSize, false, color.White)
	}

	detail := local.Format("Monday, January 2")
	if weather != nil && strings.TrimSpace(weather.Condition) != "" {
		detail += "  •  " + strings.TrimSpace(weather.Condition)
	}
	r.centeredText(canvas, 240, 317, fitTextToWidth(detail, r.faces.regular[13], 350), 13, false, color.RGBA{250, 252, 255, 242})
	room := strings.TrimSpace(r.state.Room)
	if room == "" {
		room = "Rook"
	}
	r.drawHomeRoomStatus(canvas, room, 240, 340, 350, 12, true)

	type metric struct {
		kind, label, value string
		ink                color.RGBA
	}
	metrics := make([]metric, 0, 6)
	if weather != nil {
		if weather.HumidityText != "" {
			metrics = append(metrics, metric{"humidity", "HUMID", weather.HumidityText, color.RGBA{83, 178, 255, 255}})
		}
		if weather.WindText != "" {
			metrics = append(metrics, metric{"wind", "WIND", weather.WindText, color.RGBA{83, 211, 216, 255}})
		}
		if weather.RainText != "" {
			metrics = append(metrics, metric{"rain", "RAIN", weather.RainText, color.RGBA{64, 190, 239, 255}})
		}
		if weather.LightningText != "" {
			metrics = append(metrics, metric{"lightning", "LIGHT", weather.LightningText, color.RGBA{255, 208, 30, 255}})
		}
		if weather.IndoorTemperatureText != "" {
			metrics = append(metrics, metric{"temperature", "ROOM T", weather.IndoorTemperatureText, color.RGBA{255, 147, 66, 255}})
		}
		if weather.IndoorHumidityText != "" {
			metrics = append(metrics, metric{"humidity", "ROOM RH", weather.IndoorHumidityText, color.RGBA{114, 92, 255, 255}})
		}
	}
	if len(metrics) > 0 {
		for index, item := range metrics {
			cell, rowTop := spotPhotoMetricArcCell(len(metrics), index)
			iconX, iconY := cell.Min.X+12, rowTop+12
			textLeft := iconX + 16
			// Adjacent metrics sit at different heights along the arc, so their
			// labels can use the diagonal space beyond the nominal slot edge.
			const textWidth = 42
			r.drawWeatherMetricGlyph(canvas, iconX, iconY, 7, item.kind, item.ink)
			r.text(canvas, textLeft, rowTop+7, fitTextToWidth(item.label, r.faces.bold[8], textWidth), 8, true, color.RGBA{224, 230, 238, 230})
			valueSize := 11
			if item.kind == "lightning" {
				valueSize = 10
			}
			r.text(canvas, textLeft, rowTop+24, fitTextToWidth(item.value, r.faces.regular[valueSize], textWidth), valueSize, false, color.White)
		}
	}
	maskOutsideSpotCircle(canvas, 236)
}

func spotPhotoMetricArcCell(visibleCount, visibleIndex int) (image.Rectangle, int) {
	cell := centeredMetricCell(54, 372, 6, visibleCount, visibleIndex)
	if cell.Empty() {
		return cell, 0
	}
	// Follow the lower edge of the 236-pixel safe circle. Using the cell's
	// outermost edge keeps the glyph and both text baselines clear of the mask
	// while naturally lifting the metrics toward the sides of the display.
	outerX := max(int(math.Abs(float64(cell.Min.X-2-240))), int(math.Abs(float64(cell.Max.X+2-240))))
	inside := max(0, 236*236-outerX*outerX)
	arcBottom := 240 + int(math.Sqrt(float64(inside)))
	return cell, arcBottom - 32
}

func drawSpotPhotoShade(canvas *image.RGBA) {
	for y := 132; y < canvas.Rect.Dy(); y++ {
		progress := float64(y-132) / float64(max(1, canvas.Rect.Dy()-133))
		progress = progress * progress * (3 - 2*progress)
		alpha := uint8(190 * progress)
		draw.Draw(canvas, image.Rect(0, y, canvas.Rect.Dx(), y+1), image.NewUniform(color.NRGBA{A: alpha}), image.Point{}, draw.Over)
	}
}

func maskOutsideSpotCircle(canvas *image.RGBA, radius int) {
	cx, cy := canvas.Rect.Dx()/2, canvas.Rect.Dy()/2
	radiusSquared := radius * radius
	for y := 0; y < canvas.Rect.Dy(); y++ {
		dy := y - cy
		for x := 0; x < canvas.Rect.Dx(); x++ {
			dx := x - cx
			if dx*dx+dy*dy > radiusSquared {
				canvas.SetRGBA(x, y, color.RGBA{0, 0, 0, 255})
			}
		}
	}
}

func (r *renderer) drawSpotStatus(canvas *image.RGBA) {
	status, statusColor := assistantStatus(r.state)
	r.centeredText(canvas, 240, 449, fitTextToWidth(status, r.faces.bold[14], 214), 14, true, statusColor)
}

func (r *renderer) canCacheSpot(now time.Time) bool {
	if !r.spotScreen || !r.state.Connected || r.intercomPressed || r.timerPressed || r.spotTouchDown || r.notificationActive(now) || r.satChatVisible() {
		return false
	}
	if r.state.Timer != nil && r.state.Timer.Active {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(r.state.Phase)) {
	case "", "idle", "playing", "music", "listening", "thinking", "speaking", "intercom", "tool_call":
		return true
	default:
		return false
	}
}

func (r *renderer) spotFrameCacheKey(now time.Time, accent color.RGBA) string {
	media := r.state.Media
	_, duration := mediaProgress(media, now)
	return strings.Join([]string{
		r.state.Phase,
		r.state.ToolName,
		r.state.ToolMessage,
		mediaCacheIdentity(media),
		r.state.Room,
		r.state.DisplayTheme,
		fmt.Sprint(r.state.DisplayEdgeGlow),
		r.state.Message,
		r.state.DeviceName,
		r.state.AssistantName,
		fmt.Sprint(r.state.Connected, "|", r.state.Muted, "|", r.state.TimerActive),
		fmt.Sprint(accent),
		stableCacheValue(r.state.Weather),
		stableCacheValue(r.state.Climate) + "|" + fmt.Sprint(r.climateOpen, "|", r.climatePage) + "|" + fmt.Sprint(r.cameraOpen, "|", r.cameraPage) + "|" + fmt.Sprint(r.menuOpen),
		stableCacheValue(r.state.Cameras),
		stableCacheValue(r.state.Timer),
		fmt.Sprint(duration > 0),
		r.localTime(now).Format("200601021504"),
		fmt.Sprint(r.mediaImage != nil),
		r.wallpaperID,
		fmt.Sprint(r.wallpaperImage != nil),
		fmt.Sprint(r.state.WallpaperURLs),
	}, "|")
}

func (r *renderer) drawSpotMotion(canvas *image.RGBA, now time.Time, seconds float64, accent color.RGBA) {
	if r.state.Phase == "speaking" && r.state.DisplayEdgeGlow {
		r.drawSpotReplyRim(canvas, accent)
		if r.state.Media != nil && r.state.Media.Active {
			defer r.drawSpotStatus(canvas)
		}
	}
	if r.state.Timer != nil && r.state.Timer.Active {
		return
	}
	if r.climateOpen || r.cameraOpen || r.menuOpen {
		return
	}
	if media := r.state.Media; media != nil && media.Active {
		ink := mediaDisplayColor(media, accent)
		if r.mediaImage == nil {
			r.drawMusicVisualizerBars(canvas, image.Rect(170, 190, 310, 330), seconds, ink)
		}
		if r.musicProgressNeedsRedraw(now) {
			r.drawSpotMediaProgress(canvas, now, media, ink)
			r.musicProgressDrawn = musicProgressVisualKey(media, now)
		}
		return
	}
}

func (r *renderer) spotMotionRegions(now time.Time) []image.Rectangle {
	regions := make([]image.Rectangle, 0, 5)
	if r.state.Phase == "speaking" && r.state.DisplayEdgeGlow {
		// The round reply rim crosses the framebuffer diagonals, so its restore
		// area is the whole small 480x480 canvas. The expensive text and shape
		// composition is still reused from the cache before the rim is painted.
		return []image.Rectangle{image.Rect(0, 0, 480, 480)}
	}
	if r.state.Timer != nil && r.state.Timer.Active {
		return regions
	}
	if r.climateOpen || r.cameraOpen || r.menuOpen {
		return regions
	}
	if media := r.state.Media; media != nil && media.Active {
		if r.mediaImage == nil {
			regions = append(regions, musicVisualizerMotionBounds(image.Rect(170, 190, 310, 330)))
		}
		if r.musicProgressNeedsRedraw(now) {
			regions = append(regions, image.Rect(112, 388, 368, 432))
		}
		return regions
	}
	return regions
}

func (r *renderer) drawSpotNowPlaying(canvas *image.RGBA, now time.Time, seconds float64, accent color.RGBA, motion bool) {
	media := r.state.Media
	ink := mediaDisplayColor(media, accent)
	heading := nowPlayingHeading(r.state, media)
	r.centeredText(canvas, 240, 181, fitTextToWidth(heading, r.faces.bold[14], 270), 14, true, ink)

	art := image.Rect(170, 190, 310, 330)
	roundedRect(canvas, art, 18, mix(color.RGBA{15, 22, 34, 255}, ink, .14))
	if r.mediaImage != nil {
		drawCover(canvas, art, r.mediaImage)
	} else {
		r.drawMusicVisualizerBackdrop(canvas, art, ink)
	}

	r.centeredText(canvas, 240, 360, fitTextToWidth(firstNonEmpty(media.Title, "Music"), r.faces.bold[22], 300), 22, true, color.White)
	r.centeredText(canvas, 240, 383, fitTextToWidth(firstNonEmpty(media.Artist, media.AlbumArtist, media.GroupName, "Tater Music"), r.faces.regular[15], 270), 15, false, color.RGBA{174, 190, 211, 255})
	_, duration := mediaProgress(media, now)
	if duration > 0 {
		if motion && r.musicProgressNeedsRedraw(now) {
			r.drawSpotMediaProgress(canvas, now, media, ink)
			r.musicProgressDrawn = musicProgressVisualKey(media, now)
		}
	} else if strings.TrimSpace(media.GroupName) != "" {
		r.centeredText(canvas, 240, 416, fitTextToWidth(strings.ToUpper(media.GroupName), r.faces.bold[14], 230), 14, true, ink)
	}
}

func (r *renderer) drawSpotMediaProgress(canvas *image.RGBA, now time.Time, media *show.Media, ink color.RGBA) {
	progress, duration := mediaProgress(media, now)
	if duration <= 0 {
		return
	}
	left, right, barY := 122, 358, 402
	line(canvas, left, barY, right, barY, 3, color.RGBA{95, 108, 128, 85})
	filled := int(float64(right-left) * math.Min(1, float64(progress)/float64(duration)))
	line(canvas, left, barY, left+filled, barY, 3, ink)
	r.text(canvas, left, 424, formatMediaTime(progress), 14, true, color.RGBA{190, 203, 220, 255})
	end := formatMediaTime(duration)
	endWidth := font.MeasureString(r.faces.bold[14], end).Round()
	r.text(canvas, right-endWidth, 424, end, 14, true, color.RGBA{139, 155, 177, 255})
}

func (r *renderer) spotBackground(canvas *image.RGBA, accent color.RGBA) {
	if r.spotBase == nil || r.spotBaseAccent != accent {
		r.spotBase = image.NewRGBA(canvas.Bounds())
		r.spotBaseAccent = accent
		fillGradient(r.spotBase, color.RGBA{5, 9, 17, 255}, mix(color.RGBA{7, 12, 20, 255}, accent, .11))
		circleOutline(r.spotBase, 240, 240, 232, 2, color.RGBA{accent.R, accent.G, accent.B, 42})
		circleOutline(r.spotBase, 240, 240, 220, 1, color.RGBA{190, 203, 225, 18})
	}
	draw.Draw(canvas, canvas.Bounds(), r.spotBase, image.Point{}, draw.Src)
}

// drawSpotReplyRim is the Spot's reply indicator. The snapshot carries the
// live speaker RMS (doubled by the daemon). The Spot's display needs a
// brighter, more sensitive response than the physical LED ring, with the
// same fast attack/slower release rather than an unrelated wobble.
// Only the outer 24 pixels are touched; the weather and status stay readable.
func (r *renderer) drawSpotReplyRim(canvas *image.RGBA, replyColor color.RGBA) {
	r.drawSpotVoiceRim(canvas, replyColor, true)
}

func (r *renderer) drawSpotStateRim(canvas *image.RGBA, stateColor color.RGBA) {
	r.drawSpotVoiceRim(canvas, stateColor, false)
}

func (r *renderer) drawSpotVoiceRim(canvas *image.RGBA, stateColor color.RGBA, reactive bool) {
	level := .24
	if reactive {
		target := math.Pow(math.Min(1, math.Max(0, r.state.AudioLevel)/.22), .65)
		response := .30
		if target > r.spotReplyGlow {
			response = .60
		}
		r.spotReplyGlow += (target - r.spotReplyGlow) * response
		level = r.spotReplyGlow
	}

	cx, cy := canvas.Rect.Dx()/2, canvas.Rect.Dy()/2
	outer := min(cx, cy) - 2
	inner := outer - 24
	core := outer - 5
	middle := outer - 12
	outerSquared, innerSquared := outer*outer, inner*inner
	coreSquared, middleSquared := core*core, middle*middle
	ink := mix(stateColor, color.RGBA{255, 255, 255, 255}, .04+.10*level)
	for y := max(0, cy-outer); y <= min(canvas.Rect.Dy()-1, cy+outer); y++ {
		dy := y - cy
		maxDX := int(math.Sqrt(float64(outerSquared - dy*dy)))
		minDX := 0
		if dy*dy < innerSquared {
			minDX = int(math.Sqrt(float64(innerSquared - dy*dy)))
		}
		for dx := minDX; dx <= maxDX; dx++ {
			distance := dx*dx + dy*dy
			if distance < innerSquared || distance > outerSquared {
				continue
			}
			switch {
			case distance >= coreSquared:
				ink.A = uint8(80 + 170*level)
			case distance >= middleSquared:
				ink.A = uint8(28 + 100*level)
			default:
				ink.A = uint8(8 + 48*level)
			}
			blendPixel(canvas, cx+dx, y, ink)
			if dx != 0 {
				blendPixel(canvas, cx-dx, y, ink)
			}
		}
	}
}

func (r *renderer) drawSpotWeather(canvas *image.RGBA, seconds float64, accent color.RGBA, motion bool) {
	w := r.state.Weather
	temperature := strings.TrimSpace(w.TemperatureText)
	humidity := strings.TrimSpace(w.HumidityText)
	indoorTemperature := strings.TrimSpace(w.IndoorTemperatureText)
	indoorHumidity := strings.TrimSpace(w.IndoorHumidityText)
	hasSensorReadings := temperature != "" || humidity != "" || indoorTemperature != "" || indoorHumidity != ""
	heading := "OUTSIDE CONDITIONS"
	headingColor := accent
	if w.Stale {
		heading = "WEATHER MAY BE OUTDATED"
		headingColor = color.RGBA{255, 174, 106, 255}
	}
	if r.state.Phase == "tool_call" {
		heading = "WORKING"
		headingColor = accent
		if tool := titleWords(r.state.ToolName); tool != "" {
			heading = "USING " + strings.ToUpper(tool)
		}
	}
	weatherAccent := weatherColor(w.ConditionKind)
	iconY := 242
	iconRadius := 46
	conditionKind := strings.ToLower(strings.TrimSpace(w.ConditionKind))
	if conditionKind == "sun" || conditionKind == "partly" || strings.Contains(conditionKind, "clear") {
		// Partly sunny art places the sun above its center; keep it clear of
		// the heading without lowering rain and snow into the description.
		iconY = 254
	}
	if !hasSensorReadings {
		// A deliberately empty profile is a useful minimal mode: the clock and
		// animated current condition remain, with no fake placeholders or
		// automatic sensor fallbacks.
		iconY = 272
		iconRadius = 64
		if motion {
			r.drawWeatherIcon(canvas, 240, iconY, iconRadius, w.ConditionKind, seconds, weatherAccent)
		}
		return
	}

	r.centeredText(canvas, 240, 177, fitTextToWidth(heading, r.faces.bold[14], 250), 14, true, headingColor)
	if motion {
		r.drawWeatherIcon(canvas, 240, iconY, iconRadius, w.ConditionKind, seconds, weatherAccent)
	}

	metricSize := r.spotOuterMetricSize(temperature, humidity)
	if temperature != "" {
		temperature = fitTextToWidth(temperature, r.faces.bold[metricSize], 136)
		r.drawSpotOuterMetric(canvas, 107, 275, temperature, metricSize, 136)
		if feels, feelsColor := spotFeelsBadge(w); feels != "" {
			temperatureRight := 107 + font.MeasureString(r.faces.bold[metricSize], temperature).Round()/2
			badgeCenter := min(temperatureRight-5, 166)
			r.centeredText(canvas, badgeCenter, 275, feels, 18, true, feelsColor)
		}
	}
	if humidity != "" {
		r.drawSpotOuterMetric(canvas, 371, 275, humidity, metricSize, 148)
	}
	if condition := strings.TrimSpace(w.Condition); condition != "" {
		r.centeredText(canvas, 240, 322, fitTextToWidth(condition, r.faces.bold[20], 282), 20, true, color.RGBA{235, 242, 250, 255})
	}
	r.drawSpotRoomClimate(canvas, accent, indoorTemperature, indoorHumidity)
}

// Keep both outside readings optically balanced. In particular, a percent
// sign makes a normal humidity reading wider than a temperature reading; it
// should use the available round-screen width rather than shrinking only the
// right side. If either value truly cannot fit, both step down together.
func (r *renderer) spotOuterMetricSize(left, right string) int {
	const large = 64
	if (left != "" && r.spotOuterMetricWidth(left, large) > 136) ||
		(right != "" && r.spotOuterMetricWidth(right, large) > 148) {
		return 48
	}
	return large
}

func (r *renderer) spotOuterMetricWidth(value string, size int) int {
	if number, percent := spotPercentValue(value); percent {
		return font.MeasureString(r.faces.bold[size], number).Round() + 4 +
			font.MeasureString(r.faces.bold[18], "%").Round()
	}
	return font.MeasureString(r.faces.bold[size], value).Round()
}

func (r *renderer) drawSpotOuterMetric(canvas *image.RGBA, centerX, baseline int, value string, size, maxWidth int) {
	if number, percent := spotPercentValue(value); percent {
		unitWidth := font.MeasureString(r.faces.bold[18], "%").Round()
		number = fitTextToWidth(number, r.faces.bold[size], max(1, maxWidth-unitWidth-4))
		numberWidth := font.MeasureString(r.faces.bold[size], number).Round()
		left := centerX - (numberWidth+4+unitWidth)/2
		r.text(canvas, left, baseline, number, size, true, color.White)
		r.text(canvas, left+numberWidth+4, baseline, "%", 18, true, color.White)
		return
	}
	r.centeredText(canvas, centerX, baseline, fitTextToWidth(value, r.faces.bold[size], maxWidth), size, true, color.White)
}

func spotPercentValue(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if !strings.HasSuffix(trimmed, "%") {
		return trimmed, false
	}
	number := strings.TrimSpace(strings.TrimSuffix(trimmed, "%"))
	return number, number != ""
}

// The small number beneath the degree symbol is the apparent temperature. It
// stays with the outside-temperature cluster without spending a whole row.
func spotFeelsBadge(w *show.Weather) (string, color.RGBA) {
	if w == nil || strings.TrimSpace(w.FeelsLikeText) == "" {
		return "", color.RGBA{}
	}
	value := fitSpotTemperatureText(trimFeelsLike(w.FeelsLikeText))
	ink := color.RGBA{190, 190, 190, 255}
	if actual, actualOK := spotTemperatureValue(w.TemperatureText); actualOK {
		if apparent, apparentOK := spotTemperatureValue(value); apparentOK {
			switch {
			case apparent > actual:
				return value, color.RGBA{255, 125, 105, 255}
			case apparent < actual:
				return value, color.RGBA{99, 186, 255, 255}
			default:
				return value, ink
			}
		}
	}
	switch strings.ToLower(strings.TrimSpace(w.FeelsLikeRelation)) {
	case "warmer":
		ink = color.RGBA{255, 125, 105, 255}
	case "cooler":
		ink = color.RGBA{99, 186, 255, 255}
	}
	return value, ink
}

func fitSpotTemperatureText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return ellipsize(value, 6)
}

func spotTemperatureValue(value string) (float64, bool) {
	value = trimFeelsLike(value)
	start, end := -1, -1
	for index, character := range value {
		if character >= '0' && character <= '9' || character == '-' || character == '.' {
			if start < 0 {
				start = index
			}
			end = index + 1
		} else if start >= 0 {
			break
		}
	}
	if start < 0 {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(value[start:end], 64)
	return parsed, err == nil
}

func (r *renderer) drawSpotRoomClimate(canvas *image.RGBA, accent color.RGBA, temperature, humidity string) {
	temperature, humidity = strings.TrimSpace(temperature), strings.TrimSpace(humidity)
	if temperature == "" && humidity == "" {
		return
	}
	// Echo Show uses an open room-climate strip. Keep the same visual language
	// here, scaled to the Spot's narrower round display instead of boxing the
	// two readings into a card.
	line(canvas, 125, 358, 133, 351, 2, accent)
	line(canvas, 133, 351, 141, 358, 2, accent)
	line(canvas, 128, 357, 128, 365, 2, accent)
	line(canvas, 128, 365, 138, 365, 2, accent)
	line(canvas, 138, 365, 138, 357, 2, accent)
	heading := fitTextToWidth(spotRoomClimateHeading(r.state.Room), r.faces.bold[14], 194)
	r.text(canvas, 151, 364, heading, 14, true, accent)
	lineStart := 151 + font.MeasureString(r.faces.bold[14], heading).Round() + 12
	if lineStart < 355 {
		line(canvas, lineStart, 359, 355, 359, 1, color.RGBA{accent.R, accent.G, accent.B, 60})
	}
	if temperature != "" && humidity != "" {
		r.drawSpotRoomMetric(canvas, 132, "temperature", "TEMP", temperature, color.RGBA{255, 147, 66, 255})
		r.drawSpotRoomMetric(canvas, 266, "humidity", "HUMIDITY", humidity, color.RGBA{83, 178, 255, 255})
		return
	}
	if temperature != "" {
		r.drawSpotRoomMetric(canvas, 184, "temperature", "TEMP", temperature, color.RGBA{255, 147, 66, 255})
	} else {
		r.drawSpotRoomMetric(canvas, 184, "humidity", "HUMIDITY", humidity, color.RGBA{83, 178, 255, 255})
	}
}

func spotRoomClimateHeading(room string) string {
	room = strings.TrimSpace(room)
	if room == "" {
		return "ROOM CLIMATE"
	}
	return strings.ToUpper(room) + " CLIMATE"
}

func (r *renderer) drawSpotRoomMetric(canvas *image.RGBA, iconX int, kind, label, value string, ink color.RGBA) {
	r.drawWeatherMetricGlyph(canvas, iconX, 391, 8, kind, ink)
	r.text(canvas, iconX+21, 383, label, 14, true, ink)
	r.text(canvas, iconX+21, 407, fitTextToWidth(value, r.faces.bold[22], 84), 22, true, color.RGBA{238, 243, 250, 255})
}

func (r *renderer) drawSpotTimer(canvas *image.RGBA, now time.Time, seconds float64, accent color.RGBA) {
	timer := r.state.Timer
	timerNow := time.UnixMilli(r.currentTaterUnixMS(now))
	ink := accent
	if timer.Ringing {
		ink = color.RGBA{255, 102, 96, 255}
	}
	r.centeredText(canvas, 240, 207, "TATER TIMER", 14, true, ink)
	circleOutline(canvas, 240, 282, 73, 5, color.RGBA{ink.R, ink.G, ink.B, 55})
	if timer.Ringing {
		arc(canvas, 240, 282, 73, -90+seconds*70, 110, 6, ink)
		r.centeredText(canvas, 240, 292, "TIME’S UP", 26, true, color.White)
	} else {
		arc(canvas, 240, 282, 73, -90, 360*timerProgress(timer, timerNow), 6, ink)
		remaining := formatDuration(timerRemaining(timer, timerNow))
		size := 36
		if len(remaining) > 6 {
			size = 26
		}
		r.centeredText(canvas, 240, 293, remaining, size, true, color.White)
	}
	label := firstNonEmpty(timer.Label, timer.Name)
	if label != "" {
		r.centeredText(canvas, 240, 372, fitTextToWidth(label, r.faces.regular[14], 240), 14, false, color.RGBA{180, 194, 211, 255})
	}
	button := spotTimerStopBounds()
	buttonColor := color.RGBA{ink.R, ink.G, ink.B, 50}
	if r.timerPressed {
		buttonColor.A = 120
	}
	roundedRect(canvas, button, 21, buttonColor)
	roundedRectOutline(canvas, button, 21, 2, ink)
	text := "CANCEL TIMER"
	if timer.Ringing {
		text = "STOP TIMER"
	}
	r.centeredText(canvas, 240, 413, text, 16, true, color.White)
}

func (r *renderer) drawSpotNotification(canvas *image.RGBA, now time.Time, seconds float64, accent color.RGBA) {
	item := r.state.Notification
	ink := accent
	if item.Priority == "critical" {
		ink = color.RGBA{255, 100, 100, 255}
	}
	title := firstNonEmpty(item.CameraName, item.Title, notificationFallbackTitle(item))
	if title != "" {
		title = notificationCategory(item) + " · " + strings.ToUpper(title)
	} else {
		title = notificationCategory(item)
	}
	titleSize := fittingTextSize(title, r.faces.bold, 14, 8, 265)
	r.centeredText(canvas, 240, 203, title, titleSize, true, ink)
	panel := image.Rect(107, 213, 373, 311)
	roundedRect(canvas, panel, 14, color.RGBA{25, 31, 43, 255})
	if r.notificationImage != nil {
		drawNotificationImage(canvas, panel, r.notificationImage, notificationImageFit(item, "cover"))
	} else {
		pulse := .5 + .5*math.Sin(seconds*2.2)
		circleOutline(canvas, 240, 261, int(20+pulse*8), 3, ink)
		circle(canvas, 240, 261, 7, ink)
	}
	lines := wrapTextToWidth(item.Description, r.faces.regular[16], 285)
	for index, value := range lines {
		if index >= 3 {
			break
		}
		r.centeredText(canvas, 240, 338+index*23, fitTextToWidth(value, r.faces.regular[16], 285), 16, false, color.RGBA{227, 235, 245, 255})
	}
	if item.ExpiresAtUnixMS > 0 {
		drawNotificationProgressBar(canvas, 176, 304, 414, 2,
			notificationProgress(item, r.currentTaterUnixMS(now)), ink)
	}
}

// Rook has no visible intercom button: a deliberate hold anywhere on the
// screen starts capture. A quick tap is inert except for the timer stop chip.
// The display ticker checks the deadline because a stationary finger does not
// necessarily generate any Move events on the protocol-A touchscreen.
func (r *renderer) handleSpotTouch(client *socketClient, event linuxinput.TouchEvent) {
	if r.cameraOpen && !r.notificationActive(time.Now()) {
		r.handleCameraTouch(client, event, 480, 480, true)
		return
	}
	if r.climateOpen && !r.notificationActive(time.Now()) {
		if event.Kind == linuxinput.Up {
			r.handleClimateTouch(client, event, 480, 480)
		}
		return
	}
	if r.menuOpen && !r.notificationActive(time.Now()) {
		switch event.Kind {
		case linuxinput.Down:
			r.spotTouchDown = true
			r.spotTouchAt = time.Now()
		case linuxinput.Up:
			if r.intercomPressed {
				r.intercomPressed = false
				client.send("intercom.stop")
			} else {
				r.handleActionMenuTouch(client, event, 480, 480, true)
			}
			r.spotTouchDown = false
			r.spotTouchAt = time.Time{}
		}
		return
	}
	if r.satChatVisible() {
		if event.Kind == linuxinput.Up {
			r.handleSpotSatChatTouch(client, event)
		}
		return
	}
	switch event.Kind {
	case linuxinput.Down:
		r.spotTouchDown = true
		r.spotTouchAt = time.Now()
		r.spotTimerDown = r.state.Timer != nil && r.state.Timer.Active && image.Pt(event.X, event.Y).In(spotTimerStopBounds())
		r.timerPressed = r.spotTimerDown
	case linuxinput.Up:
		wasIntercom := r.intercomPressed
		if wasIntercom {
			r.intercomPressed = false
			client.send("intercom.stop")
		} else if r.spotTimerDown && r.state.Timer != nil && r.state.Timer.Active && image.Pt(event.X, event.Y).In(spotTimerStopBounds()) {
			client.send("timer.stop")
		} else if r.notificationActive(time.Now()) {
			r.notificationDismissedID = notificationIdentity(r.state.Notification)
			r.menuOpen = false
			if r.cameraOpen {
				client.sendCamera("camera.close", "")
			}
			r.cameraOpen = false
			r.climateOpen = false
		} else if !r.spotTimerDown {
			r.menuOpen = true
		}
		r.spotTouchDown = false
		r.spotTimerDown = false
		r.timerPressed = false
	}
}

func (r *renderer) drawSpotSatChat(canvas *image.RGBA) {
	palette := r.menuPalette()
	r.centeredText(canvas, 240, 99, "SAT CHAT", 22, true, color.White)
	chat := r.state.SatChat
	if chat != nil && chat.Call != nil {
		call := chat.Call
		name := firstNonEmpty(call.TargetName, call.CallerName, "Satellite")
		if call.Target == chat.Self {
			name = firstNonEmpty(call.CallerName, "Satellite")
		}
		if strings.EqualFold(call.Mode, "video") {
			r.drawSpotSatChatVideo(canvas)
			button := spotSatChatEndBounds()
			drawMenuRoundedRect(canvas, button, button.Dy()/2, color.RGBA{139, 30, 39, 248})
			drawMenuRoundedOutline(canvas, button, button.Dy()/2, 2, color.RGBA{255, 80, 72, 240})
			r.centeredText(canvas, 240, button.Min.Y+button.Dy()*2/3, "END VIDEO", 16, true, color.White)
			return
		}
		name, nameSize := r.menuName(name, 25, 14, 320)
		r.centeredText(canvas, 240, 194, name, nameSize, true, color.White)
		r.centeredText(canvas, 240, 230, "CONNECTED · "+strings.ToUpper(firstNonEmpty(call.Mode, "audio")), 14, true, color.RGBA{77, 223, 158, 255})
		button := spotSatChatEndBounds()
		drawMenuRoundedRect(canvas, button, button.Dy()/2, color.RGBA{139, 30, 39, 248})
		drawMenuRoundedOutline(canvas, button, button.Dy()/2, 2, color.RGBA{255, 80, 72, 240})
		r.centeredText(canvas, 240, button.Min.Y+button.Dy()*2/3, "END CHAT", 16, true, color.White)
		return
	}
	participants := []show.SatChatParticipant{}
	if chat != nil {
		participants = chat.Participants
	}
	closeBounds := spotSatChatCloseBounds()
	drawMenuCloseButton(canvas, closeBounds)
	if len(participants) == 0 {
		r.centeredText(canvas, 240, 246, "NO SATELLITES AVAILABLE", 16, true, color.RGBA{164, 179, 198, 255})
		return
	}
	pageSize := r.satChatPageSize(480, 480)
	page := r.satChatPageFor(len(participants), pageSize)
	pageCount := satChatPageCount(len(participants), pageSize)
	start := page * pageSize
	end := min(len(participants), start+pageSize)
	r.centeredText(canvas, 240, 105, fmt.Sprintf("%d–%d OF %d", start+1, end, len(participants)), 10, true, color.RGBA{215, 224, 237, 245})
	for visibleIndex, participant := range participants[start:end] {
		top := 128 + visibleIndex*48
		row := image.Rect(90, top, 390, top+44)
		nameInk := color.RGBA{255, 255, 255, 255}
		statusInk := color.RGBA{49, 239, 116, 255}
		if participant.Busy {
			nameInk = color.RGBA{139, 148, 163, 255}
			statusInk = color.RGBA{255, 83, 67, 255}
		}
		drawMenuCard(canvas, row, row.Dy()/2, participant.Busy, palette.primary)
		avatar := image.Rect(96, top+4, 132, top+40)
		drawMenuIconMedallion(canvas, avatar, palette.primary)
		drawSatChatNetworkGlyph(canvas, 114, top+22, 8, palette.primary)
		circle(canvas, 143, top+14, 5, statusInk)
		name, nameSize := r.menuName(firstNonEmpty(participant.Name, participant.Room), 15, 9, 154)
		r.text(canvas, 156, top+23, name, nameSize, true, nameInk)
		label := "AUDIO ONLY"
		if participant.Video {
			label = "VIDEO + AUDIO"
		}
		if participant.Busy {
			label = "BUSY"
		}
		iconInk := palette.primary
		if participant.Busy {
			r.centeredText(canvas, 336, top+27, label, 9, true, color.RGBA{159, 169, 185, 255})
		} else {
			drawSatChatMicGlyph(canvas, 350, top+16, 8, iconInk)
			if participant.Video {
				drawSatChatVideoGlyph(canvas, 326, top+16, 8, iconInk)
			}
			r.centeredText(canvas, 338, top+38, label, 8, true, color.RGBA{191, 207, 226, 255})
		}
	}
	if pageCount > 1 {
		previous, next := spotSatChatPreviousBounds(), spotSatChatNextBounds()
		previousInk, nextInk := palette.secondary, palette.secondary
		if page == 0 {
			previousInk = color.RGBA{83, 93, 108, 150}
		}
		if page == pageCount-1 {
			nextInk = color.RGBA{83, 93, 108, 150}
		}
		drawMenuRoundedRect(canvas, previous, previous.Dy()/2, menuControlInk)
		drawMenuRoundedRect(canvas, next, next.Dy()/2, menuControlInk)
		drawMenuRoundedOutline(canvas, previous, previous.Dy()/2, 1, previousInk)
		drawMenuRoundedOutline(canvas, next, next.Dy()/2, 2, nextInk)
		r.centeredText(canvas, (previous.Min.X+previous.Max.X)/2, 420, "‹ PREV", 12, true, previousInk)
		r.centeredText(canvas, (next.Min.X+next.Max.X)/2, 420, "NEXT ›", 12, true, nextInk)
	}
}

func spotSatChatCloseBounds() image.Rectangle {
	return image.Rect(370, 66, 420, 116)
}

func spotSatChatPreviousBounds() image.Rectangle {
	return image.Rect(100, 390, 230, 434)
}

func spotSatChatNextBounds() image.Rectangle {
	return image.Rect(250, 390, 380, 434)
}

func spotSatChatEndBounds() image.Rectangle {
	return image.Rect(130, 374, 350, 426)
}

func (r *renderer) drawSpotSatChatVideo(canvas *image.RGBA) image.Rectangle {
	palette := r.menuPalette()
	videoBounds := r.satChatVideoBounds().Inset(-4)
	drawMenuRoundedRect(canvas, videoBounds, 24, color.RGBA{4, 13, 22, 255})
	if r.satChatVideoImage != nil {
		drawPreparedCover(canvas, videoBounds.Inset(4), r.satChatVideoImage)
	} else {
		r.centeredText(canvas, 240, 248, "WAITING FOR CAMERA", 14, true, color.RGBA{164, 179, 198, 255})
	}
	drawMenuRoundedOutline(canvas, videoBounds, 24, 2, menuAlpha(palette.primary, 230))
	r.drawSpotSatChatVideoOverlay(canvas)
	return videoBounds
}

func (r *renderer) drawSpotSatChatVideoFrame(canvas *image.RGBA) image.Rectangle {
	content := r.satChatVideoBounds()
	drawPreparedCover(canvas, content, r.satChatVideoImage)
	r.drawSpotSatChatVideoOverlay(canvas)
	return content
}

func (r *renderer) drawSpotSatChatVideoOverlay(canvas *image.RGBA) {
	call := r.state.SatChat.Call
	name := firstNonEmpty(call.TargetName, call.CallerName, "Satellite")
	if call.Target == r.state.SatChat.Self {
		name = firstNonEmpty(call.CallerName, "Satellite")
	}
	nameBox := image.Rect(118, 112, 362, 154)
	drawMenuRoundedRect(canvas, nameBox, nameBox.Dy()/2, color.RGBA{5, 17, 28, 232})
	drawMenuRoundedOutline(canvas, nameBox, nameBox.Dy()/2, 1, menuOutlineInk)
	circle(canvas, nameBox.Min.X+24, (nameBox.Min.Y+nameBox.Max.Y)/2, 5, color.RGBA{52, 240, 105, 255})
	r.centeredText(canvas, 248, 140, fitTextToWidth(name, r.faces.bold[16], 200), 16, true, color.White)
}

func (r *renderer) handleSpotSatChatTouch(client *socketClient, event linuxinput.TouchEvent) {
	chat := r.state.SatChat
	if chat != nil && chat.Call != nil {
		if image.Pt(event.X, event.Y).In(spotSatChatEndBounds()) {
			client.sendSatChat("sat_chat.end", "", "", chat.Call.ID)
		}
		return
	}
	point := image.Pt(event.X, event.Y)
	if point.In(spotSatChatCloseBounds()) {
		r.satChatOpen = false
		r.satChatPage = 0
		return
	}
	if chat == nil {
		return
	}
	pageSize := r.satChatPageSize(480, 480)
	page := r.satChatPageFor(len(chat.Participants), pageSize)
	pages := satChatPageCount(len(chat.Participants), pageSize)
	if pages > 1 && point.In(spotSatChatPreviousBounds()) {
		if page > 0 {
			r.satChatPage--
		}
		return
	}
	if pages > 1 && point.In(spotSatChatNextBounds()) {
		if page+1 < pages {
			r.satChatPage++
		}
		return
	}
	if event.Y < 128 || event.Y >= 368 {
		return
	}
	visibleIndex := (event.Y - 128) / 48
	index := page*pageSize + visibleIndex
	if visibleIndex >= 0 && visibleIndex < pageSize && index < len(chat.Participants) {
		participant := chat.Participants[index]
		if !participant.Busy {
			mode := "audio"
			if participant.Video {
				mode = "video"
			}
			client.sendSatChat("sat_chat.start", participant.ID, mode, "")
		}
	}
}

func (r *renderer) updateSpotHold(client *socketClient, now time.Time) {
	if r.intercomPressed && (!r.state.Connected || r.state.Muted) {
		r.intercomPressed = false
		r.spotTouchDown = false
		r.spotTimerDown = false
		client.send("intercom.stop")
	}
	if !r.spotTouchDown || r.intercomPressed || !r.state.Connected || r.state.Muted || r.spotTouchAt.IsZero() {
		return
	}
	if now.Sub(r.spotTouchAt) >= spotHoldDelay {
		r.intercomPressed = true
		r.timerPressed = false
		client.send("intercom.start")
	}
}

func (r *renderer) drawSpotConnecting(canvas *image.RGBA, seconds float64) {
	if r.connectingBase == nil || r.connectingBase.Rect.Dx() != 480 || r.connectingBase.Rect.Dy() != 480 {
		r.connectingBase = buildConnectingBase(480, 480)
	}
	draw.Draw(canvas, canvas.Bounds(), r.connectingBase, image.Point{}, draw.Src)
	r.centeredText(canvas, 240, 380, "Connecting to Tater", 18, true, color.RGBA{224, 228, 235, 255})
	for index := 0; index < 3; index++ {
		alpha := uint8(75)
		if int(seconds*2)%3 == index {
			alpha = 230
		}
		circle(canvas, 221+index*19, 405, 3, color.RGBA{255, 132, 48, alpha})
	}
}

func (r *renderer) drawSpotSetup(canvas *image.RGBA, seconds float64, accent color.RGBA) {
	r.spotBackground(canvas, accent)
	r.centeredText(canvas, 240, 82, "TATER SPOT SETUP", 18, true, accent)
	r.centeredText(canvas, 240, 132, "Connect to Tater", 30, true, color.White)
	if strings.HasPrefix(r.state.Room, "Tater-Setup-") {
		r.centeredText(canvas, 240, 204, "1 · JOIN THE WI-FI NETWORK", 16, true, color.RGBA{166, 182, 203, 255})
		r.centeredText(canvas, 240, 239, fitTextToWidth(r.state.Room, r.faces.bold[20], 325), 20, true, color.White)
		r.centeredText(canvas, 240, 291, "2 · OPEN ON YOUR PHONE", 16, true, color.RGBA{166, 182, 203, 255})
		r.centeredText(canvas, 240, 326, "192.168.4.1", 24, true, color.White)
		r.centeredText(canvas, 240, 376, "Pair in Tater · Satellites", 16, false, color.RGBA{211, 221, 235, 255})
	} else {
		r.centeredText(canvas, 240, 239, "Connect USB to your computer", 20, true, color.White)
		r.centeredText(canvas, 240, 282, "Open Tater · Add Satellite", 18, false, color.RGBA{211, 221, 235, 255})
		r.centeredText(canvas, 240, 326, "Waiting for provisioning", 16, true, color.RGBA{166, 182, 203, 255})
	}
	for index := 0; index < 3; index++ {
		alpha := uint8(70)
		if int(seconds*2)%3 == index {
			alpha = 240
		}
		circle(canvas, 221+index*19, 408, 3, color.RGBA{accent.R, accent.G, accent.B, alpha})
	}
}
