#!/usr/bin/env python3
"""Read and validate the immutable firmware platform input lock."""

from __future__ import annotations

import argparse
import json
from pathlib import Path
import re
import sys


ROOT = Path(__file__).resolve().parents[1]
LOCK_PATH = ROOT / "platforms" / "tater-echo-linux-lock.json"
SHA256 = re.compile(r"^[0-9a-f]{64}$")
COMMIT = re.compile(r"^[0-9a-f]{40}$")
CANONICAL_REPOSITORY = "https://github.com/TaterTotterson/Tater-Echo-Linux.git"


def load() -> dict:
    try:
        value = json.loads(LOCK_PATH.read_text(encoding="utf-8"))
    except (OSError, ValueError) as error:
        raise SystemExit(f"cannot read {LOCK_PATH}: {error}") from error
    if value.get("schema") != 2:
        raise SystemExit("unsupported platform lock schema")
    return value


def get(value: object, dotted: str) -> object:
    current = value
    for part in dotted.split("."):
        if not isinstance(current, dict) or part not in current:
            raise SystemExit(f"platform lock has no {dotted!r}")
        current = current[part]
    return current


def verify(value: dict, require_builder_digest: bool = False) -> None:
    source = value.get("source", {})
    if source.get("repository") != CANONICAL_REPOSITORY:
        raise SystemExit("platform source is not the canonical Tater Echo Linux repository")

    builder = value.get("builder", {})
    digest = builder.get("digest")
    if require_builder_digest and not isinstance(digest, str):
        raise SystemExit("platform lock has no published builder digest")
    if digest is not None and (not isinstance(digest, str) or not digest.startswith("sha256:")
                               or not SHA256.fullmatch(digest.removeprefix("sha256:"))):
        raise SystemExit("builder digest must be null or sha256:<64 lowercase hex>")
    platform_digests = builder.get("platform_digests", {})
    if require_builder_digest:
        for platform in ("linux/amd64", "linux/arm/v7"):
            if platform not in platform_digests:
                raise SystemExit(f"platform lock has no builder digest for {platform}")
    for platform, platform_digest in platform_digests.items():
        if platform not in {"linux/amd64", "linux/arm/v7"}:
            raise SystemExit(f"unsupported builder platform {platform}")
        if (not isinstance(platform_digest, str)
                or not platform_digest.startswith("sha256:")
                or not SHA256.fullmatch(platform_digest.removeprefix("sha256:"))):
            raise SystemExit(f"builder digest for {platform} must be sha256:<64 lowercase hex>")
    base_image = str(builder.get("base_image", ""))
    if "@sha256:" not in base_image or not SHA256.fullmatch(base_image.rsplit("@sha256:", 1)[-1]):
        raise SystemExit("builder base image must be digest-pinned")
    for name, version in builder.get("packages", {}).items():
        if not name or not isinstance(version, str) or not version:
            raise SystemExit("builder packages must have exact versions")

    platforms = value.get("platforms", {})
    for key in ("dot", "show", "athena", "rook"):
        if key not in platforms:
            raise SystemExit(f"platform lock is missing {key}")
    for platform in ("dot", "rook"):
        if not COMMIT.fullmatch(platforms[platform].get("commit", "")):
            raise SystemExit(f"invalid {platform} source commit")
    if not COMMIT.fullmatch(platforms["show"].get("checkers_commit", "")):
        raise SystemExit("invalid Checkers/Crown source commit")
    if not COMMIT.fullmatch(platforms["athena"].get("commit", "")):
        raise SystemExit("invalid Athena source commit")

    def walk(node: object, path: str = "") -> None:
        if isinstance(node, dict):
            for key, child in node.items():
                child_path = f"{path}.{key}" if path else key
                if key == "sha256" and (not isinstance(child, str) or not SHA256.fullmatch(child)):
                    raise SystemExit(f"invalid SHA-256 at {child_path}")
                if key == "url" and (not isinstance(child, str) or not child.startswith("https://")):
                    raise SystemExit(f"platform asset URL is not HTTPS at {child_path}")
                walk(child, child_path)
        elif isinstance(node, list):
            for index, child in enumerate(node):
                walk(child, f"{path}[{index}]")

    walk(platforms)


def scalar(value: object) -> str:
    if isinstance(value, bool):
        return "true" if value else "false"
    if value is None:
        return ""
    if isinstance(value, (str, int, float)):
        return str(value)
    raise SystemExit("requested platform lock value is not a scalar")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)
    get_parser = subparsers.add_parser("get")
    get_parser.add_argument("path")
    subparsers.add_parser("verify").add_argument("--require-builder-digest", action="store_true")
    builder_parser = subparsers.add_parser("builder-reference")
    builder_parser.add_argument("--platform", choices=("linux/amd64", "linux/arm/v7"))
    args = parser.parse_args()

    value = load()
    if args.command == "get":
        print(scalar(get(value, args.path)))
    elif args.command == "verify":
        verify(value, args.require_builder_digest)
    elif args.command == "builder-reference":
        verify(value, require_builder_digest=True)
        digest = (get(value, f"builder.platform_digests.{args.platform}")
                  if args.platform else get(value, "builder.digest"))
        print(f"{get(value, 'builder.image')}@{digest}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
