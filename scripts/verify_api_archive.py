#!/usr/bin/env python3
"""Dependency-free, bounded, offline validation of an API Docker archive."""
from __future__ import annotations

import argparse
import dataclasses
import gzip
import hashlib
import io
import json
import os
import posixpath
import re
import stat
import sys
import tarfile
from typing import Any, BinaryIO


class ValidationError(Exception):
    """The archive does not satisfy the closed API image contract."""


@dataclasses.dataclass(frozen=True)
class Limits:
    archive_bytes: int = 512 * 1024 * 1024
    members: int = 4096
    member_bytes: int = 256 * 1024 * 1024
    member_total_bytes: int = 512 * 1024 * 1024
    layers: int = 64
    layer_bytes: int = 256 * 1024 * 1024
    expanded_layer_bytes: int = 512 * 1024 * 1024
    selected_bytes: int = 128 * 1024 * 1024


@dataclasses.dataclass(frozen=True)
class Policy:
    base_layer_digests: tuple[str, ...]
    base_diff_ids: tuple[str, ...]
    base_history: tuple[dict[str, Any], ...]
    traceroute_sha256: str
    go_version: str
    go_path: str
    path_env: str


@dataclasses.dataclass(frozen=True)
class ProjectedFile:
    data: bytes
    mode: int


PINNED_ALPINE_IMAGE_DIGEST = "sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc"
PINNED_ALPINE_LAYER_DIGESTS = (
    "sha256:25f1d6b1951ac8eb3740558fe94cb83d377bdadf95fd9f98b50d2e1b96130471",
)
PINNED_ALPINE_DIFF_IDS = (
    "sha256:08bc4e534116aa76b16015484b82eac51f9a593416feae9296c8a2d4bb7aa4a2",
)
PINNED_ALPINE_HISTORY = (
    {
        "created": "2026-04-16T23:53:26.803599608Z",
        "created_by": "ADD alpine-minirootfs-3.20.10-x86_64.tar.gz / # buildkit",
        "comment": "buildkit.dockerfile.v0",
    },
    {
        "created": "2026-04-16T23:53:26.803599608Z",
        "created_by": "CMD [\"/bin/sh\"]",
        "comment": "buildkit.dockerfile.v0",
        "empty_layer": True,
    },
)
DEFAULT_POLICY = Policy(
    base_layer_digests=PINNED_ALPINE_LAYER_DIGESTS,
    base_diff_ids=PINNED_ALPINE_DIFF_IDS,
    base_history=PINNED_ALPINE_HISTORY,
    traceroute_sha256="f10fa4938a33f3b11350eb4930f4aa75aebf810264f9b7a98597788f49d1d956",
    go_version="go1.22.12",
    go_path="github.com/network-troubleshooting-company/checknetwork/cmd/checknetwork-api",
    path_env="/:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
)
SCHEMA = "checknetwork.api-archive.v1"
DIGEST = re.compile(r"sha256:[0-9a-f]{64}\Z")
BLOB_PATH = re.compile(r"blobs/sha256/([0-9a-f]{64})\Z")
HEX_40 = re.compile(r"[0-9a-f]{40}\Z")
HEX_64 = re.compile(r"[0-9a-f]{64}\Z")
GO_BUILDINFO_MAGIC = b"\xff Go buildinf:"
GO_MODULE_FRAME_START = bytes.fromhex("3077af0c9274080241e1c107e6d618e6")
GO_MODULE_FRAME_END = bytes.fromhex("f932433186182072008242104116d8f2")
EXPECTED_OUTER_METADATA = {"manifest.json", "index.json", "oci-layout"}
EXPECTED_DIRECTORIES = {"blobs", "blobs/sha256"}
EXPECTED_FILES = {"checknetwork-api", "traceroute"}


def canonical_name(name: str, *, directory: bool) -> str:
    if directory:
        name = name[:-1] if name.endswith("/") else name
    if not name or name.startswith("/") or "\\" in name or "\x00" in name:
        raise ValidationError(f"unsafe archive path: {name!r}")
    parts = name.split("/")
    if any(part in ("", ".", "..") for part in parts):
        raise ValidationError(f"noncanonical archive path: {name!r}")
    if posixpath.normpath(name) != name:
        raise ValidationError(f"noncanonical archive path: {name!r}")
    return name


