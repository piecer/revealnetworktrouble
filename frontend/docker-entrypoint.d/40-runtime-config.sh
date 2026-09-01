#!/bin/sh
set -eu

runtime_config=/usr/share/nginx/html/runtime-config.js
escaped_value=$(printf '%s' "${CARTO_BASE_MAP:-}" | sed 's/\\/\\\\/g; s/"/\\"/g')
printf 'window.CHECKNETWORK_CONFIG = { CARTO_BASE_MAP: "%s" };\n' "$escaped_value" > "$runtime_config"
