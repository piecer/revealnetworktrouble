#!/usr/bin/env python3
"""Persistent fault matrix for the two API Docker resources."""
from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
HELPER = ROOT / "scripts" / "release-resource-ownership.sh"
NONCE = "1" * 32
OWNER = "2" * 32
BASE = "alpine@sha256:" + "d" * 64

FAKE_DOCKER = r'''#!/usr/bin/env python3
import json, os, pathlib, sys
args = sys.argv[1:]
state_path = pathlib.Path(os.environ["FAKE_STATE"])
state = json.loads(state_path.read_text())
state["calls"].append(args)
def save(): state_path.write_text(json.dumps(state, sort_keys=True))
def option(name, default=""):
    try: return args[args.index(name)+1]
    except (ValueError, IndexError): return default
def labels():
    out = {}
    for i, arg in enumerate(args[:-1]):
        if arg == "--label" and "=" in args[i+1]:
            key, value = args[i+1].split("=", 1); out[key] = value
    return out
def ref_after_inspect():
    skip = False
    for arg in args[2:]:
        if skip: skip = False; continue
        if arg == "--format": skip = True; continue
        return arg
    return ""
def missing(kind, ref):
    save(); sys.stderr.write("Error: No such %s: %s\n" % (kind, ref)); raise SystemExit(1)
def inspect_named(kind, ref):
    mode = os.environ.get("FAKE_MODE", "success")
    if os.environ.get("CLEANUP_PHASE") == "true" and mode == "cleanup_pre_inspect_error":
        save(); sys.stderr.write("daemon unavailable during cleanup inspect\n"); raise SystemExit(125)
    if state.get("removed_pending_inspect") == kind and mode == "cleanup_post_inspect_error":
        save(); sys.stderr.write("daemon unavailable after cleanup removal\n"); raise SystemExit(125)
    rec = state[kind]
    if rec is None or ref not in (rec["name"], rec["id"]): missing(kind, ref)
    return rec
def outcome(kind, name, nonce, owner, ident):
    mode = os.environ.get("FAKE_MODE", "success")
    rec = {"name": name, "nonce": nonce, "owner": owner, "id": ident}
    if mode == "name_mismatch": rec["name"] += "-other"
    if mode == "nonce_mismatch": rec["nonce"] = "caller-nonce"
    if mode in ("owner_mismatch", "race_conflict", "other_error", "signal_caller"): rec["owner"] = "caller-owner"
    if mode in ("race_conflict", "other_error", "signal_caller"): rec["id"] += "-caller"
    state[kind] = rec; save()
    if mode == "race_conflict": sys.stderr.write("Error response from daemon: Conflict. name already in use\n"); raise SystemExit(1)
    if mode in ("response_loss", "name_mismatch", "nonce_mismatch", "owner_mismatch"):
        sys.stderr.write("error during connect: unexpected EOF\n"); raise SystemExit(125)
    if mode == "other_error": sys.stderr.write("permission denied while waiting for response\n"); raise SystemExit(2)
    if mode == "empty": return ""
    if mode == "id_mismatch": return ident + "-wrong"
    return ident
if args[:2] in (["network", "inspect"], ["container", "inspect"]):
    kind=args[0]; rec=inspect_named(kind, ref_after_inspect())
    if "--format" in args: print("|".join([rec["id"], ("/" if kind == "container" else "")+rec["name"], rec["nonce"], rec["owner"]]))
    save(); raise SystemExit(0)
if args[:2] == ["network", "create"]:
    lab=labels(); result=outcome("network", args[-1], lab.get("com.checknetwork.release.nonce", ""), lab.get("com.checknetwork.release.owner", ""), "network-owned-id")
    if result: print(result)
    raise SystemExit(0)
if args[:2] == ["container", "create"]:
    lab=labels(); result=outcome("container", option("--name"), lab.get("com.checknetwork.release.nonce", ""), lab.get("com.checknetwork.release.owner", ""), "container-owned-id")
    if result: print(result)
    raise SystemExit(0)
if args[:2] in (["network", "rm"], ["container", "rm"]):
    kind=args[0]; ref=args[-1]; rec=state[kind]
    if rec is None or ref != rec["id"]: missing(kind, ref)
    if os.environ.get("FAKE_MODE") == "removal_failure":
        save(); sys.stderr.write("daemon refused cleanup removal\n"); raise SystemExit(2)
    state["removed"].append([kind, ref]); state[kind]=None
    if os.environ.get("FAKE_MODE") == "cleanup_post_inspect_error": state["removed_pending_inspect"] = kind
    save(); raise SystemExit(0)
if args[:1] == ["__replace"]:
    kind=args[1]; state[kind]["id"] += "-replacement"; save(); raise SystemExit(0)
save(); sys.stderr.write("unexpected invocation: %r\n" % args); raise SystemExit(98)
'''

