# Anomaly Menu Rules Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Narrow the anomaly menu to clear non-work and clear high-cost signals, remove noisy anomaly types, switch remaining cost checks to `effective_tokens`, and show Chinese reason text in the admin anomaly menu.

**Architecture:** The worker keeps anomaly generation authoritative, but its rule set is reduced to four persisted anomaly types. Cost checks move to a shared `effective_tokens` metric and a new effective-token trace baseline. The admin API adds a display-oriented Chinese reason field so the UI can render readable reasons without coupling worker persistence text to presentation wording.

**Tech Stack:** Python worker (`uv`, `pytest`), Go admin backend (`go test`), vanilla admin UI JavaScript, PostgreSQL-backed repositories.

---

## File Structure

### Worker Rules And Context

- Modify: `workers/analysis_worker/models.py`
  - Remove unused `AnalysisContext` fields tied only to deleted anomaly rules.
  - Rename the remaining per-trace baseline field from total-token semantics to effective-token semantics.
- Modify: `workers/analysis_worker/baseline.py`
  - Compute trace-level baseline from `effective_tokens`.
  - Stop computing hourly/model baselines that only fed deleted rules.
- Modify: `workers/analysis_worker/repository.py`
  - Load only the baseline fields still needed by surviving anomaly rules.
- Modify: `workers/analysis_worker/rules.py`
  - Delete removed anomaly types.
  - Add `effective_tokens` helper.
  - Rework `high_trace_tokens`, `long_output_anomaly`, and `off_hours_high_usage`.
  - Collapse explicit non-work anomalies to `non_work_use`.
- Modify: `workers/analysis_worker/work_relevance.py`
  - Remove `unknown_high_cost` review path.
  - Keep `work_nonwork_conflict` as review-only.

### Worker Tests

- Modify: `workers/analysis_worker/tests/test_baseline.py`
- Modify: `workers/analysis_worker/tests/test_repository.py`
- Modify: `workers/analysis_worker/tests/test_models.py`
- Modify: `workers/analysis_worker/tests/test_rules.py`
- Modify: `workers/analysis_worker/tests/test_work_relevance.py`
- Modify: `workers/analysis_worker/tests/test_pipeline.py`

### Admin Presentation

- Modify: `internal/admin/models.go`
  - Add a display-oriented Chinese reason field to anomaly summaries.
- Create: `internal/admin/anomaly_reason.go`
  - Format `display_reason` from structured anomaly summary data.
- Create: `internal/admin/anomaly_reason_test.go`
  - Unit tests for Chinese reason rendering.
- Modify: `internal/admin/handlers.go`
  - Populate `display_reason` before returning anomaly list responses.
- Modify: `internal/adminui/app.js`
  - Prefer `display_reason` over raw `reason`.

### Documentation

- Modify: `README.md`
- Modify: `ARCHITECTURE.md`

## Task 1: Convert Remaining Cost Baselines To Effective Tokens

**Files:**
- Modify: `workers/analysis_worker/models.py`
- Modify: `workers/analysis_worker/baseline.py`
- Modify: `workers/analysis_worker/repository.py`
- Modify: `workers/analysis_worker/tests/test_baseline.py`
- Modify: `workers/analysis_worker/tests/test_repository.py`
- Modify: `workers/analysis_worker/tests/test_models.py`

- [ ] **Step 1: Write the failing baseline and context tests**

Add or update these tests first.

```python
# workers/analysis_worker/tests/test_baseline.py
def test_compute_trace_level_baselines_returns_effective_and_completion_rows():
    rows = [
        {"fingerprint_key": "fp_a", "p95_effective": 18000.0, "p95_completion": 3000.0},
    ]

    result = compute_trace_level_baselines(rows)

    assert result[0] == BaselineRow(
        fingerprint_key="fp_a",
        metric_type="trace_effective_tokens_p95",
        metric_value=18000.0,
        metadata_json={},
    )
    assert result[1] == BaselineRow(
        fingerprint_key="fp_a",
        metric_type="completion_tokens_p95",
        metric_value=3000.0,
        metadata_json={},
    )
```

