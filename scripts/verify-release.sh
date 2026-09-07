#!/bin/sh
set -eu

# Keep archives, validation output, and other intermediates private regardless
# of the caller. Canonical source extraction uses its own build-facing umask.
umask 077

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_dir"
release_output=${CHECKNETWORK_RELEASE_OUTPUT:-}
case "$release_output" in
    '') ;;
    /*) ;;
    *) release_output=$repo_dir/$release_output ;;
esac

fail() {
    printf '%s\n' "$1" >&2
    exit 1
}

[ "$#" -ge 1 ] && [ "$#" -le 2 ] || {
    printf 'usage: %s EXACT_HEAD_SHA [GIT_ARCHIVE_TAR]\n' "$0" >&2
    exit 2
}
revision=$1
archive_tar=${2:-}
case "$revision" in
    *[!0-9a-f]*) printf 'EXACT_HEAD_SHA must be 40 lowercase hexadecimal characters\n' >&2; exit 2 ;;
esac
[ "${#revision}" -eq 40 ] || {
    printf 'EXACT_HEAD_SHA must be 40 lowercase hexadecimal characters\n' >&2
    exit 2
}

resolved_revision=$(git rev-parse --verify "$revision^{commit}" 2>/dev/null) || fail 'requested SHA did not resolve exactly'
[ "$resolved_revision" = "$revision" ] || fail 'requested SHA did not resolve exactly'
[ "$(git rev-parse HEAD)" = "$revision" ] || fail 'release accepts only the current exact HEAD SHA'
dirty=$(git status --porcelain --untracked-files=all)
[ "$dirty" = "" ] || fail "working tree is not clean; refusing release:\n$dirty"
git ls-files --error-unmatch -- VERSION >/dev/null 2>&1 || fail 'VERSION must be tracked at the requested revision'
[ -f VERSION ] || fail 'VERSION is missing'
git show "$revision:VERSION" | cmp -s - VERSION || fail 'VERSION bytes differ from the requested revision'
source_date_epoch=$(git show -s --format=%ct "$revision")
case "$source_date_epoch" in
    ''|*[!0-9]*) fail 'SOURCE_DATE_EPOCH must be a non-empty decimal commit timestamp' ;;
esac

archive_tmp=$(mktemp -d "${TMPDIR:-/tmp}/checknetwork-release-source.XXXXXX")
canonical_archive=$archive_tmp/source.tar
archive_source=$archive_tmp/source
cleanup_archive_source() {
    rm -rf -- "$archive_tmp"
}
trap cleanup_archive_source EXIT HUP INT TERM
mkdir -m 0700 -- "$archive_source"
chmod 0700 "$archive_tmp" "$archive_source"
git archive "$revision" > "$canonical_archive"
chmod 0600 "$canonical_archive"
if [ "$archive_tar" != "" ]; then
    [ -f "$archive_tar" ] && [ ! -L "$archive_tar" ] || fail 'GIT_ARCHIVE_TAR must be a regular non-symlink file'
    archive_revision=$(git get-tar-commit-id < "$archive_tar" 2>/dev/null) || fail 'archive has no valid Git commit ID'
    [ "$archive_revision" = "$revision" ] || fail 'archive Git commit ID does not match requested revision'
    cmp -s "$archive_tar" "$canonical_archive" || fail 'archive differs from canonical git archive'
fi
(umask 022; tar -xf "$canonical_archive" -C "$archive_source")
chmod 0700 "$archive_source"
source_dir=$archive_source
[ -f "$source_dir/VERSION" ] || fail 'VERSION is missing from canonical archive'
git -C "$repo_dir" show "$revision:VERSION" | cmp -s - "$source_dir/VERSION" || fail 'canonical archive VERSION bytes differ from the requested revision'
cd "$source_dir"

[ "$(wc -l < VERSION | tr -d ' ')" -eq 1 ] || fail 'VERSION must contain exactly one line'
version=$(sed -n '1p' VERSION)
semver='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-((0|[1-9][0-9]*)|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)(\.((0|[1-9][0-9]*)|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'
printf '%s\n' "$version" | grep -Eq "$semver" || fail 'VERSION must be valid SemVer without a v prefix'

command -v docker >/dev/null 2>&1 || fail 'Docker is required for release verification'
docker info >/dev/null 2>&1 || fail 'Docker is required and its daemon must be available for release verification'
command -v sha256sum >/dev/null 2>&1 || fail 'sha256sum is required for release verification'
command -v stat >/dev/null 2>&1 || fail 'stat is required for canonical source mode verification'

api_nonce=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')
case "$api_nonce" in ''|*[!0-9a-f]*) fail 'could not generate high-entropy API resource nonce' ;; esac
[ "${#api_nonce}" -eq 32 ] || fail 'could not generate high-entropy API resource nonce'
api_owner_token=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')
case "$api_owner_token" in ''|*[!0-9a-f]*) fail 'could not generate independent high-entropy API owner token' ;; esac
[ "${#api_owner_token}" -eq 32 ] || fail 'could not generate independent high-entropy API owner token'
[ "$api_owner_token" != "$api_nonce" ] || fail 'API owner token must be independent from the resource nonce'
release_id=checknetwork-release-$api_nonce
tmp=$(mktemp -d "${TMPDIR:-/tmp}/${release_id}.XXXXXX")
api_label_key=com.checknetwork.release.nonce
api_owner_label_key=com.checknetwork.release.owner
api_network_name=checknetwork-api-${api_nonce}-network
api_network_id=
api_network_state=inactive
api_network_preflight_absent=false
api_container_name=
api_container_image=
api_container_role=
api_container_id=
api_container_state=inactive
api_container_preflight_absent=false
api_base=alpine@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc
api_archive_one=$tmp/api-one.tar
api_archive_two=$tmp/api-two.tar
api_extract_one=$tmp/api-extract-one
api_extract_two=$tmp/api-extract-two
api_validation_one=$tmp/api-one.json
api_validation_two=$tmp/api-two.json
api_binary=$api_extract_one/checknetwork-api
api_traceroute=$api_extract_one/traceroute
container_name=checknetwork-api-${api_nonce}-smoke
network_name=$api_network_name
publish_tmp=
publish_backup=
output_existed=false
# Publication state transitions are:
# inactive -> preparing -> prepared -> publish_pending -> published -> committed
# Any unsuccessful active state transitions to rolled_back, or rollback_failed
# when caller data cannot be restored without touching an unsafe path.
publication_state=inactive
cleanup_failure_reported=false

# API and Web archives are never loaded into the daemon. Runtime resources use
# independent high-entropy ownership transactions and pinned borrowed bases.
web_enabled=false
web_nonce=
web_owner_token=
web_label_key=com.checknetwork.release.nonce
web_owner_label_key=com.checknetwork.release.owner
web_network_name=
web_container_name=
web_network_id=
web_container_id=
web_network_state=inactive
web_container_state=inactive
web_network_preflight_absent=false
web_container_preflight_absent=false
web_base=nginx@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10

[ -f "$source_dir/scripts/release-resource-ownership.sh" ] || fail 'release resource ownership helper is missing from canonical source'
# shellcheck source=release-resource-ownership.sh
. "$source_dir/scripts/release-resource-ownership.sh"

release_hook() {
    hook_stage=$1
    if [ "${CHECKNETWORK_RELEASE_FAIL_AT:-}" = "$hook_stage" ]; then
        fail "injected release failure after $hook_stage"
    fi
    if [ "${CHECKNETWORK_RELEASE_SIGNAL_AT:-}" = "$hook_stage" ]; then
        hook_signal=${CHECKNETWORK_RELEASE_SIGNAL:-TERM}
        kill -s "$hook_signal" "$$"
    fi
}

remove_nondirectory() {
    remove_path=$1
    if [ -d "$remove_path" ] && [ ! -L "$remove_path" ]; then
        return 1
    fi
    rm -f -- "$remove_path"
}

rollback_publication() {
    case "$publication_state" in
        inactive|rolled_back|committed)
            return 0
            ;;
        rollback_failed)
            return 1
            ;;
        preparing|prepared)
            if [ "$publish_tmp" != "" ]; then
                remove_nondirectory "$publish_tmp" || {
                    publication_state=rollback_failed
                    return 1
                }
                publish_tmp=
            fi
            if [ "$publish_backup" != "" ]; then
                remove_nondirectory "$publish_backup" || {
                    publication_state=rollback_failed
                    return 1
                }
                publish_backup=
            fi
            publication_state=rolled_back
            return 0
            ;;
        publish_pending|published)
            if [ "$output_existed" = true ]; then
                if [ "$publish_backup" = "" ] || [ -d "$release_output" ] || [ -L "$release_output" ] ||
                    ! mv -f "$publish_backup" "$release_output"; then
                    publication_state=rollback_failed
                    return 1
                fi
                publish_backup=
            else
                if [ -e "$release_output" ] || [ -L "$release_output" ]; then
                    if [ -d "$release_output" ] || [ -L "$release_output" ] || [ ! -f "$release_output" ] ||
                        ! remove_nondirectory "$release_output"; then
                        publication_state=rollback_failed
                        return 1
                    fi
                fi
            fi
            if [ "$publish_tmp" != "" ] && { [ -e "$publish_tmp" ] || [ -L "$publish_tmp" ]; }; then
                remove_nondirectory "$publish_tmp" || {
                    publication_state=rollback_failed
                    return 1
                }
            fi
            publish_tmp=
            publication_state=rolled_back
            return 0
            ;;
        *)
            publication_state=rollback_failed
            return 1
            ;;
    esac
}

report_rollback_failure() {
    if [ "$output_existed" = true ]; then
        printf '%s\n' 'release interrupted; existing output could not be restored safely' >&2
    else
        printf '%s\n' 'release interrupted; new output could not be removed safely' >&2
    fi
}

report_cleanup_failure() {
    if [ "$cleanup_failure_reported" = false ]; then
        printf '%s\n' 'release cleanup failed' >&2
        cleanup_failure_reported=true
    fi
}

cleanup_owned_resources() {
    cleanup_owned_failed=false
    if command -v docker >/dev/null 2>&1; then
        if ! cleanup_web_container; then cleanup_owned_failed=true; fi
        if ! cleanup_web_network; then cleanup_owned_failed=true; fi
        if ! cleanup_api_container; then cleanup_owned_failed=true; fi
        if ! cleanup_api_network; then cleanup_owned_failed=true; fi
    fi
    [ "$cleanup_owned_failed" = false ]
}

cleanup_resources() {
    cleanup_resources_failed=false
    if ! cleanup_owned_resources; then cleanup_resources_failed=true; fi
    [ "$publish_tmp" = "" ] || remove_nondirectory "$publish_tmp" || :
    # rollback_failed intentionally retains the only recovery copy. Every other
    # state has either preserved the destination or completed the transaction.
    if [ "$publication_state" != rollback_failed ] && [ "$publish_backup" != "" ]; then
        remove_nondirectory "$publish_backup" || :
        publish_backup=
    fi
    rm -rf -- "$tmp"
    cleanup_archive_source
    [ "$cleanup_resources_failed" = false ]
}

cleanup() {
    cleanup_status=$?
    trap - EXIT HUP INT TERM
    if [ "$cleanup_status" -ne 0 ]; then
        rollback_publication || report_rollback_failure
    elif [ "$publication_state" != inactive ] && [ "$publication_state" != committed ] && [ "$publication_state" != rolled_back ]; then
        if ! rollback_publication; then
            report_rollback_failure
        fi
        cleanup_status=1
    fi
    if ! cleanup_resources; then
        report_cleanup_failure
        [ "$cleanup_status" -ne 0 ] || cleanup_status=1
    fi
    exit "$cleanup_status"
}

handle_signal() {
    signal_status=$1
    # Ignore further asynchronous signals while rollback runs. EXIT remains
    # installed so the ordinary cleanup path exercises idempotent rollback.
    trap '' HUP INT TERM
    rollback_publication || report_rollback_failure
    exit "$signal_status"
}

trap cleanup EXIT
trap 'handle_signal 129' HUP
trap 'handle_signal 130' INT
trap 'handle_signal 143' TERM

original_tree_is_valid() {
    current_head=$(git -C "$repo_dir" rev-parse HEAD 2>/dev/null) || return 1
    [ "$current_head" = "$revision" ] || return 1
    current_dirty=$(git -C "$repo_dir" status --porcelain --untracked-files=all 2>/dev/null) || return 1
    [ "$current_dirty" = "" ] || return 1
}

revalidate_original_tree() {
    original_tree_is_valid || fail 'working tree changed during release verification'
}

command -v python3 >/dev/null 2>&1 || fail 'python3 is required for offline API archive validation'
[ -f "$source_dir/scripts/verify_api_archive.py" ] || fail 'offline API archive validator is missing from canonical source'
mkdir -m 0700 -- "$api_extract_one" "$api_extract_two"
for pass in 1 2; do
    if [ "$pass" -eq 1 ]; then
        api_archive=$api_archive_one
        api_extract=$api_extract_one
        api_validation=$api_validation_one
    else
        api_archive=$api_archive_two
        api_extract=$api_extract_two
        api_validation=$api_validation_two
    fi
    docker buildx build --no-cache --provenance=false \
        --build-arg "VERSION=$version" \
        --build-arg "REVISION=$revision" \
        --build-arg "SOURCE_DATE_EPOCH=$source_date_epoch" \
        --output=type=docker,dest="$api_archive",rewrite-timestamp=true .
    release_hook "api_archive_build_$pass"
    if [ "$pass" -eq 1 ]; then
        python3 scripts/verify_api_archive.py --archive "$api_archive" \
            --version "$version" --revision "$revision" \
            --extract-dir "$api_extract" >"$api_validation"
    else
        python3 scripts/verify_api_archive.py --archive "$api_archive" \
            --version "$version" --revision "$revision" \
            --binary-sha256 "$api_binary_sha" --extract-dir "$api_extract" >"$api_validation"
    fi
    release_hook "api_archive_validation_$pass"
    if [ "$pass" -eq 1 ]; then
        api_binary_sha=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1], encoding="utf-8"))["binary"])' "$api_validation")
    fi
done
cmp -s "$api_archive_one" "$api_archive_two" || fail 'API archive byte reproducibility mismatch'
python3 - "$api_validation_one" "$api_validation_two" <<'PY'
import json
import sys
one = json.load(open(sys.argv[1], encoding="utf-8"))
two = json.load(open(sys.argv[2], encoding="utf-8"))
expected = {"schema", "archive", "config", "manifest", "rootfs", "layers", "binary", "traceroute", "go_version", "go_path"}
if set(one) != expected or set(two) != expected:
    raise SystemExit("API archive validator output schema mismatch")
for field in ("schema", "archive", "config", "manifest", "rootfs", "layers", "binary", "traceroute", "go_version", "go_path"):
    if one[field] != two[field]:
        raise SystemExit("API archive reproducibility mismatch for " + field)
PY
cmp -s "$api_extract_one/checknetwork-api" "$api_extract_two/checknetwork-api" || fail 'API binary bytes differ between no-cache archives'
cmp -s "$api_extract_one/traceroute" "$api_extract_two/traceroute" || fail 'API traceroute bytes differ between no-cache archives'
api_archive_sha=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1], encoding="utf-8"))["archive"])' "$api_validation_one")
api_config_sha=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1], encoding="utf-8"))["config"])' "$api_validation_one")
api_manifest_sha=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1], encoding="utf-8"))["manifest"])' "$api_validation_one")
api_rootfs_sha=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1], encoding="utf-8"))["rootfs"])' "$api_validation_one")
api_traceroute_sha=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1], encoding="utf-8"))["traceroute"])' "$api_validation_one")

api_base_id=$(docker image inspect "$api_base" --format '{{.Id}}' 2>/dev/null) || fail 'pinned Alpine base must already be present for borrowed-base API smoke'
[ "$api_base_id" = 'sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc' ] || fail 'borrowed Alpine base identity mismatch'

create_owned_api_network
api_container_role=smoke
api_container_name=$container_name
api_container_id=
api_container_state=inactive
api_container_preflight_absent=false
create_owned_api_container
api_smoke_container_id=$api_container_id
# This executes validated pass-one bytes through read-only mounts on the exact
# pinned Alpine base. It only proves pinned-base mount compatibility; it does not execute the derived API archive.
docker container start "$api_smoke_container_id" >/dev/null
release_hook api_container_start
docker exec "$api_smoke_container_id" test -x /traceroute
traceroute_output=$(docker exec "$api_smoke_container_id" traceroute -n -m 1 -w 1 127.0.0.1) || fail 'functional traceroute smoke failed'
printf '%s\n' "$traceroute_output" | grep -F '127.0.0.1' >/dev/null || fail 'functional traceroute smoke did not reach loopback'
release_hook api_traceroute_smoke
attempt=0
ready=
while [ "$attempt" -lt 30 ]; do
    ready=$(docker exec "$api_smoke_container_id" wget -qO- http://127.0.0.1:8080/readyz 2>/dev/null || :)
    [ "$ready" = '{"status":"ready"}' ] && break
    attempt=$((attempt + 1))
    sleep 1
done
[ "$ready" = '{"status":"ready"}' ] || fail "readiness smoke failed: $ready"
release_hook api_ready_smoke
live=$(docker exec "$api_smoke_container_id" wget -qO- http://127.0.0.1:8080/livez)
[ "$live" = '{"status":"live"}' ] || fail "liveness smoke failed: $live"
health=$(docker exec "$api_smoke_container_id" wget -qO- http://127.0.0.1:8080/api/v1/health)
expected_health=$(printf '{"status":"ok","version":"%s","revision":"%s"}' "$version" "$revision")
[ "$health" = "$expected_health" ] || fail "health identity mismatch: $health"
logs=$(docker logs "$api_smoke_container_id" 2>&1)
printf '%s\n' "$logs" | grep -F '"msg":"server started"' >/dev/null || fail 'startup log is missing'
printf '%s\n' "$logs" | grep -F "\"version\":\"$version\"" >/dev/null || fail 'startup version mismatch'
printf '%s\n' "$logs" | grep -F "\"revision\":\"$revision\"" >/dev/null || fail 'startup revision mismatch'
printf '%s\n' "$logs" | grep -Eq '"build_(path|time)"' && fail 'startup log leaked raw build path/time'
release_hook api_identity_smoke

# A complete canonical source archive enables Web release verification. Minimal
# API-only contract fixtures intentionally omit frontend/ and keep exercising the
# pre-existing API transaction in isolation.
if [ -f "$source_dir/frontend/Dockerfile" ]; then
    web_enabled=true
    command -v python3 >/dev/null 2>&1 || fail 'python3 is required for offline Web archive validation'
    [ -f "$source_dir/scripts/verify_web_archive.py" ] || fail 'offline Web archive validator is missing from canonical source'
    [ -f "$source_dir/scripts/release-resource-ownership.sh" ] || fail 'Web release resource ownership helper is missing from canonical source'
    # shellcheck source=release-resource-ownership.sh
    . "$source_dir/scripts/release-resource-ownership.sh"
    for asset in nginx.conf app.js index.html state.js styles.css topology-model.js topology-renderer.js topology-visualizer.js; do
        [ -f "$source_dir/frontend/$asset" ] && [ ! -L "$source_dir/frontend/$asset" ] || fail "canonical Web input is missing or not regular: $asset"
    done
    [ "$(stat -c %a "$source_dir/frontend/nginx.conf")" = 644 ] || fail 'canonical Web nginx.conf mode is not 0644'
    for asset in app.js index.html state.js styles.css topology-model.js topology-renderer.js topology-visualizer.js; do
        [ "$(stat -c %a "$source_dir/frontend/$asset")" = 644 ] || fail "canonical Web asset mode is not 0644: $asset"
    done
    web_nonce=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')
    case "$web_nonce" in ''|*[!0-9a-f]*) fail 'could not generate high-entropy Web resource nonce' ;; esac
    [ "${#web_nonce}" -eq 32 ] || fail 'could not generate high-entropy Web resource nonce'
    web_owner_token=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')
    case "$web_owner_token" in ''|*[!0-9a-f]*) fail 'could not generate independent high-entropy Web owner token' ;; esac
    [ "${#web_owner_token}" -eq 32 ] || fail 'could not generate independent high-entropy Web owner token'
    [ "$web_owner_token" != "$web_nonce" ] || fail 'Web owner token must be independent from the resource nonce'
    web_network_name=checknetwork-web-${web_nonce}-network
    web_container_name=checknetwork-web-${web_nonce}-smoke
    web_archive_one=$tmp/web-one.tar
    web_archive_two=$tmp/web-two.tar
    web_base_archive_one=$tmp/nginx-base-one.tar
    web_base_archive_two=$tmp/nginx-base-two.tar
    web_base_context=$tmp/web-base-context
    validation_one=$tmp/web-one.json
    validation_two=$tmp/web-two.json

    base_id=$(docker image inspect "$web_base" --format '{{.Id}}' 2>/dev/null) || fail 'pinned nginx base must already be present for borrowed-base smoke'
    [ "$base_id" = 'sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10' ] || fail 'borrowed nginx base identity mismatch'
    mkdir -m 0700 "$web_base_context"
    chmod 0700 "$web_base_context"
    printf '%s\n' 'FROM nginx@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10' > "$web_base_context/Dockerfile"
    chmod 0600 "$web_base_context/Dockerfile"
    # The immutable base is revision-independent. Epoch zero leaves its newer
    # layer/config timestamps intact while fixing exporter metadata.
    docker buildx build --platform linux/amd64 --no-cache --provenance=false \
        --build-arg SOURCE_DATE_EPOCH=0 \
        --output=type=docker,dest="$web_base_archive_one",rewrite-timestamp=true \
        "$web_base_context"
    release_hook web_base_archive_build_1
    docker buildx build --platform linux/amd64 --no-cache --provenance=false \
        --build-arg SOURCE_DATE_EPOCH=0 \
        --output=type=docker,dest="$web_base_archive_two",rewrite-timestamp=true \
        "$web_base_context"
    release_hook web_base_archive_build_2
    cmp -s "$web_base_archive_one" "$web_base_archive_two" || fail 'Web pinned-base archive byte reproducibility mismatch'

    for pass in 1 2; do
        if [ "$pass" -eq 1 ]; then web_archive=$web_archive_one; web_base_archive=$web_base_archive_one; else web_archive=$web_archive_two; web_base_archive=$web_base_archive_two; fi
        docker buildx build --platform linux/amd64 --no-cache --provenance=false \
            --build-arg "VERSION=$version" \
            --build-arg "REVISION=$revision" \
            --build-arg "SOURCE_DATE_EPOCH=$source_date_epoch" \
            --output=type=docker,dest="$web_archive",rewrite-timestamp=true \
            frontend
        release_hook "web_archive_build_$pass"
    done
    cmp -s "$web_archive_one" "$web_archive_two" || fail 'Web archive byte reproducibility mismatch'

    python3 scripts/verify_web_archive.py --archive "$web_archive_one" --base-archive "$web_base_archive" \
        --source frontend --version "$version" --revision "$revision" > "$validation_one"
    release_hook web_archive_validation_1
    python3 scripts/verify_web_archive.py --archive "$web_archive_two" --base-archive "$web_base_archive" \
        --source frontend --version "$version" --revision "$revision" > "$validation_two"
    release_hook web_archive_validation_2
    cmp -s "$validation_one" "$validation_two" || fail 'Web config/manifest/rootfs/asset reproducibility mismatch'
    web_archive_sha=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))[sys.argv[2]])' "$validation_one" archive)
    web_config_sha=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))[sys.argv[2]])' "$validation_one" config)
    web_manifest_sha=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))[sys.argv[2]])' "$validation_one" manifest)
    web_rootfs_sha=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))[sys.argv[2]])' "$validation_one" rootfs)
    web_asset_sha=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))[sys.argv[2]])' "$validation_one" asset_manifest)

    create_owned_web_network
    create_owned_web_container

    docker container start "$web_container_id" >/dev/null
    release_hook web_container_start
    attempt=0
    web_healthy=false
    while [ "$attempt" -lt 30 ]; do
        if docker exec "$web_container_id" wget --no-verbose --tries=1 --spider http://127.0.0.1/ >/dev/null 2>&1; then
            web_healthy=true
            break
        fi
        attempt=$((attempt + 1))
        sleep 1
    done
    [ "$web_healthy" = true ] || fail 'borrowed-base Web health smoke failed'
    release_hook web_health
    docker exec "$web_container_id" sh -c 'for f in app.js index.html state.js styles.css topology-model.js topology-renderer.js topology-visualizer.js; do test -f "/usr/share/nginx/html/$f" || exit 1; done'
    release_hook web_enumerate
    for asset in app.js index.html state.js styles.css topology-model.js topology-renderer.js topology-visualizer.js; do
        docker exec "$web_container_id" wget -qO- "http://127.0.0.1/$asset" > "$tmp/fetched-$asset"
        cmp -s "$tmp/fetched-$asset" "$source_dir/frontend/$asset" || fail "borrowed-base Web byte smoke mismatch: $asset"
        release_hook "web_fetch_$asset"
    done
fi

if [ "$release_output" != "" ]; then
    output_dir=$(dirname -- "$release_output")
    mkdir -p -- "$output_dir"
    [ ! -d "$release_output" ] || fail 'CHECKNETWORK_RELEASE_OUTPUT must not name a directory'
    [ ! -L "$release_output" ] || fail 'CHECKNETWORK_RELEASE_OUTPUT must not name a symlink'
    publication_state=preparing
    if [ -e "$release_output" ]; then
        [ -f "$release_output" ] || fail 'CHECKNETWORK_RELEASE_OUTPUT must name a regular file or an absent path'
        output_existed=true
        publish_backup=$(mktemp "$output_dir/.checknetwork-release-backup.XXXXXX")
        cp -p "$release_output" "$publish_backup"
    fi
    publish_tmp=$(mktemp "$output_dir/.checknetwork-release-output.XXXXXX")
    publication_state=prepared
    # Keep the canonical extracted artifact immutable: only the destination-local
    # staging file is copied and chmodded.  cp/chmod are fixed POSIX operations;
    # POSIX has no per-file fsync utility, so use sync when one is available.
    cp "$api_extract_one/checknetwork-api" "$publish_tmp"
    chmod 0755 "$publish_tmp"
    if command -v sync >/dev/null 2>&1; then
        sync
    fi
    # This is deliberately the last operation before the atomic rename. State
    # changes first because a signal may arrive after rename but before mv exits.
    revalidate_original_tree
    publication_state=publish_pending
    mv -f "$publish_tmp" "$release_output"
    publish_tmp=
    publication_state=published
    if ! original_tree_is_valid; then
        if ! rollback_publication; then
            if [ "$output_existed" = true ]; then
                fail 'working tree changed during release verification; existing output could not be restored safely'
            fi
            fail 'working tree changed during release verification; new output could not be removed safely'
        fi
        fail 'working tree changed during release verification'
    fi
    if ! cleanup_owned_resources; then
        report_cleanup_failure
        rollback_publication || report_rollback_failure
        exit 1
    fi
    publication_state=committed
    if [ "$publish_backup" != "" ]; then
        remove_nondirectory "$publish_backup" || fail 'release output backup could not be removed safely'
        publish_backup=
    fi
else
    revalidate_original_tree
    if ! cleanup_owned_resources; then
        report_cleanup_failure
        exit 1
    fi
fi

# An uncooperative writer can always race after this final acceptance check.
# Mutations observable by the checks are rejected, and no writer can alter the
# canonical archive artifact because publication only reads it into staging.
printf 'release verified: api_archive=%s api_config=%s api_manifest=%s api_rootfs=%s api_binary=%s api_traceroute=%s\n' \
    "$api_archive_sha" "$api_config_sha" "$api_manifest_sha" "$api_rootfs_sha" "$api_binary_sha" "$api_traceroute_sha"
if [ "$web_enabled" = true ]; then
    printf 'Web verified offline: archive=%s config=%s manifest=%s rootfs=%s asset_manifest=%s\n' \
        "$web_archive_sha" "$web_config_sha" "$web_manifest_sha" "$web_rootfs_sha" "$web_asset_sha"
fi
