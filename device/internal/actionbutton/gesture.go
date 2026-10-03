// Package actionbutton implements the Tater action-button gestures shared by
// the Echo hardware path.  It deliberately owns timing locally: controller
// round trips are far too variable to distinguish a short click from a hold.
package actionbutton

import (
	"sync"
	"time"
)

const (
	DefaultIntercomHold = 600 * time.Millisecond
	DefaultShortClick   = 500 * time.Millisecond
	DefaultClickWindow  = 3 * time.Second
	DefaultArmWindow    = 5 * time.Second
	DefaultSetupHold    = 5 * time.Second
	DefaultClickTarget  = 5
	DefaultSteps        = 12
)

// Timer is the small part of time.Timer the gesture engine needs.
type Timer interface {
	Stop() bool
}

// Clock makes all gesture boundaries deterministic in tests.
type Clock interface {
	Now() time.Time
	AfterFunc(time.Duration, func()) Timer
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) AfterFunc(d time.Duration, fn func()) Timer {
	return time.AfterFunc(d, fn)
}

// Config contains the ESP-native gesture timings. Zero values select the
// production defaults above.
type Config struct {
	IntercomHold time.Duration
	ShortClick   time.Duration
	ClickWindow  time.Duration
	ArmWindow    time.Duration
	SetupHold    time.Duration
	ClickTarget  int
	Steps        int
	Clock        Clock
}

func (c Config) defaults() Config {
	if c.IntercomHold <= 0 {
		c.IntercomHold = DefaultIntercomHold
	}
	if c.ShortClick <= 0 {
		c.ShortClick = DefaultShortClick
	}
	if c.ClickWindow <= 0 {
		c.ClickWindow = DefaultClickWindow
	}
	if c.ArmWindow <= 0 {
		c.ArmWindow = DefaultArmWindow
	}
	if c.SetupHold <= 0 {
		c.SetupHold = DefaultSetupHold
	}
	if c.ClickTarget <= 0 {
		c.ClickTarget = DefaultClickTarget
	}
	if c.Steps <= 0 {
		c.Steps = DefaultSteps
	}
	if c.Clock == nil {
		c.Clock = realClock{}
	}
	return c
}

// Callbacks connect the pure gesture engine to audio, LEDs, and provisioning.
// StartIntercom reports whether a voice turn really started (for example it
// returns false while muted or disconnected), so release never sends a stray
// voice.stop.
type Callbacks struct {
	StartIntercom func() bool
	StopIntercom  func()
	ShowClicks    func(count, target int)
	ShowCountdown func(remaining, total int)
	ClearFeedback func()
	SetupComplete func()
}

// Gesture recognizes:
//   - hold for 600 ms: start intercom; release: finish and process it
//   - five short clicks, then hold the sixth press for five seconds: setup
//
// Press and Release are safe to call from the evdev reader while timer
// callbacks run concurrently.
type Gesture struct {
	mu        sync.Mutex
	cfg       Config
	callbacks Callbacks

	down          bool
	pressedAt     time.Time
	generation    uint64
	clicks        int
	clickDeadline time.Time
	armDeadline   time.Time
	setupHold     bool
	intercom      bool
	holdTimer     Timer
	stepTimer     Timer
	expiryTimer   Timer
}

func New(cfg Config, callbacks Callbacks) *Gesture {
	return &Gesture{cfg: cfg.defaults(), callbacks: callbacks}
}

// Press records a physical action-button down transition.
func (g *Gesture) Press() {
	now := g.cfg.Clock.Now()
	g.mu.Lock()
	if g.down {
		g.mu.Unlock()
		return
	}
	g.expireLocked(now)
	g.down = true
	g.pressedAt = now
	g.generation++
	gen := g.generation
	g.intercom = false
	g.setupHold = g.clicks >= g.cfg.ClickTarget && now.Before(g.armDeadline)
	setup := g.setupHold
	if setup {
		g.stopTimerLocked(&g.expiryTimer)
		g.holdTimer = g.cfg.Clock.AfterFunc(g.cfg.SetupHold, func() { g.completeSetup(gen) })
		g.scheduleCountdownLocked(gen, g.cfg.Steps-1)
	} else {
		g.holdTimer = g.cfg.Clock.AfterFunc(g.cfg.IntercomHold, func() { g.startIntercom(gen) })
	}
	g.mu.Unlock()

	if setup && g.callbacks.ShowCountdown != nil {
		g.callbacks.ShowCountdown(g.cfg.Steps, g.cfg.Steps)
	}
}

