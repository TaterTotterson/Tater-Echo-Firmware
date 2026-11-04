package main

import "testing"

func TestMWWNativeStatusReportsOnlyActiveCustomURL(t *testing.T) {
	const modelURL = "https://wake.example/computer.json"
	setMWWState(true, true, "tater_custom_computer", "computer", "Computer", "url", modelURL,
		"normal", "balanced", 0.9, 5, 0.7, "")
	t.Cleanup(func() {
		setMWWState(false, false, "hey_tater", "", "", "", "", "normal", "balanced", 0, 0, 0, "")
	})
	status := mwwNativeStatus()
	if status["active_model_source"] != "url" || status["active_model_url"] != modelURL {
		t.Fatalf("active model status = %#v", status)
	}

	setMWWState(true, false, "tater_custom_computer", "computer", "Computer", "url", modelURL,
		"normal", "balanced", 0.9, 5, 0.7, "model failed")
	status = mwwNativeStatus()
	if status["active_model_url"] != "" {
		t.Fatalf("inactive model URL = %v", status["active_model_url"])
	}
}
