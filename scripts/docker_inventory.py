#!/usr/bin/env python3
"""Write a stable, canonical projection of shared Docker state."""

import json
import subprocess
import sys
from typing import Any


def docker_json(kind: str, identifiers: list[str]) -> list[dict[str, Any]]:
    if not identifiers:
        return []
    completed = subprocess.run(
        ["docker", kind, "inspect", *identifiers],
        check=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    value = json.loads(completed.stdout)
    if not isinstance(value, list) or not all(isinstance(item, dict) for item in value):
        raise ValueError(f"docker {kind} inspect returned a non-array payload")
    return value


def docker_ids(kind: str) -> list[str]:
    command = ["docker", kind, "ls"]
    if kind in ("image", "container"):
        command.append("--all")
    command.extend(("--quiet", "--no-trunc"))
    completed = subprocess.run(
        command,
        check=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    return sorted(set(line for line in completed.stdout.splitlines() if line))


def sorted_labels(value: Any) -> list[list[str]]:
    if value is None:
        return []
    if not isinstance(value, dict) or not all(isinstance(key, str) and isinstance(item, str) for key, item in value.items()):
        raise ValueError("Docker labels must be a string map")
    return [[key, value[key]] for key in sorted(value)]


def project_images(items: list[dict[str, Any]]) -> list[dict[str, Any]]:
    projected = []
    for item in items:
        projected.append(
            {
                "id": item.get("Id", ""),
                "tags": sorted(item.get("RepoTags") or []),
                "digests": sorted(item.get("RepoDigests") or []),
            }
        )
    return sorted(projected, key=lambda item: (item["id"], item["tags"], item["digests"]))


def project_containers(items: list[dict[str, Any]]) -> list[dict[str, Any]]:
    projected = []
    for item in items:
        settings = item.get("NetworkSettings") or {}
        networks = settings.get("Networks") or {}
        attachments = []
        for name in sorted(networks):
            attachment = networks[name] or {}
            attachments.append(
                {
                    "name": name,
                    "network_id": attachment.get("NetworkID", ""),
                    "endpoint_id": attachment.get("EndpointID", ""),
                    "aliases": sorted(attachment.get("Aliases") or []),
                    "gateway": attachment.get("Gateway", ""),
                    "ip_address": attachment.get("IPAddress", ""),
                    "ip_prefix_len": attachment.get("IPPrefixLen", 0),
                    "global_ipv6_address": attachment.get("GlobalIPv6Address", ""),
                    "global_ipv6_prefix_len": attachment.get("GlobalIPv6PrefixLen", 0),
                    "mac_address": attachment.get("MacAddress", ""),
                }
            )
        config = item.get("Config") or {}
        projected.append(
            {
                "id": item.get("Id", ""),
                "name": item.get("Name", ""),
                "image": item.get("Image", ""),
                "labels": sorted_labels(config.get("Labels")),
                "networks": attachments,
            }
        )
    return sorted(projected, key=lambda item: (item["id"], item["name"]))


def project_networks(items: list[dict[str, Any]]) -> list[dict[str, Any]]:
    projected = []
    for item in items:
        members = []
        for container_id, endpoint_value in (item.get("Containers") or {}).items():
            endpoint = endpoint_value or {}
            members.append(
                {
                    "container_id": container_id,
                    "name": endpoint.get("Name", ""),
                    "endpoint_id": endpoint.get("EndpointID", ""),
                    "mac_address": endpoint.get("MacAddress", ""),
                    "ipv4_address": endpoint.get("IPv4Address", ""),
                    "ipv6_address": endpoint.get("IPv6Address", ""),
                }
            )
        members.sort(key=lambda member: (member["container_id"], member["endpoint_id"]))
        projected.append(
            {
                "id": item.get("Id", ""),
                "name": item.get("Name", ""),
                "driver": item.get("Driver", ""),
                "scope": item.get("Scope", ""),
                "labels": sorted_labels(item.get("Labels")),
                "members": members,
            }
        )
    return sorted(projected, key=lambda item: (item["id"], item["name"]))


def main() -> int:
    if len(sys.argv) != 1:
        print(f"usage: {sys.argv[0]}", file=sys.stderr)
        return 2
    inventory = {
        "images": project_images(docker_json("image", docker_ids("image"))),
        "containers": project_containers(docker_json("container", docker_ids("container"))),
        "networks": project_networks(docker_json("network", docker_ids("network"))),
    }
    json.dump(inventory, sys.stdout, sort_keys=True, separators=(",", ":"))
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (json.JSONDecodeError, OSError, subprocess.CalledProcessError, ValueError) as exc:
        print(f"could not capture canonical Docker inventory: {exc}", file=sys.stderr)
        raise SystemExit(1)
