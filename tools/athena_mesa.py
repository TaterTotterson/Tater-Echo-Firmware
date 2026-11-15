"""Verify Athena's isolated open-source Mesa runtime, never system/vendor libs."""
import argparse
import hashlib
import json
from pathlib import Path
import tarfile

ROOT = Path(__file__).resolve().parents[1]
MANIFEST_SHA256 = "ab77ccdd446064c327bf3fe150b3eeb73d65cc8f9bf3c5aeb79f7376725a5ef8"
RUNTIME_ID = "athena-mt8183-panfrost-mesa-24.2.8-1"

def expected_manifest() -> bytes:
    contents=(ROOT/"device/internal/gpucompositor/testdata/athena-mesa-runtime.json").read_bytes()
    if hashlib.sha256(contents).hexdigest()!=MANIFEST_SHA256:
        raise ValueError("compiled Mesa identity and source fixture disagree")
    return contents

def verify_rootfs(filename: Path) -> None:
    """Verify without extracting a rootfs or accepting links into other libs."""
    contents=expected_manifest()
    expected=dict(json.loads(contents)["files"])
    expected["runtime.json"]={"sha256":MANIFEST_SHA256,"size":len(contents)}
    seen=set()
    prefix="usr/lib/tater-mesa/"
    with tarfile.open(filename,"r|gz") as archive:
        for entry in archive:
            name=entry.name.removeprefix("./")
            if name==prefix.rstrip("/") and not entry.isdir():
                raise ValueError("rootfs Mesa root may not be a link or file")
            if not name.startswith(prefix) or entry.isdir():continue
            relative=name[len(prefix):]
            if relative in seen or relative not in expected or not entry.isfile() or entry.size!=expected[relative]["size"]:
                raise ValueError("rootfs contains an unexpected Mesa file or link")
            stream=archive.extractfile(entry)
            digest=hashlib.sha256()
            for block in iter(lambda:stream.read(1024*1024),b""):digest.update(block)
            if digest.hexdigest()!=expected[relative]["sha256"]:
                raise ValueError("rootfs Mesa file differs from the qualified runtime")
            seen.add(relative)
    if seen!=set(expected):raise ValueError("rootfs is missing the Athena Mesa runtime")

def verify(directory: Path) -> dict:
    if directory.is_symlink() or not directory.is_dir():
        raise ValueError("Mesa runtime must be an ordinary directory")
    manifest_path = directory/"runtime.json"
    if manifest_path.is_symlink() or not manifest_path.is_file() or manifest_path.stat().st_size > 65536:
        raise ValueError("Mesa manifest unavailable")
    contents = manifest_path.read_bytes()
    if hashlib.sha256(contents).hexdigest() != MANIFEST_SHA256:
        raise ValueError("Mesa manifest differs from the qualified runtime")
    manifest = json.loads(contents)
    expected = manifest["files"]
    seen = set()
    for path in directory.rglob("*"):
        if path.is_symlink(): raise ValueError("Mesa runtime may not contain symlinks")
        if path.is_dir(): continue
        relative = path.relative_to(directory).as_posix()
        if relative == "runtime.json": continue
        if not path.is_file() or relative not in expected:
            raise ValueError("Mesa runtime has an unexpected file")
        info = expected[relative]
        digest = hashlib.sha256()
        with path.open("rb") as stream:
            for block in iter(lambda: stream.read(1024*1024), b""):
                digest.update(block)
        actual = digest.hexdigest()
        if path.stat().st_size != info["size"] or actual != info["sha256"]:
            raise ValueError("Mesa runtime file mismatch: "+relative)
        seen.add(relative)
    if seen != set(expected) or manifest["schema"] != 3 or manifest["runtime_id"] != RUNTIME_ID:
        raise ValueError("Mesa runtime is incomplete")
    return manifest

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory",type=Path)
    args = parser.parse_args()
    try:
        manifest = verify(args.directory)
        print(json.dumps({"runtime_id":manifest["runtime_id"],"files":len(manifest["files"])}))
    except (OSError,ValueError,KeyError) as error:
        parser.exit(1,f"refusing: {error}\n")
