# LED ring behavior

Biscuit renders animations locally so motion stays smooth when Wi-Fi latency or
Tater event-loop timing varies. Tater chooses the semantic state and animation;
the firmware owns frame timing, brightness limits, direction overlays, and the
disconnect timeout.

## Priority

The visible state is resolved in this order:

1. hardware mute and safety/setup feedback;
2. active voice state (listening, thinking, tool call, reply);
3. timer/notification feedback;
4. media/reply audio animation; and
5. idle/off.

A newer state replaces the older animation atomically. Time-limited states
carry a TTL so the ring clears if Tater disconnects in the middle of a turn.
Hardware mute remains authoritative across reconnects and reboots.

## Native animation message

Tater sends `led_anim` with an animation name, color palette, frame period,
optional listening flag, TTL, and audio-glow response parameters. Supported
families include:

- `off`, `solid`, `spin`, and `rotate`;
- `audio_glow`/`meter` for whole-ring audio response;
- `sparkle`, `ping_pong`, `voice_ring`, `spinner`, `orbit`, `pulse`,
  `breathe`, `comet`, `dual_comet`, `scanner`, `ripple`, `heartbeat`,
  `theater`, `wave`, `shimmer`, `twinkle`, and `equalizer`.

Unknown animation names degrade to a safe supported rendering rather than
leaving stale LEDs active.

## Listening direction

While listening, Biscuit overlays the live seven-mic direction estimate only
after speech activity is credible. Before that, the ring uses the selected
listening glow instead of pointing at background noise. Direction is smoothed
for readable movement and held for reply so the response remains oriented
toward the speaker. Stereo reply targets use their front reference consistently
when a shared physical bearing would look contradictory.

Checkers does not claim a 360-degree bearing from its two microphones. Its
screen shows the voice state and audio-reactive orb instead.

## Audio Glow

`audio_glow` lights the full ring and maps live speaker RMS to perceived
brightness. Its response curve has independent attack, decay, silence floor,
gamma, reference level, and input curve parameters. Defaults favor visible
syllable movement without flicker:

| Parameter | Default |
|---|---:|
| attack | 0.60 |
| decay | 0.30 |
| floor | 0.06 |
| gamma | 2.20 |
| reference RMS | 0.22 |
| input curve | 0.70 |

Firmware clamps all received values to safe ranges. Audio is measured from the
actual local playback path, including stereo/group replies; the animation is
not a synthetic timer.

## Setup recovery

Setup gestures temporarily own the ring. Biscuit shows press progress, the
sixth-press hold countdown, and reset confirmation before rebooting into the
setup hotspot. Those frames cannot be overwritten by ordinary reply or media
state during the gesture.
