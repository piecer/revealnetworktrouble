#!/usr/bin/env python3
"""Bounded, offline validation of a tagless CheckNetwork Web Docker archive."""
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
from typing import Any


class ValidationError(Exception):
    pass


@dataclasses.dataclass(frozen=True)
class Limits:
    archive_bytes: int = 512 * 1024 * 1024
    members: int = 4096
    member_bytes: int = 256 * 1024 * 1024
    member_total_bytes: int = 512 * 1024 * 1024
    layers: int = 64
    layer_bytes: int = 256 * 1024 * 1024
    selected_bytes: int = 32 * 1024 * 1024


@dataclasses.dataclass(frozen=True)
class ProjectedFile:
    data: bytes
    mode: int


@dataclasses.dataclass(frozen=True)
class Policy:
    base_config_digest: str
    base_layer_digests: tuple[str, ...]
    base_diff_ids: tuple[str, ...]
    base_history_digest: str


HEX_BLOB = re.compile(r"blobs/sha256/([0-9a-f]{64})\Z")
WEB_ROOT = "usr/share/nginx/html"
CONFIG_PATH = "etc/nginx/nginx.conf"
ASSETS = ("app.js", "index.html", "state.js", "styles.css", "topology-model.js", "topology-renderer.js", "topology-visualizer.js")
IDENTITY = ".checknetwork-assets.sha256"
PINNED_NGINX_CONFIG_DIGEST = "sha256:6769dc3a703c719c1d2756bda113659be28ae16cf0da58dd5fd823d6b9a050ea"
PINNED_NGINX_LAYER_DIGESTS = (
    "sha256:f18232174bc91741fdf3da96d85011092101a032a93a388b79e99e69c2d5c870",
    "sha256:61ca4f733c802afd9e05a32f0de0361b6d713b8b53292dc15fb093229f648674",
    "sha256:b464cfdf2a6319875aeb27359ec549790ce14d8214fcb16ef915e4530e5ed235",
    "sha256:d7e5070240863957ebb0b5a44a5729963c3462666baa2947d00628cb5f2d5773",
    "sha256:81bd8ed7ec6789b0cb7f1b47ee731c522f6dba83201ec73cd6bca1350f582948",
    "sha256:197eb75867ef4fcecd4724f17b0972ab0489436860a594a9445f8eaff8155053",
    "sha256:34a64644b756511a2e217f0508e11d1a572085d66cd6dc9a555a082ad49a3102",
    "sha256:39c2ddfd6010082a4a646e7ca44e95aca9bf3eaebc00f17f7ccc2954004f2a7d",
)
PINNED_NGINX_DIFF_IDS = (
    "sha256:08000c18d16dadf9553d747a58cf44023423a9ab010aab96cf263d2216b8b350",
    "sha256:d71eae0084c1aa823dd8fb2ecf8604d5c0f4911226c042bb1f8297e819f4b192",
    "sha256:c56f134d380585340a68d0db2f2c170641a1c0ff72ccf2438cf2f693df756a85",
    "sha256:e244aa659f612a80c40dd8645812301e3def6b15ec67b9e486ed2201172b51d1",
    "sha256:b8d7d1d2263425d6044e059b2810017d062d659b9b755241f3747eda77726250",
    "sha256:811a4dbbf4a5309e4390cf655c12db92e1a4304fb9d9731f83e7b02e95a617c6",
    "sha256:947e805a4ac71f68e6703550c0b36c2aa2e554c4fa670ca2da6a25c6d7dccb66",
    "sha256:0d853d50b128aa460b47e7121849463a14b18d4fd976caf5014744aae24d28aa",
)
PINNED_NGINX_HISTORY_DIGEST = "sha256:68c8152f89b0ff00509214a85e3be7dac3451fd914c15f2ef15067024da9e5a2"
DEFAULT_POLICY = Policy(
    base_config_digest=PINNED_NGINX_CONFIG_DIGEST,
    base_layer_digests=PINNED_NGINX_LAYER_DIGESTS,
    base_diff_ids=PINNED_NGINX_DIFF_IDS,
    base_history_digest=PINNED_NGINX_HISTORY_DIGEST,
)