```python
# workers/analysis_worker/tests/test_repository.py
def test_analysis_context_maps_trace_effective_tokens_p95_from_baseline_cache():
    computed = datetime.now(timezone.utc)
    conn = FakeConnection()
    conn.baseline_rows = [
        ("trace_effective_tokens_p95", 22000.0, {}, computed),
        ("completion_tokens_p95", 9000.0, {}, computed),
    ]
    repo = PostgresAnalysisRepository(conn)

    ctx = repo.analysis_context_for(job(token_fingerprint="fp_1"))

    assert ctx.trace_effective_tokens_p95 == 22000.0
    assert ctx.completion_tokens_p95 == 9000.0
```

```python
# workers/analysis_worker/tests/test_models.py
def test_analysis_context_defaults_only_keep_surviving_cost_fields():
    ctx = AnalysisContext()

    assert ctx.trace_effective_tokens_p95 is None
    assert ctx.completion_tokens_p95 is None
    assert ctx.long_output_token_threshold == 16_000
    assert ctx.off_hours_token_threshold == 20_000
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run:

```bash
cd /Users/roy/codes/new-api-gateway/workers/analysis_worker
uv run pytest -q tests/test_baseline.py tests/test_repository.py tests/test_models.py -k "trace_effective or surviving_cost_fields"
```

Expected:

- FAIL because `trace_effective_tokens_p95` does not exist yet
- FAIL because `QUERY_TRACE_LEVEL` still emits `p95_total`
- FAIL because `AnalysisContext` still exposes deleted rule defaults

- [ ] **Step 3: Implement effective-token baseline plumbing**

Apply these focused changes.

```python
# workers/analysis_worker/models.py
@dataclass(frozen=True)
class AnalysisContext:
    long_output_token_threshold: int = 16_000
    local_timezone_offset_hours: int = 8
    off_hours_token_threshold: int = 20_000
    trace_effective_tokens_p95: float | None = None
    completion_tokens_p95: float | None = None
    baseline_computed_at: str | None = None
```

```python
# workers/analysis_worker/baseline.py
QUERY_TRACE_LEVEL = """
SELECT
    token_fingerprint AS fingerprint_key,
    PERCENTILE_CONT(0.95) WITHIN GROUP (
        ORDER BY GREATEST(usage_prompt_tokens - usage_cached_tokens, 0) + usage_completion_tokens
    ) AS p95_effective,
    PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY usage_completion_tokens) AS p95_completion
FROM traces
WHERE request_started_at >= (now() - (%s || ' days')::interval)
GROUP BY token_fingerprint
HAVING COUNT(*) >= 5
"""

def compute_trace_level_baselines(rows: list[dict]) -> list[BaselineRow]:
    result: list[BaselineRow] = []
    for row in rows:
        result.append(
            BaselineRow(
                fingerprint_key=row["fingerprint_key"],
                metric_type="trace_effective_tokens_p95",
                metric_value=float(row["p95_effective"]),
                metadata_json={},
            )
        )
        result.append(
            BaselineRow(
                fingerprint_key=row["fingerprint_key"],
                metric_type="completion_tokens_p95",
                metric_value=float(row["p95_completion"]),
                metadata_json={},
            )
        )
    return result
