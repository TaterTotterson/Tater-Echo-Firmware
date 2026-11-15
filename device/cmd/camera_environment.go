package main

import "strings"

// Camera encoding and video playback are independent. Enable the verified
// encoder integration on qualified Shows (Athena needs matched AP opt-in);
// runtime identity checks and
// peer negotiation still gate its use. Other peers keep the MJPEG path.
// This policy never disables incoming video decoding or GPU presentation.
const cameraH264CaptureEnabled = true

// Application-slot camera helpers must not depend on which older immutable
// supervisor launched the daemon. Set capture policy explicitly rather than
// inheriting an older supervisor's encoder setting. Playback flags are retained.
func slottedCameraEnvironment(base []string, target, address string) []string {
	result := make([]string, 0, len(base)+2)
	athenaAP := false
	for _, value := range base {
		if value == "TATER_ATHENA_AP_ENABLE=1" {
			athenaAP = true
		}
		if !strings.HasPrefix(value, "TATER_CAMERA_ADDRESS=") && !strings.HasPrefix(value, "TATER_VENC_ENABLE=") {
			result = append(result, value)
		}
	}
	enabled := "0"
	if cameraH264CaptureEnabled && (target == "checkers" || target == "cronos" || target == "crown" || target == "rook" || target == "athena" && athenaAP) {
		enabled = "1"
	}
	return append(result, "TATER_CAMERA_ADDRESS="+address, "TATER_VENC_ENABLE="+enabled)
}

// Hosts can retain a call profile briefly across an application update. If
// hardware capture is disabled again, use balanced MJPEG for those requests
// rather than falling through to full-size snapshots.
func satChatCameraCaptureProfile(profile string) string {
	if !cameraH264CaptureEnabled {
		switch strings.ToLower(strings.TrimSpace(profile)) {
		case "echo-h264", "echo-h264-balanced":
			return "echo-balanced"
		}
	}
	return profile
}
