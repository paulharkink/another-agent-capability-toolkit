#!/bin/bash
set -euo pipefail

if [[ "${1:-}" == server && "${2:-}" == start ]]; then
    shift 2
    transport_args=()
    port_args=()
    if [[ -n "${AZMCP_TRANSPORT:-}" ]]; then
        transport_args=(--transport "$AZMCP_TRANSPORT")
    fi
    if [[ -n "${AZMCP_PORT:-}" ]]; then
        port_args=(--port "$AZMCP_PORT")
    fi
    exec dotnet /app/azmcp.dll server start "${transport_args[@]}" "${port_args[@]}" "$@"
fi

exec dotnet /app/azmcp.dll "$@"