def _json_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise ValidationError(f"duplicate JSON key: {key}")
        result[key] = value
    return result


def load_json(data: bytes, what: str) -> Any:
    try:
        return json.loads(data.decode("utf-8"), object_pairs_hook=_json_object)
    except ValidationError:
        raise
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise ValidationError(f"malformed {what} JSON: {exc}") from exc


def _read_bounded(stream: BinaryIO, maximum: int, what: str) -> bytes:
    chunks: list[bytes] = []
    total = 0
    while True:
        chunk = stream.read(min(1024 * 1024, maximum + 1 - total))
        if not chunk:
            break
        chunks.append(chunk)
        total += len(chunk)
        if total > maximum:
            raise ValidationError(f"{what} exceeds byte limit")
    return b"".join(chunks)


def _hash_file(path: str, maximum: int) -> str:
    digest = hashlib.sha256()
    total = 0
    with open(path, "rb", buffering=0) as source:
        while True:
            chunk = source.read(1024 * 1024)
            if not chunk:
                break
            total += len(chunk)
            if total > maximum:
                raise ValidationError("archive exceeds byte limit")
            digest.update(chunk)
    return digest.hexdigest()


def _validate_limits(limits: Limits) -> None:
    for field in dataclasses.fields(limits):
        if getattr(limits, field.name) <= 0:
            raise ValidationError(f"invalid non-positive limit: {field.name}")


def _read_outer(path: str, limits: Limits) -> tuple[dict[str, bytes], set[str]]:
    try:
        metadata = os.lstat(path)
    except OSError as exc:
        raise ValidationError(f"cannot stat Docker archive: {exc}") from exc
    if not stat.S_ISREG(metadata.st_mode) or os.path.islink(path):
        raise ValidationError("Docker archive must be a regular non-symlink file")
    if metadata.st_size > limits.archive_bytes:
        raise ValidationError("archive exceeds byte limit")
    contents: dict[str, bytes] = {}
    directories: set[str] = set()
    seen: set[str] = set()
    total_bytes = 0
    try:
        archive = tarfile.open(path, mode="r:*")
        with archive:
            count = 0
            for member in archive:
                count += 1
                if count > limits.members:
                    raise ValidationError("archive member count exceeds limit")
                name = canonical_name(member.name, directory=member.isdir())
                if name in seen:
                    raise ValidationError(f"duplicate archive path: {name}")
                seen.add(name)
                if member.isdir():
                    directories.add(name)
                    continue
                if not member.isreg():
                    raise ValidationError(f"archive links/devices/FIFO/socket are forbidden: {name}")
                if member.size < 0 or member.size > limits.member_bytes:
                    raise ValidationError(f"archive member exceeds byte limit: {name}")
                total_bytes += member.size
                if total_bytes > limits.member_total_bytes:
                    raise ValidationError("archive member bytes exceed total limit")
                source = archive.extractfile(member)
                if source is None:
                    raise ValidationError(f"archive member has no bytes: {name}")
                data = _read_bounded(source, limits.member_bytes, f"archive member {name}")
                if len(data) != member.size:
                    raise ValidationError(f"archive member is truncated: {name}")
                contents[name] = data
    except ValidationError:
        raise
    except (OSError, EOFError, tarfile.TarError) as exc:
        raise ValidationError(f"malformed Docker archive: {exc}") from exc
    return contents, directories


def _blob_digest(name: str, data: bytes) -> str:
    match = BLOB_PATH.fullmatch(name)
    if match is None:
        raise ValidationError(f"noncanonical blob path: {name}")
    actual = hashlib.sha256(data).hexdigest()
    if actual != match.group(1):
        raise ValidationError(f"blob path/digest mismatch: {name}")
    return "sha256:" + actual


def _require_digest(value: Any, what: str) -> str:
    if not isinstance(value, str) or DIGEST.fullmatch(value) is None:
        raise ValidationError(f"{what} digest is malformed")
    return value


def _blob_name(digest: str) -> str:
    return "blobs/sha256/" + digest.removeprefix("sha256:")


