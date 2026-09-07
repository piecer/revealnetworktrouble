#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
root=$(mktemp -d "${TMPDIR:-/tmp}/checknetwork-clean-archive-test.XXXXXX")
cleanup() { rm -rf -- "$root"; }
trap cleanup EXIT HUP INT TERM

make_fixture() {
    case_name=$1
    metadata_kind=$2
    caller_goflags=$3
    case_root="$root/$case_name with spaces"
    fixture_repo="$case_root/repository"
    hostile_parent="$case_root/caller tmp parent"
    fixture_tmp="$hostile_parent/tmp directory"
    fake_bin="$case_root/fake bin"
    mkdir -p "$fixture_repo/scripts" "$fixture_tmp" "$fake_bin"

    case "$metadata_kind" in
        empty-directory) mkdir "$hostile_parent/.git" ;;
        malformed-file) printf '%s\n' 'this is not a gitdir file' >"$hostile_parent/.git" ;;
        *) printf 'unknown metadata fixture: %s\n' "$metadata_kind" >&2; exit 2 ;;
    esac

    cp "$repo_dir/scripts/ci-clean-archive.sh" "$fixture_repo/scripts/ci-clean-archive.sh"
    chmod 0755 "$fixture_repo/scripts/ci-clean-archive.sh"
    cat >"$fixture_repo/go.mod" <<'EOF'
module example.com/cleanarchive

go 1.25
EOF
    cat >"$fixture_repo/main.go" <<'EOF'
package main

func main() {}
EOF
    cat >"$fixture_repo/scripts/verify-release.sh" <<'EOF'
#!/bin/sh
set -eu
[ "$#" -eq 2 ]
[ -f "$2" ]
EOF
    chmod 0755 "$fixture_repo/scripts/verify-release.sh"
    cat >"$fake_bin/make" <<'EOF'
#!/bin/sh
set -eu
[ "$1" = ci-inner ]
case "$2" in CI_EVIDENCE_DIR=*) evidence_dir=${2#CI_EVIDENCE_DIR=} ;; *) exit 91 ;; esac
[ ! -e .git ]
[ "$(find . -name .git -print)" = "" ]
printf '%s' "${GOFLAGS-}" >"$ARCHIVE_TEST_GOFLAGS_OUT"
go build -o "$ARCHIVE_TEST_BINARY_OUT" .
mkdir -p "$evidence_dir" android/app/build/test-results/testDebugUnitTest android/app/build/test-results/testReleaseUnitTest
printf '%s\n' '{"Action":"pass","Test":"TestArchiveBuild"}' >"$evidence_dir/go-test.json"
printf '%s\n' '# tests 128' '# pass 128' '# fail 0' >"$evidence_dir/web-test.tap"
printf '%s\n' '<testsuite tests="134"></testsuite>' >android/app/build/test-results/testDebugUnitTest/results.xml
printf '%s\n' '<testsuite tests="134"></testsuite>' >android/app/build/test-results/testReleaseUnitTest/results.xml
EOF
    chmod 0755 "$fake_bin/make"

    (
        cd "$fixture_repo"
        git init -q
        git config user.name 'Clean Archive Test'
        git config user.email 'clean-archive@example.invalid'
        git add go.mod main.go scripts
        git commit -qm 'fixture'
        sha=$(git rev-parse HEAD)
        PATH="$fake_bin:$PATH" \
            TMPDIR="$fixture_tmp" \
            GOFLAGS="$caller_goflags" \
            ARCHIVE_TEST_GOFLAGS_OUT="$case_root/observed GOFLAGS" \
            ARCHIVE_TEST_BINARY_OUT="$case_root/archive binary" \
            ./scripts/ci-clean-archive.sh "$sha"
    )

    [ -x "$case_root/archive binary" ]
    observed=$(cat "$case_root/observed GOFLAGS")
    expected="${caller_goflags}${caller_goflags:+ }-buildvcs=false"
    [ "$observed" = "$expected" ] || {
        printf 'GOFLAGS mismatch for %s: expected <%s>, got <%s>\n' "$case_name" "$expected" "$observed" >&2
        exit 1
    }
}

make_fixture 'empty metadata' empty-directory '-mod=readonly -trimpath'
make_fixture 'malformed metadata' malformed-file '-mod=readonly'
make_fixture 'conflicting buildvcs' empty-directory '-buildvcs=true -mod=readonly'
printf 'ci-clean-archive adversarial cases: 3 passed\n'
