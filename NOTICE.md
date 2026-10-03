# Third-party notices

Tater Echo Firmware is MIT licensed. The firmware and release bundles also
contain or link the components below under their own terms. Keep this notice
with redistributed binaries and factory bundles.

## Firmware components

| Component | Location | License |
|---|---|---|
| SpeexDSP acoustic echo canceller | `device/internal/aec/` | BSD-3-Clause |
| GoTinyAlsa | `device/third_party/GoTinyAlsa/` | BSD-3-Clause |
| TFLite Micro and microfrontend dependencies | `device/internal/wakeword/microwakeword/native/` build | Apache-2.0 and BSD-3-Clause |
| EchoMuse emOS and Biscuit hardware work | `emos/`, `factory/biscuit/`, Biscuit firmware | MIT (Wil Bowes) |
| TECHO5 Checkers Linux tooling, camera driver, and direct ALSA speaker path | `linux/checkers/`, `device/internal/checkersalsa/`, generated rootfs, factory bundle | MIT |
| Linux kernel in the pinned TECHO5 Checkers boot image | Checkers factory bundle `payload/boot.img` | GPL-2.0 |

SpeexDSP is copyright Xiph.Org Foundation, Jean-Marc Valin, Analog Devices,
and CSIRO. EchoMuse is copyright Wil Bowes; its MIT notice is preserved in
`LICENSE`. GoTinyAlsa is copyright binozoworks; this repository uses the
`wilbowes/GoTinyAlsa` fork containing a stream leak fix, with local tinyalsa
2.x compatibility fixes retained in the vendored copy. Checkers Linux hardware
enablement, rescue/A-B contract, framebuffer research, camera driver, and direct
ALSA speaker path are
derived from TECHO5 by HuskerMinion; its license is embedded in the generated
rootfs and factory bundle and included with the speaker source. Detailed wake-runtime
attribution is in
[`device/internal/wakeword/microwakeword/native/THIRD_PARTY_NOTICES.md`](device/internal/wakeword/microwakeword/native/THIRD_PARTY_NOTICES.md).

The Checkers boot image contains a Linux 4.9.337 kernel rebuilt by TECHO5
from [Amazon's published kernel source](https://github.com/amazon-oss/android_kernel_amazon_mt8163)
with the Checkers changes described in [TECHO5's kernel notice](https://github.com/HuskerMinion/techo5/blob/main/NOTICE).
Its GPL-2.0 terms are separate from this repository's MIT license.

## BusyBox

Biscuit factory releases contain BusyBox under GPL-2.0. Every published
factory archive includes the exact corresponding source tarball, license, and
build script under `sources/`.

## Build dependencies

Native runtime libraries fetched by the pinned build scripts retain their
upstream terms.

## Wake-word models

Wake-word models retain the license and terms supplied by their publisher.
Adding a model to a release does not relicense it under this repository's MIT
license.

The timer-stop model is published by Kevin Ahrendt and is pinned from the
Tater-Wake-Words model collection. Its publisher metadata remains in
`device/internal/wakeword/microwakeword/models/stop.json`.
