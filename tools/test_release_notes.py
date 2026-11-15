from __future__ import annotations

import unittest

from tools.release_notes import CHANGELOG, REPO, extract


class ReleaseNotesTests(unittest.TestCase):
    CHANGELOG = """# Changelog

## v2.1.0

### What's Changed

- Rook first release.
- Biscuit beamforming update.
- Checkers voice processing update.

## v2.0.4

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

    def test_v5_notes_and_dot_readmes_explain_twrp_usb_upgrade(self) -> None:
        documents = {
            "release notes": extract("v5.0.0", CHANGELOG.read_text(encoding="utf-8")),
            "main README": (REPO / "README.md").read_text(encoding="utf-8"),
            "bundled README": (REPO / "factory/dot-linux/README.md").read_text(encoding="utf-8"),
        }
        for name, document in documents.items():
            with self.subTest(document=name):
                for required in (
                    "v4.2.3", "python3 to_twrp.py", "py -3 to_twrp.py",
                    "Volume Up", "--factory-reset", "--repair",
                ):
                    self.assertIn(required, document)
        self.assertNotIn("pre-v4", documents["bundled README"])

    def test_v5_final_notes_and_readme_link_kernel_and_hardware_sources(self) -> None:
        notes = extract("v5.0.0", CHANGELOG.read_text(encoding="utf-8"))
        self.assertEqual(notes.splitlines()[0], "## v5.0.0")
        self.assertNotIn("release candidate", notes)
        for document in (notes, (REPO / "README.md").read_text(encoding="utf-8")):
            for repository in ("Tater-Echo-Linux", "Tater-Echo-Hardware"):
                self.assertIn("https://github.com/TaterTotterson/" + repository, document)
        self.assertIn("echo-5-release-review.md", notes)
        self.assertIn("One-time USB upgrade required", notes)


if __name__ == "__main__":
    unittest.main()
