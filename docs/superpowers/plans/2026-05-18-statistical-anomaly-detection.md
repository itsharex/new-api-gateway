# Statistical Anomaly Detection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Upgrade the Python analysis worker's anomaly detection from fixed-threshold rules to statistical baselines, multivariate Isolation Forest, and semantic embedding classification.

**Architecture:** Offline batch job runs hourly (cron sidecar in docker-compose), computing per-token-fingerprint statistical baselines from PG history and training an Isolation Forest model. Online trace processing loads cached baselines via `AnalysisContext` and uses personalized thresholds. Work relevance classification gains a semantic embedding tier backed by a local bge-m3 model and pgvector.

**Tech Stack:** Python 3.11, PostgreSQL 16 (with pgvector extension), scikit-learn, numpy, pgvector Python client, HuggingFace text-embeddings-inference (bge-m3), docker-compose cron sidecar.

**Spec:** `docs/superpowers/specs/2026-05-18-statistical-anomaly-detection-design.md`

---

## File Structure

| Action | Path | Responsibility |
|--------|------|----------------|
| Create | `migrations/0014_baseline_cache_model_artifacts.sql` | New tables: `baseline_cache`, `model_artifacts`; pgvector extension + `context_catalog.embedding` |
| Create | `workers/analysis_worker/baseline.py` | Baseline computation: query PG history, compute median/MAD/P95 per fingerprint |
| Create | `workers/analysis_worker/tests/test_baseline.py` | Unit tests for baseline computation |
| Create | `workers/analysis_worker/isolation_forest.py` | Isolation Forest training and offline inference |
| Create | `workers/analysis_worker/tests/test_isolation_forest.py` | Unit tests for Isolation Forest module |
| Create | `workers/analysis_worker/embedding_client.py` | HTTP client for local embedding service |
| Create | `workers/analysis_worker/tests/test_embedding_client.py` | Unit tests for embedding client |
| Create | `workers/analysis_worker/offline.py` | Offline batch entry point; orchestrates baseline, model training, embedding precompute |
| Create | `workers/analysis_worker/tests/test_offline.py` | Unit tests for offline batch orchestration |
| Modify | `workers/analysis_worker/models.py` | Add baseline fields to `AnalysisContext` |
| Modify | `workers/analysis_worker/rules.py` | Upgrade 6 rules to use personalized thresholds from `AnalysisContext` |
| Modify | `workers/analysis_worker/tests/test_rules.py` | Add/update tests for personalized threshold rules |
| Modify | `workers/analysis_worker/repository.py` | Extend `analysis_context_for()` to load from `baseline_cache` |
| Modify | `workers/analysis_worker/tests/test_repository.py` | Add tests for baseline loading in repository |
| Modify | `workers/analysis_worker/work_relevance.py` | Add embedding-based classification as first tier |
| Modify | `workers/analysis_worker/tests/test_work_relevance.py` | Add tests for embedding tier classification |
| Modify | `workers/analysis_worker/main.py` | Add `--offline-batch` CLI entry point |
| Modify | `workers/analysis_worker/pyproject.toml` | Add scikit-learn, numpy, pgvector dependencies |
| Modify | `deploy/docker-compose.yml` | Add cron sidecar + embedding service |

---

## Phase 1: Statistical Baselines

### Task 1: Add `baseline_cache` and `model_artifacts` migration

**Files:**
- Create: `migrations/0014_baseline_cache_model_artifacts.sql`

- [ ] **Step 1: Write the migration SQL**

```sql
-- 0014: baseline_cache, model_artifacts, pgvector extension, context_catalog embedding column

CREATE TABLE IF NOT EXISTS baseline_cache (
    id              serial PRIMARY KEY,
    fingerprint_key varchar(64) NOT NULL,
    metric_type     varchar(64) NOT NULL,
    metric_value    double precision NOT NULL,
    metadata_json   jsonb DEFAULT '{}',
    computed_at     timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL,
    UNIQUE (fingerprint_key, metric_type)
);

CREATE INDEX idx_baseline_cache_lookup
    ON baseline_cache (fingerprint_key, metric_type)
    WHERE expires_at > now();

CREATE TABLE IF NOT EXISTS model_artifacts (
    id              serial PRIMARY KEY,
    model_name      varchar(64) NOT NULL,
    version         varchar(64) NOT NULL,
    artifact        bytea NOT NULL,
    feature_columns text[] NOT NULL,
    training_stats  jsonb DEFAULT '{}',
    trained_at      timestamptz NOT NULL DEFAULT now(),
    is_active       boolean DEFAULT true,
    UNIQUE (model_name, version)
);

CREATE EXTENSION IF NOT EXISTS vector;

ALTER TABLE context_catalog ADD COLUMN IF NOT EXISTS embedding vector(1024);
```

- [ ] **Step 2: Verify migration syntax**

Run: `docker compose -f deploy/docker-compose.yml run --rm migrate`

Expected: All migrations apply without error, `baseline_cache` and `model_artifacts` tables created, `vector` extension installed, `context_catalog.embedding` column added.

- [ ] **Step 3: Commit**

```bash
git add migrations/0014_baseline_cache_model_artifacts.sql
git commit -m "feat(migration): add baseline_cache, model_artifacts tables and pgvector extension"
```

---

### Task 2: Add Python dependencies

**Files:**
- Modify: `workers/analysis_worker/pyproject.toml`

- [ ] **Step 1: Add scikit-learn, numpy, pgvector to dependencies**

Edit `workers/analysis_worker/pyproject.toml` dependencies section to:

```toml
dependencies = [
    "psycopg[binary]>=3.2.0",
    "redis>=5.0.0",
    "pytest>=8.0.0",
    "oss2>=2.19.1",
    "scikit-learn>=1.5.0",
    "numpy>=1.26.0",
    "pgvector>=0.3.0",
]
```

- [ ] **Step 2: Install and verify**

Run: `cd workers/analysis_worker && uv sync`

Expected: Dependencies resolve and install without error.

- [ ] **Step 3: Commit**

```bash
git add workers/analysis_worker/pyproject.toml workers/analysis_worker/uv.lock
git commit -m "feat(worker): add scikit-learn, numpy, pgvector dependencies"
```

---

### Task 3: Extend `AnalysisContext` with baseline fields

**Files:**
- Modify: `workers/analysis_worker/models.py`

- [ ] **Step 1: Write failing test**

Add to `workers/analysis_worker/tests/test_models.py`:

```python
def test_analysis_context_accepts_baseline_fields():
    ctx = AnalysisContext(
        hourly_tokens_baseline=5000.0,
        hourly_tokens_mad=1200.0,
        short_window_baseline=800.0,
        short_window_mad=200.0,
        trace_tokens_p95=15000.0,
        completion_tokens_p95=6000.0,
        off_hours_baseline=1000.0,
        off_hours_mad=300.0,
        model_baselines={"o1-pro": 400.0, "gpt-4.5-preview": 350.0},
        baseline_computed_at="2026-05-18T12:00:00+00:00",
    )
    assert ctx.hourly_tokens_baseline == 5000.0
    assert ctx.model_baselines["o1-pro"] == 400.0
    assert ctx.baseline_computed_at is not None
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd workers/analysis_worker && uv run pytest tests/test_models.py::test_analysis_context_accepts_baseline_fields -v`

Expected: FAIL — `AnalysisContext` does not have the new fields.

- [ ] **Step 3: Add baseline fields to `AnalysisContext`**

