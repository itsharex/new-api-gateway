# Worker Anomaly and Coverage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist review-ready rule anomalies and worker-generated coverage alerts from each captured trace analysis job.

**Architecture:** Keep the Go gateway responsible for synchronous proxying, evidence capture, identity resolution, and immediate route coverage alerts. Extend the Python worker so the same `trace_captured` job also produces deterministic anomaly rows and normalization coverage rows after evidence normalization and usage aggregation complete.

**Tech Stack:** Python 3.11 dataclasses, pytest, PostgreSQL via `psycopg`, existing Redis queue worker, existing filesystem evidence loader, SQL migrations.

---

## Completed Context Check

The approved design in `docs/superpowers/specs/2026-04-25-new-api-gateway-audit-design.md` requires explainable anomaly rules, unsupported-content coverage alerts, reviewer workflow data, and sample traces.

Existing implemented slices already provide the inputs this plan needs:

- `migrations/0001_core_schema.sql` has `traces`, `raw_evidence_objects`, `token_identity_cache`, and a minimal `coverage_alerts` table.
- `migrations/0003_analysis_normalization_usage.sql` has `normalized_messages`, `analysis_results`, and `usage_aggregates`.
- `workers/analysis_worker/main.py` parses `trace_captured`, loads evidence, normalizes JSON routes, writes analysis results, and upserts hourly/daily usage aggregates.
- `workers/analysis_worker/repository.py` already uses fake-connection tests, so new persistence should follow that style instead of requiring a live PostgreSQL server.

This plan intentionally does not implement Admin API, Web UI, RBAC, API key lookup UI, media snapshot downloading, embeddings, or statistical baselines. Those should remain follow-on plans after anomalies and alerts are queryable.

## File Structure

- Create `migrations/0004_worker_anomaly_coverage.sql`: add `usage_anomalies`, `anomaly_rules`, and enrich `coverage_alerts` with design fields missing from the core schema.
- Modify `workers/analysis_worker/models.py`: add `AnomalyAlert` and `CoverageAlert` dataclasses plus stable ID helpers.
- Create `workers/analysis_worker/tests/test_rules.py`: unit tests for deterministic rules and coverage alert generation.
- Create `workers/analysis_worker/rules.py`: rule-based detectors for identity issues, high token count, raw-only high volume, retry storms, and normalization gaps.
- Modify `workers/analysis_worker/repository.py`: persist anomaly alerts and upsert coverage alerts alongside messages/results/aggregates.
- Modify `workers/analysis_worker/tests/test_repository.py`: assert repository SQL writes the new tables and upserts coverage alerts.
- Modify `workers/analysis_worker/main.py`: run rules after normalization and include counts in worker status output.
- Modify `workers/analysis_worker/tests/test_pipeline.py`: assert the end-to-end in-process pipeline stores anomalies and coverage alerts.
- Modify `workers/analysis_worker/contract_example.json`: include fields that trigger no anomalies by default and document the available rule inputs.
- Modify `docs/development.md`: document anomaly and coverage outputs plus the local pytest command.

---

### Task 1: Anomaly and Coverage Schema

**Files:**
- Create: `migrations/0004_worker_anomaly_coverage.sql`

- [ ] **Step 1: Write the migration**

Create `migrations/0004_worker_anomaly_coverage.sql`:

