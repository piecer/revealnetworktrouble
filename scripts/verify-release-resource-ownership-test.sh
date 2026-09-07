#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
root=$(mktemp -d "${TMPDIR:-/tmp}/checknetwork-release-ownership-test.XXXXXX")
trap 'rm -rf -- "$root"' EXIT HUP INT TERM
mkdir -p "$root/bin" "$root/frontend"
for asset in nginx.conf app.js index.html state.js styles.css topology-model.js topology-renderer.js topology-presentation.js topology-visualizer.js; do
    : >"$root/frontend/$asset"
done

cat >"$root/bin/docker" <<'FAKE_DOCKER'
#!/bin/sh
set -eu
kind=$1
shift
printf '%s:%s:%s\n' "$kind" "${1:-}" "$*" >>"$FAKE_STATE/calls"
case "$kind:$1" in
    network:inspect|container:inspect)
        action=$1
        shift
        ref=
        for arg in "$@"; do ref=$arg; done
        record=$FAKE_STATE/$kind
        if [ "${CLEANUP_PHASE:-false}" = true ] && [ "${FAKE_CLEANUP_MODE:-}" = pre-inspect-error ]; then
            printf 'daemon unavailable during cleanup inspect\n' >&2
            exit 125
        fi
        if [ "${FAKE_CLEANUP_MODE:-}" = post-inspect-error ] && [ -f "$FAKE_STATE/$kind.removed" ]; then
            printf 'daemon unavailable after cleanup removal\n' >&2
            exit 125
        fi
        [ -f "$record" ] || { printf 'Error: No such %s: %s\n' "$kind" "$ref" >&2; exit 1; }
        IFS='|' read -r id name nonce owner <"$record"
        [ "$ref" = "$id" ] || [ "$ref" = "$name" ] || { printf 'Error: No such %s: %s\n' "$kind" "$ref" >&2; exit 1; }
        if [ "$kind" = container ]; then name=/$name; fi
        printf '%s|%s|%s|%s\n' "$id" "$name" "$nonce" "$owner"
        ;;
    network:create|container:create)
        action=$1
        shift
        name=
        nonce=
        owner=
        previous=
        for arg in "$@"; do
            case "$previous:$arg" in
                --name:*) name=$arg ;;
                --label:com.checknetwork.release.nonce=*) nonce=${arg#*=} ;;
                --label:com.checknetwork.release.owner=*) owner=${arg#*=} ;;
            esac
            previous=$arg
            if [ "$kind" = network ]; then name=$arg; fi
        done
        mode=$(eval "printf '%s' \"\${FAKE_${kind}_MODE:-success}\"")
        id=${kind}-owned-id
        case "$mode" in
            success)
                printf '%s|%s|%s|%s\n' "$id" "$name" "$nonce" "$owner" >"$FAKE_STATE/$kind"
                printf '%s\n' "$id"
                ;;
            response_loss)
                printf '%s|%s|%s|%s\n' "$id" "$name" "$nonce" "$owner" >"$FAKE_STATE/$kind"
                printf 'error during connect: unexpected EOF\n' >&2
                exit 125
                ;;
            owner_mismatch)
                printf '%s|%s|%s|caller-owner\n' "$id" "$name" "$nonce" >"$FAKE_STATE/$kind"
                printf 'error during connect: unexpected EOF\n' >&2
                exit 125
                ;;
            nonce_mismatch)
                printf '%s|%s|caller-nonce|%s\n' "$id" "$name" "$owner" >"$FAKE_STATE/$kind"
                printf 'error during connect: unexpected EOF\n' >&2
                exit 125
                ;;
            success_owner_mismatch)
                printf '%s|%s|%s|caller-owner\n' "$id" "$name" "$nonce" >"$FAKE_STATE/$kind"
                printf '%s\n' "$id"
                ;;
            success_id_mismatch)
                printf '%s|%s|%s|%s\n' "$id" "$name" "$nonce" "$owner" >"$FAKE_STATE/$kind"
                printf '%s\n' "${kind}-wrong-stdout-id"
                ;;
            other_error)
                printf '%s|%s|%s|%s\n' "$id" "$name" "$nonce" "$owner" >"$FAKE_STATE/$kind"
                printf 'permission denied while waiting for response\n' >&2
                exit 2
                ;;
            race_conflict)
                printf '%s|%s|%s|caller-owner\n' "${kind}-caller-id" "$name" "$nonce" >"$FAKE_STATE/$kind"
                printf 'Error response from daemon: Conflict. The %s name is already in use.\n' "$kind" >&2
                exit 1
                ;;
            signal_owned)
                printf '%s|%s|%s|%s\n' "$id" "$name" "$nonce" "$owner" >"$FAKE_STATE/$kind"
                kill -s "$FAKE_SIGNAL" "$FAKE_RELEASE_PID"
                sleep 2
                ;;
            signal_caller)
                printf '%s|%s|%s|caller-owner\n' "${kind}-caller-id" "$name" "$nonce" >"$FAKE_STATE/$kind"
                kill -s "$FAKE_SIGNAL" "$FAKE_RELEASE_PID"
                sleep 2
                ;;
            *) exit 99 ;;
        esac
        ;;
    network:rm|container:rm)
        action=$1
        shift
        if [ "$kind" = container ] && [ "${1:-}" = -f ]; then shift; fi
        if [ "${1:-}" = -- ]; then shift; fi
        ref=$1
        record=$FAKE_STATE/$kind
        [ -f "$record" ] || exit 1
        IFS='|' read -r id name nonce owner <"$record"
        [ "$ref" = "$id" ] || exit 1
        if [ "${FAKE_CLEANUP_MODE:-}" = removal-failure ]; then
            printf 'daemon refused cleanup removal\n' >&2
            exit 2
        fi
        printf '%s:%s\n' "$kind" "$id" >>"$FAKE_STATE/removed"
        rm -f "$record"
        [ "${FAKE_CLEANUP_MODE:-}" != post-inspect-error ] || : >"$FAKE_STATE/$kind.removed"
        ;;
    *) printf 'unexpected docker invocation: %s %s\n' "$kind" "$*" >&2; exit 98 ;;