def safe_name(name: str) -> str:
    if not name or name.startswith("/") or "\\" in name or "\x00" in name:
        raise ValidationError(f"unsafe archive path: {name!r}")
    components = name.split("/")
    if any(part in ("", ".", "..") for part in components):
        raise ValidationError(f"noncanonical archive path: {name!r}")
    normalized = posixpath.normpath(name)
    if normalized != name or normalized.startswith("../"):
        raise ValidationError(f"escaping archive path: {name!r}")
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
    except (UnicodeDecodeError, json.JSONDecodeError, ValidationError) as exc:
        raise ValidationError(f"malformed {what} JSON: {exc}") from exc


def _bounded_read(stream: Any, maximum: int, what: str) -> bytes:
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


def _validate_limits(limits: Limits) -> None:
    for field in dataclasses.fields(limits):
        if getattr(limits, field.name) <= 0:
            raise ValidationError(f"invalid non-positive limit: {field.name}")


def _is_selected(name: str) -> bool:
    return name == CONFIG_PATH or name == WEB_ROOT or name.startswith(WEB_ROOT + "/")


def expanded_layer(layer_data: bytes, limits: Limits) -> bytes:
    if len(layer_data) > limits.layer_bytes:
        raise ValidationError("layer exceeds byte limit")
    if not layer_data.startswith(b"\x1f\x8b"):
        return layer_data
    try:
        return _bounded_read(gzip.GzipFile(fileobj=io.BytesIO(layer_data)), limits.layer_bytes, "expanded layer")
    except (OSError, EOFError) as exc:
        raise ValidationError(f"malformed compressed layer: {exc}") from exc


def apply_whiteout(name: str, projection: dict[str, ProjectedFile]) -> None:
    safe_name(name)
    directory, leaf = posixpath.split(name)
    if ".wh." not in leaf or not leaf.startswith(".wh."):
        raise ValidationError(f"malformed or unsupported whiteout: {name}")
    if leaf == ".wh..wh..opq":
        prefix = directory + "/" if directory else ""
        for target in tuple(projection):
            if not directory or target.startswith(prefix):
                del projection[target]
        return
    target_leaf = leaf[4:]
    if (not target_leaf or target_leaf in (".", "..") or "/" in target_leaf or
            ".wh." in target_leaf or target_leaf.startswith(".")):
        raise ValidationError(f"malformed or escaping whiteout: {name}")
    target = posixpath.join(directory, target_leaf)
    prefix = target + "/"
    for projected in tuple(projection):
        if projected == target or projected.startswith(prefix):
            del projection[projected]


def _validate_base_link_target(name: str, target: str, *, hardlink: bool) -> None:
    if not target or "\\" in target or "\x00" in target or target == "/":
        raise ValidationError(f"unsafe base-layer link target: {name} -> {target!r}")
    absolute = target.startswith("/")
    target_parts = target.removeprefix("/").split("/")
    if any(part in ("", ".") for part in target_parts) or posixpath.normpath(target) != target:
        raise ValidationError(f"unsafe base-layer link target: {name} -> {target!r}")
    if hardlink and absolute:
        raise ValidationError(f"unsafe base-layer link target: {name} -> {target!r}")
    if absolute:
        if any(part == ".." for part in target_parts):
            raise ValidationError(f"unsafe base-layer link target: {name} -> {target!r}")
        return
    resolved = posixpath.normpath(target if hardlink else posixpath.join(posixpath.dirname(name), target))
    if resolved == ".." or resolved.startswith("../") or resolved.startswith("/"):
        raise ValidationError(f"unsafe base-layer link target: {name} -> {target!r}")


