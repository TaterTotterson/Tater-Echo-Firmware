package main

func fade(frame, total, fadeFrames int) float64 {
	if frame < fadeFrames {
		return float64(frame) / float64(fadeFrames)
	}
	remaining := total - frame - 1
	if remaining < fadeFrames {
		return float64(remaining) / float64(fadeFrames)
	}
	return 1
}
