#!/usr/bin/env python3
"""Cold-runner base preparation must match the unchanged release verifier."""
import importlib.util
import json
from pathlib import Path
import subprocess
import unittest
from unittest.mock import Mock

ROOT = Path(__file__).resolve().parents[1]


def module():
    spec = importlib.util.spec_from_file_location("prepare_bases", ROOT / "scripts/prepare-release-bases.py")
    assert spec is not None and spec.loader is not None
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


def image(ref):
    return {"Id": ref.split("@", 1)[1], "Os": "linux", "Architecture": "amd64", "RepoDigests": [ref]}


class ReleaseBaseTests(unittest.TestCase):
    def test_classic_config_ids_are_accepted_without_reinterpreting_the_index_pin(self):
        mod = module()
        # These config IDs are the linux/amd64 descriptors of the pinned indexes.
        config_ids = (
            "sha256:bf8527eb54c3680e728d5b4b383a8ba730d72dae7236fbc8dff97ed6b224a731",
            "sha256:6769dc3a703c719c1d2756bda113659be28ae16cf0da58dd5fd823d6b9a050ea",
        )
        values = []
        for ref, config_id in zip(mod.BASES, config_ids):
            inspected = image(ref)
            inspected["Id"] = config_id
            values.extend(("", json.dumps([inspected])))
        call = Mock(side_effect=values)
        try:
            receipts = mod.prepare(call)
        except ValueError as exc:
            self.fail("Known pinned classic config IDs must be accepted: " + str(exc))
        self.assertEqual([receipt["id"] for receipt in receipts], list(config_ids))
        self.assertEqual([receipt["reference"] for receipt in receipts], list(mod.BASES))

    def test_prepare_pulls_exact_bases_then_verifies_each_identity(self):
        mod = module()
        values = []
        for ref in mod.BASES:
            values.extend(("", json.dumps([image(ref)])))
        call = Mock(side_effect=values)
        receipt = mod.prepare(call)
        self.assertEqual(len(receipt), 2)
        self.assertEqual([x.args for x in call.call_args_list],
                         [args for ref in mod.BASES for args in
                          (("pull", "--platform", "linux/amd64", ref), ("image", "inspect", ref))])
        verifier = (ROOT / "scripts/verify-release.sh").read_text()
        for ref in mod.BASES:
            self.assertIn(ref, verifier)

    def test_wrong_identity_platform_digest_or_shape_is_rejected(self):
        mod = module()
        for field, value in (("Id", "sha256:" + "0" * 64), ("Os", "windows"),
                             ("Architecture", "arm64"), ("RepoDigests", [])):
            with self.subTest(field=field):
                wrong = image(mod.BASES[0])
                wrong[field] = value
                call = Mock(side_effect=["", json.dumps([wrong])])
                with self.assertRaises(ValueError):
                    mod.prepare(call)
                self.assertEqual(call.call_count, 2)
        for malformed in ("bad json", "[]", "{}", "[{}, {}]"):
            with self.subTest(malformed=malformed):
                with self.assertRaises(ValueError):
                    mod.prepare(Mock(side_effect=["", malformed]))

    def test_classic_identity_cannot_bypass_reference_platform_or_shape_checks(self):
        mod = module()
        config_ids = (
            "sha256:bf8527eb54c3680e728d5b4b383a8ba730d72dae7236fbc8dff97ed6b224a731",
            "sha256:6769dc3a703c719c1d2756bda113659be28ae16cf0da58dd5fd823d6b9a050ea",
        )
        for index, ref in enumerate(mod.BASES):
            for field, value in (
                    ("Id", config_ids[1 - index]), ("Os", "windows"),
                    ("Architecture", "arm64"), ("RepoDigests", []),
                    ("RepoDigests", [ref.replace("@", "-impostor@")]),
                    ("RepoDigests", ref), ("RepoDigests", [ref, None])):
                with self.subTest(reference=ref, field=field, value=value):
                    inspected = image(ref)
                    inspected["Id"] = config_ids[index]
                    inspected[field] = value
                    with self.assertRaises(ValueError):
                        mod.prepare(Mock(side_effect=["", json.dumps([inspected])]))

    def test_pull_errors_are_not_suppressed(self):
        mod = module()
        call = Mock(side_effect=subprocess.CalledProcessError(125, ["docker", "pull"]))
        with self.assertRaises(subprocess.CalledProcessError):
            mod.prepare(call)
        self.assertEqual(call.call_count, 1)

    def test_workflow_prepares_before_any_verifier_inventory_baseline(self):
        body = (ROOT / ".github/workflows/release.yml").read_text()
        self.assertLess(body.index("make release-bases"), body.index("make ci-clean-archive"))
        self.assertLess(body.index("make release-bases"), body.index("make verify-release-real"))
        self.assertIn("release-bases:", (ROOT / "Makefile").read_text())


if __name__ == "__main__":
    unittest.main()