```sql
CREATE TABLE IF NOT EXISTS usage_anomalies (
    id BIGSERIAL PRIMARY KEY,
    anomaly_id TEXT NOT NULL UNIQUE,
    anomaly_type TEXT NOT NULL,
    severity TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'open',
    token_fingerprint TEXT NOT NULL DEFAULT '',
    fingerprint_display TEXT NOT NULL DEFAULT '',
    new_api_token_id INTEGER NOT NULL DEFAULT 0,
    employee_no TEXT NOT NULL DEFAULT '',
    token_name_snapshot TEXT NOT NULL DEFAULT '',
    window_start TIMESTAMPTZ,
    window_end TIMESTAMPTZ,
    observed_value NUMERIC NOT NULL DEFAULT 0,
    threshold_value NUMERIC NOT NULL DEFAULT 0,
    baseline_value NUMERIC,
    model TEXT NOT NULL DEFAULT '',
    route_pattern TEXT NOT NULL DEFAULT '',
    sample_trace_ids TEXT[] NOT NULL DEFAULT '{}',
    reason TEXT NOT NULL DEFAULT '',
    detector_version TEXT NOT NULL,
    reviewer_id INTEGER,
    review_note TEXT NOT NULL DEFAULT '',
    reviewed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_usage_anomalies_status_created
    ON usage_anomalies(status, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_usage_anomalies_employee_created
    ON usage_anomalies(employee_no, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_usage_anomalies_token_created
    ON usage_anomalies(token_fingerprint, created_at DESC);

CREATE TABLE IF NOT EXISTS anomaly_rules (
    id BIGSERIAL PRIMARY KEY,
    rule_key TEXT NOT NULL UNIQUE,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    scope_type TEXT NOT NULL DEFAULT 'global',
    scope_value TEXT NOT NULL DEFAULT '',
    window TEXT NOT NULL DEFAULT '',
    threshold_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    severity TEXT NOT NULL DEFAULT 'medium',
    cooldown TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO anomaly_rules (rule_key, threshold_json, severity, window)
VALUES
    ('identity_unresolved_success', '{"enabled": true}'::jsonb, 'high', 'per_trace'),
    ('invalid_employee_no', '{"enabled": true}'::jsonb, 'high', 'per_trace'),
    ('high_trace_tokens', '{"total_tokens": 20000}'::jsonb, 'medium', 'per_trace'),
    ('raw_only_large_response', '{"response_body_bytes": 1048576}'::jsonb, 'medium', 'per_trace'),
    ('retry_storm_trace', '{"status_code_min": 500}'::jsonb, 'medium', 'per_trace')
ON CONFLICT (rule_key) DO NOTHING;

ALTER TABLE coverage_alerts
    ADD COLUMN IF NOT EXISTS payload_shape_hash TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS normalizer TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS normalizer_version TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS affected_trace_count BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS affected_token_count BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS affected_employee_count BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS owner_note TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_coverage_alerts_status_last_seen
    ON coverage_alerts(status, last_seen_at DESC);
```

- [ ] **Step 2: Verify the migration has all expected objects**

Run:

```bash
rg -n "CREATE TABLE IF NOT EXISTS (usage_anomalies|anomaly_rules)|ALTER TABLE coverage_alerts" migrations/0004_worker_anomaly_coverage.sql
```

Expected: three matches, one for each schema section.

- [ ] **Step 3: Commit**

```bash
git add migrations/0004_worker_anomaly_coverage.sql
git commit -m "feat: add anomaly and coverage schema"
```

---

### Task 2: Worker Models for Review Outputs

**Files:**
- Modify: `workers/analysis_worker/models.py`
- Test: `workers/analysis_worker/tests/test_models.py`

- [ ] **Step 1: Add model tests first**

Append these tests to `workers/analysis_worker/tests/test_models.py`:

```python
from models import anomaly_id, coverage_alert_id


def test_anomaly_id_is_stable_for_same_rule_and_trace():
    first = anomaly_id("high_trace_tokens", "trace_123", "E10001")
    second = anomaly_id("high_trace_tokens", "trace_123", "E10001")

    assert first == second
    assert first.startswith("anom_high_trace_tokens_")


def test_coverage_alert_id_groups_by_alert_route_and_shape():
    first = coverage_alert_id("normalization_gap", "/v1/chat/completions", "abc123")
    second = coverage_alert_id("normalization_gap", "/v1/chat/completions", "abc123")
    other = coverage_alert_id("normalization_gap", "/v1/responses", "abc123")

    assert first == second
    assert first != other
    assert first.startswith("cov_normalization_gap_")
```

- [ ] **Step 2: Run model tests and verify failure**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_models.py -q
```

Expected: FAIL because `anomaly_id` and `coverage_alert_id` are not defined.

- [ ] **Step 3: Add dataclasses and ID helpers**

Modify `workers/analysis_worker/models.py` by adding imports and classes after `UsageAggregateDelta`.

Add `timedelta` to the existing datetime import:

```python
from datetime import datetime, timedelta, timezone
```

Add these models and helpers:

```python
@dataclass(frozen=True)
class AnomalyAlert:
    anomaly_id: str
    anomaly_type: str
    severity: str
    token_fingerprint: str
    fingerprint_display: str
    new_api_token_id: int
    employee_no: str
    token_name_snapshot: str
    window_start: str
    window_end: str
    observed_value: float
    threshold_value: float
    baseline_value: float | None
    model: str
    route_pattern: str
    sample_trace_ids: list[str]
    reason: str
    detector_version: str


