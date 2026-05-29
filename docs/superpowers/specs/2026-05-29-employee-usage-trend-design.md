# Employee Usage Trend Design

Date: 2026-05-29

## Goal

Extend the existing admin usage page so an auditor can select an employee and see that employee's token and model usage over a recent time window.

The page should stay focused and operational: choose an employee, inspect the trend, filter by model, and use the summary table for exact input, output, cache, and total token values.

## Current State

The gateway and worker already persist the data needed for this view:

- `usage_aggregates.username`
- `usage_aggregates.model`
- `usage_aggregates.bucket_start`
- `usage_aggregates.bucket_size`
- `usage_aggregates.request_count`
- `usage_aggregates.prompt_tokens`
- `usage_aggregates.completion_tokens`
- `usage_aggregates.cached_tokens`
- `usage_aggregates.total_tokens`

The admin API already exposes `/admin/api/usage`, but the current page is a generic bucket table. It does not provide a focused employee trend view, fixed recent ranges, dynamic model filters, or chart-ready daily series.

## Approved UI Direction

The approved mockup is stored at:

- `docs/superpowers/mockups/employee-usage.html`

The usage page keeps the existing sidebar entry and becomes an employee-first view:

1. Top control: employee input and a "view" action.
2. Summary cards: request count, total tokens, input/output tokens, and cache tokens for the selected range.
3. Trend chart: daily token series for the selected employee.
4. Range filter: `1d`, `7d`, `30d`, defaulting to `30d`.
5. Model filter: model names are derived from models the employee used in the selected range. The model controls sit in the chart's upper-right area. Selecting a model filters both chart and table.
6. Model summary table: exact per-model request, input, output, cache, and total token values remain visible below the chart.

## Chart Semantics

The chart is a multi-series token trend:

- Blue solid line: `total_tokens`
- Green dashed line: `prompt_tokens` shown as `Input`
- Orange dashed line: `completion_tokens` shown as `Output`
- Purple dashed line: `cached_tokens` shown as `Cache`

All series are daily aggregates. For `1d`, the chart still uses daily aggregation and shows the current one-day range. If the selected range contains a single daily bucket, render it as a visible point marker with the same legend and table semantics.

The chart must not recompute total tokens from input and output. It uses the stored `total_tokens`.

## API Design

Extend the existing `/admin/api/usage` response rather than creating a separate page-specific endpoint.

Query parameters:

- `username`: optional for backward compatibility, required to populate `employee_usage`.
- `range`: one of `1d`, `7d`, `30d`; default `30d`.
- `model`: optional; when set, filters chart series and summary table to that model.

The handler should translate `range` into a bounded time window ending at `h.auth.now()`. All employee trend queries use `bucket_size = 'day'`.

Response shape:

```json
{
  "usage": [],
  "employee_usage": {
    "username": "E10001",
    "range": "30d",
    "selected_model": "",
    "models": ["gpt-5.2", "claude-4-sonnet"],
    "summary": {
      "request_count": 2418,
      "success_count": 2377,
      "error_count": 41,
      "prompt_tokens": 12400000,
      "completion_tokens": 5100000,
      "cached_tokens": 1100000,
      "total_tokens": 18600000
    },
    "daily": [
      {
        "bucket_start": "2026-05-29T00:00:00Z",
        "request_count": 183,
        "prompt_tokens": 812450,
        "completion_tokens": 367870,
        "cached_tokens": 104600,
        "total_tokens": 1284920
      }
    ],
    "model_summary": [
      {
        "model": "gpt-5.2",
        "request_count": 1184,
        "prompt_tokens": 7240320,
        "completion_tokens": 3128900,
        "cached_tokens": 712450,
        "total_tokens": 10369220
      }
    ]
  }
}
```

The existing `usage` bucket list can remain in the response for compatibility. The admin UI should use `employee_usage` for the new employee-focused view. When `username` is empty, the API keeps the current generic usage behavior and omits `employee_usage`.

## Repository Design

Add a repository method for employee usage trends. It should:

- Require a non-empty username for the employee trend repository method.
- Validate range values in the handler before calling the repository.
- Query `usage_aggregates` with `bucket_size = 'day'`.
- Apply `bucket_start >= start` and `bucket_start < end`.
- Group daily series by `bucket_start`.
- Group model summary by `model`.
- Return dynamic model names from the same bounded employee dataset.

The model list should be computed before applying the optional selected-model filter, so the UI can still show all models available in the selected employee/range while one model is active.

## Error Handling

- Missing `username` keeps the existing generic `/admin/api/usage` behavior and does not populate `employee_usage`.
- Invalid `range` falls back to `30d`.
- Unknown `model` returns empty filtered series and summary while keeping the dynamic model list.
- Database errors use the existing admin API `500` error path.

No raw evidence access is involved, and no plaintext API keys are handled by this feature.

## Testing

Focused coverage should include:

- Repository test verifies the trend query uses `usage_aggregates`, `bucket_size = 'day'`, username binding, bounded `bucket_start` filters, and split token columns.
- Handler test verifies `range` defaults to `30d`, accepts `1d/7d/30d`, and returns the `employee_usage` JSON key.
- UI smoke-level test or browser verification confirms selecting a range/model updates active controls and renders chart/table data from the API response.

Run the focused admin tests while developing and `make test` before completion.

## Documentation

After implementation, check `README.md`, `ARCHITECTURE.md`, and `CLAUDE.md` for any description of admin usage analytics. Update them if they still describe usage as only a generic aggregate table or total-only token view.

## Non-Goals

- No schema migration.
- No historical backfill.
- No per-hour trend control.
- No custom arbitrary date range picker.
- No new standalone "Employee Usage" navigation item.
- No cost calculation redesign.

## Acceptance Criteria

- The existing admin "用量" page lets an auditor enter/select an employee.
- The page defaults to the last 30 days and can switch to `1d` and `7d`.
- The chart renders total, input, output, and cache token series with a clear legend.
- Model names come from the selected employee's usage in the active range.
- Selecting a model filters chart and table data.
- The table shows exact model-level request, input, output, cache, and total token values.
- Existing admin usage and trace functionality remains intact.
