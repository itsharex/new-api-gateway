# Anomaly Menu Rules Cleanup Design

## Goal

Reduce noise in the admin anomaly menu so it only contains signals that are operationally worth acting on.

After this change, the anomaly menu should represent:

- clear non-work usage
- clear high-cost usage

It should no longer mix in:

- identity / linkage noise
- weak review-only signals
- broad low-confidence cost heuristics

## Current Problems

- The anomaly menu currently mixes strong signals and weak signals in the same list.
- Several anomaly types are operational noise for this product, especially identity and linkage related rules.
- Some cost anomalies are based on `usage_total_tokens`, which overstates real cost when cached tokens are high.
- Non-work anomalies are split into multiple subtypes even though the underlying classification is not reliable enough to justify product-level granularity.
- The admin anomaly list shows raw English reasons, which are less readable for Roy's workflow.

## Product Decision

The anomaly system is narrowed to two purposes:

1. Show clear non-work usage.
2. Show clear high-cost usage.

Trace review remains the place for uncertain or ambiguous signals.

## Final Rule Model

### Keep As Anomalies

The anomaly menu will only contain these four anomaly types:

- `non_work_use`
- `high_trace_tokens`
- `long_output_anomaly`
- `off_hours_high_usage`

### Keep As Trace Review Only

- `work_nonwork_conflict`

This signal should continue to produce `work_relevance.needs_review = true` so the trace appears with `needs_review`, but it must not insert a row into `usage_anomalies`.

### Delete Entirely

These rules are removed and no longer insert anomalies:

- `missing_username`
- `identity_unresolved_success`
- `invalid_username`
- `retry_storm_trace`
- `raw_only_large_response`
- `unknown_high_cost`
- `daily_token_limit_exceeded`
- `short_window_token_spike`
- `expensive_model_overuse`
- `possible_token_leak`

## Non-Work Anomaly Design

### Single Anomaly Type

All explicit non-work detections collapse into one anomaly type:

- `non_work_use`

The system may still classify a trace internally as `job_search`, `side_business`, `personal_chat`, `shopping`, `travel`, `entertainment`, or `policy_violation`, but those distinctions must not appear as separate anomaly types in `usage_anomalies`.

### Why Collapse Them

- Current category classification is not reliable enough to justify product-facing subtype precision.
- One anomaly type makes the menu and downstream counting much easier to trust.
- Detailed category context can still remain in `analysis_results.result.task_category`, `evidence`, and derived Chinese reason text.

## Cost Metric Design

### Effective Tokens

Cost-related anomaly rules must use `effective_tokens` instead of `usage_total_tokens`.

Definition:

```text
effective_tokens = max(prompt_tokens - cached_tokens, 0) + completion_tokens
```

For worker code, that means:

```text
effective_tokens = max(usage_prompt_tokens - usage_cached_tokens, 0) + usage_completion_tokens
```

### Why Effective Tokens

- `usage_total_tokens` includes cached tokens.
- Cached tokens can be high while actual marginal cost stays low.
- Roy wants cost anomalies to track real spend risk, not raw context volume.

## Cost Anomaly Rules

### `high_trace_tokens`

This remains a per-trace high-cost anomaly.

Observed metric:

- `effective_tokens`

Threshold:

- without baseline: `effective_tokens >= 40_000`
- with baseline: `effective_tokens >= max(trace_effective_p95 * 1.5, 40_000)`

Design note:

- The current `trace_tokens_p95` baseline is based on `usage_total_tokens`.
- It must be replaced by an equivalent baseline computed from `effective_tokens`.

### `long_output_anomaly`

This remains an extreme-output anomaly.

Observed metric:

- `usage_completion_tokens`

Threshold:

- without baseline: `completion_tokens >= 16_000`
- with baseline: `completion_tokens >= max(completion_p95 * 1.5, 16_000)`

Design note:

- This rule intentionally stays output-only.
- Cached input tokens do not affect this rule.

### `off_hours_high_usage`

This remains a night-time high-cost anomaly, but only for clear large usage.

Observed metric:

- `effective_tokens`

Night hours:

- local time `23:00` through before `07:00`

Threshold:

- `effective_tokens >= 20_000`

Design note:

- This rule does not use a personal baseline after the cleanup.
- It becomes a simple "night + large cost" check.

## Trace Review Behavior

The trace list review badge continues to represent uncertain signals.

After this cleanup:

