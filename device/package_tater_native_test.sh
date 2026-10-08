#!/bin/bash
set -euo pipefail

DEVICE_DIR=$(cd "$(dirname "$0")" && pwd)
STAMP=$(date +%Y%m%d-%H%M%S)
BUNDLE_DIR=${1:-"$DEVICE_DIR/build/tater-native-test-$STAMP"}
SERVER="$DEVICE_DIR/build/server"
RUNTIME="$DEVICE_DIR/build/microwakeword-android/libtater_microwakeword.so"
MODEL="$DEVICE_DIR/internal/wakeword/microwakeword/models/hey_tater.tflite"
MANIFEST="$DEVICE_DIR/internal/wakeword/microwakeword/models/hey_tater.json"
OWW_ONNX_MODEL="$DEVICE_DIR/internal/wakeword/microwakeword/models/hey_tater.oww.onnx"
OWW_METADATA="$DEVICE_DIR/internal/wakeword/microwakeword/models/hey_tater.oww.json"
WAKE_BUNDLE="$DEVICE_DIR/internal/wakeword/microwakeword/models/hey_tater.wake-bundle.json"
STOP_MODEL="$DEVICE_DIR/build/microwakeword-testdata/stop.tflite"
STOP_MANIFEST="$DEVICE_DIR/internal/wakeword/microwakeword/models/stop.json"
ORT_RUNTIME="$DEVICE_DIR/build/onnxruntime/armeabi-v7a/libonnxruntime.so"
OWW_MELSPEC_ONNX="$DEVICE_DIR/build/microwakeword-testdata/melspectrogram.onnx"
OWW_EMBED_ONNX="$DEVICE_DIR/build/microwakeword-testdata/embedding_model.onnx"

for required in "$SERVER" "$RUNTIME" "$ORT_RUNTIME" "$MODEL" "$MANIFEST" "$OWW_ONNX_MODEL" "$OWW_METADATA" "$WAKE_BUNDLE" "$STOP_MODEL" "$STOP_MANIFEST" "$OWW_MELSPEC_ONNX" "$OWW_EMBED_ONNX"; do
  if [[ ! -f "$required" ]]; then
    echo "missing test artifact: $required" >&2
    echo "run ./compile.sh, ./build_microwakeword_runtime.sh, and ./test_microwakeword_runtime.sh first" >&2
    exit 1
  fi
done
if [[ -e "$BUNDLE_DIR" ]]; then
  echo "bundle destination already exists: $BUNDLE_DIR" >&2
  exit 1
fi

mkdir -p "$BUNDLE_DIR/firmware" "$BUNDLE_DIR/microwakeword"
install -m 0755 "$SERVER" "$BUNDLE_DIR/firmware/server"
install -m 0755 "$RUNTIME" "$BUNDLE_DIR/microwakeword/libtater_microwakeword.so"
install -m 0755 "$ORT_RUNTIME" "$BUNDLE_DIR/microwakeword/libonnxruntime.so"
install -m 0644 "$MODEL" "$BUNDLE_DIR/microwakeword/hey_tater.tflite"
install -m 0644 "$MANIFEST" "$BUNDLE_DIR/microwakeword/hey_tater.json"
install -m 0644 "$OWW_ONNX_MODEL" "$BUNDLE_DIR/microwakeword/hey_tater.oww.onnx"
install -m 0644 "$OWW_METADATA" "$BUNDLE_DIR/microwakeword/hey_tater.oww.json"
install -m 0644 "$WAKE_BUNDLE" "$BUNDLE_DIR/microwakeword/hey_tater.wake-bundle.json"
install -m 0644 "$STOP_MODEL" "$BUNDLE_DIR/microwakeword/stop.tflite"
install -m 0644 "$STOP_MANIFEST" "$BUNDLE_DIR/microwakeword/stop.json"
install -m 0644 "$OWW_MELSPEC_ONNX" "$BUNDLE_DIR/microwakeword/melspectrogram.onnx"
install -m 0644 "$OWW_EMBED_ONNX" "$BUNDLE_DIR/microwakeword/embedding_model.onnx"
install -m 0600 "$DEVICE_DIR/tater-native.example.json" "$BUNDLE_DIR/native.example.json"

(
  cd "$BUNDLE_DIR"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum firmware/server microwakeword/* native.example.json > SHA256SUMS
  else
    shasum -a 256 firmware/server microwakeword/* native.example.json > SHA256SUMS
  fi
)
tar -C "$(dirname "$BUNDLE_DIR")" -czf "$BUNDLE_DIR.tar.gz" "$(basename "$BUNDLE_DIR")"

echo "Tater native Echo test bundle:"
echo "  $BUNDLE_DIR"
echo "  $BUNDLE_DIR.tar.gz"