def expanded_layer(layer: bytes, limits: Limits) -> bytes:
    if len(layer) > limits.layer_bytes:
        raise ValidationError("layer exceeds byte limit")
    if not layer.startswith(b"\x1f\x8b"):
        if len(layer) > limits.expanded_layer_bytes:
            raise ValidationError("expanded layer exceeds byte limit")
        return layer
    try:
        with gzip.GzipFile(fileobj=io.BytesIO(layer), mode="rb") as stream:
            return _read_bounded(stream, limits.expanded_layer_bytes, "expanded layer")
    except ValidationError:
        raise
    except (OSError, EOFError) as exc:
        raise ValidationError(f"malformed compressed layer: {exc}") from exc


def _selected_name(name: str) -> bool:
    first = name.split("/", 1)[0]
    return first == "checknetwork-api" or first.startswith("checknetwork-api.") or first == "traceroute" or first.startswith("traceroute.")


def _validate_base_link_target(name: str, target: str, *, hardlink: bool) -> None:
    if not target or "\\" in target or "\x00" in target or target == "/":
        raise ValidationError(f"unsafe base-layer link target: {name} -> {target!r}")
    absolute = target.startswith("/")
    target_parts = target.removeprefix("/").split("/")
    if any(part in ("", ".") for part in target_parts) or posixpath.normpath(target) != target:
        raise ValidationError(f"unsafe base-layer link target: {name} -> {target!r}")
    if absolute:
        if any(part == ".." for part in target_parts):
            raise ValidationError(f"unsafe base-layer link target: {name} -> {target!r}")
        return
    resolved = posixpath.normpath(target if hardlink else posixpath.join(posixpath.dirname(name), target))
    if resolved == ".." or resolved.startswith("../") or resolved.startswith("/"):
        raise ValidationError(f"unsafe base-layer link target: {name} -> {target!r}")


def _apply_whiteout(name: str, projection: dict[str, ProjectedFile]) -> None:
    directory, leaf = posixpath.split(name)
    if leaf == ".wh..wh..opq":
        prefix = directory + "/" if directory else ""
        for target in tuple(projection):
            if not directory or target.startswith(prefix):
                del projection[target]
        return
    if not leaf.startswith(".wh.") or leaf.count(".wh.") != 1:
        raise ValidationError(f"malformed whiteout: {name}")
    target_leaf = leaf[4:]
    if not target_leaf or target_leaf in (".", "..") or "/" in target_leaf or target_leaf.startswith("."):
        raise ValidationError(f"malformed whiteout: {name}")
    target = posixpath.join(directory, target_leaf)
    prefix = target + "/"
    for projected in tuple(projection):
        if projected == target or projected.startswith(prefix):
            del projection[projected]


