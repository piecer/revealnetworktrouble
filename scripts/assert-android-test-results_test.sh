#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
gate="$script_dir/assert-android-test-results.sh"
work=$(mktemp -d "${TMPDIR:-/tmp}/assert-android-results-test.XXXXXX")
trap 'rm -rf -- "$work"' EXIT HUP INT TERM

debug_class=com.checknetwork.app.DebugNetworkSecurityContractTest
release_class=com.checknetwork.app.ReleaseNetworkSecurityContractTest
generic_class=com.checknetwork.app.core.ReportParserTest
cases=0

reset_case() {
    case_dir=$work/$1
    rm -rf -- "$case_dir"
    mkdir -p -- "$case_dir/debug" "$case_dir/release"
}

write_suite_file() {
    file=$1
    suite=$2
    class=$3
    tests=$4
    errors=${5:-0}
    failures=${6:-0}
    mkdir -p -- "$(dirname -- "$file")"
    printf '%s\n' "<testsuite name=\"$suite\" tests=\"$tests\" failures=\"$failures\" errors=\"$errors\"><testcase name=\"contract\" classname=\"$class\"/></testsuite>" >"$file"
}

write_suite() {
    dir=$1
    class=$2
    tests=$3
    write_suite_file "$dir/TEST-$class.xml" "$class" "$class" "$tests"
}

write_good_pair() {
    write_suite "$case_dir/debug" "$debug_class" 1
    write_suite "$case_dir/debug" "$generic_class" 2
    write_suite "$case_dir/release" "$release_class" 1
    write_suite "$case_dir/release" "$generic_class" 2
}

write_binary_companion() {
    dir=$1/binary
    mkdir -p -- "$dir"
    : >"$dir/output.bin"
    printf '\001' >"$dir/output.bin.idx"
    printf 'gradle binary test metadata\n' >"$dir/results.bin"
}

expect_failure() {
    name=$1
    if "$gate" "$case_dir/debug" "$case_dir/release" >"$case_dir/stdout" 2>"$case_dir/stderr"; then
        printf 'not ok - %s unexpectedly passed\n' "$name" >&2
        exit 1
    fi
    cases=$((cases + 1))
    printf 'ok - %s\n' "$name"
}

expect_success() {
    name=$1
    if ! "$gate" "$case_dir/debug" "$case_dir/release" >"$case_dir/stdout" 2>"$case_dir/stderr"; then
        printf 'not ok - %s unexpectedly failed\n' "$name" >&2
        cat "$case_dir/stderr" >&2
        exit 1
    fi
    cases=$((cases + 1))
    printf 'ok - %s\n' "$name"
}

expect_usage_failure() {
    name=$1
    shift
    reset_case "usage-$cases"
    set +e
    "$gate" "$@" >"$case_dir/stdout" 2>"$case_dir/stderr"
    status=$?
    set -e
    if [ "$status" -ne 2 ]; then
        printf 'not ok - %s returned %s instead of 2\n' "$name" "$status" >&2
        exit 1
    fi
    cases=$((cases + 1))
    printf 'ok - %s\n' "$name"
}

expect_usage_failure 'zero directories rejected'
expect_usage_failure 'one directory rejected' "$work"
expect_usage_failure 'three directories rejected' "$work" "$work" "$work"

reset_case absent
expect_failure 'absent XML'

reset_case wrong-variants
write_suite "$case_dir/debug" "$release_class" 1
write_suite "$case_dir/release" "$debug_class" 1
expect_failure 'wrong variant classes'

reset_case contaminated
write_good_pair
write_suite "$case_dir/debug" "$release_class" 1
expect_failure 'forbidden exact suite anywhere'

reset_case zero
write_suite "$case_dir/debug" "$debug_class" 0
write_suite "$case_dir/debug" "$generic_class" 5
write_suite "$case_dir/release" "$release_class" 0
write_suite "$case_dir/release" "$generic_class" 5
expect_failure 'zero variant-specific tests despite generic totals'

reset_case arbitrary-text
write_good_pair
rm -f -- "$case_dir/debug/TEST-$debug_class.xml"
printf '%s\n' "<testsuite name=\"$generic_class\" tests=\"5\" failures=\"0\" errors=\"0\"><testcase name=\"generic\" classname=\"$generic_class\"/><system-out>$debug_class tests=\"99\"</system-out></testsuite>" >"$case_dir/debug/TEST-$debug_class.xml"
expect_failure 'generic mention and arbitrary text cannot satisfy identity'

