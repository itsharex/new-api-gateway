# Work Relevance Anomaly V2 Design

## Context

The current analysis worker already produces `WorkRelevanceAssessment` records and can raise
`low_work_relevance_high_cost` when a trace has both high token usage and a high personal-use
score. This is useful as a cost-control signal, but it is not enough for the product goal:
identify whether a user is doing work-related activity, and surface clearly non-work usage even
when the trace is small.

This design upgrades work relevance from a coarse classifier into an explainable decision system.
It uses the existing trace text and `context_catalog` entries as the initial source of work
context. It does not depend on user/team profiles, external issue trackers, PR systems, or an LLM
classifier in the first version.

## Goals

- Flag clearly non-work-related traces even when token usage is low.
- Send high-cost unknown traces to review without labeling them as non-work.
- Keep low-cost unknown traces recorded in `analysis_results` without alerting.
- Avoid alerting on traces that are clearly work-related.
- Preserve explainability: every decision must include scores, matched context, and evidence.
- Build a feedback path so administrator confirmations can improve future thresholds and evidence.

## Non-Goals

- Do not auto-punish users or block requests.
- Do not introduce external work-context integrations in this version.
- Do not train a supervised classifier until enough reviewed examples exist.
- Do not replace existing usage anomaly detection, baseline computation, or coverage alerts.

## Decision Semantics

`WorkRelevanceAssessment` should become the canonical decision object for work relevance. It should
keep the existing fields and add explicit decision fields:

- `task_category`: detected category, such as `coding`, `debugging`, `personal_chat`,
  `job_search`, `side_business`, or `policy_violation`.
- `work_related_score`: normalized work evidence score.
- `personal_use_score`: normalized non-work or personal-use evidence score.
- `confidence`: confidence in the final decision.
- `decision`: one of `work_related`, `non_work_related`, `needs_review`, or `unknown`.
- `recommended_action`: one of `allow`, `alert_non_work`, `review_high_cost_unknown`,
  `review_conflict`, or `record_only`.
- `matched_context`: matching `context_catalog` entries, including the match source and score.
- `evidence`: structured evidence items rather than only free-form strings.
- `score_breakdown`: work, non-work, risk, conflict, and uncertainty components.
- `needs_review`: kept for compatibility and derived from `decision` and `recommended_action`.

The alerting policy is:

- `decision=work_related`: do not alert.
- `decision=non_work_related`: always alert.
- `decision=unknown` with token usage above the configured review threshold: alert for review.
- `decision=unknown` with low token usage: record only.
- High-risk categories alert with high severity regardless of token usage.

## Classifier Architecture

The first version should be an explainable hybrid classifier with three stages.

### 1. Evidence Extraction

Extract evidence from normalized messages without making the final decision. Evidence types:

- Work context evidence: keyword, alias, name, or embedding match against active
  `context_catalog` entries.
- Work task evidence: coding, debugging, documentation, operations, data analysis, customer
  support, and similar work-oriented task patterns.
- Non-work evidence: personal life, entertainment, shopping, travel, relationship, gaming, casual
  chat, job search, resume, interview, side business, external customer, and non-company commercial
  work.
- High-risk evidence: security abuse, fraud, privacy misuse, policy bypass, credential misuse, and
  similar categories that should be reviewed independently of ordinary work relevance.
- Insufficient evidence: no normalized text, very short text, generic prompt, or no active
  `context_catalog` match.

Each evidence item should include:

- `kind`: work_context, work_task, non_work, high_risk, conflict, or insufficient.
- `category`: the more specific category.
- `weight`: contribution to scoring.
- `source`: keyword, context_catalog, embedding, heuristic, or fallback.
- `snippet` or `source_path`: a short explanation or normalized message location.
- `reason`: human-readable rationale.

### 2. Evidence Scoring

Convert evidence into normalized score components:

- Strong work context match contributes high work score.
- High embedding similarity with a catalog entry contributes high work score.
- Work task evidence without catalog context contributes medium work score.
- Clear personal, job-search, or side-business evidence contributes high non-work score.
- High-risk evidence contributes high risk score and can force alert severity upward.
- Simultaneous strong work and non-work evidence creates a conflict component rather than a clean
  allow decision.
- Insufficient evidence lowers confidence and can lead to `unknown`.

The scoring layer should start with transparent constants and unit tests. Administrator feedback can
later tune these constants before any model training is considered.

### 3. Decision Layer

