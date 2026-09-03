#!/bin/sh
set -eu

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
mkdir -p -- "$archive_source"
git archive "$revision" > "$canonical_archive"
if [ "$archive_tar" != "" ]; then
    [ -f "$archive_tar" ] && [ ! -L "$archive_tar" ] || fail 'GIT_ARCHIVE_TAR must be a regular non-symlink file'
    archive_revision=$(git get-tar-commit-id < "$archive_tar" 2>/dev/null) || fail 'archive has no valid Git commit ID'
    [ "$archive_revision" = "$revision" ] || fail 'archive Git commit ID does not match requested revision'
    cmp -s "$archive_tar" "$canonical_archive" || fail 'archive differs from canonical git archive'
fi
tar -xf "$canonical_archive" -C "$archive_source"
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

release_id=$(printf 'checknetwork-release-%.12s-%s' "$revision" "$$")
tmp=$(mktemp -d "${TMPDIR:-/tmp}/${release_id}.XXXXXX")
container_name=${release_id}-smoke
extract_one=${release_id}-extract-one
extract_two=${release_id}-extract-two
network_name=${release_id}-network
image_one=${release_id}:one
image_two=${release_id}:two
publish_tmp=
publish_backup=
output_existed=false
# Publication state transitions are:
# inactive -> preparing -> prepared -> publish_pending -> published -> committed
# Any unsuccessful active state transitions to rolled_back, or rollback_failed
# when caller data cannot be restored without touching an unsafe path.
publication_state=inactive

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

cleanup_resources() {
    if command -v docker >/dev/null 2>&1; then
        docker container rm -f -- "$container_name" "$extract_one" "$extract_two" >/dev/null 2>&1 || :
        docker image rm -f -- "$image_one" "$image_two" >/dev/null 2>&1 || :
        docker network rm -- "$network_name" >/dev/null 2>&1 || :
    fi
    [ "$publish_tmp" = "" ] || remove_nondirectory "$publish_tmp" || :
    # rollback_failed intentionally retains the only recovery copy. Every other
    # state has either preserved the destination or completed the transaction.
    if [ "$publication_state" != rollback_failed ] && [ "$publish_backup" != "" ]; then
        remove_nondirectory "$publish_backup" || :
        publish_backup=
    fi
    rm -rf -- "$tmp"
    cleanup_archive_source
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
    cleanup_resources
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

for pass in 1 2; do
    if [ "$pass" -eq 1 ]; then image=$image_one; else image=$image_two; fi
    DOCKER_BUILDKIT=1 docker build --no-cache --pull=false --provenance=false \
        --build-arg "VERSION=$version" \
        --build-arg "REVISION=$revision" \
        --build-arg "SOURCE_DATE_EPOCH=$source_date_epoch" \
        --tag "$image" .
done

docker create --name "$extract_one" "$image_one" >/dev/null
docker create --name "$extract_two" "$image_two" >/dev/null
docker cp "$extract_one:/checknetwork-api" "$tmp/checknetwork-api-image-1"
docker cp "$extract_two:/checknetwork-api" "$tmp/checknetwork-api-image-2"
binary_one=$(sha256sum "$tmp/checknetwork-api-image-1" | cut -d ' ' -f 1)
binary_two=$(sha256sum "$tmp/checknetwork-api-image-2" | cut -d ' ' -f 1)
[ "$binary_one" = "$binary_two" ] || fail "binary reproducibility mismatch: $binary_one != $binary_two"

image_id_one=$(docker image inspect "$image_one" --format '{{.Id}}')
image_id_two=$(docker image inspect "$image_two" --format '{{.Id}}')
[ "$image_id_one" = "$image_id_two" ] || fail "image/config digest mismatch: $image_id_one != $image_id_two"
rootfs_one=$(docker image inspect "$image_one" --format '{{json .RootFS.Layers}}')
rootfs_two=$(docker image inspect "$image_two" --format '{{json .RootFS.Layers}}')
[ "$rootfs_one" = "$rootfs_two" ] || fail 'RootFS.Layers reproducibility mismatch'

for image in "$image_one" "$image_two"; do
    label_version=$(docker image inspect "$image" --format '{{index .Config.Labels "org.opencontainers.image.version"}}')
    label_revision=$(docker image inspect "$image" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')
    [ "$label_version" = "$version" ] || fail "OCI version label mismatch for $image"
    [ "$label_revision" = "$revision" ] || fail "OCI revision label mismatch for $image"
done

docker network create "$network_name" >/dev/null
docker run -d --name "$container_name" --network "$network_name" -e CHECKNETWORK_ADDR=:8080 "$image_one" >/dev/null
docker exec "$container_name" test -x /traceroute
traceroute_output=$(docker exec "$container_name" traceroute -n -m 1 -w 1 127.0.0.1) || fail 'functional traceroute smoke failed'
printf '%s\n' "$traceroute_output" | grep -F '127.0.0.1' >/dev/null || fail 'functional traceroute smoke did not reach loopback'
attempt=0
ready=
while [ "$attempt" -lt 30 ]; do
    ready=$(docker exec "$container_name" wget -qO- http://127.0.0.1:8080/readyz 2>/dev/null || :)
    [ "$ready" = '{"status":"ready"}' ] && break
    attempt=$((attempt + 1))
    sleep 1
done
[ "$ready" = '{"status":"ready"}' ] || fail "readiness smoke failed: $ready"
live=$(docker exec "$container_name" wget -qO- http://127.0.0.1:8080/livez)
[ "$live" = '{"status":"live"}' ] || fail "liveness smoke failed: $live"
health=$(docker exec "$container_name" wget -qO- http://127.0.0.1:8080/api/v1/health)
expected_health=$(printf '{"status":"ok","version":"%s","revision":"%s"}' "$version" "$revision")
[ "$health" = "$expected_health" ] || fail "health identity mismatch: $health"
logs=$(docker logs "$container_name" 2>&1)
printf '%s\n' "$logs" | grep -F '"msg":"server started"' >/dev/null || fail 'startup log is missing'
printf '%s\n' "$logs" | grep -F "\"version\":\"$version\"" >/dev/null || fail 'startup version mismatch'
printf '%s\n' "$logs" | grep -F "\"revision\":\"$revision\"" >/dev/null || fail 'startup revision mismatch'
printf '%s\n' "$logs" | grep -Eq '"build_(path|time)"' && fail 'startup log leaked raw build path/time'

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
    cp "$tmp/checknetwork-api-image-1" "$publish_tmp"
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
    publication_state=committed
    if [ "$publish_backup" != "" ]; then
        remove_nondirectory "$publish_backup" || fail 'release output backup could not be removed safely'
        publish_backup=
    fi
else
    revalidate_original_tree
fi

# An uncooperative writer can always race after this final acceptance check.
# Mutations observable by the checks are rejected, and no writer can alter the
# canonical archive artifact because publication only reads it into staging.
printf 'release verified: version=%s revision=%s binary=%s image=%s\n' "$version" "$revision" "$binary_one" "$image_id_one"
