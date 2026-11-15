#!/usr/bin/env python3
"""Regressions for reviewed security floors after dependency-tree changes."""
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import test_ci_definition as definition


class ReviewedSecurityFloorTests(unittest.TestCase):
    def run_policy(self, packages):
        # Feed a synthetic lockfile to the actual repository policy test.
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "frontend").mkdir()
            (root / "frontend/package-lock.json").write_text(json.dumps({"packages": packages}))
            result = unittest.TestResult()
            case = definition.PipelineDefinitionTests("test_npm_lock_contains_reviewed_security_fixes")
            with patch.object(definition, "ROOT", root):
                case.run(result)
            return result

    def test_removed_transitive_packages_do_not_fail_version_floor_check(self):
        result = self.run_policy({"node_modules/undici": {"version": "8.12.4"}})
        self.assertTrue(result.wasSuccessful(), result.errors + result.failures)

    def test_nested_vulnerable_copy_is_rejected_even_when_root_copy_is_safe(self):
        result = self.run_policy({
            "node_modules/source-map-js": {"version": "1.2.2"},
            "node_modules/undici": {"version": "8.12.4"},
            "node_modules/dependency/node_modules/source-map-js": {"version": "1.2.1"},
        })
        self.assertFalse(result.wasSuccessful(), "nested vulnerable package bypassed the reviewed floor")

    def test_vulnerable_undici_is_rejected(self):
        result = self.run_policy({
            "node_modules/source-map-js": {"version": "1.2.2"},
            "node_modules/undici": {"version": "8.12.3"},
        })
        self.assertFalse(result.wasSuccessful())

    def test_safe_nested_copies_are_accepted(self):
        result = self.run_policy({
            "node_modules/source-map-js": {"version": "1.2.2"},
            "node_modules/undici": {"version": "8.12.4"},
            "node_modules/dependency/node_modules/source-map-js": {"version": "1.3.0"},
            "node_modules/dependency/node_modules/undici": {"version": "9.0.0"},
        })
        self.assertTrue(result.wasSuccessful(), result.errors + result.failures)

    def test_unparseable_version_fails_closed(self):
        result = self.run_policy({
            "node_modules/source-map-js": {"version": "1.2.2"},
            "node_modules/undici": {"version": "unknown"},
        })
        self.assertFalse(result.wasSuccessful())


if __name__ == "__main__":
    unittest.main()
