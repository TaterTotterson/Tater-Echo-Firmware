#!/bin/bash
set -euo pipefail

DEVICE_DIR=$(cd "$(dirname "$0")" && pwd)
DEST="$DEVICE_DIR/internal/wakeword/microwakeword/release_assets"

RUNTIME="$DEVICE_DIR/build/microwakeword-android/libtater_microwakeword.so"
ORT="$DEVICE_DIR/build/onnxruntime/armeabi-v7a/libonnxruntime.so"
MELSPEC="$DEVICE_DIR/build/microwakeword-testdata/melspectrogram.onnx"
EMBEDDING="$DEVICE_DIR/build/microwakeword-testdata/embedding_model.onnx"

for source in "$RUNTIME" "$ORT" "$MELSPEC" "$EMBEDDING"; do
  [[ -s "$source" ]] || {
    echo "required Biscuit release wake asset is missing: $source" >&2
    exit 1
  }
done

mkdir -p "$DEST"
install -m 0755 "$RUNTIME" "$DEST/libtater_microwakeword.so"
install -m 0755 "$ORT" "$DEST/libonnxruntime.so"
install -m 0644 "$MELSPEC" "$DEST/melspectrogram.onnx"
install -m 0644 "$EMBEDDING" "$DEST/embedding_model.onnx"

echo "Prepared self-contained Biscuit OTA wake assets."