@dataclass(frozen=True)
class CoverageAlert:
    alert_id: str
    alert_code: str
    severity: str
    method: str
    route_pattern: str
    raw_path: str
    content_type: str
    protocol_family: str
    payload_shape_hash: str
    normalizer: str
    normalizer_version: str
    sample_trace_ids: list[str]
    message: str
    affected_trace_count: int = 1
    affected_token_count: int = 0
    affected_employee_count: int = 0


def stable_suffix(*parts: str) -> str:
    joined = "\x00".join(parts)
    return sha256(joined.encode("utf-8")).hexdigest()[:16]


def anomaly_id(rule_key: str, trace_id: str, employee_no: str) -> str:
    return f"anom_{rule_key}_{stable_suffix(rule_key, trace_id, employee_no)}"


def coverage_alert_id(alert_code: str, route_pattern: str, payload_shape_hash: str) -> str:
    return f"cov_{alert_code}_{stable_suffix(alert_code, route_pattern, payload_shape_hash)}"


def window_end_from_start(value: str, seconds: int = 60) -> str:
    if not value:
        return datetime.now(timezone.utc).isoformat()
    parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    return (parsed.astimezone(timezone.utc) + timedelta(seconds=seconds)).isoformat()
```

- [ ] **Step 4: Run model tests and verify pass**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_models.py -q
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/analysis_worker/models.py workers/analysis_worker/tests/test_models.py
git commit -m "feat: add worker anomaly coverage models"
```

---

### Task 3: Deterministic Rule Engine

**Files:**
- Create: `workers/analysis_worker/rules.py`
- Create: `workers/analysis_worker/tests/test_rules.py`

- [ ] **Step 1: Write rule tests first**

Create `workers/analysis_worker/tests/test_rules.py`:

```python
from models import NormalizedMessage, TraceCapturedJob
from rules import DETECTOR_VERSION, detect_anomalies, detect_coverage_alerts


def job(**overrides):
    values = {
        "type": "trace_captured",
        "trace_id": "trace_1",
        "route_pattern": "/v1/chat/completions",
        "protocol_family": "openai_chat",
        "capture_mode": "raw_and_normalized",
        "employee_no": "E10001",
        "model_requested": "gpt-4.1",
        "usage_total_tokens": 18,
        "token_fingerprint": "tkfp_raw",
        "fingerprint_display": "tkfp_display",
        "new_api_token_id": 42,
        "token_name_snapshot": "E10001",
        "status_code": 200,
        "upstream_status_code": 200,
        "request_started_at": "2026-04-28T13:45:22Z",
        "request_body_size": 128,
        "response_body_size": 256,
    }
    values.update(overrides)
    return TraceCapturedJob(**values)


def test_detects_identity_unresolved_success():
    alerts = detect_anomalies(job(employee_no="", status_code=200, upstream_status_code=200))

    assert [alert.anomaly_type for alert in alerts] == ["identity_unresolved_success"]
    assert alerts[0].severity == "high"
    assert alerts[0].observed_value == 1
    assert alerts[0].threshold_value == 0
    assert alerts[0].detector_version == DETECTOR_VERSION


def test_detects_high_trace_tokens():
    alerts = detect_anomalies(job(usage_total_tokens=25000))

    assert [alert.anomaly_type for alert in alerts] == ["high_trace_tokens"]
    assert alerts[0].observed_value == 25000
    assert alerts[0].threshold_value == 20000
    assert alerts[0].sample_trace_ids == ["trace_1"]


def test_detects_raw_only_large_response():
    alerts = detect_anomalies(job(
        capture_mode="raw_only",
        usage_total_tokens=0,
        response_body_size=2 * 1024 * 1024,
        route_pattern="/mj/*",
        protocol_family="midjourney",
    ))

    assert [alert.anomaly_type for alert in alerts] == ["raw_only_large_response"]
    assert "raw-only" in alerts[0].reason


def test_detects_normalization_gap_when_no_messages_for_normalized_route():
    alerts = detect_coverage_alerts(job(), messages=[])

    assert [alert.alert_code for alert in alerts] == ["normalization_gap"]
    assert alerts[0].severity == "high"
    assert alerts[0].route_pattern == "/v1/chat/completions"
    assert alerts[0].payload_shape_hash


def test_no_coverage_alert_when_messages_exist():
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
        metadata={},
    )

    assert detect_coverage_alerts(job(), [message]) == []
```

