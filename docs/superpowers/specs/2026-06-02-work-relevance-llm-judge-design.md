# Work Relevance LLM Judge Design

## Objective

Improve work relevance classification quality by replacing the current embedding-based semantic match with a conservative rule-and-LLM judge pipeline.

The new classifier must:

- Remove the embedding service and per-trace embedding calls.
- Preserve the existing rule-based anomaly detection path.
- Use `context_catalog` as a work-context knowledge source.
- Use non-work rules as a separate policy layer.
- Call an external vLLM OpenAI-compatible endpoint only for ambiguous or high-value cases.
- Produce the existing `WorkRelevanceAssessment` shape so current anomaly conversion remains compatible.

## Non-Goals

- Do not deploy a local LLM service from this repository.
- Do not add an `llm-judge` service to Docker Compose.
- Do not let the LLM write `usage_anomalies` directly.
- Do not keep embedding as a fallback or optional primary path.
- Do not redesign the existing usage anomaly rules in this phase.

## Current Problem

The current embedding flow turns all normalized trace text into one BGE-M3 vector, then compares it with `context_catalog.embedding`.

This is weak for the target use case:

- Short catalog anchors like `ios`, `xwallet app`, and `联通项目` are better handled by exact or alias matching than vector similarity.
- Long trace context can dilute the actual user intent into a broad average vector.
- The project does not have a clear production path that populates `context_catalog.embedding`.
- CPU usage can be high even when semantic matches provide little value.

## High-Level Flow

```text
normalized messages
  -> extract user/developer intent text
  -> match non-work policy rules
  -> match context_catalog strong and weak terms
  -> apply token-cost gate
  -> optionally call external vLLM judge
  -> build WorkRelevanceAssessment
  -> existing rules.py converts assessment to usage_anomalies
```

## Context Catalog Semantics

`context_catalog` represents work contexts only.

Recommended use:

- `aliases`: strong work-context matches, such as project names, internal codenames, customer names, and product names.
- `keywords`: weak or medium matches, such as technologies, business concepts, and domain terms.
- `description`: human-readable business context used in LLM judge prompts.
- Future optional `boundaries`: work/non-work boundary notes for the LLM, such as "resume or interview preparation is not work-related even if it mentions this project."

Non-work categories do not belong in `context_catalog`.

## Non-Work Policy Rules

Non-work detection is a separate policy layer.

Initial categories remain aligned with current `work_relevance.py` behavior:

- `job_search`
- `personal_chat`
- `entertainment`
- `shopping`
- `travel`
- `side_business`
- `policy_violation`

Non-work strong signals are evaluated before catalog short-circuiting. If a trace mentions a work project and also contains strong non-work evidence, the result is a conflict and should go to the LLM judge when available.

## Token Cost Tiers

Classification decisions use `usage_total_tokens`:

- `low`: `< 2,000`
- `medium`: `2,000 <= tokens < 20,000`
- `high`: `>= 20,000`

The existing high-cost threshold of `20,000` remains compatible with current `rules.py` behavior.

## LLM Trigger Policy

The LLM judge is conservatively triggered.

Short-circuit without LLM:

- Strong catalog match, no non-work conflict, low or medium cost: `work_related`.
- Strong non-work match, no catalog conflict: `non_work_related`.
- Low cost with no meaningful signals: `unknown` / `record_only`.

Call LLM:

- Catalog and non-work signals conflict.
- Only weak catalog signals exist on medium or high cost traces.
- High cost trace has no strong work evidence.
- Rules produce a low-confidence or ambiguous assessment that is worth review.

## LLM Input

The LLM judge sees only user-side intent, not assistant response text.

Included:

- User/developer message excerpts after truncation.
- Trace metadata needed for decision context: model, route, token tier, and protocol family.
- Strong catalog matches.
- Weak catalog candidates.
- Non-work signals.
- Relevant catalog descriptions.
- A system instruction that trace content is untrusted data and must not override judge instructions.

Excluded:

- Assistant response content.
- Raw full request or response bodies.
- Plaintext API keys or other secrets.

## Truncation

The first implementation should keep truncation simple and explicit:

- Prefer request-side user/developer content.
- Ignore assistant content.
- Cap the total extracted text length.
- Preserve snippets used as evidence.
- Record truncation metadata in the assessment result when truncation occurs.

The truncation policy can be tuned later, but it must avoid sending very long raw model contexts to the judge.

## External vLLM API

The worker calls an external OpenAI-compatible vLLM endpoint.

Configuration:

```text
LLM_JUDGE_BASE_URL
LLM_JUDGE_MODEL
LLM_JUDGE_API_KEY optional
LLM_JUDGE_TIMEOUT_SECONDS
```

The repository does not manage the model deployment.

