# Trace Analysis Results Presentation Design

## Background

The trace detail page currently renders `analysis_results` as a generic table with shared columns for `analyzer_name`, `category`, `label`, `score`, `confidence`, `severity`, and `created_at`.

This creates a misleading presentation problem:

- `usage_extraction.score` is a factual quantity: `usage_total_tokens`
- `work_relevance.score` is a heuristic score: `work_related_score` in the `0..1` range

Because both appear under the same "分数" column, the UI implies they are comparable measurements when they are not.

## Problem Statement

The trace detail page needs to communicate analyzer results in a way that preserves the current backend contract while making each analyzer's semantics obvious to the reader.

The immediate source of confusion is the mismatch between:

- fact-style analyzers such as `usage_extraction`
- classification-style analyzers such as `work_relevance`

The current table optimizes for schema uniformity instead of user comprehension.

## Goals

- Remove the misleading impression that all analyzer scores are comparable
- Make `work_relevance` readable as a conclusion first, then a scoring breakdown
- Make `usage_extraction` readable as token facts, not as a rule score
- Keep raw diagnostic detail available without overwhelming the main view
- Limit this change to the trace detail page presentation layer

## Non-Goals

- Do not change worker scoring logic
- Do not change `analysis_results` storage schema
- Do not change the meaning of persisted `score` or `confidence`
- Do not redesign the trace list page
- Do not require backend API changes unless frontend parsing proves impossible with current payloads

## Current Data Semantics

### `usage_extraction`

Current worker behavior:

- `label` is `usage_from_gateway_job` or `usage_not_available`
- `score` is `float(job.usage_total_tokens)`
- `confidence` is `1.0` when usage exists, otherwise `0.0`
- `result_json` carries `prompt_tokens`, `completion_tokens`, `total_tokens`, `reasoning_tokens`, and `cached_tokens`

This is not a judgment score. It is a normalized fact record.

### `work_relevance`

Current worker behavior:

- top-level `score` is `work_related_score`
- `confidence` is the classifier confidence
- `label` is the selected task category
- `result_json` carries:
  - `task_category`
  - `work_related_score`
  - `personal_use_score`
  - `confidence`
  - `decision`
  - `recommended_action`
  - `needs_review`
  - `score_breakdown`
  - `matched_context`
  - `evidence`
  - optional LLM judge metadata

This is a classification result with a conclusion and a score breakdown.

## Proposed Direction

Replace the generic "分析结果" table in trace detail with analyzer-specific cards.

Each analyzer card should prioritize the information that best represents that analyzer's meaning. The UI should distinguish between:

- conclusion-oriented analyzers
- fact-oriented analyzers

## Information Architecture

### `work_relevance` card

Primary line:

- `decision + category`
- example: `work_related · debugging`
- example: `non_work_related · job_search`

Secondary line:

- `work / non-work / risk` breakdown from `result_json.score_breakdown`

Tertiary metadata:

- `confidence`
- `recommended_action`
- `created_at`

Badge behavior:

- show a `review` badge prominently when `needs_review=true` or top-level `severity=review`

Optional raw detail area:

- collapsed by default
- can expose `matched_context`, `evidence`, and raw JSON for investigation

### `usage_extraction` card

Primary line:

- `total tokens`

Secondary line:

- `input / output / cache / reasoning`

Tertiary metadata:

- availability wording derived from current payload
- example: `usage available`
- example: `usage missing`
- `created_at`

Confidence wording:

- do not visually frame this as a classifier confidence
- treat it as data availability state in the presentation copy

### Unknown or future analyzers

Render a generic fallback card when the analyzer is not explicitly recognized.

Fallback card fields:

- `analyzer_name`
- `category`
- `label`
- `score`
- `confidence`
- `severity`
- `created_at`

This preserves forward compatibility without blocking future analyzer rollout.

## Interaction Model

The card UI should remain simple and readable.

- No multi-level nested inspection UI
- No complex tabs inside cards
- No heavy expandable debug consoles by default

Default behavior:

- show the most important summary information immediately
- keep raw JSON or detailed evidence behind a lightweight `<details>` or similar expandable section

The main page should answer "what happened?" before "show me every field."

## Rendering Rules

### `work_relevance`

Preferred display mapping:

- title: `decision + category`
- subtitle: `work X / non-work Y / risk Z`
- metadata row: `confidence`, `recommended_action`, `time`
- badge: `review` when applicable

If `score_breakdown` is missing, fall back to:

- `work_related_score`
- `personal_use_score`
- risk defaults to `0`

If `decision` or `task_category` is missing, fall back to top-level `label`.

### `usage_extraction`

Preferred display mapping:

- title: `<total_tokens> total tokens`
- subtitle: `input <prompt> / output <completion> / cache <cached> / reasoning <reasoning>`
- metadata row: availability state, time

If fields are missing, fall back in this order:

1. `result_json.total_tokens`
2. top-level `score`
3. display `usage unavailable`

## Implementation Scope

This design changes only the trace detail frontend rendering.

In scope:

- parse existing `trace.analysis_results`
- replace table rendering in trace detail with analyzer cards
- add analyzer-specific formatting for `work_relevance` and `usage_extraction`
- preserve a fallback presentation for unknown analyzers

Out of scope:

- worker logic updates
- database migrations
- API contract changes
- list page redesign

## Testing Strategy

Frontend behavior should be covered at the presentation level.

Minimum verification:

- trace detail renders a `work_relevance` card with:
  - `decision + category`
  - breakdown line
  - review badge when applicable
- trace detail renders a `usage_extraction` card with token facts
- unknown analyzer still renders via fallback card
- missing optional fields degrade gracefully without breaking the page

Manual verification should confirm:

- no shared "分数" framing remains for heterogeneous analyzers
- `usage_extraction` reads like a usage fact
- `work_relevance` reads like a classification result

## Risks and Mitigations

### Risk: overloaded raw detail area

Mitigation:

- keep raw detail collapsed by default
- prioritize short summary copy in the visible card body

### Risk: analyzer-specific rendering grows ad hoc

Mitigation:

- structure rendering as a small formatter layer per analyzer type
- preserve one generic fallback path for unknown analyzers

### Risk: existing users rely on the old table density

Mitigation:

- keep each card compact
- keep important metadata visible without requiring expansion

## Recommendation

Proceed with analyzer-specific cards in trace detail.

The preferred `work_relevance` hierarchy is:

1. `decision + category`
2. `work / non-work / risk`
3. metadata and review status

The preferred `usage_extraction` hierarchy is:

1. `total tokens`
2. token component breakdown
3. availability and time

This resolves the core confusion without changing the underlying analysis model.
