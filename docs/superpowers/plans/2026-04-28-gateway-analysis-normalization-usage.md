# Gateway Analysis Normalization and Usage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn captured gateway evidence jobs into persisted normalized messages, analysis results, and hourly/daily usage aggregates.

**Architecture:** Keep the Go gateway as the capture boundary and move protocol understanding into the Python worker. The worker reads `trace_captured` jobs, loads filesystem evidence by object ref, normalizes known JSON protocols into `normalized_messages`, records a deterministic usage extraction result, and upserts usage totals into PostgreSQL aggregate buckets.

**Tech Stack:** Go 1.22+ migrations, PostgreSQL, Python 3.11, `psycopg[binary]`, `redis`, `pytest`, existing filesystem evidence refs, existing Redis `analysis_jobs` list.

---

## Completed Context Check

The approved design in `docs/superpowers/specs/2026-04-25-new-api-gateway-audit-design.md` covers gateway capture, identity, async analysis, usage aggregation, anomaly detection, admin product, RBAC, audit logs, media snapshots, and operations.

The completed plans cover the gateway side:

- `docs/superpowers/plans/2026-04-25-new-api-gateway-core-mvp.md`: proxying, API-key fingerprinting, identity resolution, raw evidence capture, trace rows, coverage alerts.
- `docs/superpowers/plans/2026-04-27-gateway-evidence-minimal-metadata.md`: redacted header evidence, broader route registry, model and usage extraction in the gateway for non-streaming JSON, enriched `trace_captured` job envelope.

Current verification baseline before this plan:

```bash
go test ./...
cd workers/analysis_worker && uv run python main.py < contract_example.json
```

Expected current result: Go packages pass, and the worker prints an accepted `trace_example` response. The worker still only accepts the job contract; it does not persist normalized messages, analysis results, or usage aggregates.

## Remaining Requirements Map

This plan covers the next independent slice:

- Protocol normalization for common JSON routes.
- Usage extraction result persistence.
- Hourly and daily usage aggregation by employee, token, route, model, and protocol.
- Worker queue execution against Redis and PostgreSQL.

Separate future plans should cover:

- Rule-based anomalies and review workflow.
- Work relevance classification and context catalog.
- Admin API, Web UI, RBAC, raw evidence access audit logs, and API key lookup UI.
- Remote media snapshot downloader and SSRF protections.
- Metrics, readiness checks, degraded spooling, retention, backup, and reanalysis jobs.

## File Structure

- Create `migrations/0003_analysis_normalization_usage.sql`: tables and indexes for normalized messages, analysis results, and usage aggregates.
- Modify `internal/jobs/jobs.go`: add identity, status, timing, and body-size fields needed by worker aggregates.
- Modify `internal/jobs/jobs_test.go`: verify the enriched `trace_captured` JSON envelope.
- Modify `internal/gateway/proxy.go`: pass trace snapshot, status, timing, stream, and body sizes into the job envelope.
- Modify `internal/gateway/proxy_test.go`: assert the gateway publishes aggregate-ready job metadata.
- Modify `workers/analysis_worker/contract_example.json`: include the enriched fields consumed by the worker.
- Modify `workers/analysis_worker/pyproject.toml`: add `psycopg[binary]`, `redis`, and `pytest`.
- Create `workers/analysis_worker/tests/test_models.py`: job parsing tests.
- Create `workers/analysis_worker/models.py`: job, message, result, and aggregate dataclasses.
- Create `workers/analysis_worker/tests/test_evidence.py`: safe evidence loading tests.
- Create `workers/analysis_worker/evidence.py`: filesystem evidence reader for gateway object refs.
- Create `workers/analysis_worker/tests/test_normalizers.py`: protocol normalization tests.
- Create `workers/analysis_worker/normalizers.py`: OpenAI Chat, Responses, Claude Messages, and generic prompt normalizers.
- Create `workers/analysis_worker/tests/test_repository.py`: SQL persistence tests with fake connection/cursor.
- Create `workers/analysis_worker/repository.py`: PostgreSQL writes for normalized messages, analysis results, and aggregate upserts.
- Create `workers/analysis_worker/tests/test_pipeline.py`: end-to-end in-process worker test with fake repository.
- Modify `workers/analysis_worker/main.py`: process stdin jobs, Redis jobs, evidence loading, normalization, persistence, and JSON status output.
- Modify `docs/development.md`: document worker DB/Redis execution and the new persistence outputs.

---

### Task 1: Analysis Schema Migration

**Files:**
- Create: `migrations/0003_analysis_normalization_usage.sql`

- [ ] **Step 1: Write the migration**

Create `migrations/0003_analysis_normalization_usage.sql`:

```sql
CREATE TABLE IF NOT EXISTS normalized_messages (
    id BIGSERIAL PRIMARY KEY,
    trace_id TEXT NOT NULL REFERENCES traces(trace_id) ON DELETE CASCADE,
    direction TEXT NOT NULL,
    sequence_index INTEGER NOT NULL,
    role TEXT NOT NULL DEFAULT '',
    modality TEXT NOT NULL DEFAULT 'text',
    content_text TEXT NOT NULL DEFAULT '',
    content_text_hash TEXT NOT NULL DEFAULT '',
    media_object_id BIGINT,
    media_url TEXT NOT NULL DEFAULT '',
    source_path TEXT NOT NULL DEFAULT '',
    protocol_item_type TEXT NOT NULL DEFAULT '',
    token_count_estimate INTEGER NOT NULL DEFAULT 0,
    metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_normalized_messages_trace_sequence
    ON normalized_messages(trace_id, direction, sequence_index, source_path);

CREATE INDEX IF NOT EXISTS idx_normalized_messages_trace
    ON normalized_messages(trace_id);

CREATE TABLE IF NOT EXISTS analysis_results (
    id BIGSERIAL PRIMARY KEY,
    trace_id TEXT NOT NULL REFERENCES traces(trace_id) ON DELETE CASCADE,
    analyzer_name TEXT NOT NULL,
    analyzer_version TEXT NOT NULL,
    policy_version TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL,
    label TEXT NOT NULL,
    score NUMERIC NOT NULL DEFAULT 0,
    confidence NUMERIC NOT NULL DEFAULT 0,
    severity TEXT NOT NULL DEFAULT '',
    evidence_message_ids BIGINT[] NOT NULL DEFAULT '{}',
    evidence_spans_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    result_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_analysis_results_trace
    ON analysis_results(trace_id);

CREATE INDEX IF NOT EXISTS idx_analysis_results_category_label
    ON analysis_results(category, label);

CREATE TABLE IF NOT EXISTS usage_aggregates (
    id BIGSERIAL PRIMARY KEY,
    bucket_start TIMESTAMPTZ NOT NULL,
    bucket_size TEXT NOT NULL,
    token_fingerprint TEXT NOT NULL DEFAULT '',
    new_api_token_id INTEGER NOT NULL DEFAULT 0,
    employee_no TEXT NOT NULL DEFAULT '',
    token_name_snapshot TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    route_pattern TEXT NOT NULL DEFAULT '',
    protocol_family TEXT NOT NULL DEFAULT '',
    request_count BIGINT NOT NULL DEFAULT 0,
    success_count BIGINT NOT NULL DEFAULT 0,
    error_count BIGINT NOT NULL DEFAULT 0,
    stream_count BIGINT NOT NULL DEFAULT 0,
    prompt_tokens BIGINT NOT NULL DEFAULT 0,
    completion_tokens BIGINT NOT NULL DEFAULT 0,
    total_tokens BIGINT NOT NULL DEFAULT 0,
    reasoning_tokens BIGINT NOT NULL DEFAULT 0,
    cached_tokens BIGINT NOT NULL DEFAULT 0,
    estimated_cost TEXT NOT NULL DEFAULT '',
    request_body_bytes BIGINT NOT NULL DEFAULT 0,
    response_body_bytes BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (
        bucket_start, bucket_size, token_fingerprint, employee_no,
        model, route_pattern, protocol_family
    )
);

CREATE INDEX IF NOT EXISTS idx_usage_aggregates_employee_bucket
    ON usage_aggregates(employee_no, bucket_size, bucket_start);

CREATE INDEX IF NOT EXISTS idx_usage_aggregates_token_bucket
    ON usage_aggregates(token_fingerprint, bucket_size, bucket_start);
```

