//go:build !linux

package linuxinput

import (
	"context"
	"errors"
)

type EventKind uint8

const (
	Down EventKind = iota + 1
	Move
	Up
)

type TouchEvent struct {
	Kind EventKind
	X    int
	Y    int
}

type Device struct{}

func Open() (*Device, error)     { return nil, errors.New("Linux touch input is unavailable") }
func (d *Device) String() string { return "unavailable" }
func (d *Device) Close() error   { return nil }
func (d *Device) Run(context.Context, chan<- TouchEvent) error {
	return errors.New("Linux touch input is unavailable")
}
