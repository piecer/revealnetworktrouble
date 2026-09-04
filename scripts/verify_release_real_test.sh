#!/bin/sh
set -eu

umask 077
repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_dir"
tmp=
child_pid=
inventory_started=false
inventory_finished=false

terminate_child() {
    if [ "$child_pid" != "" ]; then
        kill -TERM "$child_pid" 2>/dev/null || :
        wait "$child_pid" 2>/dev/null || :
        child_pid=
    fi
}

cleanup() {
    cleanup_status=$?
    trap - EXIT HUP INT TERM
    terminate_child
    if [ "$inventory_started" = true ] && [ "$inventory_finished" = false ] && [ "$tmp" != "" ]; then
        capture_final_inventory || :
    fi
    cleanup_path=$tmp
    tmp=
    [ "$cleanup_path" = "" ] || rm -rf -- "$cleanup_path"
    exit "$cleanup_status"
}

handle_signal() {
    signal_status=$1
    trap '' HUP INT TERM
    terminate_child
    exit "$signal_status"
}

trap cleanup EXIT
trap 'handle_signal 129' HUP
trap 'handle_signal 130' INT
trap 'handle_signal 143' TERM

fail() {
    printf '%s\n' "$1" >&2
    exit 1
}

[ "$#" -eq 1 ] || {
    printf 'usage: %s EXACT_CURRENT_HEAD_SHA\n' "$0" >&2
    exit 2
}
revision=$1
case "$revision" in
    *[!0-9a-f]*) printf '%s\n' 'EXACT_CURRENT_HEAD_SHA must be 40 lowercase hexadecimal characters' >&2; exit 2 ;;
esac
[ "${#revision}" -eq 40 ] || {
    printf '%s\n' 'EXACT_CURRENT_HEAD_SHA must be 40 lowercase hexadecimal characters' >&2
    exit 2
}

resolved_revision=$(git rev-parse --verify "$revision^{commit}" 2>/dev/null) || fail 'requested SHA did not resolve exactly'
[ "$resolved_revision" = "$revision" ] || fail 'requested SHA did not resolve exactly'
current_head=$(git rev-parse HEAD 2>/dev/null) || fail 'current HEAD could not be resolved'
[ "$current_head" = "$revision" ] || fail 'real release verification accepts only the current exact HEAD SHA'
dirty=$(git status --porcelain --untracked-files=all 2>/dev/null) || fail 'working tree status could not be read'
[ "$dirty" = "" ] || fail 'working tree is not clean; refusing real release verification'

verifier=$repo_dir/scripts/verify-release.sh
inventory_verifier=$repo_dir/scripts/docker_inventory.py
[ -f "$verifier" ] && [ ! -L "$verifier" ] && [ -x "$verifier" ] || fail 'production release verifier must be an executable regular non-symlink file'
command -v docker >/dev/null 2>&1 || fail 'Docker is required for real release verification'
docker info >/dev/null 2>&1 || fail 'Docker daemon is required for real release verification'
command -v python3 >/dev/null 2>&1 || fail 'python3 is required for exact release output validation'
command -v cmp >/dev/null 2>&1 || fail 'cmp is required for real release verification'
command -v sort >/dev/null 2>&1 || fail 'sort is required for real release verification'
[ -f "$inventory_verifier" ] && [ ! -L "$inventory_verifier" ] || fail 'canonical Docker inventory verifier is missing or unsafe'

tmp=$(mktemp -d "${TMPDIR:-/tmp}/checknetwork-verify-release-real.XXXXXX") || fail 'could not create private real release verification directory'
chmod 0700 "$tmp" || fail 'could not protect private real release verification directory'
canonical_archive=$tmp/canonical-source.tar
first_stdout=$tmp/standalone.stdout
first_stderr=$tmp/standalone.stderr
first_status_file=$tmp/standalone.status
second_stdout=$tmp/canonical.stdout
second_stderr=$tmp/canonical.stderr
second_status_file=$tmp/canonical.status
third_stdout=$tmp/umask000.stdout
third_stderr=$tmp/umask000.stderr
third_status_file=$tmp/umask000.status

inventory() {
    inventory_prefix=$1
    python3 "$inventory_verifier" >"$inventory_prefix.canonical"
}

release_labels() {
    labels_output=$1
    labels_raw=$labels_output.raw
    : >"$labels_raw"
    for label_key in \
        com.checknetwork.release.nonce \
        com.checknetwork.release.owner
    do
        docker image ls --all --quiet --no-trunc --filter "label=$label_key" \
            >>"$labels_raw" || return 1
        docker container ls --all --quiet --no-trunc --filter "label=$label_key" \
            >>"$labels_raw" || return 1
        docker network ls --quiet --no-trunc --filter "label=$label_key" \
            >>"$labels_raw" || return 1
    done
    LC_ALL=C sort -u "$labels_raw" >"$labels_output" || return 1
}

