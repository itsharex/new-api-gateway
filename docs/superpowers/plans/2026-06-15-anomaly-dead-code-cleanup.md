# Anomaly Dead Code Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove residual dead code and outdated DB design left over from the anomaly detection relaxation — drop the unused `anomaly_rules` table, trim `AnalysisContext` to only the 3 fields actually read, remove wasted per-job SQL and offline baseline compute, and close the admin display gap for `multivariate_anomaly`.

**Architecture:** Pure deletion + one schema drop. No new behavior. Worker keeps its 5 live anomaly types (`high_trace_tokens`, `long_output_anomaly`, `off_hours_high_usage`, `non_work_use`, `multivariate_anomaly`); everything that feeds the 4 dead rule families (daily-limit / short-window / expensive-model / repeated-prompt / token-leak / identity-db / hourly+model baselines) goes away. One new migration `0019` drops the table; per CLAUDE.md rule, historical migrations 0004/0005/0009 are left untouched.

**Tech Stack:** Go (admin), Python 3.11 (worker, `uv`), PostgreSQL (migrations), Docker Compose (local verify).

**Suggested branch:** `feat/anomaly-dead-code-cleanup` (matches prior `feat/trace-message-cas` convention).

---

## File Structure

**Modified files:**
- `migrations/0019_drop_anomaly_rules.sql` — new, drops the unused table.
- `workers/analysis_worker/models.py` — trim `AnalysisContext` (93-122), drop `expensive_model_set()`.
- `workers/analysis_worker/repository.py` — `analysis_context_for` (121-223) loses 3 SQL queries and dead kwargs.
- `workers/analysis_worker/baseline.py` — drop `QUERY_HOURLY`, `QUERY_MODEL_HOURLY`, `compute_hourly_baselines`, `compute_model_baselines`.
- `workers/analysis_worker/offline.py` — `run_offline_batch` stops computing hourly + model baselines.
- `internal/admin/anomaly_reason.go` — add `multivariate_anomaly` case.
- `workers/analysis_worker/tests/test_models.py` — remove dead-field default tests (226-263).
- `workers/analysis_worker/tests/test_repository.py` — remove dead-field assertions; keep `trace_effective_tokens_p95` / `completion_tokens_p95` / `baseline_computed_at` (kept? see Task 2 note).
- `workers/analysis_worker/tests/test_rules.py` — remove `AnalysisContext(...)` kwargs referencing dead fields (244-249, 276-279).
- `workers/analysis_worker/tests/test_baseline.py` — remove `compute_model_baselines` tests (114-148), remove hourly-row fixtures.
- `workers/analysis_worker/tests/test_pipeline.py` — collapse tombstone list (267-281) to the still-relevant negatives.
- `internal/admin/anomaly_reason_test.go` — add `multivariate_anomaly` test case.

**Unchanged:** migrations 0004/0005/0009 (CLAUDE.md forbids rewriting published migrations); admin UI app.js; gateway Go code.

---

## Live anomaly_type inventory (post-cleanup target)

| anomaly_type | rule_key in DB? | emitter |
|---|---|---|
| `high_trace_tokens` | dropped (table gone) | `rules.py:46` |
| `long_output_anomaly` | dropped (table gone) | `rules.py:64` |
| `off_hours_high_usage` | dropped (table gone) | `rules.py:81` |
| `non_work_use` | was never in table | `rules.py:102` |
| `multivariate_anomaly` | was never in table | `isolation_forest.py:113` |

The `anomaly_rules` table has zero SELECT callers in Go or Python — thresholds in `rules.py` are hardcoded constants. Dropping the table loses nothing functional.

---

## Task 1: Migration 0019 — DROP anomaly_rules table

**Files:**
- Create: `migrations/0019_drop_anomaly_rules.sql`

- [ ] **Step 1: Write the migration**

Create `migrations/0019_drop_anomaly_rules.sql`:

```sql
-- 0019: drop unused anomaly_rules table
-- 该表在 0004/0005/0009 中被 INSERT，但 worker 和 admin 从未 SELECT。
-- 规则阈值在 workers/analysis_worker/rules.py 中以硬编码常量形式存在，
-- 与 anomaly_rules.threshold_json 完全脱钩。整表为死配置，直接 drop。

DROP TABLE IF EXISTS anomaly_rules;
```

- [ ] **Step 2: Verify migration applies cleanly**

Run:
```bash
docker compose -f deploy/docker-compose.yml --env-file .env.local --profile tools run --rm migrate
```
Expected: migration `0019_drop_anomaly_rules` applies; `schema_migrations` shows `0019` as current.

- [ ] **Step 3: Verify table is gone**

Run:
```bash
docker compose -f deploy/docker-compose.yml --env-file .env.local exec postgres psql -U audit -d audit_gateway -c "\dt anomaly_rules"
```
Expected: `Did not find any relation named 'anomaly_rules'.`

- [ ] **Step 4: Commit**

```bash
git add migrations/0019_drop_anomaly_rules.sql
git commit -m "feat(db): drop unused anomaly_rules table (migration 0019)"
```

---

## Task 2: Trim AnalysisContext dead fields + method

**Files:**
- Modify: `workers/analysis_worker/models.py:93-122`

The 3 fields actually read by `detect_anomalies` (`rules.py:40,58,76`):
- `trace_effective_tokens_p95: float | None`
- `completion_tokens_p95: float | None`
- `local_timezone_offset_hours: int = 8`

Everything else in the dataclass is dead.

- [ ] **Step 1: Replace AnalysisContext with trimmed version**

In `workers/analysis_worker/models.py`, replace the entire `AnalysisContext` class (lines 93-122) with:

```python
@dataclass(frozen=True)
class AnalysisContext:
    trace_effective_tokens_p95: float | None = None
    completion_tokens_p95: float | None = None
    local_timezone_offset_hours: int = 8
```

This deletes 20 dead fields and the `expensive_model_set()` method.

- [ ] **Step 2: Verify imports still resolve**

Run:
```bash
cd workers/analysis_worker && uv run python -c "from models import AnalysisContext; ctx = AnalysisContext(); print(ctx)"
```
Expected: prints `AnalysisContext(trace_effective_tokens_p95=None, completion_tokens_p95=None, local_timezone_offset_hours=8)` with no ImportError.

- [ ] **Step 3: Commit (will not pass full test suite yet — dependent tasks follow)**

```bash
git add workers/analysis_worker/models.py
git commit -m "refactor(worker): trim AnalysisContext to live fields only"
```

---

## Task 3: Trim repository.py analysis_context_for

**Files:**
- Modify: `workers/analysis_worker/repository.py:121-223`

After Task 2, `AnalysisContext` only takes 3 fields. `analysis_context_for` currently runs 4 SQL queries — keep only the `baseline_cache` read; drop daily/short-window/client-hash queries.

- [ ] **Step 1: Replace analysis_context_for body**

In `workers/analysis_worker/repository.py`, replace lines 121-223 (the entire `analysis_context_for` method) with:

```python
    def analysis_context_for(self, job: TraceCapturedJob) -> AnalysisContext:
        if not job.token_fingerprint:
            return AnalysisContext()
        if not _has_valid_timestamp(job.request_started_at):
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

        trace_effective_tokens_p95: float | None = None
        completion_tokens_p95: float | None = None
        for metric_type, metric_value, _metadata_json, _computed_at in baseline_rows:
            if metric_type == "trace_effective_tokens_p95":
                trace_effective_tokens_p95 = metric_value
            elif metric_type == "completion_tokens_p95":
                completion_tokens_p95 = metric_value

        return AnalysisContext(
            trace_effective_tokens_p95=trace_effective_tokens_p95,
            completion_tokens_p95=completion_tokens_p95,
            local_timezone_offset_hours=8,
        )
```

This removes: daily token sum query, short-window sum query, distinct client hash query, `baseline_fields` dict (9 entries), `model_baselines` dict construction, `baseline_computed_at` tracking, `trace_tokens_p95` alias.

