#!/bin/sh
set -eu

usage() {
    echo "usage: $0 --version vX.Y.Z --base-rootfs FILE --spot-source DIR --techo5 DIR [--output FILE]" >&2
    exit 2
}

version=
base_rootfs=
spot_source=
techo5=
output=
while [ "$#" -gt 0 ]; do
    case "$1" in
        --version) [ "$#" -ge 2 ] || usage; version=$2; shift 2 ;;
        --base-rootfs) [ "$#" -ge 2 ] || usage; base_rootfs=$2; shift 2 ;;
        --spot-source) [ "$#" -ge 2 ] || usage; spot_source=$2; shift 2 ;;
        --techo5) [ "$#" -ge 2 ] || usage; techo5=$2; shift 2 ;;
        --output) [ "$#" -ge 2 ] || usage; output=$2; shift 2 ;;
        *) usage ;;
    esac
done
[ -n "$version" ] && [ -n "$base_rootfs" ] && [ -n "$spot_source" ] && [ -n "$techo5" ] || usage
case "$version" in
    v[0-9]*.[0-9]*.[0-9]*) ;;
    *) echo "version must look like v2.0.0" >&2; exit 2 ;;
esac

script_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
repo=$(CDPATH= cd -- "$script_dir/../.." && pwd)
device=$repo/device
base_rootfs=$(CDPATH= cd -- "$(dirname "$base_rootfs")" && pwd)/$(basename "$base_rootfs")
spot_source=$(CDPATH= cd -- "$spot_source" && pwd)
techo5=$(CDPATH= cd -- "$techo5" && pwd)
expected_spot=8253a4a1d52dd37dd152dd1d435b97e541e0a191
actual_spot=$(git -C "$spot_source" rev-parse HEAD)
[ "$actual_spot" = "$expected_spot" ] || {
    echo "TECHO5 Spot source is $actual_spot, expected $expected_spot" >&2
    exit 1
}
expected_techo5=0f042360fbc22d798930cfd683d251606bfb7412
actual_techo5=$(git -C "$techo5" rev-parse HEAD)
[ "$actual_techo5" = "$expected_techo5" ] || {
    echo "TECHO5 checkout is $actual_techo5, expected $expected_techo5" >&2
    exit 1
}

output=${output:-$device/build/rook/tater-rook-rootfs-$version.tar.gz}
output_dir=$(dirname "$output")
mkdir -p "$device/build/rook" "$output_dir"
output=$(CDPATH= cd -- "$output_dir" && pwd)/$(basename "$output")

image=${TATER_ALPINE_ARMV7_IMAGE:-alpine:3.22}
cache_root=${TATER_BUILD_CACHE:-/tmp/tater-rook-build-cache}
mkdir -p "$cache_root/go-build" "$cache_root/go-mod"

"$device/prepare_onnxruntime_linux_armv7.sh"

docker run --rm --platform linux/arm/v7 \
    -e TATER_VERSION="$version" -e GOTOOLCHAIN=auto \
    -v "$repo:/src" \
    -v "$cache_root/go-build:/root/.cache/go-build" \
    -v "$cache_root/go-mod:/root/go/pkg/mod" \
    -w /src/device "$image" sh -lc '
set -eu
apk add --no-cache build-base ca-certificates cmake go linux-headers ninja tinyalsa-dev >/dev/null
cmake -S internal/wakeword/microwakeword/native \
  -B build/rook/microwakeword -G Ninja \
  -DCMAKE_BUILD_TYPE=Release -DTATER_MWW_BUILD_SMOKE_TEST=OFF
cmake --build build/rook/microwakeword --parallel 2
strip build/rook/microwakeword/libtater_microwakeword.so
runtime_sha=$(sha256sum build/rook/microwakeword/libtater_microwakeword.so | awk "{print \$1}")
ort_sha=$(sha256sum build/onnxruntime-linux-armv7/libonnxruntime.so | awk "{print \$1}")
smoke=build/onnxruntime-linux-armv7/smoke
mkdir -p "$smoke"
cp build/onnxruntime-linux-armv7/libonnxruntime.so "$smoke/libonnxruntime.so"
cp build/microwakeword-testdata/melspectrogram.onnx "$smoke/melspectrogram.onnx"
cp build/microwakeword-testdata/embedding_model.onnx "$smoke/embedding_model.onnx"
cp internal/wakeword/microwakeword/models/hey_tater.oww.onnx "$smoke/hey_tater.oww.onnx"
TATER_ORT_SMOKE_DIR=/src/device/$smoke \
  go test -count=1 -tags onnxruntime -run "^TestLinuxARMv7ORTModels$" \
  ./internal/wakeword/microwakeword
