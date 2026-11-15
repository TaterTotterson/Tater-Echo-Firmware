package hardwarebundle

import "testing"

func TestHelperFollowsOwningSlot(t *testing.T) {
	for _, root := range []string{"/usr/local/bin", "/data/tater-linux/app/slots/a", "/data/tater-linux/app/slots/b"} {
		if got := Beside(root+"/tater-show", "tater-hw-gpu"); got != root+"/tater-hw-gpu" {
			t.Fatalf("helper escaped owning slot: %s", got)
		}
	}
}