// Release records a physical action-button up transition.
func (g *Gesture) Release() {
	now := g.cfg.Clock.Now()
	g.mu.Lock()
	if !g.down {
		g.mu.Unlock()
		return
	}
	duration := now.Sub(g.pressedAt)
	g.down = false
	g.generation++
	g.stopTimerLocked(&g.holdTimer)
	g.stopTimerLocked(&g.stepTimer)

	wasSetup := g.setupHold
	wasIntercom := g.intercom
	g.setupHold = false
	g.intercom = false

	clickCount := 0
	if wasSetup || wasIntercom {
		g.resetClicksLocked()
	} else if duration > 0 && duration <= g.cfg.ShortClick {
		g.expireLocked(now)
		g.clicks++
		g.clickDeadline = now.Add(g.cfg.ClickWindow)
		if g.clicks >= g.cfg.ClickTarget {
			g.clicks = g.cfg.ClickTarget
			g.armDeadline = now.Add(g.cfg.ArmWindow)
		}
		clickCount = g.clicks
		g.scheduleExpiryLocked()
	} else {
		g.resetClicksLocked()
	}
	g.mu.Unlock()

	switch {
	case wasIntercom:
		if g.callbacks.StopIntercom != nil {
			g.callbacks.StopIntercom()
		}
	case wasSetup:
		if g.callbacks.ClearFeedback != nil {
			g.callbacks.ClearFeedback()
		}
	case clickCount > 0:
		if g.callbacks.ShowClicks != nil {
			g.callbacks.ShowClicks(clickCount, g.cfg.ClickTarget)
		}
	}
}

func (g *Gesture) startIntercom(generation uint64) {
	g.mu.Lock()
	if !g.down || g.setupHold || generation != g.generation {
		g.mu.Unlock()
		return
	}
	// Keep the transition serialized with Release. StartIntercom only queues a
	// local WebSocket message; holding this mutex for that short operation
	// guarantees a release racing the threshold always observes whether the
	// turn actually opened and therefore cannot strand a capture.
	g.intercom = g.callbacks.StartIntercom != nil && g.callbacks.StartIntercom()
	g.mu.Unlock()
}

func (g *Gesture) completeSetup(generation uint64) {
	g.mu.Lock()
	if !g.down || !g.setupHold || generation != g.generation {
		g.mu.Unlock()
		return
	}
	g.down = false // consume the eventual physical release
	g.setupHold = false
	g.generation++
	g.stopTimerLocked(&g.stepTimer)
	g.resetClicksLocked()
	g.mu.Unlock()

	if g.callbacks.ShowCountdown != nil {
		g.callbacks.ShowCountdown(0, g.cfg.Steps)
	}
	if g.callbacks.SetupComplete != nil {
		g.callbacks.SetupComplete()
	}
}

func (g *Gesture) scheduleCountdownLocked(generation uint64, remaining int) {
	if remaining <= 0 {
		return
	}
	elapsedSteps := g.cfg.Steps - remaining
	due := time.Duration(elapsedSteps) * g.cfg.SetupHold / time.Duration(g.cfg.Steps)
	previous := time.Duration(elapsedSteps-1) * g.cfg.SetupHold / time.Duration(g.cfg.Steps)
	delay := due - previous
	g.stepTimer = g.cfg.Clock.AfterFunc(delay, func() {
		g.mu.Lock()
		active := g.down && g.setupHold && generation == g.generation
		if active {
			g.scheduleCountdownLocked(generation, remaining-1)
		}
		g.mu.Unlock()
		if active && g.callbacks.ShowCountdown != nil {
			g.callbacks.ShowCountdown(remaining, g.cfg.Steps)
		}
	})
}

func (g *Gesture) scheduleExpiryLocked() {
	g.stopTimerLocked(&g.expiryTimer)
	now := g.cfg.Clock.Now()
	deadline := g.clickDeadline
	if g.clicks >= g.cfg.ClickTarget && g.armDeadline.After(deadline) {
		deadline = g.armDeadline
	}
	delay := deadline.Sub(now)
	if delay < 0 {
		delay = 0
	}
	gen := g.generation
	g.expiryTimer = g.cfg.Clock.AfterFunc(delay, func() {
		g.mu.Lock()
		if gen != g.generation || g.down {
			g.mu.Unlock()
			return
		}
		g.expireLocked(g.cfg.Clock.Now())
		cleared := g.clicks == 0
		g.mu.Unlock()
		if cleared && g.callbacks.ClearFeedback != nil {
			g.callbacks.ClearFeedback()
		}
	})
}

func (g *Gesture) expireLocked(now time.Time) {
	if g.clicks == 0 {
		return
	}
	armed := g.clicks >= g.cfg.ClickTarget && now.Before(g.armDeadline)
	if !armed && !now.Before(g.clickDeadline) {
		g.resetClicksLocked()
	}
}

func (g *Gesture) resetClicksLocked() {
	g.clicks = 0
	g.clickDeadline = time.Time{}
	g.armDeadline = time.Time{}
	g.stopTimerLocked(&g.expiryTimer)
}

func (g *Gesture) stopTimerLocked(timer *Timer) {
	if *timer != nil {
		(*timer).Stop()
		*timer = nil
	}
}
