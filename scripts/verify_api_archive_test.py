#!/usr/bin/env python3
"""Adversarial contract tests for the offline API Docker-archive validator."""
from __future__ import annotations

import dataclasses
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import stat
import tarfile
import tempfile
import unittest

import verify_api_archive as validator

VERSION = "1.2.3"
REVISION = "a" * 40
GO_PATH = "github.com/network-troubleshooting-company/checknetwork/cmd/checknetwork-api"
GO_VERSION = "go1.22.12"
PATH_ENV = "/:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"


def sha(data: bytes) -> str:
    return "sha256:" + hashlib.sha256(data).hexdigest()


def go_binary(*, framed=False) -> bytes:
    # Go 1.18+ inline build-info encoding: magic/header, then two varint strings.
    module = (
        "path\t" + GO_PATH + "\n"
        "mod\tgithub.com/network-troubleshooting-company/checknetwork\t(devel)\t\n"
        "build\t-buildmode=exe\n"
        "build\t-compiler=gc\n"
        "build\t-ldflags=\"-s -w -buildid= -X main.version=" + VERSION
        + " -X main.revision=" + REVISION + "\"\n"
        "build\tCGO_ENABLED=0\n"
    ).encode()

    def vint(value: int) -> bytes:
        result = bytearray()
        while value >= 0x80:
            result.append((value & 0x7f) | 0x80)
            value >>= 7
        result.append(value)
        return bytes(result)

    if framed:
        module = validator.GO_MODULE_FRAME_START + module + validator.GO_MODULE_FRAME_END
    return b"ELF-fixture\0\xff Go buildinf:" + bytes((8, 2)) + bytes(16) + vint(len(GO_VERSION)) + GO_VERSION.encode() + vint(len(module)) + module


BINARY = go_binary()
TRACEROUTE = b"ELF traceroute fixture\n"


def tar_bytes(entries, *, gz=False):
    output = io.BytesIO()
    mode = "w:gz" if gz else "w:"
    with tarfile.open(fileobj=output, mode=mode, format=tarfile.PAX_FORMAT) as archive:
        for entry in entries:
            if len(entry) == 2:
                name, data = entry
                kind, mode_bits, link = "file", 0o644, ""
            else:
                name, data, kind, mode_bits, link = entry
            info = tarfile.TarInfo(name)
            info.mode = mode_bits
            if kind == "dir":
                info.type = tarfile.DIRTYPE
                info.size = 0
                archive.addfile(info)
            elif kind == "symlink":
                info.type = tarfile.SYMTYPE
                info.linkname = link
                archive.addfile(info)
            elif kind == "hardlink":
                info.type = tarfile.LNKTYPE
                info.linkname = link
                archive.addfile(info)
            elif kind == "fifo":
                info.type = tarfile.FIFOTYPE
                archive.addfile(info)
            elif kind == "device":
                info.type = tarfile.CHRTYPE
                archive.addfile(info)
            elif kind == "socket":
                info.type = b"s"
                archive.addfile(info)
            else:
                info.size = len(data)
                archive.addfile(info, io.BytesIO(data))
    return output.getvalue()


@dataclasses.dataclass
class Fixture:
    outer: list
    policy: validator.Policy
    names: dict

    def bytes(self):
        return tar_bytes(self.outer)


