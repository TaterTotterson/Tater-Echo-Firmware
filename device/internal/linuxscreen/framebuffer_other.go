//go:build !linux

package linuxscreen

import (
	"errors"
	"image"
)

type Device struct{}

func Open() (*Device, error)          { return nil, errors.New("Linux framebuffer is unavailable") }
func (d *Device) Canvas() *image.RGBA { return nil }
func (d *Device) Size() (int, int)    { return 0, 0 }
func (d *Device) String() string      { return "unavailable" }
func (d *Device) Present() error      { return errors.New("Linux framebuffer is unavailable") }
func (d *Device) Close() error        { return nil }
func SetBacklight(int) error          { return errors.New("Linux backlight is unavailable") }
func Backlight() (int, error)         { return 0, errors.New("Linux backlight is unavailable") }
