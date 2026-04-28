#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"

health_body="$(curl -fsS "$BASE_URL/healthz")"
ready_body="$(curl -fsS "$BASE_URL/readyz" || true)"
metrics_body="$(curl -fsS "$BASE_URL/metrics")"

printf '%s\n' "$health_body" | rg '"status"[[:space:]]*:[[:space:]]*"ok"'
printf '%s\n' "$ready_body" | rg '"checks"'
printf '%s\n' "$metrics_body" | rg 'audit_gateway_up'
printf '%s\n' "$metrics_body" | rg 'audit_gateway_analysis_queue_depth'

echo "operational health smoke passed"