def scan_layer(
    layer_data: bytes,
    limits: Limits,
    projection: dict[str, ProjectedFile],
    *,
    inherited_base: bool = False,
) -> None:
    layer_data = expanded_layer(layer_data, limits)
    # OCI whiteouts only affect entries inherited from lower layers. Keep this
    # layer's additions separate so tar member order cannot change semantics.
    additions: dict[str, ProjectedFile] = {}
    seen: set[str] = set()
    try:
        archive = tarfile.open(fileobj=io.BytesIO(layer_data), mode="r:")
    except tarfile.TarError as exc:
        raise ValidationError(f"malformed layer tar: {exc}") from exc
    with archive:
        count = 0
        for member in archive:
            count += 1
            if count > limits.members:
                raise ValidationError("layer member count exceeds limit")
            name = safe_name(member.name.rstrip("/") if member.isdir() else member.name)
            if name in seen:
                raise ValidationError(f"duplicate layer path: {name}")
            seen.add(name)
            leaf = posixpath.basename(name)
            is_whiteout = leaf.startswith(".wh.") or ".wh." in leaf
            if is_whiteout:
                if not member.isreg() or member.size != 0:
                    raise ValidationError(f"whiteout is not an empty regular file: {name}")
                apply_whiteout(name, projection)
                continue
            selected = _is_selected(name)
            if selected and not (member.isreg() or member.isdir()):
                raise ValidationError(f"selected special file is forbidden: {name}")
            if member.isdir():
                continue
            if not member.isreg():
                if not inherited_base:
                    raise ValidationError(f"post-base layer links/devices/FIFO/socket are forbidden: {name}")
                if member.issym() or member.islnk():
                    _validate_base_link_target(name, member.linkname, hardlink=member.islnk())
                    continue
                raise ValidationError(f"base layer device/FIFO/socket is forbidden: {name}")
            if member.size < 0 or member.size > limits.member_bytes:
                raise ValidationError(f"layer member exceeds byte limit: {name}")
            if not selected:
                continue
            extracted = archive.extractfile(member)
            if extracted is None:
                raise ValidationError(f"selected member has no bytes: {name}")
            data = _bounded_read(extracted, limits.member_bytes, name)
            additions[name] = ProjectedFile(data, stat.S_IMODE(member.mode))
            selected_total = sum(len(value.data) for value in {**projection, **additions}.values())
            if selected_total > limits.selected_bytes:
                raise ValidationError("expanded selected-file bytes exceed limit")
    projection.update(additions)


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
    except (OSError, tarfile.TarError) as exc:
        raise ValidationError(f"malformed Docker archive: {exc}") from exc
    with archive:
        count = 0
        for member in archive:
            count += 1
            if count > limits.members:
                raise ValidationError("archive member count exceeds limit")
            name = safe_name(member.name.rstrip("/") if member.isdir() else member.name)
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
            stream = archive.extractfile(member)
            if stream is None:
                raise ValidationError(f"archive member has no bytes: {name}")
            data = _bounded_read(stream, limits.member_bytes, f"archive member {name}")
            if len(data) != member.size:
                raise ValidationError(f"archive member is truncated: {name}")
            contents[name] = data
    return contents, directories


def _digest_named_blob(name: str, data: bytes) -> str:
    match = HEX_BLOB.fullmatch(name)
    if not match or hashlib.sha256(data).hexdigest() != match.group(1):
        raise ValidationError(f"blob path/digest mismatch: {name}")
    return "sha256:" + match.group(1)


