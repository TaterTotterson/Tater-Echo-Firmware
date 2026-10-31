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
camera_dir=$checkout/echod/internal/hardware/camera
cp "$script_dir/camera-helper/tater_video_show.go" "$camera_dir/tater_video_show.go"
cp "$script_dir/camera-helper/tater_video_show_test.go" "$camera_dir/tater_video_show_test.go"
(
    cd "$checkout/echod"
    GOTOOLCHAIN=local GOFLAGS=-buildvcs=false \
        GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build \
        -trimpath -ldflags '-s -w' -o "$output" ./cmd/tater-checkers-camera
)
