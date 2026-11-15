package earlykey

import (
	"errors"
	"testing"
	"time"
)

func TestHeld(t *testing.T) {
	missing := errors.New("input unavailable")
	for _, tc := range []struct {
		name string
		read func(time.Duration) (bool, error)
		want bool
		err  bool
		wait time.Duration
	}{
		{"idle", func(time.Duration) (bool, error) { return false, nil }, false, false, 0},
		{"held", func(time.Duration) (bool, error) { return true, nil }, true, false, Hold},
		{"released", func(d time.Duration) (bool, error) { return d < time.Second, nil }, false, false, time.Second},
		{"driver delayed", func(d time.Duration) (bool, error) {
			if d < time.Second {
				return false, missing
			}
			return true, nil
		}, true, false, Hold + time.Second},
		{"driver missing", func(time.Duration) (bool, error) { return false, missing }, false, true, Settle},
		{"driver lost", func(d time.Duration) (bool, error) {
			if d >= time.Second {
				return false, missing
			}
			return true, nil
		}, false, true, time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Unix(0, 0)
			now := start
			got, err := Held(func() (bool, error) { return tc.read(now.Sub(start)) },
				func() time.Time { return now }, func(d time.Duration) { now = now.Add(d) })
			if got != tc.want || (err != nil) != tc.err || now.Sub(start) != tc.wait {
				t.Fatalf("held=%v err=%v elapsed=%v", got, err, now.Sub(start))
			}
		})
	}
}
