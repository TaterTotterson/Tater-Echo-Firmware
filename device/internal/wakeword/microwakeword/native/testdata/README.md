# microWakeWord golden vectors

`hey_tater_integer_sweep_v1.golden` is a reviewable, model-specific parity
fixture. It contains per-frame FNV-1a hashes for the raw 40-bin uint16
microfrontend output, exact uint8 scores for the end-to-end PCM path, and exact
uint8 scores for a deterministic 400-frame model probe.

The vector was generated from two implementations independent of the firmware
runtime:

- `pymicro-features==2.0.2` for the trainer-compatible microfrontend
  (Apache-2.0).
- `ai-edge-litert==2.2.0` builtin reference kernels for full LiteRT inference
  (Apache-2.0). The reference resolver is explicit because the default XNNPACK
  delegate may select adjacent quantized output buckets from TFLM.

Both use the immutable `hey_tater.tflite` model at SHA-256
`d3bf0d87c5c00ccfeda3cebba528c5d4012a5aaae51e61b7b01ae5af9008b4b9`.
The quantization step follows Home Assistant Android's microWakeWord runtime:
scale the raw frontend value by `0.0390625`, apply the model tensor's scale and
zero point, and round to the nearest integer.

To regenerate intentionally:

```bash
python3 -m pip install pymicro-features==2.0.2 ai-edge-litert==2.2.0
python3 generate_golden.py /path/to/hey_tater.tflite \
  hey_tater_integer_sweep_v1.golden
```

Treat a changed vector as an implementation or dependency change requiring
review. Do not update it merely to make a parity failure pass.
