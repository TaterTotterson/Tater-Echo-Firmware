package main

import "testing"

func TestParseMuteStateFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name, data string
		want       bool
		wantError  bool
	}{
		{"unmuted", `{"muted":false}`, false, false},
		{"muted", `{"muted":true}`, true, false},
		{"missing", `{}`, false, true},
		{"invalid", `{`, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseMuteState([]byte(test.data))
			if got != test.want || (err != nil) != test.wantError {
				t.Fatalf("parseMuteState(%q) = %t, %v", test.data, got, err)
			}
		})
	}
}
