#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
    echo "usage: $0 <Tater Echo Linux Show checkout> <output>" >&2
    exit 2
fi

checkout=$1
script_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
repo=$(CDPATH= cd -- "$script_dir/../.." && pwd)
output_dir=$(dirname "$2")
mkdir -p "$output_dir"
output=$(cd "$output_dir" && pwd)/$(basename "$2")
expected=$(python3 "$repo/tools/platform_lock.py" get platforms.show.checkers_commit)
actual=$(git -C "$checkout" rev-parse HEAD)
[ "$actual" = "$expected" ] || {
    echo "Tater Echo Linux Show checkout is $actual, expected $expected" >&2
    exit 1
}

command_dir=$checkout/echod/cmd/tater-rook-camera
mkdir -p "$command_dir"
cp "$script_dir/camera-helper/main.go" "$command_dir/main.go"
cp "$script_dir/camera-helper/main_test.go" "$command_dir/main_test.go"
for name in h264.go h264_athena.go h264_cadence.go h264_lifecycle.go; do
    cp "$repo/linux/checkers/camera-helper/$name" "$command_dir/$name"
done
camera_dir=$checkout/echod/internal/hardware/camera
cp "$script_dir/camera-helper/tater_video_spot.go" "$camera_dir/tater_video_spot.go"
cp "$script_dir/camera-helper/tater_video_spot_test.go" "$camera_dir/tater_video_spot_test.go"
cp "$script_dir/camera-helper/tater_encoder_input_spot.go" "$camera_dir/tater_encoder_input_spot.go"
cp "$script_dir/camera-helper/tater_encoder_input_spot_test.go" "$camera_dir/tater_encoder_input_spot_test.go"
cp "$script_dir/camera-helper/tater_capture_slots_spot.go" "$camera_dir/tater_capture_slots_spot.go"
cp "$script_dir/camera-helper/tater_capture_slots_spot_test.go" "$camera_dir/tater_capture_slots_spot_test.go"
cp "$script_dir/camera-helper/tater_live_exposure_spot.go" "$camera_dir/tater_live_exposure_spot.go"
cp "$script_dir/camera-helper/tater_live_exposure_spot_test.go" "$camera_dir/tater_live_exposure_spot_test.go"

# TECHO5 normally develops every second sensor frame because its standard path
# expands each 640x480 Bayer frame before scaling it. Tater's echo-low path
# develops directly to 320x180, so it is cheap enough to consume every sensor
# frame. Patch the pinned checkout only for this build and always put it back.
camera_driver=$camera_dir/camera_spot.go
driver_backup=$(mktemp "${TMPDIR:-/tmp}/tater-rook-camera.XXXXXX")
cp "$camera_driver" "$driver_backup"
restore_driver() {
    cp "$driver_backup" "$camera_driver"
    rm -f "$driver_backup"
}
trap restore_driver EXIT HUP INT TERM
matches=$(grep -c '^[[:space:]]*convertEvery = 2$' "$camera_driver" || true)
[ "$matches" -eq 1 ] || {
    echo "expected exactly one TECHO5 convertEvery = 2 setting, found $matches" >&2
    exit 1
}
sed 's/^\([[:space:]]*\)convertEvery = 2$/\1convertEvery = 1/' "$driver_backup" > "$camera_driver"
git -C "$checkout" apply --check "$script_dir/camera-helper/held_slots.patch"
git -C "$checkout" apply "$script_dir/camera-helper/held_slots.patch"
git -C "$checkout" apply --check "$script_dir/camera-helper/live_exposure.patch"
git -C "$checkout" apply "$script_dir/camera-helper/live_exposure.patch"
(
    cd "$checkout/echod"
    # Run tests against the same temporary driver hooks as the shipped binary.
    # This opt-in requires a Linux host with the race detector's C toolchain.
    if [ "${TATER_CAMERA_TEST:-0}" = 1 ]; then
        GOTOOLCHAIN=local GOFLAGS=-buildvcs=false go test -race -count=1 \
            -tags spot ./internal/hardware/camera ./cmd/tater-rook-camera
    fi
    GOTOOLCHAIN=local GOFLAGS=-buildvcs=false \
        GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build \
        -tags spot -trimpath -ldflags '-s -w' -o "$output" ./cmd/tater-rook-camera
)
