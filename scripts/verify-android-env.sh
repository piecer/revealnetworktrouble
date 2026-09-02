#!/bin/sh
set -eu

java_home=${JAVA_HOME:-}
sdk_root=${ANDROID_HOME:-${ANDROID_SDK_ROOT:-}}

[ -n "$java_home" ] || { printf 'JAVA_HOME must point to JDK 17\n' >&2; exit 1; }
[ -x "$java_home/bin/java" ] || { printf 'JAVA_HOME has no executable bin/java: %s\n' "$java_home" >&2; exit 1; }

java_major=$("$java_home/bin/java" -version 2>&1 | awk -F'[".]' '/version/ { print $2; exit }')
[ "$java_major" = "17" ] || {
    printf 'JDK 17 is required; JAVA_HOME reports major version %s\n' "${java_major:-unknown}" >&2
    exit 1
}

[ -n "$sdk_root" ] || { printf 'ANDROID_HOME or ANDROID_SDK_ROOT must point to Android SDK 35\n' >&2; exit 1; }
[ -f "$sdk_root/platforms/android-35/android.jar" ] || {
    printf 'missing Android SDK platform: %s/platforms/android-35/android.jar\n' "$sdk_root" >&2
    exit 1
}
[ -x "$sdk_root/build-tools/35.0.0/aapt2" ] || {
    printf 'missing Android SDK Build-Tools 35.0.0: %s/build-tools/35.0.0/aapt2\n' "$sdk_root" >&2
    exit 1
}

printf 'Android environment verified: JDK 17, platform 35, build-tools 35.0.0\n'
