#!/usr/bin/env python3
"""Resolve pinned borrowed-base IDs across classic and containerd image stores."""
import json
import subprocess
import sys

# Registry indexes stay pinned. Classic stores expose the selected config ID;
# containerd stores expose the index ID. Both identities describe linux/amd64.
BASES = (
    "alpine@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc",
    "nginx@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10",
)
CONFIG_IDS = {
    BASES[0]: "sha256:bf8527eb54c3680e728d5b4b383a8ba730d72dae7236fbc8dff97ed6b224a731",
    BASES[1]: "sha256:6769dc3a703c719c1d2756bda113659be28ae16cf0da58dd5fd823d6b9a050ea",
}


def validate_inspect(reference, info):
    if reference not in CONFIG_IDS:
        raise ValueError("unknown borrowed-base reference")
    if not isinstance(info, list) or len(info) != 1 or not isinstance(info[0], dict):
        raise ValueError("base inspect must contain exactly one image")
    image = info[0]
    digests = image.get("RepoDigests")
    accepted_ids = (reference.split("@", 1)[1], CONFIG_IDS[reference])
    if (image.get("Id") not in accepted_ids
            or image.get("Os") != "linux" or image.get("Architecture") != "amd64"
            or not isinstance(digests, list) or not all(isinstance(value, str) for value in digests)
            or reference not in digests):
        raise ValueError("base does not match the verifier's exact identity/platform: " + reference)
    return image["Id"]


def main():
    try:
        if len(sys.argv) != 2 or sys.argv[1] not in CONFIG_IDS:
            raise ValueError("exactly one known pinned borrowed-base reference is required")
        reference = sys.argv[1]
        info = json.loads(subprocess.check_output(["docker", "image", "inspect", reference], text=True))
        image_id = validate_inspect(reference, info)
    except (ValueError, OSError, subprocess.CalledProcessError) as exc:
        print("borrowed base identity rejected: " + str(exc), file=sys.stderr)
        return 1
    print(image_id)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
