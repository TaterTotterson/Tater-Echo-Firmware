# Biscuit hardware notes

This document keeps the measured Echo Dot 2 (`biscuit`) facts needed by the
Tater firmware and its diagnostic tools. Installation instructions live in
[`factory/biscuit/README.md`](../factory/biscuit/README.md).

## Microphone and echo-reference channels

The production capture stream is ALSA card 0, device 24: nine interleaved
S24_3LE channels at 16 kHz.

| Channel | Physical source |
|---|---|
| 0 | Perimeter mic, 330 degrees |
| 1 | Perimeter mic, 30 degrees |
| 2 | Perimeter mic, 90 degrees |
| 3 | Perimeter mic, 150 degrees |
| 4 | Perimeter mic, 210 degrees |
| 5 | Perimeter mic, 270 degrees |
| 6 | Center omnidirectional mic |
| 7 | Left playback loopback/reference |
| 8 | Right playback loopback/reference |

The six perimeter microphones are approximately 36 mm from the center. Tater
uses channel 6 for direction-neutral wake detection, continuously estimates
direction from channels 0–5, and locks a suitable perimeter microphone for a
voice turn when confidence is sufficient.

Channels 7 and 8 are playback loopbacks, not microphones. Measurements show
that the internal speaker path emits the right channel, so production echo
cancellation uses channel 8. The reference arrives in the same TDM frame as
the microphone samples and trails acoustically captured playback by about 33
samples (2.06 ms at 16 kHz), with inverted polarity. It is before the playback
volume control and therefore does not represent downstream DAC clipping.

The capture hardware uses four TLV320ADC3101 stereo ADCs on I2C bus 0 at
addresses 0x18–0x1b. Their probe order maps pairs to channels 0/1, 2/3, 4/5,
and 6/7. All four share the TDM data bus.

## Audio path

- Wake detection receives the center microphone without adaptive gain.
- Direction estimators remain warm while idle, then select a perimeter mic at
  the start of a turn.
- SpeexDSP AEC consumes the selected microphone and channel 8 reference.
- Playback uses ALSA card 0, device 22 and drives the right channel.
- Mixer controls are addressed by name because Fire OS builds can renumber
  them.

The diagnostic programs under [`device/tools`](../device/tools/) contain the
capture, angle-mapping, beamforming, and speaker-profile procedures used to
verify these facts.

## Operational boundaries

The factory installer builds emOS from the attached device's own stock boot
partition and keeps stock in the other boot slot. emOS still mounts the
compatible vendor/system partitions for hardware support; it replaces the
Android userspace, not the device kernel or device trees.

Do not copy partition maps, boot images, or mixer assumptions to another Echo
codename. Each model needs hardware-backed validation and its own recovery
path before release support is enabled.
