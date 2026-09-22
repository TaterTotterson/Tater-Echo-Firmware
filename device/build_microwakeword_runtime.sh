#!/bin/bash
set -euo pipefail

DEVICE_DIR=$(cd "$(dirname "$0")" && pwd)
NATIVE_DIR="internal/wakeword/microwakeword/native"
BUILDER_IMAGE="tater-echo-microwakeword-builder:ndk21.4"
BUILD_DIR="/sdk/build/microwakeword-android"
NDK_ROOT="/opt/android/ndk/21.4.7075529"
READELF="$NDK_ROOT/toolchains/llvm/prebuilt/linux-x86_64/bin/llvm-readelf"
NM="$NDK_ROOT/toolchains/llvm/prebuilt/linux-x86_64/bin/llvm-nm"

docker build \
  --platform linux/amd64 \
  -t "$BUILDER_IMAGE" \
  -f "$DEVICE_DIR/$NATIVE_DIR/Dockerfile.armv7" \
  "$DEVICE_DIR"

docker run --rm \
  --platform linux/amd64 \
  --entrypoint bash \
  -v "$DEVICE_DIR:/sdk" \
  "$BUILDER_IMAGE" \
  -lc "cmake -S /sdk/$NATIVE_DIR -B $BUILD_DIR -G Ninja \
        -DCMAKE_BUILD_TYPE=Release \
        -DCMAKE_TOOLCHAIN_FILE=$NDK_ROOT/build/cmake/android.toolchain.cmake \
        -DANDROID_ABI=armeabi-v7a \
        -DANDROID_PLATFORM=android-22 \
        -DANDROID_STL=c++_static \
        -DTATER_MWW_BUILD_SMOKE_TEST=OFF && \
       cmake --build $BUILD_DIR --parallel 2"

LIBRARY="$DEVICE_DIR/build/microwakeword-android/libtater_microwakeword.so"
if [[ ! -f "$LIBRARY" ]]; then
  echo "native build did not produce $LIBRARY" >&2
  exit 1
fi

docker run --rm \
  --platform linux/amd64 \
  --entrypoint bash \
  -v "$DEVICE_DIR:/sdk" \
  "$BUILDER_IMAGE" \
  -lc "set -euo pipefail
       library=$BUILD_DIR/libtater_microwakeword.so
       $READELF -h \"\$library\" | grep -q 'Class:.*ELF32'
       $READELF -h \"\$library\" | grep -q 'Machine:.*ARM'
       ! $READELF -d \"\$library\" | grep -q 'libc++_shared'
       exports=\$($NM -D --defined-only \"\$library\" | awk '{print \$3}' | grep '^tater_mww_' | sort)
       count=\$(printf '%s\n' \"\$exports\" | grep -c '^tater_mww_')
       test \"\$count\" -eq 10
       echo \"\$exports\""

echo
echo "Built ARMv7/API-22 runtime: $LIBRARY"
ls -lh "$LIBRARY"
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum "$LIBRARY"
else
  shasum -a 256 "$LIBRARY"
fi
