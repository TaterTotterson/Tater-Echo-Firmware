import hashlib
import json
import io
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest import mock
from tools import athena_mesa

class AthenaMesaTests(unittest.TestCase):
    def fixture(self, root):
        (root/'lib').mkdir()
        data=b'generated ELF stand-in'
        (root/'lib/loader.so').write_bytes(data)
        manifest={'schema':3,'runtime_id':athena_mesa.RUNTIME_ID,'files':{
            'lib/loader.so':{'size':len(data),'sha256':hashlib.sha256(data).hexdigest()}}}
        contents=json.dumps(manifest).encode()
        (root/'runtime.json').write_bytes(contents)
        return hashlib.sha256(contents).hexdigest()

    def test_manifest_pin_matches_the_go_adapter_and_fixture(self):
        source=(athena_mesa.ROOT/'device/internal/gpucompositor/mesa_linux.go').read_text()
        contents=(athena_mesa.ROOT/'device/internal/gpucompositor/testdata/athena-mesa-runtime.json').read_bytes()
        self.assertEqual(hashlib.sha256(contents).hexdigest(),athena_mesa.MANIFEST_SHA256)
        self.assertIn(athena_mesa.MANIFEST_SHA256,source)

    def test_exact_tree_and_reject_drift_or_extra_files(self):
        for mutation in (None,'changed','extra','missing','symlink','metadata'):
            with self.subTest(mutation=mutation),tempfile.TemporaryDirectory() as temporary:
                root=Path(temporary)
                digest=self.fixture(root)
                if mutation=='changed': (root/'lib/loader.so').write_bytes(b'wrong')
                elif mutation=='extra': (root/'private.so').write_bytes(b'never package')
                elif mutation=='missing': (root/'lib/loader.so').unlink()
                elif mutation=='symlink':
                    (root/'alias').symlink_to(root/'lib')
                elif mutation=='metadata': (root/'runtime.json').write_text('{}')
                with mock.patch.object(athena_mesa,'MANIFEST_SHA256',digest):
                    if mutation is None: athena_mesa.verify(root)
                    else:
                        with self.assertRaises(ValueError):athena_mesa.verify(root)

    def test_rootfs_requires_exact_embedded_runtime_without_links_or_duplicates(self):
        for mutation in (None,'missing','changed','link','root_link','duplicate','extra'):
            with self.subTest(mutation=mutation),tempfile.TemporaryDirectory() as temporary:
                root=Path(temporary)
                digest=self.fixture(root)
                contents=(root/'runtime.json').read_bytes()
                path=root/'rootfs.tar.gz'
                entries=[('runtime.json',contents),('lib/loader.so',(root/'lib/loader.so').read_bytes())]
                if mutation=='missing':entries.pop()
                elif mutation=='changed':entries[-1]=('lib/loader.so',b'wrong')
                elif mutation=='extra':entries.append(('private.so',b'wrong'))
                elif mutation=='duplicate':entries.append(entries[-1])
                with tarfile.open(path,'w:gz') as archive:
                    if mutation=='root_link':
                        entry=tarfile.TarInfo('usr/lib/tater-mesa')
                        entry.type=tarfile.SYMTYPE;entry.linkname='/usr/lib/other'
                        archive.addfile(entry)
                    for relative,body in entries:
                        entry=tarfile.TarInfo('usr/lib/tater-mesa/'+relative)
                        entry.size=len(body)
                        if mutation=='link' and relative=='lib/loader.so':
                            entry.type=tarfile.SYMTYPE;entry.linkname='/usr/lib/other.so';entry.size=0
                        archive.addfile(entry,io.BytesIO(body) if entry.isfile() else None)
                with mock.patch.object(athena_mesa,'MANIFEST_SHA256',digest),mock.patch.object(athena_mesa,'expected_manifest',return_value=contents):
                    if mutation is None:athena_mesa.verify_rootfs(path)
                    else:
                        with self.assertRaises(ValueError):athena_mesa.verify_rootfs(path)
