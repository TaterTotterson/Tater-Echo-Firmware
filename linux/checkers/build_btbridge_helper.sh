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
        echo "Tater Echo Linux Show checkout is $actual, not a pinned Show platform" >&2
        exit 1
        ;;
esac

# The rootfs boot script and btbridge must come from the same source commit.
# In particular, current Show policy invokes -p192-keys; an older baseline
# binary exits on that option and leaves Cronos without hci0.
(
    cd "$checkout"
    GOTOOLCHAIN=local GOFLAGS=-buildvcs=false \
        GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build \
        -trimpath -ldflags '-s -w' -o "$output" ./cmd/btbridge
)
