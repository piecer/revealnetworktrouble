#!/usr/bin/env python3
"""Process-boundary regressions for the independently pinned Android wrapper."""
from pathlib import Path
import os
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class AndroidWrapperVerificationTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        (self.root / "scripts").mkdir()
        self.wrapper = self.root / "android/gradle/wrapper"
        self.wrapper.mkdir(parents=True)
        shutil.copy2(ROOT / "scripts/verify-android-wrapper.sh", self.root / "scripts/verify-android-wrapper.sh")
        for name in ("gradle-wrapper.jar", "gradle-wrapper.properties"):
            shutil.copy2(ROOT / "android/gradle/wrapper" / name, self.wrapper / name)

    def verify(self):
        # Invoke the real verifier without running the JAR or downloading Gradle.
        return subprocess.run(["sh", str(self.root / "scripts/verify-android-wrapper.sh")],
                              capture_output=True, text=True, check=False)

    def test_checked_in_wrapper_matches_independently_pinned_release(self):
        result = self.verify()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("Gradle wrapper verified: 9.9.2", result.stdout)

    def test_canonical_comments_and_blank_lines_are_accepted(self):
        properties = self.wrapper / "gradle-wrapper.properties"
        properties.write_bytes(
            b"# Wrapper pins\n! Ordinary comment\n  # Indented comment\n\n   \n"
            + properties.read_bytes()
            + b"\n# End of wrapper properties\n"
        )
        result = self.verify()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_duplicate_keys_and_redefinitions_are_rejected(self):
        properties = self.wrapper / "gradle-wrapper.properties"
        original = properties.read_bytes()
        url_line = next(line for line in original.splitlines() if line.startswith(b"distributionUrl="))
        variants = {
            "identical-pinned-url": url_line + b"\n",
            "redefined-pinned-url": b"distributionUrl=https\\://example.invalid/gradle.zip\n",
            "identical-other-key": b"networkTimeout=10000\n",
            "redefined-other-key": b"networkTimeout=1\n",
        }
        for name, duplicate in variants.items():
            with self.subTest(name=name):
                properties.write_bytes(original + b"\n" + duplicate)
                result = self.verify()
                self.assertNotEqual(result.returncode, 0, name + ": duplicate key passed verification")

    def test_modified_jar_is_rejected(self):
        jar = self.wrapper / "gradle-wrapper.jar"
        jar.write_bytes(jar.read_bytes() + b"tampered-fixture")
        result = self.verify()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("wrapper JAR checksum mismatch", result.stdout + result.stderr)

    def test_properties_cannot_replace_independent_checksum(self):
        properties = self.wrapper / "gradle-wrapper.properties"
        lines = properties.read_text().splitlines()
        properties.write_text("\n".join(
            "distributionSha256Sum=" + "0" * 64 if line.startswith("distributionSha256Sum=") else line
            for line in lines) + "\n")
        result = self.verify()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("distribution checksum mismatch", (result.stdout + result.stderr).lower())

    def test_other_distribution_url_is_rejected(self):
        properties = self.wrapper / "gradle-wrapper.properties"
        properties.write_text(properties.read_text().replace("gradle-9.9.2-bin.zip", "gradle-9.9.3-bin.zip"))
        result = self.verify()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("unexpected Gradle distribution URL", result.stdout + result.stderr)

    def test_nul_prefixed_pins_are_rejected(self):
        self.assert_nul_prefixed_pins_rejected(dict(os.environ))

    def test_busybox_awk_rejects_nul_prefixed_pins(self):
        busybox = shutil.which("busybox")
        if busybox is None:
            self.skipTest("optional BusyBox awk implementation is not installed")
        tools = self.root / "bin"
        tools.mkdir()
        (tools / "awk").symlink_to(busybox)
        self.assert_nul_prefixed_pins_rejected(
            dict(os.environ, PATH=str(tools) + os.pathsep + os.environ["PATH"]))

    def assert_nul_prefixed_pins_rejected(self, environment):
        properties = self.wrapper / "gradle-wrapper.properties"
        original = properties.read_bytes()
        # BusyBox awk splits on NUL while Java treats it as part of the key.
        for key in (b"distributionUrl=", b"distributionSha256Sum="):
            with self.subTest(key=key.decode()):
                properties.write_bytes(original.replace(key, b"\x00" + key))
                result = subprocess.run(
                    ["sh", str(self.root / "scripts/verify-android-wrapper.sh")],
                    env=environment, capture_output=True, text=True, check=False)
                self.assertNotEqual(result.returncode, 0,
                                    "NUL-prefixed pin passed wrapper verification")

    def test_missing_or_failing_byte_reader_is_rejected(self):
        tools = self.root / "byte-reader-bin"
        tools.mkdir()
        checksum_command = "sha256sum" if shutil.which("sha256sum") else "shasum"
        for command in ("dirname", checksum_command, "awk"):
            command_path = shutil.which(command)
            assert command_path is not None, command + " is required for this fixture"
            (tools / command).symlink_to(command_path)
        shell = shutil.which("sh")
        assert shell is not None, "sh is required for this fixture"
        environment = dict(os.environ, PATH=str(tools))
        for reader in ("missing", "failing-after-output"):
            with self.subTest(reader=reader):
                if reader == "failing-after-output":
                    od = tools / "od"
                    # Valid decimal output must not hide the producer's failure.
                    od.write_text("#!/bin/sh\nprintf ' 35 10\\n'\nexit 7\n")
                    od.chmod(0o755)
                result = subprocess.run(
                    [shell, str(self.root / "scripts/verify-android-wrapper.sh")],
                    env=environment, capture_output=True, text=True, check=False)
                self.assertNotEqual(result.returncode, 0,
                                    "unavailable or failing byte reader passed verification")
                self.assertIn("cannot read Gradle wrapper properties bytes", result.stderr)

    def test_noncanonical_overrides_cannot_change_java_effective_pins(self):
        properties = self.wrapper / "gradle-wrapper.properties"
        original = properties.read_bytes()
        # Java Properties accepts these spellings; the verifier's narrower
        # canonical format must reject them rather than overlook a later value.
        variants = {
            "leading-space": b" distributionUrl=https\\://example.invalid/gradle.zip\n",
            "leading-tab": b"\tdistributionSha256Sum=" + b"0" * 64 + b"\n",
            "leading-form-feed": b"\fdistributionUrl=https\\://example.invalid/gradle.zip\n",
            "escaped-key": b"distribution\\u0055rl=https\\://example.invalid/gradle.zip\n",
            "colon-separator": b"distributionUrl:https\\://example.invalid/gradle.zip\n",
            "space-separator": b"distributionUrl https\\://example.invalid/gradle.zip\n",
            "continued-key": b"distributionUr\\\nl=https\\://example.invalid/gradle.zip\n",
            "carriage-return-in-comment": b"# harmless comment\rdistributionUrl=https\\://example.invalid/gradle.zip\n",
        }
        for name, override in variants.items():
            with self.subTest(name=name):
                properties.write_bytes(original + b"\n" + override)
                result = self.verify()
                self.assertNotEqual(result.returncode, 0, name + ": ambiguous properties passed verification")


if __name__ == "__main__":
    unittest.main()
