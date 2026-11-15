//go:build !dot && !spot && !panel

package camera

import (
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"sync"
	"sync/atomic"
)

var taterLiveVideoUsers atomic.Int32

// AcquireTaterLiveVideo requests motion-priority exposure only while a live
// stream holds the qualified Checkers/Cronos sensor. Normal exposure policy
// resumes when the last live lease ends. Native still resolution is unchanged.
func (c *Camera) AcquireTaterLiveVideo() (func(), error) {
	taterLiveVideoUsers.Add(1)
	release, err := c.Acquire()
	if err != nil {
		taterLiveVideoUsers.Add(-1)
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { taterLiveVideoUsers.Add(-1); release() }) }, nil
}

// Retain the existing encoder contract; exposure policy does not select a codec.
func (c *Camera) AcquireTaterH264() (func(), error) {
	return c.AcquireTaterLiveVideo()
}

func taterHoldExposure(held, modeChanged bool) bool {
	// A stable scene must not suppress switching into/out of motion exposure.
	return held && !modeChanged
}

func taterVideoExposureLimits() (midGain, maxShutter int, live bool) {
	live = (layout.Checkers() || layout.Crown() || layout.Board == "cronos") && taterLiveVideoUsers.Load() > 0
	if live {
		return aeMaxGain, aeFrame, true
	}
	return aeMidGain, aeMaxShut, false
}
