#!/usr/bin/env python3
"""Verify every security gate runs and no scanner error becomes success."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
GATES = ("security-test", "go-vuln", "npm-audit", "secret-scan")


class SecurityGateTests(unittest.TestCase):
    def test_success_and_every_failure_keep_real_status(self):
        for failure in ("", *GATES):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as temp:
                work = Path(temp)
                (work / "scripts").mkdir()
                shutil.copy2(ROOT / "scripts/ci-security.sh", work / "scripts")
                fake = work / "make"
                fake.write_text("#!/bin/sh\nprintf '%s\\n' \"$1\" >>\"$CALLS\"\n"
                                "[ \"$1\" != \"$FAIL_GATE\" ] || exit 42\n")
                fake.chmod(0o755)
                env = dict(os.environ, PATH=str(work) + os.pathsep + os.environ["PATH"],
                           CALLS=str(work / "calls"), FAIL_GATE=failure,
                           CI_EVIDENCE_DIR=str(work / "evidence"))
                result = subprocess.run(["sh", str(work / "scripts/ci-security.sh")],
                                        env=env, capture_output=True, text=True)
                self.assertEqual((work / "calls").read_text().splitlines(), list(GATES))
                self.assertEqual(result.returncode == 0, not failure)
                logs = list((work / "evidence").glob("*.log"))
                self.assertEqual(len(logs), len(GATES))
                if failure:
                    self.assertIn("CI_FAILED: " + failure + " (status 42)", result.stdout + result.stderr)
                    self.assertNotIn("CI_OK: " + failure, result.stdout)


if __name__ == "__main__":
    unittest.main()