- [ ] **Step 2: Run rule tests and verify failure**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_rules.py -q
```

Expected: FAIL because `rules.py` does not exist.

- [ ] **Step 3: Implement rules**

Create `workers/analysis_worker/rules.py`:

```python
from models import (
    AnomalyAlert,
    CoverageAlert,
    NormalizedMessage,
    TraceCapturedJob,
    anomaly_id,
    bucket_start_hour,
    coverage_alert_id,
    stable_suffix,
    window_end_from_start,
)


DETECTOR_VERSION = "rules_mvp_2026_04_28"
NORMALIZER_VERSION = "normalizer_mvp_2026_04_28"

HIGH_TRACE_TOKEN_THRESHOLD = 20_000
RAW_ONLY_RESPONSE_BYTES_THRESHOLD = 1_048_576


def detect_anomalies(job: TraceCapturedJob) -> list[AnomalyAlert]:
    alerts: list[AnomalyAlert] = []
    if _upstream_success(job) and not job.employee_no:
        alerts.append(_anomaly(
            job,
            "identity_unresolved_success",
            "high",
            observed_value=1,
            threshold_value=0,
            reason="identity was unresolved while upstream returned a successful response",
        ))
    if job.employee_no and job.token_name_snapshot and job.employee_no != job.token_name_snapshot:
        alerts.append(_anomaly(
            job,
            "invalid_employee_no",
            "high",
            observed_value=1,
            threshold_value=0,
            reason="resolved employee number does not match the new-api token name snapshot",
        ))
    if job.usage_total_tokens > HIGH_TRACE_TOKEN_THRESHOLD:
        alerts.append(_anomaly(
            job,
            "high_trace_tokens",
            "medium",
            observed_value=job.usage_total_tokens,
            threshold_value=HIGH_TRACE_TOKEN_THRESHOLD,
            reason=f"single trace used {job.usage_total_tokens} tokens, exceeding {HIGH_TRACE_TOKEN_THRESHOLD}",
        ))
    if job.capture_mode == "raw_only" and job.response_body_size > RAW_ONLY_RESPONSE_BYTES_THRESHOLD:
        alerts.append(_anomaly(
            job,
            "raw_only_large_response",
            "medium",
            observed_value=job.response_body_size,
            threshold_value=RAW_ONLY_RESPONSE_BYTES_THRESHOLD,
            reason="raw-only route returned a large response body without deep normalization",
        ))
    if job.status_code >= 500 or job.upstream_status_code >= 500:
        alerts.append(_anomaly(
            job,
            "retry_storm_trace",
            "medium",
            observed_value=max(job.status_code, job.upstream_status_code),
            threshold_value=500,
            reason="trace returned a server error and may contribute to retry storms",
        ))
    return alerts


def detect_coverage_alerts(job: TraceCapturedJob, messages: list[NormalizedMessage]) -> list[CoverageAlert]:
    if job.capture_mode != "raw_and_normalized":
        return []
    if messages:
        return []
    shape = stable_suffix(
        job.route_pattern,
        job.protocol_family,
        job.request_content_type,
        job.response_content_type,
        str(job.request_body_size),
        str(job.response_body_size),
    )
    return [CoverageAlert(
        alert_id=coverage_alert_id("normalization_gap", job.route_pattern, shape),
        alert_code="normalization_gap",
        severity="high",
        method="POST",
        route_pattern=job.route_pattern,
        raw_path=job.route_pattern,
        content_type=job.request_content_type or job.response_content_type,
        protocol_family=job.protocol_family,
        payload_shape_hash=shape,
        normalizer=job.protocol_family,
        normalizer_version=NORMALIZER_VERSION,
        sample_trace_ids=[job.trace_id],
        message="route was marked raw_and_normalized but the worker extracted no normalized messages",
        affected_trace_count=1,
        affected_token_count=1 if job.token_fingerprint else 0,
        affected_employee_count=1 if job.employee_no else 0,
    )]