def scan_layer(
    layer: bytes,
    limits: Limits,
    projection: dict[str, ProjectedFile],
    *,
    selected_consumed: list[int] | None = None,
    inherited_base: bool = False,
) -> bytes:
    expanded = expanded_layer(layer, limits)
    seen: set[str] = set()
    # OCI markers remove only lower-layer entries, never additions from this
    # layer. Merge additions after scanning to make tar ordering irrelevant.
    additions: dict[str, ProjectedFile] = {}
    consumed = selected_consumed if selected_consumed is not None else [sum(len(item.data) for item in projection.values())]
    try:
        archive = tarfile.open(fileobj=io.BytesIO(expanded), mode="r:")
        with archive:
            count = 0
            for member in archive:
                count += 1
                if count > limits.members:
                    raise ValidationError("layer member count exceeds limit")
                name = canonical_name(member.name, directory=member.isdir())
                if name in seen:
                    raise ValidationError(f"duplicate layer path: {name}")
                seen.add(name)
                leaf = posixpath.basename(name)
                if ".wh." in leaf:
                    if not member.isreg() or member.size != 0:
                        raise ValidationError(f"whiteout is not an empty regular file: {name}")
                    _apply_whiteout(name, projection)
                    continue
                if _selected_name(name) and (not member.isreg() or stat.S_IMODE(member.mode) & 0o111 == 0):
                    raise ValidationError(f"selected member must be a regular executable non-link: {name}")
                if member.isdir():
                    if _selected_name(name):
                        raise ValidationError(f"selected extra directory is forbidden: {name}")
                    continue
                if not member.isreg():
                    if not inherited_base:
                        raise ValidationError(f"post-base layer links/devices/FIFO/socket are forbidden: {name}")
                    if member.issym() or member.islnk():
                        _validate_base_link_target(name, member.linkname, hardlink=member.islnk())
                        continue
                    if member.ischr() or member.isblk():
                        if member.size != 0 or member.devmajor < 0 or member.devminor < 0:
                            raise ValidationError(f"malformed base-layer device: {name}")
                        continue
                    if member.isfifo() or member.type == b"s":
                        raise ValidationError(f"base layer FIFO/socket is forbidden: {name}")
                    raise ValidationError(f"unsupported base-layer member type: {name}")
                if member.size < 0 or member.size > limits.member_bytes:
                    raise ValidationError(f"layer member exceeds byte limit: {name}")
                if not _selected_name(name):
                    continue
                source = archive.extractfile(member)
                if source is None:
                    raise ValidationError(f"selected member has no bytes: {name}")
                data = _read_bounded(source, limits.member_bytes, f"selected member {name}")
                if len(data) != member.size:
                    raise ValidationError(f"selected member is truncated: {name}")
                consumed[0] += len(data)
                if consumed[0] > limits.selected_bytes:
                    raise ValidationError("expanded selected-file bytes exceed limit")
                additions[name] = ProjectedFile(data=data, mode=stat.S_IMODE(member.mode))
    except ValidationError:
        raise
    except (EOFError, OSError, tarfile.TarError) as exc:
        raise ValidationError(f"malformed layer tar: {exc}") from exc
    projection.update(additions)
    return expanded


def _docker_manifest(contents: dict[str, bytes], limits: Limits) -> dict[str, Any]:
    raw = contents.get("manifest.json")
    if raw is None:
        raise ValidationError("Docker archive has no manifest.json")
    manifest = load_json(raw, "Docker manifest")
    if not isinstance(manifest, list) or len(manifest) != 1 or not isinstance(manifest[0], dict):
        raise ValidationError("Docker archive must contain exactly one manifest")
    entry = manifest[0]
    if set(entry) - {"Config", "RepoTags", "Layers", "LayerSources"}:
        raise ValidationError("Docker manifest has unexpected fields")
    if entry.get("RepoTags") not in (None, []):
        raise ValidationError("Docker archive must be tagless")
    config_name = entry.get("Config")
    layers = entry.get("Layers")
    if not isinstance(config_name, str) or not isinstance(layers, list) or not layers:
        raise ValidationError("Docker manifest config/layers are malformed")
    if len(layers) > limits.layers or any(not isinstance(name, str) for name in layers):
        raise ValidationError("Docker manifest layer count or values are invalid")
    if len(set(layers)) != len(layers):
        raise ValidationError("Docker manifest has duplicate layer ambiguity")
    canonical_name(config_name, directory=False)
    for name in layers:
        canonical_name(name, directory=False)
    return entry


