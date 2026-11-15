import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock
from tools import prepare_release_mesa as mesa


class PrepareReleaseMesaTests(unittest.TestCase):
    def test_source_pins_match_the_public_recipe(self):
        self.assertEqual(len(mesa.SOURCES), 2)
        self.assertRegex(mesa.SOURCE_COMMIT, r'^[0-9a-f]{40}$')
        self.assertEqual(mesa.PACKAGE_BASE, 'https://dl-cdn.alpinelinux.org/alpine/v3.21/main/armv7/')

    def test_source_drift_is_rejected_before_execution(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            commit = mesa.SOURCE_COMMIT
            with mock.patch.object(mesa.subprocess, 'check_output', return_value=commit+'\n'), mock.patch.object(mesa.subprocess, 'run') as run:
                with self.assertRaises(ValueError): mesa.prepare(root, root/'packages', root/'output')
                run.assert_not_called()

    def test_package_hash_size_and_redirect_are_checked_before_writing(self):
        for mode in ('good', 'hash', 'size', 'redirect'):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                inventory = root/'probes/mt8163-gpu/athena-mesa-packages.json'
                inventory.parent.mkdir(parents=True)
                contents = json.dumps({'sample.apk': hashlib.sha256(b'package').hexdigest()})
                inventory.write_text(contents)
                sources = {inventory.relative_to(root).as_posix(): hashlib.sha256(contents.encode()).hexdigest()}
                response = mock.MagicMock()
                response.__enter__.return_value = response
                response.geturl.return_value = 'http://bad' if mode == 'redirect' else 'https://packages.example'
                response.read.return_value = b'wrong' if mode == 'hash' else b'package'
                with mock.patch.object(mesa, 'SOURCES', sources), mock.patch.object(mesa, 'MAX_PACKAGE', 1 if mode == 'size' else 100), mock.patch.object(mesa.request, 'urlopen', return_value=response):
                    if mode == 'good':
                        mesa.fetch_packages(root, root/'packages')
                        self.assertEqual((root/'packages/sample.apk').read_bytes(), b'package')
                    else:
                        with self.assertRaises(ValueError): mesa.fetch_packages(root, root/'packages')
                        self.assertFalse((root/'packages/sample.apk').exists())


if __name__ == '__main__': unittest.main()