reset_case substrings
write_good_pair
rm -f -- "$case_dir/debug/TEST-$debug_class.xml"
write_suite_file "$case_dir/debug/TEST-$debug_class-suffix.xml" "$debug_class-suffix" "$debug_class-suffix" 1
expect_failure 'class and basename substrings do not match'

reset_case duplicate-attributes
write_good_pair
printf '%s\n' "<testsuite name=\"$debug_class\" tests=\"1\" tests=\"9\" failures=\"0\" errors=\"0\"><testcase name=\"contract\" classname=\"$debug_class\"/></testsuite>" >"$case_dir/debug/TEST-$debug_class.xml"
expect_failure 'duplicate XML attributes are malformed'

reset_case swapped-filenames
write_good_pair
write_suite_file "$case_dir/debug/TEST-$debug_class.xml" "$release_class" "$release_class" 1
write_suite_file "$case_dir/release/TEST-$release_class.xml" "$debug_class" "$debug_class" 1
expect_failure 'swapped exact filenames cannot satisfy suite identity'

reset_case nested-spoof
write_good_pair
rm -f -- "$case_dir/debug/TEST-$debug_class.xml"
printf '%s\n' "<testsuite name=\"$generic_class\" tests=\"1\" failures=\"0\" errors=\"0\"><testsuite name=\"$debug_class\" tests=\"99\" failures=\"0\" errors=\"0\"><testcase name=\"spoof\" classname=\"$debug_class\"/></testsuite><testcase name=\"generic\" classname=\"$generic_class\"/></testsuite>" >"$case_dir/debug/TEST-$debug_class.xml"
expect_failure 'nested suite cannot spoof expected root identity'

reset_case malformed-other
write_good_pair
printf '%s\n' '<testsuite name="broken" tests="1">' >"$case_dir/debug/TEST-broken.xml"
expect_failure 'malformed unrelated XML rejects collection'

reset_case wrong-testcase-class
write_good_pair
write_suite_file "$case_dir/debug/TEST-$debug_class.xml" "$debug_class" "$generic_class" 1
expect_failure 'expected suite testcase class must match exactly'

reset_case nested-ordinary-suite
write_good_pair
mkdir -p -- "$case_dir/debug/nested"
mv -- "$case_dir/debug/TEST-$generic_class.xml" "$case_dir/debug/nested/TEST-other.OrdinarySuite.xml"
expect_failure 'nested ordinary suite is rejected'

reset_case duplicate-basename
write_good_pair
mkdir -p -- "$case_dir/debug/nested"
write_suite "$case_dir/debug/nested" "$debug_class" 1
expect_failure 'duplicate expected basename rejects ambiguity'

reset_case duplicate-ordinary-basename
write_good_pair
mkdir -p -- "$case_dir/debug/nested"
write_suite "$case_dir/debug/nested" "$generic_class" 2
expect_failure 'duplicate ordinary basename via nested directory rejects ambiguity'

reset_case nested-canary
write_good_pair
mkdir -p -- "$case_dir/debug/nested"
mv -- "$case_dir/debug/TEST-$debug_class.xml" "$case_dir/debug/nested/TEST-$debug_class.xml"
expect_failure 'nested variant canary is rejected'

reset_case empty-directory
write_good_pair
mkdir -p -- "$case_dir/debug/unexpected"
expect_failure 'unexpected empty directory is rejected'

reset_case nonempty-directory
write_good_pair
mkdir -p -- "$case_dir/debug/unexpected"
printf '%s\n' 'not XML' >"$case_dir/debug/unexpected/note.txt"
expect_failure 'unexpected nonempty directory is rejected'

reset_case non-xml-file
write_good_pair
printf '%s\n' 'not XML' >"$case_dir/debug/note.txt"
expect_failure 'direct non-XML file is rejected'

reset_case fifo
write_good_pair
mkfifo -- "$case_dir/debug/unexpected.fifo"
expect_failure 'FIFO result entry is rejected'

reset_case symlink-file
write_good_pair
mv -- "$case_dir/debug/TEST-$debug_class.xml" "$case_dir/outside.xml"
ln -s -- "$case_dir/outside.xml" "$case_dir/debug/TEST-$debug_class.xml"
expect_failure 'symlinked expected file is rejected'

