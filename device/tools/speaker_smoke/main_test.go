package main

import "testing"

func TestFadeBoundsAndPlateau(t *testing.T) {
	for _, tc := range []struct {
		frame int
		want  float64
	}{
		{frame: 0, want: 0},
		{frame: 5, want: 0.5},
		{frame: 10, want: 1},
		{frame: 89, want: 1},
		{frame: 99, want: 0},
	} {
		if got := fade(tc.frame, 100, 10); got != tc.want {
			t.Fatalf("fade(%d) = %v, want %v", tc.frame, got, tc.want)
		}
	}
}
