#!/usr/bin/env python3
"""Prepare only the exact borrowed bases before release inventory capture."""
import importlib.util
import json
from pathlib import Path
import subprocess
import sys

# Import the canonical sibling even when tests load this script by file path.
_spec = importlib.util.spec_from_file_location(
    "borrowed_base_identity", Path(__file__).with_name("borrowed_base_identity.py"))
if _spec is None or _spec.loader is None:
    raise ImportError("canonical borrowed-base identity helper cannot be loaded")
_identity = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(_identity)
BASES = _identity.BASES


def docker(*args):
    return subprocess.check_output(["docker", *args], text=True)


def prepare(call=docker):
    receipts = []
    for ref in BASES:
        call("pull", "--platform", "linux/amd64", ref)
        info = json.loads(call("image", "inspect", ref))
        image_id = _identity.validate_inspect(ref, info)
        receipts.append({"reference": ref, "id": image_id, "platform": "linux/amd64"})
    return receipts


def main():
    try:
        receipts = prepare()
    except (ValueError, OSError, subprocess.CalledProcessError) as exc:
        print("release base preparation failed: " + str(exc), file=sys.stderr)
        return 1
    print(json.dumps({"prepared_bases": receipts}, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
