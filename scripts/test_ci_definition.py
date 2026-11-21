#!/usr/bin/env python3
"""Repository-specific CI contracts; actionlint handles the workflow schema."""
from pathlib import Path
import json
import re
import unittest

ROOT = Path(__file__).resolve().parents[1]


class PipelineDefinitionTests(unittest.TestCase):
    def test_release_builder_and_archive_contract_use_the_reviewed_ci_toolchain(self):
        # Keep the independently pinned image, compiler and offline contract together.
        version = "1.26.11"
        builder = "sha256:dc40a13b61304647ed7e5d581d246b86413dfc29d63c6d018310feb93cd98fc0"
        required = {
            "Dockerfile": ("FROM golang@" + builder + " AS build", "/checknetwork-api: go" + version),
            ".github/actions/toolchain/action.yml": ("go-version: '" + version + "'",),
            "scripts/verify_api_archive.py": ('go_version="go' + version + '"',),
            "scripts/verify_api_archive_test.py": ('GO_VERSION = "go' + version + '"',),
            "cmd/checknetwork-api/release_lifecycle_fake_test.go": ('"go_version":"go' + version + '"',),
            "README.md": ("GOTOOLCHAIN=go" + version + " go run ./cmd/checknetwork-api",),
            "README.en.md": ("GOTOOLCHAIN=go" + version + " go run ./cmd/checknetwork-api",),
        }
        for name, snippets in required.items():
            with self.subTest(path=name):
                body = (ROOT / name).read_text()
                for snippet in snippets:
                    self.assertIn(snippet, body)
        for name in ("README.md", "README.en.md"):
            for line in (ROOT / name).read_text().splitlines():
                if "go run ./cmd/checknetwork-api" in line:
                    self.assertIn("GOTOOLCHAIN=go" + version + " go run ./cmd/checknetwork-api", line)

    def test_npm_lock_contains_reviewed_security_fixes(self):
        # Audit feeds may lag a vendor advisory; keep the reviewed floor explicit.
        packages = json.loads((ROOT / "frontend/package-lock.json").read_text())["packages"]
        for name, minimum in (("source-map-js", (1, 2, 2)), ("undici", (8, 12, 4))):
            # Removed transitive dependencies need no floor; every installed copy,
            # including nested npm trees, must still meet the reviewed minimum.
            suffix = "node_modules/" + name
            for path, package in packages.items():
                if path != suffix and not path.endswith("/" + suffix):
                    continue
                with self.subTest(package=path):
                    value = package["version"]
                    self.assertRegex(value, r"^[0-9]+\.[0-9]+\.[0-9]+$")
                    version = tuple(int(part) for part in value.split("."))
                    self.assertGreaterEqual(version, minimum, path)

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
        for required in ("go-version: '1.26.11'", "node-version: '22.23.1'", "java-version: '17.0.21+9'",
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

    def test_android_unit_test_gates_match_the_agp9_task_model(self):
        # AGP 9 creates unit-test components only for the tested build type by
        # default, which removes testReleaseUnitTest. The gates intentionally run
        # and count both debug and release results, so the both-build-types
        # behavior must be pinned explicitly in gradle.properties.
        properties = (ROOT / "android/gradle.properties").read_text()
        self.assertIn("android.onlyEnableUnitTestForTheTestedBuildType=false", properties)
        for name in ("Makefile", "scripts/ci-inner.sh", "scripts/ci-clean-archive.sh"):
            body = (ROOT / name).read_text()
            for task in ("testDebugUnitTest", "testReleaseUnitTest"):
                self.assertIn(task, body)


if __name__ == "__main__":
    unittest.main()