esac
FAKE_DOCKER
chmod +x "$root/bin/docker"

run_driver() {
    scenario=$1
    resource=$2
    state=$root/state-$scenario-$resource
    rm -rf -- "$state"
    mkdir -p "$state"
    PATH=$root/bin:$PATH FAKE_STATE=$state SCENARIO=$scenario RESOURCE=$resource TEST_ROOT=$root \
        sh -c '
            set -eu
            fail() { printf "%s\n" "$1" >&2; exit 1; }
            release_hook() { :; }
            . "$1/scripts/release-resource-ownership.sh"
            web_nonce=11111111111111111111111111111111
            web_owner_token=22222222222222222222222222222222
            web_network_name=checknetwork-web-${web_nonce}-network
            web_container_name=checknetwork-web-${web_nonce}-smoke
            web_network_id=
            web_container_id=
            web_network_state=inactive
            web_container_state=inactive
            web_network_preflight_absent=false
            web_container_preflight_absent=false
            web_label_key=com.checknetwork.release.nonce
            web_owner_label_key=com.checknetwork.release.owner
            source_dir=$TEST_ROOT
            web_base=nginx@example
            case "$SCENARIO" in
                collision)
                    printf "%s|%s|%s|caller-owner\n" "${RESOURCE}-original-id" "$(eval "printf %s \"\$web_${RESOURCE}_name\"")" "$web_nonce" >"$FAKE_STATE/$RESOURCE"
                    ;;
            esac
            cleanup_all() {
                CLEANUP_PHASE=true; export CLEANUP_PHASE
                cleanup_failed=false
                cleanup_web_container || cleanup_failed=true
                cleanup_web_network || cleanup_failed=true
                [ "$cleanup_failed" = false ]
            }
            cleanup_barrier() { cleanup_all || { printf "release cleanup failed\n" >&2; return 1; }; }
            on_signal() { code=$1; trap - EXIT; trap "" HUP INT TERM; if ! cleanup_all; then printf "release cleanup failed\n" >&2; fi; exit "$code"; }
            trap cleanup_all EXIT
            trap "on_signal 129" HUP
            trap "on_signal 130" INT
            trap "on_signal 143" TERM
            export FAKE_RELEASE_PID=$$
            case "$SCENARIO" in
                response-loss) eval "FAKE_${RESOURCE}_MODE=response_loss"; export "FAKE_${RESOURCE}_MODE" ;;
                owner-mismatch) eval "FAKE_${RESOURCE}_MODE=owner_mismatch"; export "FAKE_${RESOURCE}_MODE" ;;
                nonce-mismatch) eval "FAKE_${RESOURCE}_MODE=nonce_mismatch"; export "FAKE_${RESOURCE}_MODE" ;;
                success-owner-mismatch) eval "FAKE_${RESOURCE}_MODE=success_owner_mismatch"; export "FAKE_${RESOURCE}_MODE" ;;
                success-id-mismatch) eval "FAKE_${RESOURCE}_MODE=success_id_mismatch"; export "FAKE_${RESOURCE}_MODE" ;;
                other-error) eval "FAKE_${RESOURCE}_MODE=other_error"; export "FAKE_${RESOURCE}_MODE" ;;
                race-conflict) eval "FAKE_${RESOURCE}_MODE=race_conflict"; export "FAKE_${RESOURCE}_MODE" ;;
                signal-owned) eval "FAKE_${RESOURCE}_MODE=signal_owned"; export "FAKE_${RESOURCE}_MODE" ;;
                signal-caller) eval "FAKE_${RESOURCE}_MODE=signal_caller"; export "FAKE_${RESOURCE}_MODE" ;;
                removal-failure) FAKE_CLEANUP_MODE=removal-failure; export FAKE_CLEANUP_MODE ;;
                cleanup-pre-inspect-error) FAKE_CLEANUP_MODE=pre-inspect-error; export FAKE_CLEANUP_MODE ;;
                cleanup-post-inspect-error) FAKE_CLEANUP_MODE=post-inspect-error; export FAKE_CLEANUP_MODE ;;
            esac
            case "$SCENARIO" in
                pending-absent|pending-absent-failure|pending-absent-signal)
                    eval "web_${RESOURCE}_state=create_pending"
                    [ "$SCENARIO" != pending-absent-signal ] || kill -s "$FAKE_SIGNAL" "$$"
                    [ "$SCENARIO" != pending-absent-failure ] || exit 97
                    cleanup_barrier
                    printf "SUCCESS pending-absent\n"
                    exit 0
                    ;;
            esac
            if [ "$RESOURCE" = network ]; then create_owned_web_network; else web_network_id=network-id; create_owned_web_container; fi
            if [ "$SCENARIO" = replacement ]; then
                name=$(eval "printf %s \"\$web_${RESOURCE}_name\"")
                printf "%s|%s|%s|%s\n" "${RESOURCE}-replacement-id" "$name" "$web_nonce" "$web_owner_token" >"$FAKE_STATE/$RESOURCE"
            fi
            cleanup_barrier
            printf "SUCCESS id=%s\n" "$(eval "printf %s \"\$web_${RESOURCE}_id\"")"
        ' sh "$repo_dir" >"$state/output" 2>&1
}

