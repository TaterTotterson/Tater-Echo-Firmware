# Companion Display v1

Companion Display is a small trusted-LAN protocol for presenting now-playing
information and temporary notifications on screen-capable Tater Echo devices.
It is independent of Tater, Home Assistant, and Tater Tube. Tater Tube Server
and Awareness Core are clients; any other local system can implement the same
discovery and HTTP calls.

## Discovery

Screen devices advertise `_tater-display._tcp.local.` over mDNS. The TXT record
includes `path=/v1`, `id`, `name`, `room`, `board`, `protocol=1`,
`now_playing=1`, and `notifications=1`. TCP port `8930` is the default.

For containers or networks where mDNS is unavailable, send this JSON datagram
to UDP port `8930` using the local subnet broadcast address:

```json
{"type":"tater.display.discover","protocol_version":1,"request_id":"client-generated-id"}
```

The device replies directly to the sender with its stable device ID, friendly
name, room, board, firmware version, TCP port, API path, and capabilities. A
client must build the HTTP address from the datagram's source address rather
than accepting an address from the payload.

`GET /v1/capabilities` returns the same identity plus supported artwork types,
maximum artwork size, progress support, and lease support.

## Now playing

Send `PUT /v1/now-playing` with `Content-Type: application/json`:

```json
{
  "active": true,
  "source_id": "media-server:living-room-player",
  "source_name": "Living Room TV",
  "event_id": "playback-session-123",
  "playback_state": "playing",
  "media_type": "movie",
  "title": "Example Movie",
  "series_title": "",
  "season": 0,
  "episode": 0,
  "subtitle": "Movie",
  "progress_ms": 42000,
  "duration_ms": 7200000,
  "playback_speed": 1,
  "progress_updated_at_unix_ms": 1793971200000,
  "lease_ms": 90000,
  "revision": 123,
  "image_data_b64": "optional base64 image bytes",
  "image_content_type": "image/jpeg"
}
```

The device interpolates progress locally. Renew an active presentation before
its lease expires; 30-second renewals with a 90-second lease are recommended.
If the sender disappears, the display returns home automatically.

Send the same request with `active: false` and the same `source_id`, or use
`DELETE /v1/now-playing?source_id=...`, to clear the presentation. A stale
source cannot clear a different source that currently owns an unexpired lease.

Artwork is uploaded rather than fetched by the device, preventing credential
leaks and server-side request forgery. JPEG, PNG, and WebP are accepted up to
the advertised limit. A same-event renewal may omit artwork; the device retains
the existing image until the event changes or the presentation clears.

## Notifications

Send `POST /v1/notifications` with `Content-Type: application/json`:

```json
{
  "source_id": "awareness-core:front-door",
  "event_id": "event-123",
  "kind": "notification",
  "display_label": "Awareness",
  "priority": "normal",
  "title": "Front Door",
  "camera_name": "Front Door",
  "description": "A person was detected at the front door.",
  "source": "awareness_core",
  "image_fit": "cover",
  "created_at_unix_ms": 1793971200000,
  "ttl_ms": 90000,
  "remember_as_last_event": true,
  "image_data_b64": "optional base64 image bytes",
  "image_content_type": "image/jpeg"
}
```

`source_id` and `event_id` identify the sender and event. Either `title` or
`description` is required. Priorities are `low`, `normal`, `high`, or
`critical`; image fitting is `contain` or `cover`; and the firmware bounds the
requested TTL. With `remember_as_last_event`, Crown retains the notification in
its normal Last Event pane after the temporary popup expires.

Notifications use the existing generic card renderer, including priority
color, progress bar, and touch dismissal. When the sender omits an image, the
screen uses its embedded primary Tater artwork. Uploaded JPEG, PNG, or WebP
bytes have the same size limit as now-playing artwork, and the device never
fetches a sender-provided URL.

## Security

Version 1 follows the same trusted-LAN model as Tater Echo's unpaired Sendspin
mode. Do not expose TCP or UDP port `8930` to the internet or an untrusted VLAN.
Payload sizes, artwork types, source ownership, and leases are bounded by the
device.