- [ ] **Step 2: Verify import compiles**

Run:
```bash
cd workers/analysis_worker && uv run python -c "from repository import PostgresAnalysisRepository; print('ok')"
```
Expected: `ok`

- [ ] **Step 3: Commit**

```bash
git add workers/analysis_worker/repository.py
git commit -m "refactor(worker): drop wasted SQL from analysis_context_for"
```

---

## Task 4: Trim baseline.py + offline.py dead compute

**Files:**
- Modify: `workers/analysis_worker/baseline.py:14-49, 52-63, 88-100`
- Modify: `workers/analysis_worker/offline.py:100-120`

Keep `QUERY_TRACE_LEVEL` + `compute_trace_level_baselines` (these feed `trace_effective_tokens_p95` and `completion_tokens_p95`, which are still live). Drop hourly + model.

- [ ] **Step 1: Trim baseline.py**

In `workers/analysis_worker/baseline.py`:

(a) Delete `QUERY_HOURLY` (lines 14-24).
(b) Delete `QUERY_MODEL_HOURLY` (lines 39-49).
(c) Delete `compute_hourly_baselines` function (lines 52-63).
(d) Delete `compute_model_baselines` function (lines 88-100).

Leave `BaselineRow`, `QUERY_TRACE_LEVEL`, `compute_trace_level_baselines`, `upsert_baselines` intact.

- [ ] **Step 2: Trim offline.py run_offline_batch**

In `workers/analysis_worker/offline.py`, the `run_offline_batch` function (line 100+) currently has 4 stages (hourly, model, trace-level, isolation forest). Remove stages 1 and 2 (hourly + model). Replace the body from line 100 through the `upsert_baselines(connection, all_baselines, ttl_hours=25)` call with:

```python
def run_offline_batch(connection, lookback_days: int = 7) -> dict:
    cursor = connection.cursor()
    usage_aggregate_rows = _rebuild_usage_aggregates(connection)

    # 1. Compute trace-level baselines (feeds high_trace_tokens + long_output_anomaly)
    fact_trace_rows = load_trace_level_rows(connection, lookback_days)
    trace_baselines = compute_trace_level_baselines(fact_trace_rows)
    upsert_baselines(connection, trace_baselines, ttl_hours=25)

    fingerprints = set(b.fingerprint_key for b in trace_baselines)
```

Then keep the existing Isolation Forest block (was stage 4, becomes stage 2) unchanged.

Also remove `compute_hourly_baselines` and `compute_model_baselines` from the `from baseline import (...)` block at the top of `offline.py` if present (line 9 area — verify before editing).

- [ ] **Step 3: Verify imports**

Run:
```bash
cd workers/analysis_worker && uv run python -c "from baseline import compute_trace_level_baselines, upsert_baselines; from offline import run_offline_batch; print('ok')"
```
Expected: `ok`

- [ ] **Step 4: Commit**

```bash
git add workers/analysis_worker/baseline.py workers/analysis_worker/offline.py
git commit -m "refactor(worker): drop hourly+model baseline compute (unused)"
```

---

## Task 5: Add multivariate_anomaly case in admin

**Files:**
- Modify: `internal/admin/anomaly_reason.go:9-20`
- Modify: `internal/admin/anomaly_reason_test.go`

Currently `multivariate_anomaly` falls through to the `default` branch and renders the raw English `reason` from the worker. Add a Chinese display case consistent with the others.

- [ ] **Step 1: Add failing test**

Append to `internal/admin/anomaly_reason_test.go`:

```go
func TestFormatAnomalyDisplayReasonZHMultivariate(t *testing.T) {
	item := AnomalySummary{
		AnomalyType:   "multivariate_anomaly",
		ObservedValue: "1",
		Reason:        "Isolation Forest flagged this trace as a multivariate anomaly",
	}
	got := formatAnomalyDisplayReasonZH(item)
	want := "多变量异常检测标记本次请求为异常（Isolation Forest）。"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/admin/ -run TestFormatAnomalyDisplayReasonZHMultivariate -v`
