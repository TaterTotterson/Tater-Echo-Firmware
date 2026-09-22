package taternative

import "testing"

func TestNormalizeURL(t *testing.T) {
	cases := map[string]string{
		"tater.local:8501":                                "ws://tater.local:8501/api/tater/satellite/v1/ws",
		"https://tater.example":                           "wss://tater.example/api/tater/satellite/v1/ws",
		"ws://tater.local:8501/api/tater/satellite/v1/ws": "ws://tater.local:8501/api/tater/satellite/v1/ws",
	}
	for input, want := range cases {
		got, err := NormalizeURL(input)
		if err != nil {
			t.Fatalf("NormalizeURL(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("NormalizeURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeURLRejectsUnsafeScheme(t *testing.T) {
	if _, err := NormalizeURL("file:///tmp/tater"); err == nil {
		t.Fatal("file URL unexpectedly accepted")
	}
}
