#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
make_bin=$(command -v make)
system_readlink=$(command -v readlink)
base_path=$PATH
tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/checknetwork-android-env-test.XXXXXX")
cleanup() {
    rm -rf -- "$tmp_dir"
}
trap cleanup EXIT HUP INT TERM

fake_home=$tmp_dir/home
fake_jdk=$tmp_dir/fake-jdk
fake_path=$tmp_dir/path
standard_sdk=$fake_home/Android/Sdk
managed_sdk=$fake_home/.local/share/checknetwork-android/sdk
mkdir -p "$fake_jdk/bin" "$fake_path" "$standard_sdk/platforms/android-35" "$standard_sdk/build-tools/35.0.0"
: >"$standard_sdk/platforms/android-35/android.jar"

cat >"$fake_jdk/bin/javac" <<'EOF'
#!/bin/sh
exit 0
EOF
cat >"$fake_jdk/bin/java" <<'EOF'
#!/bin/sh
printf '%s\n' 'openjdk version "17.0.99"' >&2
EOF
cat >"$standard_sdk/build-tools/35.0.0/aapt2" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod +x "$fake_jdk/bin/javac" "$fake_jdk/bin/java" "$standard_sdk/build-tools/35.0.0/aapt2"
ln -s "$fake_jdk/bin/javac" "$fake_path/javac"

probe_makefile=$tmp_dir/probe.mk
cat >"$probe_makefile" <<'EOF'
.PHONY: android-env-bootstrap-probe
android-env-bootstrap-probe:
	@printf 'make JAVA_HOME=<%s> ANDROID_HOME=<%s> ANDROID_SDK_ROOT=<%s>\n' '$(JAVA_HOME)' '$(ANDROID_HOME)' '$(ANDROID_SDK_ROOT)'
	@printf 'recipe JAVA_HOME=<%s> ANDROID_HOME=<%s> ANDROID_SDK_ROOT=<%s>\n' "$$JAVA_HOME" "$$ANDROID_HOME" "$$ANDROID_SDK_ROOT"
EOF

probe() {
    (
        unset JAVA_HOME ANDROID_HOME ANDROID_SDK_ROOT
        HOME=$fake_home PATH="$fake_path:$base_path" \
            "$make_bin" --no-print-directory -s -f "$repo_dir/Makefile" -f "$probe_makefile" \
            android-env-bootstrap-probe "$@"
    )
}

assert_output() {
    expected=$1
    shift
    actual=$(probe "$@")
    [ "$actual" = "$expected" ] || {
        printf 'unexpected bootstrap output\nexpected:\n%s\nactual:\n%s\n' "$expected" "$actual" >&2
        exit 1
    }
}

standard_expected="make JAVA_HOME=<$fake_jdk> ANDROID_HOME=<$standard_sdk> ANDROID_SDK_ROOT=<$standard_sdk>
recipe JAVA_HOME=<$fake_jdk> ANDROID_HOME=<$standard_sdk> ANDROID_SDK_ROOT=<$standard_sdk>"
assert_output "$standard_expected"

rm -rf -- "$standard_sdk"
mkdir -p "$standard_sdk" "$managed_sdk/platforms/android-35" "$managed_sdk/build-tools/35.0.0"
: >"$managed_sdk/platforms/android-35/android.jar"
cat >"$managed_sdk/build-tools/35.0.0/aapt2" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod +x "$managed_sdk/build-tools/35.0.0/aapt2"
managed_expected="make JAVA_HOME=<$fake_jdk> ANDROID_HOME=<$managed_sdk> ANDROID_SDK_ROOT=<$managed_sdk>
recipe JAVA_HOME=<$fake_jdk> ANDROID_HOME=<$managed_sdk> ANDROID_SDK_ROOT=<$managed_sdk>"
assert_output "$managed_expected"

invalid_home=$tmp_dir/invalid-android-home
invalid_root=$tmp_dir/invalid-android-root
explicit_java=$tmp_dir/invalid-java-home
assert_output "make JAVA_HOME=<$fake_jdk> ANDROID_HOME=<$invalid_home> ANDROID_SDK_ROOT=<$invalid_home>
recipe JAVA_HOME=<$fake_jdk> ANDROID_HOME=<$invalid_home> ANDROID_SDK_ROOT=<$invalid_home>" \
    ANDROID_HOME="$invalid_home"