Expected: FAIL — `got "Isolation Forest flagged this trace as a multivariate anomaly"`, want the Chinese string.

- [ ] **Step 3: Add the case**

In `internal/admin/anomaly_reason.go`, add before the `default:` line:

```go
	case "multivariate_anomaly":
		return "多变量异常检测标记本次请求为异常（Isolation Forest）。"
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/admin/ -run TestFormatAnomalyDisplayReasonZHMultivariate -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/admin/anomaly_reason.go internal/admin/anomaly_reason_test.go
git commit -m "feat(admin): add multivariate_anomaly display reason"
```

---

## Task 6: Clean test tombstones and dead-field tests

**Files:**
- Modify: `workers/analysis_worker/tests/test_models.py:226-263`
- Modify: `workers/analysis_worker/tests/test_repository.py` (multiple sites — see steps)
- Modify: `workers/analysis_worker/tests/test_rules.py:244-249, 276-279`
- Modify: `workers/analysis_worker/tests/test_baseline.py:1-20, 114-148`
- Modify: `workers/analysis_worker/tests/test_pipeline.py:267-281`

This is mechanical test cleanup — every assertion on a field that no longer exists must go.

- [ ] **Step 1: Fix test_models.py**

In `workers/analysis_worker/tests/test_models.py`:
- Delete the test that constructs `AnalysisContext(daily_token_limit=..., short_window_token_threshold=..., expensive_models=..., expensive_model_token_threshold=...)` (around lines 226-249).
- Delete `test_analysis_context_defaults_preserve_legacy_rule_fields` (lines 253-263).
- Keep any test that checks `trace_effective_tokens_p95` / `completion_tokens_p95` defaults.

If the file becomes empty of meaningful AnalysisContext tests, leave at least one smoke test:

```python
def test_analysis_context_defaults():
    ctx = AnalysisContext()
    assert ctx.trace_effective_tokens_p95 is None
    assert ctx.completion_tokens_p95 is None
    assert ctx.local_timezone_offset_hours == 8
```

- [ ] **Step 2: Fix test_repository.py**

In `workers/analysis_worker/tests/test_repository.py`:
- Lines 483-488: remove `trace_tokens_p95`, `daily_tokens_before`, `short_window_tokens_before`, `distinct_client_hashes_1h`, `baseline_computed_at` assertions. Keep `trace_effective_tokens_p95` and `completion_tokens_p95` assertions if present nearby.
- Lines 535-537, 705, 743, 745, 749, 751, 752, 763, 782, 844-846, 890: same treatment — remove assertions on dead fields, keep assertions on `trace_effective_tokens_p95` / `completion_tokens_p95`.
- For the `baseline_cache` row fixtures (e.g. line 719 `("off_hours_baseline", 400.0, {}, computed)`): drop rows for dead metric_types (`hourly_tokens_median`, `short_window_baseline`, `off_hours_baseline`, `model_hourly_median_*`). Keep rows for `trace_effective_tokens_p95`, `completion_tokens_p95`.
- If a whole test loses its purpose (e.g. a test that was specifically asserting `model_baselines` dict gets populated), delete the test.

- [ ] **Step 3: Fix test_rules.py**

In `workers/analysis_worker/tests/test_rules.py`:
- Line 244-249: the `aggregate_context = AnalysisContext(daily_tokens_before=..., short_window_tokens_before=..., distinct_client_hashes_1h=...)` construction must drop those kwargs. If the test was specifically asserting these *don't* fire anomalies (negative test), keep the test but simplify the construction to `AnalysisContext()`. Verify the test still asserts the original intent (e.g. no anomaly fired).
- Line 276-279: same treatment.

- [ ] **Step 4: Fix test_baseline.py**

In `workers/analysis_worker/tests/test_baseline.py`:
- Remove `compute_model_baselines` from the import block.
- Delete `test_compute_model_baselines_returns_one_row_per_input` (line 117) and `test_compute_model_baselines_returns_empty_for_no_rows` (line 147).
- Remove `compute_hourly_baselines` from imports if present, and delete its tests.
- Keep `compute_trace_level_baselines` tests.

