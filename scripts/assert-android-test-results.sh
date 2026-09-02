#!/bin/sh
set -eu

[ "$#" -gt 0 ] || { printf 'usage: %s TEST-RESULT-DIR...\n' "$0" >&2; exit 2; }

for result_dir in "$@"; do
    [ -d "$result_dir" ] || {
        printf 'missing Android unit-test result directory: %s\n' "$result_dir" >&2
        exit 1
    }

    # `find -exec` preserves filenames containing whitespace; concatenated JUnit
    # XML is sufficient because only tests="N" attributes are counted.
    total=$(find "$result_dir" -type f -name '*.xml' -exec cat -- {} + | awk '
        match($0, /tests="[0-9]+"/) {
            value = substr($0, RSTART + 7, RLENGTH - 8)
            sum += value
        }
        END { print sum + 0 }
    ')

    [ "$total" -gt 0 ] || {
        printf 'Android unit-test XML reports zero collected tests: %s\n' "$result_dir" >&2
        exit 1
    }

    printf 'Android unit-test collection verified: %s (%s tests)\n' "$result_dir" "$total"
done
