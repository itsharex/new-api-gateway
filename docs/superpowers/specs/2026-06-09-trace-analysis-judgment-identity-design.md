# Trace Analysis Judgment Identity Design

## Background

The trace detail page already renders analyzer-specific cards for `work_relevance` and `usage_extraction`, but the presentation still exposes internal analyzer names as the primary identity of each card.

For traces that go through both:

- core-stage heuristic work relevance classification
- enrichment-stage LLM re-evaluation

the UI can display two cards with the same visible label: `WORK_RELEVANCE`.

This is technically accurate but operationally confusing. The target reader for the trace detail page is not expected to understand internal concepts such as:

- `analyzer_name`
- `stage`
- `producer`
- `result_key`

The page should answer a business question first:

- what did the system judge initially?
- what did it conclude after review?
- which result should the operator trust more?

## Problem Statement

When a trace has both a primary heuristic work relevance result and a secondary LLM-backed review result, the current UI does not clearly communicate:

- which card is the initial judgment
- which card is the reviewed judgment
- what their sequence is
- which card should be treated as the primary operational reference

This leads to a predictable support question: "Why are there two `WORK_RELEVANCE` cards and what do they mean?"

## Goals

- Keep both the initial and reviewed work relevance judgments visible
- Make their identity understandable in business language
- Preserve the sequence from initial judgment to reviewed judgment
- Visually emphasize the reviewed judgment as the primary operational reference
- Keep usage facts separate from work relevance judgments
- Preserve raw diagnostic detail for investigation without making it the default reading mode

## Non-Goals

- Do not change worker classification logic
- Do not change enrichment triggering rules
- Do not change `analysis_results` storage semantics
- Do not merge initial and reviewed judgments into one synthesized backend record
- Do not redesign unrelated trace detail sections

## Confirmed Product Constraints

These constraints were explicitly validated during brainstorming:

- The trace detail page is primarily for operations and audit readers
- The UI should keep the initial and reviewed judgments side by side
- Card identity should use business wording, not technical wording
- Card titles should lead with identity, while the subtitle should show the judgment result
- The default visible content should show human-readable conclusions plus one key numeric score
- The reviewed judgment should receive stronger visual emphasis than the initial judgment
- A "review-only" scenario is not a valid normal-path business case because enrichment is gated by the initial judgment flow

## Current Data Model Reality

The frontend currently recognizes cards mainly from `analyzer_name` and `result_json`.

For work relevance, the persistence layer already distinguishes result origin through backend fields such as:

- `stage`
- `producer`
- `result_key`

The intended identity mapping is:

- core + heuristic_work_relevance + work_relevance_primary -> initial judgment
- enrichment + llm_judge + work_relevance_secondary -> reviewed judgment

The trace detail page should not infer long-term business identity purely from prose fields inside `result_json` if the backend can return the explicit origin fields directly.

## Proposed Information Architecture

Replace internal analyzer-first labeling with business-first judgment cards.

The trace analysis section should render up to three primary cards in this order:

1. `初步判断`
2. `复核判断`
3. `用量信息`

### Visual emphasis

- `复核判断` remains in the second position for sequence clarity
- `复核判断` receives stronger visual emphasis
- `复核判断` includes an explicit marker such as `最终参考`
- `初步判断` remains visible but lower-emphasis
- `用量信息` remains separate and neutral

This preserves process order while still guiding the operator toward the reviewed result as the primary reference.

## Card Copy Model

### `初步判断`

Visible structure:

- title: `初步判断`
- subtitle: `结论 + 类别`
- body line 1: `建议：业务化动作文案`
- body line 2: `置信度：业务化等级 + 原始分数`

Example:

- title: `初步判断`
- subtitle: `未知 · 编码相关`
- body: `建议：仅记录`
- body: `置信度：低（0.25）`

### `复核判断`

Visible structure:

- title: `复核判断`
- marker: `最终参考`
- subtitle: `结论 + 类别`
- body line 1: `建议：业务化动作文案`
- body line 2: `置信度：业务化等级 + 原始分数`

Example:

- title: `复核判断`
- marker: `最终参考`
- subtitle: `工作相关 · 软件开发`
- body: `建议：允许`
- body: `置信度：高（0.95）`

### `用量信息`

Visible structure:

- title: `用量信息`
- subtitle: `<total tokens> 总 tokens`
- body line 1: `输入 X / 输出 Y`
- body line 2: `缓存命中 Z / 推理 R`

This card remains factual and separate from judgment semantics.

## Default vs Expanded Information

### Default visible content

Default visible card content should be optimized for fast operational reading:

- business identity
- human-readable result
- action recommendation
- one key numeric score

### Expanded content

Expanded card content should expose the more diagnostic fields:

