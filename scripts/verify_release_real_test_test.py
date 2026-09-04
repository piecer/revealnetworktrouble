#!/usr/bin/env python3
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import textwrap
import unittest

ROOT = Path(__file__).resolve().parents[1]
GATE = ROOT / "scripts" / "verify_release_real_test.sh"
INVENTORY = ROOT / "scripts" / "docker_inventory.py"
SHA = "0123456789abcdef0123456789abcdef01234567"
OTHER_SHA = "89abcdef0123456789abcdef0123456789abcdef"
DIGEST = "0" * 64
SUCCESS = (
    f"release verified: api_archive={DIGEST} api_config=sha256:{DIGEST} "
    f"api_manifest=sha256:{DIGEST} api_rootfs=sha256:{DIGEST} api_binary={DIGEST} api_traceroute={DIGEST}\n"
    f"Web verified offline: archive={DIGEST} config=sha256:{DIGEST} manifest=sha256:{DIGEST} "
    f"rootfs=sha256:{DIGEST} asset_manifest={DIGEST}\n"
)


def executable(path: Path, body: str) -> None:
    path.write_text(body, encoding="utf-8")
    path.chmod(0o700)


class FakeRepository:
    def __init__(self):
        self.temp = tempfile.TemporaryDirectory(prefix="verify-release-real-fake-")
        self.base = Path(self.temp.name)
        self.repo = self.base / "repo"
        self.bin = self.base / "bin"
        self.private_tmp = self.base / "tmp"
        (self.repo / "scripts").mkdir(parents=True)
        self.bin.mkdir()
        self.private_tmp.mkdir()
        shutil.copy2(GATE, self.repo / "scripts" / GATE.name)
        if INVENTORY.exists():
            shutil.copy2(INVENTORY, self.repo / "scripts" / INVENTORY.name)
        executable(
            self.repo / "scripts" / "verify-release.sh",
            textwrap.dedent(f"""\
                #!/bin/sh
                set -eu
                count_file=${{FAKE_STATE}}/release-count
                count=0
                [ ! -f "$count_file" ] || count=$(cat "$count_file")
                count=$((count + 1))
                printf '%s\n' "$count" >"$count_file"
                printf '%s\n' "$#:$*" >>"${{FAKE_STATE}}/release-args"
                printf '%s\n' "$(umask)" >>"${{FAKE_STATE}}/release-umasks"
                stat -Lc '%a' /proc/$$/fd/2 >>"${{FAKE_STATE}}/stderr-modes"
                stat -Lc '%a' "$(dirname "$(readlink /proc/$$/fd/2)")" >>"${{FAKE_STATE}}/tmp-modes"
                if [ "$#" -eq 2 ]; then cp "$2" "${{FAKE_STATE}}/supplied-archive"; fi
                if [ "${{FAKE_RELEASE_FAILURE:-}}" = 1 ]; then
                    printf '%s\n' 'private secret diagnostic' >&2
                    exit 98
                fi
                if [ "${{FAKE_SIGNAL:-}}" != "" ]; then
                    kill -"${{FAKE_SIGNAL}}" "$PPID"
                    exit 99
                fi
                output={SUCCESS!r}
                if [ "${{FAKE_OUTPUT_MISMATCH:-}}" = 1 ] && [ "$count" -eq 2 ]; then
                    output=$(printf '%s' "$output" | sed 's/api_binary={'0' * 64}/api_binary={'1' * 64}/')
                fi
                if [ "${{FAKE_SUCCESS_STDERR:-}}" = 1 ]; then
                    printf '%s\n' 'bounded build progress' >&2
                fi
                printf '%b' "$output"
            """),
        )
        executable(
            self.bin / "git",
            textwrap.dedent(f"""\
                #!/bin/sh
                set -eu
                case "$1" in
                    rev-parse)
                        case "${{2:-}}" in
                            --show-toplevel) printf '%s\n' "$FAKE_REPO" ;;
                            HEAD) printf '%s\n' "${{FAKE_HEAD:-{SHA}}}" ;;
                            --verify) printf '%s\n' "${{FAKE_RESOLVED:-{SHA}}}" ;;
                            *) exit 91 ;;
                        esac
                        ;;
                    status)
                        if [ "${{FAKE_DIRTY:-}}" = 1 ]; then printf '%s\n' '?? dirty-file'; fi
                        ;;
                    archive)
                        [ "$2" = "{SHA}" ] || exit 92
                        printf '%s' 'canonical archive bytes'
                        ;;
                    *) exit 93 ;;
                esac
            """),
        )
        executable(
            self.bin / "docker",
            textwrap.dedent(r"""
                #!/bin/sh
                set -eu
                printf '%s\n' "$*" >>"${FAKE_STATE}/docker-calls"
                count=0
                [ ! -f "${FAKE_STATE}/release-count" ] || count=$(cat "${FAKE_STATE}/release-count")
                case "$1 ${2:-}" in
                    'image ls')
                        case " $* " in *' --filter label='*)
                            if [ "${FAKE_RELEASE_LABEL_RESIDUE:-}" = 1 ] && [ "$count" -ge 3 ]; then printf '%s\n' residue; fi
                            ;; *) printf '%s\n' sha256:image-one ;; esac
                        ;;
                    'container ls')
                        case " $* " in *' --filter label='*)
                            if [ "${FAKE_RELEASE_LABEL_RESIDUE:-}" = 1 ] && [ "$count" -ge 3 ]; then printf '%s\n' residue; fi
                            ;; *) printf '%s\n' container-one ;; esac
                        ;;
                    'network ls')
                        case " $* " in *' --filter label='*)
                            if [ "${FAKE_RELEASE_LABEL_RESIDUE:-}" = 1 ] && [ "$count" -ge 3 ]; then printf '%s\n' residue; fi
                            ;; *) printf '%s\n' network-one ;; esac
                        ;;
                    'image inspect')
                        if [ "${FAKE_IMAGE_MUTATION:-}" = 1 ] && [ "$count" -ge 3 ]; then suffix=changed; else suffix=one; fi
                        printf '[{"Id":"sha256:image-%s","RepoTags":["z:tag","a:tag"],"RepoDigests":["z@sha256:2","a@sha256:1"]}]\n' "$suffix"
                        ;;
                    'container inspect')
                        if [ "${FAKE_CONTAINER_REPLACEMENT:-}" = 1 ] && [ "$count" -ge 3 ]; then id=container-two; else id=container-one; fi
                        if [ "${FAKE_CONTAINER_LABEL_MUTATION:-}" = 1 ] && [ "$count" -ge 3 ]; then label=changed; else label=value; fi
                        if [ "${FAKE_CONTAINER_MEMBERSHIP_MUTATION:-}" = 1 ] && [ "$count" -ge 3 ]; then network=network-two; else network=network-one; fi
                        if [ "$count" -ge 3 ]; then labels='"a":"'"$label"'","z":"last"'; aliases='"a","z"'; else labels='"z":"last","a":"'"$label"'"'; aliases='"z","a"'; fi
                        printf '[{"Id":"%s","Name":"/caller","Image":"sha256:caller","Config":{"Labels":{%s}},"NetworkSettings":{"Networks":{"caller-net":{"NetworkID":"%s","EndpointID":"endpoint-one","Aliases":[%s],"Gateway":"172.20.0.1","IPAddress":"172.20.0.2","IPPrefixLen":16,"GlobalIPv6Address":"","GlobalIPv6PrefixLen":0,"MacAddress":"02:42:ac:14:00:02"}}},"State":{"Status":"running","StartedAt":"%s"}}]\n' "$id" "$labels" "$network" "$aliases" "$count"
                        ;;
                    'network inspect')
                        if [ "${FAKE_NETWORK_LABEL_MUTATION:-}" = 1 ] && [ "$count" -ge 3 ]; then label=changed; else label=value; fi
                        if [ "${FAKE_NETWORK_MEMBERSHIP_MUTATION:-}" = 1 ] && [ "$count" -ge 3 ]; then member=container-two; else member=container-one; fi
                        if [ "$count" -ge 3 ]; then labels='"a":"'"$label"'","z":"last"'; else labels='"z":"last","a":"'"$label"'"'; fi
                        printf '[{"Id":"network-one","Name":"caller-net","Driver":"bridge","Scope":"local","Labels":{%s},"Containers":{"%s":{"Name":"caller","EndpointID":"endpoint-one","MacAddress":"02:42:ac:14:00:02","IPv4Address":"172.20.0.2/16","IPv6Address":""}}}]\n' "$labels" "$member"
                        ;;
                    'info ')
                        ;;
                    *) exit 94 ;;
                esac
            """).lstrip(),
        )

    def close(self):
        self.temp.cleanup()

    def run(self, revision=SHA, caller_umask=0o022, **changes):
        env = os.environ.copy()
        env.update(
            PATH=f"{self.bin}:{env['PATH']}",
            TMPDIR=str(self.private_tmp),
            FAKE_REPO=str(self.repo),
            FAKE_STATE=str(self.base),
        )
        env.update({key: str(value) for key, value in changes.items()})
        return subprocess.run(
            [str(self.repo / "scripts" / GATE.name), revision],
            cwd=self.repo,
            env=env,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=10,
            preexec_fn=lambda: os.umask(caller_umask),
        )