```

```python
# workers/analysis_worker/repository.py
def analysis_context_for(self, job: TraceCapturedJob) -> AnalysisContext:
    if not job.token_fingerprint:
        return AnalysisContext()
    cursor = self.connection.cursor()
    cursor.execute(
        """
        SELECT metric_type, metric_value, metadata_json, computed_at
        FROM baseline_cache
        WHERE fingerprint_key = %s AND expires_at > now()
        """,
        (job.token_fingerprint,),
    )
    baseline_rows = cursor.fetchall()

    baseline_fields = {
        "trace_effective_tokens_p95": "trace_effective_tokens_p95",
        "completion_tokens_p95": "completion_tokens_p95",
    }

    baseline_kwargs: dict[str, object] = {}
    max_computed_at = None
    for metric_type, metric_value, _metadata_json, computed_at in baseline_rows:
        if metric_type in baseline_fields:
            baseline_kwargs[baseline_fields[metric_type]] = metric_value
        if computed_at is not None and (max_computed_at is None or computed_at > max_computed_at):
            max_computed_at = computed_at
    if max_computed_at is not None:
        baseline_kwargs["baseline_computed_at"] = max_computed_at.isoformat()
    return AnalysisContext(**baseline_kwargs)
```

```python
# workers/analysis_worker/offline.py
def run_offline_batch(connection, lookback_days: int = 7) -> dict:
    cursor = connection.cursor()
    cursor.execute(QUERY_TRACE_LEVEL, (str(lookback_days),))
    columns = ["fingerprint_key", "p95_effective", "p95_completion"]
    trace_rows = [dict(zip(columns, row)) for row in cursor.fetchall()]
    trace_baselines = compute_trace_level_baselines(trace_rows)
    upsert_baselines(connection, trace_baselines, ttl_hours=25)
    return {
        "fingerprints_processed": len({b.fingerprint_key for b in trace_baselines}),
        "baselines_written": len(trace_baselines),
    }
```

```python
# workers/analysis_worker/repository.py
baseline_fields = {
    "trace_effective_tokens_p95": "trace_effective_tokens_p95",
    "completion_tokens_p95": "completion_tokens_p95",
}
```

- [ ] **Step 4: Re-run the targeted tests and confirm they pass**

Run:

```bash
cd /Users/roy/codes/new-api-gateway/workers/analysis_worker
uv run pytest -q tests/test_baseline.py tests/test_repository.py tests/test_models.py -k "trace_effective or surviving_cost_fields"
```

Expected:

- PASS for the new baseline metric name
- PASS for reduced `AnalysisContext`

- [ ] **Step 5: Commit the baseline/context cleanup**

```bash
cd /Users/roy/codes/new-api-gateway
git add workers/analysis_worker/models.py workers/analysis_worker/baseline.py workers/analysis_worker/repository.py workers/analysis_worker/tests/test_baseline.py workers/analysis_worker/tests/test_repository.py workers/analysis_worker/tests/test_models.py
git commit -m "refactor(worker): switch cost baseline plumbing to effective tokens"
```

## Task 2: Rewrite Worker Anomaly Rules To The Approved Narrow Set

**Files:**
- Modify: `workers/analysis_worker/rules.py`
- Modify: `workers/analysis_worker/work_relevance.py`
- Modify: `workers/analysis_worker/tests/test_rules.py`
- Modify: `workers/analysis_worker/tests/test_work_relevance.py`

- [ ] **Step 1: Write the failing worker rule tests**

Add or update the worker rule tests to describe the final contract.

```python
# workers/analysis_worker/tests/test_rules.py
def test_detects_high_trace_tokens_from_effective_tokens():
    alerts = detect_anomalies(job(
        usage_prompt_tokens=50000,
        usage_cached_tokens=20000,
        usage_completion_tokens=12000,
        usage_total_tokens=62000,
    ))
    assert [alert.anomaly_type for alert in alerts] == ["high_trace_tokens"]
    assert alerts[0].observed_value == 42000
    assert alerts[0].threshold_value == 40000

def test_work_nonwork_conflict_is_review_only_and_not_an_anomaly():
    assessment = WorkRelevanceAssessment(
        trace_id="trace_conflict",
        task_category="job_search",
        work_related_score=0.5,
        personal_use_score=0.5,
        confidence=0.65,
        matched_context=[],
        evidence=[],
        needs_review=True,
        analyzer_version="test",
        decision="needs_review",
        recommended_action="review_conflict",
        score_breakdown={"work": 0.5, "non_work": 0.5, "risk": 0.0, "conflict": 0.5, "uncertainty": 0.35},
    )

    assert detect_work_relevance_anomalies(job(trace_id="trace_conflict"), assessment) == []

