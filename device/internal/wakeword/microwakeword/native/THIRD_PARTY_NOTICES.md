# Third-party notices

The native microWakeWord runtime builds the following pinned dependencies. The
source archives are verified by SHA-256 in `CMakeLists.txt`; they are not
silently updated during normal firmware builds.

- TensorFlow Lite Micro, commit `2747abd5c82a95fb1624106a946fc671c31f16e8`
  — Apache License 2.0.
- FlatBuffers, commit `0100f6a5779831fa7a651e4b67ef389a8752bd9b`
  — Apache License 2.0.
- gemmlowp, commit `fda83bdc38b118cc6b56753bd540caa49e570745`
  — Apache License 2.0.
- ruy, commit `54774a7a2cf85963777289193629d4bd42de4a59`
  — Apache License 2.0.
- kissfft, commit `7bce4153c6bc8aba2db0e889e576f9d00505cbe1`
  — BSD-3-Clause.

The dependency selection and Android build structure were informed by Home
Assistant Android's Apache-2.0 `microwakeword` module. The runtime API and
engine in this directory are Tater-specific implementations: they return raw
scores to Go, reset TFLM resource variables, validate the Tater model contract,
and do not contain its JNI layer.

Golden-vector generation additionally uses `pymicro-features` 2.0.2 and
`ai-edge-litert` 2.2.0, both under Apache License 2.0. They are development-only
reference tools and are not linked into or distributed with the firmware.
