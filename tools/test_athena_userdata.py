import ast
import hashlib
from pathlib import Path
import shlex
import subprocess
import tempfile
import unittest

from tools import athena_userdata

FIXTURE = Path(__file__).with_name('testdata') / 'athena_userdata_prepare.py'


class LocalAdb:
    """Run only the test's narrow cleanup inside its fresh temporary userdata."""
    def sh(self, command):
        return subprocess.check_output(['sh', '-c', command], text=True).strip()


class AthenaUserdataTests(unittest.TestCase):
    def function(self, source):
        namespace = {'quote': shlex.quote, 'fail': lambda message: self.fail(message)}
        exec(compile(source, str(FIXTURE), 'exec'), namespace)
        return namespace['prepare_tater_userdata']

    def write(self, root, relative):
        destination = root / relative
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_text('test sentinel')
        return destination

    def test_preserve_clears_real_nested_app_overrides_only(self):
        prepare = self.function(athena_userdata.patch_source(FIXTURE.read_text()))
        with tempfile.TemporaryDirectory(prefix='athena-usb-layout-') as temporary:
            root = Path(temporary) / 'userdata with spaces'
            apps = [self.write(root, prefix + suffix + '/old-binary')
                    for prefix in ('tater-linux/', 'techo5-linux/', 'techo5/techo5-linux/', 'techo5/tater-linux/')
                    for suffix in ('app', 'app.new')]
            preserved = [self.write(root, name) for name in (
                'techo5/local/etc/tater/native.json', 'techo5/local/etc/tater/device_token',
                'techo5/tater-linux/wpa_supplicant.conf', 'techo5/tater-linux/ssh/host_key',
                'techo5/misc/techo5/psk', 'unrelated/private-file')]
            prepare(LocalAdb(), str(root), False)
            self.assertTrue(all(not path.exists() for path in apps), 'old nested Tater app survived USB reinstall')
            self.assertTrue(all(path.read_text() == 'test sentinel' for path in preserved))

    def test_reset_clears_nested_tater_state_not_unrelated_userdata(self):
        prepare = self.function(athena_userdata.patch_source(FIXTURE.read_text()))
        with tempfile.TemporaryDirectory(prefix='athena-reset-layout-') as temporary:
            root = Path(temporary) / 'userdata'
            cleared = [self.write(root, name) for name in (
                'techo5/local/etc/tater/native.json', 'techo5/local/share/tater/cache',
                'techo5/tater-linux/wpa_supplicant.conf', 'techo5/tater-linux/ssh/host_key')]
            other = self.write(root, 'unrelated/private-file')
            prepare(LocalAdb(), str(root), True)
            self.assertTrue(all(not path.exists() for path in cleared))
            self.assertEqual(other.read_text(), 'test sentinel')

    def test_exact_source_identity_and_idempotence(self):
        source = FIXTURE.read_text()
        original = ast.get_source_segment(source, ast.parse(source).body[0])
        self.assertEqual(hashlib.sha256(original.encode()).hexdigest(), athena_userdata.ORIGINAL_SHA256)
        patched = athena_userdata.patch_source(source)
        function = ast.get_source_segment(patched, ast.parse(patched).body[0])
        self.assertEqual(hashlib.sha256(function.encode()).hexdigest(), athena_userdata.PATCHED_SHA256)
        self.assertEqual(athena_userdata.patch_source(patched), patched)

    def test_changed_missing_duplicate_functions_are_refused(self):
        source = FIXTURE.read_text()
        for changed in (source.replace("'/local/etc/tater'", "'/other/settings'"),
                        source.replace('prepare_tater_userdata', 'other_function'), source + source):
            with self.subTest(source=changed[:40]), self.assertRaises(ValueError):
                athena_userdata.patch_source(changed)


if __name__ == '__main__':
    unittest.main()
