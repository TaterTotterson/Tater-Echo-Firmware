#!/bin/sh
set -eu

usage() {
    echo "usage: $0 --version vX.Y.Z --target biscuit|radar --base-rootfs FILE --platform DIR [--output FILE]" >&2
    exit 2
}

version=
target=
base_rootfs=
platform_source=
output=
while [ "$#" -gt 0 ]; do
    case "$1" in
        --version) [ "$#" -ge 2 ] || usage; version=$2; shift 2 ;;
        --target) [ "$#" -ge 2 ] || usage; target=$2; shift 2 ;;
        --base-rootfs) [ "$#" -ge 2 ] || usage; base_rootfs=$2; shift 2 ;;
        --platform) [ "$#" -ge 2 ] || usage; platform_source=$2; shift 2 ;;
        --output) [ "$#" -ge 2 ] || usage; output=$2; shift 2 ;;
        *) usage ;;
    esac
done
[ -n "$version" ] && [ -n "$target" ] && [ -n "$base_rootfs" ] && [ -n "$platform_source" ] || usage
case "$target" in biscuit|radar) ;; *) echo "target must be biscuit or radar" >&2; exit 2 ;; esac
case "$version" in v[0-9]*.[0-9]*.[0-9]*) ;; *) echo "version must look like v2.0.0" >&2; exit 2 ;; esac

script_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
repo=$(CDPATH= cd -- "$script_dir/../.." && pwd)
device=$repo/device
base_rootfs=$(CDPATH= cd -- "$(dirname "$base_rootfs")" && pwd)/$(basename "$base_rootfs")
platform_source=$(CDPATH= cd -- "$platform_source" && pwd)
output=${output:-$device/build/tater-$target-rootfs-$version.tar.gz}
mkdir -p "$device/build" "$(dirname "$output")"

expected_platform=$(python3 "$repo/tools/platform_lock.py" get platforms.dot.commit)
actual_platform=$(git -C "$platform_source" rev-parse HEAD)
[ "$actual_platform" = "$expected_platform" ] || {
    echo "Tater Echo Linux Dot checkout is $actual_platform, expected $expected_platform" >&2
    exit 1
}

image=${TATER_FIRMWARE_BUILDER_IMAGE:-$(python3 "$repo/tools/platform_lock.py" builder-reference)}
source_date_epoch=${SOURCE_DATE_EPOCH:-$(git -C "$repo" show -s --format=%ct HEAD)}
cache_root=${TATER_BUILD_CACHE:-/tmp/tater-$target-linux-build-cache}
mkdir -p "$cache_root/go-build" "$cache_root/go-mod"

"$device/prepare_onnxruntime_linux_armv7.sh"

docker run --rm --platform linux/arm/v7 \
    -e TATER_VERSION="$version" -e TATER_TARGET="$target" \
    -e GOTOOLCHAIN=local -e GOFLAGS=-buildvcs=false \
    -e SOURCE_DATE_EPOCH="$source_date_epoch" \
    -v "$repo:/src" \
    -v "$cache_root/go-build:/root/.cache/go-build" \
    -v "$cache_root/go-mod:/root/go/pkg/mod" \
    -w /src/device "$image" sh -lc '
set -eu
cmake -S internal/wakeword/microwakeword/native \
  -B build/microwakeword-linux -G Ninja \
  -DCMAKE_BUILD_TYPE=Release -DTATER_MWW_BUILD_SMOKE_TEST=OFF
cmake --build build/microwakeword-linux --parallel 2
strip build/microwakeword-linux/libtater_microwakeword.so
runtime_sha=$(sha256sum build/microwakeword-linux/libtater_microwakeword.so | awk "{print \$1}")
ort_sha=$(sha256sum build/onnxruntime-linux-armv7/libonnxruntime.so | awk "{print \$1}")
smoke=build/onnxruntime-linux-armv7/smoke
mkdir -p "$smoke"
cp build/onnxruntime-linux-armv7/libonnxruntime.so "$smoke/libonnxruntime.so"
cp build/microwakeword-testdata/melspectrogram.onnx "$smoke/melspectrogram.onnx"
cp build/microwakeword-testdata/embedding_model.onnx "$smoke/embedding_model.onnx"
cp internal/wakeword/microwakeword/models/hey_tater.oww.onnx "$smoke/hey_tater.oww.onnx"
cp internal/wakeword/microwakeword/models/hey_tater.nww.cnn.onnx "$smoke/hey_tater.nww.cnn.onnx"
cp internal/wakeword/microwakeword/models/hey_tater.nww.gru.onnx "$smoke/hey_tater.nww.gru.onnx"
TATER_ORT_SMOKE_DIR=/src/device/$smoke \
  go test -count=1 -tags onnxruntime -run "^TestLinuxARMv7ORTModels$" \
  ./internal/wakeword/microwakeword
build_unix=$SOURCE_DATE_EPOCH
ldflags="-s -w \
  -X github.com/TaterTotterson/Tater-Echo-Firmware/internal/client.Version=$TATER_VERSION \
  -X github.com/TaterTotterson/Tater-Echo-Firmware/internal/client.FirmwareTarget=$TATER_TARGET \
  -X github.com/TaterTotterson/Tater-Echo-Firmware/internal/clock.BuildUnix=$build_unix \
  -X github.com/TaterTotterson/Tater-Echo-Firmware/internal/wakeword/microwakeword.ReleaseRuntimeSHA256=$runtime_sha \
  -X github.com/TaterTotterson/Tater-Echo-Firmware/internal/wakeword/microwakeword.ReleaseORTRuntimeSHA256=$ort_sha"