DRIVER = r'''#!/bin/sh
set -eu
fail() { printf 'FAIL:%s\n' "$1" >&2; exit 1; }
release_hook() {
    [ "${SIGNAL_AT:-}" = "$1" ] || return 0
    kill -s "$SIGNAL_NAME" "$$"
}
tmp=$TEST_TMP
api_nonce=$NONCE
api_owner_token=$OWNER
api_label_key=com.checknetwork.release.nonce
api_owner_label_key=com.checknetwork.release.owner
api_network_name=api-network-$NONCE
api_network_id=
api_network_state=inactive
api_network_preflight_absent=false
api_container_name=api-smoke-$NONCE
api_container_role=smoke
api_container_id=
api_container_state=inactive
api_container_preflight_absent=false
api_base=$BASE
api_binary=$TEST_TMP/checknetwork-api
api_traceroute=$TEST_TMP/traceroute
printf binary >"$api_binary"
printf traceroute >"$api_traceroute"
. "$HELPER"
cleanup_one() { CLEANUP_PHASE=true; export CLEANUP_PHASE; if [ "$TYPE" = network ]; then cleanup_api_network; else cleanup_api_container; fi; }
cleanup_barrier() { cleanup_one || { printf 'release cleanup failed\n' >&2; return 1; }; }
on_signal() { code=$1; trap - EXIT; trap '' HUP INT TERM; if ! cleanup_one; then printf 'release cleanup failed\n' >&2; fi; exit "$code"; }
trap cleanup_one EXIT
trap 'on_signal 129' HUP
trap 'on_signal 130' INT
trap 'on_signal 143' TERM
if [ "${PENDING_ABSENT:-false}" = true ]; then
    if [ "$TYPE" = network ]; then api_network_state=create_pending; else api_container_state=create_pending; fi
    if [ "${PENDING_SIGNAL:-}" != "" ]; then kill -s "$PENDING_SIGNAL" "$$"; fi
    [ "${PENDING_FAIL:-false}" = false ] || exit 97
    cleanup_barrier
    printf 'SUCCESS pending-absent\n'
    exit 0
fi
if [ "$TYPE" = network ]; then create_owned_api_network; id=$api_network_id; else api_network_id=network-owned-id; create_owned_api_container; id=$api_container_id; fi
if [ "${REPLACE_AFTER:-false}" = true ]; then docker __replace "$TYPE"; fi
cleanup_barrier
printf 'SUCCESS id=%s\n' "$id"
[ "$PUBLICATION" = caller-publication ] || exit 97
'''


def initial_state(resource: str, scenario: str) -> dict:
    state = {"network": None, "container": None, "calls": [], "removed": [], "caller_bytes": "caller-publication", "removed_pending_inspect": None}
    if scenario == "collision":
        state[resource] = {
            "name": f"api-{resource if resource == 'network' else 'smoke'}-{NONCE}",
            "id": f"{resource}-caller-id",
            "nonce": NONCE,
            "owner": "caller-owner",
            "opaque": "preserve-byte-for-byte",
        }
    return state


def run_case(resource: str, scenario: str, signal_name: str = "") -> tuple[subprocess.CompletedProcess[str], dict, dict]:
    with tempfile.TemporaryDirectory(prefix="checknetwork-api-ownership-") as directory:
        work = Path(directory)
        fake = work / "docker"; fake.write_text(FAKE_DOCKER); fake.chmod(0o755)
        driver = work / "driver.sh"; driver.write_text(DRIVER); driver.chmod(0o755)
        before = initial_state(resource, scenario)
        state_path = work / "state.json"; state_path.write_text(json.dumps(before, sort_keys=True))
        env = os.environ.copy()
        env.update({
            "PATH": str(work) + os.pathsep + env["PATH"], "FAKE_STATE": str(state_path),
            "FAKE_MODE": "signal_caller" if scenario == "signal-caller" else ("success" if scenario in ("success", "replacement", "signal-owned") else scenario.replace("-", "_")),
            "TYPE": resource, "TEST_TMP": str(work), "HELPER": str(HELPER), "NONCE": NONCE,
            "OWNER": OWNER, "BASE": BASE, "PUBLICATION": "caller-publication",
            "SIGNAL_NAME": signal_name, "SIGNAL_AT": f"api_{resource}_create" if signal_name else "",
            "REPLACE_AFTER": "true" if scenario == "replacement" else "false",
            "PENDING_ABSENT": "true" if scenario.startswith("pending-absent") else "false",
            "PENDING_FAIL": "true" if scenario == "pending-absent-failure" else "false",
            "PENDING_SIGNAL": signal_name if scenario == "pending-absent-signal" else "",
        })
        result = subprocess.run([str(driver)], cwd=ROOT, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=20)
        return result, json.loads(state_path.read_text()), before


