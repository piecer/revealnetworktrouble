#!/usr/bin/env python3
import dataclasses
import hashlib
import gzip
import io
import json
import os
import tarfile
import tempfile
import unittest
from unittest import mock

import verify_web_archive as validator

VERSION = "1.2.3"
REVISION = "1" * 40


def layer_bytes(members):
    payload = io.BytesIO()
    with tarfile.open(fileobj=payload, mode="w") as archive:
        for member in members:
            name = member[0]
            kind = member[1]
            info = tarfile.TarInfo(name)
            info.mode = 0o644
            if kind == "file":
                data = member[2]
                info.size = len(data)
                archive.addfile(info, io.BytesIO(data))
            elif kind == "dir":
                info.type = tarfile.DIRTYPE
                archive.addfile(info)
            elif kind in ("symlink", "hardlink"):
                info.type = tarfile.SYMTYPE if kind == "symlink" else tarfile.LNKTYPE
                info.linkname = member[2]
                archive.addfile(info)
            else:
                info.type = {
                    "char": tarfile.CHRTYPE,
                    "block": tarfile.BLKTYPE,
                    "fifo": tarfile.FIFOTYPE,
                    "socket": b"s",
                }[kind]
                info.devmajor = 1
                info.devminor = 3
                archive.addfile(info)
    return payload.getvalue()


def outer_member_bytes(entries, *, gz=False):
    payload = io.BytesIO()
    with tarfile.open(fileobj=payload, mode="w:gz" if gz else "w") as archive:
        for name, data in entries:
            info = tarfile.TarInfo(name)
            info.size = len(data)
            archive.addfile(info, io.BytesIO(data))
    return payload.getvalue()


def blob_name(data):
    return "blobs/sha256/" + hashlib.sha256(data).hexdigest()


def config_bytes(diff_ids, history, *, derived):
    value = {"architecture": "amd64", "os": "linux", "rootfs": {"type": "layers", "diff_ids": diff_ids}, "history": history}
    if derived:
        value["config"] = {"Labels": {
            "org.opencontainers.image.version": VERSION,
            "org.opencontainers.image.revision": REVISION,
        }}
    return json.dumps(value, sort_keys=True, separators=(",", ":")).encode()


def archive_bytes(layers, history, *, derived, config_override=None, outer_extra=(), buildx_shape=False, gz=False):
    diff_ids = ["sha256:" + hashlib.sha256(layer).hexdigest() for layer in layers]
    config = config_override or config_bytes(diff_ids, history, derived=derived)
    layer_names = [blob_name(layer) for layer in layers]
    config_name = blob_name(config)
    manifest = [{"Config": config_name, "RepoTags": [], "Layers": layer_names}]
    entries = [("manifest.json", json.dumps(manifest, separators=(",", ":")).encode()), (config_name, config)]
    entries.extend(zip(layer_names, layers))
    if buildx_shape:
        oci = {
            "schemaVersion": 2,
            "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
            "config": {
                "mediaType": "application/vnd.docker.container.image.v1+json",
                "digest": "sha256:" + config_name.rsplit("/", 1)[1],
                "size": len(config),
            },
            "layers": [
                {
                    "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
                    "digest": "sha256:" + name.rsplit("/", 1)[1],
                    "size": len(data),
                }
                for name, data in zip(layer_names, layers)
            ],
        }
        oci_data = json.dumps(oci, separators=(",", ":")).encode()
        oci_name = blob_name(oci_data)
        index = {
            "schemaVersion": 2,
            "mediaType": "application/vnd.oci.image.index.v1+json",
            "manifests": [{
                "mediaType": oci["mediaType"],
                "digest": "sha256:" + oci_name.rsplit("/", 1)[1],
                "size": len(oci_data),
                "annotations": {"org.opencontainers.image.created": "2026-09-03T00:25:50Z"},
                "platform": {"architecture": "amd64", "os": "linux"},
            }],
        }
        entries = [
            ("blobs", None),
            ("blobs/sha256", None),
            *entries,
            (oci_name, oci_data),
            ("index.json", json.dumps(index, separators=(",", ":")).encode()),
            ("oci-layout", b'{"imageLayoutVersion":"1.0.0"}'),
        ]
    entries.extend(outer_extra)
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode="w:gz" if gz else "w") as archive:
        for name, data in entries:
            info = tarfile.TarInfo(name)
            if data is None:
                info.type = tarfile.DIRTYPE
                archive.addfile(info)
            else:
                info.size = len(data)
                archive.addfile(info, io.BytesIO(data))
    return output.getvalue(), config, tuple("sha256:" + name.rsplit("/", 1)[1] for name in layer_names), tuple(diff_ids)


