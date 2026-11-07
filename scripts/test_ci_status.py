#!/usr/bin/env python3
"""Behavioral regression tests for the required GitHub check (stdlib only)."""
import itertools
import json
import os
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[1]


class RequiredStatusTests(unittest.TestCase):
    def test_every_job_outcome_combination_is_fail_closed(self):
        # Only a deliberately optional Full job may be skipped on pull requests.
        outcomes = ("success", "failure", "cancelled", "skipped", "", "unknown")
        for required in ("true", "false", "invalid"):
            for quick, security, full in itertools.product(outcomes, repeat=3):
                env = dict(os.environ, QUICK_RESULT=quick, SECURITY_RESULT=security,
                           FULL_RESULT=full, FULL_REQUIRED=required)
                result = subprocess.run([sys.executable, str(ROOT / "scripts/ci-status.py")],
                                        env=env, capture_output=True, text=True)
                expected = (required in ("true", "false") and quick == security == "success"
                            and (full == "success" or (required == "false" and full == "skipped")))
                self.assertEqual(result.returncode == 0, expected,
                                 (required, quick, security, full, result.stdout, result.stderr))
                if expected:
                    self.assertTrue(json.loads(result.stdout)["passed"])


if __name__ == "__main__":
    unittest.main()
