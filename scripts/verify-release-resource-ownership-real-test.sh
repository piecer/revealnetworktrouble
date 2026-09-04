#!/bin/sh
set -eu

real_docker=$(command -v docker)
"$real_docker" info >/dev/null
repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
root=$(mktemp -d "${TMPDIR:-/tmp}/checknetwork-release-ownership-real.XXXXXX")
nonce=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')
owner=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')
base=alpine@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc
base_id=$("$real_docker" image inspect "$base" --format '{{.Id}}')
owned_ids=$root/owned.ids
: >"$owned_ids"
printf '#!/bin/sh\nexit 0\n' >"$root/checknetwork-api"
printf '#!/bin/sh\nexit 0\n' >"$root/traceroute"
chmod 0755 "$root/checknetwork-api" "$root/traceroute"

inventory() {
    destination=$1
    {
        "$real_docker" image ls -a --no-trunc --digests --format 'image|{{.ID}}|{{.Repository}}|{{.Tag}}|{{.Digest}}'
        "$real_docker" container ls -a --no-trunc --format 'container|{{.ID}}|{{.Names}}|{{.Image}}'
        "$real_docker" network ls --no-trunc --format 'network|{{.ID}}|{{.Name}}|{{.Driver}}'
    } | LC_ALL=C sort >"$destination"
}

cleanup() {
    while IFS='|' read -r kind id; do
        [ "$id" != "" ] || continue
        case "$kind" in
            container) "$real_docker" container rm -f -- "$id" >/dev/null 2>&1 || : ;;
            network) "$real_docker" network rm -- "$id" >/dev/null 2>&1 || : ;;
        esac
    done <"$owned_ids"
    rm -rf -- "$root"
}
trap cleanup EXIT HUP INT TERM
inventory "$root/inventory.before"
mkdir -p "$root/bin"
cat >"$root/bin/docker" <<'WRAPPER'
#!/bin/sh
set -eu
case "${RESPONSE_LOSS_KIND:-}:$1:${2:-}" in
    network:network:create|container:container:create)
        "$REAL_DOCKER" "$@" >/dev/null
        printf 'error during connect: unexpected EOF\n' >&2
        exit 125
        ;;
esac
exec "$REAL_DOCKER" "$@"
WRAPPER
chmod +x "$root/bin/docker"

run_helper() {
    scenario=$1
    resource=$2
    resource_nonce=$nonce-$resource
    PATH=$root/bin:$PATH REAL_DOCKER=$real_docker \
    RESPONSE_LOSS_KIND=$(case "$scenario:$resource" in response-loss:network) printf network;; response-loss:container) printf container;; *) printf '';; esac) \
    TEST_ROOT=$root TEST_REPO=$repo_dir TEST_NONCE=$resource_nonce TEST_OWNER=$owner TEST_BASE=$base TEST_RESOURCE=$resource \
        sh -c '
            set -eu
            fail() { printf "%s\n" "$1" >&2; exit 1; }
            release_hook() { :; }
            tmp=$TEST_ROOT
            api_nonce=$TEST_NONCE
            api_owner_token=$TEST_OWNER
            api_label_key=com.checknetwork.release.nonce
            api_owner_label_key=com.checknetwork.release.owner
            api_network_name=checknetwork-real-${api_nonce}-network
            api_network_id=
            api_network_state=inactive
            api_network_preflight_absent=false
            api_container_role=smoke
            api_container_name=checknetwork-real-${api_nonce}-smoke
            api_container_id=
            api_container_state=inactive
            api_container_preflight_absent=false
            api_base=$TEST_BASE
            api_binary=$TEST_ROOT/checknetwork-api
            api_traceroute=$TEST_ROOT/traceroute
            . "$TEST_REPO/scripts/release-resource-ownership.sh"
            if [ "$TEST_RESOURCE" = network ]; then
                trap cleanup_api_network EXIT
                create_owned_api_network
                printf "SUCCESS id=%s\n" "$api_network_id"
            else
                trap cleanup_api_container EXIT
                api_network_id=bridge
                create_owned_api_container
                printf "SUCCESS id=%s\n" "$api_container_id"
            fi
        ' sh
}

resource_name() {
    resource_nonce=$nonce-$1
    case "$1" in network) printf 'checknetwork-real-%s-network' "$resource_nonce";; container) printf 'checknetwork-real-%s-smoke' "$resource_nonce";; esac
}

# Exact-name collisions remain caller-owned and byte-identical by immutable ID.
for resource in network container; do
    name=$(resource_name "$resource")
    if [ "$resource" = network ]; then
        caller_id=$("$real_docker" network create --internal --label "com.checknetwork.release.nonce=$nonce-$resource" --label com.checknetwork.release.owner=caller "$name")
        printf 'network|%s\n' "$caller_id" >>"$owned_ids"
    else
        caller_id=$("$real_docker" container create --name "$name" --label "com.checknetwork.release.nonce=$nonce-$resource" --label com.checknetwork.release.owner=caller "$base")
        printf 'container|%s\n' "$caller_id" >>"$owned_ids"
    fi
    if run_helper collision "$resource" >"$root/$resource-collision.out" 2>&1; then
        printf 'real %s collision unexpectedly succeeded\n' "$resource" >&2; exit 1
    fi
    if [ "$resource" = network ]; then after_id=$("$real_docker" network inspect "$name" --format '{{.Id}}'); else after_id=$("$real_docker" container inspect "$name" --format '{{.Id}}'); fi
    [ "$after_id" = "$caller_id" ] || { printf 'real %s collision changed caller immutable ID\n' "$resource" >&2; exit 1; }
    if [ "$resource" = network ]; then "$real_docker" network rm -- "$caller_id" >/dev/null; else "$real_docker" container rm -f -- "$caller_id" >/dev/null; fi
done
: >"$owned_ids"

# Daemon success with CLI response loss is recovered by nonce+owner+immutable ID.
for resource in network container; do
    output=$(run_helper response-loss "$resource")
    printf '%s\n' "$output" | grep -q '^SUCCESS id=' || { printf 'real %s response loss was not recovered\n' "$resource" >&2; exit 1; }
    name=$(resource_name "$resource")
    if [ "$resource" = network ]; then
        ! "$real_docker" network inspect "$name" >/dev/null 2>&1 || { printf 'response-loss network leaked\n' >&2; exit 1; }
    else
        ! "$real_docker" container inspect "$name" >/dev/null 2>&1 || { printf 'response-loss container leaked\n' >&2; exit 1; }
    fi
done

[ "$("$real_docker" image inspect "$base" --format '{{.Id}}')" = "$base_id" ] || { printf 'borrowed base identity changed\n' >&2; exit 1; }
inventory "$root/inventory.after"
cmp -s "$root/inventory.before" "$root/inventory.after" || {
    printf 'real Docker inventory changed:\n' >&2
    diff -u "$root/inventory.before" "$root/inventory.after" >&2 || :
    exit 1
}
printf 'real Docker API ownership tests passed: two collision types, two response-loss types, exact inventory, borrowed base preserved\n'
