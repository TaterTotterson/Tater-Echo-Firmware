#!/bin/sh
set -eu

usage() {
    echo "usage: $0 --version vX.Y.Z --base-rootfs FILE --techo5 DIR [--output FILE]" >&2
    exit 2
}

version=
base_rootfs=
techo5=
output=
while [ "$#" -gt 0 ]; do
    case "$1" in
        --version) [ "$#" -ge 2 ] || usage; version=$2; shift 2 ;;
        --base-rootfs) [ "$#" -ge 2 ] || usage; base_rootfs=$2; shift 2 ;;
        --techo5) [ "$#" -ge 2 ] || usage; techo5=$2; shift 2 ;;
        --output) [ "$#" -ge 2 ] || usage; output=$2; shift 2 ;;
        *) usage ;;
    esac
done
[ -n "$version" ] && [ -n "$base_rootfs" ] && [ -n "$techo5" ] || usage

case "$version" in
    v[0-9]*.[0-9]*.[0-9]*) ;;
    *) echo "version must look like v2.0.0" >&2; exit 2 ;;
esac

script_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
repo=$(CDPATH= cd -- "$script_dir/../.." && pwd)
device=$repo/device
base_rootfs=$(CDPATH= cd -- "$(dirname "$base_rootfs")" && pwd)/$(basename "$base_rootfs")
techo5=$(CDPATH= cd -- "$techo5" && pwd)
output=${output:-$device/build/tater-checkers-rootfs-$version.tar.gz}
output_dir=$(dirname "$output")
mkdir -p "$device/build" "$output_dir"
output=$(CDPATH= cd -- "$output_dir" && pwd)/$(basename "$output")

expected_techo5=0f042360fbc22d798930cfd683d251606bfb7412
actual_techo5=$(git -C "$techo5" rev-parse HEAD)
[ "$actual_techo5" = "$expected_techo5" ] || {
    echo "TECHO5 checkout is $actual_techo5, expected $expected_techo5" >&2
    exit 1
}

image=${TATER_ALPINE_ARMV7_IMAGE:-alpine:3.22}
cache_root=${TATER_BUILD_CACHE:-/tmp/tater-checkers-build-cache}
mkdir -p "$cache_root/go-build" "$cache_root/go-mod"

docker run --rm --platform linux/arm/v7 \
    -e TATER_VERSION="$version" \
    -e GOTOOLCHAIN=auto \
    -v "$repo:/src" \
    -v "$cache_root/go-build:/root/.cache/go-build" \
    -v "$cache_root/go-mod:/root/go/pkg/mod" \
    -w /src/device "$image" sh -lc '
set -eu
apk add --no-cache build-base ca-certificates cmake go linux-headers ninja tinyalsa-dev >/dev/null
build_unix=$(date +%s)
ldflags="-s -w \
  -X github.com/TaterTotterson/Tater-Echo-Firmware/internal/client.Version=$TATER_VERSION \
  -X github.com/TaterTotterson/Tater-Echo-Firmware/internal/client.FirmwareTarget=checkers \
  -X github.com/TaterTotterson/Tater-Echo-Firmware/internal/clock.BuildUnix=$build_unix"
CGO_ENABLED=1 GOOS=linux GOARCH=arm GOARM=7 \
  CGO_CFLAGS="-Wno-deprecated-declarations -Wno-null-dereference" \
  go build -trimpath -tags server -ldflags "$ldflags" \
  -o build/tater-echo ./cmd/
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
  go build -trimpath -ldflags "-s -w -X main.version=$TATER_VERSION" \
  -o build/tater-show-linux ./cmd/tater-show-linux
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
  go build -trimpath -ldflags "-s -w" \
  -o build/tater-reboot-now ./cmd/tater-reboot-now
cmake -S internal/wakeword/microwakeword/native \
  -B build/microwakeword-linux -G Ninja \
  -DCMAKE_BUILD_TYPE=Release -DTATER_MWW_BUILD_SMOKE_TEST=OFF
cmake --build build/microwakeword-linux --parallel 2
strip build/microwakeword-linux/libtater_microwakeword.so
tiny=$(readlink -f /usr/lib/libtinyalsa.so.2)
mkdir -p build/tater-linux-libs
cp "$tiny" build/tater-linux-libs/libtinyalsa.so.2.0.0
'

"$script_dir/build_camera_helper.sh" "$techo5" "$device/build/tater-camera"
python3 "$script_dir/build_rescue_fbprobe.py" "$techo5" "$device/build/tater-rescue-fbprobe"

for required in \
    "$device/build/microwakeword-testdata/hey_tater.tflite" \
    "$device/build/microwakeword-testdata/stop.tflite"; do
    [ -f "$required" ] || { echo "required model is missing: $required" >&2; exit 1; }
done

python3 "$script_dir/build_rootfs.py" \
    --base-rootfs "$base_rootfs" \
    --server "$device/build/tater-echo" \
    --show "$device/build/tater-show-linux" \
    --reboot "$device/build/tater-reboot-now" \
    --camera "$device/build/tater-camera" \
    --mww-runtime "$device/build/microwakeword-linux/libtater_microwakeword.so" \
    --hey-tater-model "$device/build/microwakeword-testdata/hey_tater.tflite" \
    --hey-tater-manifest "$device/internal/wakeword/microwakeword/models/hey_tater.json" \
    --stop-model "$device/build/microwakeword-testdata/stop.tflite" \
    --stop-manifest "$device/internal/wakeword/microwakeword/models/stop.json" \
    --tinyalsa "$device/build/tater-linux-libs/libtinyalsa.so.2.0.0" \
    --techo5-license "$techo5/LICENSE" \
    --apk-cache "$device/build" \
    --version "$version" \
    --output "$output"

file "$device/build/tater-echo" "$device/build/tater-show-linux" "$device/build/tater-reboot-now" \
    "$device/build/tater-camera" "$device/build/tater-rescue-fbprobe" \
    "$device/build/microwakeword-linux/libtater_microwakeword.so"
file "$device/build/tater-echo" | grep -q '32-bit.*ARM'
file "$device/build/tater-show-linux" | grep -q '32-bit.*ARM'
file "$device/build/tater-reboot-now" | grep -q '32-bit.*ARM'
file "$device/build/tater-camera" | grep -q '32-bit.*ARM'
file "$device/build/tater-rescue-fbprobe" | grep -q '32-bit.*ARM'
strings "$device/build/tater-echo" | grep -qF "$version"
strings "$device/build/tater-echo" | grep -qF checkers
echo "Checkers Linux rootfs: $output"
