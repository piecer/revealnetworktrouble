#!/usr/bin/env python3
"""Repository-specific CI contracts; actionlint handles the workflow schema."""
from pathlib import Path
import json
import re
import unittest

ROOT = Path(__file__).resolve().parents[1]


class PipelineDefinitionTests(unittest.TestCase):
    def test_npm_lock_contains_reviewed_security_fixes(self):
        # Audit feeds may lag a vendor advisory; keep the reviewed floor explicit.
        packages = json.loads((ROOT / "frontend/package-lock.json").read_text())["packages"]
        for name, minimum in (("source-map-js", (1, 2, 2)), ("undici", (8, 12, 4))):
            version = tuple(int(part) for part in packages["node_modules/" + name]["version"].split("."))
            self.assertGreaterEqual(version, minimum, name)

    def test_workflows_are_read_only_pinned_and_keep_failure_evidence(self):
        workflows = [ROOT / ".github/workflows/ci.yml", ROOT / ".github/workflows/release.yml"]
        files = [*workflows, ROOT / ".github/actions/toolchain/action.yml"]
        for path in files:
            body = path.read_text()
            for ref in re.findall(r"uses: ([^\s#]+)", body):
                if not ref.startswith("./"):
                    self.assertRegex(ref, r"^[\w./-]+@[a-f0-9]{40}$")
            for forbidden in ("continue-on-error:", "pull_request_target:", "contents: write", "secrets: inherit"):
                self.assertNotIn(forbidden, body)
        for path in workflows:
            body = path.read_text()
            self.assertIn("contents: read", body)
            self.assertIn("persist-credentials: false", body)
            self.assertIn("if: always()", body)
            self.assertIn("if-no-files-found: error", body)
            self.assertIn("timeout-minutes:", body)
            self.assertNotIn("paths:", body)
        ci = workflows[0].read_text()
        for required in ("pull_request:", "merge_group:", "schedule:", "name: CI required",
                         "needs: [quick, full, security]", "make ci-quick ci-workflows", "make ci-security",
                         "FULL_REQUIRED: ${{ github.event_name != 'pull_request' }}",
                         "if: github.event_name != 'pull_request'", "python3 scripts/ci-status.py"):
            self.assertIn(required, ci)
        release = workflows[1].read_text()
        for required in ("python3 scripts/ci-release-ref.py", "make ci-clean-archive", "make verify-release-real",
                         "make release", 'REVISION="$REQUESTED_SHA"', "sha256sum -c SHA256SUMS"):
            self.assertIn(required, release)
        toolchain = files[2].read_text()
        self.assertIn("make release-vuln", release)
        for required in ("go-version: '1.26.10'", "node-version: '22.23.1'", "java-version: '17.0.21+9'",
                         "platforms;android-35 build-tools;35.0.0"):
            self.assertIn(required, toolchain)

    def test_make_reuses_existing_gates_and_scanners_fail_closed(self):
        body = (ROOT / "Makefile").read_text()
        for target in ("ci-quick:", "ci-policy:", "ci-workflows:", "ci-security:", "security-test:",
                       "go-vuln:", "npm-audit:", "secret-scan:", "release-vuln:"):
            self.assertIn(target, body)
        for pinned in ("actionlint@v1.7.12", "govulncheck@v1.9.0", "gitleaks/v8@v8.30.1"):
            self.assertIn(pinned, body)
        self.assertIn("--audit-level=high", body)
        self.assertIn("--redact=100", body)
        self.assertIn("--log-opts=--all", body)
        self.assertNotIn("audit --omit=dev", body)
        self.assertNotIn("|| true", body)


if __name__ == "__main__":
    unittest.main()