def _validate_oci(contents: dict[str, bytes], entry: dict[str, Any], layer_digests: list[str]) -> tuple[str, set[str]]:
    layout = load_json(contents.get("oci-layout", b""), "OCI layout")
    if layout != {"imageLayoutVersion": "1.0.0"}:
        raise ValidationError("OCI layout is malformed or unexpected")
    index = load_json(contents.get("index.json", b""), "OCI index")
    if not isinstance(index, dict) or index.get("schemaVersion") != 2:
        raise ValidationError("OCI index is malformed or has unexpected fields")
    docker_media = set(index) == {"schemaVersion", "mediaType", "manifests"}
    if docker_media:
        if index.get("mediaType") != "application/vnd.oci.image.index.v1+json":
            raise ValidationError("OCI index media type is wrong")
    elif set(index) != {"schemaVersion", "manifests"}:
        raise ValidationError("OCI index is malformed or has unexpected fields")
    descriptors = index.get("manifests")
    if not isinstance(descriptors, list) or len(descriptors) != 1 or not isinstance(descriptors[0], dict):
        raise ValidationError("OCI index must reference exactly one manifest")
    descriptor = descriptors[0]
    expected_descriptor_fields = {"mediaType", "digest", "size", "annotations", "platform"} if docker_media else {"mediaType", "digest", "size"}
    if set(descriptor) != expected_descriptor_fields:
        raise ValidationError("OCI manifest descriptor has unexpected fields")
    expected_manifest_media = "application/vnd.docker.distribution.manifest.v2+json" if docker_media else "application/vnd.oci.image.manifest.v1+json"
    if descriptor.get("mediaType") != expected_manifest_media:
        raise ValidationError("OCI manifest descriptor media type is wrong")
    if docker_media:
        annotations = descriptor.get("annotations")
        created = annotations.get("org.opencontainers.image.created") if isinstance(annotations, dict) and set(annotations) == {"org.opencontainers.image.created"} else None
        if not isinstance(created, str) or re.fullmatch(r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]+)?Z", created) is None:
            raise ValidationError("OCI manifest descriptor created annotation is malformed")
        if descriptor.get("platform") != {"architecture": "amd64", "os": "linux"}:
            raise ValidationError("OCI manifest descriptor platform is not exact linux/amd64")
    digest = _require_digest(descriptor.get("digest"), "OCI manifest")
    name = _blob_name(digest)
    data = contents.get(name)
    if data is None:
        raise ValidationError("referenced OCI manifest is missing")
    if descriptor.get("size") != len(data) or _blob_digest(name, data) != digest:
        raise ValidationError("OCI manifest descriptor size/digest mismatch")
    manifest = load_json(data, "OCI manifest")
    if not isinstance(manifest, dict) or set(manifest) != {"schemaVersion", "mediaType", "config", "layers"}:
        raise ValidationError("OCI manifest is malformed or has unexpected fields")
    if manifest.get("schemaVersion") != 2 or manifest.get("mediaType") != expected_manifest_media:
        raise ValidationError("OCI manifest schema/media type is wrong")
    config_descriptor = manifest.get("config")
    layers = manifest.get("layers")
    if not isinstance(config_descriptor, dict) or not isinstance(layers, list):
        raise ValidationError("OCI manifest descriptors are malformed")
    expected_config = _blob_digest(entry["Config"], contents[entry["Config"]])
    if config_descriptor.get("digest") != expected_config or config_descriptor.get("size") != len(contents[entry["Config"]]):
        raise ValidationError("OCI and Docker manifests disagree on config")
    expected_config_media = "application/vnd.docker.container.image.v1+json" if docker_media else "application/vnd.oci.image.config.v1+json"
    if config_descriptor.get("mediaType") != expected_config_media:
        raise ValidationError("OCI config media type is wrong")
    if len(layers) != len(layer_digests):
        raise ValidationError("OCI and Docker manifests disagree on layer count/order")
    for index_value, (layer_descriptor, expected_digest) in enumerate(zip(layers, layer_digests)):
        if not isinstance(layer_descriptor, dict) or set(layer_descriptor) != {"mediaType", "digest", "size"}:
            raise ValidationError("OCI layer descriptor is malformed or has unexpected fields")
        if layer_descriptor.get("digest") != expected_digest:
            raise ValidationError("OCI and Docker manifests disagree on layer order/digests")
        data_value = contents[entry["Layers"][index_value]]
        if layer_descriptor.get("size") != len(data_value):
            raise ValidationError("OCI layer descriptor size mismatch")
        allowed = {"application/vnd.docker.image.rootfs.diff.tar.gzip"} if docker_media else {"application/vnd.oci.image.layer.v1.tar", "application/vnd.oci.image.layer.v1.tar+gzip"}
        if layer_descriptor.get("mediaType") not in allowed:
            raise ValidationError("OCI layer media type is wrong")
        is_gzip = data_value.startswith(b"\x1f\x8b")
        declares_gzip = layer_descriptor.get("mediaType") in {
            "application/vnd.docker.image.rootfs.diff.tar.gzip",
            "application/vnd.oci.image.layer.v1.tar+gzip",
        }
        if is_gzip != declares_gzip:
            raise ValidationError("OCI layer compression does not match media type")
    return digest, {name}


