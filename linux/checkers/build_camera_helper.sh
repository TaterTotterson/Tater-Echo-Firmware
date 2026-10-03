#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
    echo "usage: $0 <techo5 checkout> <output>" >&2
    exit 2
fi

checkout=$1
output_dir=$(dirname "$2")
mkdir -p "$output_dir"
output=$(cd "$output_dir" && pwd)/$(basename "$2")
expected=0f042360fbc22d798930cfd683d251606bfb7412
actual=$(git -C "$checkout" rev-parse HEAD)
[ "$actual" = "$expected" ] || {
    echo "TECHO5 checkout is $actual, expected $expected" >&2
    exit 1
}

command_dir=$checkout/echod/cmd/tater-checkers-camera
mkdir -p "$command_dir"
cp "$(dirname "$0")/camera-helper/main.go" "$command_dir/main.go"
(
    cd "$checkout/echod"
    GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build \
        -trimpath -ldflags '-s -w' -o "$output" ./cmd/tater-checkers-camera
)