- [ ] **Step 2: Verify the migration is self-contained**

Run:

```bash
rg -n "CREATE TABLE IF NOT EXISTS (normalized_messages|analysis_results|usage_aggregates)" migrations/0003_analysis_normalization_usage.sql
```

Expected: three matches, one for each new table.

- [ ] **Step 3: Commit**

```bash
git add migrations/0003_analysis_normalization_usage.sql
git commit -m "feat: add analysis persistence schema"
```

---

### Task 2: Aggregate-Ready Gateway Job Envelope

**Files:**
- Modify: `internal/jobs/jobs.go`
- Modify: `internal/jobs/jobs_test.go`
- Modify: `internal/gateway/proxy.go`
- Modify: `internal/gateway/proxy_test.go`
- Modify: `workers/analysis_worker/contract_example.json`

- [ ] **Step 1: Write job envelope tests first**

Modify `TestRedisListPublisherPushesTraceCapturedEnvelope` in `internal/jobs/jobs_test.go` so the `TraceCapturedInput` includes aggregate dimensions and the decoded job assertions verify them:

```go
job := NewTraceCaptured(TraceCapturedInput{
	TraceID:             "trace_1",
	RoutePattern:        "/v1/chat/completions",
	ProtocolFamily:      "openai_chat",
	CaptureMode:         "raw_and_normalized",
	EmployeeNo:          "E12345",
	TokenFingerprint:    "tkfp_raw_value",
	FingerprintDisplay:  "tkfp_display",
	NewAPITokenID:       42,
	TokenNameSnapshot:   "E12345",
	StatusCode:          200,
	UpstreamStatusCode:  200,
	Stream:              false,
	RequestStartedAt:    "2026-04-28T13:45:22Z",
	RequestBodySize:     128,
	ResponseBodySize:    256,
	RequestRawRef:       "raw/trace_1/request_body.bin",
	RequestHeadersRef:   "raw/trace_1/request_headers.bin",
	ResponseRawRef:      "raw/trace_1/response_body.bin",
	ResponseHeadersRef:  "raw/trace_1/response_headers.bin",
	RequestContentType:  "application/json",
	ResponseContentType: "application/json",
	ModelRequested:      "gpt-test",
	UsageTotalTokens:    18,
})
```

Add these assertions after decoding:

```go
if decoded.TokenFingerprint != "tkfp_raw_value" || decoded.FingerprintDisplay != "tkfp_display" {
	t.Fatalf("fingerprint fields = %+v", decoded)
}
if decoded.NewAPITokenID != 42 || decoded.TokenNameSnapshot != "E12345" {
	t.Fatalf("token snapshot fields = %+v", decoded)
}
if decoded.StatusCode != 200 || decoded.UpstreamStatusCode != 200 || decoded.Stream {
	t.Fatalf("status fields = %+v", decoded)
}
if decoded.RequestStartedAt != "2026-04-28T13:45:22Z" {
	t.Fatalf("RequestStartedAt = %q", decoded.RequestStartedAt)
}
if decoded.RequestBodySize != 128 || decoded.ResponseBodySize != 256 {
	t.Fatalf("body sizes = %+v", decoded)
}
```

- [ ] **Step 2: Run job tests and verify failure**

Run:

```bash
go test ./internal/jobs -run TestRedisListPublisherPushesTraceCapturedEnvelope -v
```

Expected: FAIL because the job structs do not have aggregate-ready fields yet.

- [ ] **Step 3: Extend job structs**

Modify `TraceCapturedJob` and `TraceCapturedInput` in `internal/jobs/jobs.go` by adding these fields after `EmployeeNo`:

```go
	TokenFingerprint   string `json:"token_fingerprint"`
	FingerprintDisplay string `json:"fingerprint_display"`
	NewAPITokenID      int    `json:"new_api_token_id"`
	TokenNameSnapshot  string `json:"token_name_snapshot"`
	StatusCode         int    `json:"status_code"`
	UpstreamStatusCode int    `json:"upstream_status_code"`
	Stream             bool   `json:"stream"`
	RequestStartedAt   string `json:"request_started_at"`
	RequestBodySize    int64  `json:"request_body_size"`
	ResponseBodySize   int64  `json:"response_body_size"`
```

Modify `NewTraceCaptured` so it copies the new fields:

```go
TokenFingerprint:   input.TokenFingerprint,
FingerprintDisplay: input.FingerprintDisplay,
NewAPITokenID:      input.NewAPITokenID,
TokenNameSnapshot:  input.TokenNameSnapshot,
StatusCode:         input.StatusCode,
UpstreamStatusCode: input.UpstreamStatusCode,
Stream:             input.Stream,
RequestStartedAt:   input.RequestStartedAt,
RequestBodySize:    input.RequestBodySize,
ResponseBodySize:   input.ResponseBodySize,
```

- [ ] **Step 4: Enrich gateway job publication**

Modify the `jobs.NewTraceCaptured(jobs.TraceCapturedInput{...})` call in `internal/gateway/proxy.go` to include:

```go
TokenFingerprint:    record.snapshot.TokenFingerprint,
FingerprintDisplay:  record.snapshot.FingerprintDisplay,
NewAPITokenID:       record.snapshot.NewAPITokenID,
TokenNameSnapshot:   record.snapshot.TokenNameRaw,
StatusCode:          record.statusCode,
UpstreamStatusCode:  record.upstreamCode,
Stream:              record.stream,
RequestStartedAt:    record.startedAt.UTC().Format(time.RFC3339),
RequestBodySize:     record.requestSize,
ResponseBodySize:    record.responseSize,
```

- [ ] **Step 5: Assert gateway publishes aggregate dimensions**

In `internal/gateway/proxy_test.go`, extend `TestProxyRecordsHeaderEvidenceAndMinimalMetadata` after the existing job metadata assertions:

```go
if job.TokenFingerprint == "" || job.FingerprintDisplay == "" {
	t.Fatalf("job fingerprint fields missing: %+v", job)
}
if job.NewAPITokenID != 7 || job.TokenNameSnapshot != "E12345" {
	t.Fatalf("job token snapshot fields = %+v", job)
}
if job.StatusCode != http.StatusOK || job.UpstreamStatusCode != http.StatusOK {
	t.Fatalf("job status fields = %+v", job)
}
if job.RequestStartedAt == "" || job.RequestBodySize == 0 || job.ResponseBodySize == 0 {
	t.Fatalf("job timing/body metadata missing: %+v", job)
}
```

- [ ] **Step 6: Update the worker contract fixture**

Replace `workers/analysis_worker/contract_example.json` with:

```json
{
  "type": "trace_captured",
  "trace_id": "trace_example",
  "route_pattern": "/v1/chat/completions",
  "protocol_family": "openai_chat",
  "capture_mode": "raw_and_normalized",
  "employee_no": "E12345",
  "token_fingerprint": "tkfp_raw_value",
  "fingerprint_display": "tkfp_display",
  "new_api_token_id": 42,
  "token_name_snapshot": "E12345",
  "status_code": 200,
  "upstream_status_code": 200,
  "stream": false,
  "request_started_at": "2026-04-28T13:45:22Z",
  "request_body_size": 128,
  "response_body_size": 256,
  "request_raw_ref": "raw/2026/04/27/trace_example/request_body.bin",
  "request_headers_ref": "raw/2026/04/27/trace_example/request_headers.bin",
  "response_raw_ref": "raw/2026/04/27/trace_example/response_body.bin",
  "response_headers_ref": "raw/2026/04/27/trace_example/response_headers.bin",
  "request_content_type": "application/json",
  "response_content_type": "application/json",
  "model_requested": "gpt-test",
  "usage_prompt_tokens": 11,
  "usage_completion_tokens": 7,
  "usage_total_tokens": 18,
  "usage_reasoning_tokens": 2,
  "usage_cached_tokens": 3
}
```

- [ ] **Step 7: Run gateway job tests and verify pass**

Run:

```bash
go test ./internal/jobs ./internal/gateway -run 'TestRedisListPublisherPushesTraceCapturedEnvelope|TestProxyRecordsHeaderEvidenceAndMinimalMetadata' -v
```

Expected: PASS for both targeted tests.

- [ ] **Step 8: Commit**

```bash
git add internal/jobs/jobs.go internal/jobs/jobs_test.go internal/gateway/proxy.go internal/gateway/proxy_test.go workers/analysis_worker/contract_example.json
git commit -m "feat: enrich trace captured jobs for aggregation"
```

---

### Task 3: Worker Models and Dependency Setup

**Files:**
- Modify: `workers/analysis_worker/pyproject.toml`
- Create: `workers/analysis_worker/models.py`
- Create: `workers/analysis_worker/tests/test_models.py`

- [ ] **Step 1: Add model tests first**

Create `workers/analysis_worker/tests/test_models.py`:

```python
import json

from models import TraceCapturedJob, bucket_start_hour, parse_job


def test_parse_job_keeps_gateway_contract_fields():
    job = parse_job(json.dumps({
        "type": "trace_captured",
        "trace_id": "trace_123",
        "route_pattern": "/v1/chat/completions",
        "protocol_family": "openai_chat",
        "capture_mode": "raw_and_normalized",
        "employee_no": "E10001",
        "request_raw_ref": "raw/2026/04/28/trace_123/request_body.bin",
        "response_raw_ref": "raw/2026/04/28/trace_123/response_body.bin",
        "model_requested": "gpt-4.1",
        "usage_prompt_tokens": 11,
        "usage_completion_tokens": 7,
        "usage_total_tokens": 18,
        "usage_reasoning_tokens": 2,
        "usage_cached_tokens": 3,
        "token_fingerprint": "tkfp_raw_value",
        "fingerprint_display": "tkfp_display",
        "new_api_token_id": 42,
        "token_name_snapshot": "E10001",
        "request_body_size": 128,
        "response_body_size": 256
    }))

    assert job == TraceCapturedJob(
        type="trace_captured",
        trace_id="trace_123",
        route_pattern="/v1/chat/completions",
        protocol_family="openai_chat",
        capture_mode="raw_and_normalized",
        employee_no="E10001",
        request_raw_ref="raw/2026/04/28/trace_123/request_body.bin",
        response_raw_ref="raw/2026/04/28/trace_123/response_body.bin",
        request_headers_ref="",
        response_headers_ref="",
        request_content_type="",
        response_content_type="",
        model_requested="gpt-4.1",
        usage_prompt_tokens=11,
        usage_completion_tokens=7,
        usage_total_tokens=18,
        usage_reasoning_tokens=2,
        usage_cached_tokens=3,
        token_fingerprint="tkfp_raw_value",
        fingerprint_display="tkfp_display",
        new_api_token_id=42,
        token_name_snapshot="E10001",
        status_code=0,
        upstream_status_code=0,
        stream=False,
        request_started_at="",
        request_body_size=128,
        response_body_size=256
    )


def test_bucket_start_hour_truncates_iso_timestamp():
    assert bucket_start_hour("2026-04-28T13:45:22Z") == "2026-04-28T13:00:00+00:00"
```

- [ ] **Step 2: Run the model tests and verify failure**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_models.py -q
```

Expected: FAIL because `pytest` and `models.py` are not available yet.

- [ ] **Step 3: Add worker dependencies**

Modify `workers/analysis_worker/pyproject.toml`:

```toml
[project]
name = "new-api-gateway-analysis-worker"
version = "0.1.0"
description = "Analysis worker contracts for the new-api audit gateway"
requires-python = ">=3.11"
dependencies = [
    "psycopg[binary]>=3.2.0",
    "redis>=5.0.0",
    "pytest>=8.0.0",
]

[tool.uv]
package = false
```

- [ ] **Step 4: Create worker model code**

Create `workers/analysis_worker/models.py`:

```python
import json
from dataclasses import dataclass
from datetime import datetime, timezone
from hashlib import sha256
from typing import Any