def test_explicit_non_work_collapses_to_non_work_use():
    assessment = WorkRelevanceAssessment(
        trace_id="trace_non_work",
        task_category="job_search",
        work_related_score=0.0,
        personal_use_score=0.9,
        confidence=0.9,
        matched_context=[],
        evidence=[{"reason": "Matched job_search terms: resume."}],
        needs_review=False,
        analyzer_version="test",
        decision="non_work_related",
        recommended_action="alert_non_work",
        score_breakdown={"work": 0.0, "non_work": 0.9, "risk": 0.0, "conflict": 0.0, "uncertainty": 0.1},
    )

    alerts = detect_work_relevance_anomalies(job(trace_id="trace_non_work"), assessment)

    assert [alert.anomaly_type for alert in alerts] == ["non_work_use"]
```

```python
# workers/analysis_worker/tests/test_work_relevance.py
def test_unknown_high_cost_no_longer_requires_review():
    assessment = classify_work_relevance(
        job(usage_total_tokens=25000),
        messages=[message("tell me something vague")],
        contexts=[],
    )

    assert assessment.decision == "unknown"
    assert assessment.recommended_action == "record_only"
    assert assessment.needs_review is False
```

- [ ] **Step 2: Run the targeted worker tests and confirm they fail**

Run:

```bash
cd /Users/roy/codes/new-api-gateway/workers/analysis_worker
uv run pytest -q tests/test_rules.py tests/test_work_relevance.py -k "high_trace_tokens or non_work_use or conflict or unknown_high_cost or long_output or off_hours"
```

Expected:

- FAIL because deleted anomaly types still exist
- FAIL because `high_trace_tokens` still uses `usage_total_tokens`
- FAIL because `review_high_cost_unknown` still exists

- [ ] **Step 3: Implement the narrowed worker rules**

Use these code shapes.

```python
# workers/analysis_worker/rules.py
HIGH_TRACE_TOKEN_THRESHOLD = 40_000
LONG_OUTPUT_TOKEN_THRESHOLD = 16_000
OFF_HOURS_TOKEN_THRESHOLD = 20_000

def _effective_tokens(job: TraceCapturedJob) -> int:
    return max(job.usage_prompt_tokens - job.usage_cached_tokens, 0) + job.usage_completion_tokens

def detect_anomalies(
    job: TraceCapturedJob,
    messages: list[NormalizedMessage] | None = None,
    context: AnalysisContext | None = None,
) -> list[AnomalyAlert]:
    context = context or AnalysisContext()
    alerts: list[AnomalyAlert] = []
    effective_tokens = _effective_tokens(job)

    trace_threshold = HIGH_TRACE_TOKEN_THRESHOLD
    trace_baseline = context.trace_effective_tokens_p95
    if trace_baseline is not None:
        trace_threshold = max(trace_baseline * 1.5, HIGH_TRACE_TOKEN_THRESHOLD)
    if effective_tokens >= trace_threshold:
        alerts.append(_anomaly(
            job,
            "high_trace_tokens",
            "medium",
            observed_value=effective_tokens,
            threshold_value=trace_threshold,
            reason=f"effective token usage reached {effective_tokens}, meeting or exceeding {trace_threshold:.0f}",
            baseline_value=trace_baseline,
        ))

    output_threshold = LONG_OUTPUT_TOKEN_THRESHOLD
    output_baseline = context.completion_tokens_p95
    if output_baseline is not None:
        output_threshold = max(output_baseline * 1.5, LONG_OUTPUT_TOKEN_THRESHOLD)
    if job.usage_completion_tokens >= output_threshold:
        alerts.append(_anomaly(
            job,
            "long_output_anomaly",
            "medium",
            observed_value=job.usage_completion_tokens,
            threshold_value=output_threshold,
            reason=f"completion tokens reached {job.usage_completion_tokens}, meeting or exceeding {output_threshold:.0f}",
            baseline_value=output_baseline,
        ))

    if _is_off_hours(job.request_started_at, context.local_timezone_offset_hours) and effective_tokens >= OFF_HOURS_TOKEN_THRESHOLD:
        alerts.append(_anomaly(
            job,
            "off_hours_high_usage",
            "medium",
            observed_value=effective_tokens,
            threshold_value=OFF_HOURS_TOKEN_THRESHOLD,
            reason=f"off-hours effective token usage reached {effective_tokens}, meeting or exceeding {OFF_HOURS_TOKEN_THRESHOLD}",
        ))

    return alerts
