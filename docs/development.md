# Development

## Local Services

Start PostgreSQL and Redis:

```bash
docker compose -f deploy/docker-compose.yml up -d
```

Run schema migrations in the compose network:

```bash
docker compose -f deploy/docker-compose.yml run --rm migrate
```

## Tests

```bash
make test
```

## Python Worker

The analysis worker uses uv for Python dependency management. When `EVIDENCE_STORAGE_DIR` and `POSTGRES_DSN` are set (normal deployment), the worker starts in continuous Redis consumption mode — it blocks on BLPOP, processes each job, records a heartbeat, and loops. It exits gracefully on SIGTERM/SIGINT.

```bash
cd workers/analysis_worker
uv sync
uv run python main.py  # continuous Redis consumption (default)
```

Use `--redis-once` for testing or e2e scripts (process one job, then exit):

```bash
uv run python main.py --redis-once
```

Contract validation mode (no env vars, reads stdin):

```bash
uv run python main.py < contract_example.json
```

## Gateway Environment

Copy `.env.example` to `.env.local` and set `NEW_API_BASE_URL` to a running new-api instance.
Export those values into your shell before starting the Go binary; `make run` reads process environment variables, not `.env.local` directly:

```bash
set -a
source .env.local
set +a
make run
```

The gateway must never log or persist plaintext API keys. Tests should assert that API-key handling only stores HMAC fingerprints and token metadata.

## Evidence and Analysis Jobs

The gateway stores request body, response body, request headers, and response headers as raw evidence objects. Header evidence is JSON and redacts API-key-bearing headers before writing to storage.

The Redis `analysis_jobs` list receives `trace_captured` envelopes only after the trace row and raw evidence rows are persisted. Job envelopes include evidence refs, content types, requested model, and token usage fields when the gateway can extract them from non-streaming JSON responses.

## Analysis Persistence

Apply migrations through your local migration runner before processing analysis jobs. The worker now writes:

- `normalized_messages` for extracted request and response text.
- `analysis_results` for deterministic usage extraction status and `work_relevance` classification.
- `usage_aggregates` for hourly and daily employee/token/model/route totals.

Run the worker against a single stdin job:

```bash
cd workers/analysis_worker
uv sync
EVIDENCE_STORAGE_DIR=/absolute/path/to/evidence \
POSTGRES_DSN=postgres://audit:audit@localhost:5432/audit_gateway?sslmode=disable \
uv run python main.py < contract_example.json
```

Run the worker against one Redis job:

```bash
cd workers/analysis_worker
EVIDENCE_STORAGE_DIR=/absolute/path/to/evidence \
POSTGRES_DSN=postgres://audit:audit@localhost:5432/audit_gateway?sslmode=disable \
REDIS_URL=redis://localhost:6379/0 \
uv run python main.py --redis-once  # single job for testing
```

## Worker Anomalies and Coverage Alerts

After normalization and usage aggregation, the Python worker also writes review-ready outputs:

- `usage_anomalies` for deterministic MVP rules such as unresolved identity on successful upstream responses, invalid employee number snapshots, high single-trace token use, raw-only large responses, and server-error traces that may contribute to retry storms.
- `coverage_alerts` for worker-side normalization gaps where a route is marked `raw_and_normalized` but no normalized messages are extracted.

Run the worker tests:

```bash
cd workers/analysis_worker
uv run pytest -q
```

Run the Docker Compose end-to-end worker anomaly/coverage check:

```bash
./scripts/e2e_worker_anomaly_coverage.sh
```

The script recreates a local `audit_gateway_e2e` database, applies all migrations, pushes a synthetic `trace_captured` Redis job, runs the worker in the `analysis-worker` compose service, and verifies that `usage_anomalies` and `coverage_alerts` rows are persisted.

The MVP rules are intentionally explainable and per-trace. Baselines, semantic similarity, and cross-trace clustering should be implemented in later plans.

## Worker Work Relevance

The worker loads active `context_catalog` rows from PostgreSQL and writes a `work_relevance` row to `analysis_results` for every processed trace. The MVP classifier is deterministic: it matches normalized text against context keywords/aliases and a fixed task-category keyword map. Low-confidence, personal, or entertainment results set `needs_review` in `result_json`.

Run the Docker Compose work relevance check:

```bash
./scripts/e2e_worker_work_relevance.sh
```

## Admin API MVP

Admin API routes live under `/admin/api/*` in the same Go process as the proxy. The values below are for local smoke testing only; do not deploy these secrets or credentials to any shared environment. Required local settings:

```bash
export ADMIN_SESSION_SECRET=<at-least-32-character-local-secret>
export ADMIN_COOKIE_NAME=audit_admin_session
export ADMIN_COOKIE_SECURE=false
```

