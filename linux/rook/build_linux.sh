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

docker run --rm --platform linux/arm/v7 \
    -e TATER_VERSION="$version" -e GOTOOLCHAIN=auto \
    -v "$repo:/src" \
    -v "$cache_root/go-build:/root/.cache/go-build" \
    -v "$cache_root/go-mod:/root/go/pkg/mod" \
    -w /src/device "$image" sh -lc '
set -eu
apk add --no-cache build-base ca-certificates cmake go linux-headers ninja tinyalsa-dev >/dev/null
build_unix=$(date +%s)
ldflags="-s -w \
  -X github.com/TaterTotterson/Tater-Echo-Firmware/internal/client.Version=$TATER_VERSION \
  -X github.com/TaterTotterson/Tater-Echo-Firmware/internal/client.FirmwareTarget=rook \
  -X github.com/TaterTotterson/Tater-Echo-Firmware/internal/clock.BuildUnix=$build_unix"
CGO_ENABLED=1 GOOS=linux GOARCH=arm GOARM=7 \
  CGO_CFLAGS="-Wno-deprecated-declarations -Wno-null-dereference" \
  go build -trimpath -tags server -ldflags "$ldflags" \
  -o build/rook/tater-echo ./cmd/
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
  go build -trimpath -ldflags "-s -w -X main.version=$TATER_VERSION" \
  -o build/rook/tater-show ./cmd/tater-show-linux
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
  go build -trimpath -ldflags "-s -w" \
  -o build/rook/tater-reboot-now ./cmd/tater-reboot-now
cmake -S internal/wakeword/microwakeword/native \
  -B build/rook/microwakeword -G Ninja \
  -DCMAKE_BUILD_TYPE=Release -DTATER_MWW_BUILD_SMOKE_TEST=OFF
cmake --build build/rook/microwakeword --parallel 2
strip build/rook/microwakeword/libtater_microwakeword.so
tiny=$(readlink -f /usr/lib/libtinyalsa.so.2)
cp "$tiny" build/rook/libtinyalsa.so.2.0.0
'

sh "$script_dir/build_camera_helper.sh" "$techo5" "$device/build/rook/tater-camera"

for required in \
    "$device/build/microwakeword-testdata/hey_tater.tflite" \
    "$device/build/microwakeword-testdata/stop.tflite"; do
    [ -f "$required" ] || { echo "required model is missing: $required" >&2; exit 1; }
done

python3 "$script_dir/build_rootfs.py" \
    --base-rootfs "$base_rootfs" \
    --server "$device/build/rook/tater-echo" \
    --show "$device/build/rook/tater-show" \
    --reboot "$device/build/rook/tater-reboot-now" \
    --camera "$device/build/rook/tater-camera" \
    --mww-runtime "$device/build/rook/microwakeword/libtater_microwakeword.so" \
    --hey-tater-model "$device/build/microwakeword-testdata/hey_tater.tflite" \
    --hey-tater-manifest "$device/internal/wakeword/microwakeword/models/hey_tater.json" \
    --stop-model "$device/build/microwakeword-testdata/stop.tflite" \
    --stop-manifest "$device/internal/wakeword/microwakeword/models/stop.json" \
    --tinyalsa "$device/build/rook/libtinyalsa.so.2.0.0" \
    --techo5-spot-license "$spot_source/LICENSE" \
    --techo5-license "$techo5/LICENSE" \
    --apk-cache "$device/build" \
    --version "$version" \
    --output "$output"

file "$device/build/rook/tater-echo" "$device/build/rook/tater-show" "$device/build/rook/tater-reboot-now" "$device/build/rook/tater-camera"
file "$device/build/rook/tater-echo" | grep -q '32-bit.*ARM'
file "$device/build/rook/tater-show" | grep -q '32-bit.*ARM'
file "$device/build/rook/tater-camera" | grep -q '32-bit.*ARM'
strings "$device/build/rook/tater-echo" | grep -qF "$version"
strings "$device/build/rook/tater-echo" | grep -qF rook
echo "Rook Linux rootfs: $output"