```

```python
# workers/analysis_worker/rules.py
def detect_work_relevance_anomalies(
    job: TraceCapturedJob,
    assessment: WorkRelevanceAssessment,
) -> list[AnomalyAlert]:
    action = getattr(assessment, "recommended_action", "")
    if action == "alert_non_work":
        return [_anomaly(
            job,
            "non_work_use",
            "high",
            observed_value=job.usage_total_tokens,
            threshold_value=0,
            reason=_work_relevance_reason(assessment, "trace was classified as explicit non-work use"),
        )]
    return []
```

```python
# workers/analysis_worker/work_relevance.py
VALID_ACTIONS = {
    ACTION_ALLOW,
    ACTION_ALERT_NON_WORK,
    ACTION_REVIEW_CONFLICT,
    ACTION_RECORD_ONLY,
}
VALID_DECISION_ACTIONS = {
    DECISION_WORK_RELATED: {ACTION_ALLOW},
    DECISION_NON_WORK_RELATED: {ACTION_ALERT_NON_WORK},
    DECISION_NEEDS_REVIEW: {ACTION_REVIEW_CONFLICT},
    DECISION_UNKNOWN: {ACTION_RECORD_ONLY},
}

def _decision_from_scores(job: TraceCapturedJob, score: dict[str, float]) -> tuple[str, str, bool, float]:
    if score["conflict"] >= 0.5:
        return DECISION_NEEDS_REVIEW, ACTION_REVIEW_CONFLICT, True, 0.65
    if score["risk"] >= 0.8:
        return DECISION_NON_WORK_RELATED, ACTION_ALERT_NON_WORK, False, score["risk"]
    if score["non_work"] >= 0.7:
        return DECISION_NON_WORK_RELATED, ACTION_ALERT_NON_WORK, False, score["non_work"]
    if score["work"] >= 0.7 and score["non_work"] < 0.3 and score["risk"] < 0.3:
        return DECISION_WORK_RELATED, ACTION_ALLOW, False, score["work"]
    return DECISION_UNKNOWN, ACTION_RECORD_ONLY, False, 0.25
```

```python
# workers/analysis_worker/rules.py
def _is_off_hours(value: str, offset_hours: int) -> bool:
    if not value:
        return False
    parsed = _parse_utc(value)
    if parsed is None:
        return False
    local_time = parsed.astimezone(timezone.utc) + timedelta(hours=offset_hours)
    return local_time.hour >= 23 or local_time.hour < 7