def _outer_graph_references(
    contents: dict[str, bytes], entry: dict[str, Any], layer_digests: list[str]
) -> tuple[str, set[str]]:
    has_index = "index.json" in contents
    has_layout = "oci-layout" in contents
    if has_index != has_layout:
        raise ValidationError("OCI index/layout must be present together")
    if not has_index:
        return "sha256:" + hashlib.sha256(contents["manifest.json"]).hexdigest(), set()
    if load_json(contents["oci-layout"], "OCI layout") != {"imageLayoutVersion": "1.0.0"}:
        raise ValidationError("OCI layout is malformed or unexpected")
    index = load_json(contents["index.json"], "OCI index")
    if not isinstance(index, dict) or index.get("schemaVersion") != 2:
        raise ValidationError("OCI index is malformed or unexpected")
    docker_media = set(index) == {"schemaVersion", "mediaType", "manifests"}
    if docker_media:
        if index.get("mediaType") != "application/vnd.oci.image.index.v1+json":
            raise ValidationError("OCI index media type is wrong")
    elif set(index) != {"schemaVersion", "manifests"}:
        raise ValidationError("OCI index has unexpected fields")
    descriptors = index.get("manifests")
    if not isinstance(descriptors, list) or len(descriptors) != 1 or not isinstance(descriptors[0], dict):
        raise ValidationError("OCI index must reference exactly one manifest")
    descriptor = descriptors[0]
    expected_descriptor = {"mediaType", "digest", "size", "annotations", "platform"} if docker_media else {"mediaType", "digest", "size"}
    if set(descriptor) != expected_descriptor:
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
    digest = descriptor.get("digest")
    if not isinstance(digest, str) or re.fullmatch(r"sha256:[0-9a-f]{64}", digest) is None:
        raise ValidationError("OCI manifest digest is malformed")
    name = "blobs/sha256/" + digest.split(":", 1)[1]
    data = contents.get(name)
    if data is None or descriptor.get("size") != len(data) or _digest_named_blob(name, data) != digest:
        raise ValidationError("OCI manifest descriptor size/digest mismatch")
    manifest = load_json(data, "OCI manifest")
    if not isinstance(manifest, dict) or set(manifest) != {"schemaVersion", "mediaType", "config", "layers"}:
        raise ValidationError("OCI manifest is malformed or has unexpected fields")
    if manifest.get("schemaVersion") != 2 or manifest.get("mediaType") != expected_manifest_media:
        raise ValidationError("OCI manifest schema/media type is wrong")
    config_descriptor = manifest.get("config")
    layers = manifest.get("layers")
    if not isinstance(config_descriptor, dict) or set(config_descriptor) != {"mediaType", "digest", "size"}:
        raise ValidationError("OCI config descriptor is malformed")
    config_digest = _digest_named_blob(entry["Config"], contents[entry["Config"]])
    config_media = "application/vnd.docker.container.image.v1+json" if docker_media else "application/vnd.oci.image.config.v1+json"
    if config_descriptor != {"mediaType": config_media, "digest": config_digest, "size": len(contents[entry["Config"]])}:
        raise ValidationError("OCI and Docker manifests disagree on config")
    if not isinstance(layers, list) or len(layers) != len(layer_digests):
        raise ValidationError("OCI and Docker manifests disagree on layer count")
    for layer_descriptor, layer_digest, layer_name in zip(layers, layer_digests, entry["Layers"]):
        if not isinstance(layer_descriptor, dict) or set(layer_descriptor) != {"mediaType", "digest", "size"}:
            raise ValidationError("OCI layer descriptor is malformed")
        data_value = contents[layer_name]
        allowed_media = {"application/vnd.docker.image.rootfs.diff.tar.gzip"} if docker_media else {
            "application/vnd.oci.image.layer.v1.tar", "application/vnd.oci.image.layer.v1.tar+gzip"
        }
        if (layer_descriptor.get("digest") != layer_digest or
                layer_descriptor.get("size") != len(data_value) or
                layer_descriptor.get("mediaType") not in allowed_media):
            raise ValidationError("OCI and Docker manifests disagree on layers")
        declares_gzip = layer_descriptor["mediaType"].endswith("gzip")
        if data_value.startswith(b"\x1f\x8b") != declares_gzip:
            raise ValidationError("OCI layer compression does not match media type")
    return digest, {name}


def _validate_outer_closure(contents: dict[str, bytes], directories: set[str], referenced: set[str]) -> None:
    regular = set(contents)
    if regular != referenced:
        extras = sorted(regular - referenced)
        missing = sorted(referenced - regular)
        raise ValidationError(
            f"archive contains unexpected/missing regular members: extras={extras} missing={missing}"
        )
    allowed_directories: set[str] = set()
    for name in referenced:
        parent = posixpath.dirname(name)
        while parent:
            allowed_directories.add(parent)
            parent = posixpath.dirname(parent)
    unexpected = sorted(directories - allowed_directories)
    if unexpected:
        raise ValidationError(f"archive contains unexpected directories: {unexpected}")


def _docker_manifest(contents: dict[str, bytes], limits: Limits, allow_tags: bool) -> dict[str, Any]:
    raw = contents.get("manifest.json")
    if raw is None:
        raise ValidationError("Docker archive has no manifest.json")
    manifest = load_json(raw, "manifest")
    if not isinstance(manifest, list) or len(manifest) != 1 or not isinstance(manifest[0], dict):
        raise ValidationError("Docker archive must contain exactly one manifest")
    entry = manifest[0]
    if set(entry) - {"Config", "RepoTags", "Layers", "LayerSources"}:
        raise ValidationError("Docker manifest has unexpected fields")
    if not allow_tags and entry.get("RepoTags") not in (None, []):
        raise ValidationError("derived Web archive RepoTags must be empty")
    if not isinstance(entry.get("Config"), str) or not isinstance(entry.get("Layers"), list) or not entry["Layers"]:
        raise ValidationError("Docker manifest config/layers are malformed")
    if len(entry["Layers"]) > limits.layers or any(not isinstance(item, str) for item in entry["Layers"]):
        raise ValidationError("Docker manifest layer list is malformed or oversized")
    return entry


