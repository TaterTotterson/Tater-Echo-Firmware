#!/usr/bin/env python3
"""Assemble/verify Athena's original AP helpers/modules, never owner libraries."""
from __future__ import annotations
import argparse
import hashlib
import json
from pathlib import Path
import shutil

ROOT = Path(__file__).resolve().parents[1]
LOCK = ROOT / 'platforms/athena-codec-lock.json'
MANIFEST = 'tater-athena-codecs.json'


def digest(path):
    if path.is_symlink() or not path.is_file():
        raise ValueError('codec input must be an ordinary file')
    result = hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024*1024), b''):
            result.update(chunk)
    return result.hexdigest()


def load_lock():
    lock = json.loads(LOCK.read_text())
    if (lock.get('schema'), lock.get('target'), lock.get('experimental')) != (1, 'athena', True):
        raise ValueError('not the reviewed experimental Athena lock')
    if set(lock['codec_source_lock']['helpers']) != {'tater-hw-vdec', 'tater-hw-venc'} or set(lock['modules']) != {
        'athena_ap.ko', 'athena_ap_vdec.ko', 'mtk_rpmsg.ko', 'mtk_scp_ipi.ko', 'mtk_scp.ko'
    }:
        raise ValueError('incomplete AP codec lock')
    return lock


def identities(lock):
    return {**lock['codec_source_lock']['helpers'], **lock['modules']}


def metadata(lock):
    return {'schema': 1, 'target': 'athena', 'experimental': True,
            'bundle_id': lock['bundle_id'], 'files': identities(lock),
            'source_lock_sha256': hashlib.sha256(json.dumps(lock, sort_keys=True, separators=(',', ':')).encode()).hexdigest()}


def verify(directory, *, kernel_image=None, lock=None):
    lock = load_lock() if lock is None else lock
    files = identities(lock)
    if directory.is_symlink() or not directory.is_dir() or {p.name for p in directory.iterdir()} != {*files, MANIFEST}:
        raise ValueError('codec bundle must contain exactly its public helpers/modules and manifest')
    for name, expected in files.items():
        path = directory / name
        if path.is_symlink() or not path.is_file() or not 20 <= path.stat().st_size <= 8*1024*1024 or digest(path) != expected:
            raise ValueError('unqualified codec file: ' + name)
        with path.open('rb') as stream:
            header = stream.read(20)
        elf_class, machine = (2, b'\xb7\x00') if name.endswith('.ko') else (1, b'\x28\x00')
        if header[:4] != b'\x7fELF' or header[4:6] != bytes([elf_class, 1]) or header[18:20] != machine:
            raise ValueError('incorrect codec ELF architecture: ' + name)
    manifest = directory / MANIFEST
    if manifest.is_symlink() or not manifest.is_file() or manifest.stat().st_size > 16384 or json.loads(manifest.read_text()) != metadata(lock):
        raise ValueError('codec metadata does not match the source lock')
    if kernel_image is not None and digest(kernel_image) != lock['kernel_template_sha256']:
        raise ValueError('AP modules require their exact empty-tuning kernel template')
    return metadata(lock)


def assemble(helpers, modules, output):
    lock = load_lock()
    if output.exists() or output.is_symlink():
        raise ValueError('output already exists; refusing to merge codec files')
    inputs = {name: (modules if name.endswith('.ko') else helpers)/name for name in identities(lock)}
    for name, path in inputs.items():
        if digest(path) != identities(lock)[name]:
            raise ValueError('unqualified codec input: ' + name)
    output.mkdir(parents=True)
    for name, path in inputs.items():
        shutil.copyfile(path, output/name)
        (output/name).chmod(0o644 if name.endswith('.ko') else 0o755)
    (output/MANIFEST).write_text(json.dumps(metadata(lock), sort_keys=True, separators=(',', ':'))+'\n')
    verify(output, lock=lock)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    check = commands.add_parser('verify')
    check.add_argument('directory', type=Path)
    check.add_argument('--kernel-image', type=Path)
    build = commands.add_parser('assemble')
    for name in ('helpers', 'modules', 'output'):
        build.add_argument('--'+name, required=True, type=Path)
    args = parser.parse_args()
    try:
        if args.command == 'verify': print(json.dumps(verify(args.directory, kernel_image=args.kernel_image), sort_keys=True))
        else: assemble(args.helpers, args.modules, args.output)
    except (OSError, ValueError, KeyError) as error:
        parser.exit(1, 'refusing: '+str(error)+'\n')