def _upstream_success(job: TraceCapturedJob) -> bool:
    status = job.upstream_status_code or job.status_code
    return 200 <= status < 400


def _anomaly(
    job: TraceCapturedJob,
    anomaly_type: str,
    severity: str,
    observed_value: float,
    threshold_value: float,
    reason: str,
) -> AnomalyAlert:
    return AnomalyAlert(
        anomaly_id=anomaly_id(anomaly_type, job.trace_id, job.employee_no),
        anomaly_type=anomaly_type,
        severity=severity,
        token_fingerprint=job.token_fingerprint,
        fingerprint_display=job.fingerprint_display,
        new_api_token_id=job.new_api_token_id,
        employee_no=job.employee_no,
        token_name_snapshot=job.token_name_snapshot,
        window_start=bucket_start_hour(job.request_started_at),
        window_end=window_end_from_start(job.request_started_at),
        observed_value=observed_value,
        threshold_value=threshold_value,
        baseline_value=None,
        model=job.model_requested,
        route_pattern=job.route_pattern,
        sample_trace_ids=[job.trace_id],
        reason=reason,
        detector_version=DETECTOR_VERSION,
    )
```

- [ ] **Step 4: Run rule tests and verify pass**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_rules.py -q
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/analysis_worker/rules.py workers/analysis_worker/tests/test_rules.py
git commit -m "feat: add deterministic worker anomaly rules"
```

---

### Task 4: Repository Persistence

**Files:**
- Modify: `workers/analysis_worker/repository.py`
- Modify: `workers/analysis_worker/tests/test_repository.py`

- [ ] **Step 1: Extend repository test first**

Modify the import in `workers/analysis_worker/tests/test_repository.py`:

```python
from models import AnalysisResult, AnomalyAlert, CoverageAlert, NormalizedMessage, UsageAggregateDelta
```

Rename the test and add anomaly/coverage fixtures:

```python
def test_repository_inserts_messages_results_aggregates_anomalies_and_coverage():
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
    anomaly = AnomalyAlert(
        anomaly_id="anom_high_trace_tokens_abc",
        anomaly_type="high_trace_tokens",
        severity="medium",
        token_fingerprint="tkfp_raw",
        fingerprint_display="tkfp_display",
        new_api_token_id=42,
        employee_no="E10001",
        token_name_snapshot="E10001",
        window_start="2026-04-28T13:00:00+00:00",
        window_end="2026-04-28T13:46:22+00:00",
        observed_value=25000,
        threshold_value=20000,
        baseline_value=None,
        model="gpt-4.1",
        route_pattern="/v1/chat/completions",
        sample_trace_ids=["trace_1"],
        reason="single trace exceeded threshold",
        detector_version="rules_mvp_2026_04_28",
    )
    coverage = CoverageAlert(
        alert_id="cov_normalization_gap_abc",
        alert_code="normalization_gap",
        severity="high",
        method="POST",
        route_pattern="/v1/chat/completions",
        raw_path="/v1/chat/completions",
        content_type="application/json",
        protocol_family="openai_chat",
        payload_shape_hash="shape123",
        normalizer="openai_chat",
        normalizer_version="normalizer_mvp_2026_04_28",
        sample_trace_ids=["trace_1"],
        message="no normalized messages",
        affected_trace_count=1,
        affected_token_count=1,
        affected_employee_count=1,
    )

    repo.save_trace_analysis([message], [result], [aggregate], [anomaly], [coverage])

    queries = "\n".join(query for query, _ in conn.cursor_obj.executed)
    assert "INSERT INTO normalized_messages" in queries
    assert "INSERT INTO analysis_results" in queries
    assert "INSERT INTO usage_aggregates" in queries
    assert "INSERT INTO usage_anomalies" in queries
    assert "INSERT INTO coverage_alerts" in queries
    assert "ON CONFLICT" in queries
    assert conn.committed is True
```

