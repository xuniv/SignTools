#!/usr/bin/env python3
"""Collect GoReleaser's named binary artifacts and verify their checksums."""

import argparse
import hashlib
import json
from pathlib import Path
import shutil


def collect(dist: Path, output: Path) -> int:
    dist = dist.resolve()
    artifacts = json.loads((dist / "artifacts.json").read_text())
    selected = [
        item for item in artifacts
        if item.get("type") == "Checksum"
        or (item.get("type") == "Binary" and item.get("extra", {}).get("Format") == "binary")
    ]
    files = {}
    for item in selected:
        name = item["name"]
        if Path(name).name != name or name in ("", ".", "..") or "\\" in name:
            raise ValueError("invalid release artifact name")
        # Metadata paths are relative to the repository, including dist/.
        source = Path(item["path"])
        if not source.is_absolute():
            source = dist.parent / source
        source = source.resolve()
        if not source.is_relative_to(dist) or not source.is_file():
            raise ValueError(f"missing or invalid release artifact: {name}")
        if name in files:
            raise ValueError(f"duplicate release artifact: {name}")
        files[name] = source
    if "checksums.txt" not in files:
        raise ValueError("release checksums are missing")
    checksums = {}
    for line in files["checksums.txt"].read_text().splitlines():
        digest, name = line.split(maxsplit=1)
        checksums[name.lstrip("*")] = digest
    binaries = set(files) - {"checksums.txt"}
    if not binaries or binaries != set(checksums):
        raise ValueError("release binaries do not match the checksum manifest")
    for name in binaries:
        with files[name].open("rb") as handle:
            digest = hashlib.file_digest(handle, "sha256").hexdigest()
        if digest != checksums[name]:
            raise ValueError(f"release checksum mismatch: {name}")
    output.mkdir(parents=True, exist_ok=True)
    if any(output.iterdir()):
        raise ValueError("release output directory must be empty")
    for name, source in files.items():
        shutil.copy2(source, output / name)
    return len(binaries)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dist", type=Path, default=Path("dist"))
    parser.add_argument("--output", type=Path, default=Path("release-assets"))
    args = parser.parse_args()
    print(f"Collected and verified {collect(args.dist, args.output)} release binaries.")
