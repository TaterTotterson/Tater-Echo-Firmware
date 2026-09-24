package taternative

import "math"

func optionalDirection(value any) *float64 {
	var angle float64
	switch typed := value.(type) {
	case float64:
		angle = typed
	case float32:
		angle = float64(typed)
	case int:
		angle = float64(typed)
	case int64:
		angle = float64(typed)
	default:
		return nil
	}
	if math.IsNaN(angle) || math.IsInf(angle, 0) {
		return nil
	}
	angle = math.Mod(angle, 360)
	if angle < 0 {
		angle += 360
	}
	return &angle
}

func (c *Client) applyReplyDirection(angle *float64) {
	if angle == nil {
		return
	}
	if hook := c.hooks.ReplyDirection; hook != nil {
		hook(*angle)
	}
}
