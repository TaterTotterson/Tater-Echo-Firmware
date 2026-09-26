package buttons

import (
	"context"
	"errors"
	"github.com/TaterTotterson/Tater-Echo-Firmware/pkg/buttons"
	evdev "github.com/gvalkov/golang-evdev"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const dotButton = "/dev/input/event1"
const volumeButton = "/dev/input/event2"

// VolumeCallback is called on volume button release with direction "up" or "down".
type VolumeCallback func(direction string)

// VolumeEventCallback receives both edges and the release hold duration. It
// exists for deliberate recovery gestures; normal volume handling remains on
// VolumeCallback so existing targets do not change behavior.
type VolumeEventCallback func(direction string, down bool, heldMs int64)

// MuteCallback is called on mute button release.
type MuteCallback func()

type EvDevController struct {
	volumeCallback      func(direction string)
	volumeEventCallback func(direction string, down bool, heldMs int64)
	muteCallback        func()
	dotPath             string
	volumePath          string
	actionCode          uint16
	muteCode            uint16
	stopAceButton       bool
}

// SetVolumeCallback registers a function to be called on volume button events.
// Must be called before SubscribeToButton.
func (e *EvDevController) SetVolumeCallback(cb func(direction string)) {
	e.volumeCallback = cb
}

func (e *EvDevController) SetVolumeEventCallback(cb VolumeEventCallback) {
	e.volumeEventCallback = cb
}

// SetMuteCallback registers a function to be called on mute button events.
// Must be called before SubscribeToButton.
func (e *EvDevController) SetMuteCallback(cb func()) {
	e.muteCallback = cb
}

// Init the button listeners
// Kills alexa's native button functions
func (e *EvDevController) Init() error {
	if !e.stopAceButton {
		return nil
	}
	cmd := exec.Command("stop", "acebutton")
	return cmd.Run()
}

func (e *EvDevController) SubscribeToButton(callback buttons.ButtonClickCallback) (*buttons.EventSubscription, error) {
	if callback == nil {
		return nil, errors.New("callback can't be nil")
	}

	dotDevice, err := evdev.Open(e.dotPath)
	if err != nil {
		return nil, err
	}
	var volDevice *evdev.InputDevice
	if e.volumePath != e.dotPath {
		volDevice, err = evdev.Open(e.volumePath)
		if err != nil {
			dotDevice.Release()
			return nil, err
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	eventSub := buttons.NewEventSubscription(cancel)

	readBtn := func(btnDevice *evdev.InputDevice) {
		defer btnDevice.Release()

		// When each click type was pressed, so a release can report how long
		// it was held. Keyed by click type because the dot device carries the
		// mute button too, and interleaving the two must not attribute one
		// button's hold to the other.
		downAt := map[buttons.ClickType]time.Time{}
		downState := map[uint16]bool{}

		for {
			if ctx.Err() != nil {
				return
			}

			inputEvent, err := btnDevice.ReadOne()
			if err != nil {
				return
			}

			// Only key events. Every key press is followed immediately by
			// an EV_SYN separator whose Code and Value are both 0 — and
			// without this filter that SYN fell through to the Code==0
			// branch, took the previous click type, computed Value==1 as
			// FALSE, and fired a "release" microseconds after the press.
			//
			// So the button has always acted on the SYN rather than on the
			// real release, which is why it felt instant and why the actual
			// release (a genuine transition to 0) was then swallowed as a
			// no-change. Invisible until something needed to know how long
			// the button was held: heldMs came out at ~0 every time.
			if inputEvent.Type != evdev.EV_KEY {
				continue
			}

			rawCode := uint16(inputEvent.Code)
			clickType := buttons.ClickType(rawCode)
			if inputEvent.Value != 0 && inputEvent.Value != 1 {
				continue // key-repeat; not a new press or release
			}
			down := inputEvent.Value == 1
			if downState[rawCode] == down {
				continue
			}
			downState[rawCode] = down

			// Volume events can share an evdev node with the mute button on
			// Checkers, so dispatch by key code rather than by which file was
			// opened.
			if clickType == buttons.VolumeUpClick || clickType == buttons.VolumeDownClick {
				direction := "down"
				if clickType == buttons.VolumeUpClick {
					direction = "up"
				}
				var heldMs int64
				if down {
					downAt[clickType] = time.Now()
				} else if pressed, ok := downAt[clickType]; ok {
					heldMs = time.Since(pressed).Milliseconds()
					delete(downAt, clickType)
				}
				if e.volumeEventCallback != nil {
					e.volumeEventCallback(direction, down, heldMs)
				}
				if !down && e.volumeCallback != nil {
					e.volumeCallback(direction)
				}
				continue
			}

			if rawCode == e.muteCode {
				if down {
					downAt[buttons.MuteClick] = time.Now()
				}
				if !down {
					delete(downAt, buttons.MuteClick)
				}
				if !down && e.muteCallback != nil {
					e.muteCallback()
				}
				continue
			}

			// Boards without a physical action button use the screen intercom
			// command. Ignore unrelated kernel keys rather than presenting one
			// as an action press.
			if e.actionCode == 0 || rawCode != e.actionCode {
				continue
			}
			clickType = buttons.DotClick

			var heldMs int64
			if down {
				downAt[clickType] = time.Now()
			} else if t, ok := downAt[clickType]; ok {
				heldMs = time.Since(t).Milliseconds()
				delete(downAt, clickType)
			}

			callback(buttons.ButtonClickEvent{
				Button:    e.GetDotButton(),
				ClickType: clickType,
				Down:      down,
				HeldMs:    heldMs,
			})
		}
	}

	go readBtn(dotDevice)
	if volDevice != nil {
		go readBtn(volDevice)
	}

	return eventSub, nil
}

func (e *EvDevController) GetVolumeButton() buttons.Button {
	return buttons.Button{
		Type: buttons.VolumeButton,
	}
}

func (e *EvDevController) GetDotButton() buttons.Button {
	return buttons.Button{
		Type: buttons.DotButton,
	}
}

func NewButtonController() (*EvDevController, error) {
	return NewButtonControllerForTarget("biscuit")
}

var eventHandler = regexp.MustCompile(`\b(event[0-9]+)\b`)

func inputPathByName(data []byte, name string) string {
	for _, block := range strings.Split(string(data), "\n\n") {
		if !strings.Contains(block, `N: Name="`+name+`"`) {
			continue
		}
		for _, line := range strings.Split(block, "\n") {
			if !strings.HasPrefix(line, "H: Handlers=") {
				continue
			}
			if match := eventHandler.FindStringSubmatch(line); len(match) == 2 {
				return "/dev/input/" + match[1]
			}
		}
	}
	return ""
}

// NewButtonControllerForTarget resolves Checkers' shared gpio-keys node by
// device name. Event numbers are enumeration order and are not a board ABI.
func NewButtonControllerForTarget(target string) (*EvDevController, error) {
	controller := &EvDevController{
		dotPath: dotButton, volumePath: volumeButton,
		actionCode: uint16(buttons.DotClick), muteCode: uint16(buttons.MuteClick),
		stopAceButton: true,
	}
	if strings.EqualFold(strings.TrimSpace(target), "checkers") {
		data, err := os.ReadFile("/proc/bus/input/devices")
		if err != nil {
			return nil, err
		}
		mutePath := inputPathByName(data, "gating")
		volumePath := inputPathByName(data, "gpio-keys")
		if mutePath == "" || volumePath == "" {
			return nil, errors.New("checkers gating or gpio-keys input device not found")
		}
		controller.dotPath, controller.volumePath = mutePath, volumePath
		controller.actionCode = 0 // Checkers uses the touchscreen for intercom.
		// The mic/camera-off push button reports KEY_POWER from the dedicated
		// `gating` node. Confirmed twice in the guided hardware trace; code 9
		// in the GPIO boot log is not this control.
		controller.muteCode = 116
		controller.stopAceButton = false
	}
	if err := controller.Init(); err != nil {
		return nil, err
	}
	return controller, nil
}