@dataclass(frozen=True)
class TraceCapturedJob:
    type: str
    trace_id: str
    route_pattern: str
    protocol_family: str
    capture_mode: str
    employee_no: str
    request_raw_ref: str = ""
    request_headers_ref: str = ""
    response_raw_ref: str = ""
    response_headers_ref: str = ""
    request_content_type: str = ""
    response_content_type: str = ""
    model_requested: str = ""
    usage_prompt_tokens: int = 0
    usage_completion_tokens: int = 0
    usage_total_tokens: int = 0
    usage_reasoning_tokens: int = 0
    usage_cached_tokens: int = 0
    token_fingerprint: str = ""
    fingerprint_display: str = ""
    new_api_token_id: int = 0
    token_name_snapshot: str = ""
    status_code: int = 0
    upstream_status_code: int = 0
    stream: bool = False
    request_started_at: str = ""
    request_body_size: int = 0
    response_body_size: int = 0


@dataclass(frozen=True)
class NormalizedMessage:
    trace_id: str
    direction: str
    sequence_index: int
    role: str
    modality: str
    content_text: str
    content_text_hash: str
    media_url: str
    source_path: str
    protocol_item_type: str
    token_count_estimate: int
    metadata: dict[str, Any]


@dataclass(frozen=True)
class AnalysisResult:
    trace_id: str
    analyzer_name: str
    analyzer_version: str
    policy_version: str
    category: str
    label: str
    score: float
    confidence: float
    severity: str
    result: dict[str, Any]


@dataclass(frozen=True)
class UsageAggregateDelta:
    bucket_start: str
    bucket_size: str
    token_fingerprint: str
    new_api_token_id: int
    employee_no: str
    token_name_snapshot: str
    model: str
    route_pattern: str
    protocol_family: str
    request_count: int
    success_count: int
    error_count: int
    stream_count: int
    prompt_tokens: int
    completion_tokens: int
    total_tokens: int
    reasoning_tokens: int
    cached_tokens: int
    request_body_bytes: int
    response_body_bytes: int


def parse_job(line: str) -> TraceCapturedJob:
    data = json.loads(line)
    known = {field: data[field] for field in TraceCapturedJob.__dataclass_fields__ if field in data}
    return TraceCapturedJob(**known)


def text_hash(value: str) -> str:
    return sha256(value.encode("utf-8")).hexdigest()


def bucket_start_hour(value: str) -> str:
    if not value:
        return datetime.now(timezone.utc).replace(minute=0, second=0, microsecond=0).isoformat()
    parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    return parsed.astimezone(timezone.utc).replace(minute=0, second=0, microsecond=0).isoformat()


def bucket_start_day(value: str) -> str:
    if not value:
        now = datetime.now(timezone.utc)
        return now.replace(hour=0, minute=0, second=0, microsecond=0).isoformat()
    parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    return parsed.astimezone(timezone.utc).replace(hour=0, minute=0, second=0, microsecond=0).isoformat()
```

- [ ] **Step 5: Run model tests and verify pass**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_models.py -q
```

Expected: PASS with `2 passed`.

- [ ] **Step 6: Commit**

```bash
git add workers/analysis_worker/pyproject.toml workers/analysis_worker/models.py workers/analysis_worker/tests/test_models.py
git commit -m "feat: add analysis worker job models"
```

---

### Task 4: Safe Filesystem Evidence Loading

**Files:**
- Create: `workers/analysis_worker/evidence.py`
- Create: `workers/analysis_worker/tests/test_evidence.py`

- [ ] **Step 1: Write evidence tests first**

Create `workers/analysis_worker/tests/test_evidence.py`:

```python
from pathlib import Path

import pytest

from evidence import FileEvidenceStore


def test_file_evidence_store_reads_ref_under_root(tmp_path: Path):
    evidence_path = tmp_path / "raw" / "2026" / "04" / "28" / "trace_1"
    evidence_path.mkdir(parents=True)
    (evidence_path / "request_body.bin").write_text('{"model":"gpt-4.1"}', encoding="utf-8")

    store = FileEvidenceStore(tmp_path)

    assert store.read_text("raw/2026/04/28/trace_1/request_body.bin") == '{"model":"gpt-4.1"}'


def test_file_evidence_store_rejects_path_escape(tmp_path: Path):
    store = FileEvidenceStore(tmp_path)

    with pytest.raises(ValueError, match="invalid object ref"):
        store.read_text("../secrets.env")
```

- [ ] **Step 2: Run evidence tests and verify failure**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_evidence.py -q
```

Expected: FAIL because `evidence.py` is not available yet.

- [ ] **Step 3: Create evidence reader**

Create `workers/analysis_worker/evidence.py`:

```python
from pathlib import Path


class FileEvidenceStore:
    def __init__(self, root: str | Path):
        self.root = Path(root).resolve()

    def read_text(self, object_ref: str) -> str:
        path = self._path_for_ref(object_ref)
        return path.read_text(encoding="utf-8")

    def _path_for_ref(self, object_ref: str) -> Path:
        if not object_ref:
            raise ValueError("object ref is empty")
        if "\\" in object_ref or "//" in object_ref or ".." in object_ref:
            raise ValueError(f"invalid object ref {object_ref!r}")
        ref_path = Path(object_ref)
        if ref_path.is_absolute():
            raise ValueError(f"invalid object ref {object_ref!r}")
        candidate = (self.root / ref_path).resolve()
        if candidate != self.root and self.root not in candidate.parents:
            raise ValueError(f"object ref escapes evidence root {object_ref!r}")
        return candidate
```

- [ ] **Step 4: Run evidence tests and verify pass**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_evidence.py -q
```

Expected: PASS with `2 passed`.

- [ ] **Step 5: Commit**

```bash
git add workers/analysis_worker/evidence.py workers/analysis_worker/tests/test_evidence.py
git commit -m "feat: add safe evidence reader"
```

---

### Task 5: JSON Protocol Normalizers

**Files:**
- Create: `workers/analysis_worker/normalizers.py`
- Create: `workers/analysis_worker/tests/test_normalizers.py`

- [ ] **Step 1: Write normalizer tests first**

Create `workers/analysis_worker/tests/test_normalizers.py`:

```python
import json

from models import TraceCapturedJob
from normalizers import normalize_json_trace


def job(protocol_family: str, route_pattern: str = "/v1/chat/completions") -> TraceCapturedJob:
    return TraceCapturedJob(
        type="trace_captured",
        trace_id="trace_1",
        route_pattern=route_pattern,
        protocol_family=protocol_family,
        capture_mode="raw_and_normalized",
        employee_no="E10001",
        model_requested="gpt-4.1",
        usage_prompt_tokens=11,
        usage_completion_tokens=7,
        usage_total_tokens=18,
    )


def test_openai_chat_messages_are_normalized():
    request_body = json.dumps({
        "model": "gpt-4.1",
        "messages": [
            {"role": "system", "content": "You are helpful."},
            {"role": "user", "content": "Summarize the incident."}
        ]
    })
    response_body = json.dumps({
        "choices": [
            {"message": {"role": "assistant", "content": "The incident was resolved."}}
        ],
        "usage": {"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18}
    })

    messages, results = normalize_json_trace(job("openai_chat"), request_body, response_body)

    assert [message.role for message in messages] == ["system", "user", "assistant"]
    assert messages[1].direction == "request"
    assert messages[2].direction == "response"
    assert messages[2].content_text == "The incident was resolved."
    assert results[0].category == "usage_extraction"
    assert results[0].label == "usage_from_gateway_job"


def test_claude_messages_are_normalized():
    request_body = json.dumps({
        "model": "claude-3-5-sonnet",
        "messages": [
            {"role": "user", "content": [{"type": "text", "text": "Review this diff."}]}
        ]
    })
    response_body = json.dumps({
        "content": [{"type": "text", "text": "The diff is safe."}],
        "usage": {"input_tokens": 5, "output_tokens": 4}
    })

    messages, _ = normalize_json_trace(job("claude_messages"), request_body, response_body)

    assert len(messages) == 2
    assert messages[0].content_text == "Review this diff."
    assert messages[1].role == "assistant"
    assert messages[1].content_text == "The diff is safe."


def test_generic_json_prompt_is_used_for_images():
    request_body = json.dumps({"model": "gpt-image-1", "prompt": "Draw the launch diagram"})
    response_body = json.dumps({"created": 1777366800})

    messages, _ = normalize_json_trace(job("openai_images", "/v1/images/generations"), request_body, response_body)

    assert len(messages) == 1
    assert messages[0].role == "user"
    assert messages[0].content_text == "Draw the launch diagram"
    assert messages[0].protocol_item_type == "generic_prompt"
```

- [ ] **Step 2: Run normalizer tests and verify failure**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_normalizers.py -q
```

Expected: FAIL because `normalizers.py` is not available yet.

- [ ] **Step 3: Create normalizer implementation**

Create `workers/analysis_worker/normalizers.py`:

```python
import json
from typing import Any

from models import AnalysisResult, NormalizedMessage, TraceCapturedJob, text_hash


ANALYZER_VERSION = "normalizer_mvp_2026_04_28"


def normalize_json_trace(
    job: TraceCapturedJob,
    request_body: str,
    response_body: str,
) -> tuple[list[NormalizedMessage], list[AnalysisResult]]:
    request_json = _load_json_object(request_body)
    response_json = _load_json_object(response_body)
    messages: list[NormalizedMessage]
    if job.protocol_family == "openai_chat":
        messages = _normalize_openai_chat(job, request_json, response_json)
    elif job.protocol_family == "openai_responses":
        messages = _normalize_openai_responses(job, request_json, response_json)
    elif job.protocol_family == "claude_messages":
        messages = _normalize_claude_messages(job, request_json, response_json)
    else:
        messages = _normalize_generic_prompt(job, request_json)
    return messages, [_usage_result(job)]


def _load_json_object(body: str) -> dict[str, Any]:
    if not body:
        return {}
    try:
        loaded = json.loads(body)
    except json.JSONDecodeError:
        return {}
    return loaded if isinstance(loaded, dict) else {}


def _normalize_openai_chat(
    job: TraceCapturedJob,
    request_json: dict[str, Any],
    response_json: dict[str, Any],
) -> list[NormalizedMessage]:
    messages: list[NormalizedMessage] = []
    for index, item in enumerate(request_json.get("messages", [])):
        if not isinstance(item, dict):
            continue
        text = _content_to_text(item.get("content"))
        if text:
            messages.append(_message(job, "request", len(messages), str(item.get("role", "")), text, f"request.messages[{index}]", "openai_chat_message"))
    for index, choice in enumerate(response_json.get("choices", [])):
        if not isinstance(choice, dict):
            continue
        message = choice.get("message")
        if not isinstance(message, dict):
            continue
        text = _content_to_text(message.get("content"))
        if text:
            messages.append(_message(job, "response", len(messages), str(message.get("role", "assistant")), text, f"response.choices[{index}].message", "openai_chat_message"))
    return messages


def _normalize_openai_responses(
    job: TraceCapturedJob,
    request_json: dict[str, Any],
    response_json: dict[str, Any],
) -> list[NormalizedMessage]:
    messages: list[NormalizedMessage] = []
    request_text = _content_to_text(request_json.get("input"))
    if request_text:
        messages.append(_message(job, "request", 0, "user", request_text, "request.input", "openai_responses_input"))
    output = response_json.get("output")
    if isinstance(output, list):
        for index, item in enumerate(output):
            text = _content_to_text(item)
            if text:
                messages.append(_message(job, "response", len(messages), "assistant", text, f"response.output[{index}]", "openai_responses_output"))
    return messages


def _normalize_claude_messages(
    job: TraceCapturedJob,
    request_json: dict[str, Any],
    response_json: dict[str, Any],
) -> list[NormalizedMessage]:
    messages: list[NormalizedMessage] = []
    for index, item in enumerate(request_json.get("messages", [])):
        if not isinstance(item, dict):
            continue
        text = _content_to_text(item.get("content"))
        if text:
            messages.append(_message(job, "request", len(messages), str(item.get("role", "")), text, f"request.messages[{index}]", "claude_message"))
    response_text = _content_to_text(response_json.get("content"))
    if response_text:
        messages.append(_message(job, "response", len(messages), "assistant", response_text, "response.content", "claude_message"))
    return messages


def _normalize_generic_prompt(job: TraceCapturedJob, request_json: dict[str, Any]) -> list[NormalizedMessage]:
    for key in ("prompt", "input", "text", "query"):
        text = _content_to_text(request_json.get(key))
        if text:
            return [_message(job, "request", 0, "user", text, f"request.{key}", "generic_prompt")]
    return []


def _content_to_text(value: Any) -> str:
    if value is None:
        return ""
    if isinstance(value, str):
        return value
    if isinstance(value, list):
        parts = [_content_to_text(item) for item in value]
        return "\n".join(part for part in parts if part)
    if isinstance(value, dict):
        if isinstance(value.get("text"), str):
            return value["text"]
        if isinstance(value.get("content"), str):
            return value["content"]
        if isinstance(value.get("type"), str) and value["type"] == "output_text" and isinstance(value.get("text"), str):
            return value["text"]
    return ""