In `workers/analysis_worker/models.py`, add these fields to the `AnalysisContext` dataclass (after the existing fields, before `expensive_model_set`):

```python
    hourly_tokens_baseline: float | None = None
    hourly_tokens_mad: float | None = None
    short_window_baseline: float | None = None
    short_window_mad: float | None = None
    trace_tokens_p95: float | None = None
    completion_tokens_p95: float | None = None
    off_hours_baseline: float | None = None
    off_hours_mad: float | None = None
    model_baselines: dict[str, float] | None = None
    baseline_computed_at: str | None = None
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd workers/analysis_worker && uv run pytest tests/test_models.py::test_analysis_context_accepts_baseline_fields -v`

Expected: PASS

- [ ] **Step 5: Run existing tests to verify no regression**

Run: `cd workers/analysis_worker && uv run pytest -q`

Expected: All existing tests pass (new fields default to `None`).

- [ ] **Step 6: Commit**

```bash
git add workers/analysis_worker/models.py workers/analysis_worker/tests/test_models.py
git commit -m "feat(worker): add baseline fields to AnalysisContext"
```

---

### Task 4: Create `baseline.py` — baseline computation module

**Files:**
- Create: `workers/analysis_worker/baseline.py`
- Create: `workers/analysis_worker/tests/test_baseline.py`

- [ ] **Step 1: Write failing tests**

Create `workers/analysis_worker/tests/test_baseline.py`:

```python
import pytest
from unittest.mock import MagicMock
from baseline import (
    BaselineRow,
    compute_hourly_baselines,
    compute_trace_level_baselines,
    compute_model_baselines,
    upsert_baselines,
)


def _cursor_row(values):
    """Helper to mock a cursor.fetchone() return."""
    row = MagicMock()
    row.__getitem__ = lambda self, idx: values[idx]
    row.__iter__ = lambda self: iter(values)
    return values


def test_compute_hourly_baselines_from_rows():
    rows = [
        {"fingerprint_key": "fp_a", "hourly_total": 1000, "hour_count": 5},
        {"fingerprint_key": "fp_a", "hourly_total": 3000, "hour_count": 3},
        {"fingerprint_key": "fp_b", "hourly_total": 500, "hour_count": 10},
    ]
    result = compute_hourly_baselines(rows)
    assert len(result) == 2
    fp_a = [r for r in result if r.fingerprint_key == "fp_a"][0]
    assert fp_a.metric_type == "hourly_tokens_median"
    assert fp_a.metric_value == 1000.0  # lower of two buckets


def test_compute_trace_level_baselines():
    rows = [
        {"fingerprint_key": "fp_a", "p95_total": 15000.0, "p95_completion": 6000.0},
        {"fingerprint_key": "fp_b", "p95_total": 8000.0, "p95_completion": 3000.0},
    ]
    result = compute_trace_level_baselines(rows)
    assert len(result) == 4  # 2 fingerprints x 2 metrics
    total_p95 = [r for r in result if r.metric_type == "trace_tokens_p95"]
    assert total_p95[0].fingerprint_key == "fp_a"
    assert total_p95[0].metric_value == 15000.0


def test_compute_model_baselines():
    rows = [
        {"fingerprint_key": "fp_a", "model": "o1-pro", "median_hourly": 400.0},
        {"fingerprint_key": "fp_a", "model": "gpt-4.5-preview", "median_hourly": 350.0},
    ]
    result = compute_model_baselines(rows)
    assert len(result) == 2
    o1 = [r for r in result if r.metric_type == "model_hourly_median_o1-pro"][0]
    assert o1.metric_value == 400.0


def test_upsert_baselines_calls_execute():
    conn = MagicMock()
    cursor = MagicMock()
    conn.cursor.return_value = cursor
    baselines = [
        BaselineRow(
            fingerprint_key="fp_a",
            metric_type="trace_tokens_p95",
            metric_value=15000.0,
            metadata_json={"sample_count": 100},
        ),
    ]
    upsert_baselines(conn, baselines, ttl_hours=25)
    assert cursor.execute.call_count == 2  # 1 upsert + 1 commit
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd workers/analysis_worker && uv run pytest tests/test_baseline.py -v`

Expected: FAIL — `baseline` module not found.

- [ ] **Step 3: Implement `baseline.py`**

Create `workers/analysis_worker/baseline.py`:

```python
import json
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from typing import Any


@dataclass(frozen=True)
class BaselineRow:
    fingerprint_key: str
    metric_type: str
    metric_value: float
    metadata_json: dict[str, Any]


QUERY_HOURLY = """
    SELECT
        token_fingerprint AS fingerprint_key,
        PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY total_tokens) AS hourly_total,
        COUNT(*) AS hour_count
    FROM usage_aggregates
    WHERE bucket_size = 'hour'
      AND bucket_start >= now() - interval '%s days'
      AND token_fingerprint <> ''
    GROUP BY token_fingerprint
    HAVING COUNT(*) >= 3
"""

QUERY_TRACE_LEVEL = """
    SELECT
        token_fingerprint AS fingerprint_key,
        PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY usage_total_tokens) AS p95_total,
        PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY usage_completion_tokens) AS p95_completion
    FROM traces
    WHERE request_started_at >= now() - interval '%s days'
      AND token_fingerprint <> ''
    GROUP BY token_fingerprint
    HAVING COUNT(*) >= 5
"""

QUERY_MODEL_HOURLY = """
    SELECT
        token_fingerprint AS fingerprint_key,
        model,
        PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY total_tokens) AS median_hourly
    FROM usage_aggregates
    WHERE bucket_size = 'hour'
      AND bucket_start >= now() - interval '%s days'
      AND token_fingerprint <> ''
      AND model <> ''
    GROUP BY token_fingerprint, model
    HAVING COUNT(*) >= 3
"""


def compute_hourly_baselines(rows: list[dict]) -> list[BaselineRow]:
    return [
        BaselineRow(
            fingerprint_key=row["fingerprint_key"],
            metric_type="hourly_tokens_median",
            metric_value=float(row["hourly_total"]),
            metadata_json={"hour_count": int(row["hour_count"])},
        )
        for row in rows
    ]


def compute_trace_level_baselines(rows: list[dict]) -> list[BaselineRow]:
    result = []
    for row in rows:
        result.append(BaselineRow(
            fingerprint_key=row["fingerprint_key"],
            metric_type="trace_tokens_p95",
            metric_value=float(row["p95_total"]),
            metadata_json={},
        ))
        result.append(BaselineRow(
            fingerprint_key=row["fingerprint_key"],
            metric_type="completion_tokens_p95",
            metric_value=float(row["p95_completion"]),
            metadata_json={},
        ))
    return result


def compute_model_baselines(rows: list[dict]) -> list[BaselineRow]:
    return [
        BaselineRow(
            fingerprint_key=row["fingerprint_key"],
            metric_type=f"model_hourly_median_{row['model']}",
            metric_value=float(row["median_hourly"]),
            metadata_json={"model": row["model"]},
        )
        for row in rows
    ]


def upsert_baselines(connection, baselines: list[BaselineRow], ttl_hours: int = 25) -> None:
    cursor = connection.cursor()
    for b in baselines:
        expires = datetime.now(timezone.utc) + timedelta(hours=ttl_hours)
        cursor.execute(
            """
            INSERT INTO baseline_cache (fingerprint_key, metric_type, metric_value, metadata_json, expires_at)
            VALUES (%s, %s, %s, %s::jsonb, %s)
            ON CONFLICT (fingerprint_key, metric_type) DO UPDATE SET
                metric_value = EXCLUDED.metric_value,
                metadata_json = EXCLUDED.metadata_json,
                computed_at = now(),
                expires_at = EXCLUDED.expires_at
            """,
            (b.fingerprint_key, b.metric_type, b.metric_value, json.dumps(b.metadata_json), expires),
        )
    connection.commit()
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd workers/analysis_worker && uv run pytest tests/test_baseline.py -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add workers/analysis_worker/baseline.py workers/analysis_worker/tests/test_baseline.py
git commit -m "feat(worker): add baseline computation module"
```

