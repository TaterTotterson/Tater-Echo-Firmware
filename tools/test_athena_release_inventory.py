import hashlib
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest import mock

from tools import package_echo_release as release
from tools import test_hardware_bundle as gpu_tests, test_athena_codec_bundle as codec_tests


class AthenaReleaseInventoryTests(unittest.TestCase):
    def test_verified_system_codecs_supply_the_complete_app_pair(self):
        for mutation in (None, 'missing', 'changed', 'duplicate', 'private', 'kernel', 'kernel_missing', 'link'):
            with self.subTest(mutation=mutation), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                gpu = root/'gpu'
                gpu_lock = gpu_tests.HardwareBundleTests().fixture(gpu, 'athena')
                codec = root/'codec'
                codec_lock = codec_tests.AthenaCodecBundleTests().fixture(codec)
                template = b'empty tuning kernel template'
                codec_lock['kernel_template_sha256'] = hashlib.sha256(template).hexdigest()
                (codec/release.athena_codec_bundle.MANIFEST).write_text(json.dumps(release.athena_codec_bundle.metadata(codec_lock)))
                entries = {'usr/local/bin/'+p.name: p.read_bytes() for p in gpu.iterdir()}
                entries.update({('usr/lib/tater-athena-codecs/' if p.name.endswith('.ko') or p.name.endswith('.json') else 'usr/local/bin/')+p.name: p.read_bytes() for p in codec.iterdir()})
                entries['usr/share/techo5/kernel/boot-athena.img'] = template
                selected = 'usr/local/bin/tater-hw-vdec'
                if mutation == 'missing': entries.pop(selected)
                elif mutation == 'changed': entries[selected] = b'wrong'
                elif mutation == 'private': entries['usr/lib/tater-athena-codecs/vendor.so'] = b'never ship'
                elif mutation == 'kernel': entries['usr/share/techo5/kernel/boot-athena.img'] = b'old kernel'
                elif mutation == 'kernel_missing': entries.pop('usr/share/techo5/kernel/boot-athena.img')
                archive_path = root/'rootfs.tgz'
                with tarfile.open(archive_path, 'w:gz') as archive:
                    for name, data in entries.items():
                        entry = tarfile.TarInfo(name); entry.size = len(data)
                        if mutation == 'link' and name == selected:
                            entry.type = tarfile.SYMTYPE; entry.linkname = '/data/vendor'; entry.size = 0
                        archive.addfile(entry, io.BytesIO(data) if entry.isfile() else None)
                        if mutation == 'duplicate' and name == selected: archive.addfile(entry, io.BytesIO(data))
                with mock.patch.object(release.athena_mesa, 'verify_rootfs'), mock.patch.object(release.hardware_bundle, 'load_lock', return_value=gpu_lock), mock.patch.object(release.athena_codec_bundle, 'load_lock', return_value=codec_lock):
                    if mutation is None:
                        files = release.hardware_files_from_rootfs(archive_path, 'athena')
                        self.assertEqual(set(files), {'tater-hw-gpu','tater-hardware.json','tater-hw-vdec','tater-hw-venc'})
                    else:
                        with self.assertRaises(ValueError): release.hardware_files_from_rootfs(archive_path, 'athena')


if __name__ == '__main__': unittest.main()