def _message(
    job: TraceCapturedJob,
    direction: str,
    sequence_index: int,
    role: str,
    content_text: str,
    source_path: str,
    protocol_item_type: str,
) -> NormalizedMessage:
    return NormalizedMessage(
        trace_id=job.trace_id,
        direction=direction,
        sequence_index=sequence_index,
        role=role,
        modality="text",
        content_text=content_text,
        content_text_hash=text_hash(content_text),
        media_url="",
        source_path=source_path,
        protocol_item_type=protocol_item_type,
        token_count_estimate=max(1, len(content_text.split())),
        metadata={"route_pattern": job.route_pattern, "protocol_family": job.protocol_family},
    )


def _usage_result(job: TraceCapturedJob) -> AnalysisResult:
    label = "usage_from_gateway_job" if job.usage_total_tokens > 0 else "usage_not_available"
    confidence = 1.0 if job.usage_total_tokens > 0 else 0.0
    return AnalysisResult(
        trace_id=job.trace_id,
        analyzer_name="usage_extraction",
        analyzer_version=ANALYZER_VERSION,
        policy_version="",
        category="usage_extraction",
        label=label,
        score=float(job.usage_total_tokens),
        confidence=confidence,
        severity="",
        result={
            "prompt_tokens": job.usage_prompt_tokens,
            "completion_tokens": job.usage_completion_tokens,
            "total_tokens": job.usage_total_tokens,
            "reasoning_tokens": job.usage_reasoning_tokens,
            "cached_tokens": job.usage_cached_tokens,
        },
    )
```

- [ ] **Step 4: Run normalizer tests and verify pass**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_normalizers.py -q
```

Expected: PASS with `3 passed`.

- [ ] **Step 5: Commit**

```bash
git add workers/analysis_worker/normalizers.py workers/analysis_worker/tests/test_normalizers.py
git commit -m "feat: normalize captured JSON traces"
```

---

### Task 6: Repository Persistence and Usage Aggregates

**Files:**
- Create: `workers/analysis_worker/repository.py`
- Create: `workers/analysis_worker/tests/test_repository.py`

- [ ] **Step 1: Write repository tests first**

Create `workers/analysis_worker/tests/test_repository.py`:

```python
from models import AnalysisResult, NormalizedMessage, UsageAggregateDelta
from repository import PostgresAnalysisRepository


class FakeCursor:
    def __init__(self):
        self.executed = []

    def execute(self, query, params):
        self.executed.append((query, params))


class FakeConnection:
    def __init__(self):
        self.cursor_obj = FakeCursor()
        self.committed = False

    def cursor(self):
        return self.cursor_obj

    def commit(self):
        self.committed = True


def test_repository_inserts_messages_results_and_aggregate():
    conn = FakeConnection()
    repo = PostgresAnalysisRepository(conn)
    message = NormalizedMessage(
        trace_id="trace_1",
        direction="request",
        sequence_index=0,
        role="user",
        modality="text",
        content_text="Summarize incident",
        content_text_hash="abc",
        media_url="",
        source_path="request.messages[0]",
        protocol_item_type="openai_chat_message",
        token_count_estimate=2,
        metadata={"protocol_family": "openai_chat"},
    )
    result = AnalysisResult(
        trace_id="trace_1",
        analyzer_name="usage_extraction",
        analyzer_version="normalizer_mvp_2026_04_28",
        policy_version="",
        category="usage_extraction",
        label="usage_from_gateway_job",
        score=18,
        confidence=1.0,
        severity="",
        result={"total_tokens": 18},
    )
    aggregate = UsageAggregateDelta(
        bucket_start="2026-04-28T13:00:00+00:00",
        bucket_size="hour",
        token_fingerprint="tkfp_raw",
        new_api_token_id=42,
        employee_no="E10001",
        token_name_snapshot="E10001",
        model="gpt-4.1",
        route_pattern="/v1/chat/completions",
        protocol_family="openai_chat",
        request_count=1,
        success_count=1,
        error_count=0,
        stream_count=0,
        prompt_tokens=11,
        completion_tokens=7,
        total_tokens=18,
        reasoning_tokens=2,
        cached_tokens=3,
        request_body_bytes=0,
        response_body_bytes=0,
    )

    repo.save_trace_analysis([message], [result], [aggregate])

    queries = "\n".join(query for query, _ in conn.cursor_obj.executed)
    assert "INSERT INTO normalized_messages" in queries
    assert "INSERT INTO analysis_results" in queries
    assert "INSERT INTO usage_aggregates" in queries
    assert "ON CONFLICT" in queries
    assert conn.committed is True
```

- [ ] **Step 2: Run repository tests and verify failure**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_repository.py -q
```

Expected: FAIL because `repository.py` is not available yet.

- [ ] **Step 3: Create repository implementation**

Create `workers/analysis_worker/repository.py`:

```python
import json
from typing import Iterable

from models import AnalysisResult, NormalizedMessage, UsageAggregateDelta


