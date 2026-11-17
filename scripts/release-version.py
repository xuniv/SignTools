#!/usr/bin/env python3
"""Choose the next fork patch tag without changing a commit or a remote."""

import argparse
import os
from pathlib import Path
import re
import subprocess


def next_version(upstream: str, tags: list[str]) -> str:
    upstream = upstream.strip().removeprefix("v")
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", upstream):
        raise ValueError("UPSTREAM_VERSION must contain a version such as 3.1.0")
    pattern = re.compile(r"v?" + re.escape(upstream) + r"-([1-9][0-9]*)")
    patches = [int(match[1]) for tag in tags if (match := pattern.fullmatch(tag))]
    return f"{upstream}-{max(patches, default=0) + 1}"


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--upstream-file", type=Path, default=Path("UPSTREAM_VERSION"))
    parser.add_argument("--github-output", action="store_true")
    args = parser.parse_args()
    tags = subprocess.check_output(["git", "tag", "--list"], text=True).splitlines()
    version = next_version(args.upstream_file.read_text(), tags)
    tag = "v" + version
    print(tag)
    if args.github_output:
        with Path(os.environ["GITHUB_OUTPUT"]).open("a") as output:
            output.write(f"version={version}\ntag={tag}\n")


if __name__ == "__main__":
    main()
