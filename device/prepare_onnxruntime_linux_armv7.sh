#!/bin/sh
set -eu

# Build the Linux/musl ARMv7 ONNX Runtime used by both Checkers and Rook.
# Microsoft does not publish a Linux ARM32 binary, so the release workflow
# builds a reduced, XNNPACK-enabled runtime from a hash-pinned source archive.

script_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
repo=$(CDPATH= cd -- "$script_dir/.." && pwd)
version=1.19.2
source_sha=870ee3772d265d1c7b0799ac7280071f8ecd55b11ee3f11868a3e79c7f4cab0f
eigen_commit=e7248b26a1ed53fa030c5c459f7ea095dfd276ac
eigen_sha=f9dd558b4e0c4b8cafdec90b902c1722d40cf6230a77d53c947af1cfa27d1afa
work=$script_dir/build/onnxruntime-linux-armv7
archive=$work/onnxruntime-v$version.tar.gz
source=$work/src
source_stamp=$source/.tater-source-sha256
eigen_archive=$work/eigen-$eigen_commit.tar.gz
eigen_source=$work/eigen
eigen_stamp=$eigen_source/.tater-source-sha256
output=$work/libonnxruntime.so
header_dir=$script_dir/build/onnxruntime/include
asset_dir=$script_dir/build/microwakeword-testdata
operators=$script_dir/onnxruntime/required_operators.config
image=${TATER_ALPINE_ARMV7_IMAGE:-alpine:3.22}
build_jobs=${TATER_ORT_BUILD_JOBS:-2}

sha256_file() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}

download_model() {
    name=$1
    digest=$2
    path=$asset_dir/$name
    if [ -s "$path" ] && [ "$(sha256_file "$path")" = "$digest" ]; then
        return
    fi
    partial=$path.partial
    rm -f "$partial"
    curl -fL "https://github.com/dscripka/openWakeWord/releases/download/v0.5.1/$name" -o "$partial"
    [ "$(sha256_file "$partial")" = "$digest" ] || {
        echo "downloaded $name failed SHA-256 verification" >&2
        exit 1
    }
    mv "$partial" "$path"
}

mkdir -p "$work" "$header_dir" "$asset_dir"
download_model melspectrogram.onnx ba2b0e0f8b7b875369a2c89cb13360ff53bac436f2895cced9f479fa65eb176f
download_model embedding_model.onnx 70d164290c1d095d1d4ee149bc5e00543250a7316b59f31d056cff7bd3075c1f

if [ -s "$output" ] && [ -s "$header_dir/onnxruntime_c_api.h" ]; then
    file "$output" | grep -q '32-bit.*ARM' || {
        echo "cached ONNX Runtime is not Linux ARMv7: $output" >&2
        exit 1
    }
    echo "Linux ARMv7 ONNX Runtime already prepared: $output"
    exit 0
fi

if [ ! -s "$archive" ] || [ "$(sha256_file "$archive")" != "$source_sha" ]; then
    partial=$archive.partial
    rm -f "$partial"
    curl -fL "https://github.com/microsoft/onnxruntime/archive/refs/tags/v$version.tar.gz" -o "$partial"
    [ "$(sha256_file "$partial")" = "$source_sha" ] || {
        echo "ONNX Runtime source archive failed SHA-256 verification" >&2
        exit 1
    }
    mv "$partial" "$archive"
fi

# ORT 1.19 uses Eigen APIs newer than Alpine's packaged 3.4.0 headers. Fetch
# the exact revision pinned by ORT and verify the archive ourselves. GitLab's
# generated archive changed bytes after ORT published its SHA-1, so relying on
# FetchContent's historical archive checksum is no longer reproducible.
if [ ! -s "$eigen_archive" ] || [ "$(sha256_file "$eigen_archive")" != "$eigen_sha" ]; then
    partial=$eigen_archive.partial
    rm -f "$partial"
    curl -fL "https://gitlab.com/libeigen/eigen/-/archive/$eigen_commit/eigen-$eigen_commit.tar.gz" \
        -o "$partial"
    [ "$(sha256_file "$partial")" = "$eigen_sha" ] || {
        echo "Eigen source archive failed SHA-256 verification" >&2
        exit 1
    }
    mv "$partial" "$eigen_archive"
fi

if [ ! -s "$eigen_source/Eigen/Core" ] || [ ! -s "$eigen_stamp" ] || \
        [ "$(cat "$eigen_stamp" 2>/dev/null || true)" != "$eigen_sha" ]; then
    rm -rf "$eigen_source"
    mkdir -p "$eigen_source"
    tar -xzf "$eigen_archive" --strip-components=1 -C "$eigen_source"
    echo "$eigen_sha" > "$eigen_stamp"
fi

if [ ! -s "$source/build.sh" ] || [ ! -s "$source_stamp" ] || \
        [ "$(cat "$source_stamp" 2>/dev/null || true)" != "$source_sha" ]; then
    rm -rf "$source"
    mkdir -p "$source"
    tar -xzf "$archive" --strip-components=1 -C "$source"
    echo "$source_sha" > "$source_stamp"
fi

docker run --rm --platform linux/arm/v7 \
    -e TATER_ORT_BUILD_JOBS="$build_jobs" \
    -v "$repo:/src" \
    -w /src/device/build/onnxruntime-linux-armv7/src \
    "$image" sh -lc '
set -eu
apk add --no-cache bash build-base cmake git linux-headers ninja python3 py3-pip py3-flatbuffers >/dev/null
./build.sh --allow_running_as_root --skip_submodule_sync --update --build --config MinSizeRel \
  --build_shared_lib --target onnxruntime --parallel "${TATER_ORT_BUILD_JOBS:-2}" \
  --skip_tests \
  --disable_rtti \
  --disable_ml_ops \
  --cmake_extra_defines CMAKE_CXX_FLAGS="-I/src/device/onnxruntime/musl-compat -Wno-psabi" \
  --include_ops_by_config /src/device/onnxruntime/required_operators.config \
  --use_preinstalled_eigen --eigen_path /src/device/build/onnxruntime-linux-armv7/eigen \
  --use_xnnpack
runtime=$(find build/Linux/MinSizeRel -maxdepth 1 -type f -name "libonnxruntime.so*" | sort | tail -n 1)
[ -n "$runtime" ]
strip "$runtime"
install -m 0755 "$runtime" /src/device/build/onnxruntime-linux-armv7/libonnxruntime.so
'

install -m 0644 "$source/include/onnxruntime/core/session/onnxruntime_c_api.h" \
    "$header_dir/onnxruntime_c_api.h"
file "$output" | grep -q '32-bit.*ARM' || {
    echo "built ONNX Runtime is not Linux ARMv7: $output" >&2
    exit 1
}
echo "Prepared Linux ARMv7 ONNX Runtime $version: $output"
