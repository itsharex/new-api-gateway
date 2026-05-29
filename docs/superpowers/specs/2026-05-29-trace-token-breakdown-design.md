# Trace Token Breakdown Design

Date: 2026-05-29

## Goal

Make new trace token usage visible as separate input, output, cached, and total token values in the admin trace and usage views.

This design does not backfill, migrate, or preserve historical display behavior. Historical data may remain incomplete or be deleted outside this work.

## Current State

The storage and worker contract already carry split token fields:

- `traces.usage_prompt_tokens`
- `traces.usage_completion_tokens`
- `traces.usage_cached_tokens`
- `traces.usage_reasoning_tokens`
- `traces.usage_total_tokens`
- `usage_aggregates.prompt_tokens`
- `usage_aggregates.completion_tokens`
- `usage_aggregates.cached_tokens`
- `usage_aggregates.total_tokens`

The Go trace model, Redis `TraceCapturedJob`, and Python worker aggregate model also already include these fields.

The visible gap is mainly in admin querying and UI display: trace lists, trace detail, and usage aggregates primarily expose a single total token value today.

The upstream new-api database is not a complete source for this feature. Its consume log model exposes prompt and completion token fields, but not a stable cached-token column. Cached token data should therefore come from the gateway-captured upstream response usage object, not from a post-hoc new-api DB lookup.

## Token Semantics

- Input token: `usage_prompt_tokens`
- Output token: `usage_completion_tokens`
- Cached token: `usage_cached_tokens`
- Total token: `usage_total_tokens`

Cached token means the upstream response explicitly reported cache-related input tokens. For example:

- OpenAI Chat: `usage.prompt_tokens_details.cached_tokens`
- OpenAI Responses: `usage.input_tokens_details.cached_tokens`
- Claude Messages: `usage.cache_read_input_tokens + usage.cache_creation_input_tokens`

The UI must not recompute total tokens from input and output. It displays the stored total value. If upstream usage is missing or malformed, all missing values remain `0`.

## Proposed Approach

Use the existing database columns and contracts. Extend the admin API and UI to expose the split fields, and fill any obvious extractor gaps where a supported upstream response already contains cached-token detail.

This keeps the work narrow:

- No migration is required.
- No historical backfill is required.
- No new dependency on new-api DB usage logs is introduced.
- No worker redesign is required.

## Components

### Gateway Extractors

Keep `minimalUsage` as the shared extraction result.

Audit the existing extractors and add cached-token parsing where the response schema already contains a stable field. Do not infer cached tokens from quota, total tokens, or provider-specific fields that do not clearly mean cache hit or cache creation.

The expected behavior is:

- OpenAI Chat and Responses continue to populate input, output, cached, reasoning, and total where available.
- Claude Messages continues to map cache read plus cache creation input tokens into cached tokens.
- Gemini and Images expose input/output/total where available; cached remains `0` unless a stable cached-token field exists in the response.
- Generic extractor may parse common OpenAI-compatible cached-token details if present, but should otherwise leave cached as `0`.

### Admin API

Extend admin models:

- `TraceSummary` includes `usage_prompt_tokens`, `usage_completion_tokens`, and `usage_cached_tokens`.
- `UsageBucket` includes `prompt_tokens`, `completion_tokens`, and `cached_tokens`.

Update repository queries:

- `ListTraces` selects and scans split token fields from `traces`.
- `GetTraceDetail` selects and scans the same fields; `TraceDetail` inherits them via `TraceSummary`.
- `ListUsage` selects and scans split token fields from `usage_aggregates`.

Existing total token fields remain unchanged.

### Admin UI

Update the built-in admin UI tables:

- Trace list columns: `Input`, `Output`, `Cached`, `Total`
- Usage page columns: `Input`, `Output`, `Cached`, `Total`
- Trace detail metadata: separate rows for input, output, cached, and total tokens

Do not add historical-data warnings, backfill controls, or compatibility toggles.

### Worker

Do not change the worker data flow. The worker already receives split token fields from the Go job and writes them into `usage_aggregates`.

Add or adjust tests only where useful to prove that prompt, completion, cached, and total tokens survive aggregation.

## Data Flow

1. Gateway proxies the request to new-api.
2. Gateway captures the upstream response body or SSE usage event.
3. Protocol extractor maps upstream usage fields into `minimalUsage`.
4. Gateway persists the split values to `traces`.
5. Gateway publishes the same split values in `TraceCapturedJob`.
6. Python worker aggregates the split values into hourly and daily `usage_aggregates`.
7. Admin API reads split values from `traces` and `usage_aggregates`.
8. Admin UI renders input, output, cached, and total tokens separately.

## Error Handling

- Invalid or missing upstream usage keeps token fields at `0`.
- Missing cached-token detail keeps cached tokens at `0`.
- Admin API query failures use the existing error paths.
- This work does not add new new-api DB queries, so it does not add cross-database runtime failure modes.

## Testing

Run focused tests while developing and `make test` before completion.

Coverage should include:

- Gateway extractor tests for input, output, cached, reasoning, and total token extraction where supported.
- Gateway proxy trace persistence test verifying split fields are stored and published.
- Admin repository tests verifying trace and usage queries select and scan split fields.
- Admin handler/API tests verifying JSON responses expose split fields.
- Worker pipeline test verifying aggregate deltas retain prompt, completion, cached, and total tokens.

## Documentation

Check `README.md` and `ARCHITECTURE.md` after implementation. Update them if they still describe token usage as a single total-only value.

## Non-Goals

- No historical backfill.
- No migration for new token columns.
- No new-api logs reconciliation.
- No cached-token inference when upstream does not explicitly report it.
- No redesign of anomaly detection thresholds.
- No UI controls for deleting historical data.

## Acceptance Criteria

- New OpenAI Chat, OpenAI Responses, and Claude traces show input, output, cached, and total token values in trace list, trace detail, and usage views.
- New Gemini and Images traces show input, output, and total values where available; cached is shown only when explicitly captured.
- Existing total-token behavior remains available.
- `make test` passes.
- Relevant documentation is checked and updated if needed.