expect_failure_preserved() {
    scenario=$1
    resource=$2
    state=$root/state-$scenario-$resource
    if run_driver "$scenario" "$resource"; then
        printf '%s/%s unexpectedly succeeded\n' "$scenario" "$resource" >&2
        exit 1
    fi
    ! grep -q '^SUCCESS ' "$state/output" || { printf '%s/%s printed success\n' "$scenario" "$resource" >&2; exit 1; }
    [ -f "$state/$resource" ] || { printf '%s/%s deleted caller resource\n' "$scenario" "$resource" >&2; exit 1; }
    [ ! -f "$state/removed" ] || { printf '%s/%s recorded caller deletion\n' "$scenario" "$resource" >&2; exit 1; }
}

for resource in network container; do
    expect_failure_preserved collision "$resource"
    grep -q "^${resource}-original-id|" "$root/state-collision-$resource/$resource" || { printf 'collision/%s changed original ID\n' "$resource" >&2; exit 1; }
    [ "$(grep -c "^$resource:inspect:" "$root/state-collision-$resource/calls")" -eq 1 ] || { printf 'collision/%s inspected after collision\n' "$resource" >&2; exit 1; }
    expect_failure_preserved owner-mismatch "$resource"
    expect_failure_preserved nonce-mismatch "$resource"
    expect_failure_preserved success-owner-mismatch "$resource"
    expect_failure_preserved success-id-mismatch "$resource"
    expect_failure_preserved other-error "$resource"
    [ "$(grep -c "^$resource:inspect:" "$root/state-other-error-$resource/calls")" -eq 1 ] || { printf 'other-error/%s attempted response-loss recovery\n' "$resource" >&2; exit 1; }
    expect_failure_preserved race-conflict "$resource"
    [ "$(grep -c "^$resource:inspect:" "$root/state-race-conflict-$resource/calls")" -eq 1 ] || { printf 'race-conflict/%s inspected after conflict\n' "$resource" >&2; exit 1; }

    run_driver response-loss "$resource"
    state=$root/state-response-loss-$resource
    grep -q '^SUCCESS ' "$state/output" || { printf 'response-loss/%s did not recover\n' "$resource" >&2; exit 1; }
    [ ! -f "$state/$resource" ] || { printf 'response-loss/%s leaked owned resource\n' "$resource" >&2; exit 1; }
    grep -q "^$resource:${resource}-owned-id$" "$state/removed" || { printf 'response-loss/%s cleanup not proven\n' "$resource" >&2; exit 1; }

    for scenario in removal-failure cleanup-pre-inspect-error cleanup-post-inspect-error replacement; do
        state=$root/state-$scenario-$resource
        if run_driver "$scenario" "$resource"; then
            printf '%s/%s unexpectedly succeeded\n' "$scenario" "$resource" >&2; exit 1
        fi
        ! grep -q '^SUCCESS ' "$state/output" || { printf '%s/%s printed false success\n' "$scenario" "$resource" >&2; exit 1; }
        grep -q '^release cleanup failed$' "$state/output" || { printf '%s/%s omitted safe cleanup diagnostic\n' "$scenario" "$resource" >&2; exit 1; }
    done
    state=$root/state-replacement-$resource
    grep -q "^${resource}-replacement-id|" "$state/$resource" || { printf 'replacement/%s identity changed\n' "$resource" >&2; exit 1; }
    [ ! -f "$state/removed" ] || { printf 'replacement/%s removal was attempted\n' "$resource" >&2; exit 1; }

    run_driver pending-absent "$resource"
    state=$root/state-pending-absent-$resource
    grep -q '^SUCCESS pending-absent$' "$state/output" || { printf 'pending-absent/%s did not succeed\n' "$resource" >&2; exit 1; }
    state=$root/state-pending-absent-failure-$resource
    set +e
    run_driver pending-absent-failure "$resource"
    status=$?
    set -e
    [ "$status" -eq 97 ] && ! grep -q '^SUCCESS ' "$state/output" || { printf 'pending-absent-failure/%s changed ordinary status\n' "$resource" >&2; exit 1; }

    for signal_and_code in HUP:129 INT:130 TERM:143; do
        signal=${signal_and_code%:*}
        code=${signal_and_code#*:}
        state=$root/state-signal-owned-$resource
        set +e
        FAKE_SIGNAL=$signal run_driver signal-owned "$resource"
        status=$?
        set -e
        [ "$status" -eq "$code" ] || { printf 'signal-owned/%s/%s status=%s want=%s\n' "$resource" "$signal" "$status" "$code" >&2; exit 1; }
        [ ! -f "$state/$resource" ] || { printf 'signal-owned/%s/%s leaked resource\n' "$resource" "$signal" >&2; exit 1; }

        state=$root/state-signal-caller-$resource
        set +e
        FAKE_SIGNAL=$signal run_driver signal-caller "$resource"
        status=$?
        set -e
        [ "$status" -eq "$code" ] || { printf 'signal-caller/%s/%s status=%s want=%s output=' "$resource" "$signal" "$status" "$code" >&2; cat "$state/output" >&2; exit 1; }
        [ "$(cat "$state/output")" = 'release cleanup failed' ] || { printf 'signal-caller/%s/%s cleanup diagnostic leaked resource data\n' "$resource" "$signal" >&2; exit 1; }
        [ -f "$state/$resource" ] || { printf 'signal-caller/%s/%s deleted caller resource\n' "$resource" "$signal" >&2; exit 1; }
        [ ! -f "$state/removed" ] || { printf 'signal-caller/%s/%s attempted deletion\n' "$resource" "$signal" >&2; exit 1; }

        state=$root/state-pending-absent-signal-$resource
        set +e
        FAKE_SIGNAL=$signal run_driver pending-absent-signal "$resource"
        status=$?
        set -e
        [ "$status" -eq "$code" ] && ! grep -q '^SUCCESS ' "$state/output" || { printf 'pending-absent-signal/%s/%s status changed\n' "$resource" "$signal" >&2; exit 1; }
    done
done

printf 'release resource ownership tests passed\n'
