#!/usr/bin/env bash

set -euo pipefail

binary="${1:?usage: runtime-smoke.sh /path/to/tsflow}"
if [[ ! -x "$binary" ]]; then
    printf 'runtime smoke: binary is missing or not executable: %s\n' "$binary" >&2
    exit 1
fi

runtime_tmpdir="$(mktemp -d)"
runtime_pid=""

cleanup() {
    if [[ -n "$runtime_pid" ]] && kill -0 "$runtime_pid" 2>/dev/null; then
        kill "$runtime_pid" 2>/dev/null || true
        wait "$runtime_pid" 2>/dev/null || true
    fi
    rm -rf "$runtime_tmpdir"
}
trap cleanup EXIT

runtime_port="18080"
runtime_log="$runtime_tmpdir/tsflow.log"

TAILSCALE_API_KEY=runtime-smoke-key \
TAILSCALE_TAILNET=runtime-smoke \
TAILSCALE_API_URL=http://127.0.0.1:9 \
TSFLOW_DB_PATH="$runtime_tmpdir/tsflow.db" \
TSFLOW_INITIAL_BACKFILL=1m \
TSFLOW_POLL_INTERVAL=1h \
TSFLOW_RETENTION=0 \
ENVIRONMENT=production \
PORT="$runtime_port" \
"$binary" >"$runtime_log" 2>&1 &
runtime_pid=$!

health_body=""
for _ in {1..60}; do
    if ! kill -0 "$runtime_pid" 2>/dev/null; then
        printf 'runtime smoke: server exited before becoming ready\n' >&2
        cat "$runtime_log" >&2
        exit 1
    fi
    if health_body="$(curl --fail --silent --show-error --compressed --max-time 2 "http://127.0.0.1:${runtime_port}/health" 2>/dev/null)" \
        && grep -q '"status":"healthy"' <<<"$health_body"; then
        break
    fi
    sleep 0.25
done

if ! grep -q '"status":"healthy"' <<<"$health_body"; then
    printf 'runtime smoke: /health did not become healthy\n' >&2
    cat "$runtime_log" >&2
    exit 1
fi

api_health_body="$(curl --fail --silent --show-error --compressed "http://127.0.0.1:${runtime_port}/api/health")"
grep -q '"service":"tsflow-backend"' <<<"$api_health_body"

index_body="$(curl --fail --silent --show-error --compressed "http://127.0.0.1:${runtime_port}/")"
grep -q 'TSFlow - Tailscale Network Visualizer' <<<"$index_body"

spa_body="$(curl --fail --silent --show-error --compressed "http://127.0.0.1:${runtime_port}/analytics")"
grep -q 'TSFlow - Tailscale Network Visualizer' <<<"$spa_body"

printf 'runtime smoke: health endpoints and embedded SPA passed\n'
