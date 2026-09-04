#!/bin/sh
# Exact-name Docker ownership lifecycle for verify-release.sh.
# The caller provides fail(), resource variables, source_dir, tmp, and web_base.

web_resource_error_file() {
    printf '%s/release-%s-%s.stderr' "${tmp:-${TMPDIR:-/tmp}}" "$1" "$$"
}

web_error_is_absent() {
    printf '%s\n' "$1" | grep -Eiq 'no such (network|container)|network .* not found'
}

web_error_is_conflict() {
    printf '%s\n' "$1" | grep -Eiq 'conflict|already[- ]exists|already in use'
}

web_error_is_response_loss() {
    printf '%s\n' "$1" | grep -Eiq 'unexpected EOF|error during connect|connection reset|broken pipe|context (canceled|deadline exceeded)|transport is closing|closed network connection'
}

# Cleanup is a closed transaction. These globals are shared only by the fixed
# API/Web wrappers, and no daemon-controlled value is evaluated as shell source.
cleanup_docker_inspect() {
    cleanup_inspect_kind=$1
    cleanup_inspect_ref=$2
    cleanup_inspect_file=$(web_resource_error_file "cleanup-${cleanup_inspect_kind}")
    if [ "$cleanup_inspect_kind" = network ]; then
        if cleanup_inspect_identity=$(docker network inspect --format '{{.Id}}|{{.Name}}|{{index .Labels "com.checknetwork.release.nonce"}}|{{index .Labels "com.checknetwork.release.owner"}}' "$cleanup_inspect_ref" 2>"$cleanup_inspect_file"); then
            cleanup_inspect_status=0
        else
            cleanup_inspect_status=$?
        fi
    else
        if cleanup_inspect_identity=$(docker container inspect --format '{{.Id}}|{{.Name}}|{{index .Config.Labels "com.checknetwork.release.nonce"}}|{{index .Config.Labels "com.checknetwork.release.owner"}}' "$cleanup_inspect_ref" 2>"$cleanup_inspect_file"); then
            cleanup_inspect_status=0
        else
            cleanup_inspect_status=$?
        fi
    fi
    cleanup_inspect_error=$(cat "$cleanup_inspect_file")
    [ "$cleanup_inspect_status" -eq 0 ]
}

cleanup_error_is_absent() {
    web_error_is_absent "$1"
}

cleanup_mark_inactive() {
    cleanup_mark_family=$1
    cleanup_mark_kind=$2
    eval "${cleanup_mark_family}_${cleanup_mark_kind}_state=inactive"
    eval "${cleanup_mark_family}_${cleanup_mark_kind}_id="
}

cleanup_confirm_name_absent() {
    cleanup_confirm_family=$1
    cleanup_confirm_kind=$2
    cleanup_confirm_name=$3
    if cleanup_docker_inspect "$cleanup_confirm_kind" "$cleanup_confirm_name"; then
        return 1
    fi
    cleanup_error_is_absent "$cleanup_inspect_error" || return 1
    cleanup_mark_inactive "$cleanup_confirm_family" "$cleanup_confirm_kind"
}

cleanup_owned_docker_resource() {
    cleanup_family=$1
    cleanup_kind=$2
    cleanup_name=$3
    cleanup_expected_name=$4
    eval "cleanup_state=\${${cleanup_family}_${cleanup_kind}_state}"
    [ "$cleanup_state" != inactive ] || return 0
    eval "cleanup_id=\${${cleanup_family}_${cleanup_kind}_id}"
    eval "cleanup_nonce=\${${cleanup_family}_nonce}"
    eval "cleanup_owner=\${${cleanup_family}_owner_token}"
    eval "cleanup_preflight_absent=\${${cleanup_family}_${cleanup_kind}_preflight_absent}"

    if [ "$cleanup_id" = "" ]; then
        if ! cleanup_docker_inspect "$cleanup_kind" "$cleanup_name"; then
            if cleanup_error_is_absent "$cleanup_inspect_error"; then
                cleanup_mark_inactive "$cleanup_family" "$cleanup_kind"
                return 0
            fi
            return 1
        fi
        [ "$cleanup_preflight_absent" = true ] || return 1
        cleanup_candidate_id=${cleanup_inspect_identity%%|*}
        [ "$cleanup_candidate_id" != "" ] && [ "$cleanup_candidate_id" != "$cleanup_inspect_identity" ] || return 1
        [ "$cleanup_inspect_identity" = "$cleanup_candidate_id|$cleanup_expected_name|$cleanup_nonce|$cleanup_owner" ] || return 1
        cleanup_id=$cleanup_candidate_id
        eval "${cleanup_family}_${cleanup_kind}_id=\$cleanup_candidate_id"
    fi

    if ! cleanup_docker_inspect "$cleanup_kind" "$cleanup_id"; then
        if cleanup_error_is_absent "$cleanup_inspect_error"; then
            cleanup_confirm_name_absent "$cleanup_family" "$cleanup_kind" "$cleanup_name"
            return $?
        fi
        return 1
    fi
    [ "$cleanup_inspect_identity" = "$cleanup_id|$cleanup_expected_name|$cleanup_nonce|$cleanup_owner" ] || return 1

    if [ "$cleanup_kind" = network ]; then
        docker network rm -- "$cleanup_id" >/dev/null 2>&1 || :
    else
        docker container rm -f -- "$cleanup_id" >/dev/null 2>&1 || :
    fi
    cleanup_confirm_name_absent "$cleanup_family" "$cleanup_kind" "$cleanup_name"
}