class Fixture:
    def __init__(self, *, base_extra=(), derived_extra=(), derived_layers=None):
        self.temp = tempfile.TemporaryDirectory()
        self.source = os.path.join(self.temp.name, "frontend")
        os.mkdir(self.source)
        self.asset_data = {}
        for name in validator.ASSETS:
            data = ("canonical-" + name + "\n").encode()
            self.asset_data[name] = data
            with open(os.path.join(self.source, name), "wb") as output:
                output.write(data)
        with open(os.path.join(self.source, "nginx.conf"), "wb") as output:
            output.write(b"canonical nginx\n")

        base_members = [
            ("etc/base", "file", b"base"),
            ("usr/lib/libbase.so", "file", b"library"),
            *base_extra,
        ]
        self.base_layer = layer_bytes(base_members)
        self.base_history = [{"created_by": "PINNED BASE"}, {"created_by": "BASE CMD", "empty_layer": True}]
        base_archive, base_config, base_layer_digests, base_diff_ids = archive_bytes(
            [self.base_layer], self.base_history, derived=False
        )
        self.policy = validator.Policy(
            base_config_digest="sha256:" + hashlib.sha256(base_config).hexdigest(),
            base_layer_digests=base_layer_digests,
            base_diff_ids=base_diff_ids,
            base_history_digest="sha256:" + hashlib.sha256(json.dumps(
                self.base_history, sort_keys=True, separators=(",", ":")
            ).encode()).hexdigest(),
        )
        self.base_path = self._write("base.tar", base_archive)

        selected = [(validator.CONFIG_PATH, "file", b"canonical nginx\n")]
        identity = []
        for name in validator.ASSETS:
            data = self.asset_data[name]
            selected.append((f"{validator.WEB_ROOT}/{name}", "file", data))
            identity.append(f"{hashlib.sha256(data).hexdigest()}  {name}\n")
        selected.append((
            f"{validator.WEB_ROOT}/{validator.IDENTITY}",
            "file",
            "".join(sorted(identity, key=lambda line: line.split("  ", 1)[1])).encode(),
        ))
        self.selected = selected
        generated = [layer_bytes([*derived_extra, *selected])]
        self.derived_layers = list(derived_layers) if derived_layers is not None else generated
        self.history = [*self.base_history, {"created_by": "derived"}]
        derived_archive, self.config, _, _ = archive_bytes(
            [self.base_layer, *self.derived_layers], self.history, derived=True
        )
        self.archive_path = self._write("derived.tar", derived_archive)

    def _write(self, name, data):
        path = os.path.join(self.temp.name, name)
        with open(path, "wb") as output:
            output.write(data)
        return path

    def replace_base(self, layers, history=None, config_override=None):
        archive, _, _, _ = archive_bytes(
            layers,
            self.base_history if history is None else history,
            derived=False,
            config_override=config_override,
        )
        self.base_path = self._write("base-mutated.tar", archive)

    def replace_derived(self, layers, history=None, config_override=None):
        archive, _, _, _ = archive_bytes(
            layers,
            self.history if history is None else history,
            derived=True,
            config_override=config_override,
        )
        self.archive_path = self._write("derived-mutated.tar", archive)

    def validate(self):
        return validator.validate(
            self.archive_path, self.base_path, self.source, VERSION, REVISION, policy=self.policy
        )

    def close(self):
        self.temp.cleanup()