- [ ] **Step 2: Run repository test and verify failure**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_repository.py -q
```

Expected: FAIL because `save_trace_analysis` does not accept anomalies or coverage alerts.

- [ ] **Step 3: Extend repository persistence**

Modify the imports in `workers/analysis_worker/repository.py`:

```python
from models import AnalysisResult, AnomalyAlert, CoverageAlert, NormalizedMessage, UsageAggregateDelta
```

Change the method signature:

```python
def save_trace_analysis(
    self,
    messages: Iterable[NormalizedMessage],
    results: Iterable[AnalysisResult],
    aggregates: Iterable[UsageAggregateDelta],
    anomalies: Iterable[AnomalyAlert] = (),
    coverage_alerts: Iterable[CoverageAlert] = (),
) -> None:
```

Add these two loops before `self.connection.commit()`:

```python
        for anomaly in anomalies:
            cursor.execute(
                """
                INSERT INTO usage_anomalies (
                    anomaly_id, anomaly_type, severity, token_fingerprint, fingerprint_display,
                    new_api_token_id, employee_no, token_name_snapshot, window_start, window_end,
                    observed_value, threshold_value, baseline_value, model, route_pattern,
                    sample_trace_ids, reason, detector_version
                ) VALUES (
                    %s,%s,%s,%s,%s,
                    %s,%s,%s,%s,%s,
                    %s,%s,%s,%s,%s,
                    %s,%s,%s
                )
                ON CONFLICT (anomaly_id) DO UPDATE SET
                    severity = EXCLUDED.severity,
                    observed_value = EXCLUDED.observed_value,
                    threshold_value = EXCLUDED.threshold_value,
                    baseline_value = EXCLUDED.baseline_value,
                    sample_trace_ids = EXCLUDED.sample_trace_ids,
                    reason = EXCLUDED.reason,
                    updated_at = now()
                """,
                (
                    anomaly.anomaly_id,
                    anomaly.anomaly_type,
                    anomaly.severity,
                    anomaly.token_fingerprint,
                    anomaly.fingerprint_display,
                    anomaly.new_api_token_id,
                    anomaly.employee_no,
                    anomaly.token_name_snapshot,
                    anomaly.window_start,
                    anomaly.window_end,
                    anomaly.observed_value,
                    anomaly.threshold_value,
                    anomaly.baseline_value,
                    anomaly.model,
                    anomaly.route_pattern,
                    anomaly.sample_trace_ids,
                    anomaly.reason,
                    anomaly.detector_version,
                ),
            )
        for alert in coverage_alerts:
            cursor.execute(
                """
                INSERT INTO coverage_alerts (
                    alert_id, alert_code, severity, method, route_pattern, raw_path,
                    content_type, protocol_family, payload_shape_hash, normalizer,
                    normalizer_version, occurrence_count, sample_trace_ids, message,
                    affected_trace_count, affected_token_count, affected_employee_count
                ) VALUES (
                    %s,%s,%s,%s,%s,%s,
                    %s,%s,%s,%s,
                    %s,1,%s,%s,
                    %s,%s,%s
                )
                ON CONFLICT (alert_id) DO UPDATE SET
                    last_seen_at = now(),
                    occurrence_count = coverage_alerts.occurrence_count + 1,
                    sample_trace_ids = (
                        SELECT ARRAY(
                            SELECT DISTINCT unnest(coverage_alerts.sample_trace_ids || EXCLUDED.sample_trace_ids)
                        )
                    ),
                    message = EXCLUDED.message,
                    affected_trace_count = coverage_alerts.affected_trace_count + EXCLUDED.affected_trace_count,
                    affected_token_count = GREATEST(coverage_alerts.affected_token_count, EXCLUDED.affected_token_count),
                    affected_employee_count = GREATEST(coverage_alerts.affected_employee_count, EXCLUDED.affected_employee_count),
                    updated_at = now()
                """,
                (
                    alert.alert_id,
                    alert.alert_code,
                    alert.severity,
                    alert.method,
                    alert.route_pattern,
                    alert.raw_path,
                    alert.content_type,
                    alert.protocol_family,
                    alert.payload_shape_hash,
                    alert.normalizer,
                    alert.normalizer_version,
                    alert.sample_trace_ids,
                    alert.message,
                    alert.affected_trace_count,
                    alert.affected_token_count,
                    alert.affected_employee_count,
                ),
            )
```

- [ ] **Step 4: Run repository test and verify pass**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_repository.py -q
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/analysis_worker/repository.py workers/analysis_worker/tests/test_repository.py
git commit -m "feat: persist worker anomalies and coverage alerts"
```