The decision layer maps scores and token usage to a decision and recommended action:

- Strong work score and weak non-work score: `work_related / allow`.
- Strong non-work score: `non_work_related / alert_non_work`.
- Strong high-risk evidence: `non_work_related / alert_non_work`, severity `high`.
- Strong work and non-work evidence together: `needs_review / review_conflict`.
- Weak evidence and high token usage: `unknown / review_high_cost_unknown`.
- Weak evidence and low token usage: `unknown / record_only`.

The high-cost unknown threshold should be configurable and should default to the existing
work-relevance cost threshold of 20,000 tokens unless a better product threshold is chosen later.

## Alert Types and Severity

Work relevance should use more specific alert types than the current
`low_work_relevance_high_cost` rule:

- `non_work_personal_use`: personal life, entertainment, casual chat, travel, shopping, gaming.
- `non_work_job_search`: resumes, interview preparation, job applications, resignation planning.
- `non_work_side_business`: external clients, freelance work, private commercial projects.
- `non_work_high_risk`: security abuse, fraud, privacy misuse, policy bypass, credential misuse.
- `unknown_high_cost`: high-token trace without enough evidence to determine work relevance.
- `work_nonwork_conflict`: strong work and non-work evidence in the same trace.

Suggested severity:

- `high`: high-risk categories, side business, clear job-search activity, or high-token non-work
  traces.
- `medium`: ordinary personal use, entertainment, and low-token non-work traces.
- `review` or `medium`: unknown high-cost traces and work/non-work conflicts.

`low_work_relevance_high_cost` can remain during migration for compatibility, but new behavior
should prefer the more specific alert types.

## Persistence

`analysis_results` remains the full-fidelity storage location for the classifier output. Its
`result_json` should include:

- `decision`
- `recommended_action`
- `score_breakdown`
- `evidence`
- `matched_context`
- `needs_review`

`usage_anomalies` should only store traces that require administrator attention. Its `reason` should
be generated from structured evidence, for example:

> Detected job_search evidence from resume/interview terms; no active context_catalog entry matched;
> total_tokens=532.

The admin UI can later filter by `anomaly_type`, `severity`, `decision`, and category.

## Feedback Loop

The first implementation should reserve a feedback path for administrator review outcomes:

- `confirmed_work_related`
- `confirmed_non_work`
- `false_positive`
- `false_negative`
- optional reviewer note

Feedback should first be used for reporting, keyword updates, threshold tuning, and `context_catalog`
improvements. A supervised classifier or LLM-assisted review step should only be considered after
there is a meaningful reviewed sample set.

## Implementation Phases

### Phase 1: Core Recognition Loop

- Extend `WorkRelevanceAssessment` with decision, recommended action, evidence breakdown, and score
  breakdown fields.
- Refactor `work_relevance.py` into evidence extraction, scoring, and decision functions.
- Extend `rules.py` so clearly non-work traces alert regardless of token usage.
- Add `unknown_high_cost` behavior for high-token unknown traces.
- Keep `context_catalog` as the only work-context source.
- Preserve compatibility with existing work-relevance analysis results and tests.

### Phase 2: Feedback and Calibration

- Add or reuse admin review storage for work-relevance feedback.
- Add offline reporting for false positives, false negatives, and category distribution.
- Tune evidence weights, thresholds, and category patterns based on feedback.
- Consider a model-based second-stage classifier only after the feedback set is large enough.

## Testing Strategy

Unit tests should cover:

- Clear work-related trace with catalog match: no alert.
- Clear personal use with low token usage: alert.
- Clear job-search usage: alert with specific type.
- Clear side-business usage: alert with specific type.
- High-risk usage: high severity alert.
- Unknown low-cost trace: record only.
- Unknown high-cost trace: review alert.
- Work and non-work conflict: review alert.
- No normalized text: low confidence and unknown.

Pipeline tests should verify:

- `analysis_results.result_json` contains decision, recommended action, score breakdown, and
  structured evidence.
- `usage_anomalies` is written only for alert-worthy outcomes.
- Existing work relevance behavior still has a compatibility path.

E2E tests should include:

- Low-token non-work trace still alerts.
- High-token unknown trace alerts for review.
- Clear work-related trace does not alert.

## Rollout

Roll out as review-only behavior. The system should not block, punish, or automatically mark a user
as violating policy. Start with conservative severities, observe alert volume, then tune thresholds
and category weights using administrator feedback.
