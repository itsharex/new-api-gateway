#!/usr/bin/env bash
set -euo pipefail

: "${AUDIT_GATEWAY_URL:=http://localhost:8080}"
: "${NEW_API_KEY:?Set NEW_API_KEY to a new-api token for smoke testing}"

curl -sS "$AUDIT_GATEWAY_URL/v1/chat/completions" \
  -H "Authorization: Bearer $NEW_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-test","messages":[{"role":"user","content":"hello"}]}'
