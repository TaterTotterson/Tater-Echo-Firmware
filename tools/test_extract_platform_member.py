from __future__ import annotations

import hashlib
import io
from pathlib import Path
import tarfile
import tempfile
import unittest

from tools import extract_platform_member


class ExtractPlatformMemberTests(unittest.TestCase):
    def test_extracts_only_verified_regular_member(self) -> None:
        payload = b"pinned arm decoder"
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "rootfs.tar.gz"
            with tarfile.open(archive, "w:gz") as bundle:
                info = tarfile.TarInfo("./usr/local/bin/techo5-ffmpeg")
                info.size = len(payload)
                bundle.addfile(info, io.BytesIO(payload))
            output = root / "tater-ffmpeg"
            extract_platform_member.extract(
                archive,
                "usr/local/bin/techo5-ffmpeg",
                output,
                hashlib.sha256(payload).hexdigest(),
                len(payload),
            )
            self.assertEqual(output.read_bytes(), payload)
            self.assertTrue(output.stat().st_mode & 0o100)

    def test_rejects_hash_mismatch(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "rootfs.tar"
            with tarfile.open(archive, "w") as bundle:
                info = tarfile.TarInfo("decoder")
                info.size = 3
                bundle.addfile(info, io.BytesIO(b"bad"))
            with self.assertRaisesRegex(SystemExit, "failed verification"):
                extract_platform_member.extract(archive, "decoder", root / "out", "0" * 64, 3)

    def test_rejects_parent_traversal_in_requested_or_archived_name(self) -> None:
        payload = b"not the requested decoder"
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "rootfs.tar"
            with tarfile.open(archive, "w") as bundle:
                info = tarfile.TarInfo("../usr/local/bin/techo5-ffmpeg")
                info.size = len(payload)
                bundle.addfile(info, io.BytesIO(payload))
            with self.assertRaisesRegex(SystemExit, "safe relative path"):
                extract_platform_member.extract(
                    archive,
                    "../usr/local/bin/techo5-ffmpeg",
                    root / "out",
                    hashlib.sha256(payload).hexdigest(),
                    len(payload),
                )
            with self.assertRaisesRegex(SystemExit, "does not contain"):
                extract_platform_member.extract(
                    archive,
                    "usr/local/bin/techo5-ffmpeg",
                    root / "out",
                    hashlib.sha256(payload).hexdigest(),
                    len(payload),
                )


if __name__ == "__main__":
    unittest.main()
