#!/usr/bin/env bash
set -euo pipefail

timeout_seconds="${1:-60}"
deadline=$(( $(date +%s) + timeout_seconds ))

while [ "$(date +%s)" -lt "$deadline" ]; do
    status=$(podman compose ps --format json 2>/dev/null || echo '[]')
    count=$(printf '%s' "$status" | jq 'length')
    healthy=$(printf '%s' "$status" | jq '[.[] | select(.Health == "healthy")] | length')
    if [ "$count" -ge 2 ] && [ "$count" = "$healthy" ]; then
        echo "all services healthy"
        exit 0
    fi
    sleep 2
done
echo "timed out waiting for compose services to become healthy" >&2
exit 1