```

- [ ] **Step 4: Re-run the targeted worker tests and confirm they pass**

Run:

```bash
cd /Users/roy/codes/new-api-gateway/workers/analysis_worker
uv run pytest -q tests/test_rules.py tests/test_work_relevance.py -k "high_trace_tokens or non_work_use or conflict or unknown_high_cost or long_output or off_hours"
```

Expected:

- PASS for collapsed non-work anomaly type
- PASS for `work_nonwork_conflict` review-only behavior
- PASS for removed `unknown_high_cost`
- PASS for new effective-token and threshold semantics

- [ ] **Step 5: Commit the worker rule rewrite**

```bash
cd /Users/roy/codes/new-api-gateway
git add workers/analysis_worker/rules.py workers/analysis_worker/work_relevance.py workers/analysis_worker/tests/test_rules.py workers/analysis_worker/tests/test_work_relevance.py
git commit -m "refactor(worker): narrow anomaly rules to trusted signals"
```

## Task 3: Update Pipeline Expectations And Persisted Anomaly Shapes

**Files:**
- Modify: `workers/analysis_worker/tests/test_pipeline.py`

- [ ] **Step 1: Write the failing pipeline tests for persisted anomaly narrowing**

Update or add pipeline assertions like these.

```python
# workers/analysis_worker/tests/test_pipeline.py
def test_process_job_line_conflict_keeps_review_but_drops_conflict_anomaly(tmp_path: Path):
    repo = RecordingRepository(tmp_path)
    judge = FakeJudge({
        "decision": "needs_review",
        "recommended_action": "review_conflict",
        "task_category": "job_search",
        "confidence": 0.8,
    })

    response = process_job_line(_job_line(tmp_path), repo, llm_judge=judge)

    work_result = next(result for result in repo.results if result.category == "work_relevance")
    assert work_result.severity == "review"
    assert work_result.result["recommended_action"] == "review_conflict"
    assert all(alert.anomaly_type != "work_nonwork_conflict" for alert in repo.anomalies)
```

```python
def test_process_job_line_persists_non_work_use_only(tmp_path: Path):
    repo = RecordingRepository(tmp_path)
    response = process_job_line(_explicit_non_work_job_line(tmp_path), repo)

    assert response["anomaly_count"] == 1
    assert [alert.anomaly_type for alert in repo.anomalies] == ["non_work_use"]
```

- [ ] **Step 2: Run the targeted pipeline tests and confirm they fail**

Run:

```bash
cd /Users/roy/codes/new-api-gateway/workers/analysis_worker
uv run pytest -q tests/test_pipeline.py -k "conflict_keeps_review or non_work_use_only"
```

Expected:

- FAIL because the pipeline still persists `work_nonwork_conflict`
- FAIL because explicit non-work still persists old subtype names

- [ ] **Step 3: Make the pipeline assertions pass with minimal fixture updates**

Adjust the existing pipeline fixtures and assertions to match the new anomaly contract.

```python
# workers/analysis_worker/tests/test_pipeline.py
assert any(result.severity == "review" for result in repo.results if result.category == "work_relevance")
assert [alert.anomaly_type for alert in repo.anomalies] == ["non_work_use"]
```

Update any hard-coded `anomaly_count` expectations to the reduced rule set, especially cases that previously counted:

- `unknown_high_cost`
- `work_nonwork_conflict`
- identity noise anomalies
- daily/short-window/model/token-leak anomalies

- [ ] **Step 4: Re-run the targeted pipeline tests and confirm they pass**

Run:

```bash
cd /Users/roy/codes/new-api-gateway/workers/analysis_worker
uv run pytest -q tests/test_pipeline.py -k "conflict_keeps_review or non_work_use_only or llm"
```

Expected:

- PASS with review preserved on the work relevance result
- PASS with narrower persisted anomaly sets

- [ ] **Step 5: Commit the pipeline expectation updates**

```bash
cd /Users/roy/codes/new-api-gateway
git add workers/analysis_worker/tests/test_pipeline.py
git commit -m "test(worker): align pipeline expectations with anomaly cleanup"
```

## Task 4: Add Chinese Display Reasons To Admin Anomaly Responses

**Files:**
- Modify: `internal/admin/models.go`
- Create: `internal/admin/anomaly_reason.go`
- Create: `internal/admin/anomaly_reason_test.go`
- Modify: `internal/admin/handlers.go`
- Modify: `internal/adminui/app.js`

- [ ] **Step 1: Write the failing admin reason-formatting tests**

Create focused Go unit tests for formatted display reasons.

```go
// internal/admin/anomaly_reason_test.go
package admin

import "testing"