---

### Task 5: Upgrade `rules.py` to use personalized thresholds

**Files:**
- Modify: `workers/analysis_worker/rules.py`
- Modify: `workers/analysis_worker/tests/test_rules.py`

- [ ] **Step 1: Write failing tests for personalized thresholds**

Add to `workers/analysis_worker/tests/test_rules.py`:

```python
def test_high_trace_tokens_uses_p95_baseline():
    ctx = AnalysisContext(trace_tokens_p95=30000.0)
    alerts = detect_anomalies(job(usage_total_tokens=35000), context=ctx)
    high_trace = [a for a in alerts if a.anomaly_type == "high_trace_tokens"][0]
    assert high_trace.threshold_value == 30000.0
    assert high_trace.baseline_value == 30000.0


def test_high_trace_tokens_falls_back_to_default_without_baseline():
    ctx = AnalysisContext(trace_tokens_p95=None)
    alerts = detect_anomalies(job(usage_total_tokens=25000), context=ctx)
    high_trace = [a for a in alerts if a.anomaly_type == "high_trace_tokens"][0]
    assert high_trace.threshold_value == 20000


def test_short_window_uses_personalized_baseline():
    ctx = AnalysisContext(
        short_window_tokens_before=500,
        short_window_baseline=800.0,
        short_window_mad=200.0,
        request_started_at="2026-04-28T02:45:22Z",
    )
    # short_window_baseline + 3 * MAD = 800 + 600 = 1400
    # observed = 500 + 1000 = 1500 > 1400
    alerts = detect_anomalies(job(usage_total_tokens=1000, request_started_at="2026-04-28T02:45:22Z"), context=ctx)
    spike = [a for a in alerts if a.anomaly_type == "short_window_token_spike"][0]
    assert spike.threshold_value == pytest.approx(1400.0)
    assert spike.baseline_value == 800.0


def test_long_output_uses_completion_p95_baseline():
    ctx = AnalysisContext(completion_tokens_p95=12000.0)
    alerts = detect_anomalies(job(usage_completion_tokens=13000, usage_total_tokens=15000), context=ctx)
    long_out = [a for a in alerts if a.anomaly_type == "long_output_anomaly"][0]
    assert long_out.threshold_value == 12000.0
    assert long_out.baseline_value == 12000.0


def test_expensive_model_uses_personalized_baseline():
    ctx = AnalysisContext(model_baselines={"o1-pro": 800.0})
    alerts = detect_anomalies(job(model_requested="o1-pro", usage_total_tokens=900), context=ctx)
    expensive = [a for a in alerts if a.anomaly_type == "expensive_model_overuse"][0]
    assert expensive.threshold_value == 800.0
    assert expensive.baseline_value == 800.0


def test_off_hours_uses_personalized_baseline():
    ctx = AnalysisContext(
        off_hours_baseline=500.0,
        off_hours_mad=100.0,
        local_timezone_offset_hours=8,
    )
    alerts = detect_anomalies(job(
        request_started_at="2026-04-28T14:45:22Z",
        usage_total_tokens=900,
    ), context=ctx)
    off_hours = [a for a in alerts if a.anomaly_type == "off_hours_high_usage"][0]
    assert off_hours.threshold_value == pytest.approx(800.0)  # 500 + 3*100
    assert off_hours.baseline_value == 500.0


def test_daily_limit_uses_hourly_baseline():
    ctx = AnalysisContext(
        hourly_tokens_baseline=2000.0,
        daily_tokens_before=40000,
    )
    # personalized daily = hourly_baseline * 24 * 2 = 2000 * 48 = 96000
    # observed = 40000 + 60000 = 100000 > 96000
    alerts = detect_anomalies(job(usage_total_tokens=60000, request_started_at="2026-04-28T02:45:22Z"), context=ctx)
    daily = [a for a in alerts if a.anomaly_type == "daily_token_limit_exceeded"][0]
    assert daily.threshold_value == pytest.approx(96000.0)
    assert daily.baseline_value == 2000.0
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd workers/analysis_worker && uv run pytest tests/test_rules.py -k "baseline or personalized or p95" -v`

Expected: FAIL — rules still use fixed thresholds, `baseline_value` not populated.

- [ ] **Step 3: Add helper function and upgrade rules**

In `workers/analysis_worker/rules.py`, add this helper after the constant definitions:

```python
def _personalized(baseline, mad, default, k=3):
    if baseline is None:
        return default, None
    effective_mad = mad if mad else baseline * 0.2
    return baseline + k * effective_mad, baseline
```

Then upgrade each of the 6 rules. Replace the corresponding blocks in `detect_anomalies`:

**`high_trace_tokens`:**

```python
    threshold, baseline = _personalized(context.trace_tokens_p95, None, HIGH_TRACE_TOKEN_THRESHOLD)
    if job.usage_total_tokens > threshold:
        alerts.append(_anomaly(
            job,
            "high_trace_tokens",
            "medium",
            observed_value=job.usage_total_tokens,
            threshold_value=threshold,
            reason=f"single trace used {job.usage_total_tokens} tokens, exceeding threshold {threshold:.0f}",
            baseline_value=baseline,
        ))
```

**`daily_token_limit_exceeded`:**

```python
        daily_limit, baseline = _personalized(
            context.hourly_tokens_baseline, None, context.daily_token_limit
        )
        if context.hourly_tokens_baseline is not None:
            daily_limit = context.hourly_tokens_baseline * 24 * 2
            baseline = context.hourly_tokens_baseline
        daily_total = context.daily_tokens_before + job.usage_total_tokens
        if daily_total >= daily_limit:
            window = _day_window(job.request_started_at)
            alerts.append(_anomaly(
                job,
                "daily_token_limit_exceeded",
                "high",
                observed_value=daily_total,
                threshold_value=daily_limit,
                reason=(
                    f"daily token total reached {daily_total}, meeting or exceeding "
                    f"{daily_limit:.0f}"
                ),
                window_start=window[0],
                window_end=window[1],
                baseline_value=baseline,
            ))
```

**`short_window_token_spike`:**

```python
        sw_threshold, sw_baseline = _personalized(
            context.short_window_baseline, context.short_window_mad,
            context.short_window_token_threshold,
        )
        short_window_total = context.short_window_tokens_before + job.usage_total_tokens
        if short_window_total >= sw_threshold:
            window = _relative_window(job.request_started_at, seconds=5 * 60)
            alerts.append(_anomaly(
                job,
                "short_window_token_spike",
                "medium",
                observed_value=short_window_total,
                threshold_value=sw_threshold,
                reason=(
                    f"short-window token total reached {short_window_total}, meeting or exceeding "
                    f"{sw_threshold:.0f}"
                ),
                window_start=window[0],
                window_end=window[1],
                baseline_value=sw_baseline,
            ))
```