build_unix=$(date +%s)
ldflags="-s -w \
  -X github.com/TaterTotterson/Tater-Echo-Firmware/internal/client.Version=$TATER_VERSION \
  -X github.com/TaterTotterson/Tater-Echo-Firmware/internal/client.FirmwareTarget=rook \
  -X github.com/TaterTotterson/Tater-Echo-Firmware/internal/clock.BuildUnix=$build_unix \
  -X github.com/TaterTotterson/Tater-Echo-Firmware/internal/wakeword/microwakeword.ReleaseRuntimeSHA256=$runtime_sha \
  -X github.com/TaterTotterson/Tater-Echo-Firmware/internal/wakeword/microwakeword.ReleaseORTRuntimeSHA256=$ort_sha"
CGO_ENABLED=1 GOOS=linux GOARCH=arm GOARM=7 \
  CGO_CFLAGS="-Wno-deprecated-declarations -Wno-null-dereference" \
  go build -trimpath -tags server,onnxruntime -ldflags "$ldflags" \
  -o build/rook/tater-echo ./cmd/
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
  go build -trimpath -ldflags "-s -w -X main.version=$TATER_VERSION" \
  -o build/rook/tater-show ./cmd/tater-show-linux
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
  go build -trimpath -ldflags "-s -w" \
  -o build/rook/tater-reboot-now ./cmd/tater-reboot-now
tiny=$(readlink -f /usr/lib/libtinyalsa.so.2)
cp "$tiny" build/rook/libtinyalsa.so.2.0.0
'

sh "$script_dir/build_camera_helper.sh" "$techo5" "$device/build/rook/tater-camera"

for required in \
    "$device/internal/wakeword/microwakeword/models/hey_tater.tflite" \
    "$device/internal/wakeword/microwakeword/models/hey_tater.oww.onnx" \
    "$device/internal/wakeword/microwakeword/models/hey_tater.oww.json" \
    "$device/internal/wakeword/microwakeword/models/hey_tater.wake-bundle.json" \
    "$device/build/microwakeword-testdata/stop.tflite" \
    "$device/build/microwakeword-testdata/melspectrogram.onnx" \
    "$device/build/microwakeword-testdata/embedding_model.onnx"; do
    [ -f "$required" ] || { echo "required model is missing: $required" >&2; exit 1; }
done

python3 "$script_dir/build_rootfs.py" \
    --base-rootfs "$base_rootfs" \
    --server "$device/build/rook/tater-echo" \
    --show "$device/build/rook/tater-show" \
    --reboot "$device/build/rook/tater-reboot-now" \
    --camera "$device/build/rook/tater-camera" \
    --mww-runtime "$device/build/rook/microwakeword/libtater_microwakeword.so" \
    --onnx-runtime "$device/build/onnxruntime-linux-armv7/libonnxruntime.so" \
    --hey-tater-model "$device/internal/wakeword/microwakeword/models/hey_tater.tflite" \
    --hey-tater-manifest "$device/internal/wakeword/microwakeword/models/hey_tater.json" \
    --hey-tater-oww-onnx "$device/internal/wakeword/microwakeword/models/hey_tater.oww.onnx" \
    --hey-tater-oww-metadata "$device/internal/wakeword/microwakeword/models/hey_tater.oww.json" \
    --hey-tater-bundle "$device/internal/wakeword/microwakeword/models/hey_tater.wake-bundle.json" \
    --stop-model "$device/build/microwakeword-testdata/stop.tflite" \
    --stop-manifest "$device/internal/wakeword/microwakeword/models/stop.json" \
    --oww-melspectrogram-onnx "$device/build/microwakeword-testdata/melspectrogram.onnx" \
    --oww-embedding-onnx "$device/build/microwakeword-testdata/embedding_model.onnx" \
    --tinyalsa "$device/build/rook/libtinyalsa.so.2.0.0" \
    --techo5-spot-license "$spot_source/LICENSE" \
    --techo5-license "$techo5/LICENSE" \
    --apk-cache "$device/build" \
    --version "$version" \
    --output "$output"

file "$device/build/rook/tater-echo" "$device/build/rook/tater-show" "$device/build/rook/tater-reboot-now" "$device/build/rook/tater-camera" \
    "$device/build/onnxruntime-linux-armv7/libonnxruntime.so"
file "$device/build/rook/tater-echo" | grep -q '32-bit.*ARM'
file "$device/build/rook/tater-show" | grep -q '32-bit.*ARM'
file "$device/build/rook/tater-camera" | grep -q '32-bit.*ARM'
file "$device/build/onnxruntime-linux-armv7/libonnxruntime.so" | grep -q '32-bit.*ARM'
strings "$device/build/rook/tater-echo" | grep -qF "$version"
strings "$device/build/rook/tater-echo" | grep -qF rook
runtime_sha=$(sha256sum "$device/build/rook/microwakeword/libtater_microwakeword.so" | awk '{print $1}')
strings "$device/build/rook/tater-echo" | grep -qF "$runtime_sha"
ort_sha=$(sha256sum "$device/build/onnxruntime-linux-armv7/libonnxruntime.so" | awk '{print $1}')
strings "$device/build/rook/tater-echo" | grep -qF "$ort_sha"
echo "Rook Linux rootfs: $output"
