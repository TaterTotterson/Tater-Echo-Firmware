package micgain

import (
	"math"
	"testing"
)

func TestGainStagingIsTargetScoped(t *testing.T) {
	for _, target := range []string{"biscuit", "radar", " RADAR "} {
		for _, oldDB := range []int{0, 28, 36, 40, 42} {
			pcm, levels := Factors(target, oldDB)
			if HardwareDB(target) != 40 || SoftwareDB(target, oldDB) != 0 || pcm != 1 || levels != 100 {
				t.Fatalf("%q/%ddB: hardware=%d PCM=%v levels=%v", target, oldDB, HardwareDB(target), pcm, levels)
			}
		}
	}
	for _, target := range []string{"checkers", "cronos", "crown", "athena", "rook", "", "unknown"} {
		for _, db := range []int{0, 28, 40, 42} {
			pcm, levels := Factors(target, db)
			want := math.Pow(10, float64(db)/20)
			if HardwareDB(target) != 0 || SoftwareDB(target, db) != db || pcm != want || levels != want {
				t.Fatalf("%q/%ddB changed: PCM=%v levels=%v want=%v", target, db, pcm, levels, want)
			}
		}
	}
}
