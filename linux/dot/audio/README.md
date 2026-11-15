# Biscuit / Radar capture profile

This independently authored overlay preserves the vendor ADC biquads and adds
Tater's existing first-order 80 Hz high-pass in the first-order coefficient
slots of all four stereo TLV320ADC3101s. No vendor firmware, calibration or
device boot image is included. The math and isolated Biscuit trial are recorded
in Tater-Echo-Hardware's `docs/findings/biscuit-adc3101-hpf-trial.md`.

The normal firmware release workflow now invokes `linux/dot/build_kernel.py`
for each target. It copies **committed** files from the pinned platform source,
retains all 50 existing security/USB patches, adds this overlay, checks the
original AOSP GCC 4.8 commit, and fixes the build timestamp. It never edits the
platform checkout. Separate Biscuit/Radar configs and appended device trees
are retained. Output contains the kernel, config, System.map and provenance
JSON; no device is accessed or flashed.

The source and toolchain are pinned in `platforms/dot-audio-lock.json`.
`tater_adc3101_hpf.h` is the canonical production helper; the driver-hook patch
is combined with that exact header at build time. The fake-I2C self-test
compiles this same header, not a reimplemented algorithm.

The filter is enabled by default only in these new kernels. For recovery or
comparison append `snd_soc_tlv320aic3101.tater_hpf80=0` to the boot command line.
The parameter is read-only after boot. Programming is restricted to the stopped
16 kHz ADC setup, diagnostic mode and AGC off, under the codec's own mutex.
All eight slots are physically read back; partial writes are restored and
errors prevent capture rather than asserting unverified success. No arbitrary
register, clock, gain, DSP-program or live coefficient-write API is added.

The application independently verifies `[1,80,255,16000]` from the read-only
ALSA status before skipping its software filter. It does not infer activation
from a kernel name or parameter. +40 dB hardware microphone gain is a separate
application startup transaction; software amplification is fixed to unity,
while VAD/DOA retain their former total-gain calibration.

Release packaging checks both the embedded board/profile and the reviewed
kernel digest. Both targets remain release-gated until the recorded hardware
and update checks are complete. Routine application and System OTA v1 do not
replace recovery kernels; existing devices need the target-specific kernel
installed through the qualified USB/recovery route until a kernel OTA
transaction is separately implemented and tested. Do not claim rootfs OTA
alone enables the hardware filter, or distribute a private unit's boot image.