def _config_from_archive(
    path: str, limits: Limits, allow_tags: bool
) -> tuple[dict[str, Any], dict[str, bytes], dict[str, Any], set[str]]:
    contents, directories = _read_outer(path, limits)
    entry = _docker_manifest(contents, limits, allow_tags)
    config_name = safe_name(entry["Config"])
    if config_name not in contents:
        raise ValidationError("referenced config is missing")
    _digest_named_blob(config_name, contents[config_name]) if config_name.startswith("blobs/") else None
    config = load_json(contents[config_name], "config")
    if not isinstance(config, dict):
        raise ValidationError("image config must be an object")
    return config, contents, entry, directories


def _authenticated_base_contract(
    path: str, limits: Limits, policy: Policy
) -> tuple[tuple[dict[str, Any], ...], int]:
    config, contents, entry, directories = _config_from_archive(path, limits, False)
    config_name = entry["Config"]
    config_digest = _digest_named_blob(config_name, contents[config_name])
    if config_digest != policy.base_config_digest:
        raise ValidationError("base archive does not have the exact pinned nginx config digest")
    if config.get("architecture") != "amd64" or config.get("os") != "linux":
        raise ValidationError("base archive platform is not exact linux/amd64")
    if len(policy.base_layer_digests) != len(policy.base_diff_ids):
        raise ValidationError("pinned nginx base policy is internally inconsistent")
    if len(entry["Layers"]) != len(policy.base_layer_digests):
        raise ValidationError("base archive does not have the exact pinned nginx layer count")
    layer_digests: list[str] = []
    diff_ids: list[str] = []
    for layer_name in entry["Layers"]:
        layer = contents.get(layer_name)
        if layer is None:
            raise ValidationError(f"referenced base layer is missing: {layer_name}")
        layer_digests.append(_digest_named_blob(layer_name, layer))
        diff_ids.append("sha256:" + hashlib.sha256(expanded_layer(layer, limits)).hexdigest())
    if tuple(layer_digests) != policy.base_layer_digests:
        raise ValidationError("base archive does not have the exact pinned nginx layer digest order")
    if tuple(diff_ids) != policy.base_diff_ids:
        raise ValidationError("base archive does not have the exact pinned nginx diff-ID order")
    _, oci_references = _outer_graph_references(contents, entry, layer_digests)
    metadata = {"manifest.json"}
    if "index.json" in contents:
        metadata.update(("index.json", "oci-layout"))
    _validate_outer_closure(
        contents, directories, metadata | {config_name, *entry["Layers"]} | oci_references
    )
    rootfs = config.get("rootfs")
    if (not isinstance(rootfs, dict) or rootfs.get("type") != "layers" or
            rootfs.get("diff_ids") != list(policy.base_diff_ids)):
        raise ValidationError("base archive does not have the exact pinned nginx rootfs")
    history = config.get("history")
    if not isinstance(history, list) or not all(isinstance(item, dict) for item in history):
        raise ValidationError("base archive has malformed pinned nginx history")
    history_digest = "sha256:" + hashlib.sha256(
        json.dumps(history, sort_keys=True, separators=(",", ":")).encode()
    ).hexdigest()
    if history_digest != policy.base_history_digest:
        raise ValidationError("base archive does not have the exact pinned nginx history")
    return tuple(history), len(policy.base_diff_ids)


