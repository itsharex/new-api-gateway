#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"

curl -fsS "$BASE_URL/healthz" >/dev/null
curl -fsS "$BASE_URL/metrics" | rg "audit_gateway_requests_total|audit_gateway_identity_status_total|audit_gateway_capture_failures_total" >/dev/null

echo "remaining MVP smoke checks passed"
