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
actual=$(git -C "$checkout" rev-parse HEAD)
checkers_commit=$(python3 "$repo/tools/platform_lock.py" get platforms.show.checkers_commit)
athena_commit=$(python3 "$repo/tools/platform_lock.py" get platforms.athena.commit)
case "$actual" in
    "$checkers_commit"|"$athena_commit") ;;
    *)
        echo "Tater Echo Linux Show checkout is $actual, not a pinned Checkers/Crown or Athena platform" >&2
        exit 1
        ;;
esac

command_dir=$checkout/echod/cmd/tater-checkers-camera
mkdir -p "$command_dir"
cp "$script_dir/camera-helper/main.go" "$command_dir/main.go"
cp "$script_dir/camera-helper/main_test.go" "$command_dir/main_test.go"
cp "$script_dir/camera-helper/h264.go" "$command_dir/h264.go"
cp "$script_dir/camera-helper/h264_athena.go" "$command_dir/h264_athena.go"
cp "$script_dir/camera-helper/h264_athena_test.go" "$command_dir/h264_athena_test.go"
cp "$script_dir/camera-helper/h264_test.go" "$command_dir/h264_test.go"
cp "$script_dir/camera-helper/h264_cadence.go" "$command_dir/h264_cadence.go"
cp "$script_dir/camera-helper/h264_cadence_test.go" "$command_dir/h264_cadence_test.go"
cp "$script_dir/camera-helper/h264_lifecycle.go" "$command_dir/h264_lifecycle.go"
cp "$script_dir/camera-helper/h264_lifecycle_test.go" "$command_dir/h264_lifecycle_test.go"
camera_dir=$checkout/echod/internal/hardware/camera
cp "$script_dir/camera-helper/tater_encoder_input_show.go" "$camera_dir/tater_encoder_input_show.go"
cp "$script_dir/camera-helper/tater_video_show.go" "$camera_dir/tater_video_show.go"
cp "$script_dir/camera-helper/tater_athena_nv12_show.go" "$camera_dir/tater_athena_nv12_show.go"
cp "$script_dir/camera-helper/tater_athena_nv12_show_test.go" "$camera_dir/tater_athena_nv12_show_test.go"
cp "$script_dir/camera-helper/tater_video_show_test.go" "$camera_dir/tater_video_show_test.go"
cp "$script_dir/camera-helper/tater_capture_bench_test.go" "$camera_dir/tater_capture_bench_test.go"
cp "$script_dir/camera-helper/tater_camera_timing_show.go" "$camera_dir/tater_camera_timing_show.go"
cp "$script_dir/camera-helper/tater_live_exposure_show.go" "$camera_dir/tater_live_exposure_show.go"
cp "$script_dir/camera-helper/tater_live_tone_show.go" "$camera_dir/tater_live_tone_show.go"
cp "$script_dir/camera-helper/tater_capture_slots_show.go" "$camera_dir/tater_capture_slots_show.go"
cp "$script_dir/camera-helper/tater_capture_slots_show_test.go" "$camera_dir/tater_capture_slots_show_test.go"
# Narrow platform hooks only. Refuse conflicting local edits, and accept
# already-applied hooks on rebuild without applying them twice.
for hook in live_exposure held_slots; do
    if git -C "$checkout" apply --check "$script_dir/camera-helper/$hook.patch" 2>/dev/null; then
        git -C "$checkout" apply "$script_dir/camera-helper/$hook.patch"
    elif ! git -C "$checkout" apply --reverse --check "$script_dir/camera-helper/$hook.patch" 2>/dev/null; then
        echo "Camera $hook hook conflicts with the pinned platform source" >&2
        exit 1
    fi
done
(
    cd "$checkout/echod"
    GOTOOLCHAIN=local GOFLAGS=-buildvcs=false \
        GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build \
        -trimpath -ldflags '-s -w' -o "$output" ./cmd/tater-checkers-camera
)