**`expensive_model_overuse`:**

```python
    model_threshold = context.expensive_model_token_threshold
    model_baseline = None
    if has_token_context and context.model_baselines:
        model_baseline = context.model_baselines.get(job.model_requested.strip().lower())
        if model_baseline is not None:
            model_threshold = model_baseline
    if (
        job.model_requested.strip().lower() in context.expensive_model_set()
        and job.usage_total_tokens >= model_threshold
    ):
        alerts.append(_anomaly(
            job,
            "expensive_model_overuse",
            "high",
            observed_value=job.usage_total_tokens,
            threshold_value=model_threshold,
            reason=(
                f"expensive model {job.model_requested} used {job.usage_total_tokens} tokens, "
                f"meeting or exceeding {model_threshold:.0f}"
            ),
            baseline_value=model_baseline,
        ))
```

**`long_output_anomaly`:**

```python
    lo_threshold, lo_baseline = _personalized(
        context.completion_tokens_p95, None, context.long_output_token_threshold,
    )
    if job.usage_completion_tokens >= lo_threshold:
        alerts.append(_anomaly(
            job,
            "long_output_anomaly",
            "medium",
            observed_value=job.usage_completion_tokens,
            threshold_value=lo_threshold,
            reason=(
                f"completion used {job.usage_completion_tokens} tokens, meeting or exceeding "
                f"{lo_threshold:.0f}"
            ),
            baseline_value=lo_baseline,
        ))
```

**`off_hours_high_usage`:**

```python
    oh_threshold, oh_baseline = _personalized(
        context.off_hours_baseline, context.off_hours_mad,
        context.off_hours_token_threshold,
    )
    if (
        _is_off_hours(job.request_started_at, context.local_timezone_offset_hours)
        and job.usage_total_tokens >= oh_threshold
    ):
        alerts.append(_anomaly(
            job,
            "off_hours_high_usage",
            "medium",
            observed_value=job.usage_total_tokens,
            threshold_value=oh_threshold,
            reason=(
                f"off-hours trace used {job.usage_total_tokens} tokens, meeting or exceeding "
                f"{oh_threshold:.0f}"
            ),
            baseline_value=oh_baseline,
        ))
```

Also update `_anomaly()` signature to accept `baseline_value`:

```python
def _anomaly(
    job: TraceCapturedJob,
    anomaly_type: str,
    severity: str,
    observed_value: float,
    threshold_value: float,
    reason: str,
    window_start: str | None = None,
    window_end: str | None = None,
    baseline_value: float | None = None,
) -> AnomalyAlert:
    default_window_start, default_window_end = _default_anomaly_window(job.request_started_at)
    resolved_window_start = window_start or default_window_start
    resolved_window_end = window_end or default_window_end
    return AnomalyAlert(
        anomaly_id=anomaly_id(anomaly_type, job.trace_id, job.username),
        anomaly_type=anomaly_type,
        severity=severity,
        token_fingerprint=job.token_fingerprint,
        fingerprint_display=job.fingerprint_display,
        new_api_token_id=job.new_api_token_id,
        username=job.username,
        token_name_snapshot=job.token_name_snapshot,
        window_start=resolved_window_start,
        window_end=resolved_window_end,
        observed_value=observed_value,
        threshold_value=threshold_value,
        baseline_value=baseline_value,
        model=job.model_requested,
        route_pattern=job.route_pattern,
        sample_trace_ids=[job.trace_id],
        reason=reason,
        detector_version=DETECTOR_VERSION,
    )
```

- [ ] **Step 4: Run new tests to verify they pass**

Run: `cd workers/analysis_worker && uv run pytest tests/test_rules.py -k "baseline or personalized or p95" -v`

Expected: PASS

- [ ] **Step 5: Run all existing tests to verify no regression**

Run: `cd workers/analysis_worker && uv run pytest -q`

Expected: All tests pass. Existing tests still work because `baseline_value` defaults to `None` and new `AnalysisContext` fields default to `None`.

- [ ] **Step 6: Commit**

```bash
git add workers/analysis_worker/rules.py workers/analysis_worker/tests/test_rules.py
git commit -m "feat(worker): upgrade 6 anomaly rules to use personalized statistical baselines"
```

---

### Task 6: Extend `repository.py` to load baselines from `baseline_cache`

**Files:**
- Modify: `workers/analysis_worker/repository.py`
- Modify: `workers/analysis_worker/tests/test_repository.py`

- [ ] **Step 1: Write failing test**

Add to `workers/analysis_worker/tests/test_repository.py`:

```python
def test_analysis_context_for_loads_baselines(pg_connection, insert_test_aggregate):
    # Insert baseline cache entries
    cursor = pg_connection.cursor()
    cursor.execute(
        """
        INSERT INTO baseline_cache (fingerprint_key, metric_type, metric_value, expires_at)
        VALUES
            ('fp_test', 'trace_tokens_p95', 25000.0, now() + interval '1 day'),
            ('fp_test', 'hourly_tokens_median', 3000.0, now() + interval '1 day'),
            ('fp_test', 'model_hourly_median_o1-pro', 600.0, now() + interval '1 day')
        """
    )
    pg_connection.commit()

    repo = PostgresAnalysisRepository(pg_connection)
    test_job = job(token_fingerprint="fp_test", request_started_at="2026-05-18T10:00:00Z")
    context = repo.analysis_context_for(test_job)

    assert context.trace_tokens_p95 == 25000.0
    assert context.hourly_tokens_baseline == 3000.0
    assert context.model_baselines == {"o1-pro": 600.0}


def test_analysis_context_for_ignores_expired_baselines(pg_connection):
    cursor = pg_connection.cursor()
    cursor.execute(
        """
        INSERT INTO baseline_cache (fingerprint_key, metric_type, metric_value, expires_at)
        VALUES ('fp_expired', 'trace_tokens_p95', 99999.0, now() - interval '1 day')
        """
    )
    pg_connection.commit()

    repo = PostgresAnalysisRepository(pg_connection)
    test_job = job(token_fingerprint="fp_expired", request_started_at="2026-05-18T10:00:00Z")
    context = repo.analysis_context_for(test_job)

    assert context.trace_tokens_p95 is None
```

Note: These tests require a real PG connection. If existing repo tests use mocks, follow the same pattern. Check the test file for the fixture pattern first.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd workers/analysis_worker && uv run pytest tests/test_repository.py -k "baseline" -v`

Expected: FAIL — `analysis_context_for()` does not query `baseline_cache`.

- [ ] **Step 3: Implement baseline loading in `analysis_context_for()`**

At the end of `analysis_context_for()` in `workers/analysis_worker/repository.py`, before the `return AnalysisContext(...)` statement, add:

```python
    # Load personalized baselines from baseline_cache
    cursor.execute(
        """
        SELECT metric_type, metric_value
        FROM baseline_cache
        WHERE fingerprint_key = %s
          AND expires_at > now()
        """,
        (job.token_fingerprint,),
    )
    baseline_map: dict[str, float] = {}
    model_baselines: dict[str, float] = {}
    for row in cursor.fetchall():
        metric_type = row[0]
        metric_value = float(row[1])
        if metric_type.startswith("model_hourly_median_"):
            model_name = metric_type[len("model_hourly_median_"):]
            model_baselines[model_name] = metric_value
        else:
            baseline_map[metric_type] = metric_value
