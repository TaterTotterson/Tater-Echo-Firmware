#!/bin/bash
set -euo pipefail

DEVICE_DIR=$(cd "$(dirname "$0")" && pwd)
BUILD_DIR="$DEVICE_DIR/build/onnxruntime"
ASSET_DIR="$DEVICE_DIR/build/microwakeword-testdata"
AAR="$BUILD_DIR/onnxruntime-android-1.19.2.aar"
HEADER="$BUILD_DIR/include/onnxruntime_c_api.h"
RUNTIME="$BUILD_DIR/armeabi-v7a/libonnxruntime.so"

AAR_URL="https://repo1.maven.org/maven2/com/microsoft/onnxruntime/onnxruntime-android/1.19.2/onnxruntime-android-1.19.2.aar"
AAR_SHA="84737dd1fdb715f0ee695c0a9864f6949b66ecb4c46ca733bc9211bf0c2a7b5f"
HEADER_SHA="f4047359e0dbf2078fff0e88bfb806de3c2b8891a895ac0dffdc3dfcb8bb489b"
RUNTIME_SHA="174233cf1a3f841f1eac82a4328f4f23f8819d5c62969c09e994a6b1abf498c1"

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

mkdir -p "$BUILD_DIR/include" "$BUILD_DIR/armeabi-v7a" "$ASSET_DIR"
if [[ ! -f "$AAR" ]] || [[ "$(sha256_file "$AAR")" != "$AAR_SHA" ]]; then
  curl -fsSL "$AAR_URL" -o "$AAR.tmp"
  [[ "$(sha256_file "$AAR.tmp")" == "$AAR_SHA" ]]
  mv "$AAR.tmp" "$AAR"
fi
unzip -p "$AAR" headers/onnxruntime_c_api.h > "$HEADER.tmp"
unzip -p "$AAR" jni/armeabi-v7a/libonnxruntime.so > "$RUNTIME.tmp"
[[ "$(sha256_file "$HEADER.tmp")" == "$HEADER_SHA" ]]
[[ "$(sha256_file "$RUNTIME.tmp")" == "$RUNTIME_SHA" ]]
mv "$HEADER.tmp" "$HEADER"
mv "$RUNTIME.tmp" "$RUNTIME"
chmod 0755 "$RUNTIME"

download_model() {
  local name=$1
  local sha=$2
  local path="$ASSET_DIR/$name"
  if [[ -f "$path" ]] && [[ "$(sha256_file "$path")" == "$sha" ]]; then
    return
  fi
  curl -fsSL "https://github.com/dscripka/openWakeWord/releases/download/v0.5.1/$name" -o "$path.tmp"
  [[ "$(sha256_file "$path.tmp")" == "$sha" ]]
  mv "$path.tmp" "$path"
}

download_model melspectrogram.onnx ba2b0e0f8b7b875369a2c89cb13360ff53bac436f2895cced9f479fa65eb176f
download_model embedding_model.onnx 70d164290c1d095d1d4ee149bc5e00543250a7316b59f31d056cff7bd3075c1f

echo "Prepared ONNX Runtime 1.19.2 and openWakeWord ONNX feature models."
