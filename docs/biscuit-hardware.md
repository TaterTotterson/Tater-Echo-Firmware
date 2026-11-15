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
- Playback uses ALSA card 0, device 23 (stereo, 48 kHz), not device 22.
  Channel 8 remains the qualified echo reference; a stereo PCM configuration
  alone does not establish the speaker's physical channel routing.
- Mixer controls are addressed by name because numeric ALSA control IDs are not
  stable across target and kernel builds.

The diagnostic programs under [`device/tools`](../device/tools/) contain the
capture, angle-mapping, beamforming, and speaker-profile procedures used to
verify these facts.

## Hardware microphone gain (pending release)

The isolated filter trial described below was independent of gain staging:
its test application retained the original software gain and PGA settings.

The new application profile moves the previous +40 dB software boost into
the four TLV320ADC3101 microphone PGAs. Each stereo `ADC_A`–`ADC_D MICPGA
Volume Ctrl` is set to `80,80` (half-dB steps: +40 dB), while each `Digital
Volume Control` must read `88,88` (unity). The vendor driver's advertised
digital-volume maximum is wrong; do not clamp unity to 64 or rewrite it with
a range-checked tinyalsa setter. Startup reads and verifies both channels of
all eight controls, failing closed and rolling back partial PGA writes if the
expected gain staging cannot be established.

Software mic gain is fixed at 0 dB, including when an older controller or
environment supplies the former +40 dB setting. DOA/VAD thresholds retain
their original total-gain calibration of 100, without multiplying PCM again.
The synchronized playback-reference extraction remains at unity. Wake models,
pre-roll, boundary timing, AEC and beamforming algorithms are unchanged.

The 2026-11-14 temporary Biscuit trial passed normal/quiet/far requests,
immediate first words, waking during a reply and close speech. The owner
preferred its pickup. These are listening checks, not measured SNR or ADC
headroom results. Gains/services were restored after that trial; the production
source is prepared but not yet installed. Radar shares this capture hardware
and the source profile, but still needs its own live checks. Longer loud-room
and loud-playback tests plus the planned fleet OTA test remain release gates.

PGA state survives an application-only restart. Supported application OTA and
fast-failure rollback reboot the device. When manually returning to an older
software-gain binary, reboot as well: starting it over the new PGA state could
stack both +40 dB stages.

The ADC also supports programmable filtering/miniDSP and AGC. The vendor kernel
already initializes filter tuning; no additional array-wide AEC, beamforming
or wake-model offload has been demonstrated. Do not enable per-ADC AGC or
replace filter coefficients without phase, clipping and echo-cancellation tests.

## Hardware high-pass (pending release)

The pinned public Biscuit and Radar codec drivers have identical source hashes.
Their existing normal tuning uses the ADC's PRB_R2 processing block and a
vendor biquad high-pass section. The independent Tater experiment preserves
that section and adds a first-order 80 Hz filter in the otherwise unused
first-order coefficient slots. It does not upload miniDSP firmware, enable
AGC, change gain, or offload beamforming, AEC or wake inference.

The isolated trial used a default-off kernel and explicit enable parameter.
The permanent build integration now enables the same profile by default on
both Biscuit and Radar. Its boot-time opt-out is
`snd_soc_tlv320aic3101.tater_hpf80=0`; it cannot be switched while capture is
running. The driver only programs the fixed profile at the stopped-ADC phase,
under its own page-cache lock, and physically verifies all eight channel slots.
The read-only `Tater ADC HPF Status` control must report `[1,80,255,16000]`.
The matching application skips its software 80 Hz filter only for that exact
status on Biscuit/Radar. A missing, partial or unexpected status retains the
software path; a boot request alone is not proof of hardware activation.

The first owner-operated Biscuit boot passed with the feature disabled. The
enabled trial then reached the exact verified hardware status with healthy
nine-channel capture and unchanged original PGA/digital gain. The owner passed
immediate first words, quieter/far requests, reply interruption and physical
mute/unmute; the software bypass was verified active again after unmute.
Capture telemetry reported zero stalls/subscriber drops through 120 seconds.
The original kernel was restored on disk before testing and rebooted when the
trial ended. This short comparison is not a measured SNR/latency improvement
or extended headroom qualification. Radar has not been physically tested.
The normal release workflow now builds target-specific kernels through
`linux/dot/build_kernel.py` using a pinned source/toolchain and hash-locked
independent overlay. No private boot image is shipped. The reviewed kernel
digests and remaining release gates live in `platforms/dot-audio-lock.json`.
Neither application OTA nor System OTA v1 replaces the installed kernel;
existing units retain software filtering until their matching kernel is
installed through the qualified USB/recovery route. Final kernel OTA migration
is still a separate gate, not silently assumed from a rootfs update.

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
