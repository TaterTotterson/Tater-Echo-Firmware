//go:build linux

package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log"
	"math"
	"strings"
	"time"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/displaycontrol"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/linuxscreen"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/screenambient"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

const (
	brightnessStepInterval = 500 * time.Millisecond
	sensorRetryInterval    = 30 * time.Second
)

type displayPowerController struct {
	target       string
	sensor       screenambient.Reader
	sensorName   string
	sensorError  string
	lastProbe    time.Time
	lastStep     time.Time
	lux          float64
	haveLux      bool
	sampleErrors int
	current      float64
	desired      float64
	written      int
	night        bool
}

func newDisplayPowerController(target string) *displayPowerController {
	level, err := linuxscreen.Backlight()
	if err != nil {
		level = 90 // the former fixed startup level, about 35 percent
	}
	controller := &displayPowerController{
		target: strings.ToLower(strings.TrimSpace(target)), current: float64(level), desired: float64(level), written: level,
	}
	controller.probe(time.Now())
	return controller
}

func (d *displayPowerController) Close() {
	if d.sensor != nil {
		_ = d.sensor.Close()
		d.sensor = nil
	}
}

func (d *displayPowerController) probe(now time.Time) {
	if d.sensor != nil || (!d.lastProbe.IsZero() && now.Sub(d.lastProbe) < sensorRetryInterval) {
		return
	}
	d.lastProbe = now
	sensor, err := screenambient.Open(d.target)
	if err != nil {
		d.sensorError = err.Error()
		return
	}
	d.sensor, d.sensorName, d.sensorError = sensor, sensor.Name(), ""
	d.sampleErrors = 0
	log.Printf("[display] ambient light sensor using %s", d.sensorName)
}

func displayConfig(state show.Snapshot) displaycontrol.Config {
	return displaycontrol.Config{
		Brightness:      state.ScreenBrightness,
		Auto:            state.ScreenAutoBrightness,
		AutoMinimum:     state.ScreenAutoBrightnessMinimum,
		NightEnabled:    state.ScreenNightMode,
		NightBrightness: state.ScreenNightBrightness,
		NightStart:      state.ScreenNightStart,
		NightEnd:        state.ScreenNightEnd,
		NightDisplay:    state.ScreenNightDisplay,
	}
}

func (d *displayPowerController) Update(state show.Snapshot, local, now time.Time) {
	if !d.lastStep.IsZero() && now.Sub(d.lastStep) < brightnessStepInterval {
		return
	}
	d.lastStep = now
	d.probe(now)
	if d.sensor != nil {
		lux, ok, err := d.sensor.Sample()
		switch {
		case err != nil:
			d.sampleErrors++
			d.sensorError = err.Error()
			if d.sampleErrors >= 5 {
				log.Printf("[display] ambient light sensor %s stopped: %v; falling back to manual brightness", d.sensorName, err)
				_ = d.sensor.Close()
				d.sensor, d.sensorName = nil, ""
				d.haveLux = false
				d.lastProbe = now
			}
		default:
			d.sampleErrors, d.sensorError = 0, ""
			if !ok {
				break
			}
			if d.haveLux {
				d.lux = .75*d.lux + .25*lux
			} else {
				d.lux, d.haveLux = lux, true
			}
		}
	}
	config := displayConfig(state)
	d.night = config.NightEnabled && displaycontrol.NightActive(local, config.NightStart, config.NightEnd)
	d.desired = displaycontrol.TargetLevel(config, d.target, d.lux, d.haveLux, d.night)
	d.current = displaycontrol.SmoothStep(d.current, d.desired)
	level := int(math.Round(d.current))
	if level == d.written {
		return
	}
	if err := linuxscreen.SetBacklight(level); err != nil {
		log.Printf("[display] setting backlight to %d failed: %v", level, err)
		return
	}
	d.written = level
}

func (d *displayPowerController) ApplyDiagnostics(diagnostics *show.ScreenDiagnostics, state show.Snapshot) {
	if diagnostics == nil {
		return
	}
	diagnostics.BacklightPercent = int(math.Round(d.current * 100 / displaycontrol.BacklightMax))
	diagnostics.BacklightTargetPercent = int(math.Round(d.desired * 100 / displaycontrol.BacklightMax))
	diagnostics.AutoBrightness = state.ScreenAutoBrightness
	diagnostics.NightMode = d.night
	diagnostics.NightDisplay = state.ScreenNightDisplay
	diagnostics.AmbientLightAvailable = d.sensor != nil
	diagnostics.AmbientLightSensor = d.sensorName
	diagnostics.AmbientLightError = d.sensorError
	if d.haveLux {
		lux := math.Round(d.lux*10) / 10
		diagnostics.AmbientLux = &lux
	}
}

func (r *renderer) nightClockVisible(now time.Time) bool {
	if !r.nightModeActive || strings.ToLower(strings.TrimSpace(r.state.ScreenNightDisplay)) != "red_clock" {
		return false
	}
	if !r.state.Connected || r.state.Phase != "idle" || r.menuOpen || r.cameraOpen || r.satChatOpen || r.climateOpen {
		return false
	}
	if r.notificationActive(now) || (r.state.Timer != nil && r.state.Timer.Active) {
		return false
	}
	if r.state.Media != nil && r.state.Media.Active {
		return false
	}
	return r.state.SatChat == nil || r.state.SatChat.Call == nil
}

func (r *renderer) renderNightClock(canvas *image.RGBA, now time.Time) {
	local := r.localTime(now)
	key := fmt.Sprintf("%dx%d|%s", canvas.Rect.Dx(), canvas.Rect.Dy(), r.clockKey(now))
	if r.nightClockCanvas == canvas && r.nightClockKey == key {
		r.presentRegions = nil
		return
	}
	draw.Draw(canvas, canvas.Rect, &image.Uniform{C: color.Black}, image.Point{}, draw.Src)
	width, height := canvas.Rect.Dx(), canvas.Rect.Dy()
	timeSize := 96
	if width == height {
		timeSize = 82
	} else if width < 1100 {
		timeSize = 82
	}
	red := color.RGBA{174, 12, 12, 255}
	dimRed := color.RGBA{104, 8, 8, 255}
	r.centeredClock(canvas, width/2, height/2+timeSize/3, local, timeSize, red)
	r.centeredText(canvas, width/2, height/2+timeSize/3+42, local.Format("Monday · January 2"), 18, false, dimRed)
	r.nightClockCanvas, r.nightClockKey = canvas, key
	r.presentRegions = []image.Rectangle{canvas.Rect}
	r.transientPresent = false
}
