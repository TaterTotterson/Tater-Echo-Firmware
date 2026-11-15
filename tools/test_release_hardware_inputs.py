import copy
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest import mock

from tools import release_hardware_inputs as inputs
from tools import test_hardware_bundle as hardware_tests
from tools import test_athena_codec_bundle as codec_tests


class ReleaseHardwareInputTests(unittest.TestCase):
    def test_catalog_is_complete_bounded_and_source_pinned(self):
        lock = inputs.load()
        self.assertEqual(set(lock['inputs']), {*inputs.TARGETS, 'athena-codecs'})
        self.assertRegex(lock['source']['commit'], r'^[0-9a-f]{40}$')

    def fixture(self, directory, name):
        if name == 'athena-codecs':
            lock = codec_tests.AthenaCodecBundleTests().fixture(directory)
            return mock.patch.object(inputs.athena_codec_bundle, 'load_lock', return_value=lock)
        lock = hardware_tests.HardwareBundleTests().fixture(directory, name)
        return mock.patch.object(inputs.hardware_bundle, 'load_lock', return_value=lock)

    def test_every_target_has_deterministic_exact_allowlist_and_roundtrip(self):
        for name in (*inputs.TARGETS, 'athena-codecs'):
            with self.subTest(name=name), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                with self.fixture(root/'source', name):
                    first = inputs.pack(root/'source', root/'a.tar.gz', name)
                    second = inputs.pack(root/'source', root/'b.tar.gz', name)
                    self.assertEqual(first, second)
                    inputs.unpack((root/'a.tar.gz').read_bytes(), root/'roundtrip', name)
                    self.assertEqual({p.name for p in (root/'roundtrip').iterdir()}, inputs.names(name))
                    with self.assertRaisesRegex(ValueError, 'overwrite'):
                        inputs.unpack((root/'a.tar.gz').read_bytes(), root/'roundtrip', name)

    def test_unsafe_members_fail_without_publishing_output(self):
        for kind in ('traversal', 'private', 'symlink', 'duplicate', 'oversized'):
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                data = io.BytesIO()
                with tarfile.open(fileobj=data, mode='w:gz') as archive:
                    entry = tarfile.TarInfo({'traversal':'../secret', 'private':'libMtkOmxVenc.so'}.get(kind, 'tater-hw-gpu'))
                    entry.size = inputs.MAX_FILE+1 if kind == 'oversized' else 1
                    if kind == 'symlink':
                        entry.type = tarfile.SYMTYPE
                        entry.linkname = '/secret'
                        entry.size = 0
                    archive.addfile(entry, io.BytesIO(b'x'*entry.size) if entry.size else None)
                    if kind == 'duplicate': archive.addfile(entry, io.BytesIO(b'x'))
                with self.assertRaises(ValueError):
                    inputs.unpack(data.getvalue(), root/'output', 'checkers')
                self.assertFalse((root/'output').exists())

    def test_download_checks_outer_digest_and_size_before_unpack(self):
        for mode in ('good', 'hash', 'size', 'redirect'):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                with self.fixture(root/'source', 'athena'):
                    asset = inputs.pack(root/'source', root/'a.tar.gz', 'athena')
                    data = (root/'a.tar.gz').read_bytes()
                    asset['url'] = inputs.load()['inputs']['athena']['url']
                    if mode == 'hash': asset['sha256'] = '0'*64
                    if mode == 'size': asset['size'] += 1
                    response = mock.MagicMock()
                    response.__enter__.return_value = response
                    response.geturl.return_value = 'http://bad' if mode == 'redirect' else 'https://asset.example/file'
                    response.read.return_value = data
                    with mock.patch.object(inputs, 'load', return_value={'inputs':{'athena':asset}}), mock.patch.object(inputs.request, 'urlopen', return_value=response):
                        if mode == 'good': inputs.fetch('athena', root/'output')
                        else:
                            with self.assertRaises(ValueError): inputs.fetch('athena', root/'output')
                            self.assertFalse((root/'output').exists())

    def test_decompressed_stream_is_bounded_before_tar_metadata_is_parsed(self):
        with tempfile.TemporaryDirectory() as temporary:
            root=Path(temporary)
            with self.fixture(root/'source','athena'):
                inputs.pack(root/'source',root/'input.tgz','athena')
                with mock.patch.object(inputs,'MAX_UNPACKED',64):
                    with self.assertRaisesRegex(ValueError,'decompressed boundary'):
                        inputs.unpack((root/'input.tgz').read_bytes(),root/'output','athena')
                self.assertFalse((root/'output').exists())

    def test_source_and_asset_drift_are_rejected(self):
        original = inputs.load()
        for key, value in (('sha256', 'bad'), ('size', 0), ('url', 'https://example.com/latest.tgz')):
            lock = copy.deepcopy(original)
            lock['inputs']['athena'][key] = value
            with mock.patch.object(Path, 'read_text', return_value=json.dumps(lock)):
                with self.assertRaises(ValueError): inputs.load()


if __name__ == '__main__': unittest.main()
