package taternative

import "testing"

func TestDisplayWeatherCommandReachesScreenHook(t *testing.T) {
	var got map[string]any
	client, err := New(Config{URL: "ws://tater.test", DeviceID: "echo-show"}, Hooks{
		DisplayWeather: func(payload map[string]any) { got = payload },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	client.handle(Envelope{Type: "display.weather", Payload: map[string]any{
		"available":        true,
		"temperature_text": "74°",
		"condition":        "Partly cloudy",
	}})

	if got == nil || got["available"] != true || got["temperature_text"] != "74°" {
		t.Fatalf("display weather hook payload = %#v", got)
	}
}

func TestDisplayNotificationCommandReachesScreenHook(t *testing.T) {
	var got map[string]any
	client, err := New(Config{URL: "ws://tater.test", DeviceID: "echo-show"}, Hooks{
		DisplayNotification: func(payload map[string]any) { got = payload },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	client.handle(Envelope{Type: "display.notification", Payload: map[string]any{
		"id": "event-1", "camera_name": "Front Door", "description": "A package was delivered.",
	}})

	if got == nil || got["id"] != "event-1" || got["camera_name"] != "Front Door" {
		t.Fatalf("display notification hook payload = %#v", got)
	}
}