---

### Task 5: Pipeline Integration

**Files:**
- Modify: `workers/analysis_worker/main.py`
- Modify: `workers/analysis_worker/tests/test_pipeline.py`
- Modify: `workers/analysis_worker/contract_example.json`

- [ ] **Step 1: Extend pipeline test first**

Modify `RecordingRepository` in `workers/analysis_worker/tests/test_pipeline.py`:

```python
class RecordingRepository:
    def __init__(self):
        self.messages = []
        self.results = []
        self.aggregates = []
        self.anomalies = []
        self.coverage_alerts = []

    def save_trace_analysis(self, messages, results, aggregates, anomalies=(), coverage_alerts=()):
        self.messages.extend(messages)
        self.results.extend(results)
        self.aggregates.extend(aggregates)
        self.anomalies.extend(anomalies)
        self.coverage_alerts.extend(coverage_alerts)
```

Append this test to the same file:

```python
def test_process_job_line_persists_anomaly_and_coverage_alert(tmp_path: Path):
    evidence_dir = tmp_path / "raw" / "2026" / "04" / "28" / "trace_gap"
    evidence_dir.mkdir(parents=True)
    (evidence_dir / "request_body.bin").write_text("{}", encoding="utf-8")
    (evidence_dir / "response_body.bin").write_text("{}", encoding="utf-8")
    repo = RecordingRepository()
    line = json.dumps({
        "type": "trace_captured",
        "trace_id": "trace_gap",
        "route_pattern": "/v1/chat/completions",
        "protocol_family": "openai_chat",
        "capture_mode": "raw_and_normalized",
        "employee_no": "",
        "request_raw_ref": "raw/2026/04/28/trace_gap/request_body.bin",
        "response_raw_ref": "raw/2026/04/28/trace_gap/response_body.bin",
        "request_content_type": "application/json",
        "response_content_type": "application/json",
        "model_requested": "gpt-4.1",
        "usage_total_tokens": 25001,
        "token_fingerprint": "tkfp_raw",
        "fingerprint_display": "tkfp_display",
        "new_api_token_id": 42,
        "token_name_snapshot": "",
        "status_code": 200,
        "upstream_status_code": 200,
        "stream": False,
        "request_started_at": "2026-04-28T13:45:22Z",
        "request_body_size": 2,
        "response_body_size": 2
    })

    response = process_job_line(line, FileEvidenceStore(tmp_path), repo)

    assert response["worker_status"] == "processed"
    assert response["anomaly_count"] == 2
    assert response["coverage_alert_count"] == 1
    assert [alert.anomaly_type for alert in repo.anomalies] == [
        "identity_unresolved_success",
        "high_trace_tokens",
    ]
    assert [alert.alert_code for alert in repo.coverage_alerts] == ["normalization_gap"]
```

- [ ] **Step 2: Run pipeline tests and verify failure**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_pipeline.py -q
```

Expected: FAIL because `process_job_line` does not pass anomalies or coverage alerts.

- [ ] **Step 3: Integrate rules into main worker**

Modify imports in `workers/analysis_worker/main.py`:

```python
from rules import detect_anomalies, detect_coverage_alerts
```

Modify `process_job_line`:

```python
def process_job_line(line: str, evidence_store: FileEvidenceStore, repository) -> dict:
    job = parse_job(line)
    request_body = evidence_store.read_text(job.request_raw_ref) if job.request_raw_ref else ""
    response_body = evidence_store.read_text(job.response_raw_ref) if job.response_raw_ref else ""
    messages, results = normalize_json_trace(job, request_body, response_body)
    aggregates = aggregate_deltas(job)
    anomalies = detect_anomalies(job)
    coverage_alerts = detect_coverage_alerts(job, messages)
    repository.save_trace_analysis(messages, results, aggregates, anomalies, coverage_alerts)
    return {
        "accepted_trace_id": job.trace_id,
        "worker_status": "processed",
        "normalized_message_count": len(messages),
        "analysis_result_count": len(results),
        "aggregate_count": len(aggregates),
        "anomaly_count": len(anomalies),
        "coverage_alert_count": len(coverage_alerts),
        "usage_total_tokens": job.usage_total_tokens,
    }