preflight_web_network_absent() {
    web_network_preflight_file=$(web_resource_error_file network-preflight)
    if web_network_preflight_identity=$(docker network inspect --format '{{.Id}}|{{.Name}}|{{index .Labels "com.checknetwork.release.nonce"}}|{{index .Labels "com.checknetwork.release.owner"}}' "$web_network_name" 2>"$web_network_preflight_file"); then
        web_network_state=inactive
        web_network_preflight_absent=false
        fail "Web network name collision: $web_network_name"
    else
        web_network_preflight_status=$?
    fi
    web_network_preflight_error=$(cat "$web_network_preflight_file")
    [ "$web_network_preflight_status" -ne 0 ] || fail 'Web network preflight status was not preserved'
    web_error_is_absent "$web_network_preflight_error" || fail "Web network preflight failed: $web_network_preflight_error"
    web_network_preflight_absent=true
}

preflight_web_container_absent() {
    web_container_preflight_file=$(web_resource_error_file container-preflight)
    if web_container_preflight_identity=$(docker container inspect --format '{{.Id}}|{{.Name}}|{{index .Config.Labels "com.checknetwork.release.nonce"}}|{{index .Config.Labels "com.checknetwork.release.owner"}}' "$web_container_name" 2>"$web_container_preflight_file"); then
        web_container_state=inactive
        web_container_preflight_absent=false
        fail "Web container name collision: $web_container_name"
    else
        web_container_preflight_status=$?
    fi
    web_container_preflight_error=$(cat "$web_container_preflight_file")
    [ "$web_container_preflight_status" -ne 0 ] || fail 'Web container preflight status was not preserved'
    web_error_is_absent "$web_container_preflight_error" || fail "Web container preflight failed: $web_container_preflight_error"
    web_container_preflight_absent=true
}

inspect_web_network_by_name() {
    docker network inspect --format '{{.Id}}|{{.Name}}|{{index .Labels "com.checknetwork.release.nonce"}}|{{index .Labels "com.checknetwork.release.owner"}}' "$web_network_name" 2>/dev/null
}

inspect_web_network_by_id() {
    docker network inspect --format '{{.Id}}|{{.Name}}|{{index .Labels "com.checknetwork.release.nonce"}}|{{index .Labels "com.checknetwork.release.owner"}}' "$1" 2>/dev/null
}

inspect_web_container_by_name() {
    docker container inspect --format '{{.Id}}|{{.Name}}|{{index .Config.Labels "com.checknetwork.release.nonce"}}|{{index .Config.Labels "com.checknetwork.release.owner"}}' "$web_container_name" 2>/dev/null
}

inspect_web_container_by_id() {
    docker container inspect --format '{{.Id}}|{{.Name}}|{{index .Config.Labels "com.checknetwork.release.nonce"}}|{{index .Config.Labels "com.checknetwork.release.owner"}}' "$1" 2>/dev/null
}

recover_pending_web_network_id() {
    [ "$web_network_preflight_absent" = true ] || return 1
    web_network_recovery_identity=$(inspect_web_network_by_name) || return 1
    web_network_recovery_id=${web_network_recovery_identity%%|*}
    [ "$web_network_recovery_id" != "$web_network_recovery_identity" ] || return 1
    [ "$web_network_recovery_identity" = "$web_network_recovery_id|$web_network_name|$web_nonce|$web_owner_token" ] || return 1
    web_network_id=$web_network_recovery_id
    web_network_recovery_check=$(inspect_web_network_by_id "$web_network_id") || return 1
    [ "$web_network_recovery_check" = "$web_network_id|$web_network_name|$web_nonce|$web_owner_token" ]
}