def fixture(*, base_entries=None, layer_entries=None, gzip_layer=False, docker_media=False) -> Fixture:
    base_expanded = tar_bytes(base_entries or [("etc/alpine-release", b"3.20.3\n")])
    app_expanded = tar_bytes(layer_entries or [
        ("checknetwork-api", BINARY, "file", 0o755, ""),
        ("traceroute", TRACEROUTE, "file", 0o755, ""),
    ])
    base_layer = gzip.compress(base_expanded, mtime=0) if docker_media else base_expanded
    app_layer = gzip.compress(app_expanded, mtime=0) if docker_media or gzip_layer else app_expanded
    diff_ids = [sha(base_expanded), sha(app_expanded)]
    base_history = [{"created_by": "ADD alpine-minirootfs"}]
    history = base_history + [{"created_by": "COPY /checknetwork-api"}]
    config = {
        "architecture": "amd64",
        "os": "linux",
        "config": {
            "Env": ["PATH=" + PATH_ENV],
            "Entrypoint": ["/checknetwork-api"],
            "User": "65532:65532",
            "ExposedPorts": {"8080/tcp": {}},
            "Labels": {
                "org.opencontainers.image.version": VERSION,
                "org.opencontainers.image.revision": REVISION,
            },
        },
        "rootfs": {"type": "layers", "diff_ids": diff_ids},
        "history": history,
    }
    config_data = json.dumps(config, separators=(",", ":")).encode()
    config_name = "blobs/sha256/" + hashlib.sha256(config_data).hexdigest()
    layers = []
    blobs = [(config_name, config_data)]
    for data in (base_layer, app_layer):
        name = "blobs/sha256/" + hashlib.sha256(data).hexdigest()
        layers.append(name)
        blobs.append((name, data))
    oci = {
        "schemaVersion": 2,
        "mediaType": "application/vnd.docker.distribution.manifest.v2+json" if docker_media else "application/vnd.oci.image.manifest.v1+json",
        "config": {"mediaType": "application/vnd.docker.container.image.v1+json" if docker_media else "application/vnd.oci.image.config.v1+json", "digest": sha(config_data), "size": len(config_data)},
        "layers": [
            {"mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip" if docker_media else "application/vnd.oci.image.layer.v1.tar" + ("+gzip" if i == 1 and gzip_layer else ""), "digest": sha(data), "size": len(data)}
            for i, data in enumerate((base_layer, app_layer))
        ],
    }
    oci_data = json.dumps(oci, separators=(",", ":")).encode()
    oci_name = "blobs/sha256/" + hashlib.sha256(oci_data).hexdigest()
    manifest = [{"Config": config_name, "RepoTags": [], "Layers": layers}]
    index_descriptor = {"mediaType": oci["mediaType"], "digest": sha(oci_data), "size": len(oci_data)}
    index = {"schemaVersion": 2, "manifests": [index_descriptor]}
    if docker_media:
        index["mediaType"] = "application/vnd.oci.image.index.v1+json"
        index_descriptor["annotations"] = {"org.opencontainers.image.created": "2026-09-03T00:25:50Z"}
        index_descriptor["platform"] = {"architecture": "amd64", "os": "linux"}
    outer = [
        ("blobs", b"", "dir", 0o755, ""),
        ("blobs/sha256", b"", "dir", 0o755, ""),
        *blobs,
        (oci_name, oci_data),
        ("manifest.json", json.dumps(manifest, separators=(",", ":")).encode()),
        ("index.json", json.dumps(index, separators=(",", ":")).encode()),
        ("oci-layout", b'{"imageLayoutVersion":"1.0.0"}'),
    ]
    policy = validator.Policy(
        base_layer_digests=(sha(base_layer),),
        base_diff_ids=(diff_ids[0],), base_history=tuple(base_history),
        traceroute_sha256=hashlib.sha256(TRACEROUTE).hexdigest(),
        go_version=GO_VERSION, go_path=GO_PATH, path_env=PATH_ENV,
    )
    return Fixture(outer, policy, {"config": config_name, "layers": layers, "oci": oci_name})


def replace_entry(fx: Fixture, name: str, data: bytes):
    for index, entry in enumerate(fx.outer):
        if entry[0] == name:
            fx.outer[index] = (name, data) if len(entry) == 2 else (name, data, *entry[2:])
            return
    raise AssertionError(name)


