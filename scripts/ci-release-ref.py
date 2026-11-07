#!/usr/bin/env python3
"""Validate a release candidate without creating tags, commits, or releases."""
import os
from pathlib import Path
import re
import subprocess
import sys


def git(*args):
    return subprocess.check_output(["git", *args], text=True)


def validate(requested, event, ref, version):
    if re.fullmatch(r"[0-9a-f]{40}", requested) is None:
        raise ValueError("requested revision must be exactly 40 lowercase hexadecimal characters")
    if git("rev-parse", "HEAD").strip() != requested:
        raise ValueError("checkout HEAD does not equal the requested exact SHA")
    if git("status", "--porcelain", "--untracked-files=all"):
        raise ValueError("release checkout is not clean")
    if event == "push":
        if ref != "refs/tags/v" + version.rstrip("\n"):
            raise ValueError("release tag must equal v plus the tracked VERSION")
    elif event != "workflow_dispatch":
        raise ValueError("release verification accepts only tag pushes or explicit dispatch")
    return requested


def main():
    try:
        sha = validate(os.environ.get("REQUESTED_SHA", ""),
                       os.environ.get("GITHUB_EVENT_NAME", ""),
                       os.environ.get("GITHUB_REF", ""), Path("VERSION").read_text())
    except (ValueError, OSError, subprocess.CalledProcessError) as exc:
        print("release candidate rejected: " + str(exc), file=sys.stderr)
        return 1
    print("validated exact release candidate: " + sha)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