- `work_nonwork_conflict` still sets `needs_review = true`
- trace list `needs_review` behavior remains based on `analysis_results.severity = 'review'`
- anomaly menu no longer duplicates that signal

This preserves analyst visibility without polluting the anomaly list.

## Admin Menu Behavior

The `/anomalies` page still reads from `usage_anomalies`, but the table should become much narrower in meaning.

Expected anomaly menu contents after cleanup:

- clear non-work usage
- extreme per-trace effective token usage
- extreme output length
- large night-time effective token usage

Expected exclusions:

- identity noise
- routing / raw capture noise
- vague review-only work relevance states
- broad daily / short-window / model heuristics

## Chinese Reason Rendering

The admin anomaly menu should display Chinese reason text.

### Rendering Strategy

- Keep `usage_anomalies.reason` as the stored raw rule/debug reason.
- Add display-layer Chinese reason rendering in the admin API or admin UI.
- Build Chinese reason text from structured fields such as:
  - `anomaly_type`
  - `observed_value`
  - `threshold_value`
  - `baseline_value`
  - time-window fields when relevant
- For `non_work_use`, optionally incorporate `analysis_results.result.task_category` or evidence details to produce a better Chinese explanation.

### Why Display-Layer Translation

- It avoids coupling worker rule behavior to presentation wording.
- It keeps tests for anomaly generation focused on rule semantics rather than copy text.
- It allows future wording changes without touching worker persistence behavior.

### Example Chinese Reasons

- `high_trace_tokens`: `本次请求有效 token 消耗 48,200，超过阈值 40,000。`
- `long_output_anomaly`: `本次输出 token 为 18,300，超过阈值 16,000。`
- `off_hours_high_usage`: `夜间时段（23:00-07:00）本次有效 token 消耗 22,500，超过阈值 20,000。`
- `non_work_use`: `检测到明确非工作用途内容。`

## Data And Implementation Implications

### Worker Rule Changes

- Remove generation of all deleted anomaly types.
- Change work relevance anomaly mapping so explicit non-work outcomes produce `non_work_use`.
- Keep `work_nonwork_conflict` as review-only and stop inserting a conflict anomaly row.
- Introduce shared `effective_tokens` calculation for cost anomaly evaluation.

### Baseline Changes

- Replace total-token-based trace baseline usage for `high_trace_tokens` with an effective-token-based baseline.
- Keep completion baseline logic for `long_output_anomaly`.
- `off_hours_high_usage` no longer depends on personal off-hours baseline.

### Admin Rendering Changes

- Add Chinese reason rendering for anomaly list display.
- Ensure the anomaly menu reflects the reduced anomaly type set cleanly.

## Testing Requirements

### Worker Rule Tests

Update `workers/analysis_worker/tests/test_rules.py` to cover:

- deleted rules no longer produce anomalies
- `work_nonwork_conflict` no longer inserts an anomaly
- explicit non-work classifications now produce `non_work_use`
- `high_trace_tokens` uses `effective_tokens`
- `long_output_anomaly` uses the new higher threshold
- `off_hours_high_usage` uses `23:00-07:00` and `effective_tokens >= 20_000`

### Pipeline Tests

Update `workers/analysis_worker/tests/test_pipeline.py` to cover:

- final persisted anomaly type set is reduced
- `work_nonwork_conflict` still appears as `needs_review` in work relevance analysis result
- `work_nonwork_conflict` no longer appears in persisted anomalies

### Admin UI / API Tests

Add or update tests so the anomaly menu displays Chinese reason text for supported anomaly types.

## Documentation Requirements

Update these documents if implementation changes their described behavior:

- `/Users/roy/codes/new-api-gateway/README.md`
- `/Users/roy/codes/new-api-gateway/ARCHITECTURE.md`

At minimum, document:

- anomaly menu now represents only clear non-work and clear high-cost signals
- trace review remains the place for uncertain signals
- cost anomaly rules use `effective_tokens`

## Out Of Scope

This design does not introduce:

- new admin filters or grouping UI
- runtime rule toggling via `anomaly_rules`
- new review workflows
- migration of historical anomaly rows

## Success Criteria

- The anomaly menu contains only four anomaly types: `non_work_use`, `high_trace_tokens`, `long_output_anomaly`, `off_hours_high_usage`.
- `work_nonwork_conflict` remains visible through trace review but no longer appears in anomalies.
- Removed anomaly types are no longer persisted by the worker.
- Cost anomalies no longer use cached tokens as part of their primary spend signal.
- The anomaly menu displays Chinese reason text by default.