class PostgresAnalysisRepository:
    def __init__(self, connection):
        self.connection = connection

    def save_trace_analysis(
        self,
        messages: Iterable[NormalizedMessage],
        results: Iterable[AnalysisResult],
        aggregates: Iterable[UsageAggregateDelta],
    ) -> None:
        cursor = self.connection.cursor()
        for message in messages:
            cursor.execute(
                """
                INSERT INTO normalized_messages (
                    trace_id, direction, sequence_index, role, modality,
                    content_text, content_text_hash, media_url, source_path,
                    protocol_item_type, token_count_estimate, metadata_json
                ) VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s::jsonb)
                ON CONFLICT (trace_id, direction, sequence_index, source_path)
                DO UPDATE SET
                    role = EXCLUDED.role,
                    modality = EXCLUDED.modality,
                    content_text = EXCLUDED.content_text,
                    content_text_hash = EXCLUDED.content_text_hash,
                    media_url = EXCLUDED.media_url,
                    protocol_item_type = EXCLUDED.protocol_item_type,
                    token_count_estimate = EXCLUDED.token_count_estimate,
                    metadata_json = EXCLUDED.metadata_json
                """,
                (
                    message.trace_id,
                    message.direction,
                    message.sequence_index,
                    message.role,
                    message.modality,
                    message.content_text,
                    message.content_text_hash,
                    message.media_url,
                    message.source_path,
                    message.protocol_item_type,
                    message.token_count_estimate,
                    json.dumps(message.metadata, sort_keys=True),
                ),
            )
        for result in results:
            cursor.execute(
                """
                INSERT INTO analysis_results (
                    trace_id, analyzer_name, analyzer_version, policy_version,
                    category, label, score, confidence, severity, result_json
                ) VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s::jsonb)
                """,
                (
                    result.trace_id,
                    result.analyzer_name,
                    result.analyzer_version,
                    result.policy_version,
                    result.category,
                    result.label,
                    result.score,
                    result.confidence,
                    result.severity,
                    json.dumps(result.result, sort_keys=True),
                ),
            )
        for aggregate in aggregates:
            cursor.execute(
                """
                INSERT INTO usage_aggregates (
                    bucket_start, bucket_size, token_fingerprint, new_api_token_id,
                    employee_no, token_name_snapshot, model, route_pattern, protocol_family,
                    request_count, success_count, error_count, stream_count,
                    prompt_tokens, completion_tokens, total_tokens, reasoning_tokens, cached_tokens,
                    request_body_bytes, response_body_bytes
                ) VALUES (
                    %s,%s,%s,%s,%s,%s,%s,%s,%s,
                    %s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s
                )
                ON CONFLICT (
                    bucket_start, bucket_size, token_fingerprint, employee_no,
                    model, route_pattern, protocol_family
                ) DO UPDATE SET
                    request_count = usage_aggregates.request_count + EXCLUDED.request_count,
                    success_count = usage_aggregates.success_count + EXCLUDED.success_count,
                    error_count = usage_aggregates.error_count + EXCLUDED.error_count,
                    stream_count = usage_aggregates.stream_count + EXCLUDED.stream_count,
                    prompt_tokens = usage_aggregates.prompt_tokens + EXCLUDED.prompt_tokens,
                    completion_tokens = usage_aggregates.completion_tokens + EXCLUDED.completion_tokens,
                    total_tokens = usage_aggregates.total_tokens + EXCLUDED.total_tokens,
                    reasoning_tokens = usage_aggregates.reasoning_tokens + EXCLUDED.reasoning_tokens,
                    cached_tokens = usage_aggregates.cached_tokens + EXCLUDED.cached_tokens,
                    request_body_bytes = usage_aggregates.request_body_bytes + EXCLUDED.request_body_bytes,
                    response_body_bytes = usage_aggregates.response_body_bytes + EXCLUDED.response_body_bytes,
                    updated_at = now()
                """,
                (
                    aggregate.bucket_start,
                    aggregate.bucket_size,
                    aggregate.token_fingerprint,
                    aggregate.new_api_token_id,
                    aggregate.employee_no,
                    aggregate.token_name_snapshot,
                    aggregate.model,
                    aggregate.route_pattern,
                    aggregate.protocol_family,
                    aggregate.request_count,
                    aggregate.success_count,
                    aggregate.error_count,
                    aggregate.stream_count,
                    aggregate.prompt_tokens,
                    aggregate.completion_tokens,
                    aggregate.total_tokens,
                    aggregate.reasoning_tokens,
                    aggregate.cached_tokens,
                    aggregate.request_body_bytes,
                    aggregate.response_body_bytes,
                ),
            )
        self.connection.commit()
```

- [ ] **Step 4: Run repository tests and verify pass**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_repository.py -q
```

Expected: PASS with `1 passed`.

- [ ] **Step 5: Commit**

```bash
git add workers/analysis_worker/repository.py workers/analysis_worker/tests/test_repository.py
git commit -m "feat: persist normalized analysis outputs"
```

---

### Task 7: Worker Pipeline and Redis Execution

**Files:**
- Modify: `workers/analysis_worker/main.py`
- Create: `workers/analysis_worker/tests/test_pipeline.py`

- [ ] **Step 1: Write pipeline tests first**

Create `workers/analysis_worker/tests/test_pipeline.py`:

```python
import json
from pathlib import Path

from evidence import FileEvidenceStore
from main import process_job_line


class RecordingRepository:
    def __init__(self):
        self.messages = []
        self.results = []
        self.aggregates = []

    def save_trace_analysis(self, messages, results, aggregates):
        self.messages.extend(messages)
        self.results.extend(results)
        self.aggregates.extend(aggregates)


def test_process_job_line_reads_evidence_normalizes_and_persists(tmp_path: Path):
    evidence_dir = tmp_path / "raw" / "2026" / "04" / "28" / "trace_1"
    evidence_dir.mkdir(parents=True)
    (evidence_dir / "request_body.bin").write_text(json.dumps({
        "model": "gpt-4.1",
        "messages": [{"role": "user", "content": "Summarize incident"}]
    }), encoding="utf-8")
    (evidence_dir / "response_body.bin").write_text(json.dumps({
        "choices": [{"message": {"role": "assistant", "content": "Incident resolved"}}],
        "usage": {"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18}
    }), encoding="utf-8")
    repo = RecordingRepository()
    line = json.dumps({
        "type": "trace_captured",
        "trace_id": "trace_1",
        "route_pattern": "/v1/chat/completions",
        "protocol_family": "openai_chat",
        "capture_mode": "raw_and_normalized",
        "employee_no": "E10001",
        "request_raw_ref": "raw/2026/04/28/trace_1/request_body.bin",
        "response_raw_ref": "raw/2026/04/28/trace_1/response_body.bin",
        "model_requested": "gpt-4.1",
        "usage_prompt_tokens": 11,
        "usage_completion_tokens": 7,
        "usage_total_tokens": 18,
        "usage_reasoning_tokens": 2,
        "usage_cached_tokens": 3,
        "token_fingerprint": "tkfp_raw",
        "fingerprint_display": "tkfp_display",
        "new_api_token_id": 42,
        "token_name_snapshot": "E10001",
        "status_code": 200,
        "upstream_status_code": 200,
        "stream": False,
        "request_started_at": "2026-04-28T13:45:22Z",
        "request_body_size": 128,
        "response_body_size": 256
    })

    response = process_job_line(line, FileEvidenceStore(tmp_path), repo)

    assert response["accepted_trace_id"] == "trace_1"
    assert response["normalized_message_count"] == 2
    assert response["analysis_result_count"] == 1
    assert len(repo.messages) == 2
    assert len(repo.results) == 1
    assert [aggregate.bucket_size for aggregate in repo.aggregates] == ["hour", "day"]
    assert repo.aggregates[0].total_tokens == 18
    assert repo.aggregates[0].request_body_bytes == 128
    assert repo.aggregates[0].response_body_bytes == 256
```

- [ ] **Step 2: Run pipeline tests and verify failure**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_pipeline.py -q
```

Expected: FAIL because `process_job_line` is not available in `main.py`.

- [ ] **Step 3: Replace worker entrypoint**

Replace `workers/analysis_worker/main.py` with:

```python
import argparse
import json
import os
import sys

import psycopg
import redis

from evidence import FileEvidenceStore
from models import TraceCapturedJob, UsageAggregateDelta, bucket_start_day, bucket_start_hour, parse_job
from normalizers import normalize_json_trace
from repository import PostgresAnalysisRepository


