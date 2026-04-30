#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"

curl -fsS "$BASE_URL/healthz" >/dev/null
curl -fsS "$BASE_URL/readyz" >/dev/null

metrics="$(curl -fsS "$BASE_URL/metrics")"

printf '%s\n' "$metrics" | rg '^audit_gateway_up 1$' >/dev/null
printf '%s\n' "$metrics" | rg '^audit_gateway_requests_total [0-9]+$' >/dev/null
printf '%s\n' "$metrics" | rg '^audit_gateway_capture_failures_total [0-9]+$' >/dev/null
printf '%s\n' "$metrics" | rg '^audit_gateway_identity_status_total\{status="[^"]+"\} [0-9]+$' >/dev/null

echo "remaining MVP smoke checks passed"