class VerifyReleaseRealGateTest(unittest.TestCase):
    def setUp(self):
        self.fake = FakeRepository()

    def tearDown(self):
        self.fake.close()

    def test_links_production_verifier_standalone_then_canonical_archive(self):
        result = self.fake.run()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, SUCCESS)
        calls = (self.fake.base / "release-args").read_text(encoding="utf-8").splitlines()
        self.assertEqual(calls[0], f"1:{SHA}")
        self.assertEqual(len(calls), 3)
        for call in calls[1:]:
            count, rest = call.split(":", 1)
            revision, archive = rest.split(" ", 1)
            self.assertEqual((count, revision), ("2", SHA))
            self.assertTrue(archive.endswith("/canonical-source.tar"))
        self.assertEqual((self.fake.base / "supplied-archive").read_bytes(), b"canonical archive bytes")

    def test_exact_restrictive_umasks_keep_gate_private_and_build_modes_deterministic(self):
        for caller_umask in (0o077, 0o027, 0o000):
            with self.subTest(caller_umask=oct(caller_umask)):
                result = self.fake.run(caller_umask=caller_umask)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(
                    (self.fake.base / "release-umasks").read_text(encoding="utf-8").splitlines(),
                    ["0077", "0027", "0000"],
                )
                self.assertEqual(set((self.fake.base / "stderr-modes").read_text().splitlines()), {"600"})
                self.assertEqual(set((self.fake.base / "tmp-modes").read_text().splitlines()), {"700"})
                for name in ("release-count", "release-args", "release-umasks", "stderr-modes", "tmp-modes", "docker-calls", "supplied-archive"):
                    (self.fake.base / name).unlink(missing_ok=True)

    def test_success_allows_captured_build_progress_on_stderr(self):
        result = self.fake.run(FAKE_SUCCESS_STDERR=1)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, SUCCESS)
        self.assertNotIn("bounded build progress", result.stdout)
        self.assertEqual(result.stderr.count("bounded build progress"), 3)

    def test_failure_emits_only_fixed_diagnostic_and_keeps_captured_stderr_private(self):
        result = self.fake.run(FAKE_RELEASE_FAILURE=1)
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stderr, "production release verification failed\n")
        self.assertNotIn("private secret diagnostic", result.stderr)
        self.assertEqual((self.fake.base / "stderr-modes").read_text().splitlines(), ["600"])

    def test_rejects_dirty_tree_before_docker_or_release(self):
        result = self.fake.run(FAKE_DIRTY=1)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("working tree is not clean", result.stderr)
        self.assertFalse((self.fake.base / "release-args").exists())
        self.assertFalse((self.fake.base / "docker-calls").exists())

    def test_rejects_malformed_mismatched_and_nonexact_revision(self):
        for revision, env in (
            ("HEAD", {}),
            (OTHER_SHA, {}),
            (SHA, {"FAKE_RESOLVED": OTHER_SHA}),
            (SHA, {"FAKE_HEAD": OTHER_SHA}),
        ):
            with self.subTest(revision=revision, env=env):
                result = self.fake.run(revision, **env)
                self.assertNotEqual(result.returncode, 0)

    def inventory_passes(self):
        calls_path = self.fake.base / "docker-calls"
        if not calls_path.exists():
            return 0
        return sum(
            line.startswith("image inspect")
            for line in calls_path.read_text(encoding="utf-8").splitlines()
        )

    def test_rejects_success_output_field_mismatch(self):
        result = self.fake.run(FAKE_OUTPUT_MISMATCH=1)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("success output mismatch", result.stderr)
        self.assertEqual(self.inventory_passes(), 2)

    def test_rejects_canonical_inventory_mutations_but_ignores_uptime_and_order(self):
        stable = self.fake.run()
        self.assertEqual(stable.returncode, 0, stable.stderr)
        for name in ("release-count", "release-args", "release-umasks", "stderr-modes", "tmp-modes", "docker-calls", "supplied-archive"):
            (self.fake.base / name).unlink(missing_ok=True)
        for mutation in (
            "FAKE_IMAGE_MUTATION",
            "FAKE_CONTAINER_REPLACEMENT",
            "FAKE_CONTAINER_LABEL_MUTATION",
            "FAKE_CONTAINER_MEMBERSHIP_MUTATION",
            "FAKE_NETWORK_LABEL_MUTATION",
            "FAKE_NETWORK_MEMBERSHIP_MUTATION",
        ):
            with self.subTest(mutation=mutation):
                result = self.fake.run(**{mutation: 1})
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("Docker inventory changed", result.stderr)
                for name in ("release-count", "release-args", "release-umasks", "stderr-modes", "tmp-modes", "docker-calls", "supplied-archive"):
                    (self.fake.base / name).unlink(missing_ok=True)

    def test_rejects_release_label_residue(self):
        result = self.fake.run(FAKE_RELEASE_LABEL_RESIDUE=1)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("release label residue", result.stderr)

    def test_signals_have_fixed_status_and_clean_private_temp(self):
        expected = {"HUP": 129, "INT": 130, "TERM": 143}
        for name, status in expected.items():
            with self.subTest(signal=name):
                result = self.fake.run(FAKE_SIGNAL=name)
                self.assertEqual(result.returncode, status, result.stderr)
                self.assertEqual(self.inventory_passes(), 2)
                self.assertEqual(list(self.fake.private_tmp.iterdir()), [])
                (self.fake.base / "release-count").unlink(missing_ok=True)
                (self.fake.base / "release-args").unlink(missing_ok=True)
                (self.fake.base / "release-umasks").unlink(missing_ok=True)
                (self.fake.base / "stderr-modes").unlink(missing_ok=True)
                (self.fake.base / "tmp-modes").unlink(missing_ok=True)
                (self.fake.base / "docker-calls").unlink(missing_ok=True)

    def test_make_and_docs_name_exact_gate_separately_from_component_gates(self):
        makefile = (ROOT / "Makefile").read_text(encoding="utf-8")
        operations = (ROOT / "docs" / "OPERATIONS.md").read_text(encoding="utf-8")
        testing = (ROOT / "docs" / "TESTING.md").read_text(encoding="utf-8")
        self.assertIn("verify-release-real:", makefile)
        self.assertIn('./scripts/verify_release_real_test.sh "$(REVISION)"', makefile)
        self.assertIn("make verify-release-real REVISION=<exact-40-character-HEAD-SHA>", operations)
        self.assertIn("verify-archives-real", operations)
        self.assertIn("component", operations.lower())
        self.assertIn("umask 077/027/000", operations)
        self.assertIn("canonical Docker inspect JSON", operations)
        self.assertIn("verify-release-real", testing)
        self.assertIn("umask 077/027/000", testing)

    def test_static_contract_has_no_recursive_caller_path_or_secret_dump(self):
        source = GATE.read_text(encoding="utf-8")
        self.assertIn('verifier=$repo_dir/scripts/verify-release.sh', source)
        self.assertIn('"$verifier" "$revision"', source)
        self.assertIn('git archive "$revision"', source)
        production = (ROOT / "scripts" / "verify-release.sh").read_text(encoding="utf-8")
        self.assertIn('(umask 022; tar -xf "$canonical_archive" -C "$archive_source")', production)
        self.assertIn('stat -c %a "$source_dir/frontend/nginx.conf"', production)
        self.assertIn('stat -c %a "$source_dir/frontend/$asset"', production)
        self.assertNotIn("printenv", source)
        self.assertNotIn("set -x", source)
        self.assertNotIn("$@", source)


if __name__ == "__main__":
    unittest.main()
