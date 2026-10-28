#!/bin/sh
set -eu

usage() {
    echo "usage: $0 --version vX.Y.Z --target checkers|crown|athena --base-rootfs FILE --platform DIR --ffmpeg FILE --ffmpeg-license FILE [--output FILE]" >&2
    exit 2
}

version=
target=checkers
base_rootfs=
platform_source=
ffmpeg=
ffmpeg_license=
output=
while [ "$#" -gt 0 ]; do
    case "$1" in
        --version) [ "$#" -ge 2 ] || usage; version=$2; shift 2 ;;
        --target) [ "$#" -ge 2 ] || usage; target=$2; shift 2 ;;
        --base-rootfs) [ "$#" -ge 2 ] || usage; base_rootfs=$2; shift 2 ;;
        --platform|--techo5) [ "$#" -ge 2 ] || usage; platform_source=$2; shift 2 ;;
        --ffmpeg) [ "$#" -ge 2 ] || usage; ffmpeg=$2; shift 2 ;;
        --ffmpeg-license) [ "$#" -ge 2 ] || usage; ffmpeg_license=$2; shift 2 ;;
        --output) [ "$#" -ge 2 ] || usage; output=$2; shift 2 ;;
        *) usage ;;
    esac
done
[ -n "$version" ] && [ -n "$base_rootfs" ] && [ -n "$platform_source" ] && [ -n "$ffmpeg" ] && [ -n "$ffmpeg_license" ] || usage

case "$target" in
    checkers|crown|athena) ;;
    *) echo "target must be checkers, crown, or athena" >&2; exit 2 ;;
esac

case "$version" in
    v[0-9]*.[0-9]*.[0-9]*) ;;
    *) echo "version must look like v2.0.0" >&2; exit 2 ;;
esac

script_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
repo=$(CDPATH= cd -- "$script_dir/../.." && pwd)
device=$repo/device
base_rootfs=$(CDPATH= cd -- "$(dirname "$base_rootfs")" && pwd)/$(basename "$base_rootfs")
platform_source=$(CDPATH= cd -- "$platform_source" && pwd)
ffmpeg=$(CDPATH= cd -- "$(dirname "$ffmpeg")" && pwd)/$(basename "$ffmpeg")
ffmpeg_license=$(CDPATH= cd -- "$(dirname "$ffmpeg_license")" && pwd)/$(basename "$ffmpeg_license")
[ -f "$ffmpeg_license" ] || { echo "pinned FFmpeg license is missing: $ffmpeg_license" >&2; exit 1; }
output=${output:-$device/build/tater-$target-rootfs-$version.tar.gz}
output_dir=$(dirname "$output")
mkdir -p "$device/build" "$output_dir"
packaged_ffmpeg=$device/build/tater-ffmpeg
if [ "$ffmpeg" != "$packaged_ffmpeg" ]; then
    cp "$ffmpeg" "$packaged_ffmpeg"
fi
chmod 755 "$packaged_ffmpeg"
packaged_ffmpeg_license=$device/build/FFMPEG-LGPL-2.1.txt
if [ "$ffmpeg_license" != "$packaged_ffmpeg_license" ]; then
    cp "$ffmpeg_license" "$packaged_ffmpeg_license"
fi
output=$(CDPATH= cd -- "$output_dir" && pwd)/$(basename "$output")

expected_platform=$(python3 "$repo/tools/platform_lock.py" get platforms.show.checkers_commit)
[ "$target" = athena ] && \
    expected_platform=$(python3 "$repo/tools/platform_lock.py" get platforms.athena.commit)
actual_platform=$(git -C "$platform_source" rev-parse HEAD)
[ "$actual_platform" = "$expected_platform" ] || {
    echo "Tater Echo Linux Show checkout is $actual_platform, expected $expected_platform" >&2
    exit 1
}

image=${TATER_FIRMWARE_BUILDER_IMAGE:-$(python3 "$repo/tools/platform_lock.py" builder-reference)}
source_date_epoch=${SOURCE_DATE_EPOCH:-$(git -C "$repo" show -s --format=%ct HEAD)}
cache_root=${TATER_BUILD_CACHE:-/tmp/tater-$target-build-cache}
mkdir -p "$cache_root/go-build" "$cache_root/go-mod"

"$device/prepare_onnxruntime_linux_armv7.sh"

docker run --rm --platform linux/arm/v7 \
    -e TATER_VERSION="$version" \
    -e TATER_TARGET="$target" \
    -e GOTOOLCHAIN=local \
    -e GOFLAGS=-buildvcs=false \
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
  -o build/tater-echo ./cmd/
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
  go build -trimpath -ldflags "-s -w -X main.version=$TATER_VERSION -X main.target=$TATER_TARGET" \
  -o build/tater-show-linux ./cmd/tater-show-linux
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
  go build -trimpath -ldflags "-s -w" \
  -o build/tater-reboot-now ./cmd/tater-reboot-now
tiny=$(readlink -f /usr/lib/libtinyalsa.so.2)
mkdir -p build/tater-linux-libs
cp "$tiny" build/tater-linux-libs/libtinyalsa.so.2.0.0
'

