#!/bin/sh
# Run every scanner even when a sibling fails; retain each real failure code.
set -eu
repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_dir"
if [ -z "${CI_EVIDENCE_DIR:-}" ]; then
    printf '%s\n' 'CI_EVIDENCE_DIR is required for security scan receipts' >&2
    exit 2
fi
mkdir -p "$CI_EVIDENCE_DIR"
failed=0
for gate in security-test go-vuln npm-audit secret-scan; do
    log=$CI_EVIDENCE_DIR/$gate.log
    if make "$gate" >"$log" 2>&1; then
        cat "$log"
        printf 'CI_OK: %s\n' "$gate"
    else
        status=$?
        cat "$log" >&2
        printf 'CI_FAILED: %s (status %s)\n' "$gate" "$status" >&2
        failed=1
    fi
done
exit "$failed"
