#!/usr/bin/env bash
set -euo pipefail

readonly REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly COMPOSE_FILE="${COMPOSE_FILE:-$REPO_ROOT/deploy/docker-compose.yml}"
readonly EVIDENCE_ROOT="${EVIDENCE_ROOT:-$REPO_ROOT/var/e2e-evidence}"
readonly E2E_DB="${E2E_DB:-audit_gateway_e2e}"
readonly E2E_POSTGRES_DSN="postgres://audit:audit@postgres:5432/$E2E_DB?sslmode=disable"
cd "$REPO_ROOT"

if [[ ! "$E2E_DB" =~ ^[A-Za-z0-9_]+$ ]]; then
  echo "E2E_DB must contain only letters, numbers, and underscores" >&2
  exit 1
fi

docker compose -f "$COMPOSE_FILE" up -d postgres redis

until docker compose -f "$COMPOSE_FILE" exec -T postgres pg_isready -U audit -d audit_gateway >/dev/null; do
  sleep 1
done

docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U audit -d postgres -v ON_ERROR_STOP=1 \
  -c "DROP DATABASE IF EXISTS $E2E_DB WITH (FORCE);"
docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U audit -d postgres -v ON_ERROR_STOP=1 \
  -c "CREATE DATABASE $E2E_DB;"

POSTGRES_DB="$E2E_DB" docker compose -f "$COMPOSE_FILE" run --rm migrate

docker compose -f "$COMPOSE_FILE" exec -T redis redis-cli FLUSHDB >/dev/null

rm -rf "$EVIDENCE_ROOT"
mkdir -p "$EVIDENCE_ROOT/raw/e2e/trace_gap"
printf '{}\n' > "$EVIDENCE_ROOT/raw/e2e/trace_gap/request_body.bin"
printf '{}\n' > "$EVIDENCE_ROOT/raw/e2e/trace_gap/response_body.bin"

docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U audit -d "$E2E_DB" -v ON_ERROR_STOP=1 <<'SQL' >/dev/null
INSERT INTO traces (
    trace_id, method, path, route_pattern, protocol_family, capture_mode,
    status_code, upstream_status_code, stream, request_started_at,
    request_body_size, response_body_size, request_raw_ref, response_raw_ref,
    token_fingerprint, fingerprint_display, new_api_token_id_snapshot,
    token_name_snapshot, employee_no_snapshot, identity_resolution_status,
    model_requested, usage_total_tokens
) VALUES (
    'trace_gap', 'POST', '/v1/chat/completions', '/v1/chat/completions',
    'openai_chat', 'raw_and_normalized', 200, 200, false,
    '2026-04-28T13:45:22Z', 2, 2,
    'raw/e2e/trace_gap/request_body.bin', 'raw/e2e/trace_gap/response_body.bin',
    'tkfp_raw', 'tkfp_display', 42, '', '', 'unresolved',
    'gpt-4.1', 25001
);
SQL

job_file="$(mktemp)"
trap 'rm -f "$job_file"' EXIT
cat > "$job_file" <<'JSON'
{
  "type": "trace_captured",
  "trace_id": "trace_gap",
  "route_pattern": "/v1/chat/completions",
  "protocol_family": "openai_chat",
  "capture_mode": "raw_and_normalized",
  "employee_no": "",
  "request_raw_ref": "raw/e2e/trace_gap/request_body.bin",
  "response_raw_ref": "raw/e2e/trace_gap/response_body.bin",
  "request_content_type": "application/json",
  "response_content_type": "application/json",
  "model_requested": "gpt-4.1",
  "usage_total_tokens": 25001,
  "token_fingerprint": "tkfp_raw",
  "fingerprint_display": "tkfp_display",
  "new_api_token_id": 42,
  "token_name_snapshot": "",
  "identity_resolution_status": "unresolved",
  "status_code": 200,
  "upstream_status_code": 200,
  "stream": false,
  "request_started_at": "2026-04-28T13:45:22Z",
  "request_body_size": 2,
  "response_body_size": 2
}
JSON

docker compose -f "$COMPOSE_FILE" exec -T redis redis-cli -x RPUSH analysis_jobs < "$job_file" >/dev/null

worker_output="$(
  EVIDENCE_STORAGE_DIR="$EVIDENCE_ROOT" ANALYSIS_WORKER_POSTGRES_DSN="$E2E_POSTGRES_DSN" \
    docker compose -f "$COMPOSE_FILE" run --rm analysis-worker uv run python main.py --redis-once
)"
echo "$worker_output"

python - "$worker_output" <<'PY'
import json
import sys

payload = json.loads(sys.argv[1])
expected = {
    "worker_status": "processed",
    "anomaly_count": 2,
    "coverage_alert_count": 1,
}
for key, value in expected.items():
    if payload.get(key) != value:
        raise SystemExit(f"{key}={payload.get(key)!r}, want {value!r}")
PY

anomaly_count="$(
  docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U audit -d "$E2E_DB" -At \
    -c "SELECT count(*) FROM usage_anomalies WHERE 'trace_gap' = ANY(sample_trace_ids);"
)"
coverage_count="$(
  docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U audit -d "$E2E_DB" -At \
    -c "SELECT count(*) FROM coverage_alerts WHERE 'trace_gap' = ANY(sample_trace_ids);"
)"

if [[ "$anomaly_count" != "2" ]]; then
  echo "usage_anomalies count=$anomaly_count, want 2" >&2
  exit 1
fi

if [[ "$coverage_count" != "1" ]]; then
  echo "coverage_alerts count=$coverage_count, want 1" >&2
  exit 1
fi

docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U audit -d "$E2E_DB" \
  -c "TABLE usage_anomalies;" \
  -c "TABLE coverage_alerts;"
