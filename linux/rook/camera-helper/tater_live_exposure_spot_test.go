//go:build spot

package camera

import "testing"

func TestRookLiveExposureCapsIntegrationWithoutChangingStillLimits(t *testing.T) {
	for mean := 0; mean <= 255; mean++ {
		for _, live := range []bool{false, true} {
			shutter, gain := taterRookExposure(aeMaxShut, aeMaxGain, mean, live)
			limit := aeMaxShut
			if live {
				limit = aeFrame
			}
			if shutter < aeMinShut || shutter > limit || gain < aeMinGain || gain > aeMaxGain {
				t.Fatalf("mean=%d live=%t: shutter=%d gain=%d", mean, live, shutter, gain)
			}
		}
	}
	still, _ := taterRookExposure(aeMaxShut, aeMaxGain, 1, false)
	live, _ := taterRookExposure(aeMaxShut, aeMaxGain, 1, true)
	if still != aeMaxShut || live != aeFrame {
		t.Fatal("still/live integration limits were not independent", still, live)
	}
}
