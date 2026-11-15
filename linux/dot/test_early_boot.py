from __future__ import annotations

import gzip
import os
from pathlib import Path
import struct
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

from linux.dot import early_boot


SCRIPT = Path(__file__).with_name('tater-early-recovery')


class EarlyImageTests(unittest.TestCase):
    def test_hook_precedes_slot_selection_and_preserves_normal_boot(self):
        original = b'#!/bin/sh\nboot_slot() { echo slot; }\n' + early_boot.ANCHOR
        result = early_boot.patch_init(original)
        self.assertLess(result.index(b'/usr/local/sbin/tater-early-recovery'),
                        result.index(early_boot.ANCHOR))
        self.assertEqual(result.count(early_boot.ANCHOR), 1)
        self.assertIn(b'"$early_recovery_result" -eq 20', result)
        for invalid in (b'changed init', original + early_boot.ANCHOR, result):
            with self.assertRaises(ValueError):
                early_boot.patch_init(invalid)

    def test_static_arm_contract_rejects_host_and_dynamic_helpers(self):
        with tempfile.TemporaryDirectory() as temporary:
            helper = Path(temporary) / 'helper'
            helper.write_bytes(b'not ELF')
            with self.assertRaises(ValueError):
                early_boot.require_static_arm(helper)
            elf = bytearray(84)
            elf[:7] = b'\x7fELF\x01\x01\x01'
            struct.pack_into('<H', elf, 18, 40)
            helper.write_bytes(elf)
            early_boot.require_static_arm(helper)
            struct.pack_into('<I', elf, 28, 52)
            struct.pack_into('<HH', elf, 42, 32, 1)
            struct.pack_into('<I', elf, 52, 3)  # PT_INTERP
            helper.write_bytes(elf)
            with self.assertRaisesRegex(ValueError, 'dynamic loader'):
                early_boot.require_static_arm(helper)

    def test_image_overlay_retains_kernel_and_uses_exact_target(self):
        for target in ('biscuit', 'radar'):
            with self.subTest(target=target), tempfile.TemporaryDirectory() as temporary:
                folder = Path(temporary)
                helper = folder / 'helper'
                elf = bytearray(52)
                elf[:7] = b'\x7fELF\x01\x01\x01'
                struct.pack_into('<H', elf, 18, 40)
                helper.write_bytes(elf)
                source = b'original init' + early_boot.ANCHOR
                interface = mock.Mock()
                interface._read_boot_image.return_value = (b'header', b'kernel', gzip.compress(source), 2048)

                def patch(raw, replacements, additions):
                    self.assertEqual(raw, source)
                    self.assertIn(b'tater-early-recovery', replacements['init'](raw))
                    self.assertEqual(additions['etc/tater-early-target'], (0o644, (target + '\n').encode()))
                    self.assertEqual(additions['usr/local/bin/tater-volume-up'], (0o755, bytes(elf)))
                    return b'patched cpio'

                interface._patch_newc.side_effect = patch
                image = folder / 'boot.img'
                early_boot.patch_boot_image(interface, image, helper, SCRIPT, target)
                header, kernel, compressed, page, destination = interface._write_boot_image.call_args.args
                self.assertEqual((header, kernel, page, destination), (b'header', b'kernel', 2048, str(image)))
                self.assertEqual(gzip.decompress(compressed), b'patched cpio')
                with self.assertRaises(ValueError):
                    early_boot.patch_boot_image(interface, image, helper, SCRIPT, 'rook')

    def test_checksums_are_narrow_and_private(self):
        with tempfile.TemporaryDirectory() as temporary:
            folder = Path(temporary)
            images = [folder / name for name in ('linux', 'twrp', 'handoff')]
            for image in images:
                image.write_bytes(image.name.encode())
            output = folder / 'early-recovery.sha256'
            early_boot.write_checksums(output, *images)
            self.assertEqual([line.split()[1] for line in output.read_text().splitlines()],
                             ['linux.img', 'twrp.img', 'twrp-handoff.img'])
            self.assertEqual(output.stat().st_mode & 0o777, 0o600)

    def test_oversized_boot_image_is_refused_before_replacing_candidate(self):
        with tempfile.TemporaryDirectory() as temporary:
            folder = Path(temporary)
            helper = folder / 'helper'
            elf = bytearray(52)
            elf[:7] = b'\x7fELF\x01\x01\x01'
            struct.pack_into('<H', elf, 18, 40)
            helper.write_bytes(elf)
            interface = mock.Mock()
            interface._read_boot_image.return_value = (
                b'header', b'x' * (17 * 1024 * 1024), gzip.compress(b'init'), 2048)
            interface._patch_newc.return_value = b'cpio'
            with self.assertRaisesRegex(ValueError, 'exceeds the 16 MiB'):
                early_boot.patch_boot_image(interface, folder / 'image', helper, SCRIPT, 'radar')
            interface._write_boot_image.assert_not_called()


# This shim only simulates mount/unmount and hashing in a private fixture.
# Other applets use host tools. Production paths/device checks are replaced
# solely in the test copy of the script; no host block device is ever opened.
SHIM = r'''import hashlib, os, subprocess, sys
command, *args = sys.argv[1:]
if command in ('mount', 'umount'):
    with open(os.environ['CALLS'], 'a') as output: output.write(command + '\n')
    sys.exit(int(os.environ.get('MOUNT_FAIL', '0')) if command == 'mount' else 0)
if command == 'sha256sum':
    print(hashlib.sha256(sys.stdin.buffer.read()).hexdigest() + '  -')
    sys.exit(0)
sys.exit(subprocess.call([command, *args]))
'''


