# Native microWakeWord runtime

This directory builds the microfrontend and TFLite Micro inference engine used
by the Echo firmware. It exposes a small, versioned C ABI and is loaded by Go
with `dlopen`, so a missing or incompatible runtime disables local wake without
preventing the satellite firmware from starting.

Dependencies are fetched from immutable commits and verified by SHA-256 in
`CMakeLists.txt`. Build output belongs under `device/build/` and is ignored by
Git.

## Host build and model smoke test

The repeatable test wrapper downloads an immutable `hey_tater` model, verifies
its SHA-256, builds the host runtime, runs the C++ reset smoke test and golden
parity test, and then loads the same library through the Go ABI:

```bash
cd device
./test_microwakeword_runtime.sh
```

To use a local copy of that exact model, set `TATER_MWW_TEST_MODEL`. The wrapper
still verifies its hash. The underlying manual commands are:

```bash
cd device
cmake -S internal/wakeword/microwakeword/native \
  -B build/microwakeword-host -G Ninja \
  -DCMAKE_BUILD_TYPE=Release \
  -DTATER_MWW_TEST_MODEL=/path/to/hey_tater.tflite \
  -DTATER_MWW_GOLDEN_FILE=$PWD/internal/wakeword/microwakeword/native/testdata/hey_tater_integer_sweep_v1.golden
cmake --build build/microwakeword-host
ctest --test-dir build/microwakeword-host --output-on-failure
```

The smoke test processes two seconds of silence, resets both the microfrontend
and the model's resource variables, and requires the second score sequence to
exactly reproduce the first.

The golden test independently locks three contracts: exact 40-bin frontend
frames, end-to-end PCM scores, and stateful model scores from a 400-frame
feature probe. It also repeats PCM processing across deliberately irregular
chunk boundaries. See [`testdata/README.md`](testdata/README.md) for provenance
and regeneration instructions.

## FireOS 5 / ARMv7 build

The repository wrapper uses Android NDK 21.4.7075529 from the same pinned base
image as the firmware, then verifies ELF32/ARM, the dependency list, and the
ten-symbol public ABI:

```bash
cd device
./build_microwakeword_runtime.sh
```

The deliverable is `libtater_microwakeword.so`. It intentionally carries the
C++ runtime statically so the rooted Echo needs no matching
`libc++_shared.so` payload.
