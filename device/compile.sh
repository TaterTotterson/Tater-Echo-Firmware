#!/bin/bash
set -e
REPO_ROOT=$(git rev-parse --show-toplevel)
# Local test builds use the same semantic firmware version as the release they
# are based on. A dirty working tree is build provenance, not a newer firmware
# version; replacing it with a timestamp made OTA comparisons unreliable.
VERSION="${TATER_FIRMWARE_VERSION:-}"
if [ -z "$VERSION" ]; then
    VERSION=$(tr -d '[:space:]' < "$REPO_ROOT/VERSION")
fi
case "$VERSION" in
    v*) ;;
    *) VERSION="v$VERSION" ;;
esac
if ! echo "$VERSION" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+([+-][A-Za-z0-9.-]+)?$'; then
    echo "Invalid firmware version: $VERSION" >&2
    exit 1
fi
[ -n "$EM_EXTRA_TAGS" ] && VERSION="${VERSION}-${EM_EXTRA_TAGS// /-}"
echo "Building Tater Echo Firmware $VERSION..."

# Suppress known harmless warnings from vendored C sources:
#   -Wno-null-dereference: rnnoise/rnn.c assert-style null checks
#   -Wno-deprecated-declarations: tinyalsa pcm_read/pcm_write
SUPPRESS="-Wno-deprecated-declarations -Wno-null-dereference"

# Build explicitly with --entrypoint bash so we control ldflags directly.
# Previously we relied on the base image entrypoint to use $VERSION, which
# is opaque. This embeds the version string into the binary at compile time
# so the device reports the correct version to the controller on connect.
# clock.BuildUnix floors the TLS verification clock — an Echo can boot with a
# bogus date before NTP syncs, and cert NotBefore checks would otherwise
# strand it (see internal/client/tlscreds.go).
BUILD_UNIX=$(date +%s)
BUILD_CMD="cd /sdk && mkdir -p build && go build \
    -tags \"server ${EM_EXTRA_TAGS}\" \
    -ldflags \"-X github.com/TaterTotterson/Tater-Echo-Firmware/internal/client.Version=${VERSION} \
               -X github.com/TaterTotterson/Tater-Echo-Firmware/internal/clock.BuildUnix=${BUILD_UNIX}\" \
    -o build/server ./cmd/"

if docker run --rm \
  --entrypoint bash \
  -e CGO_LDFLAGS="-Wl,--hash-style=both" \
  -e CGO_CFLAGS="$SUPPRESS" \
  -v "$(pwd)":/sdk \
  -v "$REPO_ROOT/GoTinyAlsa":/GoTinyAlsa \
  echomuse-compiler \
  -c "$BUILD_CMD" 2>/tmp/build_err.log; then
    echo ""
    echo "✓ Build succeeded → build/server  ($VERSION)"
    echo ""
else
    echo ""
    echo "✗ Build failed:"
    echo ""
    cat /tmp/build_err.log
    echo ""
    exit 1
fi