recover_pending_web_container_id() {
    [ "$web_container_preflight_absent" = true ] || return 1
    web_container_recovery_identity=$(inspect_web_container_by_name) || return 1
    web_container_recovery_id=${web_container_recovery_identity%%|*}
    [ "$web_container_recovery_id" != "$web_container_recovery_identity" ] || return 1
    [ "$web_container_recovery_identity" = "$web_container_recovery_id|/$web_container_name|$web_nonce|$web_owner_token" ] || return 1
    web_container_id=$web_container_recovery_id
    web_container_recovery_check=$(inspect_web_container_by_id "$web_container_id") || return 1
    [ "$web_container_recovery_check" = "$web_container_id|/$web_container_name|$web_nonce|$web_owner_token" ]
}

create_owned_web_network() {
    preflight_web_network_absent
    web_network_state=network_create_pending
    web_network_create_file=$(web_resource_error_file network-create)
    if web_network_response=$(docker network create --internal \
        --label "$web_label_key=$web_nonce" \
        --label "$web_owner_label_key=$web_owner_token" \
        "$web_network_name" 2>"$web_network_create_file"); then
        web_network_create_status=0
    else
        web_network_create_status=$?
    fi
    web_network_create_error=$(cat "$web_network_create_file")
    if [ "$web_network_create_status" -ne 0 ]; then
        if web_error_is_conflict "$web_network_create_error"; then
            web_network_state=inactive
            web_network_preflight_absent=false
            fail "Web network creation collided: $web_network_create_error"
        fi
        if ! web_error_is_response_loss "$web_network_create_error"; then
            web_network_state=inactive
            web_network_preflight_absent=false
            fail "Web network creation failed (status $web_network_create_status): $web_network_create_error"
        fi
        recover_pending_web_network_id || fail "Web network response-loss ownership mismatch (status $web_network_create_status): $web_network_create_error"
    else
        [ "$web_network_response" != "" ] || fail 'Web network creation returned an empty ID'
        web_network_id=$web_network_response
        web_network_created_identity=$(inspect_web_network_by_id "$web_network_id") || fail 'created Web network disappeared'
        [ "$web_network_created_identity" = "$web_network_id|$web_network_name|$web_nonce|$web_owner_token" ] || fail 'created Web network identity mismatch'
    fi
    web_network_state=network_registered
    release_hook web_network_create
}

create_owned_web_container() {
    preflight_web_container_absent
    web_container_state=container_create_pending
    web_container_create_file=$(web_resource_error_file container-create)
    if web_container_response=$(docker container create --name "$web_container_name" \
        --label "$web_label_key=$web_nonce" \
        --label "$web_owner_label_key=$web_owner_token" \
        --network "$web_network_id" \
        --mount "type=bind,src=$source_dir/frontend/nginx.conf,dst=/etc/nginx/nginx.conf,readonly" \
        --mount "type=bind,src=$source_dir/frontend/app.js,dst=/usr/share/nginx/html/app.js,readonly" \
        --mount "type=bind,src=$source_dir/frontend/index.html,dst=/usr/share/nginx/html/index.html,readonly" \
        --mount "type=bind,src=$source_dir/frontend/state.js,dst=/usr/share/nginx/html/state.js,readonly" \
        --mount "type=bind,src=$source_dir/frontend/styles.css,dst=/usr/share/nginx/html/styles.css,readonly" \
        --mount "type=bind,src=$source_dir/frontend/topology-model.js,dst=/usr/share/nginx/html/topology-model.js,readonly" \
        --mount "type=bind,src=$source_dir/frontend/topology-renderer.js,dst=/usr/share/nginx/html/topology-renderer.js,readonly" \
        "$web_base" 2>"$web_container_create_file"); then
        web_container_create_status=0
    else
        web_container_create_status=$?
    fi
    web_container_create_error=$(cat "$web_container_create_file")
    if [ "$web_container_create_status" -ne 0 ]; then
        if web_error_is_conflict "$web_container_create_error"; then
            web_container_state=inactive
            web_container_preflight_absent=false
            fail "Web container creation collided: $web_container_create_error"
        fi
        if ! web_error_is_response_loss "$web_container_create_error"; then
            web_container_state=inactive
            web_container_preflight_absent=false
            fail "Web container creation failed (status $web_container_create_status): $web_container_create_error"
        fi
        recover_pending_web_container_id || fail "Web container response-loss ownership mismatch (status $web_container_create_status): $web_container_create_error"
    else
        [ "$web_container_response" != "" ] || fail 'Web container creation returned an empty ID'
        web_container_id=$web_container_response
        web_container_created_identity=$(inspect_web_container_by_id "$web_container_id") || fail 'created Web container disappeared'
        [ "$web_container_created_identity" = "$web_container_id|/$web_container_name|$web_nonce|$web_owner_token" ] || fail 'created Web container identity mismatch'
    fi
    web_container_state=container_registered
    release_hook web_container_create
}

