//go:build linux

package main

import "time"

// A server-requested doorbell is a temporary overlay. Keeping it separate from
// cameraOpen restores the previous workspace and does not reopen a dismissed
// popup when another camera frame or clock snapshot arrives.
func (r *renderer) cameraPopupActive(now time.Time) bool {
	camera := r.state.Cameras
	return r.state.Connected && camera != nil && camera.SelectedID != "" &&
		camera.PopupID != "" && camera.PopupID != r.cameraPopupDismissedID &&
		camera.PopupExpiresAtUnixMS > r.currentTaterUnixMS(now)
}

func (r *renderer) cameraVisible(now time.Time) bool {
	return r.cameraOpen || r.cameraPopupActive(now)
}