assert_output "make JAVA_HOME=<$fake_jdk> ANDROID_HOME=<$invalid_root> ANDROID_SDK_ROOT=<$invalid_root>
recipe JAVA_HOME=<$fake_jdk> ANDROID_HOME=<$invalid_root> ANDROID_SDK_ROOT=<$invalid_root>" \
    ANDROID_SDK_ROOT="$invalid_root"
assert_output "make JAVA_HOME=<$fake_jdk> ANDROID_HOME=<$invalid_home> ANDROID_SDK_ROOT=<$invalid_home>
recipe JAVA_HOME=<$fake_jdk> ANDROID_HOME=<$invalid_home> ANDROID_SDK_ROOT=<$invalid_home>" \
    ANDROID_HOME="$invalid_home" ANDROID_SDK_ROOT="$invalid_root"
assert_output "make JAVA_HOME=<$explicit_java> ANDROID_HOME=<$managed_sdk> ANDROID_SDK_ROOT=<$managed_sdk>
recipe JAVA_HOME=<$explicit_java> ANDROID_HOME=<$managed_sdk> ANDROID_SDK_ROOT=<$managed_sdk>" \
    JAVA_HOME="$explicit_java"
assert_output "make JAVA_HOME=<> ANDROID_HOME=<$managed_sdk> ANDROID_SDK_ROOT=<$managed_sdk>
recipe JAVA_HOME=<> ANDROID_HOME=<$managed_sdk> ANDROID_SDK_ROOT=<$managed_sdk>" \
    JAVA_HOME=

bsd_tools=$tmp_dir/bsd-tools
mkdir -p "$bsd_tools"
cat >"$bsd_tools/readlink" <<'EOF'
#!/bin/sh
if [ "${1-}" = -f ]; then
    printf '%s\n' 'readlink: illegal option -- f' >&2
    exit 1
fi
printf '%s\n' "${1-}" >>"$READLINK_CALL_LOG"
exec "$SYSTEM_READLINK" "$@"
EOF
cat >"$bsd_tools/realpath" <<'EOF'
#!/bin/sh
printf '%s\n' 'realpath must not be called' >&2
exit 127
EOF
chmod +x "$bsd_tools/readlink" "$bsd_tools/realpath"
ln -s "$(command -v dirname)" "$bsd_tools/dirname"
ln -s /usr/bin/printf "$bsd_tools/printf"

portable_probe() {
    javac_dir=$1
    (
        unset JAVA_HOME ANDROID_HOME ANDROID_SDK_ROOT
        HOME=$fake_home PATH="$javac_dir:$bsd_tools" \
            SYSTEM_READLINK=$system_readlink READLINK_CALL_LOG=$tmp_dir/readlink-calls.log \
            "$make_bin" --no-print-directory -s -f "$repo_dir/Makefile" -f "$probe_makefile" \
            android-env-bootstrap-probe
    )
}

assert_portable_java_home() {
    expected=$1
    javac_dir=$2
    : >"$tmp_dir/readlink-calls.log"
    actual=$(portable_probe "$javac_dir")
    expected_output="make JAVA_HOME=<$expected> ANDROID_HOME=<$managed_sdk> ANDROID_SDK_ROOT=<$managed_sdk>
recipe JAVA_HOME=<$expected> ANDROID_HOME=<$managed_sdk> ANDROID_SDK_ROOT=<$managed_sdk>"
    [ "$actual" = "$expected_output" ] || {
        printf 'unexpected portable JAVA_HOME\nexpected:\n%s\nactual:\n%s\n' "$expected_output" "$actual" >&2
        exit 1
    }
}

absolute_root="$tmp_dir/absolute chain"
mkdir -p "$absolute_root/path" "$absolute_root/links" "$absolute_root/jdk home/bin"
cp "$fake_jdk/bin/javac" "$absolute_root/jdk home/bin/javac"
chmod +x "$absolute_root/jdk home/bin/javac"
ln -s "$absolute_root/links/hop two" "$absolute_root/path/javac"
ln -s "$absolute_root/jdk home/bin/javac" "$absolute_root/links/hop two"
assert_portable_java_home "$absolute_root/jdk home" "$absolute_root/path"

