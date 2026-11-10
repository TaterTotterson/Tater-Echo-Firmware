# Biscuit hardware notes

This document keeps the measured Echo Dot 2 (`biscuit`) facts needed by the
Tater firmware and its diagnostic tools. Installation instructions live in
[`factory/dot-linux/README.md`](../factory/dot-linux/README.md).

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
continuously estimates direction from channels 0–5 and forms one primary beam
from all seven microphones. A speech-activity latch stabilizes the direction
through a wake phrase. MWW and the selected OWW or NWW companion consume the
same uninterrupted stream; direction changes do not replay or reset a model.
The primary beam is selected before the single SpeexDSP AEC pass, including
while music plays. The same cleaned signal feeds wake inference and transport,
and the accepted direction is held for the voice turn. Uncertain or unhealthy
arrays retain the center fallback. Continued-chat acquisition keeps its own
speech-onset lock.

Raw capture batches carry their sample-end timestamp, sequence, and producer
generation. The two 80 ms chunks in a 160 ms batch have sample-correct timestamps.
A capture restart or subscriber drop clears partial audio, detector state,
beam history, and obsolete wake candidates while preserving learned AEC
coefficients. A watchdog restarts stalled ALSA capture, and ALSA overruns end
the producer rather than silently splicing audio across the loss.

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

- Wake detection scores one seven-microphone primary beam without adaptive
  gain; channel 6 is the fallback when the array has no reliable direction.
- Direction estimators remain warm while idle and carry the winning wake
  bearing and audio directly into verification, training, STT pre-roll, and
  the seven-microphone voice beam.
- SpeexDSP AEC consumes the selected primary wake beam, center fallback, or
  locked voice beam together with the channel 8 reference. Only one AEC path
  runs per batch; directional playback pickup adds no second canceller.
- Playback uses ALSA card 0, device 22 and drives the right channel.
- Mixer controls are addressed by name because numeric ALSA control IDs are not
  stable across target and kernel builds.

The diagnostic programs under [`device/tools`](../device/tools/) contain the
capture, angle-mapping, beamforming, and speaker-profile procedures used to
verify these facts.

## Operational boundaries

The TWRP factory installer combines the attached device's own TWRP boot header
with Biscuit's pinned TECHO5 Dot Linux kernel, writes Linux to recovery, and
installs the initial A/B rootfs under cache. Stock system partitions remain
intact as firmware and audio-coefficient donors; Android is not the running
userspace. Verified boot-critical backups and a restorable TWRP image are kept
on the device.

Do not copy partition maps, boot images, or mixer assumptions to another Echo
codename. Each model needs hardware-backed validation and its own recovery
path before release support is enabled.