func TestFormatAnomalyDisplayReasonZH(t *testing.T) {
	item := AnomalySummary{
		AnomalyType:   "high_trace_tokens",
		ObservedValue: "48200",
		ThresholdValue:"40000",
	}
	got := formatAnomalyDisplayReasonZH(item)
	want := "本次请求有效 token 消耗 48,200，超过阈值 40,000。"
	if got != want {
		t.Fatalf("display reason = %q, want %q", got, want)
	}
}

func TestFormatAnomalyDisplayReasonZHFallsBackForUnknownType(t *testing.T) {
	item := AnomalySummary{
		AnomalyType: "unknown_type",
		Reason:      "raw fallback reason",
	}
	if got := formatAnomalyDisplayReasonZH(item); got != "raw fallback reason" {
		t.Fatalf("display reason = %q", got)
	}
}
```

- [ ] **Step 2: Run the targeted Go test and confirm it fails**

Run:

```bash
cd /Users/roy/codes/new-api-gateway
go test ./internal/admin -run 'TestFormatAnomalyDisplayReasonZH' -count=1
```

Expected:

- FAIL because `formatAnomalyDisplayReasonZH` and `display_reason` do not exist yet

- [ ] **Step 3: Implement API-side display reasons and UI consumption**

Add the display field and helper.

```go
// internal/admin/models.go
type AnomalySummary struct {
	AnomalyID          string `json:"anomaly_id"`
	AnomalyType        string `json:"anomaly_type"`
	Severity           string `json:"severity"`
	Status             string `json:"status"`
	Username           string `json:"username"`
	FingerprintDisplay string `json:"fingerprint_display"`
	ObservedValue      string `json:"observed_value"`
	ThresholdValue     string `json:"threshold_value"`
	Reason             string `json:"reason"`
	DisplayReason      string `json:"display_reason"`
	CreatedAt          string `json:"created_at"`
}
```

```go
// internal/admin/anomaly_reason.go
package admin

import "fmt"