"$script_dir/build_camera_helper.sh" "$platform_source" "$device/build/tater-camera"
python3 "$script_dir/build_rescue_fbprobe.py" "$platform_source" "$device/build/tater-rescue-fbprobe"
kernel_args=
if [ "$target" = athena ]; then
    kernel_image=$repo/linux/athena/inputs/tater-echo-athena-kernel-boot.img
    [ -f "$kernel_image" ] || { echo "pinned Athena boot image is missing: $kernel_image" >&2; exit 1; }
    (
        cd "$platform_source/echod"
        GOTOOLCHAIN=local GOFLAGS=-buildvcs=false \
            GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build \
            -trimpath -ldflags '-s -w' \
            -o "$device/build/tater-kernelctl" ./cmd/echod
    )
    kernel_args="--kernelctl=$device/build/tater-kernelctl --kernel-image=$kernel_image"
fi

for required in \
    "$device/internal/wakeword/microwakeword/models/hey_tater.tflite" \
    "$device/internal/wakeword/microwakeword/models/hey_tater.oww.onnx" \
    "$device/internal/wakeword/microwakeword/models/hey_tater.oww.json" \
    "$device/internal/wakeword/microwakeword/models/hey_tater.nww.cnn.onnx" \
    "$device/internal/wakeword/microwakeword/models/hey_tater.nww.gru.onnx" \
    "$device/internal/wakeword/microwakeword/models/hey_tater.wake-bundle.json" \
    "$device/build/microwakeword-testdata/stop.tflite" \
    "$device/build/microwakeword-testdata/melspectrogram.onnx" \
    "$device/build/microwakeword-testdata/embedding_model.onnx"; do
    [ -f "$required" ] || { echo "required model is missing: $required" >&2; exit 1; }
done

python3 "$script_dir/build_rootfs.py" \
    --target "$target" \
    --base-rootfs "$base_rootfs" \
    --server "$device/build/tater-echo" \
    --show "$device/build/tater-show-linux" \
    --reboot "$device/build/tater-reboot-now" \
    --camera "$device/build/tater-camera" \
    --ffmpeg "$packaged_ffmpeg" \
    $kernel_args \
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
    --tinyalsa "$device/build/tater-linux-libs/libtinyalsa.so.2.0.0" \
    --techo5-license "$platform_source/LICENSE" \
    --ffmpeg-license "$ffmpeg_license" \
    --apk-cache "$device/build" \
    --version "$version" \
    --output "$output"

listing=$(mktemp "${TMPDIR:-/tmp}/tater-$target-rootfs.XXXXXX")
trap 'rm -f "$listing"' EXIT HUP INT TERM
tar -tzf "$output" > "$listing"
for required in \
    ./usr/local/sbin/tater-usb-ethernet \
    ./usr/local/sbin/tater-usb-inventory \
    ./usr/local/sbin/tater-sshd \
    ./usr/local/sbin/tater-ssh-authorize \
    ./usr/share/licenses/tater-linux/FFMPEG-LGPL-2.1.txt \
    ./usr/sbin/dropbear \
    ./usr/bin/dropbearkey; do
    grep -qx "$required" "$listing" || {
        echo "built $target rootfs is missing $required" >&2
        exit 1
    }
done
grep -qx './usr/local/bin/tater-ffmpeg' "$listing" || {
    echo "built $target rootfs is missing the video decoder" >&2
    exit 1
}
if [ "$target" = athena ]; then
    for required in \
        ./usr/local/bin/tater-kernelctl \
        ./usr/share/techo5/kernel/boot-athena.img \
        ./usr/share/techo5/kernel/boot-athena.img.sha256; do
        grep -qx "$required" "$listing" || {
            echo "built Athena rootfs is missing $required" >&2
            exit 1
        }
    done
fi

file "$device/build/tater-echo" "$device/build/tater-show-linux" "$device/build/tater-reboot-now" \
    "$device/build/tater-camera" "$device/build/tater-rescue-fbprobe" \
    "$packaged_ffmpeg" \
    "$device/build/microwakeword-linux/libtater_microwakeword.so" \
    "$device/build/onnxruntime-linux-armv7/libonnxruntime.so"
file "$device/build/tater-echo" | grep -q '32-bit.*ARM'
file "$device/build/tater-show-linux" | grep -q '32-bit.*ARM'
file "$device/build/tater-reboot-now" | grep -q '32-bit.*ARM'
file "$device/build/tater-camera" | grep -q '32-bit.*ARM'
file "$device/build/tater-rescue-fbprobe" | grep -q '32-bit.*ARM'
[ "$target" != athena ] || file "$device/build/tater-kernelctl" | grep -q '32-bit.*ARM'
file "$packaged_ffmpeg" | grep -q '32-bit.*ARM'
file "$device/build/onnxruntime-linux-armv7/libonnxruntime.so" | grep -q '32-bit.*ARM'
strings "$device/build/tater-echo" | grep -qF "$version"
strings "$device/build/tater-echo" | grep -qF "$target"
runtime_sha=$(sha256sum "$device/build/microwakeword-linux/libtater_microwakeword.so" | awk '{print $1}')
strings "$device/build/tater-echo" | grep -qF "$runtime_sha"
ort_sha=$(sha256sum "$device/build/onnxruntime-linux-armv7/libonnxruntime.so" | awk '{print $1}')
strings "$device/build/tater-echo" | grep -qF "$ort_sha"
echo "$target Linux rootfs: $output"