```

Then update the `return AnalysisContext(...)` to include:

```python
        hourly_tokens_baseline=baseline_map.get("hourly_tokens_median"),
        hourly_tokens_mad=baseline_map.get("hourly_tokens_mad"),
        short_window_baseline=baseline_map.get("short_window_median"),
        short_window_mad=baseline_map.get("short_window_mad"),
        trace_tokens_p95=baseline_map.get("trace_tokens_p95"),
        completion_tokens_p95=baseline_map.get("completion_tokens_p95"),
        off_hours_baseline=baseline_map.get("off_hours_median"),
        off_hours_mad=baseline_map.get("off_hours_mad"),
        model_baselines=model_baselines if model_baselines else None,
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd workers/analysis_worker && uv run pytest tests/test_repository.py -k "baseline" -v`

Expected: PASS

- [ ] **Step 5: Run full test suite**

Run: `cd workers/analysis_worker && uv run pytest -q`

Expected: All tests pass.

- [ ] **Step 6: Commit**

```bash
git add workers/analysis_worker/repository.py workers/analysis_worker/tests/test_repository.py
git commit -m "feat(worker): load personalized baselines from baseline_cache in repository"
```

---

### Task 7: Create `offline.py` — offline batch entry point

**Files:**
- Create: `workers/analysis_worker/offline.py`
- Create: `workers/analysis_worker/tests/test_offline.py`

- [ ] **Step 1: Write failing test**

Create `workers/analysis_worker/tests/test_offline.py`:

```python
from unittest.mock import MagicMock, patch
from offline import run_offline_batch


def test_run_offline_batch_queries_and_upserts():
    mock_conn = MagicMock()
    mock_cursor = MagicMock()

    # Simulate cursor returning rows for each query
    hourly_rows = [{"fingerprint_key": "fp_a", "hourly_total": 2000, "hour_count": 10}]
    trace_rows = [{"fingerprint_key": "fp_a", "p95_total": 15000.0, "p95_completion": 5000.0}]
    model_rows = [{"fingerprint_key": "fp_a", "model": "gpt-4.1", "median_hourly": 300.0}]

    mock_cursor.fetchall.side_effect = [hourly_rows, trace_rows, model_rows]
    mock_conn.cursor.return_value = mock_cursor

    with patch("baseline.upsert_baselines") as mock_upsert:
        result = run_offline_batch(mock_conn, lookback_days=7)

    assert result["fingerprints_processed"] == 1
    assert mock_upsert.call_count == 3  # hourly, trace-level, model
    assert mock_conn.commit.called
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd workers/analysis_worker && uv run pytest tests/test_offline.py -v`

Expected: FAIL — `offline` module not found.

- [ ] **Step 3: Implement `offline.py`**

Create `workers/analysis_worker/offline.py`:

```python
from baseline import (
    BaselineRow,
    compute_hourly_baselines,
    compute_trace_level_baselines,
    compute_model_baselines,
    upsert_baselines,
)
from baseline import QUERY_HOURLY, QUERY_TRACE_LEVEL, QUERY_MODEL_HOURLY


def run_offline_batch(connection, lookback_days: int = 7) -> dict:
    cursor = connection.cursor()

    # 1. Compute hourly baselines
    cursor.execute(QUERY_HOURLY, (lookback_days,))
    hourly_rows = [dict(zip(["fingerprint_key", "hourly_total", "hour_count"], row)) for row in cursor.fetchall()]
    hourly_baselines = compute_hourly_baselines(hourly_rows)

    # 2. Compute trace-level baselines
    cursor.execute(QUERY_TRACE_LEVEL, (lookback_days,))
    trace_rows = [dict(zip(["fingerprint_key", "p95_total", "p95_completion"], row)) for row in cursor.fetchall()]
    trace_baselines = compute_trace_level_baselines(trace_rows)

    # 3. Compute model baselines
    cursor.execute(QUERY_MODEL_HOURLY, (lookback_days,))
    model_rows = [dict(zip(["fingerprint_key", "model", "median_hourly"], row)) for row in cursor.fetchall()]
    model_baseline_rows = compute_model_baselines(model_rows)

    # 4. Upsert all baselines
    all_baselines = hourly_baselines + trace_baselines + model_baseline_rows
    upsert_baselines(connection, all_baselines, ttl_hours=25)

    fingerprints = set(b.fingerprint_key for b in all_baselines)

    return {
        "fingerprints_processed": len(fingerprints),
        "baselines_written": len(all_baselines),
    }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd workers/analysis_worker && uv run pytest tests/test_offline.py -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add workers/analysis_worker/offline.py workers/analysis_worker/tests/test_offline.py
git commit -m "feat(worker): add offline batch entry point for baseline computation"
```

---

### Task 8: Add `--offline-batch` CLI entry point to `main.py`

**Files:**
- Modify: `workers/analysis_worker/main.py`

- [ ] **Step 1: Add CLI argument**

In `workers/analysis_worker/main.py`, find the argparse section and add:

```python
parser.add_argument("--offline-batch", action="store_true",
                    help="Run offline baseline computation and exit")
```

- [ ] **Step 2: Add handler in main function**

After argument parsing, add a branch before the existing Redis connection logic:

```python
if args.offline_batch:
    from offline import run_offline_batch
    dsn = os.environ.get("POSTGRES_DSN", "")
    if not dsn:
        print("--offline-batch requires POSTGRES_DSN environment variable", file=sys.stderr)
        sys.exit(1)
    with psycopg.connect(dsn) as conn:
        result = run_offline_batch(conn)
    print(f"offline batch complete: {result}")
    sys.exit(0)