func formatAnomalyDisplayReasonZH(item AnomalySummary) string {
	switch item.AnomalyType {
	case "high_trace_tokens":
		return fmt.Sprintf("本次请求有效 token 消耗 %s，超过阈值 %s。", formatDecimalForZH(item.ObservedValue), formatDecimalForZH(item.ThresholdValue))
	case "long_output_anomaly":
		return fmt.Sprintf("本次输出 token 为 %s，超过阈值 %s。", formatDecimalForZH(item.ObservedValue), formatDecimalForZH(item.ThresholdValue))
	case "off_hours_high_usage":
		return fmt.Sprintf("夜间时段（23:00-07:00）本次有效 token 消耗 %s，超过阈值 %s。", formatDecimalForZH(item.ObservedValue), formatDecimalForZH(item.ThresholdValue))
	case "non_work_use":
		return "检测到明确非工作用途内容。"
	default:
		return item.Reason
	}
}
```

```go
// internal/admin/handlers.go
func (h Handler) listAnomalies(w http.ResponseWriter, r *http.Request) {
	items, err := h.repo.ListAnomalies(r.Context(), 100)
	if err != nil {
		http.Error(w, "failed to list anomalies", http.StatusInternalServerError)
		return
	}
	for i := range items {
		items[i].DisplayReason = formatAnomalyDisplayReasonZH(items[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"anomalies": items})
}
```

```javascript
// internal/adminui/app.js
function renderAnomalies(body) {
  body = body || {};
  const rows = arrayValue(body.anomalies).map((item) => [
    item.anomaly_id,
    formatTime(item.created_at),
    badge(item.severity),
    item.anomaly_type,
    item.username || item.fingerprint_display,
    item.observed_value,
    item.display_reason || item.reason,
  ]);
  renderShell(page("异常", `<section class="panel">${table(["ID", "时间 (UTC+8)", "Severity", "类型", "员工", "观测值", "原因"], rows)}</section>`));
}
```

- [ ] **Step 4: Re-run the targeted Go test and a focused package test**

Run:

```bash
cd /Users/roy/codes/new-api-gateway
go test ./internal/admin -run 'TestFormatAnomalyDisplayReasonZH' -count=1
go test ./internal/admin -count=1
```

Expected:

- PASS for the formatter unit test
- PASS for the admin package after the extra `display_reason` field

- [ ] **Step 5: Commit the admin display-reason changes**

```bash
cd /Users/roy/codes/new-api-gateway
git add internal/admin/models.go internal/admin/anomaly_reason.go internal/admin/anomaly_reason_test.go internal/admin/handlers.go internal/adminui/app.js
git commit -m "feat(admin): render anomaly reasons in Chinese"
```

## Task 5: Update Docs And Run Final Verification

**Files:**
- Modify: `README.md`
- Modify: `ARCHITECTURE.md`

- [ ] **Step 1: Write the docs changes to match the narrowed anomaly model**

Update the anomaly descriptions to match the implementation.

```md
# README.md
- 异常菜单仅展示两类信号：明确非工作使用与明确高成本使用。
- trace 列表中的 `needs_review` 用于承载 `work_nonwork_conflict` 等待复核信号，不再重复出现在异常菜单。
- 成本异常基于 `effective_tokens = max(prompt_tokens - cached_tokens, 0) + completion_tokens`。
```

```md
# ARCHITECTURE.md
| `rules.py` | 精简后的异常规则：`non_work_use`、`high_trace_tokens`、`long_output_anomaly`、`off_hours_high_usage` |
| `work_relevance.py` | 明确非工作直接转为 `non_work_use`，冲突信号只保留 `needs_review` |
```

- [ ] **Step 2: Run focused worker and admin verification suites**

Run:

```bash
cd /Users/roy/codes/new-api-gateway/workers/analysis_worker
uv run pytest -q tests/test_baseline.py tests/test_repository.py tests/test_rules.py tests/test_work_relevance.py tests/test_pipeline.py
```

```bash
cd /Users/roy/codes/new-api-gateway
go test ./internal/admin/...
```

Expected:

- PASS for worker unit and pipeline tests affected by the anomaly cleanup
- PASS for admin backend tests after adding Chinese display reasons

- [ ] **Step 3: Run the project-level regression commands**

Run:

```bash
cd /Users/roy/codes/new-api-gateway
make test
```

Expected:

- PASS for the repository test suite

- [ ] **Step 4: Commit the docs and verification-aligned cleanup**

```bash
cd /Users/roy/codes/new-api-gateway
git add README.md ARCHITECTURE.md
git commit -m "docs: document narrowed anomaly menu semantics"
```

- [ ] **Step 5: Prepare the branch for review**

Run:

```bash
cd /Users/roy/codes/new-api-gateway
git status --short
git log --oneline -n 5
```

Expected:

- clean working tree
- recent commits for baseline cleanup, worker rule narrowing, admin Chinese reasons, and docs

## Self-Review

### Spec Coverage

- Rule deletion is covered by Task 2.
- `work_nonwork_conflict` review-only behavior is covered by Tasks 2 and 3.
- `non_work_use` collapse is covered by Task 2.
- `effective_tokens` adoption is covered by Tasks 1 and 2.
- Chinese anomaly reasons are covered by Task 4.
- Docs sync is covered by Task 5.

### Placeholder Scan

- No `TBD`, `TODO`, or deferred implementation markers remain in this plan.
- Every code-changing step includes concrete code.
- Every verification step includes an exact command and expected result.

### Type Consistency

- The remaining baseline field name is `trace_effective_tokens_p95` in the plan everywhere.
- The new non-work anomaly type is consistently `non_work_use`.
- The API presentation field is consistently `display_reason`.

Plan complete and saved to `docs/superpowers/plans/2026-06-03-anomaly-menu-rules-cleanup.md`. Two execution options:

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints

**Which approach?**