```

- [ ] **Step 4: Update the contract example**

Modify `workers/analysis_worker/contract_example.json` so the example includes these fields if missing:

```json
{
  "request_content_type": "application/json",
  "response_content_type": "application/json",
  "status_code": 200,
  "upstream_status_code": 200,
  "stream": false,
  "request_started_at": "2026-04-28T13:45:22Z",
  "request_body_size": 128,
  "response_body_size": 256
}
```

Keep the file as one valid `trace_captured` JSON object and keep token usage below `20000` so the default contract example produces zero anomalies.

- [ ] **Step 5: Run pipeline tests and contract example**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_pipeline.py -q
```

Expected: PASS.

Run:

```bash
cd workers/analysis_worker && uv run python main.py < contract_example.json
```

Expected: JSON output includes `"worker_status": "processed"`, `"anomaly_count": 0`, and `"coverage_alert_count": 0`.

- [ ] **Step 6: Commit**

```bash
git add workers/analysis_worker/main.py workers/analysis_worker/tests/test_pipeline.py workers/analysis_worker/contract_example.json
git commit -m "feat: run anomaly and coverage rules in worker"
```

---

### Task 6: Documentation and Full Verification

**Files:**
- Modify: `docs/development.md`

- [ ] **Step 1: Document worker review outputs**

Append this section to `docs/development.md`:

````markdown
## Worker Anomalies and Coverage Alerts

After normalization and usage aggregation, the Python worker also writes review-ready outputs:

- `usage_anomalies` for deterministic MVP rules such as unresolved identity on successful upstream responses, invalid employee number snapshots, high single-trace token use, raw-only large responses, and server-error traces that may contribute to retry storms.
- `coverage_alerts` for worker-side normalization gaps where a route is marked `raw_and_normalized` but no normalized messages are extracted.

Run the worker tests:

```bash
cd workers/analysis_worker
uv run pytest -q
```

The MVP rules are intentionally explainable and per-trace. Baselines, semantic similarity, work relevance, and cross-trace clustering should be implemented in later plans.
````

- [ ] **Step 2: Run all Python worker tests**

Run:

```bash
cd workers/analysis_worker && uv run pytest -q
```

Expected: PASS.

- [ ] **Step 3: Run Go tests to ensure gateway compatibility**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 4: Run placeholder scan on the new plan and docs**

Run:

```bash
python - <<'PY'
from pathlib import Path

needles = [
    "T" + "BD",
    "T" + "ODO",
    "implement " + "later",
    "Similar " + "to Task",
    "appropriate " + "error handling",
    "Write tests for " + "the above",
]
paths = [
    Path("docs/superpowers/plans/2026-04-28-worker-anomaly-coverage.md"),
    Path("docs/development.md"),
]
matches = []
for path in paths:
    for index, line in enumerate(path.read_text(encoding="utf-8").splitlines(), start=1):
        for needle in needles:
            if needle in line:
                matches.append(f"{path}:{index}: {needle}")
if matches:
    raise SystemExit("\n".join(matches))
PY
```

Expected: no matches.

- [ ] **Step 5: Commit**

```bash
git add docs/development.md
git commit -m "docs: document worker anomaly outputs"
```

---

## Self-Review

Spec coverage:

- Rule-based anomaly detection is covered by Tasks 1, 3, 4, and 5.
- Review-ready anomaly persistence is covered by Tasks 1 and 4.
- Coverage alert generation for normalization gaps is covered by Tasks 1, 3, 4, and 5.
- Usage aggregation remains covered by the previous plan and is reused, not duplicated.
- Admin screens, RBAC, work relevance, media snapshots, and baselines are deliberately deferred to future plans because they are separate subsystems.

Placeholder scan:

- The plan contains concrete file paths, commands, expected outcomes, schema, tests, and implementation snippets.
- No step asks an implementer to fill in unspecified behavior.

Type consistency:

- `AnomalyAlert`, `CoverageAlert`, `detect_anomalies`, and `detect_coverage_alerts` are introduced before repository and pipeline tasks consume them.
- Repository method defaults preserve compatibility with any caller still passing only messages/results/aggregates.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-28-worker-anomaly-coverage.md`. Two execution options:

**1. Subagent-Driven (recommended)** - dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** - execute tasks in this session using executing-plans, batch execution with checkpoints.