class ArchiveSafetyTest(unittest.TestCase):
    def assert_production_asset_contract(self, fx, result):
        self.assertEqual(
            validator.ASSETS,
            ("app.js", "index.html", "state.js", "styles.css", "topology-model.js", "topology-renderer.js", "topology-presentation.js", "topology-visualizer.js"),
        )
        expected_identity = "".join(
            f"{hashlib.sha256(fx.asset_data[name]).hexdigest()}  {name}\n"
            for name in sorted(validator.ASSETS)
        ).encode()
        self.assertEqual(result["asset_manifest"], hashlib.sha256(expected_identity).hexdigest())

    def test_full_validate_rejects_unexpected_outer_regular_and_directory_members(self):
        for extra in (
            (("unexpected-secret.txt", b"secret"),),
            (("repositories", b"{}"),),
            (("arbitrary", None), ("arbitrary/file.txt", b"secret")),
        ):
            fx = Fixture()
            try:
                archive, _, _, _ = archive_bytes(
                    [fx.base_layer, *fx.derived_layers], fx.history, derived=True, outer_extra=extra
                )
                fx.archive_path = fx._write("derived-extra.tar", archive)
                with self.subTest(extra=extra), self.assertRaisesRegex(
                    validator.ValidationError, "unexpected.*(?:regular members|directories)"
                ):
                    fx.validate()
            finally:
                fx.close()

    def test_synthetic_docker_media_outer_shape_is_accepted(self):
        fx = Fixture()
        self.addCleanup(fx.close)
        base_layer = gzip.compress(fx.base_layer, mtime=0)
        derived_layers = [gzip.compress(layer, mtime=0) for layer in fx.derived_layers]
        base_config = config_bytes(
            ["sha256:" + hashlib.sha256(fx.base_layer).hexdigest()], fx.base_history, derived=False
        )
        base_archive, base_config, layer_digests, _ = archive_bytes(
            [base_layer], fx.base_history, derived=False, config_override=base_config, buildx_shape=True
        )
        fx.base_path = fx._write("base-buildx.tar", base_archive)
        fx.policy = validator.Policy(
            base_config_digest="sha256:" + hashlib.sha256(base_config).hexdigest(),
            base_layer_digests=layer_digests,
            base_diff_ids=("sha256:" + hashlib.sha256(fx.base_layer).hexdigest(),),
            base_history_digest="sha256:" + hashlib.sha256(json.dumps(
                fx.base_history, sort_keys=True, separators=(",", ":")
            ).encode()).hexdigest(),
        )
        derived_config = config_bytes(
            [
                "sha256:" + hashlib.sha256(fx.base_layer).hexdigest(),
                *("sha256:" + hashlib.sha256(layer).hexdigest() for layer in fx.derived_layers),
            ],
            fx.history,
            derived=True,
        )
        derived_archive, _, _, _ = archive_bytes(
            [base_layer, *derived_layers], fx.history, derived=True,
            config_override=derived_config, buildx_shape=True
        )
        fx.archive_path = fx._write("derived-buildx.tar", derived_archive)
        fx.validate()

    def test_rejects_absolute_parent_and_noncanonical_paths(self):
        for name in ("/absolute", "../escape", "a/../escape", "./alias", "a//b"):
            with self.subTest(name=name), self.assertRaises(validator.ValidationError):
                validator.safe_name(name)

    def test_authenticated_base_legitimate_symlink_and_hardlink_entries_validate(self):
        fx = Fixture(base_extra=(
            ("usr/lib/libalias.so", "symlink", "libbase.so"),
            ("usr/lib/libcopy.so", "hardlink", "usr/lib/libbase.so"),
        ))
        self.addCleanup(fx.close)
        result = fx.validate()
        self.assertEqual(set(result), {"archive", "config", "manifest", "rootfs", "layers", "asset_manifest"})
        self.assert_production_asset_contract(fx, result)

    def test_full_validate_rejects_every_unselected_post_base_special(self):
        for kind in ("symlink", "hardlink", "char", "block", "fifo", "socket"):
            target = "usr/lib/libbase.so" if kind == "hardlink" else "libbase.so"
            extra = ("tmp/unselected-special", kind, target)
            fx = Fixture(derived_extra=(extra,))
            try:
                with self.subTest(kind=kind), self.assertRaisesRegex(
                    validator.ValidationError, "post-base layer links/devices/FIFO/socket"
                ):
                    fx.validate()
            finally:
                fx.close()

    def test_full_validate_rejects_selected_links_in_base_and_derived_layers(self):
        for layer, kind in (("base", "symlink"), ("base", "hardlink"), ("derived", "symlink"), ("derived", "hardlink")):
            name = f"{validator.WEB_ROOT}/index.html"
            target = "usr/lib/libbase.so" if kind == "hardlink" else "/usr/lib/libbase.so"
            kwargs = {"base_extra": ((name, kind, target),)} if layer == "base" else {"derived_extra": ((name, kind, target),)}
            fx = Fixture(**kwargs)
            try:
                with self.subTest(layer=layer, kind=kind), self.assertRaisesRegex(
                    validator.ValidationError, "selected special file"
                ):
                    fx.validate()
            finally:
                fx.close()

    def test_authenticated_base_rejects_unneeded_devices_fifo_and_socket(self):
        for kind in ("char", "block", "fifo", "socket"):
            fx = Fixture(base_extra=(("dev/unneeded", kind, "unused"),))
            try:
                with self.subTest(kind=kind), self.assertRaisesRegex(
                    validator.ValidationError, "base layer device/FIFO/socket"
                ):
                    fx.validate()
            finally:
                fx.close()

    def test_authenticated_base_link_targets_must_be_canonical_and_contained(self):
        for kind, target in (
            ("symlink", "../../../escape"),
            ("symlink", "a/../target"),
            ("symlink", "a//target"),
            ("hardlink", "../escape"),
            ("hardlink", "/absolute"),
        ):
            fx = Fixture(base_extra=(("usr/lib/unsafe", kind, target),))
            try:
                with self.subTest(kind=kind, target=target), self.assertRaisesRegex(
                    validator.ValidationError, "unsafe base-layer link target"
                ):
                    fx.validate()
            finally:
                fx.close()

    def test_spoofed_base_diffid_history_and_layer_reorder_fail_before_traversal(self):
        mutations = []

        def spoof_diffid(fx):
            config = json.loads(fx.config)
            config["rootfs"]["diff_ids"][0] = "sha256:" + "0" * 64
            fx.replace_derived([fx.base_layer, *fx.derived_layers], config_override=json.dumps(config, separators=(",", ":")).encode())

        def spoof_history(fx):
            fx.replace_derived([fx.base_layer, *fx.derived_layers], history=[{"created_by": "spoofed"}, *fx.history[1:]])

        def reorder(fx):
            fx.replace_derived([*fx.derived_layers, fx.base_layer])

        mutations.extend((spoof_diffid, spoof_history, reorder))
        for mutate in mutations:
            fx = Fixture()
            try:
                mutate(fx)
                with self.subTest(mutation=mutate.__name__), mock.patch.object(
                    validator, "scan_layer", side_effect=AssertionError("traversed unauthenticated layer")
                ) as scan:
                    with self.assertRaises(validator.ValidationError):
                        fx.validate()
                    scan.assert_not_called()
            finally:
                fx.close()

    def test_crafted_positional_layer_is_not_classified_as_inherited_base(self):
        crafted = layer_bytes([("tmp/positional-link", "symlink", "target")])
        fx = Fixture()
        self.addCleanup(fx.close)
        fx.replace_derived([crafted, fx.base_layer, *fx.derived_layers])
        with mock.patch.object(validator, "scan_layer", side_effect=AssertionError("traversed unauthenticated layer")) as scan:
            with self.assertRaises(validator.ValidationError):
                fx.validate()
            scan.assert_not_called()

    def test_spoofed_base_archive_diffid_history_and_reorder_are_rejected(self):
        for mutation in ("diffid", "history", "reorder"):
            fx = Fixture()
            try:
                if mutation == "diffid":
                    config = config_bytes(["sha256:" + "0" * 64], fx.base_history, derived=False)
                    fx.replace_base([fx.base_layer], config_override=config)
                elif mutation == "history":
                    fx.replace_base([fx.base_layer], history=[{"created_by": "spoofed"}])
                else:
                    second = layer_bytes([("etc/second", "file", b"second")])
                    fx.replace_base([second, fx.base_layer])
                with self.subTest(mutation=mutation), mock.patch.object(
                    validator, "scan_layer", side_effect=AssertionError("traversed unauthenticated layer")
                ) as scan:
                    with self.assertRaises(validator.ValidationError):
                        fx.validate()
                    scan.assert_not_called()
            finally:
                fx.close()

    def test_whiteouts_are_exact_and_cannot_escape(self):
        projection = {f"{validator.WEB_ROOT}/index.html": validator.ProjectedFile(b"x", 0o644)}
        for name in (
            f"{validator.WEB_ROOT}/.wh.",
            f"{validator.WEB_ROOT}/.wh...wh..opq",
            f"{validator.WEB_ROOT}/.wh.foo/bar",
            f"{validator.WEB_ROOT}/file.wh.bad",
        ):
            with self.subTest(name=name), self.assertRaises(validator.ValidationError):
                validator.apply_whiteout(name, projection)
        validator.apply_whiteout(f"{validator.WEB_ROOT}/.wh.index.html", projection)
        self.assertNotIn(f"{validator.WEB_ROOT}/index.html", projection)

    def test_final_root_opaque_layer_removes_all_canonical_selected_files(self):
        fx = Fixture()
        self.addCleanup(fx.close)
        fx.replace_derived([
            fx.base_layer,
            layer_bytes(fx.selected),
            layer_bytes([(".wh..wh..opq", "file", b"")]),
        ])
        with self.assertRaisesRegex(validator.ValidationError, "missing/extra entries"):
            fx.validate()

    def test_final_parent_directory_whiteout_removes_selected_descendants(self):
        fx = Fixture()
        self.addCleanup(fx.close)
        fx.replace_derived([
            fx.base_layer,
            layer_bytes(fx.selected),
            layer_bytes([("usr/share/nginx/.wh.html", "file", b"")]),
        ])
        with self.assertRaisesRegex(validator.ValidationError, "missing/extra entries"):
            fx.validate()

    def test_final_individual_whiteout_removes_selected_file(self):
        fx = Fixture()
        self.addCleanup(fx.close)
        fx.replace_derived([
            fx.base_layer,
            layer_bytes(fx.selected),
            layer_bytes([(f"{validator.WEB_ROOT}/.wh.index.html", "file", b"")]),
        ])
        with self.assertRaisesRegex(validator.ValidationError, "missing/extra entries"):
            fx.validate()

    def test_same_layer_opaque_preserves_recreated_files_regardless_of_tar_order(self):
        for whiteout_first in (True, False):
            fx = Fixture()
            try:
                opaque = (f"{validator.WEB_ROOT}/.wh..wh..opq", "file", b"")
                members = [opaque, *fx.selected] if whiteout_first else [*fx.selected, opaque]
                fx.replace_derived([fx.base_layer, layer_bytes(members)])
                with self.subTest(whiteout_first=whiteout_first):
                    fx.validate()
            finally:
                fx.close()

    def test_same_layer_whiteout_preserves_recreated_file_regardless_of_tar_order(self):
        for whiteout_first in (True, False):
            fx = Fixture()
            try:
                whiteout = (f"{validator.WEB_ROOT}/.wh.index.html", "file", b"")
                members = [whiteout, *fx.selected] if whiteout_first else [*fx.selected, whiteout]
                fx.replace_derived([fx.base_layer, layer_bytes(members)])
                with self.subTest(whiteout_first=whiteout_first):
                    fx.validate()
            finally:
                fx.close()

    def test_parent_whiteout_removes_nested_lower_descendants_but_not_same_layer_additions(self):
        projection = {
            f"{validator.WEB_ROOT}/nested/deep/lower.js": validator.ProjectedFile(b"lower", 0o644),
            f"{validator.WEB_ROOT}/sibling.js": validator.ProjectedFile(b"sibling", 0o644),
        }
        recreated = f"{validator.WEB_ROOT}/nested/deep/recreated.js"
        validator.scan_layer(layer_bytes([
            (recreated, "file", b"new"),
            (f"{validator.WEB_ROOT}/.wh.nested", "file", b""),
        ]), validator.Limits(), projection)
        self.assertNotIn(f"{validator.WEB_ROOT}/nested/deep/lower.js", projection)
        self.assertEqual(projection[recreated].data, b"new")
        self.assertIn(f"{validator.WEB_ROOT}/sibling.js", projection)

    def test_html_directory_opaque_removes_lower_descendants_but_preserves_same_layer_recreation(self):
        lower = f"{validator.WEB_ROOT}/nested/lower.js"
        recreated = f"{validator.WEB_ROOT}/nested/recreated.js"
        outside = validator.CONFIG_PATH
        projection = {
            lower: validator.ProjectedFile(b"lower", 0o644),
            outside: validator.ProjectedFile(b"config", 0o644),
        }
        validator.scan_layer(layer_bytes([
            (recreated, "file", b"new"),
            (f"{validator.WEB_ROOT}/.wh..wh..opq", "file", b""),
        ]), validator.Limits(), projection)
        self.assertNotIn(lower, projection)
        self.assertEqual(projection[recreated].data, b"new")
        self.assertIn(outside, projection)

    def test_valid_root_and_nested_whiteouts_and_malformed_forms_are_distinguished(self):
        projection = {
            validator.CONFIG_PATH: validator.ProjectedFile(b"config", 0o644),
            f"{validator.WEB_ROOT}/index.html": validator.ProjectedFile(b"index", 0o644),
        }
        validator.scan_layer(layer_bytes([
            (".wh.etc", "file", b""),
            (f"{validator.WEB_ROOT}/.wh.index.html", "file", b""),
        ]), validator.Limits(), projection)
        self.assertEqual(projection, {})
        for bad in (".wh.", ".wh...wh..opq", "path/file.wh.bad", "a/.wh.foo.wh.bar"):
            with self.subTest(bad=bad), self.assertRaises(validator.ValidationError):
                validator.scan_layer(layer_bytes([(bad, "file", b"")]), validator.Limits(), {})

    def test_budgets_are_bounded(self):
        limits = validator.Limits()
        self.assertLessEqual(limits.archive_bytes, 512 * 1024 * 1024)
        self.assertLessEqual(limits.members, 4096)
        self.assertLessEqual(limits.member_bytes, 256 * 1024 * 1024)
        self.assertLessEqual(limits.member_total_bytes, 512 * 1024 * 1024)
        self.assertLessEqual(limits.layers, 64)
        self.assertLessEqual(limits.selected_bytes, 32 * 1024 * 1024)

    def test_outer_member_total_byte_boundary_is_exact_and_checked_before_retaining_next_member(self):
        payload = outer_member_bytes((("first", b"12345678"), ("second", b"123456789")))
        with tempfile.TemporaryDirectory() as temp:
            path = os.path.join(temp, "outer.tar")
            with open(path, "wb") as output:
                output.write(payload)
            exact = validator.Limits(
                archive_bytes=len(payload), member_bytes=9, member_total_bytes=17
            )
            contents, _ = validator._read_outer(path, exact)
            self.assertEqual(sum(map(len, contents.values())), 17)
            with mock.patch.object(validator, "_bounded_read", wraps=validator._bounded_read) as read:
                with self.assertRaisesRegex(validator.ValidationError, "member bytes exceed total"):
                    validator._read_outer(path, dataclasses.replace(exact, member_total_bytes=16))
                self.assertEqual(read.call_count, 1)

    def test_compressed_outer_aggregate_expansion_is_bounded(self):
        entries = tuple((f"small/{index:04d}", b"x" * 1024) for index in range(128))
        payload = outer_member_bytes(entries, gz=True)
        self.assertLess(len(payload), 128 * 1024)
        with tempfile.TemporaryDirectory() as temp:
            path = os.path.join(temp, "outer.tar.gz")
            with open(path, "wb") as output:
                output.write(payload)
            limits = validator.Limits(
                archive_bytes=len(payload), members=128, member_bytes=1024,
                member_total_bytes=128 * 1024 - 1,
            )
            with self.assertRaisesRegex(validator.ValidationError, "member bytes exceed total"):
                validator._read_outer(path, limits)

    def test_outer_member_count_accepts_exactly_4096_small_members_and_rejects_4097(self):
        entries = tuple((f"small/{index:04d}", b"x") for index in range(4097))
        exact_payload = outer_member_bytes(entries[:4096])
        over_payload = outer_member_bytes(entries)
        with tempfile.TemporaryDirectory() as temp:
            exact_path = os.path.join(temp, "exact.tar")
            over_path = os.path.join(temp, "over.tar")
            for path, payload in ((exact_path, exact_payload), (over_path, over_payload)):
                with open(path, "wb") as output:
                    output.write(payload)
            limits = validator.Limits(
                archive_bytes=max(len(exact_payload), len(over_payload)), members=4096,
                member_bytes=1, member_total_bytes=4097,
            )
            contents, _ = validator._read_outer(exact_path, limits)
            self.assertEqual(len(contents), 4096)
            with self.assertRaisesRegex(validator.ValidationError, "member count"):
                validator._read_outer(over_path, limits)

    def test_layer_decompression_is_bounded(self):
        import gzip
        limits = validator.Limits(layer_bytes=32)
        bomb = gzip.compress(b"x" * 33)
        with self.assertRaisesRegex(validator.ValidationError, "expanded layer"):
            validator.expanded_layer(bomb, limits)


if __name__ == "__main__":
    unittest.main()