class EarlyHandoffTests(unittest.TestCase):
    def fixture(self, folder, target='biscuit', key=0, handoff='exit 1\n'):
        store = folder / 'store/techo5'
        store.mkdir(parents=True)
        for index, name in enumerate(('linux.img', 'twrp.img', 'twrp-handoff.img')):
            (store / name).write_bytes(b'ANDROID!' + bytes([index]) * 4096)
        early_boot.write_checksums(store / 'early-recovery.sha256',
                                  store / 'linux.img', store / 'twrp.img', store / 'twrp-handoff.img')
        (folder / 'recovery').write_bytes((store / 'linux.img').read_bytes())
        (folder / 'target').write_text(target + '\n')
        (folder / 'identity').write_text('A7WXQPH584YP\n' if target == 'radar' else 'A3S5BH2HU6VAYF\n')
        (folder / 'busybox').write_text('#!' + sys.executable + '\n' + SHIM)
        (folder / 'busybox').chmod(0o755)
        (folder / 'key').write_text('#!/bin/sh\nexit ' + str(key) + '\n')
        (folder / 'key').chmod(0o755)
        (folder / 'handoff').write_text('echo handoff >> "$CALLS"\n' + handoff)
        script = SCRIPT.read_text()
        for old, new in {
            'BB=/bin/busybox.static': 'BB=' + str(folder / 'busybox'),
            'STORE=/store': 'STORE=' + str(folder / 'store'),
            'REC=/dev/mmcblk0p12': 'REC=' + str(folder / 'recovery'),
            'ID=/proc/idme/device_type_id': 'ID=' + str(folder / 'identity'),
            'TARGET=/etc/tater-early-target': 'TARGET=' + str(folder / 'target'),
            'KEY=/usr/local/bin/tater-volume-up': 'KEY=' + str(folder / 'key'),
            'HANDOFF=/usr/local/sbin/to-twrp': 'HANDOFF=' + str(folder / 'handoff'),
            '[ -b "$REC" ]': '[ -f "$REC" ]',
        }.items():
            script = script.replace(old, new)
        (folder / 'test.sh').write_text(script)
        return store

    def run_script(self, folder, **environment):
        calls = folder / 'calls'
        result = subprocess.run(['sh', str(folder / 'test.sh')], capture_output=True, text=True,
                                env={**os.environ, 'CALLS': str(calls), **environment}, timeout=10)
        return result.returncode, calls.read_text().splitlines() if calls.exists() else []

    def test_idle_released_or_unavailable_never_mount_or_write(self):
        for status in (1, 2):
            with self.subTest(status=status), tempfile.TemporaryDirectory() as temporary:
                folder = Path(temporary)
                self.fixture(folder, key=status)
                self.assertEqual(self.run_script(folder), (0, []))

    def test_wrong_target_never_mounts_or_writes(self):
        with tempfile.TemporaryDirectory() as temporary:
            folder = Path(temporary)
            self.fixture(folder)
            (folder / 'identity').write_text('A7WXQPH584YP\n')
            self.assertEqual(self.run_script(folder), (0, []))

    def test_verified_boards_can_handoff_with_original_linux_still_valid(self):
        for target in ('biscuit', 'radar'):
            with self.subTest(target=target), tempfile.TemporaryDirectory() as temporary:
                folder = Path(temporary)
                self.fixture(folder, target)
                self.assertEqual(self.run_script(folder), (0, ['mount', 'handoff', 'umount']))

    def test_missing_or_corrupt_cache_never_handoffs(self):
        for change in ('missing manifest', 'bad twrp', 'stale linux', 'duplicate manifest', 'symlink'):
            with self.subTest(change=change), tempfile.TemporaryDirectory() as temporary:
                folder = Path(temporary)
                store = self.fixture(folder)
                if change == 'missing manifest':
                    (store / 'early-recovery.sha256').unlink()
                elif change == 'bad twrp':
                    (store / 'twrp.img').write_bytes(b'corrupted')
                elif change == 'stale linux':
                    (folder / 'recovery').write_bytes(b'ANDROID!' + bytes(4096))
                    # Alter one byte: this must not match the cached current kernel.
                    with (folder / 'recovery').open('r+b') as output:
                        output.seek(20); output.write(b'x')
                elif change == 'duplicate manifest':
                    (store / 'early-recovery.sha256').write_text(
                        ((store / 'early-recovery.sha256').read_text().splitlines()[0] + '\n') * 3)
                else:
                    (store / 'twrp.img').rename(store / 'real-twrp.img')
                    (store / 'twrp.img').symlink_to('real-twrp.img')
                self.assertEqual(self.run_script(folder), (0, ['mount', 'umount']))

    def test_mount_failure_never_handoffs(self):
        with tempfile.TemporaryDirectory() as temporary:
            folder = Path(temporary)
            self.fixture(folder)
            self.assertEqual(self.run_script(folder, MOUNT_FAIL='1'), (0, ['mount']))

    def test_unrestored_failed_handoff_stays_in_rescue(self):
        with tempfile.TemporaryDirectory() as temporary:
            folder = Path(temporary)
            self.fixture(folder, handoff='echo changed > "' + str(folder / 'recovery') + '"\nexit 1\n')
            self.assertEqual(self.run_script(folder), (20, ['mount', 'handoff']))


if __name__ == '__main__':
    unittest.main()