cleanup_web_container() {
    cleanup_owned_docker_resource web container "$web_container_name" "/$web_container_name"
}

cleanup_web_network() {
    cleanup_owned_docker_resource web network "$web_network_name" "$web_network_name"
}

# API owns exactly two shared-daemon resources: one internal network and one
# borrowed-base smoke container. Archive builds and extraction are filesystem-only.
api_resource_error_file() {
    printf '%s/release-api-%s-%s.stderr' "${tmp:-${TMPDIR:-/tmp}}" "$1" "$$"
}

api_error_is_absent() {
    printf '%s\n' "$1" | grep -Eiq 'no such (network|container)|network .* not found'
}

api_error_is_conflict() {
    printf '%s\n' "$1" | grep -Eiq 'conflict|already[- ]exists|already in use'
}

api_error_is_response_loss() {
    printf '%s\n' "$1" | grep -Eiq 'unexpected EOF|error during connect|connection reset|broken pipe|context (canceled|deadline exceeded)|transport is closing|closed network connection'
}

inspect_api_network_by_name() {
    docker network inspect --format '{{.Id}}|{{.Name}}|{{index .Labels "com.checknetwork.release.nonce"}}|{{index .Labels "com.checknetwork.release.owner"}}' "$api_network_name" 2>/dev/null
}

inspect_api_network_by_id() {
    docker network inspect --format '{{.Id}}|{{.Name}}|{{index .Labels "com.checknetwork.release.nonce"}}|{{index .Labels "com.checknetwork.release.owner"}}' "$1" 2>/dev/null
}

recover_pending_api_network_id() {
    [ "$api_network_preflight_absent" = true ] || return 1
    api_network_recovery_identity=$(inspect_api_network_by_name) || return 1
    api_network_recovery_id=${api_network_recovery_identity%%|*}
    [ "$api_network_recovery_id" != "$api_network_recovery_identity" ] || return 1
    [ "$api_network_recovery_identity" = "$api_network_recovery_id|$api_network_name|$api_nonce|$api_owner_token" ] || return 1
    api_network_id=$api_network_recovery_id
    [ "$(inspect_api_network_by_id "$api_network_id")" = "$api_network_id|$api_network_name|$api_nonce|$api_owner_token" ]
}

create_owned_api_network() {
    api_network_preflight_file=$(api_resource_error_file network-preflight)
    if docker network inspect "$api_network_name" >/dev/null 2>"$api_network_preflight_file"; then
        api_network_preflight_absent=false
        fail "API network name collision: $api_network_name"
    else
        api_network_preflight_status=$?
    fi
    api_network_preflight_error=$(cat "$api_network_preflight_file")
    [ "$api_network_preflight_status" -ne 0 ] || fail 'API network preflight status was not preserved'
    api_error_is_absent "$api_network_preflight_error" || fail "API network preflight failed: $api_network_preflight_error"
    api_network_preflight_absent=true
    api_network_state=create_pending
    api_network_create_file=$(api_resource_error_file network-create)
    if api_network_response=$(docker network create --internal \
        --label "$api_label_key=$api_nonce" \
        --label "$api_owner_label_key=$api_owner_token" \
        "$api_network_name" 2>"$api_network_create_file"); then
        api_network_create_status=0
    else
        api_network_create_status=$?
    fi
    api_network_create_error=$(cat "$api_network_create_file")
    release_hook api_network_create
    if [ "$api_network_create_status" -ne 0 ]; then
        if api_error_is_conflict "$api_network_create_error"; then
            api_network_state=inactive
            api_network_preflight_absent=false
            fail "API network creation collided: $api_network_create_error"
        fi
        if ! api_error_is_response_loss "$api_network_create_error"; then
            api_network_state=inactive
            api_network_preflight_absent=false
            fail "API network creation failed (status $api_network_create_status): $api_network_create_error"
        fi
        recover_pending_api_network_id || fail "API network response-loss ownership mismatch (status $api_network_create_status): $api_network_create_error"
    else
        [ "$api_network_response" != "" ] || fail 'API network creation returned an empty ID'
        api_network_created_identity=$(inspect_api_network_by_id "$api_network_response") || fail 'created API network disappeared or returned a malformed ID'
        [ "$api_network_created_identity" = "$api_network_response|$api_network_name|$api_nonce|$api_owner_token" ] || fail 'created API network identity mismatch'
        api_network_id=$api_network_response
    fi
    api_network_state=registered
}

