#!/bin/sh
set -eu

[ "$#" -eq 2 ] || {
    printf 'usage: %s DEBUG-TEST-RESULT-DIR RELEASE-TEST-RESULT-DIR\n' "$0" >&2
    exit 2
}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
exec python3 "$script_dir/assert_android_test_results.py" "$1" "$2"