def validate(archive_path: str, base_archive: str, source_dir: str, version: str, revision: str,
             limits: Limits | None = None, *, policy: Policy = DEFAULT_POLICY) -> dict[str, Any]:
    limits = limits or Limits()
    _validate_limits(limits)
    if not re.fullmatch(r"[0-9a-f]{40}", revision):
        raise ValidationError("expected revision is not exact lowercase 40-hex")
    config, contents, entry, directories = _config_from_archive(archive_path, limits, False)
    base_history, base_count = _authenticated_base_contract(base_archive, limits, policy)
    if config.get("architecture") != "amd64" or config.get("os") != "linux":
        raise ValidationError("derived Web archive platform is not exact linux/amd64")
    labels = config.get("config", {}).get("Labels", {})
    if labels.get("org.opencontainers.image.version") != version or labels.get("org.opencontainers.image.revision") != revision:
        raise ValidationError("OCI version/revision labels do not match")
    rootfs = config.get("rootfs")
    if not isinstance(rootfs, dict) or rootfs.get("type") != "layers" or not isinstance(rootfs.get("diff_ids"), list):
        raise ValidationError("image rootfs is malformed")
    history = config.get("history")
    if not isinstance(history, list):
        raise ValidationError("image history is malformed")

    layer_payloads: list[bytes] = []
    layer_digests: list[str] = []
    diff_ids: list[str] = []
    referenced = {"manifest.json", entry["Config"]}
    for layer_name in entry["Layers"]:
        layer_name = safe_name(layer_name)
        layer = contents.get(layer_name)
        if layer is None:
            raise ValidationError(f"referenced layer is missing: {layer_name}")
        referenced.add(layer_name)
        digest = _digest_named_blob(layer_name, layer) if layer_name.startswith("blobs/") else "sha256:" + hashlib.sha256(layer).hexdigest()
        layer_payloads.append(layer)
        layer_digests.append(digest)
        expanded = expanded_layer(layer, limits)
        diff_ids.append("sha256:" + hashlib.sha256(expanded).hexdigest())
    if diff_ids != rootfs["diff_ids"]:
        raise ValidationError("config rootfs identities do not match ordered layers")
    if tuple(diff_ids[:base_count]) != policy.base_diff_ids:
        raise ValidationError("image rootfs does not have the exact pinned nginx base prefix")
    if tuple(layer_digests[:base_count]) != policy.base_layer_digests:
        raise ValidationError("image layers do not have the exact pinned nginx base prefix")
    if tuple(history[:len(base_history)]) != base_history:
        raise ValidationError("image history does not have the exact pinned nginx base prefix")

    manifest_digest, oci_references = _outer_graph_references(contents, entry, layer_digests)
    referenced.update(oci_references)
    if "index.json" in contents:
        referenced.update(("index.json", "oci-layout"))
    _validate_outer_closure(contents, directories, referenced)

    # Only after the exact config/layer/rootfs/history base boundary is proven may
    # inherited nginx symlink/hardlink entries receive their narrow allowance.
    projection: dict[str, ProjectedFile] = {}
    for index_value, layer in enumerate(layer_payloads):
        scan_layer(layer, limits, projection, inherited_base=index_value < base_count)

    with open(os.path.join(source_dir, "nginx.conf"), "rb") as source:
        canonical_config = source.read()
    expected: dict[str, bytes] = {CONFIG_PATH: canonical_config}
    asset_lines = []
    for asset in ASSETS:
        with open(os.path.join(source_dir, asset), "rb") as source:
            data = source.read()
        expected[f"{WEB_ROOT}/{asset}"] = data
        asset_lines.append(f"{hashlib.sha256(data).hexdigest()}  {asset}\n")
    identity_bytes = "".join(sorted(asset_lines, key=lambda line: line.split("  ", 1)[1])).encode()
    expected[f"{WEB_ROOT}/{IDENTITY}"] = identity_bytes
    if set(projection) != set(expected):
        raise ValidationError(f"final selected filesystem has missing/extra entries: {sorted(set(projection) ^ set(expected))}")
    for name, data in expected.items():
        projected = projection[name]
        if projected.data != data:
            raise ValidationError(f"canonical bytes mismatch: {name}")
        if projected.mode != 0o644:
            raise ValidationError(f"canonical mode mismatch: {name}: {projected.mode:o}")

    config_digest = "sha256:" + hashlib.sha256(contents[entry["Config"]]).hexdigest()
    rootfs_digest = "sha256:" + hashlib.sha256(json.dumps(rootfs["diff_ids"], separators=(",", ":")).encode()).hexdigest()
    with open(archive_path, "rb") as source:
        archive_digest = hashlib.sha256(source.read()).hexdigest()
    return {
        "archive": archive_digest,
        "config": config_digest,
        "manifest": manifest_digest,
        "rootfs": rootfs_digest,
        "layers": layer_digests,
        "asset_manifest": hashlib.sha256(identity_bytes).hexdigest(),
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--archive", required=True)
    parser.add_argument("--base-archive", required=True)
    parser.add_argument("--source", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--revision", required=True)
    args = parser.parse_args()
    try:
        print(json.dumps(validate(args.archive, args.base_archive, args.source, args.version, args.revision), sort_keys=True, separators=(",", ":")))
    except (OSError, ValidationError) as exc:
        print(f"Web archive validation failed: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
