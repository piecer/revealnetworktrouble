#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
wrapper_dir="$repo_root/android/gradle/wrapper"
jar="$wrapper_dir/gradle-wrapper.jar"
properties="$wrapper_dir/gradle-wrapper.properties"
# Independently checked against Gradle's version-specific official checksum endpoints.
expected_jar_sha="621008afc4f5d59357fcf06ffa644dd5aade5e091bdc363742710445f73cdaf8"
expected_distribution_sha="971fcd7150ca4ba04852c0b2ca0d2d0ab9468e14b03473eab19528809914c76b"
expected_distribution_url='distributionUrl=https\://services.gradle.org/distributions/gradle-9.9.2-bin.zip'

[ -f "$jar" ] || { printf 'missing Gradle wrapper JAR: %s\n' "$jar" >&2; exit 1; }
[ -f "$properties" ] || { printf 'missing Gradle wrapper properties: %s\n' "$properties" >&2; exit 1; }

if command -v sha256sum >/dev/null 2>&1; then
    actual_jar_sha=$(sha256sum "$jar" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
    actual_jar_sha=$(shasum -a 256 "$jar" | awk '{print $1}')
else
    printf 'wrapper verification requires sha256sum or shasum\n' >&2
    exit 1
fi
[ "$actual_jar_sha" = "$expected_jar_sha" ] || {
    printf 'Gradle wrapper JAR checksum mismatch\nexpected: %s\nactual:   %s\n' \
        "$expected_jar_sha" "$actual_jar_sha" >&2
    exit 1
}

# Check raw bytes before awk can split on NUL or skip comments containing CR.
# od emits only decimal ASCII; check its status before validating that output.
if ! properties_octets=$(LC_ALL=C od -A n -v -t u1 "$properties"); then
    printf 'cannot read Gradle wrapper properties bytes (requires working od)\n' >&2
    exit 1
fi
if ! LC_ALL=C awk '
    {
        for (i = 1; i <= NF; i++) {
            if ($i != 10 && ($i < 32 || $i > 126)) {
                print "noncanonical Gradle wrapper properties: only printable ASCII and LF are allowed"
                exit 1
            }
        }
    }
' >&2 <<EOF
$properties_octets
EOF
then
    exit 1
fi

# Accept only an unambiguous subset of Java Properties before reading the pins.
if ! LC_ALL=C awk '
    function reject(reason) {
        printf "noncanonical Gradle wrapper properties at line %d: %s\n", NR, reason
        exit 1
    }
    /^ *(#|!|$)/ { next }
    !/^[A-Za-z][A-Za-z0-9_.-]*=[^=]*$/ {
        reject("expected an unescaped key=value with exactly one equals sign")
    }
    /\\$/ { reject("continuation lines are not allowed") }
    {
        key = $0
        sub(/=.*/, "", key)
        if (seen[key]++) reject("duplicate key: " key)
    }
' "$properties" >&2; then
    exit 1
fi

actual_distribution_sha=$(awk -F= '$1 == "distributionSha256Sum" { print $2 }' "$properties")
[ "$actual_distribution_sha" = "$expected_distribution_sha" ] || {
    printf 'Gradle distribution checksum mismatch\nexpected: %s\nactual:   %s\n' \
        "$expected_distribution_sha" "${actual_distribution_sha:-<missing>}" >&2
    exit 1
}

actual_distribution_url=$(awk '$0 ~ /^distributionUrl=/ { print }' "$properties")
[ "$actual_distribution_url" = "$expected_distribution_url" ] || {
    printf 'unexpected Gradle distribution URL\nexpected: %s\nactual:   %s\n' \
        "$expected_distribution_url" "${actual_distribution_url:-<missing>}" >&2
    exit 1
}

printf 'Gradle wrapper verified: 9.9.2\n'
printf 'wrapper JAR SHA-256: %s\n' "$actual_jar_sha"
printf 'distribution SHA-256: %s\n' "$actual_distribution_sha"
