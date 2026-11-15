#!/usr/bin/env python3
"""Recreate Athena Mesa from pinned signed Alpine packages, off-device."""
from __future__ import annotations

import argparse
from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import tempfile
from urllib import request

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
from tools import athena_mesa, platform_lock  # noqa: E402

SOURCE_COMMIT = '23d62e186915dbe55dc9b426ac0f18aa8020700e'
SOURCES = {
    'scripts/prepare_athena_mesa_runtime.py': 'eb3157dcc982c28a0018c41d37017435e7b0298d76a679bb7bfe37329c145216',
    'probes/mt8163-gpu/athena-mesa-packages.json': '47a2897809587b022f6970ee05334c30e27b0392f63cb2b3c484bf74cdc9cf77',
}
PACKAGE_BASE = 'https://dl-cdn.alpinelinux.org/alpine/v3.21/main/armv7/'
MAX_PACKAGE = 128 * 1024 * 1024


def prepare(source: Path, packages: Path, output: Path) -> None:
    actual = subprocess.check_output(['git', '-C', str(source), 'rev-parse', 'HEAD'], text=True).strip()
    if actual != SOURCE_COMMIT:
        raise ValueError('Mesa preparer must come from the pinned hardware source commit')
    for relative, expected in SOURCES.items():
        path = source/relative
        if path.is_symlink() or not path.is_file() or hashlib.sha256(path.read_bytes()).hexdigest() != expected:
            raise ValueError('Mesa preparation source drift: '+relative)
    builder_lock = platform_lock.load()
    platform_lock.verify(builder_lock, require_builder_digest=True)
    builder = builder_lock['builder']['image']+'@'+builder_lock['builder']['platform_digests']['linux/arm/v7']
    subprocess.run([sys.executable, str(source/'scripts/prepare_athena_mesa_runtime.py'),
                    '--packages', str(packages.resolve()), '--image', builder,
                    '--output', str(output.resolve())], check=True)
    athena_mesa.verify(output)


def fetch_packages(source: Path, output: Path) -> None:
    path = source/'probes/mt8163-gpu/athena-mesa-packages.json'
    if path.is_symlink() or hashlib.sha256(path.read_bytes()).hexdigest() != SOURCES[path.relative_to(source).as_posix()]:
        raise ValueError('not the pinned package inventory')
    pins = json.loads(path.read_text())
    if output.exists() or output.is_symlink():
        raise ValueError('package output already exists')
    output.mkdir(parents=True)

    def fetch(item):
        name, expected = item
        if '/' in name or not name.endswith('.apk'):
            raise ValueError('unsafe package name')
        with request.urlopen(PACKAGE_BASE+name, timeout=60) as response:
            if not response.geturl().startswith('https://'):
                raise ValueError('package redirect must remain HTTPS')
            data = response.read(MAX_PACKAGE+1)
        if len(data) > MAX_PACKAGE or hashlib.sha256(data).hexdigest() != expected:
            raise ValueError('package hash/size mismatch: '+name)
        (output/name).write_bytes(data)

    with ThreadPoolExecutor(max_workers=4) as workers:
        list(workers.map(fetch, sorted(pins.items())))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source', required=True, type=Path)
    parser.add_argument('--packages', type=Path, help='optional exact local signed package set; otherwise download')
    parser.add_argument('--output', required=True, type=Path)
    args = parser.parse_args()
    try:
        if args.packages:
            prepare(args.source, args.packages, args.output)
        else:
            with tempfile.TemporaryDirectory(prefix='tater-mesa-packages-') as temporary:
                packages = Path(temporary)/'packages'
                fetch_packages(args.source, packages)
                prepare(args.source, packages, args.output)
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        parser.exit(1, f'refusing: {error}\n')


if __name__ == '__main__': main()
