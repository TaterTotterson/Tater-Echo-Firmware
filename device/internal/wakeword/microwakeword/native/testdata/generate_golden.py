#!/usr/bin/env python3
"""Generate the committed host parity vector from independent references."""

from __future__ import annotations

import argparse
import hashlib
import importlib.metadata
from pathlib import Path

import numpy as np
from ai_edge_litert.interpreter import Interpreter, OpResolverType
from pymicro_features import MicroFrontend


MODEL_SHA256 = "d3bf0d87c5c00ccfeda3cebba528c5d4012a5aaae51e61b7b01ae5af9008b4b9"
PYMICRO_VERSION = "2.0.2"
LITERT_VERSION = "2.2.0"
SAMPLE_COUNT = 32000
FEATURE_SIZE = 40
MODEL_PROBE_FRAMES = 400
FRONTEND_SCALE = 0.0390625
FNV_OFFSET = 14695981039346656037
FNV_PRIME = 1099511628211


def integer_sweep() -> np.ndarray:
    pcm = np.empty(SAMPLE_COUNT, dtype=np.int16)
    state = 0x13579BDF
    for i in range(SAMPLE_COUNT):
        state = (state * 1664525 + 1013904223) & 0xFFFFFFFF
        noise = ((state >> 20) & 0xFFF) - 2048
        segment = i // 4000
        if segment == 0:
            value = noise * 2
        elif segment == 1:
            value = ((i * 37) % 512 - 256) * 48 + noise * 2
        elif segment == 2:
            value = (10000 if (i // 23) % 2 == 0 else -10000) + noise * 3
        elif segment == 3:
            value = ((i * i + 17 * i) % 1024 - 512) * 24
            value += (14000 if i % 2 == 0 else -14000) if i % 997 < 8 else 0
            value += noise
        elif segment == 4:
            value = ((i * 31) % 1024 - 512) * 20
            value += ((i * 7) % 256 - 128) * 30 + noise * 2
        elif segment == 5:
            value = (
                ((i * 53) % 1024 - 512) * 28 + noise * 2
                if (i // 320) % 2 == 0
                else noise
            )
        elif segment == 6:
            value = noise * 12 + (5000 if (i // 41) % 2 == 0 else -5000)
        else:
            value = ((i * 11) % 2048 - 1024) * 14
            value += ((i * 97) % 256 - 128) * 12 + noise * 3
        pcm[i] = np.int16(max(-32768, min(32767, value)))
    return pcm


def extract_features(pcm: np.ndarray) -> np.ndarray:
    frontend = MicroFrontend()
    frames: list[list[int]] = []
    for offset in range(0, len(pcm), 160):
        output = frontend.process_samples(pcm[offset : offset + 160].tobytes())
        if output.features:
            raw = [int(round(value / FRONTEND_SCALE)) for value in output.features]
            if len(raw) != FEATURE_SIZE:
                raise RuntimeError(f"frontend produced {len(raw)} bins")
            for original, integer in zip(output.features, raw):
                if original != integer * FRONTEND_SCALE:
                    raise RuntimeError("frontend feature was not an exact uint16 scaling")
            frames.append(raw)
    return np.asarray(frames, dtype=np.uint16)


def round_away_from_zero(values: np.ndarray) -> np.ndarray:
    return np.where(values >= 0, np.floor(values + 0.5), np.ceil(values - 0.5))


def model_probe_features() -> np.ndarray:
    frames = np.empty((MODEL_PROBE_FRAMES, FEATURE_SIZE), dtype=np.uint16)
    state = 0x2468ACE1
    for frame in range(MODEL_PROBE_FRAMES):
        for bin_index in range(FEATURE_SIZE):
            state = (state * 1664525 + 1013904223) & 0xFFFFFFFF
            jitter = ((state >> 16) & 0xFFFF) % 80
            frames[frame, bin_index] = (frame * 17 + bin_index * 29 + jitter) % 667
    return frames


def infer(model_path: Path, features: np.ndarray) -> list[int]:
    # TFLM uses reference integer kernels for this model. Full LiteRT's default
    # XNNPACK delegate can legitimately choose adjacent quantized buckets, so
    # pin the independent reference interpreter to its builtin reference ops.
    interpreter = Interpreter(
        model_path=str(model_path),
        experimental_op_resolver_type=OpResolverType.BUILTIN_REF,
    )
    interpreter.allocate_tensors()
    input_detail = interpreter.get_input_details()[0]
    output_detail = interpreter.get_output_details()[0]
    if input_detail["dtype"] != np.int8 or tuple(input_detail["shape"]) != (1, 2, 40):
        raise RuntimeError(f"unexpected model input: {input_detail}")
    if output_detail["dtype"] != np.uint8 or tuple(output_detail["shape"]) != (1, 1):
        raise RuntimeError(f"unexpected model output: {output_detail}")

    scale, zero_point = input_detail["quantization"]
    scaled = features.astype(np.float32) * np.float32(FRONTEND_SCALE)
    quantized = round_away_from_zero(scaled / np.float32(scale) + zero_point)
    quantized = np.clip(quantized, -128, 127).astype(np.int8)

    scores: list[int] = []
    stride = input_detail["shape"][1]
    for offset in range(0, len(quantized) - stride + 1, stride):
        chunk = quantized[offset : offset + stride].reshape(input_detail["shape"])
        interpreter.set_tensor(input_detail["index"], chunk)
        interpreter.invoke()
        scores.append(int(interpreter.get_tensor(output_detail["index"])[0][0]))
    return scores


def feature_hash(frame: np.ndarray) -> int:
    result = FNV_OFFSET
    for value in frame:
        integer = int(value)
        result ^= integer & 0xFF
        result = (result * FNV_PRIME) & 0xFFFFFFFFFFFFFFFF
        result ^= integer >> 8
        result = (result * FNV_PRIME) & 0xFFFFFFFFFFFFFFFF
    return result


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("model", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()

    pymicro_version = importlib.metadata.version("pymicro-features")
    litert_version = importlib.metadata.version("ai-edge-litert")
    if pymicro_version != PYMICRO_VERSION or litert_version != LITERT_VERSION:
        raise RuntimeError(
            f"reference version mismatch: pymicro-features={pymicro_version}, "
            f"ai-edge-litert={litert_version}"
        )
    model_sha = hashlib.sha256(args.model.read_bytes()).hexdigest()
    if model_sha != MODEL_SHA256:
        raise RuntimeError(f"model SHA-256 is {model_sha}, expected {MODEL_SHA256}")

    features = extract_features(integer_sweep())
    scores = infer(args.model, features)
    model_probe_scores = infer(args.model, model_probe_features())
    lines = [
        "TATER_MWW_GOLDEN_V1",
        "fixture integer_sweep_v1",
        f"model_sha256 {model_sha}",
        f"frontend_reference pymicro-features-{pymicro_version}",
        f"inference_reference ai-edge-litert-{litert_version}-builtin-ref",
        f"sample_count {SAMPLE_COUNT}",
        f"feature_size {FEATURE_SIZE}",
        f"feature_frame_count {len(features)}",
        f"score_count {len(scores)}",
        f"model_probe_frame_count {MODEL_PROBE_FRAMES}",
        f"model_probe_score_count {len(model_probe_scores)}",
        "feature_hashes",
    ]
    lines.extend(f"{feature_hash(frame):016x}" for frame in features)
    lines.append("scores_u8")
    lines.extend(" ".join(str(value) for value in scores[i : i + 20]) for i in range(0, len(scores), 20))
    lines.append("model_probe_scores_u8")
    lines.extend(
        " ".join(str(value) for value in model_probe_scores[i : i + 20])
        for i in range(0, len(model_probe_scores), 20)
    )
    args.output.write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(
        f"wrote {len(features)} feature frames, {len(scores)} PCM scores, and "
        f"{len(model_probe_scores)} model probe scores to {args.output}"
    )


if __name__ == "__main__":
    main()