reset_case symlink-directory
write_good_pair
mkdir -p -- "$case_dir/outside-dir"
write_suite "$case_dir/outside-dir" "$debug_class" 1
rm -f -- "$case_dir/debug/TEST-$debug_class.xml"
ln -s -- "$case_dir/outside-dir" "$case_dir/debug/nested"
expect_failure 'symlinked directory cannot supply expected file'

reset_case nonzero-errors
write_good_pair
write_suite_file "$case_dir/debug/TEST-$debug_class.xml" "$debug_class" "$debug_class" 1 1 0
expect_failure 'nonzero errors reject collection'

reset_case nonzero-failures
write_good_pair
write_suite_file "$case_dir/release/TEST-$release_class.xml" "$release_class" "$release_class" 1 0 1
expect_failure 'nonzero failures reject collection'

reset_case nested-count-once
write_good_pair
printf '%s\n' "<testsuite name=\"$generic_class\" tests=\"2\" failures=\"0\" errors=\"0\"><testsuite name=\"nested.generic\" tests=\"500\" failures=\"0\" errors=\"0\"></testsuite><testcase name=\"one\" classname=\"$generic_class\"/><testcase name=\"two\" classname=\"$generic_class\"/></testsuite>" >"$case_dir/debug/TEST-$generic_class.xml"
expect_success 'root suites count once and nested totals are ignored'
case "$(cat "$case_dir/stdout")" in
    *'debug unit-test collection verified:'*'(3 tests;'*) ;;
    *) printf 'not ok - root-only count was not 3\n' >&2; exit 1 ;;
esac

reset_case gradle-binary
write_good_pair
write_binary_companion "$case_dir/debug"
write_binary_companion "$case_dir/release"
expect_success 'exact Gradle binary companions are accepted but not counted'
case "$(cat "$case_dir/stdout")" in
    *'debug unit-test collection verified:'*'(3 tests;'*'release unit-test collection verified:'*'(3 tests;'*) ;;
    *) printf 'not ok - binary metadata changed XML test totals\n' >&2; exit 1 ;;
esac

reset_case binary-xml
write_good_pair
write_binary_companion "$case_dir/debug"
write_suite "$case_dir/debug/binary" nested.BinarySpoof 1
expect_failure 'XML under binary companion is rejected'

reset_case binary-nested-directory
write_good_pair
write_binary_companion "$case_dir/debug"
mkdir -p -- "$case_dir/debug/binary/nested"
printf '%s\n' metadata >"$case_dir/debug/binary/nested/data.bin"
expect_failure 'nested directory under binary companion is rejected'

reset_case binary-unexpected-file
write_good_pair
write_binary_companion "$case_dir/debug"
printf '%s\n' metadata >"$case_dir/debug/binary/unexpected.bin"
expect_failure 'unexpected binary companion metadata name is rejected'

reset_case binary-missing-file
write_good_pair
write_binary_companion "$case_dir/debug"
rm -f -- "$case_dir/debug/binary/output.bin.idx"
expect_failure 'incomplete binary companion layout is rejected'

reset_case binary-symlink
write_good_pair
write_binary_companion "$case_dir/debug"
mv -- "$case_dir/debug/binary/results.bin" "$case_dir/results.bin"
ln -s -- "$case_dir/results.bin" "$case_dir/debug/binary/results.bin"
expect_failure 'symlink under binary companion is rejected'

reset_case binary-fifo
write_good_pair
write_binary_companion "$case_dir/debug"
rm -f -- "$case_dir/debug/binary/results.bin"
mkfifo -- "$case_dir/debug/binary/results.bin"
expect_failure 'special file under binary companion is rejected'

reset_case binary-oversized
write_good_pair
write_binary_companion "$case_dir/debug"
truncate -s 67108865 "$case_dir/debug/binary/results.bin"
expect_failure 'oversized binary companion metadata is rejected'

reset_case binary-total-oversized
write_good_pair
write_binary_companion "$case_dir/debug"
truncate -s 50331648 "$case_dir/debug/binary/output.bin"
truncate -s 50331648 "$case_dir/debug/binary/output.bin.idx"
truncate -s 50331648 "$case_dir/debug/binary/results.bin"
expect_failure 'binary companion total byte bound is enforced'

reset_case good
write_good_pair
expect_success 'exact debug and release collections'

printf '%s script contract cases passed\n' "$cases"