CGO_ENABLED=1 GOOS=linux GOARCH=arm GOARM=7 \
  CGO_CFLAGS="-Wno-deprecated-declarations -Wno-null-dereference" \
  go build -trimpath -tags server,onnxruntime -ldflags "$ldflags" \
  -o build/dot/tater-echo ./cmd/
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
  go build -trimpath -ldflags "-s -w" \
  -o build/dot/tater-reboot-now ./cmd/tater-reboot-now
tiny=$(readlink -f /usr/lib/libtinyalsa.so.2)
mkdir -p build/dot
cp "$tiny" build/dot/libtinyalsa.so.2.0.0
'

for required in \
    "$device/internal/wakeword/microwakeword/models/hey_tater.tflite" \
    "$device/internal/wakeword/microwakeword/models/hey_tater.json" \
    "$device/internal/wakeword/microwakeword/models/hey_tater.oww.onnx" \
    "$device/internal/wakeword/microwakeword/models/hey_tater.oww.json" \
    "$device/internal/wakeword/microwakeword/models/hey_tater.nww.cnn.onnx" \
    "$device/internal/wakeword/microwakeword/models/hey_tater.nww.gru.onnx" \
    "$device/internal/wakeword/microwakeword/models/hey_tater.wake-bundle.json" \
    "$device/build/microwakeword-testdata/stop.tflite" \
    "$device/internal/wakeword/microwakeword/models/stop.json" \
    "$device/build/microwakeword-testdata/melspectrogram.onnx" \
    "$device/build/microwakeword-testdata/embedding_model.onnx"; do
    [ -f "$required" ] || { echo "required model is missing: $required" >&2; exit 1; }
done

python3 "$script_dir/build_rootfs.py" \
    --target "$target" --base-rootfs "$base_rootfs" \
    --server "$device/build/dot/tater-echo" \
    --reboot "$device/build/dot/tater-reboot-now" \
    --mww-runtime "$device/build/microwakeword-linux/libtater_microwakeword.so" \
    --onnx-runtime "$device/build/onnxruntime-linux-armv7/libonnxruntime.so" \
    --hey-tater-model "$device/internal/wakeword/microwakeword/models/hey_tater.tflite" \
    --hey-tater-manifest "$device/internal/wakeword/microwakeword/models/hey_tater.json" \
    --hey-tater-oww-onnx "$device/internal/wakeword/microwakeword/models/hey_tater.oww.onnx" \
    --hey-tater-oww-metadata "$device/internal/wakeword/microwakeword/models/hey_tater.oww.json" \
    --hey-tater-nww-cnn "$device/internal/wakeword/microwakeword/models/hey_tater.nww.cnn.onnx" \
    --hey-tater-nww-gru "$device/internal/wakeword/microwakeword/models/hey_tater.nww.gru.onnx" \
    --hey-tater-bundle "$device/internal/wakeword/microwakeword/models/hey_tater.wake-bundle.json" \
    --stop-model "$device/build/microwakeword-testdata/stop.tflite" \
    --stop-manifest "$device/internal/wakeword/microwakeword/models/stop.json" \
    --oww-melspectrogram-onnx "$device/build/microwakeword-testdata/melspectrogram.onnx" \
    --oww-embedding-onnx "$device/build/microwakeword-testdata/embedding_model.onnx" \
    --tinyalsa "$device/build/dot/libtinyalsa.so.2.0.0" \
    --setup-ap "$script_dir/setup-ap.sh" \
    --setup-ap-base "$repo/linux/checkers/setup-ap.sh" \
    --network "$script_dir/tater-net" \
    --eth "$script_dir/tater-usb-ethernet" \
    --wifi-set "$script_dir/wifi-set" \
    --apk-cache "$device/build" \
    --platform-license "$platform_source/LICENSE" \
    --platform-notice "$platform_source/NOTICE" \
    --version "$version" --output "$output"

file "$device/build/dot/tater-echo" "$device/build/dot/tater-reboot-now" \
    "$device/build/microwakeword-linux/libtater_microwakeword.so" \
    "$device/build/onnxruntime-linux-armv7/libonnxruntime.so"
file "$device/build/dot/tater-echo" | grep -q '32-bit.*ARM'
strings "$device/build/dot/tater-echo" | grep -qF "$version"
strings "$device/build/dot/tater-echo" | grep -qF "$target"
listing=$(mktemp "${TMPDIR:-/tmp}/tater-$target-rootfs.XXXXXX")
trap 'rm -f "$listing"' EXIT HUP INT TERM
tar -tzf "$output" > "$listing"
for required in \
    ./usr/local/bin/tater-echo \
    ./usr/local/sbin/tater-run \
    ./usr/local/sbin/tater-net \
    ./usr/local/sbin/tater-usb-ethernet \
    ./usr/local/sbin/tater-usb-inventory \
    ./usr/local/sbin/techo5-net \
    ./usr/local/sbin/techo5-eth \
    ./usr/local/sbin/wifi-set \
    ./usr/local/sbin/tater-setup-ap \
    ./usr/sbin/hostapd \
    ./usr/sbin/dnsmasq \
    ./usr/sbin/iw \
    ./usr/bin/aplay \
    ./usr/bin/arecord \
    ./usr/lib/libstdc++.so.6 \
    ./usr/lib/libstdc++.so.6.0.34; do
    grep -qx "$required" "$listing" || {
        echo "built rootfs is missing $required" >&2
        exit 1
    }
done
for retired in ./usr/local/bin/echod ./usr/local/bin/techo5-librespot ./usr/local/sbin/techo5-run; do
    if grep -qx "$retired" "$listing"; then
        echo "built rootfs still contains retired application $retired" >&2
        exit 1
    fi
done
rm -f "$listing"
echo "$target Tater Linux rootfs: $output"
