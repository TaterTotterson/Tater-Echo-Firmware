from __future__ import annotations

import unittest

from tools.release_notes import extract


class ReleaseNotesTests(unittest.TestCase):
    CHANGELOG = """# Changelog

## v2.1.0 — 2026-10-04

### What's Changed

- Rook first release.
- Biscuit beamforming update.
- Checkers voice processing update.

## v2.0.4 — 2026-10-03

- Older change.
"""

    def test_extracts_only_the_requested_release_section(self) -> None:
        notes = extract("v2.1.0", self.CHANGELOG)

        self.assertIn("Rook first release", notes)
        self.assertIn("Biscuit beamforming update", notes)
        self.assertIn("Checkers voice processing update", notes)
        self.assertNotIn("Older change", notes)

    def test_accepts_version_without_v_prefix(self) -> None:
        self.assertEqual(extract("2.1.0", self.CHANGELOG), extract("v2.1.0", self.CHANGELOG))

    def test_rejects_a_version_without_release_notes(self) -> None:
        with self.assertRaisesRegex(ValueError, "v9.9.9"):
            extract("v9.9.9", self.CHANGELOG)


if __name__ == "__main__":
    unittest.main()