def _uvarint(data: bytes, offset: int) -> tuple[int, int]:
    value = 0
    for shift in range(0, 70, 7):
        if offset >= len(data):
            raise ValidationError("truncated Go build information")
        byte = data[offset]
        offset += 1
        value |= (byte & 0x7F) << shift
        if byte < 0x80:
            return value, offset
    raise ValidationError("invalid Go build information varint")


def _inline_string(data: bytes, offset: int) -> tuple[bytes, int]:
    length, offset = _uvarint(data, offset)
    if length > len(data) - offset:
        raise ValidationError("truncated Go build information string")
    return data[offset:offset + length], offset + length


def go_build_identity(binary: bytes, policy: Policy, version: str, revision: str) -> tuple[str, str]:
    positions = []
    start = 0
    while True:
        found = binary.find(GO_BUILDINFO_MAGIC, start)
        if found < 0:
            break
        positions.append(found)
        start = found + 1
    if len(positions) != 1:
        raise ValidationError("Go build information must occur exactly once")
    offset = positions[0]
    if offset + 32 > len(binary):
        raise ValidationError("truncated Go build information header")
    pointer_size = binary[offset + 14]
    flags = binary[offset + 15]
    if pointer_size not in (4, 8) or flags & 2 == 0:
        raise ValidationError("unsupported Go build information encoding")
    go_version_raw, next_offset = _inline_string(binary, offset + 32)
    module_raw, _ = _inline_string(binary, next_offset)
    if module_raw.startswith(GO_MODULE_FRAME_START) and module_raw.endswith(GO_MODULE_FRAME_END):
        module_raw = module_raw[len(GO_MODULE_FRAME_START):-len(GO_MODULE_FRAME_END)]
    try:
        go_version = go_version_raw.decode("utf-8")
        module = module_raw.decode("utf-8", errors="strict")
    except UnicodeDecodeError as exc:
        raise ValidationError(f"invalid UTF-8 in Go build information: {exc}") from exc
    path_at = module.find("path\t")
    if path_at < 0:
        raise ValidationError("Go path is missing")
    module = module[path_at:]
    lines = module.splitlines()
    path_lines = [line[5:] for line in lines if line.startswith("path\t")]
    if go_version != policy.go_version:
        raise ValidationError(f"Go version mismatch: {go_version!r}")
    if path_lines != [policy.go_path]:
        raise ValidationError(f"Go path mismatch: {path_lines!r}")
    version_identity = "main.version=" + version
    revision_identity = "main.revision=" + revision
    linker_settings_present = version_identity in module and revision_identity in module
    linked_values_present = binary.count(version.encode("utf-8")) == 1 and binary.count(revision.encode("ascii")) == 1
    if not linker_settings_present and not linked_values_present:
        raise ValidationError("Go linker identity does not match expected version/revision")
    return go_version, path_lines[0]


def _validate_config(config: Any, version: str, revision: str, policy: Policy, diff_ids: list[str]) -> dict[str, Any]:
    if not isinstance(config, dict):
        raise ValidationError("image config must be an object")
    if config.get("architecture") != "amd64" or config.get("os") != "linux":
        raise ValidationError("image platform must be linux/amd64")
    rootfs = config.get("rootfs")
    if not isinstance(rootfs, dict) or set(rootfs) != {"type", "diff_ids"} or rootfs.get("type") != "layers" or not isinstance(rootfs.get("diff_ids"), list):
        raise ValidationError("image rootfs is malformed")
    if rootfs["diff_ids"] != diff_ids:
        raise ValidationError("config rootfs identities do not match ordered layers")
    base_count = len(policy.base_diff_ids)
    if tuple(rootfs["diff_ids"][:base_count]) != policy.base_diff_ids:
        raise ValidationError("image does not have the exact pinned Alpine base rootfs prefix")
    history = config.get("history")
    if not isinstance(history, list) or tuple(history[:len(policy.base_history)]) != policy.base_history:
        raise ValidationError("image does not have the exact pinned Alpine base history prefix")
    runtime = config.get("config")
    if not isinstance(runtime, dict):
        raise ValidationError("runtime config is missing")
    labels = runtime.get("Labels")
    if not isinstance(labels, dict) or labels.get("org.opencontainers.image.version") != version or labels.get("org.opencontainers.image.revision") != revision:
        raise ValidationError("OCI version/revision labels do not match")
    if runtime.get("User") != "65532:65532":
        raise ValidationError("runtime User must be exactly 65532:65532")
    if runtime.get("Entrypoint") != ["/checknetwork-api"]:
        raise ValidationError("runtime Entrypoint must be exactly /checknetwork-api")
    env = runtime.get("Env")
    if not isinstance(env, list) or [item for item in env if isinstance(item, str) and item.startswith("PATH=")] != ["PATH=" + policy.path_env]:
        raise ValidationError("runtime PATH is not exact")
    if runtime.get("ExposedPorts") != {"8080/tcp": {}}:
        raise ValidationError("runtime exposed port must be exactly 8080/tcp")
    return rootfs


