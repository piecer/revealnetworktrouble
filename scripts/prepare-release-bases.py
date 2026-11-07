#!/usr/bin/env python3
"""Prepare only the exact borrowed bases before release inventory capture."""
import json
import subprocess
import sys

# Match verify-release.sh's pinned borrowed-base references and immutable IDs.
BASES = (
    "alpine@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc",
    "nginx@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10",
)


def docker(*args):
    return subprocess.check_output(["docker", *args], text=True)


def prepare(call=docker):
    receipts = []
    for ref in BASES:
        call("pull", "--platform", "linux/amd64", ref)
        info = json.loads(call("image", "inspect", ref))
        if not isinstance(info, list) or len(info) != 1 or not isinstance(info[0], dict):
            raise ValueError("base inspect must contain exactly one image")
        image = info[0]
        if (image.get("Id") != ref.split("@", 1)[1]
                or image.get("Os") != "linux" or image.get("Architecture") != "amd64"
                or ref not in (image.get("RepoDigests") or [])):
            raise ValueError("base does not match the verifier's exact identity/platform: " + ref)
        receipts.append({"reference": ref, "id": image["Id"], "platform": "linux/amd64"})
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
