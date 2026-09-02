#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
wrapper_dir="$repo_root/android/gradle/wrapper"
jar="$wrapper_dir/gradle-wrapper.jar"
properties="$wrapper_dir/gradle-wrapper.properties"
expected_jar_sha="2db75c40782f5e8ba1fc278a5574bab070adccb2d21ca5a6e5ed840888448046"
expected_distribution_sha="f397b287023acdba1e9f6fc5ea72d22dd63669d59ed4a289a29b1a76eee151c6"
expected_distribution_url='distributionUrl=https\://services.gradle.org/distributions/gradle-8.11.1-bin.zip'

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

printf 'Gradle wrapper verified: 8.11.1\n'
printf 'wrapper JAR SHA-256: %s\n' "$actual_jar_sha"
printf 'distribution SHA-256: %s\n' "$actual_distribution_sha"