Recommended request behavior:

- `temperature: 0`
- Small bounded `max_tokens`, such as 512-800
- JSON-only response prompt
- Explicit timeout
- Schema validation before accepting output

## LLM Output Contract

The LLM returns structured JSON that is adapted into `WorkRelevanceAssessment`.

Expected logical fields:

```json
{
  "decision": "work_related | non_work_related | needs_review | unknown",
  "task_category": "coding",
  "matched_context": [
    {
      "type": "project",
      "name": "XWallet App",
      "source": "llm_judge"
    }
  ],
  "confidence": 0.82,
  "recommended_action": "allow | alert_non_work | review_conflict | review_high_cost_unknown | record_only",
  "evidence": [
    {
      "kind": "work_context",
      "source": "llm_judge",
      "snippet": "...",
      "reason": "..."
    }
  ]
}
```

The adapter clamps scores, validates enums, and derives:

- `work_related_score`
- `personal_use_score`
- `needs_review`
- `score_breakdown`

Invalid LLM output is treated like LLM unavailability.

## Failure Policy

LLM failures do not fail the Redis job.

Failure examples:

- Connection failure
- Timeout
- HTTP error
- Invalid JSON
- Invalid schema

Conservative fallback:

- Conflict case: `decision=needs_review`, `recommended_action=review_conflict`.
- High cost without strong work evidence: `decision=unknown`, `recommended_action=review_high_cost_unknown`.
- Low or medium weak-signal case: fall back to rules; if still unclear, `record_only`.

Fallback evidence must include:

```json
{
  "kind": "llm_unavailable",
  "source": "llm_judge",
  "reason": "LLM judge unavailable; applied conservative fallback."
}
```

## Anomaly Compatibility

The LLM judge does not generate `AnomalyAlert`.

It only produces `WorkRelevanceAssessment`. Current anomaly generation remains:

```python
anomalies = [
    *detect_anomalies(job, messages, analysis_context),
    *detect_work_relevance_anomalies(job, work_relevance),
]
```

This preserves:

- Existing identity, token, cost, model, repeated prompt, off-hours, token leak, response size, and retry-storm rules.
- Existing `usage_anomalies` schema.
- Existing `detect_work_relevance_anomalies()` mapping from `decision` and `recommended_action` to anomaly types.

## Observability

LLM fallback must be visible.

Each fallback records:

- Assessment evidence with `kind=llm_unavailable`.
- Worker heartbeat metadata, such as:

```json
{
  "llm_judge_status": "degraded",
  "llm_judge_error_type": "timeout",
  "llm_judge_fallback_count": 1,
  "trace_id": "..."
}
```

Future readiness or metrics work can derive an LLM degraded signal from heartbeat metadata. The first implementation should at least write the metadata so operations can inspect degraded behavior.

## Embedding Removal

Remove the embedding path from this project:

- Delete `deploy/embedding/`.
- Remove the `embedding` Docker Compose service.
- Remove `analysis-worker` dependency on `embedding`.
- Remove `EMBEDDING_URL`.
- Remove `workers/analysis_worker/embedding_client.py`.
- Remove `classify_work_relevance_with_embeddings`.
- Remove tests that only validate embedding client behavior.
- Update README and ARCHITECTURE deployment documentation.

The historical `context_catalog.embedding` database column can remain unused in this phase to avoid a risky schema rollback. Dropping it can be a separate migration later.

## Testing Strategy

Unit tests:

- Strong catalog match short-circuits to `work_related`.
- Strong non-work match short-circuits to `non_work_related`.
- Catalog/non-work conflict calls the LLM judge.
- High cost without strong work evidence calls the LLM judge.
- LLM valid JSON maps to `WorkRelevanceAssessment`.
- LLM timeout falls back conservatively.
- LLM invalid JSON falls back conservatively.
- Assistant response text is excluded from judge input.
- Existing `detect_anomalies()` tests continue to pass.
- Existing `detect_work_relevance_anomalies()` behavior remains compatible.

Integration tests:

- Worker processes a Redis job without requiring an embedding service.
- Worker persists one `work_relevance` analysis result.
- Existing rule-based anomalies still persist.
- LLM fallback metadata is recorded in worker heartbeat.

Documentation checks:

- README no longer describes embedding as a required service.
- ARCHITECTURE describes the external LLM judge configuration and the preserved anomaly conversion path.

## Rollout Notes

Because this change removes a service and changes classification behavior, rollout should be staged:

1. Deploy with LLM judge endpoint configured.
2. Verify worker no longer waits for embedding.
3. Verify strong catalog and non-work short-circuits.
4. Verify LLM fallback evidence and heartbeat metadata.
5. Compare work relevance outputs against recent production traces before treating alerts as final policy decisions.
