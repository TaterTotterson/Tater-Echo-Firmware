# Third-party notices

Tater Echo Firmware is MIT licensed. The firmware and release bundles also
contain or link the components below under their own terms. Keep this notice
with redistributed binaries and factory bundles.

## Firmware components

| Component | Location | License |
|---|---|---|
| SpeexDSP acoustic echo canceller | `device/internal/aec/` | BSD-3-Clause |
| GoTinyAlsa | `GoTinyAlsa/` submodule | BSD-3-Clause |
| TFLite Micro and microfrontend dependencies | `device/internal/wakeword/microwakeword/native/` build | Apache-2.0 and BSD-3-Clause |

SpeexDSP is copyright Xiph.Org Foundation, Jean-Marc Valin, Analog Devices,
and CSIRO. GoTinyAlsa is copyright binozoworks; this repository uses the
`wilbowes/GoTinyAlsa` fork containing a stream leak fix. Detailed wake-runtime
attribution is in
[`device/internal/wakeword/microwakeword/native/THIRD_PARTY_NOTICES.md`](device/internal/wakeword/microwakeword/native/THIRD_PARTY_NOTICES.md).

## BusyBox

Biscuit factory releases contain BusyBox under GPL-2.0. Every published
factory archive includes the exact corresponding source tarball, license, and
build script under `sources/`.

## Build and Android dependencies

The Checkers screen uses Android/AndroidX and Gradle dependencies under their
respective upstream licenses. Native runtime libraries fetched by the pinned
build scripts retain their upstream license files in the build output.

## Wake-word models

Wake-word models retain the license and terms supplied by their publisher.
Adding a model to a release does not relicense it under this repository's MIT
license.
