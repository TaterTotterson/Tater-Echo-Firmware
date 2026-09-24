package taternative

import "testing"

func TestApplyReplyDirectionNormalizesAngle(t *testing.T) {
	got := -1.0
	c := &Client{hooks: Hooks{ReplyDirection: func(angle float64) { got = angle }}}
	c.applyReplyDirection(optionalDirection(455.0))
	if got != 95 {
		t.Fatalf("reply direction = %.1f, want 95", got)
	}
}
