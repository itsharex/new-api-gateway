#!/usr/bin/env bash
set -euo pipefail

: "${AUDIT_GATEWAY_URL:=http://localhost:8080}"
: "${NEW_API_KEY:?Set NEW_API_KEY to a new-api token for smoke testing}"

readonly AUDIT_GATEWAY_BASE_URL="${AUDIT_GATEWAY_URL%/}"

curl -sS --fail-with-body --connect-timeout 5 --max-time 30 "$AUDIT_GATEWAY_BASE_URL/v1/chat/completions" \
  -H "Authorization: Bearer $NEW_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-test","messages":[{"role":"user","content":"hello"}]}'
