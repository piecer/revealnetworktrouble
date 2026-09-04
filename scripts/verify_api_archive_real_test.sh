#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_dir"

version=${1:-$(sed -n '1p' VERSION)}
revision=${2:-$(git rev-parse HEAD)}
source_date_epoch=${3:-$(git show -s --format=%ct "$revision")}
tmp=$(mktemp -d "${TMPDIR:-/tmp}/checknetwork-api-archive-real.XXXXXX")
trap 'rm -rf -- "$tmp"' EXIT HUP INT TERM
archive_one=$tmp/api-one.tar
archive_two=$tmp/api-two.tar
extract_one=$tmp/extract-one
extract_two=$tmp/extract-two
mkdir -m 0700 "$extract_one" "$extract_two"

inventory_images() {
    docker image ls --all --quiet --no-trunc | LC_ALL=C sort -u
}
inventory_tags() {
    docker image ls --all --no-trunc --digests --format '{{.Repository}}\t{{.Tag}}\t{{.Digest}}\t{{.ID}}' | LC_ALL=C sort
}

inventory_images >"$tmp/images.before"
inventory_tags >"$tmp/tags.before"

docker buildx build --platform linux/amd64 --no-cache --provenance=false \
    --build-arg "VERSION=$version" \
    --build-arg "REVISION=$revision" \
    --build-arg "SOURCE_DATE_EPOCH=$source_date_epoch" \
    --output=type=docker,dest="$archive_one",rewrite-timestamp=true .

python3 scripts/verify_api_archive.py \
    --archive "$archive_one" --version "$version" --revision "$revision" \
    --extract-dir "$extract_one" >"$tmp/result-one.json"
binary_sha=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1], encoding="utf-8"))["binary"])' "$tmp/result-one.json")

docker buildx build --platform linux/amd64 --no-cache --provenance=false \
    --build-arg "VERSION=$version" \
    --build-arg "REVISION=$revision" \
    --build-arg "SOURCE_DATE_EPOCH=$source_date_epoch" \
    --output=type=docker,dest="$archive_two",rewrite-timestamp=true .

python3 scripts/verify_api_archive.py \
    --archive "$archive_two" --version "$version" --revision "$revision" \
    --binary-sha256 "$binary_sha" --extract-dir "$extract_two" >"$tmp/result-two.json"

python3 -c '
import json, sys
one = json.load(open(sys.argv[1], encoding="utf-8"))
two = json.load(open(sys.argv[2], encoding="utf-8"))
fields = ("schema", "config", "manifest", "rootfs", "layers", "binary", "traceroute", "go_version", "go_path")
for field in fields:
    if one[field] != two[field]:
        raise SystemExit("API archive reproducibility mismatch for " + field)
print(json.dumps({"first": one, "second": two}, sort_keys=True, separators=(",", ":")))
' "$tmp/result-one.json" "$tmp/result-two.json" >"$tmp/results.json"

cmp -s "$extract_one/checknetwork-api" "$extract_two/checknetwork-api" || {
    printf '%s\n' 'API binary bytes differ between no-cache archives' >&2
    exit 1
}
cmp -s "$extract_one/traceroute" "$extract_two/traceroute" || {
    printf '%s\n' 'traceroute bytes differ between no-cache archives' >&2
    exit 1
}

inventory_images >"$tmp/images.after"
inventory_tags >"$tmp/tags.after"
cmp -s "$tmp/images.before" "$tmp/images.after" || {
    printf '%s\n' 'daemon image inventory changed' >&2
    exit 1
}
cmp -s "$tmp/tags.before" "$tmp/tags.after" || {
    printf '%s\n' 'daemon tag inventory changed' >&2
    exit 1
}

python3 -c 'import pathlib,sys; sys.stdout.buffer.write(pathlib.Path(sys.argv[1]).read_bytes())' "$tmp/results.json"
