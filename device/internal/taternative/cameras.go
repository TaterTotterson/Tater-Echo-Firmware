package taternative

import "bytes"

var cameraFramePrefix = []byte{'T', 'C', 'A', 'M', '1'}

const maxCameraFrameBytes = 4 * 1024 * 1024

type DisplayCamera struct {
	ID        string
	Name      string
	Room      string
	Doorbell  bool
	Talkback  bool
	Available bool
}

type DisplayCameras struct {
	Available        bool
	Items            []DisplayCamera
	SelectedID       string
	SelectedName     string
	SelectedRoom     string
	SelectedTalkback bool
	StreamURL        string
	StreamActive     bool
	Loading          bool
	Error            string
}

func decodeCameraFrame(frame []byte) ([]byte, bool) {
	if len(frame) <= len(cameraFramePrefix)+3 || len(frame)-len(cameraFramePrefix) > maxCameraFrameBytes ||
		!bytes.HasPrefix(frame, cameraFramePrefix) {
		return nil, false
	}
	jpeg := frame[len(cameraFramePrefix):]
	if jpeg[0] != 0xff || jpeg[1] != 0xd8 {
		return nil, false
	}
	return jpeg, true
}

func decodeDisplayCameras(payload map[string]any) DisplayCameras {
	result := DisplayCameras{
		Available: boolValue(payload["available"]), SelectedID: stringValue(payload["selected_id"]),
		SelectedName: stringValue(payload["selected_name"]), SelectedRoom: stringValue(payload["selected_room"]),
		SelectedTalkback: boolValue(payload["selected_talkback"]), Loading: boolValue(payload["loading"]),
		StreamURL: stringValue(payload["stream_url"]), StreamActive: boolValue(payload["stream_active"]),
		Error: stringValue(payload["error"]),
	}
	rows, _ := payload["cameras"].([]any)
	for _, value := range rows {
		row, _ := value.(map[string]any)
		if row == nil || stringValue(row["id"]) == "" {
			continue
		}
		result.Items = append(result.Items, DisplayCamera{
			ID: stringValue(row["id"]), Name: stringValue(row["name"]), Room: stringValue(row["room"]),
			Doorbell: boolValue(row["doorbell"]), Talkback: boolValue(row["talkback"]), Available: boolValue(row["available"]),
		})
	}
	return result
}

func (c *Client) OpenCameras() bool {
	return c.sendJSON("display.cameras.open", "", map[string]any{})
}

func (c *Client) SelectCamera(cameraID string) bool {
	if cameraID == "" {
		return false
	}
	return c.sendJSON("display.camera.select", "", map[string]any{"camera_id": cameraID})
}

func (c *Client) CloseCamera() bool {
	return c.sendJSON("display.camera.close", "", map[string]any{})
}

func (c *Client) StartCameraTalkback(cameraID string) bool {
	if cameraID == "" {
		return false
	}
	c.StopCapture(true)
	c.stopVoice()
	return c.startVoice("camera_talkback", "camera talkback:"+cameraID, "", 0, false)
}