Create a local admin user for the password `admin123`. Change or replace this seeded password before using any shared environment:

```sql
INSERT INTO audit_users (username, password_hash, display_name, email, role, status)
VALUES ('admin', '$2a$10$WMdq6CBypgezqL/SCoFW7uyKHHx9DVkyWlaA7iEURl9z3AuyZWV.G', 'Local Admin', 'admin@example.test', 'admin', 'active')
ON CONFLICT (username) DO NOTHING;
```

Smoke login:

```bash
curl -i -c /tmp/audit.cookies \
  -H 'content-type: application/json' \
  -d '{"username":"admin","password":"admin123"}' \
  http://localhost:8080/admin/api/login

curl -b /tmp/audit.cookies http://localhost:8080/admin/api/me
```

API key lookup computes the same HMAC fingerprint used by the gateway and clears the submitted plaintext key before writing the audit log.

## Admin Web UI

The gateway serves the admin UI from the same binary at `/admin`.

Local flow:

1. Apply all migrations through `migrations/0006_admin_rbac_audit_logs.sql`.
2. Seed an `audit_users` row with a password hash generated by `internal/admin.HashPassword`.
3. Start the gateway with `ADMIN_SESSION_SECRET`, `ADMIN_COOKIE_NAME`, `ADMIN_COOKIE_SECURE=false`, `AUDIT_HMAC_SECRET`, `POSTGRES_DSN`, `REDIS_ADDR`, `NEW_API_BASE_URL`, and `EVIDENCE_STORAGE_DIR`.
4. Open `http://localhost:8080/admin`.

The UI uses the existing session cookie and calls these APIs:

- `GET /admin/api/me`
- `POST /admin/api/login`
- `POST /admin/api/logout`
- `GET /admin/api/overview`
- `GET /admin/api/usage`
- `GET /admin/api/traces`
- `GET /admin/api/traces/{trace_id}`
- `GET /admin/api/anomalies`
- `GET /admin/api/coverage-alerts`
- `POST /admin/api/api-key-lookup`
- `GET /admin/api/context-catalog`
- `POST /admin/api/context-catalog`
- `GET /admin/api/audit-logs`

Raw evidence links point at `/admin/api/raw-evidence/{trace_id}/{object_type}` and require the `raw_access` or `admin` role. Successful raw evidence access and successful API key lookup write `audit_action_logs`.

## Operational Health and Metrics

The gateway exposes operational endpoints before admin and proxy routing:

- `GET /healthz`: process liveness. Returns HTTP 200 when the gateway process can answer.
- `GET /readyz`: readiness. Returns HTTP 200 for `ok` or `degraded`, and HTTP 503 for `down`.
- `GET /metrics`: Prometheus text metrics when `OPS_METRICS_ENABLED=true`.

Operational environment variables:

- `OPS_CHECK_TIMEOUT`: dependency check timeout, default `2s`.
- `OPS_WORKER_HEARTBEAT_MAX_AGE`: maximum fresh analysis worker heartbeat age, default `5m`.
- `OPS_QUEUE_LAG_WARN_THRESHOLD`: Redis `analysis_jobs` depth that marks readiness degraded, default `1000`.
- `OPS_METRICS_ENABLED`: enables `/metrics`, default `true`.
- `ANALYSIS_WORKER_ID`: optional stable worker ID for Python analysis worker heartbeat rows.

Operational notes:

- `/metrics` currently computes metrics from the readiness snapshot, so each scrape runs dependency checks.
- The filesystem evidence readiness check rewrites one bounded date-scoped `ops_healthcheck/readiness` object rather than creating unbounded final artifacts.

Local smoke check:

The `scripts/smoke_ops_health.sh` helper checks `/healthz`, `/readyz`, and `/metrics`.

```bash
BASE_URL=http://127.0.0.1:8080 ./scripts/smoke_ops_health.sh
```

The smoke script expects the gateway to be running. `/readyz` can report `degraded` during local development when no analysis worker has written `worker_heartbeats`; that is acceptable as long as the response includes dependency checks and `/healthz` plus `/metrics` succeed.

## Remaining MVP Gap Checks

The remaining MVP hardening adds:

- new-api-compatible API key canonicalization;
- Redis plus PostgreSQL local identity cache lookup;
- degraded capture spooling that preserves proxy forwarding;
- multipart part evidence objects;
- Gemini, media, and SSE worker normalization;
- expanded explainable anomaly rules;
- CSRF and rate limits for unsafe admin actions;
- employee/token identity, review decision, and system settings admin views;
- media snapshot job queueing with SSRF-safe URL validation;
- expanded Prometheus metrics.

Run the local smoke check after the gateway is running:

```bash
BASE_URL=http://127.0.0.1:8080 ./scripts/smoke_remaining_mvp.sh
```