def aggregate_deltas(job: TraceCapturedJob) -> list[UsageAggregateDelta]:
    success = 1 if 200 <= job.status_code < 400 or job.status_code == 0 else 0
    error = 0 if success else 1
    common = {
        "token_fingerprint": job.token_fingerprint,
        "new_api_token_id": job.new_api_token_id,
        "employee_no": job.employee_no,
        "token_name_snapshot": job.token_name_snapshot,
        "model": job.model_requested,
        "route_pattern": job.route_pattern,
        "protocol_family": job.protocol_family,
        "request_count": 1,
        "success_count": success,
        "error_count": error,
        "stream_count": 1 if job.stream else 0,
        "prompt_tokens": job.usage_prompt_tokens,
        "completion_tokens": job.usage_completion_tokens,
        "total_tokens": job.usage_total_tokens,
        "reasoning_tokens": job.usage_reasoning_tokens,
        "cached_tokens": job.usage_cached_tokens,
        "request_body_bytes": job.request_body_size,
        "response_body_bytes": job.response_body_size,
    }
    return [
        UsageAggregateDelta(bucket_start=bucket_start_hour(job.request_started_at), bucket_size="hour", **common),
        UsageAggregateDelta(bucket_start=bucket_start_day(job.request_started_at), bucket_size="day", **common),
    ]


def process_job_line(line: str, evidence_store: FileEvidenceStore, repository) -> dict:
    job = parse_job(line)
    request_body = evidence_store.read_text(job.request_raw_ref) if job.request_raw_ref else ""
    response_body = evidence_store.read_text(job.response_raw_ref) if job.response_raw_ref else ""
    messages, results = normalize_json_trace(job, request_body, response_body)
    aggregates = aggregate_deltas(job)
    repository.save_trace_analysis(messages, results, aggregates)
    return {
        "accepted_trace_id": job.trace_id,
        "worker_status": "processed",
        "normalized_message_count": len(messages),
        "analysis_result_count": len(results),
        "aggregate_count": len(aggregates),
        "usage_total_tokens": job.usage_total_tokens,
    }


def process_stdin(evidence_root: str, postgres_dsn: str) -> int:
    payload = sys.stdin.read().strip()
    if not payload:
        return 0
    with psycopg.connect(postgres_dsn) as connection:
        result = process_job_line(payload, FileEvidenceStore(evidence_root), PostgresAnalysisRepository(connection))
    print(json.dumps(result, sort_keys=True))
    return 0


def process_redis_once(redis_url: str, list_name: str, evidence_root: str, postgres_dsn: str, timeout_seconds: int) -> int:
    client = redis.Redis.from_url(redis_url, decode_responses=True)
    item = client.blpop(list_name, timeout=timeout_seconds)
    if item is None:
        print(json.dumps({"worker_status": "idle", "list": list_name}, sort_keys=True))
        return 0
    _, payload = item
    with psycopg.connect(postgres_dsn) as connection:
        result = process_job_line(payload, FileEvidenceStore(evidence_root), PostgresAnalysisRepository(connection))
    print(json.dumps(result, sort_keys=True))
    return 0


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--redis-once", action="store_true")
    parser.add_argument("--redis-url", default=os.environ.get("REDIS_URL", "redis://localhost:6379/0"))
    parser.add_argument("--redis-list", default=os.environ.get("ANALYSIS_REDIS_LIST", "analysis_jobs"))
    parser.add_argument("--redis-timeout-seconds", type=int, default=5)
    parser.add_argument("--evidence-root", default=os.environ.get("EVIDENCE_STORAGE_DIR", ""))
    parser.add_argument("--postgres-dsn", default=os.environ.get("POSTGRES_DSN", ""))
    args = parser.parse_args()
    if not args.evidence_root:
        raise SystemExit("EVIDENCE_STORAGE_DIR or --evidence-root is required")
    if not args.postgres_dsn:
        raise SystemExit("POSTGRES_DSN or --postgres-dsn is required")
    if args.redis_once:
        return process_redis_once(
            args.redis_url,
            args.redis_list,
            args.evidence_root,
            args.postgres_dsn,
            args.redis_timeout_seconds,
        )
    return process_stdin(args.evidence_root, args.postgres_dsn)


if __name__ == "__main__":
    raise SystemExit(main())
```

- [ ] **Step 4: Run pipeline tests and verify pass**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_pipeline.py -q
```

Expected: PASS with `1 passed`.

- [ ] **Step 5: Run full worker tests**

Run:

```bash
cd workers/analysis_worker && uv run pytest -q
```

Expected: PASS for all worker tests.

- [ ] **Step 6: Commit**

```bash
git add workers/analysis_worker/main.py workers/analysis_worker/tests/test_pipeline.py
git commit -m "feat: process analysis jobs into persisted outputs"
```

---

### Task 8: Documentation and End-to-End Checks

**Files:**
- Modify: `docs/development.md`

- [ ] **Step 1: Update development docs**

Append this section to `docs/development.md`:

````markdown

## Analysis Persistence

Apply migrations through your local migration runner before processing analysis jobs. The worker now writes:

- `normalized_messages` for extracted request and response text.
- `analysis_results` for deterministic usage extraction status.
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
uv run python main.py --redis-once
```
````

- [ ] **Step 2: Run Go and Python tests**

Run:

```bash
go test ./...
cd workers/analysis_worker && uv run pytest -q
```

Expected: Go tests PASS and Python tests PASS.

- [ ] **Step 3: Check for plaintext API-key persistence additions**

Run:

```bash
rg -n "api_key|authorization|x-api-key|x-goog-api-key|mj-api-secret" workers/analysis_worker migrations/0003_analysis_normalization_usage.sql
```

Expected: no matches. The analysis worker should consume only job metadata and evidence refs; it should not add API-key fields.

- [ ] **Step 4: Commit**

```bash
git add docs/development.md
git commit -m "docs: describe analysis persistence worker"
```

---

## Self-Review

**Spec coverage:** This plan covers the unimplemented async analysis slice for protocol normalization, usage extraction persistence, and usage aggregation. It maps to the design sections for `normalized_messages`, `analysis_results`, `usage_aggregates`, protocol normalization, usage extraction, and worker queue processing.

**Deferred requirements:** Rule anomalies, work relevance, context catalog, media snapshots, Admin API, Web UI, RBAC, raw access audit logs, API key lookup product flow, metrics, health checks, degraded spooling, and retention remain separate subsystems and should receive their own plans.

**Placeholder scan:** The plan contains concrete file paths, commands, expected outcomes, and code blocks for every code-writing step.

**Type consistency:** `TraceCapturedJob` feeds `normalize_json_trace`, `aggregate_deltas`, and `process_job_line`. `NormalizedMessage`, `AnalysisResult`, and `UsageAggregateDelta` are the only persistence objects passed to `PostgresAnalysisRepository.save_trace_analysis`.
