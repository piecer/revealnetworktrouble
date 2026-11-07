#!/usr/bin/env python3
"""Release input validation must fail before interpreting an untrusted revision."""
import importlib.util
from pathlib import Path
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]


class ReleaseInputTests(unittest.TestCase):
    def test_exact_revision_clean_tree_and_tag_identity(self):
        spec = importlib.util.spec_from_file_location("release_ref", ROOT / "scripts/ci-release-ref.py")
        assert spec is not None and spec.loader is not None
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        sha = "a" * 40
        for bad in ("", "main", "a" * 39, "A" * 40, sha + "\n", "$(touch injected)"):
            with self.subTest(bad=bad), patch.object(module, "git") as git:
                with self.assertRaises(ValueError):
                    module.validate(bad, "workflow_dispatch", "refs/heads/main", "0.1.0\n")
                git.assert_not_called()
        for event, ref, version, status, head, valid in (
            ("workflow_dispatch", "refs/heads/main", "0.1.0\n", "", sha, True),
            ("push", "refs/tags/v0.1.0-rc.1", "0.1.0-rc.1\n", "", sha, True),
            ("push", "refs/tags/v0.1.0-rc.1", "0.1.0\n", "", sha, False),
            ("push", "refs/heads/main", "0.1.0\n", "", sha, False),
            ("pull_request", "refs/pull/1/merge", "0.1.0\n", "", sha, False),
            ("workflow_dispatch", "refs/heads/main", "0.1.0\n", " M file\n", sha, False),
            ("workflow_dispatch", "refs/heads/main", "0.1.0\n", "", "b" * 40, False),
        ):
            with self.subTest(event=event, ref=ref, valid=valid):
                with patch.object(module, "git", side_effect=[head + "\n", status]):
                    if valid:
                        self.assertEqual(module.validate(sha, event, ref, version), sha)
                    else:
                        with self.assertRaises(ValueError):
                            module.validate(sha, event, ref, version)


if __name__ == "__main__":
    unittest.main()