- raw decision
- raw category
- score breakdown:
  - `work`
  - `non_work`
  - `risk`
- created time
- raw JSON

This keeps explainability available without forcing every reader into score interpretation mode.

## Identity Mapping Rules

The frontend should classify work relevance cards using explicit origin metadata returned by the trace detail API.

### Primary mapping

- `stage=core`, `producer=heuristic_work_relevance`, `result_key=work_relevance_primary`
  - render as `初步判断`
- `stage=enrichment`, `producer=llm_judge`, `result_key=work_relevance_secondary`
  - render as `复核判断`

### Normal-path assumptions

- `复核判断` should only appear when `初步判断` already exists
- the product does not treat "reviewed judgment without initial judgment" as a normal supported business path

### Defensive fallback

If historical or malformed data contains a work relevance result that cannot be safely classified into `初步判断` or `复核判断`, render a fallback business label:

- `工作相关性判断`

This protects the UI from mislabeling unknown data while preserving visibility.

## Ordering Rules

When both judgments exist, the display order must be:

1. `初步判断`
2. `复核判断`
3. `用量信息`

When only the initial judgment exists:

1. `初步判断`
2. `用量信息` if present

When no work relevance result exists:

- show `用量信息` if present
- otherwise show an empty-state message such as `暂无工作相关性分析结果`

The reviewed judgment should never be placed before the initial judgment, even when it is visually emphasized more strongly.

## Humanized Vocabulary

### Decision mapping

- `unknown` -> `未知`
- `work_related` -> `工作相关`
- `non_work_related` -> `非工作相关`

### Action mapping

- `allow` -> `允许`
- `record_only` -> `仅记录`
- `review_conflict` -> `需复核`
- `alert_non_work` -> `提醒非工作使用`

### Category mapping

Operator-facing categories should use friendly Chinese copy. Examples:

- `software_development` -> `软件开发`
- `coding` -> `编码相关`

If no explicit category mapping exists, fall back conservatively instead of exposing awkward internal code as the dominant visible label.

### Confidence wording

The UI should derive a simple confidence label from the numeric score:

- `confidence >= 0.80` -> `高`
- `0.50 <= confidence < 0.80` -> `中`
- `confidence < 0.50` -> `低`

The visible card should always show both:

- human label
- raw numeric confidence

## API and Presentation Contract Change

The trace detail API should expose the metadata required for stable identity mapping if it does not already do so in the frontend payload.

Preferred fields per analysis result:

- `analyzer_name`
- `category`
- `label`
- `score`
- `confidence`
- `severity`
- `result_json`
- `created_at`
- `stage`
- `producer`
- `result_key`

This is a presentation-support change, not a storage model redesign.

## Fallback and Error Handling

### Missing work relevance metadata

If `stage`, `producer`, or `result_key` is missing for a work relevance card:

- do not guess long-term identity from fragile heuristics
- render the card as `工作相关性判断`
- keep raw JSON available

### Missing business fields

If `decision`, `task_category`, or `recommended_action` is absent:

- fall back to conservative wording
- examples:
  - `结论待定`
  - `建议：仅记录`

### Multiple same-type records

If malformed or historical data contains multiple cards that map to the same visible business identity:

- choose one primary visible card using explicit priority
- place any remaining same-type records into expanded details

This prevents the operator from seeing several near-duplicate operational cards with no clear priority.

## Testing Strategy

Minimum automated coverage should include:

- initial judgment only
- initial judgment plus reviewed judgment
- no work relevance result
- missing origin metadata falling back to `工作相关性判断`
- business copy mapping for:
  - decision
  - action
  - category
- reviewed judgment emphasis marker rendering
- stable order:
  - `初步判断`
  - `复核判断`
  - `用量信息`

Explicitly excluded as a normal-path test case:

- reviewed judgment only

That case may be covered defensively if helpful, but it should not be treated as a primary product-flow scenario because enrichment is initiated by the initial judgment flow.

## Manual Acceptance Criteria

The redesign succeeds if an operations reader can open the trace detail page and immediately understand:

- what the system judged first
- what it concluded after review
- which judgment is the main operational reference
- what the usage facts were

without needing to understand internal analyzer terminology.

## Risks and Mitigations

### Risk: backend origin metadata is not available in the current payload

Mitigation:

- add `stage`, `producer`, and `result_key` to the trace detail API
- avoid building long-term UI identity rules on undocumented JSON inference

### Risk: too much emphasis on the reviewed judgment makes the initial judgment feel redundant

Mitigation:

- keep the initial judgment visible in sequence order
- use lighter styling rather than hiding it
- preserve process transparency for audit readers

### Risk: category translation becomes inconsistent over time

Mitigation:

- centralize decision/action/category formatter logic
- keep one conservative fallback path for unmapped values
