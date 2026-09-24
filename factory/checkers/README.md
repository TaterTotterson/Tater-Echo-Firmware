# Echo Show 5 first generation (`checkers`) developer preview

This bundle installs the first-stage Tater screen on an Echo Show 5 (2019)
running rooted stock Fire OS 6. It deliberately does **not** write a boot,
recovery, system, or vendor partition and does not enable Tater's native audio
service yet. This run measures Checkers' original ALSA routes and microphone
configuration before a replacement OS removes that evidence.

## Prerequisites

- The device is `checkers`, unlocked with **amonet 2.0.1 or newer**.
- TWRP still boots.
- Rooted stock Fire OS 6 is still installed and the device appears in `adb
  devices`. Root through `su` is supported; root adbd is not required.
- `adb` and Python 3 are available on the macOS or Linux host.

Do not use amonet 2.0.0 for Checkers. It has a TWRP-upgrade fault on the 1.x to
2.x path, and amonet 1.x exposes only half of the device's 2 GB RAM.

## Install and collect the bring-up profile

```bash
./install.sh --profile
```

The installer verifies every bundled file, checks the exact device codename,
supported Android generation, root access, recovery partition, and amonet 2.x boot layout.
The on-device layout cannot distinguish 2.0.0 from 2.0.1, so it also asks you
to confirm 2.0.1+ and working TWRP. It then installs the APK, makes it the HOME
app, launches it, and runs the read-only hardware profiler. Review the generated
`profile.txt` before sharing its `.tar.gz` archive.

Use `./install.sh --no-home` to launch the APK without changing the HOME app.
Use `./install.sh --demo` to cycle through every visual state before the native
audio service is enabled.
Use `./install.sh --uninstall` to remove this preview.

The screen is functional by itself and shows `Native service offline` during
this stage. Once the measured audio bindings are added, the same APK receives
live listening/thinking/reply direction, media, timer, mute, volume, and
intercom state over a loopback-only protocol. The installer also accepts the
later LineageOS 18.1 test environment, but profiling it cannot recover the
original Fire OS audio configuration.
