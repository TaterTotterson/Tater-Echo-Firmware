# Do Not Disturb

Every supported Tater Echo target implements Do Not Disturb in the satellite,
not only in the controller UI. This prevents a direct native playback command
or a Sendspin source on the LAN from bypassing the user's choice.

Tater sends four per-satellite settings:

- `dnd_enabled` is the immediate manual control.
- `dnd_schedule_enabled` enables the daily window.
- `dnd_start` is the local time at which the scheduled window begins.
- `dnd_end` is the local time at which the scheduled window ends.

The manual control and scheduled window are additive. DND is active when the
manual control is on or the current local time is inside the configured daily
window. Overnight windows such as 22:00 through 07:00 are supported. Matching
start and end times intentionally schedule DND for the full day.

When DND becomes active, the firmware stops native voice playback, overlays,
audio scenes, Sat Chat receive audio, and Sendspin music. New non-critical
playback commands are rejected until DND is inactive. Wake detection and the
microphone remain available so the user can ask Tater to disable DND. Local
timers and alarms remain audible.

The schedule uses Tater's local UTC offset rather than assuming the embedded
Linux image has the controller's timezone. Tater refreshes that offset when it
changes, including daylight-saving-time transitions.