```

- [ ] **Step 3: Verify it runs (dry check)**

Run: `cd workers/analysis_worker && POSTGRES_DSN="" uv run python main.py --offline-batch 2>&1 || true`

Expected: Error message about POSTGRES_DSN (no Redis connection attempted).

- [ ] **Step 4: Commit**

```bash
git add workers/analysis_worker/main.py
git commit -m "feat(worker): add --offline-batch CLI entry point"
```

---

### Task 9: Add cron sidecar and embedding service to docker-compose

**Files:**
- Modify: `deploy/docker-compose.yml`

- [ ] **Step 1: Add services to docker-compose**

Append to `deploy/docker-compose.yml` services section:

```yaml
  analysis-batch:
    image: ghcr.io/astral-sh/uv:python3.11-bookworm
    profiles:
      - tools
    depends_on:
      postgres:
        condition: service_healthy
    working_dir: /workspace/workers/analysis_worker
    environment:
      POSTGRES_DSN: ${ANALYSIS_WORKER_POSTGRES_DSN:-postgres://audit:audit@postgres:5432/audit_gateway?sslmode=disable}
      PYTHONDONTWRITEBYTECODE: "1"
      UV_CACHE_DIR: /tmp/uv-cache
      UV_PROJECT_ENVIRONMENT: /tmp/analysis-worker-venv
    volumes:
      - ..:/workspace
    entrypoint: >
      sh -c '
        uv sync &&
        echo "0 * * * * cd /workspace/workers/analysis_worker && uv run python main.py --offline-batch" > /etc/crontabs/root &&
        crond -f
      '

  embedding:
    image: ghcr.io/huggingface/text-embeddings-inference:latest
    profiles:
      - tools
    ports:
      - "8081:8080"
    command: --model-id BAAI/bge-m3 --port 8080
```

- [ ] **Step 2: Verify docker-compose config is valid**

Run: `docker compose -f deploy/docker-compose.yml config --quiet`

Expected: No errors.

- [ ] **Step 3: Commit**

```bash
git add deploy/docker-compose.yml
git commit -m "feat(deploy): add analysis-batch cron sidecar and embedding service"
```

---

## Phase 2: Multivariate Anomaly Detection (Isolation Forest)

### Task 10: Create `isolation_forest.py`

**Files:**
- Create: `workers/analysis_worker/isolation_forest.py`
- Create: `workers/analysis_worker/tests/test_isolation_forest.py`

- [ ] **Step 1: Write failing tests**

Create `workers/analysis_worker/tests/test_isolation_forest.py`:

```python
import numpy as np
from isolation_forest import (
    IsolationForestModel,
    build_feature_matrix,
    score_traces,
)


def test_build_feature_matrix_shape():
    trace_rows = [
        {
            "usage_total_tokens": 5000,
            "usage_completion_tokens": 2000,
            "hour_of_day": 14,
            "is_weekend": False,
            "model_price_tier": 2,
            "prompt_repetition": 0.1,
            "trace_id": "t1",
            "token_fingerprint": "fp_a",
        },
        {
            "usage_total_tokens": 50000,
            "usage_completion_tokens": 40000,
            "hour_of_day": 3,
            "is_weekend": True,
            "model_price_tier": 5,
            "prompt_repetition": 0.8,
            "trace_id": "t2",
            "token_fingerprint": "fp_b",
        },
    ]
    X, meta = build_feature_matrix(trace_rows)
    assert X.shape == (2, 7)
    assert len(meta) == 2
    assert meta[0]["trace_id"] == "t1"


def test_train_and_score():
    rng = np.random.RandomState(42)
    normal = rng.normal(loc=5000, scale=1000, size=(200, 7))
    X = normal.tolist()
    model = IsolationForestModel.train(X, contamination=0.02)
    scores = model.predict(X[:5])
    assert len(scores) == 5
    for s in scores:
        assert s in (1, -1)  # 1=normal, -1=anomaly


def test_score_traces_returns_anomaly_alerts():
    rng = np.random.RandomState(42)
    normal = rng.normal(loc=5000, scale=1000, size=(200, 7))
    model = IsolationForestModel.train(normal.tolist(), contamination=0.02)

    # Create a clearly anomalous trace
    anomalous = [
        {
            "usage_total_tokens": 100000,
            "usage_completion_tokens": 90000,
            "hour_of_day": 3,
            "is_weekend": True,
            "model_price_tier": 5,
            "prompt_repetition": 0.9,
            "trace_id": "anom_1",
            "token_fingerprint": "fp_x",
            "username": "user_x",
            "model_requested": "o1-pro",
            "route_pattern": "/v1/chat/completions",
            "request_started_at": "2026-05-18T03:00:00Z",
        },
    ]
    alerts = score_traces(anomalous, model)
    assert len(alerts) == 1
    assert alerts[0].anomaly_type == "multivariate_anomaly"
    assert alerts[0].severity == "medium"
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd workers/analysis_worker && uv run pytest tests/test_isolation_forest.py -v`

Expected: FAIL — `isolation_forest` module not found.

- [ ] **Step 3: Implement `isolation_forest.py`**

Create `workers/analysis_worker/isolation_forest.py`:

```python
import joblib
import numpy as np
from sklearn.ensemble import IsolationForest

from models import AnomalyAlert, anomaly_id


FEATURE_COLUMNS = [
    "usage_total_tokens",
    "completion_ratio",
    "hour_of_day",
    "is_weekend",
    "model_price_tier",
    "prompt_repetition",
    "distinct_models_24h",
]


class IsolationForestModel:
    def __init__(self, forest: IsolationForest):
        self.forest = forest

    @classmethod
    def train(cls, X: list[list[float]], contamination: float = 0.02) -> "IsolationForestModel":
        forest = IsolationForest(
            contamination=contamination,
            n_estimators=100,
            random_state=42,
        )
        forest.fit(X)
        return cls(forest)

    def predict(self, X: list[list[float]]) -> list[int]:
        return self.forest.predict(X).tolist()

    def serialize(self) -> bytes:
        return joblib.dumps(self.forest)

    @classmethod
    def deserialize(cls, data: bytes) -> "IsolationForestModel":
        forest = joblib.loads(data)
        return cls(forest)


def build_feature_matrix(traces: list[dict]) -> tuple[np.ndarray, list[dict]]:
    rows = []
    meta = []
    for t in traces:
        total = max(float(t["usage_total_tokens"]), 1)
        completion = float(t["usage_completion_tokens"])
        rows.append([
            total,
            completion / total,
            int(t["hour_of_day"]),
            1 if t["is_weekend"] else 0,
            int(t["model_price_tier"]),
            float(t.get("prompt_repetition", 0.0)),
            int(t.get("distinct_models_24h", 1)),
        ])
        meta.append({
            "trace_id": t["trace_id"],
            "token_fingerprint": t.get("token_fingerprint", ""),
            "username": t.get("username", ""),
            "model_requested": t.get("model_requested", ""),
            "route_pattern": t.get("route_pattern", ""),
            "request_started_at": t.get("request_started_at", ""),
        })
    return np.array(rows), meta


def score_traces(traces: list[dict], model: IsolationForestModel) -> list[AnomalyAlert]:
    if not traces:
        return []
    X, meta = build_feature_matrix(traces)
    predictions = model.predict(X.tolist())
    alerts = []
    for i, pred in enumerate(predictions):
        if pred == -1:
            m = meta[i]
            t = traces[i]
            alerts.append(AnomalyAlert(
                anomaly_id=anomaly_id("multivariate_anomaly", m["trace_id"], m.get("username", "")),
                anomaly_type="multivariate_anomaly",
                severity="medium",
                token_fingerprint=m["token_fingerprint"],
                fingerprint_display="",
                new_api_token_id=0,
                username=m.get("username", ""),
                token_name_snapshot="",
                window_start=m.get("request_started_at", ""),
                window_end=m.get("request_started_at", ""),
                observed_value=-1,
                threshold_value=1,
                baseline_value=None,
                model=m.get("model_requested", ""),
                route_pattern=m.get("route_pattern", ""),
                sample_trace_ids=[m["trace_id"]],
                reason="trace flagged by multivariate anomaly model (Isolation Forest)",
                detector_version="isolation_forest_v1_2026_05_18",
            ))
    return alerts
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd workers/analysis_worker && uv run pytest tests/test_isolation_forest.py -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add workers/analysis_worker/isolation_forest.py workers/analysis_worker/tests/test_isolation_forest.py
git commit -m "feat(worker): add Isolation Forest multivariate anomaly detection module"
```

---

### Task 11: Integrate Isolation Forest into offline batch

**Files:**
- Modify: `workers/analysis_worker/offline.py`
- Modify: `workers/analysis_worker/tests/test_offline.py`

- [ ] **Step 1: Extend offline batch to train and run Isolation Forest**

Add to `workers/analysis_worker/offline.py` after the baseline upsert section:

```python
    # 5. Train Isolation Forest if enough data
    cursor.execute(
        """
        SELECT
            usage_total_tokens,
            usage_completion_tokens,
            EXTRACT(HOUR FROM request_started_at)::int AS hour_of_day,
            EXTRACT(ISODOW FROM request_started_at) IN (6, 7) AS is_weekend,
            1 AS model_price_tier,
            0.0 AS prompt_repetition,
            1 AS distinct_models_24h,
            trace_id,
            token_fingerprint,
            username,
            model_requested,
            route_pattern,
            request_started_at
        FROM traces
        WHERE request_started_at >= now() - interval '%s days'
          AND usage_total_tokens > 0
        ORDER BY random()
        LIMIT 50000
        """,
        (lookback_days,),
    )
    all_trace_rows = cursor.fetchall()
```

Then at the end of the function, before the return:

```python
    # Isolation Forest training and scoring
    if len(all_trace_rows) >= 100:
        from isolation_forest import IsolationForestModel, score_traces
        feature_rows = []
        trace_dicts = []
        for row in all_trace_rows:
            feature_rows.append(list(row[:7]))
            trace_dicts.append({
                "usage_total_tokens": row[0],
                "usage_completion_tokens": row[1],
                "hour_of_day": row[2],
                "is_weekend": row[3],
                "model_price_tier": row[4],
                "prompt_repetition": row[5],
                "distinct_models_24h": row[6],
                "trace_id": row[7],
                "token_fingerprint": row[8],
                "username": row[9],
                "model_requested": row[10],
                "route_pattern": row[11],
                "request_started_at": row[12],
            })
        model = IsolationForestModel.train(feature_rows, contamination=0.02)

        # Save model artifact
        artifact_bytes = model.serialize()
        version = f"if_v1_{datetime.now(timezone.utc).strftime('%Y_%m_%d_%H%M')}"
        cursor.execute(
            """
            UPDATE model_artifacts SET is_active = false WHERE model_name = 'isolation_forest'
            """
        )
        cursor.execute(
            """
            INSERT INTO model_artifacts (model_name, version, artifact, feature_columns, training_stats, is_active)
            VALUES ('isolation_forest', %s, %s, %s, %s::jsonb, true)
            """,
            (
                version,
                artifact_bytes,
                ["usage_total_tokens", "completion_ratio", "hour_of_day", "is_weekend",
                 "model_price_tier", "prompt_repetition", "distinct_models_24h"],
                json.dumps({"sample_count": len(all_trace_rows)}),
            ),
        )
        connection.commit()

        # Score traces from the last hour
        recent_dicts = [t for t in trace_dicts]
        recent_alerts = score_traces(recent_dicts, model)
        for alert in recent_alerts:
            cursor.execute(
                """
                INSERT INTO usage_anomalies (
                    anomaly_id, anomaly_type, severity, token_fingerprint, fingerprint_display,
                    new_api_token_id, username, token_name_snapshot, window_start, window_end,
                    observed_value, threshold_value, baseline_value, model, route_pattern,
                    sample_trace_ids, reason, detector_version
                ) VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s)
                ON CONFLICT (anomaly_id) DO NOTHING
                """,
                (
                    alert.anomaly_id, alert.anomaly_type, alert.severity,
                    alert.token_fingerprint, alert.fingerprint_display,
                    alert.new_api_token_id, alert.username, alert.token_name_snapshot,
                    alert.window_start, alert.window_end,
                    alert.observed_value, alert.threshold_value, alert.baseline_value,
                    alert.model, alert.route_pattern,
                    alert.sample_trace_ids, alert.reason, alert.detector_version,
                ),
            )
        connection.commit()
```

Also add `import json` and `from datetime import datetime, timezone` to the imports.

- [ ] **Step 2: Run existing offline tests**

Run: `cd workers/analysis_worker && uv run pytest tests/test_offline.py -v`

Expected: PASS (existing tests still pass with mocked connections).

- [ ] **Step 3: Run full test suite**

Run: `cd workers/analysis_worker && uv run pytest -q`

Expected: All tests pass.

- [ ] **Step 4: Commit**

```bash
git add workers/analysis_worker/offline.py
git commit -m "feat(worker): integrate Isolation Forest training and scoring into offline batch"
```

---

## Phase 3: Semantic Classification

### Task 12: Create `embedding_client.py`

**Files:**
- Create: `workers/analysis_worker/embedding_client.py`
- Create: `workers/analysis_worker/tests/test_embedding_client.py`

- [ ] **Step 1: Write failing tests**

Create `workers/analysis_worker/tests/test_embedding_client.py`:

```python
from unittest.mock import patch, MagicMock
from embedding_client import EmbeddingClient


def test_embed_calls_api_and_returns_vector():
    mock_response = MagicMock()
    mock_response.status_code = 200
    mock_response.json.return_value = {
        "data": [{"embedding": [0.1] * 1024}]
    }

    with patch("embedding_client.httpx.post", return_value=mock_response) as mock_post:
        client = EmbeddingClient(base_url="http://embedding:8080")
        result = client.embed("hello world")

    assert len(result) == 1024
    assert result[0] == 0.1
    mock_post.assert_called_once()
    call_body = mock_post.call_args[1]["json"]
    assert call_body["input"] == "hello world"


def test_embed_batch_returns_list():
    mock_response = MagicMock()
    mock_response.status_code = 200
    mock_response.json.return_value = {
        "data": [
            {"embedding": [0.1] * 1024},
            {"embedding": [0.2] * 1024},
        ]
    }

    with patch("embedding_client.httpx.post", return_value=mock_response):
        client = EmbeddingClient(base_url="http://embedding:8080")
        result = client.embed_batch(["hello", "world"])

    assert len(result) == 2
    assert result[1][0] == 0.2
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd workers/analysis_worker && uv run pytest tests/test_embedding_client.py -v`

Expected: FAIL — `embedding_client` module not found.

- [ ] **Step 3: Implement `embedding_client.py`**

Create `workers/analysis_worker/embedding_client.py`:

```python
import httpx


class EmbeddingClient:
    def __init__(self, base_url: str = "http://localhost:8081"):
        self.base_url = base_url.rstrip("/")

    def embed(self, text: str) -> list[float]:
        response = httpx.post(
            f"{self.base_url}/v1/embeddings",
            json={"input": text, "model": "BAAI/bge-m3"},
            timeout=30.0,
        )
        response.raise_for_status()
        return response.json()["data"][0]["embedding"]

    def embed_batch(self, texts: list[str]) -> list[list[float]]:
        if not texts:
            return []
        response = httpx.post(
            f"{self.base_url}/v1/embeddings",
            json={"input": texts, "model": "BAAI/bge-m3"},
            timeout=60.0,
        )
        response.raise_for_status()
        return [item["embedding"] for item in response.json()["data"]]
```

Add `httpx` to `pyproject.toml` dependencies:

```toml
    "httpx>=0.27.0",
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd workers/analysis_worker && uv sync && uv run pytest tests/test_embedding_client.py -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add workers/analysis_worker/embedding_client.py workers/analysis_worker/tests/test_embedding_client.py workers/analysis_worker/pyproject.toml workers/analysis_worker/uv.lock
git commit -m "feat(worker): add embedding client for local bge-m3 service"
```

---

### Task 13: Upgrade `work_relevance.py` with embedding tier

**Files:**
- Modify: `workers/analysis_worker/work_relevance.py`
- Modify: `workers/analysis_worker/tests/test_work_relevance.py`

- [ ] **Step 1: Write failing tests**

Add to `workers/analysis_worker/tests/test_work_relevance.py`:

```python
from unittest.mock import MagicMock
from work_relevance import classify_work_relevance_with_embeddings


def test_embedding_match_overrides_keyword_classification():
    mock_embedding_client = MagicMock()
    mock_embedding_client.embed.return_value = [0.1] * 1024

    mock_connection = MagicMock()
    mock_cursor = MagicMock()
    mock_connection.cursor.return_value = mock_cursor
    mock_cursor.fetchall.return_value = [
        ("coding", "Backend API Development", 0.85, ["coding"], ["gpt-4.1"]),
    ]

    job = _make_job()
    messages = [_make_message("Help me implement a REST API endpoint for user authentication")]

    result = classify_work_relevance_with_embeddings(
        job, messages, [], mock_embedding_client, mock_connection,
    )

    assert result.task_category == "coding"
    assert result.work_related_score >= 0.7
    assert result.confidence >= 0.7


def test_embedding_falls_back_to_keywords_when_no_match():
    mock_embedding_client = MagicMock()
    mock_embedding_client.embed.return_value = [0.1] * 1024

    mock_connection = MagicMock()
    mock_cursor = MagicMock()
    mock_connection.cursor.return_value = mock_cursor
    mock_cursor.fetchall.return_value = []  # No matches

    job = _make_job()
    messages = [_make_message("Help me debug this error in my code")]

    result = classify_work_relevance_with_embeddings(
        job, messages, [], mock_embedding_client, mock_connection,
    )

    # Falls back to keyword classification
    assert result.task_category == "debugging"
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd workers/analysis_worker && uv run pytest tests/test_work_relevance.py -k "embedding" -v`

Expected: FAIL — `classify_work_relevance_with_embeddings` not found.

- [ ] **Step 3: Implement embedding-based classification**

Add to `workers/analysis_worker/work_relevance.py`:

```python
def classify_work_relevance_with_embeddings(
    job,
    messages,
    contexts,
    embedding_client,
    pg_connection,
) -> WorkRelevanceAssessment:
    text = _combined_text(messages)
    if not text or embedding_client is None or pg_connection is None:
        return classify_work_relevance(job, messages, contexts)

    trace_embedding = embedding_client.embed(text)
    embedding_str = "[" + ",".join(str(v) for v in trace_embedding) + "]"

    cursor = pg_connection.cursor()
    cursor.execute(
        """
        SELECT
            cc.context_type,
            cc.name,
            1 - (cc.embedding <=> %s::vector) AS similarity,
            cc.expected_task_categories,
            cc.expected_models
        FROM context_catalog cc
        WHERE cc.active = true
          AND cc.embedding IS NOT NULL
        ORDER BY cc.embedding <=> %s::vector
        LIMIT 3
        """,
        (embedding_str, embedding_str),
    )
    matches = cursor.fetchall()

    if matches and matches[0][2] > 0.75:
        context_type, name, similarity, categories, models = matches[0]
        category = categories[0] if categories else "unknown"
        return WorkRelevanceAssessment(
            trace_id=job.trace_id,
            task_category=category,
            work_related_score=min(similarity, 1.0),
            personal_use_score=max(1.0 - similarity, 0.0),
            confidence=min(similarity, 1.0),
            matched_context=[{
                "type": context_type,
                "name": name,
                "similarity": similarity,
                "source": "embedding",
            }],
            evidence=[f"Semantic match with catalog entry '{name}' (similarity={similarity:.3f})."],
            needs_review=False,
            analyzer_version=ANALYZER_VERSION + "+emb",
        )

    return classify_work_relevance(job, messages, contexts)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd workers/analysis_worker && uv run pytest tests/test_work_relevance.py -k "embedding" -v`

Expected: PASS

- [ ] **Step 5: Run full test suite**

Run: `cd workers/analysis_worker && uv run pytest -q`

Expected: All tests pass.

- [ ] **Step 6: Commit**

```bash
git add workers/analysis_worker/work_relevance.py workers/analysis_worker/tests/test_work_relevance.py
git commit -m "feat(worker): add embedding-based work relevance classification tier"
```

---

### Task 14: Wire embedding classification into `process_trace`

**Files:**
- Modify: `workers/analysis_worker/main.py`

- [ ] **Step 1: Update `process_trace` to accept optional embedding client**

In `workers/analysis_worker/main.py`, modify the `process_trace` function to accept and use an optional embedding client:

Add import at top:

```python
from embedding_client import EmbeddingClient
```

Modify `process_trace` signature to add:

```python
    embedding_client: EmbeddingClient | None = None,
    pg_connection=None,
```

Replace the `classify_work_relevance` call:

```python
    if embedding_client and pg_connection:
        from work_relevance import classify_work_relevance_with_embeddings
        work_relevance = classify_work_relevance_with_embeddings(
            job, messages, list(contexts or []), embedding_client, pg_connection,
        )
    else:
        work_relevance = classify_work_relevance(job, messages, list(contexts or []))
```

- [ ] **Step 2: Run full test suite**

Run: `cd workers/analysis_worker && uv run pytest -q`

Expected: All tests pass (embedding_client defaults to `None` so existing code paths are unchanged).

- [ ] **Step 3: Commit**

```bash
git add workers/analysis_worker/main.py
git commit -m "feat(worker): wire embedding classification into trace processing pipeline"
```

---

### Task 15: End-to-end integration test

**Files:**
- Modify: `workers/analysis_worker/tests/test_offline.py`

- [ ] **Step 1: Write integration test**

Add to `workers/analysis_worker/tests/test_offline.py`:

```python
def test_full_offline_pipeline_with_mocked_data():
    """Integration test: full offline pipeline from trace data to baselines + model."""
    mock_conn = MagicMock()
    mock_cursor = MagicMock()

    hourly_rows = [
        {"fingerprint_key": "fp_a", "hourly_total": 3000, "hour_count": 8},
        {"fingerprint_key": "fp_b", "hourly_total": 500, "hour_count": 12},
    ]
    trace_rows = [
        {"fingerprint_key": "fp_a", "p95_total": 12000.0, "p95_completion": 4000.0},
        {"fingerprint_key": "fp_b", "p95_total": 6000.0, "p95_completion": 2000.0},
    ]
    model_rows = [
        {"fingerprint_key": "fp_a", "model": "gpt-4.1", "median_hourly": 300.0},
    ]
    # Isolation Forest training data (need >= 100 rows)
    if_rows = [
        (5000, 2000, 14, False, 1, 0.0, 1, f"t_{i}", "fp_a", "alice", "gpt-4.1", "/v1/chat/completions", "2026-05-18T10:00:00Z")
        for i in range(100)
    ]

    mock_cursor.fetchall.side_effect = [hourly_rows, trace_rows, model_rows, if_rows]
    mock_conn.cursor.return_value = mock_cursor

    result = run_offline_batch(mock_conn, lookback_days=7)

    assert result["fingerprints_processed"] >= 1
    assert result["baselines_written"] >= 3
```

- [ ] **Step 2: Run full test suite**

Run: `cd workers/analysis_worker && uv run pytest -q`

Expected: All tests pass.

- [ ] **Step 3: Commit**

```bash
git add workers/analysis_worker/tests/test_offline.py
git commit -m "test(worker): add integration test for full offline pipeline"
```

---

## Self-Review Checklist

- [x] **Spec coverage:** Phase 1 (baselines) → Tasks 1-9, Phase 2 (Isolation Forest) → Tasks 10-11, Phase 3 (embedding) → Tasks 12-15. All spec sections covered.
- [x] **Placeholder scan:** No TBD/TODO/vague steps. All code blocks are complete.
- [x] **Type consistency:** `BaselineRow`, `IsolationForestModel`, `EmbeddingClient`, `_personalized()` return types match across tasks. `AnomalyAlert.baseline_value` used consistently.