relative_root="$tmp_dir/relative chain"
mkdir -p "$relative_root/path" "$relative_root/links one" "$relative_root/fake jdk relative/bin"
cp "$fake_jdk/bin/javac" "$relative_root/fake jdk relative/bin/javac"
chmod +x "$relative_root/fake jdk relative/bin/javac"
ln -s '../links one/./hop' "$relative_root/path/javac"
ln -s '../fake jdk relative/bin/../bin/javac' "$relative_root/links one/hop"
assert_portable_java_home "$relative_root/fake jdk relative" "$relative_root/path"

literal_root="$tmp_dir/literal \$(touch SHOULD_NOT_EXIST); chain"
mkdir -p "$literal_root/path" "$literal_root/jdk/bin"
cp "$fake_jdk/bin/javac" "$literal_root/jdk/bin/javac"
chmod +x "$literal_root/jdk/bin/javac"
ln -s '../jdk/bin/javac' "$literal_root/path/javac"
assert_portable_java_home "$literal_root/jdk" "$literal_root/path"
[ ! -e "$repo_dir/SHOULD_NOT_EXIST" ] || {
    printf '%s\n' 'JAVA_HOME derivation executed path text as shell syntax' >&2
    exit 1
}

cycle_root=$tmp_dir/cycle
mkdir -p "$cycle_root/path" "$cycle_root/links"
ln -s '../links/one' "$cycle_root/path/javac"
ln -s two "$cycle_root/links/one"
ln -s one "$cycle_root/links/two"
assert_portable_java_home '' "$cycle_root/path"

make_chain() {
    root=$1
    links=$2
    mkdir -p "$root/path" "$root/links" "$root/jdk/bin"
    cp "$fake_jdk/bin/javac" "$root/jdk/bin/javac"
    chmod +x "$root/jdk/bin/javac"
    ln -s '../links/link-1' "$root/path/javac"
    index=1
    while [ "$index" -lt "$links" ]; do
        next=$((index + 1))
        ln -s "link-$next" "$root/links/link-$index"
        index=$next
    done
    ln -s '../jdk/bin/javac' "$root/links/link-$links"
}
cap_ok_root=$tmp_dir/cap-ok
make_chain "$cap_ok_root" 39
assert_portable_java_home "$cap_ok_root/jdk" "$cap_ok_root/path"
cap_fail_root=$tmp_dir/cap-fail
make_chain "$cap_fail_root" 40
assert_portable_java_home '' "$cap_fail_root/path"

missing_root=$tmp_dir/missing
mkdir -p "$missing_root/path"
ln -s '../does-not-exist/bin/javac' "$missing_root/path/javac"
assert_portable_java_home '' "$missing_root/path"

nonexec_root=$tmp_dir/non-executable
mkdir -p "$nonexec_root/path" "$nonexec_root/jdk/bin"
: >"$nonexec_root/jdk/bin/javac"
chmod 0644 "$nonexec_root/jdk/bin/javac"
ln -s '../jdk/bin/javac' "$nonexec_root/path/javac"
assert_portable_java_home '' "$nonexec_root/path"

expect_android_env_failure() {
    expected_message=$1
    shift
    log=$tmp_dir/failure.log
    if (
        unset JAVA_HOME ANDROID_HOME ANDROID_SDK_ROOT
        HOME=$fake_home PATH="$fake_path:$base_path" \
            "$make_bin" --no-print-directory -s -f "$repo_dir/Makefile" android-env "$@"
    ) >"$log" 2>&1; then
        printf 'android-env unexpectedly accepted explicit invalid override\n' >&2
        exit 1
    fi
    grep -F "$expected_message" "$log" >/dev/null || {
        printf 'android-env failed without expected diagnostic: %s\n' "$expected_message" >&2
        cat "$log" >&2
        exit 1
    }
}
expect_android_env_failure "JAVA_HOME has no executable bin/java: $explicit_java" JAVA_HOME="$explicit_java"
expect_android_env_failure "missing Android SDK platform: $invalid_home/platforms/android-35/android.jar" ANDROID_HOME="$invalid_home"
expect_android_env_failure "missing Android SDK platform: $invalid_root/platforms/android-35/android.jar" ANDROID_SDK_ROOT="$invalid_root"

printf 'Android Make environment bootstrap tests passed\n'