capture_final_inventory() {
    inventory "$tmp/after" || {
        printf '%s\n' 'could not capture final Docker inventory' >&2
        return 1
    }
    release_labels "$tmp/after.release-labels" || {
        printf '%s\n' 'could not capture final release label inventory' >&2
        return 1
    }
    inventory_finished=true
    final_inventory_status=0
    if [ -s "$tmp/after.release-labels" ]; then
        printf '%s\n' 'release label residue detected after real release verification' >&2
        final_inventory_status=1
    fi
    if ! cmp -s "$tmp/before.canonical" "$tmp/after.canonical"; then
        printf '%s\n' 'Docker inventory changed during real release verification' >&2
        final_inventory_status=1
    fi
    [ "$final_inventory_status" -eq 0 ]
}

validate_success_output() {
    output_path=$1
    parsed_path=$2
    python3 - "$output_path" "$parsed_path" <<'PY'
import re
import sys

output_path, parsed_path = sys.argv[1:]
data = open(output_path, "rb").read()
hex_digest = rb"[0-9a-f]{64}"
pattern = re.compile(
    rb"release verified: api_archive=(" + hex_digest +
    rb") api_config=(sha256:" + hex_digest +
    rb") api_manifest=(sha256:" + hex_digest +
    rb") api_rootfs=(sha256:" + hex_digest +
    rb") api_binary=(" + hex_digest +
    rb") api_traceroute=(" + hex_digest + rb")\n" +
    rb"Web verified offline: archive=(" + hex_digest +
    rb") config=(sha256:" + hex_digest +
    rb") manifest=(sha256:" + hex_digest +
    rb") rootfs=(sha256:" + hex_digest +
    rb") asset_manifest=(" + hex_digest + rb")\n"
)
match = pattern.fullmatch(data)
if match is None or len(match.groups()) != 11:
    raise SystemExit("release verifier did not emit the exact two success lines with all 11 fields")
with open(parsed_path, "wb") as parsed:
    parsed.write(b"\n".join(match.groups()) + b"\n")
PY
}

inventory "$tmp/before" || fail 'could not capture initial Docker inventory'
release_labels "$tmp/before.release-labels" || fail 'could not capture initial release label inventory'
inventory_started=true
[ ! -s "$tmp/before.release-labels" ] || fail 'preexisting release label residue detected'

git archive "$revision" >"$canonical_archive" || fail 'canonical git archive generation failed'

set +e
(umask 077; exec "$verifier" "$revision") >"$first_stdout" 2>"$first_stderr" &
child_pid=$!
wait "$child_pid"
first_status=$?
child_pid=
set -e
printf '%s\n' "$first_status" >"$first_status_file"
[ "$first_status" -eq 0 ] || fail 'production release verification failed'
validate_success_output "$first_stdout" "$tmp/standalone.fields" || fail 'standalone production release success output is malformed'

current_head=$(git rev-parse HEAD 2>/dev/null) || fail 'current HEAD could not be revalidated'
[ "$current_head" = "$revision" ] || fail 'current HEAD changed between real release verification passes'
dirty=$(git status --porcelain --untracked-files=all 2>/dev/null) || fail 'working tree status could not be revalidated'
[ "$dirty" = "" ] || fail 'working tree changed between real release verification passes'

set +e
(umask 027; exec "$verifier" "$revision" "$canonical_archive") >"$second_stdout" 2>"$second_stderr" &
child_pid=$!
wait "$child_pid"
second_status=$?
child_pid=
set -e
printf '%s\n' "$second_status" >"$second_status_file"
[ "$second_status" -eq 0 ] || fail 'production release verification failed'
validate_success_output "$second_stdout" "$tmp/canonical.fields" || fail 'canonical-archive production release success output is malformed'
cmp -s "$first_stdout" "$second_stdout" || fail 'production release success output mismatch between standalone and canonical-archive runs'
cmp -s "$tmp/standalone.fields" "$tmp/canonical.fields" || fail 'production release success fields mismatch between runs'

set +e
(umask 000; exec "$verifier" "$revision" "$canonical_archive") >"$third_stdout" 2>"$third_stderr" &
child_pid=$!
wait "$child_pid"
third_status=$?
child_pid=
set -e
printf '%s\n' "$third_status" >"$third_status_file"
[ "$third_status" -eq 0 ] || fail 'production release verification failed'
validate_success_output "$third_stdout" "$tmp/umask000.fields" || fail 'umask-000 production release success output is malformed'
cmp -s "$first_stdout" "$third_stdout" || fail 'production release success output mismatch between restrictive umasks'
cmp -s "$tmp/standalone.fields" "$tmp/umask000.fields" || fail 'production release success fields mismatch between restrictive umasks'

current_head=$(git rev-parse HEAD 2>/dev/null) || fail 'current HEAD could not be finally revalidated'
[ "$current_head" = "$revision" ] || fail 'current HEAD changed during real release verification'
dirty=$(git status --porcelain --untracked-files=all 2>/dev/null) || fail 'working tree status could not be finally revalidated'
[ "$dirty" = "" ] || fail 'working tree changed during real release verification'

capture_final_inventory || fail 'Docker inventory or release label closure failed'

cat "$first_stderr" "$second_stderr" "$third_stderr" >&2
cat "$first_stdout"
