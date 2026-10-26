from __future__ import annotations

import json
from pathlib import Path
import unittest

from tools import platform_lock
from tools import fetch_platform_input


ROOT = Path(__file__).resolve().parents[1]


class PlatformLockTests(unittest.TestCase):
    def test_all_platform_source_commits_live_in_tater_echo_linux(self) -> None:
        value = platform_lock.load()
        self.assertEqual(
            "https://github.com/TaterTotterson/Tater-Echo-Linux.git",
            value["source"]["repository"],
        )
        for commit in (
            value["platforms"]["dot"]["commit"],
            value["platforms"]["show"]["checkers_commit"],
            value["platforms"]["athena"]["commit"],
            value["platforms"]["rook"]["commit"],
        ):
            self.assertRegex(commit, r"^[0-9a-f]{40}$")

    def test_lock_has_a_published_builder_digest(self) -> None:
        value = platform_lock.load()
        platform_lock.verify(value, require_builder_digest=True)

    def test_builder_references_are_pinned_per_platform(self) -> None:
        value = platform_lock.load()
        image = value["builder"]["image"]
        for platform in ("linux/amd64", "linux/arm/v7"):
            digest = value["builder"]["platform_digests"][platform]
            self.assertRegex(digest, r"^sha256:[0-9a-f]{64}$")
            self.assertNotEqual(digest, value["builder"]["digest"])
            self.assertEqual(
                platform_lock.get(value, f"builder.platform_digests.{platform}"),
                digest,
            )
            self.assertTrue(f"{image}@{digest}".startswith("ghcr.io/"))

    def test_release_inputs_are_https_and_digest_pinned(self) -> None:
        value = platform_lock.load()
        assets: list[dict] = []

        def collect(node: object) -> None:
            if isinstance(node, dict):
                if "url" in node:
                    assets.append(node)
                for child in node.values():
                    collect(child)
            elif isinstance(node, list):
                for child in node:
                    collect(child)

        collect(value["platforms"])
        self.assertGreaterEqual(len(assets), 12)
        for asset in assets:
            self.assertTrue(asset["url"].startswith("https://"))
            self.assertRegex(asset["sha256"], r"^[0-9a-f]{64}$")

    def test_builder_recipe_has_no_unversioned_package(self) -> None:
        dockerfile = (ROOT / "platforms/builder/Dockerfile").read_text(encoding="utf-8")
        lock = platform_lock.load()
        self.assertIn(lock["builder"]["base_image"], dockerfile)
        self.assertIn("/usr/local/bin/go", dockerfile)
        for package in lock["builder"]["packages"]:
            variable = package.upper().replace("-", "_") + "_VERSION"
            self.assertIn(f'"{package}=${{{variable}}}"', dockerfile)

    def test_lock_is_canonical_json(self) -> None:
        path = ROOT / "platforms/tater-echo-linux-lock.json"
        value = json.loads(path.read_text(encoding="utf-8"))
        self.assertEqual(path.read_text(encoding="utf-8"), json.dumps(value, indent=2) + "\n")

    def test_fetcher_rejects_non_asset_nodes(self) -> None:
        with self.assertRaises(SystemExit):
            fetch_platform_input.fetch("platforms.dot", ROOT / "unused")


if __name__ == "__main__":
    unittest.main()