cleanup_api_network() {
    cleanup_owned_docker_resource api network "$api_network_name" "$api_network_name"
}

inspect_api_container_by_name() {
    docker container inspect --format '{{.Id}}|{{.Name}}|{{index .Config.Labels "com.checknetwork.release.nonce"}}|{{index .Config.Labels "com.checknetwork.release.owner"}}' "$api_container_name" 2>/dev/null
}

inspect_api_container_by_id() {
    docker container inspect --format '{{.Id}}|{{.Name}}|{{index .Config.Labels "com.checknetwork.release.nonce"}}|{{index .Config.Labels "com.checknetwork.release.owner"}}' "$1" 2>/dev/null
}

recover_pending_api_container_id() {
    [ "$api_container_preflight_absent" = true ] || return 1
    api_container_recovery_identity=$(inspect_api_container_by_name) || return 1
    api_container_recovery_id=${api_container_recovery_identity%%|*}
    [ "$api_container_recovery_id" != "$api_container_recovery_identity" ] || return 1
    [ "$api_container_recovery_identity" = "$api_container_recovery_id|/$api_container_name|$api_nonce|$api_owner_token" ] || return 1
    api_container_id=$api_container_recovery_id
    [ "$(inspect_api_container_by_id "$api_container_id")" = "$api_container_id|/$api_container_name|$api_nonce|$api_owner_token" ]
}

create_owned_api_container() {
    [ "$api_container_role" = smoke ] || fail 'API shared-daemon container role must be exactly smoke'
    api_container_preflight_file=$(api_resource_error_file container-preflight)
    if docker container inspect "$api_container_name" >/dev/null 2>"$api_container_preflight_file"; then
        api_container_preflight_absent=false
        fail "API smoke container name collision: $api_container_name"
    else
        api_container_preflight_status=$?
    fi
    api_container_preflight_error=$(cat "$api_container_preflight_file")
    [ "$api_container_preflight_status" -ne 0 ] || fail 'API container preflight status was not preserved'
    api_error_is_absent "$api_container_preflight_error" || fail "API container preflight failed: $api_container_preflight_error"
    api_container_preflight_absent=true
    api_container_state=create_pending
    api_container_create_file=$(api_resource_error_file container-create)
    if api_container_response=$(docker container create --name "$api_container_name" \
        --label "$api_label_key=$api_nonce" --label "$api_owner_label_key=$api_owner_token" \
        --network "$api_network_id" \
        --user 65532:65532 \
        --env CHECKNETWORK_ADDR=:8080 \
        --env PATH=/:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
        --entrypoint /checknetwork-api \
        --health-cmd 'wget --no-verbose --tries=1 --spider http://127.0.0.1:8080/readyz' \
        --health-interval 1s --health-timeout 3s --health-retries 30 \
        --mount "type=bind,src=$api_binary,dst=/checknetwork-api,readonly" \
        --mount "type=bind,src=$api_traceroute,dst=/traceroute,readonly" \
        "$api_base" 2>"$api_container_create_file"); then
        api_container_create_status=0
    else
        api_container_create_status=$?
    fi
    api_container_create_error=$(cat "$api_container_create_file")
    release_hook api_container_create
    if [ "$api_container_create_status" -ne 0 ]; then
        if api_error_is_conflict "$api_container_create_error"; then
            api_container_state=inactive
            api_container_preflight_absent=false
            fail "API smoke container creation collided: $api_container_create_error"
        fi
        if ! api_error_is_response_loss "$api_container_create_error"; then
            api_container_state=inactive
            api_container_preflight_absent=false
            fail "API smoke container creation failed (status $api_container_create_status): $api_container_create_error"
        fi
        recover_pending_api_container_id || fail "API smoke container response-loss ownership mismatch (status $api_container_create_status): $api_container_create_error"
    else
        [ "$api_container_response" != "" ] || fail 'API smoke container creation returned an empty ID'
        api_container_created_identity=$(inspect_api_container_by_id "$api_container_response") || fail 'created API smoke container disappeared or returned a malformed ID'
        [ "$api_container_created_identity" = "$api_container_response|/$api_container_name|$api_nonce|$api_owner_token" ] || fail 'created API smoke container identity mismatch'
        api_container_id=$api_container_response
    fi
    api_container_state=registered
}

cleanup_api_container() {
    cleanup_owned_docker_resource api container "$api_container_name" "/$api_container_name"
}
