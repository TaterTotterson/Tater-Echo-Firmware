package taternative

import "testing"

func TestCameraPopupMetadataAndLegacyCameraPayloads(t *testing.T) {
	payload := map[string]any{
		"available": true, "selected_id": "camera.front", "popup_id": "ring-1",
		"popup_expires_at": 1795220000.125,
	}
	decoded := decodeDisplayCameras(payload)
	if decoded.PopupID != "ring-1" || decoded.PopupExpiresAtUnixMS != 1795220000125 {
		t.Fatalf("doorbell popup metadata lost: %#v", decoded)
	}
	delete(payload, "popup_id")
	delete(payload, "popup_expires_at")
	decoded = decodeDisplayCameras(payload)
	if decoded.PopupID != "" || decoded.PopupExpiresAtUnixMS != 0 || decoded.SelectedID != "camera.front" {
		t.Fatalf("legacy camera selection changed: %#v", decoded)
	}
}
