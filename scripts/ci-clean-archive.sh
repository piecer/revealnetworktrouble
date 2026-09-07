#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_dir"

[ "$#" -eq 1 ] || {
    printf 'usage: %s EXACT_HEAD_SHA\n' "$0" >&2
    exit 2
}
requested_sha=$1
case "$requested_sha" in
    *[!0-9a-f]*) printf 'EXACT_HEAD_SHA must be 40 lowercase hexadecimal characters\n' >&2; exit 2 ;;
esac
[ "${#requested_sha}" -eq 40 ] || {
    printf 'EXACT_HEAD_SHA must be 40 lowercase hexadecimal characters\n' >&2
    exit 2
}
resolved_sha=$(git rev-parse --verify "$requested_sha^{commit}")
[ "$resolved_sha" = "$requested_sha" ] || {
    printf 'requested SHA did not resolve exactly: %s\n' "$requested_sha" >&2
    exit 1
}
[ "$(git rev-parse HEAD)" = "$requested_sha" ] || {
    printf 'clean archive accepts only the current exact HEAD SHA\n' >&2
    exit 1
}
# Refuse tracked or untracked candidates: git archive must never silently omit
# uncommitted Stage6 files. Run `make ci` before commit, then this postcommit gate.
dirty=$(git status --porcelain --untracked-files=all)
[ "$dirty" = "" ] || {
    printf 'working tree is not clean; refusing a committed archive that would omit:\n%s\n' "$dirty" >&2
    exit 1
}

tmp=$(mktemp -d "${TMPDIR:-/tmp}/checknetwork-clean-archive.XXXXXX")
cleanup() { rm -rf -- "$tmp"; }
trap cleanup EXIT HUP INT TERM
archive_dir=$tmp/source
evidence_dir=$tmp/evidence
mkdir -p "$archive_dir" "$evidence_dir"
git archive "$requested_sha" >"$tmp/source.tar"
tar -xf "$tmp/source.tar" -C "$archive_dir"

preexisting=$(find "$archive_dir" -type d \( -name .git -o -name node_modules -o -name build \) -print)
[ "$preexisting" = "" ] || {
    printf 'archive contains pre-existing generated/metadata directories:\n%s\n' "$preexisting" >&2
    exit 1
}

(
    cd "$archive_dir"
    archive_goflags=${GOFLAGS-}
    GOFLAGS="${archive_goflags}${archive_goflags:+ }-buildvcs=false" \
        make ci-inner CI_EVIDENCE_DIR="$evidence_dir"
)
./scripts/verify-release.sh "$requested_sha" "$tmp/source.tar"

go_tests=$(awk '/"Action":"pass"/ && /"Test":"/ { count++ } END { print count + 0 }' "$evidence_dir/go-test.json")
[ "$go_tests" -gt 0 ] || {
    printf 'Go test collection was zero\n' >&2
    exit 1
}

node_tests=$(awk '$2 == "tests" && $3 ~ /^[0-9]+$/ { count=$3 } END { print count + 0 }' "$evidence_dir/web-test.tap")
node_pass=$(awk '$2 == "pass" && $3 ~ /^[0-9]+$/ { count=$3 } END { print count + 0 }' "$evidence_dir/web-test.tap")
node_fail=$(awk '$2 == "fail" && $3 ~ /^[0-9]+$/ { count=$3 } END { print count + 0 }' "$evidence_dir/web-test.tap")
[ "$node_tests" -ge 128 ] && [ "$node_pass" -ge 128 ] && [ "$node_fail" -eq 0 ] || {
    printf 'node pass count contract failed: tests=%s pass=%s fail=%s (need >=128 pass, 0 fail)\n' "$node_tests" "$node_pass" "$node_fail" >&2
    exit 1
}

android_count() {
    find "$1" -type f -name '*.xml' -exec awk '
        match($0, /tests="[0-9]+"/) {
            value = substr($0, RSTART + 7, RLENGTH - 8)
            sum += value
        }
        END { print sum + 0 }
    ' {} + | awk '{ sum += $1 } END { print sum + 0 }'
}
debug_tests=$(android_count "$archive_dir/android/app/build/test-results/testDebugUnitTest")
release_tests=$(android_count "$archive_dir/android/app/build/test-results/testReleaseUnitTest")
[ "$debug_tests" -ge 134 ] || {
    printf 'Android debug test count contract failed: %s (need >=134)\n' "$debug_tests" >&2
    exit 1
}
[ "$release_tests" -ge 134 ] || {
    printf 'Android release test count contract failed: %s (need >=134)\n' "$release_tests" >&2
    exit 1
}

[ "$(git rev-parse HEAD)" = "$requested_sha" ] || {
    printf 'original worktree HEAD changed during archive gate\n' >&2
    exit 1
}
post_status=$(git status --porcelain --untracked-files=all)
[ "$post_status" = "" ] || {
    printf 'original worktree changed during archive gate:\n%s\n' "$post_status" >&2
    exit 1
}

printf 'clean archive verified at %s: Go=%s Web=%s Android(debug=%s release=%s)\n' \
    "$requested_sha" "$go_tests" "$node_pass" "$debug_tests" "$release_tests"
