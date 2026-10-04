//go:build linux

package main

import (
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
	r.spotBackground(canvas, accent)
	if speaking {
		r.drawSpotReplyRim(canvas, accent)
	}
	local := r.localTime(now)
	r.centeredText(canvas, 240, 111, local.Format("3:04"), 82, false, color.White)
	r.centeredText(canvas, 240, 138, strings.ToUpper(local.Format("Mon · Jan 2")), 16, true, color.RGBA{167, 180, 199, 255})
	line(canvas, 125, 158, 355, 158, 1, color.RGBA{226, 154, 90, 48})

	switch {
	case r.notificationActive(now):
		r.drawSpotNotification(canvas, now, seconds, accent)
	case r.state.Timer != nil && r.state.Timer.Active:
		r.drawSpotTimer(canvas, now, seconds, accent)
	case r.state.Weather != nil:
		r.drawSpotWeather(canvas, seconds, accent)
	default:
		headline, detail := r.status(local)
		r.centeredText(canvas, 240, 282, fitTextToWidth(headline, r.faces.bold[24], 290), 24, true, color.White)
		r.centeredText(canvas, 240, 313, fitTextToWidth(detail, r.faces.regular[14], 265), 14, false, color.RGBA{164, 179, 198, 255})
	}
	// The round Spot no longer uses either the center or corner voice orb.
	// Keep a small stage label where it does not compete with the weather art.
	if (r.state.Timer == nil || !r.state.Timer.Active) && !r.notificationActive(now) {
		stage, stageColor := "", accent
		switch {
		case r.intercomPressed || r.state.Phase == "intercom":
			stage = "INTERCOM"
		case r.state.Phase == "listening":
			stage = "LISTENING"
		case r.state.Phase == "thinking":
			stage = "THINKING"
		case r.state.Phase == "tool_call":
			stage = "WORKING"
		}
		if stage != "" {
			r.centeredText(canvas, 240, 424, stage, 14, true, stageColor)
		}
	}
	status := connectedStatus(r.state.AssistantName)
	statusColor := color.RGBA{119, 218, 170, 255}
	if !r.state.Connected {
		status, statusColor = "TATER OFFLINE", color.RGBA{255, 166, 98, 255}
	} else if r.state.Muted {
		status, statusColor = "MIC MUTED", color.RGBA{255, 152, 155, 255}
	}
	r.centeredText(canvas, 240, 449, fitTextToWidth(status, r.faces.bold[14], 214), 14, true, statusColor)
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
	target := math.Pow(math.Min(1, math.Max(0, r.state.AudioLevel)/.22), .65)
	response := .30
	if target > r.spotReplyGlow {
		response = .60
	}
	r.spotReplyGlow += (target - r.spotReplyGlow) * response
	level := r.spotReplyGlow

	cx, cy := canvas.Rect.Dx()/2, canvas.Rect.Dy()/2
	outer := min(cx, cy) - 2
	inner := outer - 24
	core := outer - 5
	middle := outer - 12
	outerSquared, innerSquared := outer*outer, inner*inner
	coreSquared, middleSquared := core*core, middle*middle
	ink := mix(replyColor, color.RGBA{255, 255, 255, 255}, .04+.10*level)
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

func (r *renderer) drawSpotWeather(canvas *image.RGBA, seconds float64, accent color.RGBA) {
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
		r.drawWeatherIcon(canvas, 240, iconY, iconRadius, w.ConditionKind, seconds, weatherAccent)
		return
	}

	r.centeredText(canvas, 240, 177, fitTextToWidth(heading, r.faces.bold[14], 250), 14, true, headingColor)
	r.drawWeatherIcon(canvas, 240, iconY, iconRadius, w.ConditionKind, seconds, weatherAccent)

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
	title := firstNonEmpty(item.CameraName, item.Title)
	if title != "" {
		title = "AWARENESS · " + strings.ToUpper(title)
	} else {
		title = "AWARENESS"
	}
	r.centeredText(canvas, 240, 203, fitTextToWidth(title, r.faces.bold[14], 265), 14, true, ink)
	panel := image.Rect(107, 213, 373, 311)
	roundedRect(canvas, panel, 14, color.RGBA{25, 31, 43, 255})
	if r.notificationImage != nil {
		drawCover(canvas, panel, r.notificationImage)
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
		remaining := float64(item.ExpiresAtUnixMS-r.currentTaterUnixMS(now)) / 90000
		progress := math.Max(0, math.Min(1, remaining))
		line(canvas, 176, 414, 176+int(128*progress), 414, 2, ink)
	}
}

// Rook has no visible intercom button: a deliberate hold anywhere on the
// screen starts capture. A quick tap is inert except for the timer stop chip.
// The display ticker checks the deadline because a stationary finger does not
// necessarily generate any Move events on the protocol-A touchscreen.
func (r *renderer) handleSpotTouch(client *socketClient, event linuxinput.TouchEvent) {
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
		}
		r.spotTouchDown = false
		r.spotTimerDown = false
		r.timerPressed = false
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
