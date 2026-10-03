#!/bin/bash
set -euo pipefail

DEVICE_DIR=$(cd "$(dirname "$0")" && pwd)
NATIVE_DIR="$DEVICE_DIR/internal/wakeword/microwakeword/native"
BUILD_DIR="$DEVICE_DIR/build/microwakeword-host"
GOLDEN_FILE="$NATIVE_DIR/testdata/hey_tater_integer_sweep_v1.golden"
MODEL_SHA="d3bf0d87c5c00ccfeda3cebba528c5d4012a5aaae51e61b7b01ae5af9008b4b9"
MODEL_REV="18f27f6086b4e7c5adfb9f884a80340e2e81970f"
MODEL_URL="https://raw.githubusercontent.com/TaterTotterson/Tater-Wake-Words"
MODEL_URL="$MODEL_URL/$MODEL_REV/microWakeWordsV6/hey_tater.tflite"

MODEL_PATH=${TATER_MWW_TEST_MODEL:-"$DEVICE_DIR/build/microwakeword-testdata/hey_tater.tflite"}
if [[ -z "${TATER_MWW_TEST_MODEL:-}" ]]; then
  mkdir -p "$(dirname "$MODEL_PATH")"
  if [[ ! -f "$MODEL_PATH" ]]; then
    curl -fL "$MODEL_URL" -o "$MODEL_PATH"
  fi
fi

if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL_SHA=$(sha256sum "$MODEL_PATH" | awk '{print $1}')
else
  ACTUAL_SHA=$(shasum -a 256 "$MODEL_PATH" | awk '{print $1}')
fi
if [[ "$ACTUAL_SHA" != "$MODEL_SHA" ]]; then
  echo "microWakeWord test model hash mismatch" >&2
  echo "expected: $MODEL_SHA" >&2
  echo "actual:   $ACTUAL_SHA" >&2
  exit 1
fi

cmake -S "$NATIVE_DIR" -B "$BUILD_DIR" -G Ninja \
  -DCMAKE_BUILD_TYPE=Release \
  -DTATER_MWW_TEST_MODEL="$MODEL_PATH" \
  -DTATER_MWW_GOLDEN_FILE="$GOLDEN_FILE"
cmake --build "$BUILD_DIR" --parallel
ctest --test-dir "$BUILD_DIR" --output-on-failure

case "$(uname -s)" in
  Darwin) RUNTIME="$BUILD_DIR/libtater_microwakeword.dylib" ;;
  Linux) RUNTIME="$BUILD_DIR/libtater_microwakeword.so" ;;
  *)
    echo "unsupported native test host: $(uname -s)" >&2
    exit 1
    ;;
esac

TATER_MWW_LIBRARY="$RUNTIME" \
TATER_MWW_MODEL="$MODEL_PATH" \
go test ./internal/wakeword/microwakeword