def _safe_extract(extract_dir: str, files: dict[str, ProjectedFile], hashes: dict[str, str]) -> None:
    flags = os.O_RDONLY | getattr(os, "O_DIRECTORY", 0) | getattr(os, "O_NOFOLLOW", 0)
    try:
        directory_fd = os.open(extract_dir, flags)
    except OSError as exc:
        raise ValidationError(f"extraction target must be a real directory: {exc}") from exc
    created: list[str] = []
    try:
        metadata = os.fstat(directory_fd)
        if not stat.S_ISDIR(metadata.st_mode):
            raise ValidationError("extraction target must be a real directory")
        if metadata.st_uid != os.geteuid():
            raise ValidationError("extraction target must be caller-owned")
        if os.listdir(directory_fd):
            raise ValidationError("extraction target must be empty")
        for name in sorted(EXPECTED_FILES):
            value = files[name]
            output_flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0)
            try:
                fd = os.open(name, output_flags, value.mode, dir_fd=directory_fd)
            except OSError as exc:
                raise ValidationError(f"safe extraction create failed for {name}: {exc}") from exc
            created.append(name)
            try:
                os.fchmod(fd, value.mode)
                view = memoryview(value.data)
                while view:
                    written = os.write(fd, view)
                    if written <= 0:
                        raise ValidationError(f"short write while extracting {name}")
                    view = view[written:]
                os.fsync(fd)
            finally:
                os.close(fd)
        for name in sorted(EXPECTED_FILES):
            read_flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)
            fd = os.open(name, read_flags, dir_fd=directory_fd)
            try:
                metadata = os.fstat(fd)
                if not stat.S_ISREG(metadata.st_mode) or stat.S_IMODE(metadata.st_mode) != files[name].mode:
                    raise ValidationError(f"extracted mode/type mismatch: {name}")
                digest = hashlib.sha256()
                total = 0
                while True:
                    chunk = os.read(fd, 1024 * 1024)
                    if not chunk:
                        break
                    total += len(chunk)
                    digest.update(chunk)
                if total != len(files[name].data) or digest.hexdigest() != hashes[name]:
                    raise ValidationError(f"extracted bytes/hash mismatch: {name}")
            finally:
                os.close(fd)
    except Exception:
        for name in created:
            try:
                os.unlink(name, dir_fd=directory_fd)
            except OSError:
                pass
        raise
    finally:
        os.close(directory_fd)


