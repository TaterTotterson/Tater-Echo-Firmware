# Third-party components

EchoMuse is MIT licensed (see `LICENSE`). It vendors and links the components
below, each of which keeps its own licence. All are permissive and compatible
with redistribution under MIT, and each requires that its copyright notice
travels with the software — which is what this file is for.

The device binary published on the releases page is a **combined work**: it
links SpeexDSP and GoTinyAlsa. BSD-3-Clause asks that binary redistributions
reproduce the copyright notice "in the documentation or other materials
provided with the distribution", and this file is that material. If the binary
is ever distributed somewhere other than alongside this repository, this notice
needs to travel with it.

## Vendored into this repository

| Component | Where | Licence | Copyright |
|---|---|---|---|
| SpeexDSP (acoustic echo canceller) | `device/internal/aec/` | BSD-3-Clause | Xiph.Org Foundation, Jean-Marc Valin, Analog Devices, CSIRO |
| aioesphomeapi protocol buffers | `controller/esphome/vendor/` | MIT | Otto Winter |
| Home Assistant Voice PE timer sound (`timer_finished.flac`) | `controller/sounds/` | CC BY 4.0 | Clayton Charles Tapp |

Full licence texts sit beside the code, in `COPYING` or `*_LICENSE` files. Do
not remove them — they are the attribution the licences require. The timer
sound carries its attribution in `controller/sounds/LICENSE.md`, which ships
in the controller image alongside the audio: CC BY 4.0 asks that the credit
travel with the work, and the container is where the work actually goes.

## Linked as a submodule

| Component | Where | Licence | Copyright |
|---|---|---|---|
| GoTinyAlsa (`wilbowes/GoTinyAlsa` fork) | `GoTinyAlsa/` | BSD-3-Clause | binozoworks |

The fork exists to carry a `GetAudioStream` defer-in-loop leak fix.

## Installed at build or run time

These are dependencies rather than vendored code — they are fetched by the
Dockerfiles and `requirements.txt`, and are not redistributed as source here.
Listed because they end up in the published container image:

- **ONNX Runtime** (MIT) — wake word inference, controller and device.
- **openWakeWord** (Apache-2.0) — wake word models and feature pipeline.
- **DTLN** (MIT, Nils L. Westhausen) — the two pretrained noise-suppression
  models the controller image downloads at build time, pinned to a commit in
  `controller/Dockerfile`. Baked into the published image, so they are
  redistributed with it.
- **ffmpeg** (LGPL-2.1+ as packaged by Debian) — invoked as a separate
  process, never linked.
- The Python dependencies in `controller/requirements.txt`, all permissive.

## Wake word models

Wake-word models retain the licence and terms supplied by their publisher.