def replace_content_addressed_config(fx: Fixture, config_data: bytes):
    old_config = fx.names["config"]
    old_oci = fx.names["oci"]
    config_name = "blobs/sha256/" + hashlib.sha256(config_data).hexdigest()
    oci = json.loads(next(e[1] for e in fx.outer if e[0] == old_oci))
    oci["config"]["digest"] = sha(config_data)
    oci["config"]["size"] = len(config_data)
    oci_data = json.dumps(oci, separators=(",", ":")).encode()
    oci_name = "blobs/sha256/" + hashlib.sha256(oci_data).hexdigest()
    manifest = [{"Config": config_name, "RepoTags": [], "Layers": fx.names["layers"]}]
    index = {"schemaVersion": 2, "manifests": [{"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": sha(oci_data), "size": len(oci_data)}]}
    fx.outer = [entry for entry in fx.outer if entry[0] not in (old_config, old_oci)]
    fx.outer.extend([
        (config_name, config_data),
        (oci_name, oci_data),
    ])
    replace_entry(fx, "manifest.json", json.dumps(manifest, separators=(",", ":")).encode())
    replace_entry(fx, "index.json", json.dumps(index, separators=(",", ":")).encode())
    fx.names["config"] = config_name
    fx.names["oci"] = oci_name


class APIArchiveValidatorTest(unittest.TestCase):
    def validate(self, fx, **kwargs):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp, "image.tar")
            path.write_bytes(fx.bytes())
            policy = kwargs.pop("policy", fx.policy)
            kwargs.setdefault("binary_sha256", hashlib.sha256(BINARY).hexdigest())
            return validator.validate(str(path), VERSION, REVISION, policy=policy, **kwargs)

    def assert_rejected(self, fx, pattern, **kwargs):
        with self.assertRaisesRegex(validator.ValidationError, pattern):
            self.validate(fx, **kwargs)

    def test_valid_tagless_archive_returns_closed_hash_schema(self):
        fx = fixture(gzip_layer=True)
        result = self.validate(fx)
        self.assertEqual(set(result), {"schema", "archive", "config", "manifest", "rootfs", "layers", "binary", "traceroute", "go_version", "go_path"})
        self.assertEqual(result["schema"], "checknetwork.api-archive.v1")
        self.assertEqual(result["binary"], hashlib.sha256(BINARY).hexdigest())
        self.assertEqual(result["traceroute"], hashlib.sha256(TRACEROUTE).hexdigest())
        self.assertEqual(result["go_version"], GO_VERSION)
        self.assertEqual(result["go_path"], GO_PATH)
        self.assertEqual(len(result["layers"]), 2)
        for field in ("archive", "binary", "traceroute"):
            self.assertRegex(result[field], r"^[0-9a-f]{64}$")
        for field in ("config", "manifest", "rootfs"):
            self.assertRegex(result[field], r"^sha256:[0-9a-f]{64}$")

    def test_synthetic_docker_media_index_and_manifest_shape_is_accepted(self):
        result = self.validate(fixture(docker_media=True))
        self.assertEqual(result["binary"], hashlib.sha256(BINARY).hexdigest())

    def test_archive_paths_duplicates_and_special_types_are_rejected(self):
        for name in ("/absolute", "../escape", "a/../escape", "./alias", "a//b", "a\\b"):
            with self.subTest(name=name), self.assertRaises(validator.ValidationError):
                validator.canonical_name(name, directory=False)
        for mutation, pattern in (
            (lambda fx: fx.outer.append(fx.outer[-1]), "duplicate archive path"),
            (lambda fx: fx.outer.append(("bad", b"", "symlink", 0o777, "/etc/passwd")), "links/devices/FIFO/socket"),
            (lambda fx: fx.outer.append(("bad", b"", "hardlink", 0o777, "manifest.json")), "links/devices/FIFO/socket"),
            (lambda fx: fx.outer.append(("bad", b"", "fifo", 0o777, "")), "links/devices/FIFO/socket"),
            (lambda fx: fx.outer.append(("bad", b"", "device", 0o777, "")), "links/devices/FIFO/socket"),
        ):
            fx = fixture(); mutation(fx)
            with self.subTest(pattern=pattern): self.assert_rejected(fx, pattern)

    def test_all_numeric_budgets_fail_closed(self):
        baseline = fixture()
        cases = [
            ("archive_bytes", len(baseline.bytes()) - 1, "archive exceeds"),
            ("members", len(baseline.outer) - 1, "member count"),
            ("member_bytes", 16, "member exceeds"),
            ("layers", 1, "layer count"),
            ("layer_bytes", 16, "layer exceeds"),
            ("expanded_layer_bytes", 16, "expanded layer"),
            ("selected_bytes", len(BINARY) + len(TRACEROUTE) - 1, "selected-file bytes"),
        ]
        for field, value, pattern in cases:
            limits = dataclasses.replace(validator.Limits(), **{field: value})
            with self.subTest(field=field): self.assert_rejected(fixture(), pattern, limits=limits)

    def test_post_base_layer_links_devices_fifo_and_socket_are_rejected(self):
        for kind in ("symlink", "hardlink"):
            entries = [("checknetwork-api", BINARY, "file", 0o755, ""), ("traceroute", TRACEROUTE, "file", 0o755, ""), ("tmp/nested/bad", b"", kind, 0o777, "../../../escape")]
            with self.subTest(kind=kind): self.assert_rejected(fixture(layer_entries=entries), "post-base layer links/devices/FIFO/socket")
        for kind in ("fifo", "device", "socket"):
            entries = [("checknetwork-api", BINARY, "file", 0o755, ""), ("traceroute", TRACEROUTE, "file", 0o755, ""), ("tmp/bad", b"", kind, 0o777, "")]
            with self.subTest(kind=kind): self.assert_rejected(fixture(layer_entries=entries), "post-base layer links/devices/FIFO/socket")
        duplicate = [("checknetwork-api", BINARY, "file", 0o755, ""), ("checknetwork-api", BINARY, "file", 0o755, ""), ("traceroute", TRACEROUTE, "file", 0o755, "")]
        self.assert_rejected(fixture(layer_entries=duplicate), "duplicate layer path")

    def test_exact_base_accepts_canonical_unselected_links_and_devices_without_projection(self):
        base_entries = [
            ("etc/alpine-release", b"3.20.3\n", "file", 0o644, ""),
            ("bin/arch", b"", "symlink", 0o777, "/bin/busybox"),
            ("usr/lib/libcrypto.so.3", b"", "symlink", 0o777, "../../lib/libcrypto.so.3"),
            ("usr/bin/tool", b"", "hardlink", 0o755, "bin/busybox"),
            ("dev/console", b"", "device", 0o600, ""),
        ]
        self.validate(fixture(base_entries=base_entries))

    def test_same_canonical_base_specials_are_rejected_in_post_base_layers(self):
        for name, kind, link in (
            ("bin/arch", "symlink", "/bin/busybox"),
            ("usr/bin/tool", "hardlink", "bin/busybox"),
            ("dev/console", "device", ""),
        ):
            entries = [
                ("checknetwork-api", BINARY, "file", 0o755, ""),
                ("traceroute", TRACEROUTE, "file", 0o755, ""),
                (name, b"", kind, 0o755, link),
            ]
            with self.subTest(kind=kind):
                self.assert_rejected(fixture(layer_entries=entries), "post-base layer links/devices/FIFO/socket")

    def test_base_special_permission_requires_exact_rootfs_and_history_prefix_before_member_scan(self):
        crafted = fixture(base_entries=[("bin/arch", b"", "symlink", 0o777, "/bin/busybox")])
        trusted = fixture().policy
        self.assert_rejected(crafted, "pinned Alpine base rootfs prefix", policy=trusted)

        wrong_history = dataclasses.replace(crafted.policy, base_history=({"created_by": "not the pinned base"},))
        self.assert_rejected(crafted, "pinned Alpine base history prefix", policy=wrong_history)

    def test_base_specials_still_require_safe_link_targets_and_forbid_fifo_socket(self):
        for kind, link, pattern in (
            ("symlink", "../../../escape", "unsafe base-layer link target"),
            ("hardlink", "../escape", "unsafe base-layer link target"),
            ("fifo", "", "base layer FIFO/socket"),
            ("socket", "", "base layer FIFO/socket"),
        ):
            with self.subTest(kind=kind):
                self.assert_rejected(
                    fixture(base_entries=[("tmp/bad", b"", kind, 0o755, link)]),
                    pattern,
                )

    def test_selected_links_are_rejected_in_base_and_post_base_layers(self):
        for selected in ("checknetwork-api", "traceroute"):
            for kind in ("symlink", "hardlink"):
                with self.subTest(layer="base", selected=selected, kind=kind):
                    self.assert_rejected(
                        fixture(base_entries=[(selected, b"", kind, 0o755, "bin/busybox")]),
                        "selected member must be a regular executable non-link",
                    )
                entries = [
                    ("traceroute", TRACEROUTE, "file", 0o755, "")
                    if selected == "checknetwork-api"
                    else ("checknetwork-api", BINARY, "file", 0o755, ""),
                    (selected, b"", kind, 0o755, "bin/busybox"),
                ]
                with self.subTest(layer="derived", selected=selected, kind=kind):
                    self.assert_rejected(fixture(layer_entries=entries), "selected member must be a regular executable non-link")

    def test_selected_member_must_be_executable_in_every_layer_even_if_replaced_later(self):
        fx = fixture(base_entries=[("checknetwork-api", b"old", "file", 0o644, "")])
        self.assert_rejected(fx, "selected member must be a regular executable non-link")

    def test_json_duplicate_keys_are_rejected_everywhere(self):
        mutations = (
            ("manifest.json", b'[{"Config":"x","Config":"y","Layers":[]}]'),
            ("index.json", b'{"schemaVersion":2,"schemaVersion":2,"manifests":[]}'),
            ("oci-layout", b'{"imageLayoutVersion":"1.0.0","imageLayoutVersion":"1.0.0"}'),
        )
        for name, data in mutations:
            fx = fixture(); replace_entry(fx, name, data)
            with self.subTest(name=name): self.assert_rejected(fx, "duplicate JSON key")

    def test_manifest_is_exactly_one_tagless_and_unambiguous(self):
        mutations = []
        mutations.append(("two manifests", lambda fx: replace_entry(fx, "manifest.json", json.dumps([{}, {}]).encode())))
        mutations.append(("tagged", lambda fx: replace_entry(fx, "manifest.json", json.dumps([{"Config": fx.names["config"], "RepoTags": ["owned:no"], "Layers": fx.names["layers"]}]).encode())))
        mutations.append(("unreferenced", lambda fx: fx.outer.append(("blobs/sha256/" + "0" * 64, b"extra"))))
        mutations.append(("unknown outer", lambda fx: fx.outer.append(("repositories", b"{}"))))
        for name, mutate in mutations:
            fx = fixture(); mutate(fx)
            with self.subTest(name=name): self.assert_rejected(fx, "manifest|tagless|unreferenced|unexpected")

    def test_blob_layer_diffid_and_order_tampering_is_rejected(self):
        for target in ("config", "layer", "oci"):
            fx = fixture()
            blob = fx.names["config"] if target == "config" else fx.names["layers"][0] if target == "layer" else fx.names["oci"]
            original = next(e[1] for e in fx.outer if e[0] == blob)
            replace_entry(fx, blob, original + b"x")
            with self.subTest(target=target): self.assert_rejected(fx, "digest")
        fx = fixture()
        manifest = [{"Config": fx.names["config"], "RepoTags": [], "Layers": list(reversed(fx.names["layers"]))}]
        replace_entry(fx, "manifest.json", json.dumps(manifest).encode())
        self.assert_rejected(fx, "order|disagree|identities")

    def test_exact_pinned_base_prefix_is_required(self):
        fx = fixture()
        wrong = dataclasses.replace(fx.policy, base_diff_ids=("sha256:" + "0" * 64,))
        with self.assertRaisesRegex(validator.ValidationError, "pinned Alpine base rootfs prefix"):
            self.validate(fx, policy=wrong)
        wrong = dataclasses.replace(fx.policy, base_history=({"created_by": "wrong"},))
        with self.assertRaisesRegex(validator.ValidationError, "pinned Alpine base history prefix"):
            self.validate(fx, policy=wrong)
        wrong = dataclasses.replace(fx.policy, base_layer_digests=("sha256:" + "0" * 64,))
        with self.assertRaisesRegex(validator.ValidationError, "pinned Alpine base layer digest prefix"):
            self.validate(fx, policy=wrong)

    def test_oci_whiteouts_are_applied_before_exact_projection(self):
        entries = [
            ("checknetwork-api", b"old", "file", 0o755, ""),
            ("traceroute", TRACEROUTE, "file", 0o755, ""),
            (".wh.checknetwork-api", b"", "file", 0o000, ""),
            ("checknetwork-api", BINARY, "file", 0o755, ""),
        ]
        # Duplicate names in one layer are forbidden even around whiteouts.
        self.assert_rejected(fixture(layer_entries=entries), "duplicate layer path")
        fx = fixture(layer_entries=[(".wh..wh..opq", b"", "file", 0o000, ""), ("checknetwork-api", BINARY, "file", 0o755, ""), ("traceroute", TRACEROUTE, "file", 0o755, "")])
        self.validate(fx)
        for bad in (".wh.", ".wh...wh..opq", "file.wh.bad"):
            entries = [(bad, b"", "file", 0o000, ""), ("checknetwork-api", BINARY, "file", 0o755, ""), ("traceroute", TRACEROUTE, "file", 0o755, "")]
            with self.subTest(bad=bad): self.assert_rejected(fixture(layer_entries=entries), "whiteout")

    def test_same_layer_root_opaque_preserves_selected_additions_after_or_before_marker(self):
        opaque = (".wh..wh..opq", b"", "file", 0o000, "")
        selected = [
            ("checknetwork-api", BINARY, "file", 0o755, ""),
            ("traceroute", TRACEROUTE, "file", 0o755, ""),
        ]
        for entries in ([opaque, *selected], [*selected, opaque]):
            with self.subTest(marker_index=entries.index(opaque)):
                self.validate(fixture(layer_entries=entries))

    def test_same_layer_whiteout_preserves_recreated_selected_file_after_or_before_marker(self):
        whiteout = (".wh.checknetwork-api", b"", "file", 0o000, "")
        selected = [
            ("checknetwork-api", BINARY, "file", 0o755, ""),
            ("traceroute", TRACEROUTE, "file", 0o755, ""),
        ]
        for entries in ([whiteout, *selected], [*selected, whiteout]):
            with self.subTest(marker_index=entries.index(whiteout)):
                self.validate(fixture(layer_entries=entries))

    def test_parent_whiteout_removes_all_lower_selected_descendants(self):
        projection = {
            "checknetwork-api": validator.ProjectedFile(BINARY, 0o755),
            "checknetwork-api/debug/symbols": validator.ProjectedFile(b"symbols", 0o755),
            "traceroute": validator.ProjectedFile(TRACEROUTE, 0o755),
        }
        validator.scan_layer(
            tar_bytes([(".wh.checknetwork-api", b"", "file", 0o000, "")]),
            validator.Limits(),
            projection,
        )
        self.assertNotIn("checknetwork-api", projection)
        self.assertNotIn("checknetwork-api/debug/symbols", projection)
        self.assertIn("traceroute", projection)

    def test_projection_modes_executable_bytes_and_no_selected_extras_are_exact(self):
        cases = (
            ([ ("checknetwork-api", BINARY, "file", 0o644, ""), ("traceroute", TRACEROUTE, "file", 0o755, "")], "selected member must be a regular executable non-link"),
            ([ ("checknetwork-api", BINARY + b"x", "file", 0o755, ""), ("traceroute", TRACEROUTE, "file", 0o755, "")], "binary SHA|Go build"),
            ([ ("checknetwork-api", BINARY, "file", 0o755, ""), ("traceroute", TRACEROUTE + b"x", "file", 0o755, "")], "traceroute"),
            ([ ("checknetwork-api", BINARY, "file", 0o755, ""), ("traceroute", TRACEROUTE, "file", 0o755, ""), ("checknetwork-api.backup", b"x", "file", 0o755, "")], "extra"),
        )
        for entries, pattern in cases:
            with self.subTest(pattern=pattern): self.assert_rejected(fixture(layer_entries=entries), pattern)

    def test_config_runtime_identity_is_exact(self):
        mutations = {
            "version": lambda c: c["config"]["Labels"].__setitem__("org.opencontainers.image.version", "wrong"),
            "revision": lambda c: c["config"]["Labels"].__setitem__("org.opencontainers.image.revision", "b" * 40),
            "user": lambda c: c["config"].__setitem__("User", "0"),
            "entrypoint": lambda c: c["config"].__setitem__("Entrypoint", ["/bin/sh"]),
            "path": lambda c: c["config"].__setitem__("Env", ["PATH=/usr/bin"]),
            "port": lambda c: c["config"].__setitem__("ExposedPorts", {"80/tcp": {}}),
        }
        for name, mutate in mutations.items():
            fx = fixture()
            config_data = next(e[1] for e in fx.outer if e[0] == fx.names["config"])
            config = json.loads(config_data); mutate(config)
            replace_content_addressed_config(fx, json.dumps(config, separators=(",", ":")).encode())
            with self.subTest(name=name):
                self.assert_rejected(fx, "labels|User|Entrypoint|PATH|port")

    def test_duplicate_keys_in_config_and_oci_manifest_are_rejected(self):
        fx = fixture()
        config_data = next(e[1] for e in fx.outer if e[0] == fx.names["config"])
        duplicate = config_data[:-1] + b',"os":"linux"}'
        replace_content_addressed_config(fx, duplicate)
        self.assert_rejected(fx, "duplicate JSON key")

        fx = fixture()
        old_oci = fx.names["oci"]
        oci_data = next(e[1] for e in fx.outer if e[0] == old_oci)
        duplicate_oci = oci_data[:-1] + b',"schemaVersion":2}'
        new_oci = "blobs/sha256/" + hashlib.sha256(duplicate_oci).hexdigest()
        fx.outer = [entry for entry in fx.outer if entry[0] != old_oci]
        fx.outer.append((new_oci, duplicate_oci))
        replace_entry(fx, "index.json", json.dumps({"schemaVersion": 2, "manifests": [{"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": sha(duplicate_oci), "size": len(duplicate_oci)}]}).encode())
        fx.names["oci"] = new_oci
        self.assert_rejected(fx, "duplicate JSON key")

    def test_selected_byte_budget_is_cumulative_across_layers(self):
        limits = dataclasses.replace(validator.Limits(), selected_bytes=len(BINARY) + 1)
        consumed = [0]
        projection = {}
        first = tar_bytes([("checknetwork-api", BINARY, "file", 0o755, "")])
        second = tar_bytes([("checknetwork-api", BINARY, "file", 0o755, "")])
        validator.scan_layer(first, limits, projection, selected_consumed=consumed)
        with self.assertRaisesRegex(validator.ValidationError, "selected-file bytes"):
            validator.scan_layer(second, limits, projection, selected_consumed=consumed)

    def test_default_policy_is_exactly_the_final_pinned_alpine_and_apk_contract(self):
        self.assertEqual(validator.PINNED_ALPINE_IMAGE_DIGEST, "sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc")
        self.assertEqual(validator.PINNED_ALPINE_LAYER_DIGESTS, ("sha256:25f1d6b1951ac8eb3740558fe94cb83d377bdadf95fd9f98b50d2e1b96130471",))
        self.assertEqual(validator.PINNED_ALPINE_DIFF_IDS, ("sha256:08bc4e534116aa76b16015484b82eac51f9a593416feae9296c8a2d4bb7aa4a2",))
        self.assertEqual(len(validator.PINNED_ALPINE_HISTORY), 2)
        self.assertEqual(validator.DEFAULT_POLICY.traceroute_sha256, "f10fa4938a33f3b11350eb4930f4aa75aebf810264f9b7a98597788f49d1d956")

    def test_go_version_path_and_linker_identity_are_verified_offline(self):
        for old, new, pattern in (
            (GO_VERSION.encode(), b"go1.22.11", "Go version"),
            (GO_PATH.encode(), b"github.com/network-troubleshooting-company/checknetwork/cmd/checknetwork-apx", "Go path"),
            (("main.version=" + VERSION).encode(), b"main.version=9.9.9", "linker identity"),
        ):
            changed = BINARY.replace(old, new)
            entries = [("checknetwork-api", changed, "file", 0o755, ""), ("traceroute", TRACEROUTE, "file", 0o755, "")]
            with self.subTest(pattern=pattern):
                self.assert_rejected(
                    fixture(layer_entries=entries), pattern,
                    binary_sha256=hashlib.sha256(changed).hexdigest(),
                )

    def test_real_go_module_build_info_framing_is_verified(self):
        framed = go_binary(framed=True)
        entries = [("checknetwork-api", framed, "file", 0o755, ""), ("traceroute", TRACEROUTE, "file", 0o755, "")]
        result = self.validate(
            fixture(layer_entries=entries),
            binary_sha256=hashlib.sha256(framed).hexdigest(),
        )
        self.assertEqual(result["go_version"], GO_VERSION)

    def test_safe_extraction_requires_owned_empty_real_directory_and_rereads_hashes(self):
        fx = fixture()
        with tempfile.TemporaryDirectory() as temp:
            archive = Path(temp, "image.tar"); archive.write_bytes(fx.bytes())
            out = Path(temp, "out"); out.mkdir(mode=0o700)
            result = validator.validate(str(archive), VERSION, REVISION, policy=fx.policy, extract_dir=str(out))
            self.assertEqual((out / "checknetwork-api").read_bytes(), BINARY)
            self.assertEqual((out / "traceroute").read_bytes(), TRACEROUTE)
            self.assertEqual(stat.S_IMODE((out / "checknetwork-api").stat().st_mode), 0o755)
            self.assertEqual(result["binary"], hashlib.sha256((out / "checknetwork-api").read_bytes()).hexdigest())
            with self.assertRaisesRegex(validator.ValidationError, "empty"):
                validator.validate(str(archive), VERSION, REVISION, policy=fx.policy, extract_dir=str(out))
            other = Path(temp, "other"); other.mkdir(); link = Path(temp, "link"); link.symlink_to(other, target_is_directory=True)
            with self.assertRaisesRegex(validator.ValidationError, "real directory"):
                validator.validate(str(archive), VERSION, REVISION, policy=fx.policy, extract_dir=str(link))

    def test_failed_validation_extracts_nothing(self):
        fx = fixture(layer_entries=[("checknetwork-api", BINARY, "file", 0o644, ""), ("traceroute", TRACEROUTE, "file", 0o755, "")])
        with tempfile.TemporaryDirectory() as temp:
            archive = Path(temp, "image.tar"); archive.write_bytes(fx.bytes())
            out = Path(temp, "out"); out.mkdir()
            with self.assertRaises(validator.ValidationError):
                validator.validate(str(archive), VERSION, REVISION, policy=fx.policy, extract_dir=str(out))
            self.assertEqual(list(out.iterdir()), [])


if __name__ == "__main__":
    unittest.main()
