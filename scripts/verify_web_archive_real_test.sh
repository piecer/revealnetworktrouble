#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_dir"

version=${1:-$(sed -n '1p' VERSION)}
revision=${2:-$(git rev-parse HEAD)}
source_date_epoch=${3:-$(git show -s --format=%ct "$revision")}
tmp=$(mktemp -d "${TMPDIR:-/tmp}/checknetwork-web-archive-real.XXXXXX")
inventory_started=false

inventory_images() {
    docker image ls --all --quiet --no-trunc | LC_ALL=C sort -u
}
inventory_tags() {
    docker image ls --all --no-trunc --digests --format '{{.Repository}}\t{{.Tag}}\t{{.Digest}}\t{{.ID}}' | LC_ALL=C sort
}
inventory_containers() {
    docker container ls --all --no-trunc --format '{{.ID}}\t{{.Image}}\t{{.Names}}' | LC_ALL=C sort
}
inventory_networks() {
    docker network ls --no-trunc --format '{{.ID}}\t{{.Name}}\t{{.Driver}}\t{{.Scope}}' | LC_ALL=C sort
}

compare_inventory() {
    inventory_images >"$tmp/images.after"
    inventory_tags >"$tmp/tags.after"
    inventory_containers >"$tmp/containers.after"
    inventory_networks >"$tmp/networks.after"
    inventory_ok=true
    if ! cmp -s "$tmp/images.before" "$tmp/images.after"; then
        printf '%s\n' 'daemon image inventory changed' >&2
        inventory_ok=false
    fi
    if ! cmp -s "$tmp/tags.before" "$tmp/tags.after"; then
        printf '%s\n' 'daemon tag inventory changed' >&2
        inventory_ok=false
    fi
    if ! cmp -s "$tmp/containers.before" "$tmp/containers.after"; then
        printf '%s\n' 'daemon container inventory changed' >&2
        inventory_ok=false
    fi
    if ! cmp -s "$tmp/networks.before" "$tmp/networks.after"; then
        printf '%s\n' 'daemon network inventory changed' >&2
        inventory_ok=false
    fi
    [ "$inventory_ok" = true ]
}

cleanup() {
    status=$?
    trap - EXIT HUP INT TERM
    if [ "$inventory_started" = true ] && ! compare_inventory; then
        status=1
    fi
    rm -rf -- "$tmp"
    exit "$status"
}
handle_signal() {
    status=$1
    trap '' HUP INT TERM
    exit "$status"
}
real_test_hook() {
    stage=$1
    if [ "${CHECKNETWORK_WEB_ARCHIVE_REAL_FAIL_AT:-}" = "$stage" ]; then
        printf 'injected Web archive failure after %s\n' "$stage" >&2
        exit 1
    fi
    if [ "${CHECKNETWORK_WEB_ARCHIVE_REAL_SIGNAL_AT:-}" = "$stage" ]; then
        kill -s "${CHECKNETWORK_WEB_ARCHIVE_REAL_SIGNAL:-TERM}" "$$"
    fi
}
trap cleanup EXIT
trap 'handle_signal 129' HUP
trap 'handle_signal 130' INT
trap 'handle_signal 143' TERM

command -v docker >/dev/null 2>&1
docker info >/dev/null
command -v python3 >/dev/null 2>&1

inventory_images >"$tmp/images.before"
inventory_tags >"$tmp/tags.before"
inventory_containers >"$tmp/containers.before"
inventory_networks >"$tmp/networks.before"
inventory_started=true

base_context=$tmp/web-base-context
mkdir -m 0700 "$base_context"
chmod 0700 "$base_context"
printf '%s\n' 'FROM nginx@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10' >"$base_context/Dockerfile"
chmod 0600 "$base_context/Dockerfile"
base_archive_one=$tmp/nginx-base-one.tar
base_archive_two=$tmp/nginx-base-two.tar
archive_one=$tmp/web-one.tar
archive_two=$tmp/web-two.tar
result_one=$tmp/web-one.json
result_two=$tmp/web-two.json

# The immutable base export is revision-independent. Epoch zero is the canonical
# lower bound: it leaves newer pinned base layer/config timestamps unchanged and
# fixes Buildx's outer descriptor/tar timestamps across invocations.
docker buildx build --platform linux/amd64 --no-cache --provenance=false \
    --build-arg SOURCE_DATE_EPOCH=0 \
    --output=type=docker,dest="$base_archive_one",rewrite-timestamp=true \
    "$base_context"
real_test_hook base_archive_1
docker buildx build --platform linux/amd64 --no-cache --provenance=false \
    --build-arg SOURCE_DATE_EPOCH=0 \
    --output=type=docker,dest="$base_archive_two",rewrite-timestamp=true \
    "$base_context"
cmp -s "$base_archive_one" "$base_archive_two" || {
    printf '%s\n' 'Web pinned-base archive byte reproducibility mismatch' >&2
    exit 1
}

docker buildx build --platform linux/amd64 --no-cache --provenance=false \
    --build-arg "VERSION=$version" \
    --build-arg "REVISION=$revision" \
    --build-arg "SOURCE_DATE_EPOCH=$source_date_epoch" \
    --output=type=docker,dest="$archive_one",rewrite-timestamp=true \
    frontend
python3 scripts/verify_web_archive.py --archive "$archive_one" --base-archive "$base_archive_one" \
    --source frontend --version "$version" --revision "$revision" >"$result_one"

docker buildx build --platform linux/amd64 --no-cache --provenance=false \
    --build-arg "VERSION=$version" \
    --build-arg "REVISION=$revision" \
    --build-arg "SOURCE_DATE_EPOCH=$source_date_epoch" \
    --output=type=docker,dest="$archive_two",rewrite-timestamp=true \
    frontend
python3 scripts/verify_web_archive.py --archive "$archive_two" --base-archive "$base_archive_two" \
    --source frontend --version "$version" --revision "$revision" >"$result_two"

cmp -s "$archive_one" "$archive_two" || {
    printf '%s\n' 'Web derived archive byte reproducibility mismatch' >&2
    exit 1
}
cmp -s "$result_one" "$result_two" || {
    printf '%s\n' 'Web validator result reproducibility mismatch' >&2
    exit 1
}

base_sha=$(sha256sum "$base_archive_one" | cut -d ' ' -f 1)
derived_sha=$(sha256sum "$archive_one" | cut -d ' ' -f 1)
python3 - "$base_sha" "$derived_sha" "$result_one" <<'PY'
import json
import sys
result = json.load(open(sys.argv[3], encoding="utf-8"))
print(json.dumps({"base_archive": sys.argv[1], "derived_archive": sys.argv[2], "validation": result}, sort_keys=True, separators=(",", ":")))
PY