- [ ] **Step 5: Fix test_pipeline.py tombstone list**

In `workers/analysis_worker/tests/test_pipeline.py:267-281`, the `assert not any(... anomaly_type in {10 dead keys} ...)` block. Replace with:

```python
    assert not any(
        alert.anomaly_type in {
            "identity_unresolved_success",
            "daily_token_limit_exceeded",
            "short_window_token_spike",
        }
        for alert in repo.anomalies
    )
```

Drop the entries that were never anomaly_type literals in the first place (`low_work_relevance_high_cost`, `non_work_high_risk`, `non_work_job_search`, `non_work_personal_use`, `non_work_side_business`, `unknown_high_cost`, `work_nonwork_conflict` — these are rule_keys or aspirational labels, not emitted types). Keep the 3 that *were* registered rule_keys for legacy anomaly types.

- [ ] **Step 6: Run full worker test suite**

Run: `cd workers/analysis_worker && uv run pytest -q`
Expected: all tests PASS, no `AttributeError` / unexpected keyword args.

- [ ] **Step 7: Commit**

```bash
git add workers/analysis_worker/tests/
git commit -m "test(worker): remove dead-field and tombstone assertions"
```

---

## Task 7: Full verification + docs sync

**Files:**
- Verify only (no edits unless something breaks).

- [ ] **Step 1: Run `make test`**

Run: `make test`
Expected: Node admin UI tests pass; Go `./...` tests pass.

- [ ] **Step 2: Run worker tests**

Run: `cd workers/analysis_worker && uv run pytest -q`
Expected: all pass.

- [ ] **Step 3: Spot-check via local compose**

Run:
```bash
make dev   # in one terminal, or start in background
# in another terminal, send a test request that should trigger high_trace_tokens:
bash scripts/smoke_proxy.sh
```
Expected: worker still emits `high_trace_tokens` for the high-token trace; admin `/admin` page still renders anomaly rows with Chinese display reasons.

- [ ] **Step 4: Sync docs**

Check `README.md`, `ARCHITECTURE.md`, `CLAUDE.md`, `AGENTS.md` for any mention of `anomaly_rules`, the 9 dead rule_keys, or the removed AnalysisContext fields. Update or remove stale references.

Run: `grep -n "anomaly_rules\|daily_token_limit\|short_window_token_spike\|expensive_model_overuse\|repeated_prompt\|possible_token_leak\|identity_db_error\|low_work_relevance_high_cost\|raw_only_large_response\|retry_storm_trace" README.md ARCHITECTURE.md CLAUDE.md AGENTS.md`
Expected: no matches (or only historical mentions that make sense in context).

- [ ] **Step 5: Commit docs if changed**

```bash
git add README.md ARCHITECTURE.md CLAUDE.md AGENTS.md
git commit -m "docs: sync after anomaly dead-code cleanup"
```

---

## Self-Review

**Spec coverage check** — every item from the user's "B" scope is covered:
- DROP anomaly_rules table → Task 1 ✓
- Remove dead AnalysisContext fields + expensive_model_set → Task 2 ✓
- Remove wasted SQL in repository.py → Task 3 ✓
- Remove hourly + model baseline compute → Task 4 ✓
- Add multivariate_anomaly admin case → Task 5 ✓
- Clean test tombstones → Task 6 ✓
- Verify + docs sync → Task 7 ✓

**Placeholder scan** — no TBD / "handle edge cases" / "similar to Task N". Every code step shows the actual code.

**Type consistency** — `trace_effective_tokens_p95` and `completion_tokens_p95` are the names used in `rules.py`, `repository.py`, `baseline.py`, and tests; they match throughout. `local_timezone_offset_hours` likewise.

**Risk note** — Task 6 step 2 (test_repository.py) is the messiest edit because dead-field assertions are interleaved with live-field assertions across many tests. The implementer should run `uv run pytest tests/test_repository.py -q` after each file edit to catch regressions immediately, not batch them.
