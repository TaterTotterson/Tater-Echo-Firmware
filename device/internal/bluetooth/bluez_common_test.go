package bluetooth

import "testing"

func TestUUIDFromBlueZString(t *testing.T) {
	for _, test := range []struct {
		text string
		want string
	}{
		{"00002902-0000-1000-8000-00805f9b34fb", "00002902-0000-1000-8000-00805f9b34fb"},
		{"6ba1b218-15a8-461f-9fa8-5dcae273eafd", "6ba1b218-15a8-461f-9fa8-5dcae273eafd"},
	} {
		got, err := uuidFromString(test.text)
		if err != nil {
			t.Fatal(err)
		}
		if got.String() != test.want {
			t.Fatalf("uuid %q = %q, want %q", test.text, got.String(), test.want)
		}
	}
	if _, err := uuidFromString("bad"); err == nil {
		t.Fatal("accepted malformed UUID")
	}
}

func TestBlueZCharacteristicProperties(t *testing.T) {
	got := bluezCharacteristicProperties([]string{"read", "write", "write-without-response", "notify", "indicate", "secure-read"})
	if got != 0x3E {
		t.Fatalf("properties = 0x%02x, want 0x3e", got)
	}
}
