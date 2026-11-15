import hashlib
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest import mock

from tools import runtime_compat as compat


def tar_bytes(files):
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode="w:gz") as archive:
        for name, data in files.items():
            member = tarfile.TarInfo(name)
            member.size = len(data)
            archive.addfile(member, io.BytesIO(data))
    return output.getvalue()


class RuntimeCompatTests(unittest.TestCase):
    def fixture(self, root):
        root.mkdir()
        elf = bytearray(24)
        elf[:6], elf[18:20] = b"\x7fELF\x01\x01", b"\x28\x00"
        expected = {"public.so": hashlib.sha256(elf).hexdigest()}
        (root / "public.so").write_bytes(elf)
        lock = {"files": expected, "sources": {"original.c": hashlib.sha256(b"original source").hexdigest()}, "sync_sources": {}}
        lock_bytes = json.dumps(lock).encode()
        sources = {"original.c": b"original source", "platforms/runtime-compat-lock.json": lock_bytes,
                   "LICENSE": b"AGPL", "LICENSES/MIT.txt": b"MIT", "NOTICE.md": b"notice"}
        (root / "runtime-compat-source.tar.gz").write_bytes(tar_bytes(sources))
        (root / "AOSP-SYNC-NOTICE").write_bytes(b"upstream notice")
        (root / "runtime-compat.json").write_text(json.dumps({"schema": 1, "files": expected, "source_lock_sha256": hashlib.sha256(lock_bytes).hexdigest()}))
        return expected

    def test_exact_inventory_source_packet_and_deterministic_roundtrip(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            expected = self.fixture(root / "public")
            with mock.patch.object(compat, "EXPECTED", expected):
                a, b = root / "a.tar.gz", root / "b.tar.gz"
                self.assertEqual(compat.pack(root / "public", a), compat.pack(root / "public", b))
                compat.unpack(a.read_bytes(), root / "unpacked")
                self.assertEqual(compat.verify(root / "unpacked")["files"], expected)
                with self.assertRaises(ValueError):
                    compat.unpack(a.read_bytes(), root / "unpacked")
                (root / "public/vendor.so").write_bytes(b"must never ship")
                with self.assertRaises(ValueError):
                    compat.verify(root / "public")

    def test_altered_binary_and_source_are_rejected(self):
        for name in ("public.so", "runtime-compat-source.tar.gz"):
            with self.subTest(name=name), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary) / "public"
                expected = self.fixture(root)
                (root / name).write_bytes(b"changed")
                with mock.patch.object(compat, "EXPECTED", expected), self.assertRaises((ValueError, OSError)):
                    compat.verify(root)

    def test_link_and_traversal_entries_are_rejected_without_extraction(self):
        for name in ("../escape", "/absolute", "vendor.so"):
            with self.subTest(name=name), tempfile.TemporaryDirectory() as temporary:
                output = Path(temporary) / "public"
                with self.assertRaises(ValueError):
                    compat.unpack(tar_bytes({name: b"bad"}), output)
                self.assertFalse(output.exists())


if __name__ == "__main__":
    unittest.main()