def require(ok: bool, message: str, result: subprocess.CompletedProcess[str], state: dict) -> None:
    if not ok: raise AssertionError(f"{message}\nstatus={result.returncode}\noutput={result.stdout}\nstate={json.dumps(state, indent=2, sort_keys=True)}")


def main() -> int:
    for resource in ("network", "container"):
        result, state, _ = run_case(resource, "success")
        require(result.returncode == 0 and state[resource] is None, f"{resource}: normal cleanup failed", result, state)
        for scenario in ("collision", "race-conflict", "other-error"):
            result, state, before = run_case(resource, scenario)
            require(result.returncode != 0 and "SUCCESS " not in result.stdout, f"{resource}/{scenario}: did not fail closed", result, state)
            if scenario == "collision":
                require(state[resource] == before[resource], f"{resource}/{scenario}: original caller identity was not preserved byte-for-byte", result, state)
            else:
                require(state[resource] is not None, f"{resource}/{scenario}: raced caller candidate disappeared", result, state)
            require(not state["removed"], f"{resource}/{scenario}: removal attempted", result, state)
        result, state, _ = run_case(resource, "response-loss")
        require(result.returncode == 0 and state[resource] is None, f"{resource}/response-loss failed", result, state)
        for scenario in ("name-mismatch", "nonce-mismatch", "owner-mismatch", "empty", "id-mismatch"):
            result, state, _ = run_case(resource, scenario)
            require(result.returncode != 0 and "SUCCESS " not in result.stdout, f"{resource}/{scenario}: succeeded", result, state)
        for scenario in ("removal-failure", "cleanup-pre-inspect-error", "cleanup-post-inspect-error", "replacement"):
            result, state, _ = run_case(resource, scenario)
            require(result.returncode != 0 and "SUCCESS " not in result.stdout, f"{resource}/{scenario}: false success", result, state)
            if scenario != "cleanup-post-inspect-error":
                require(state[resource] is not None, f"{resource}/{scenario}: resource unexpectedly absent", result, state)
            require("release cleanup failed" in result.stdout, f"{resource}/{scenario}: cleanup failure diagnostic missing", result, state)
        result, state, _ = run_case(resource, "pending-absent")
        require(result.returncode == 0 and "SUCCESS pending-absent" in result.stdout and state[resource] is None, f"{resource}/pending-absent success failed", result, state)
        result, state, _ = run_case(resource, "pending-absent-failure")
        require(result.returncode == 97 and "SUCCESS " not in result.stdout and state[resource] is None, f"{resource}/pending-absent ordinary failure changed", result, state)
        for signame, code in (("HUP", 129), ("INT", 130), ("TERM", 143)):
            result, state, _ = run_case(resource, "signal-owned", signame)
            require(result.returncode == code and "SUCCESS " not in result.stdout and state[resource] is None, f"{resource}/{signame}: owned signal failure", result, state)
            result, state, _ = run_case(resource, "signal-caller", signame)
            require(result.returncode == code and "SUCCESS " not in result.stdout and state[resource] is not None and not state["removed"], f"{resource}/{signame}: caller signal failure", result, state)
            require(result.stdout == "release cleanup failed\n", f"{resource}/{signame}: signal diagnostic leaked resource data", result, state)
            result, state, _ = run_case(resource, "pending-absent-signal", signame)
            require(result.returncode == code and "SUCCESS " not in result.stdout and state[resource] is None, f"{resource}/{signame}: pending absent signal changed", result, state)
    helper = HELPER.read_text()
    for forbidden in ("api_image", "build_owned_api_image", "cleanup_current_api_image", "docker image", "--iidfile", "--tag"):
        if forbidden in helper: raise AssertionError(f"pre-IID/image lifecycle must be structurally impossible: {forbidden}")
    create = helper[helper.index("create_owned_api_container()") : helper.index("cleanup_api_container()")]
    for required in ("--user 65532:65532", "CHECKNETWORK_ADDR=:8080", "PATH=/:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "--entrypoint /checknetwork-api", "readonly", "--health-cmd"):
        if required not in create: raise AssertionError(f"smoke container contract missing {required}")
    print("API release resource ownership matrix passed (2 resources, lifecycle faults and 6 signal boundaries)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
