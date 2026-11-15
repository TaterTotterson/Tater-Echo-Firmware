import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

from tools import athena_codec_bundle as codec


class AthenaCodecBundleTests(unittest.TestCase):
    def fixture(self, directory):
        directory.mkdir()
        lock = codec.load_lock()
        for name in codec.identities(lock):
            data = bytearray(64)
            data[:4] = b'\x7fELF'
            data[4:6] = bytes([2 if name.endswith('.ko') else 1, 1])
            data[18:20] = b'\xb7\x00' if name.endswith('.ko') else b'\x28\x00'
            data += name.encode()
            (directory/name).write_bytes(data)
            hashes = lock['modules'] if name.endswith('.ko') else lock['codec_source_lock']['helpers']
            hashes[name] = hashlib.sha256(data).hexdigest()
        (directory/codec.MANIFEST).write_text(json.dumps(codec.metadata(lock)))
        return lock

    def test_complete_set_requires_exact_helpers_modules_and_kernel(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            directory = root/'bundle'
            lock = self.fixture(directory)
            codec.verify(directory, lock=lock)
            kernel = root/'boot.img'
            kernel.write_bytes(b'empty-tuning-test-template')
            with self.assertRaisesRegex(ValueError, 'kernel template'):
                codec.verify(directory, lock=lock, kernel_image=kernel)
            lock['kernel_template_sha256'] = codec.digest(kernel)
            (directory/codec.MANIFEST).write_text(json.dumps(codec.metadata(lock)))
            codec.verify(directory, lock=lock, kernel_image=kernel)

    def test_private_file_missing_changed_link_and_wrong_arch_are_rejected(self):
        for mode in ('private', 'missing', 'changed', 'link', 'architecture', 'metadata'):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as temporary:
                directory = Path(temporary)/'bundle'
                lock = self.fixture(directory)
                path = directory/'tater-hw-vdec'
                if mode == 'private': (directory/'libvcodecdrv.so').write_bytes(b'never include owner inputs')
                elif mode == 'missing': path.unlink()
                elif mode == 'changed': path.write_bytes(b'wrong')
                elif mode == 'link':
                    path.unlink()
                    path.symlink_to(directory/'tater-hw-venc')
                elif mode == 'architecture':
                    value = bytearray(path.read_bytes()); value[4] = 2; path.write_bytes(value)
                    lock['codec_source_lock']['helpers']['tater-hw-vdec'] = codec.digest(path)
                    (directory/codec.MANIFEST).write_text(json.dumps(codec.metadata(lock)))
                else: (directory/codec.MANIFEST).write_text('{}')
                with self.assertRaises(ValueError): codec.verify(directory, lock=lock)

    def test_boot_script_is_kernel_target_scpoff_and_hash_gated(self):
        path = codec.ROOT/'linux/athena/tater-athena-codecs'
        value = path.read_text()
        lock = codec.load_lock()
        self.assertIn(lock['kernel_release'], value)
        self.assertIn(lock['kernel_version'], value)
        self.assertIn('grep -qx amzn,athena', value)
        self.assertIn('remoteproc0/state)" = offline', value)
        for expected in lock['modules'].values(): self.assertIn(expected, value)
        self.assertLess(value.index('trap cleanup EXIT'), value.index('insmod '))
        self.assertLess(value.index('test -c /dev/tater-athena-ap-vdec'), value.index('> /run/tater-athena-ap.ready'))
        self.assertNotIn('firmware', value)
        subprocess.run(['sh','-n',str(path)],check=True)


if __name__ == '__main__': unittest.main()
