#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_dir"

cleanup_evidence=false
if [ "${CI_EVIDENCE_DIR:-}" = "" ]; then
    CI_EVIDENCE_DIR=$(mktemp -d "${TMPDIR:-/tmp}/checknetwork-ci-inner.XXXXXX")
    cleanup_evidence=true
else
    mkdir -p "$CI_EVIDENCE_DIR"
    CI_EVIDENCE_DIR=$(CDPATH= cd -- "$CI_EVIDENCE_DIR" && pwd)
fi
cleanup() {
    if [ "$cleanup_evidence" = true ]; then
        rm -rf -- "$CI_EVIDENCE_DIR"
    fi
}
trap cleanup EXIT HUP INT TERM

run_gate() {
    gate=$1
    log=$2
    shift 2
    if "$@" >"$log" 2>&1; then
        cat "$log"
        printf 'CI_OK: %s\n' "$gate"
    else
        status=$?
        cat "$log" >&2
        printf 'CI_FAILED: %s (status %s)\n' "$gate" "$status" >&2
        return "$status"
    fi
}

run_gate go-test "$CI_EVIDENCE_DIR/go-test.json" go test -count=1 -json ./...
run_gate web-test "$CI_EVIDENCE_DIR/web-test.tap" npm --prefix frontend test
run_gate web-syntax "$CI_EVIDENCE_DIR/web-syntax.log" npm --prefix frontend run test:syntax
run_gate go-race "$CI_EVIDENCE_DIR/go-race.log" go test -count=1 -race ./...
run_gate go-vet "$CI_EVIDENCE_DIR/go-vet.log" go vet ./...
run_gate go-build "$CI_EVIDENCE_DIR/go-build.log" go build -trimpath -o "$CI_EVIDENCE_DIR/checknetwork-api" ./cmd/checknetwork-api
run_gate android-wrapper "$CI_EVIDENCE_DIR/android-wrapper.log" ./scripts/verify-android-wrapper.sh
run_gate android-env "$CI_EVIDENCE_DIR/android-env.log" ./scripts/verify-android-env.sh
rm -rf -- android/app/build/test-results/testDebugUnitTest android/app/build/test-results/testReleaseUnitTest
run_gate android-test "$CI_EVIDENCE_DIR/android-test.log" sh -c 'cd android && ./gradlew --no-daemon --console=plain :app:testDebugUnitTest :app:testReleaseUnitTest'
run_gate android-results "$CI_EVIDENCE_DIR/android-results.log" ./scripts/assert-android-test-results.sh \
    android/app/build/test-results/testDebugUnitTest \
    android/app/build/test-results/testReleaseUnitTest
run_gate android-lint "$CI_EVIDENCE_DIR/android-lint.log" sh -c 'cd android && ./gradlew --no-daemon --console=plain :app:lintDebug :app:lintRelease'
run_gate android-assemble "$CI_EVIDENCE_DIR/android-assemble.log" sh -c 'cd android && ./gradlew --no-daemon --console=plain :app:assembleDebug :app:assembleRelease'