def validate(
    archive_path: str,
    version: str,
    revision: str,
    *,
    limits: Limits | None = None,
    policy: Policy = DEFAULT_POLICY,
    extract_dir: str | None = None,
    binary_sha256: str | None = None,
) -> dict[str, Any]:
    limits = limits or Limits()
    _validate_limits(limits)
    if not version or "\x00" in version or "\n" in version:
        raise ValidationError("expected version is malformed")
    if HEX_40.fullmatch(revision) is None:
        raise ValidationError("expected revision is not exact lowercase 40-hex")
    if binary_sha256 is not None and HEX_64.fullmatch(binary_sha256) is None:
        raise ValidationError("expected binary SHA-256 is malformed")
    if HEX_64.fullmatch(policy.traceroute_sha256) is None:
        raise ValidationError("pinned traceroute SHA-256 is malformed")

    contents, directories = _read_outer(archive_path, limits)
    if directories - EXPECTED_DIRECTORIES:
        raise ValidationError(f"archive contains unexpected directories: {sorted(directories - EXPECTED_DIRECTORIES)}")
    entry = _docker_manifest(contents, limits)
    config_name = entry["Config"]
    if config_name not in contents:
        raise ValidationError("referenced config is missing")
    _blob_digest(config_name, contents[config_name])

    config = load_json(contents[config_name], "image config")
    layer_payloads: list[bytes] = []
    layer_digests: list[str] = []
    diff_ids: list[str] = []
    for layer_name in entry["Layers"]:
        data = contents.get(layer_name)
        if data is None:
            raise ValidationError(f"referenced layer is missing: {layer_name}")
        layer_payloads.append(data)
        layer_digests.append(_blob_digest(layer_name, data))
        expanded = expanded_layer(data, limits)
        diff_ids.append("sha256:" + hashlib.sha256(expanded).hexdigest())

    # Establish the exact rootfs/history trust boundary before interpreting any
    # layer tar member. Position alone never grants inherited-base allowances.
    rootfs = _validate_config(config, version, revision, policy, diff_ids)
    base_count = len(policy.base_diff_ids)
    if len(policy.base_layer_digests) != base_count or tuple(layer_digests[:base_count]) != policy.base_layer_digests:
        raise ValidationError("image does not have the exact pinned Alpine base layer digest prefix")

    manifest_digest, oci_references = _validate_oci(contents, entry, layer_digests)
    referenced = EXPECTED_OUTER_METADATA | {config_name, *entry["Layers"]} | oci_references
    regular_names = set(contents)
    if regular_names != referenced:
        extras = sorted(regular_names - referenced)
        missing = sorted(referenced - regular_names)
        if extras and all(name.startswith("blobs/") for name in extras):
            raise ValidationError(f"archive contains unreferenced or ambiguous blobs: {extras}")
        raise ValidationError(f"archive contains unexpected/missing regular members: extras={extras} missing={missing}")

    projection: dict[str, ProjectedFile] = {}
    selected_consumed = [0]
    for index_value, data in enumerate(layer_payloads):
        scan_layer(
            data,
            limits,
            projection,
            selected_consumed=selected_consumed,
            inherited_base=index_value < base_count,
        )
    if set(projection) != EXPECTED_FILES:
        extras = sorted(set(projection) - EXPECTED_FILES)
        missing = sorted(EXPECTED_FILES - set(projection))
        raise ValidationError(f"final selected filesystem has missing/extra entries: missing={missing} extra={extras}")
    for name in EXPECTED_FILES:
        if projection[name].mode != 0o755:
            raise ValidationError(f"canonical mode/executable mismatch: /{name}: {projection[name].mode:o}")

    hashes = {name: hashlib.sha256(projection[name].data).hexdigest() for name in EXPECTED_FILES}
    if binary_sha256 is not None and hashes["checknetwork-api"] != binary_sha256:
        raise ValidationError("binary SHA-256 does not match expected value")
    if hashes["traceroute"] != policy.traceroute_sha256:
        raise ValidationError("traceroute bytes do not match the pinned APK payload")
    go_version, go_path = go_build_identity(projection["checknetwork-api"].data, policy, version, revision)

    result: dict[str, Any] = {
        "schema": SCHEMA,
        "archive": _hash_file(archive_path, limits.archive_bytes),
        "config": _blob_digest(config_name, contents[config_name]),
        "manifest": manifest_digest,
        "rootfs": "sha256:" + hashlib.sha256(json.dumps(rootfs["diff_ids"], separators=(",", ":")).encode()).hexdigest(),
        "layers": layer_digests,
        "binary": hashes["checknetwork-api"],
        "traceroute": hashes["traceroute"],
        "go_version": go_version,
        "go_path": go_path,
    }
    if extract_dir is not None:
        _safe_extract(extract_dir, projection, hashes)
    return result


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--archive", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--revision", required=True)
    parser.add_argument("--extract-dir")
    parser.add_argument("--binary-sha256")
    args = parser.parse_args(argv)
    try:
        result = validate(
            args.archive,
            args.version,
            args.revision,
            extract_dir=args.extract_dir,
            binary_sha256=args.binary_sha256,
        )
    except (OSError, ValidationError) as exc:
        print(f"API archive validation failed: {exc}", file=sys.stderr)
        return 1
    print(json.dumps(result, sort_keys=True, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
