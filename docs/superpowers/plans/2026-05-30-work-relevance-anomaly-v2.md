# Work Relevance Anomaly V2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Upgrade work relevance from a coarse high-cost signal into an explainable classifier that alerts on clearly non-work traces, reviews high-cost unknown traces, and records low-cost unknown traces.

**Architecture:** Extend `WorkRelevanceAssessment` with explicit decision/action fields, then refactor `work_relevance.py` into evidence extraction, scoring, and decision helpers. `rules.py` converts those decisions into specific `usage_anomalies` while `analysis_results.result_json` keeps the full assessment for audit and future feedback.

**Tech Stack:** Python 3.11, dataclasses, pytest, Redis worker pipeline, PostgreSQL persistence through existing repository code.

---

## File Structure

- Modify `workers/analysis_worker/models.py`: add compatibility-safe fields to `WorkRelevanceAssessment` and serialize them into `AnalysisResult.result`.
- Modify `workers/analysis_worker/work_relevance.py`: add evidence extraction, scoring, and decision helpers while preserving the existing public functions.
- Modify `workers/analysis_worker/rules.py`: replace the token-only work relevance alert with decision/action based alert mapping.
- Modify `workers/analysis_worker/tests/test_models.py`: cover the extended assessment serialization.
- Modify `workers/analysis_worker/tests/test_work_relevance.py`: cover personal, job-search, side-business, high-risk, unknown, conflict, and embedding paths.
- Modify `workers/analysis_worker/tests/test_rules.py`: cover new work relevance alert types and preserve compatibility where needed.
- Modify `workers/analysis_worker/tests/test_pipeline.py`: verify `analysis_results` and `usage_anomalies` behavior in the worker pipeline.
- Modify `e2e/test_worker_work_relevance.py`: add low-token non-work, high-token unknown, and clear-work scenarios.
- Modify `ARCHITECTURE.md`: update the worker file description and pipeline behavior.

## Constants and Names

Use these exact strings consistently:

```python
DECISION_WORK_RELATED = "work_related"
DECISION_NON_WORK_RELATED = "non_work_related"
DECISION_NEEDS_REVIEW = "needs_review"
DECISION_UNKNOWN = "unknown"

ACTION_ALLOW = "allow"
ACTION_ALERT_NON_WORK = "alert_non_work"
ACTION_REVIEW_HIGH_COST_UNKNOWN = "review_high_cost_unknown"
ACTION_REVIEW_CONFLICT = "review_conflict"
ACTION_RECORD_ONLY = "record_only"

NON_WORK_HIGH_COST_THRESHOLD = 20_000
UNKNOWN_HIGH_COST_THRESHOLD = 20_000
```

Alert types:

```python
"non_work_personal_use"
"non_work_job_search"
"non_work_side_business"
"non_work_high_risk"
"unknown_high_cost"
"work_nonwork_conflict"
"low_work_relevance_high_cost"
```

---

### Task 1: Extend the Work Relevance Assessment Model

**Files:**
- Modify: `workers/analysis_worker/models.py`
- Modify: `workers/analysis_worker/tests/test_models.py`

- [ ] **Step 1: Write the failing model serialization test**

Append this test to `workers/analysis_worker/tests/test_models.py`:

```python
def test_work_relevance_assessment_serializes_v2_decision_fields():
    assessment = WorkRelevanceAssessment(
        trace_id="trace_non_work",
        task_category="job_search",
        work_related_score=0.05,
        personal_use_score=0.92,
        confidence=0.88,
        matched_context=[],
        evidence=[{
            "kind": "non_work",
            "category": "job_search",
            "weight": 0.9,
            "source": "keyword",
            "snippet": "resume interview",
            "reason": "Matched job-search terms: resume, interview.",
        }],
        needs_review=True,
        analyzer_version="work_relevance_mvp_2026_04_28",
        decision="non_work_related",
        recommended_action="alert_non_work",
        score_breakdown={
            "work": 0.0,
            "non_work": 0.9,
            "risk": 0.0,
            "conflict": 0.0,
            "uncertainty": 0.1,
        },
    )

    result = assessment.to_analysis_result()

    assert result.category == "work_relevance"
    assert result.label == "job_search"
    assert result.severity == "review"
    assert result.result["decision"] == "non_work_related"
    assert result.result["recommended_action"] == "alert_non_work"
    assert result.result["score_breakdown"]["non_work"] == 0.9
    assert result.result["evidence"][0]["category"] == "job_search"
```

- [ ] **Step 2: Run the model test and verify it fails**

Run:

```bash
cd workers/analysis_worker && uv run pytest -q tests/test_models.py::test_work_relevance_assessment_serializes_v2_decision_fields
```

Expected: FAIL because `WorkRelevanceAssessment.__init__()` does not accept `decision`, `recommended_action`, or `score_breakdown`.

- [ ] **Step 3: Extend `WorkRelevanceAssessment` with compatibility defaults**

In `workers/analysis_worker/models.py`, replace the `WorkRelevanceAssessment` class with:

```python
@dataclass(frozen=True)
class WorkRelevanceAssessment:
    trace_id: str
    task_category: str
    work_related_score: float
    personal_use_score: float
    confidence: float
    matched_context: list[dict[str, Any]]
    evidence: list[Any]
    needs_review: bool
    analyzer_version: str
    decision: str = "unknown"
    recommended_action: str = "record_only"
    score_breakdown: dict[str, float] | None = None

    def to_analysis_result(self) -> AnalysisResult:
        score_breakdown = self.score_breakdown or {
            "work": self.work_related_score,
            "non_work": self.personal_use_score,
            "risk": 0.0,
            "conflict": 0.0,
            "uncertainty": max(0.0, 1.0 - self.confidence),
        }
        return AnalysisResult(
            trace_id=self.trace_id,
            analyzer_name="work_relevance",
            analyzer_version=self.analyzer_version,
            policy_version="",
            category="work_relevance",
            label=self.task_category,
            score=self.work_related_score,
            confidence=self.confidence,
            severity="review" if self.needs_review else "",
            result={
                "task_category": self.task_category,
                "work_related_score": self.work_related_score,
                "personal_use_score": self.personal_use_score,
                "confidence": self.confidence,
                "matched_context": self.matched_context,
                "evidence": self.evidence,
                "needs_review": self.needs_review,
                "decision": self.decision,
                "recommended_action": self.recommended_action,
                "score_breakdown": score_breakdown,
            },
        )
```

- [ ] **Step 4: Run model tests**

Run:

```bash
cd workers/analysis_worker && uv run pytest -q tests/test_models.py
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/analysis_worker/models.py workers/analysis_worker/tests/test_models.py
git commit -m "feat(worker): extend work relevance assessment fields"
```

---

### Task 2: Add Evidence Extraction, Scoring, and Decisions

**Files:**
- Modify: `workers/analysis_worker/work_relevance.py`
- Modify: `workers/analysis_worker/tests/test_work_relevance.py`

- [ ] **Step 1: Write failing tests for the v2 classifier decisions**

Append these tests to `workers/analysis_worker/tests/test_work_relevance.py`:

```python
def test_low_token_personal_use_decides_non_work():
    assessment = classify_work_relevance(
        job(usage_total_tokens=120),
        [message("Write a birthday toast for my friend and make it funny.")],
        [context()],
    )

    assert assessment.task_category == "personal_chat"
    assert assessment.decision == "non_work_related"
    assert assessment.recommended_action == "alert_non_work"
    assert assessment.needs_review is True
    assert assessment.score_breakdown["non_work"] >= 0.75
    assert assessment.evidence[0]["kind"] == "non_work"


def test_job_search_decides_non_work():
    assessment = classify_work_relevance(
        job(usage_total_tokens=300),
        [message("Rewrite my resume and prepare answers for a senior backend interview.")],
        [context()],
    )

    assert assessment.task_category == "job_search"
    assert assessment.decision == "non_work_related"
    assert assessment.recommended_action == "alert_non_work"
    assert any(item["category"] == "job_search" for item in assessment.evidence)


def test_side_business_decides_non_work():
    assessment = classify_work_relevance(
        job(usage_total_tokens=500),
        [message("Draft a proposal for my freelance client and price the private app project.")],
        [context()],
    )

    assert assessment.task_category == "side_business"
    assert assessment.decision == "non_work_related"
    assert assessment.recommended_action == "alert_non_work"


def test_high_risk_decides_non_work_with_risk_score():
    assessment = classify_work_relevance(
        job(usage_total_tokens=400),
        [message("Help bypass login rate limits and extract private customer data.")],
        [context()],
    )

    assert assessment.task_category == "policy_violation"
    assert assessment.decision == "non_work_related"
    assert assessment.recommended_action == "alert_non_work"
    assert assessment.score_breakdown["risk"] >= 0.8


def test_unknown_low_cost_records_only():
    assessment = classify_work_relevance(
        job(usage_total_tokens=600),
        [message("Explain this idea in a clearer way.")],
        [context()],
    )

    assert assessment.task_category == "unknown"
    assert assessment.decision == "unknown"
    assert assessment.recommended_action == "record_only"
    assert assessment.needs_review is False


def test_unknown_high_cost_requires_review():
    assessment = classify_work_relevance(
        job(usage_total_tokens=25000),
        [message("Explain this idea in a clearer way.")],
        [context()],
    )

    assert assessment.task_category == "unknown"
    assert assessment.decision == "unknown"
    assert assessment.recommended_action == "review_high_cost_unknown"
    assert assessment.needs_review is True


def test_work_and_non_work_conflict_requires_review():
    assessment = classify_work_relevance(
        job(usage_total_tokens=800),
        [message("In the new-api gateway repo, draft a resume bullet about debugging the relay.")],
        [context()],
    )

    assert assessment.task_category == "job_search"
    assert assessment.decision == "needs_review"
    assert assessment.recommended_action == "review_conflict"
    assert assessment.score_breakdown["conflict"] > 0
```

- [ ] **Step 2: Run the new tests and verify they fail**

Run:

```bash
cd workers/analysis_worker && uv run pytest -q tests/test_work_relevance.py
```

Expected: FAIL because current assessments do not have the new decisions and evidence structure.

- [ ] **Step 3: Replace classifier constants and add evidence helpers**

In `workers/analysis_worker/work_relevance.py`, replace the constants section from `TASK_KEYWORDS` through `PERSONAL_CATEGORIES` with:

```python
ANALYZER_VERSION = "work_relevance_mvp_2026_04_28"

DECISION_WORK_RELATED = "work_related"
DECISION_NON_WORK_RELATED = "non_work_related"
DECISION_NEEDS_REVIEW = "needs_review"
DECISION_UNKNOWN = "unknown"

ACTION_ALLOW = "allow"
ACTION_ALERT_NON_WORK = "alert_non_work"
ACTION_REVIEW_HIGH_COST_UNKNOWN = "review_high_cost_unknown"
ACTION_REVIEW_CONFLICT = "review_conflict"
ACTION_RECORD_ONLY = "record_only"

UNKNOWN_HIGH_COST_THRESHOLD = 20_000

TASK_KEYWORDS = {
    "debugging": ["debug", "bug", "error", "failure", "stack trace", "regression", "fix"],
    "coding": ["code", "implement", "function", "class", "api", "test", "refactor", "sql"],
    "code_review": ["review", "pull request", "diff", "regression risk"],
    "documentation": ["document", "readme", "docs", "write-up", "guide"],
    "data_analysis": ["analyze", "csv", "spreadsheet", "metric", "dashboard"],
    "operations": ["deploy", "incident", "oncall", "alert", "runbook"],
    "research": ["research", "compare", "evaluate", "investigate"],
    "product_design": ["product", "requirements", "user story", "wireframe"],
    "translation": ["translate", "localize"],
    "meeting_summary": ["meeting", "minutes", "summary"],
    "customer_support": ["customer", "support ticket", "refund"],
}

NON_WORK_KEYWORDS = {
    "personal_chat": ["birthday", "friend", "dating", "vacation", "party", "personal", "relationship"],
    "entertainment": ["game", "joke", "lyrics", "movie", "roleplay"],
    "shopping": ["shopping", "buy", "coupon", "discount", "wishlist"],
    "travel": ["trip", "hotel", "flight", "itinerary", "tourist"],
    "job_search": ["resume", "cv", "interview", "job application", "recruiter", "cover letter"],
    "side_business": ["freelance", "private app", "external client", "side business", "my client"],
}

HIGH_RISK_KEYWORDS = {
    "policy_violation": [
        "bypass login",
        "bypass rate limit",
        "extract private",
        "steal",
        "credential",
        "phishing",
        "fraud",
    ],
}

WORK_CATEGORIES = set(TASK_KEYWORDS)
NON_WORK_CATEGORIES = set(NON_WORK_KEYWORDS)
```

Then add these helpers below `_combined_text()`:

```python
def _keyword_evidence(text: str, keywords: dict[str, list[str]], kind: str, weight: float) -> list[dict[str, object]]:
    evidence: list[dict[str, object]] = []
    for category, terms in keywords.items():
        matched = [term for term in terms if term in text]
        if not matched:
            continue
        evidence.append({
            "kind": kind,
            "category": category,
            "weight": weight,
            "source": "keyword",
            "snippet": ", ".join(matched[:5]),
            "reason": f"Matched {category} terms: {', '.join(matched)}.",
        })
    return evidence


def _context_evidence(text: str, contexts: list[ContextCatalogEntry]) -> tuple[list[dict[str, object]], list[dict[str, object]]]:
    matched_context = _match_contexts(text, contexts)
    evidence = [
        {
            "kind": "work_context",
            "category": "context_catalog",
            "weight": 0.9,
            "source": "context_catalog",
            "snippet": ", ".join(match.get("matched_terms", [])),
            "reason": f"Matched active context_catalog entry {match['name']}.",
        }
        for match in matched_context
    ]
    return matched_context, evidence


def _score_evidence(evidence: list[dict[str, object]]) -> dict[str, float]:
    work = min(1.0, sum(float(item["weight"]) for item in evidence if item["kind"] in {"work_context", "work_task"}))
    non_work = min(1.0, sum(float(item["weight"]) for item in evidence if item["kind"] == "non_work"))
    risk = min(1.0, sum(float(item["weight"]) for item in evidence if item["kind"] == "high_risk"))
    conflict = min(work, max(non_work, risk))
    uncertainty = 1.0 if not evidence else max(0.0, 1.0 - max(work, non_work, risk))
    return {
        "work": round(work, 3),
        "non_work": round(non_work, 3),
        "risk": round(risk, 3),
        "conflict": round(conflict, 3),
        "uncertainty": round(uncertainty, 3),
    }


def _best_category(evidence: list[dict[str, object]]) -> str:
    priority = {"high_risk": 4, "non_work": 3, "work_task": 2, "work_context": 1}
    if not evidence:
        return "unknown"
    best = max(evidence, key=lambda item: (priority.get(str(item["kind"]), 0), float(item["weight"])))
    if best["kind"] == "high_risk":
        return "policy_violation"
    if best["kind"] == "work_context":
        return "unknown"
    return str(best["category"])


def _decision_from_scores(job: TraceCapturedJob, score: dict[str, float]) -> tuple[str, str, bool, float]:
    if score["conflict"] >= 0.5:
        return DECISION_NEEDS_REVIEW, ACTION_REVIEW_CONFLICT, True, 0.65
    if score["risk"] >= 0.8:
        return DECISION_NON_WORK_RELATED, ACTION_ALERT_NON_WORK, True, score["risk"]
    if score["non_work"] >= 0.7:
        return DECISION_NON_WORK_RELATED, ACTION_ALERT_NON_WORK, True, score["non_work"]
    if score["work"] >= 0.7 and score["non_work"] < 0.3 and score["risk"] < 0.3:
        return DECISION_WORK_RELATED, ACTION_ALLOW, False, score["work"]
    if job.usage_total_tokens >= UNKNOWN_HIGH_COST_THRESHOLD:
        return DECISION_UNKNOWN, ACTION_REVIEW_HIGH_COST_UNKNOWN, True, 0.35
    return DECISION_UNKNOWN, ACTION_RECORD_ONLY, False, 0.25
```

- [ ] **Step 4: Replace `classify_work_relevance()` with v2 logic**

In `workers/analysis_worker/work_relevance.py`, replace `classify_work_relevance()` with:

```python
def classify_work_relevance(
    job: TraceCapturedJob,
    messages: list[NormalizedMessage],
    contexts: list[ContextCatalogEntry],
) -> WorkRelevanceAssessment:
    text = _combined_text(messages)
    if not text:
        score = {
            "work": 0.0,
            "non_work": 0.0,
            "risk": 0.0,
            "conflict": 0.0,
            "uncertainty": 1.0,
        }
        decision, action, needs_review, confidence = _decision_from_scores(job, score)
        return WorkRelevanceAssessment(
            trace_id=job.trace_id,
            task_category="unknown",
            work_related_score=0.0,
            personal_use_score=0.0,
            confidence=confidence,
            matched_context=[],
            evidence=[{
                "kind": "insufficient",
                "category": "no_text",
                "weight": 0.0,
                "source": "fallback",
                "snippet": "",
                "reason": "No normalized text was available for work relevance classification.",
            }],
            needs_review=needs_review,
            analyzer_version=ANALYZER_VERSION,
            decision=decision,
            recommended_action=action,
            score_breakdown=score,
        )

    matched_context, evidence = _context_evidence(text, contexts)
    evidence.extend(_keyword_evidence(text, TASK_KEYWORDS, "work_task", 0.45))
    evidence.extend(_keyword_evidence(text, NON_WORK_KEYWORDS, "non_work", 0.8))
    evidence.extend(_keyword_evidence(text, HIGH_RISK_KEYWORDS, "high_risk", 1.0))

    if not evidence:
        evidence.append({
            "kind": "insufficient",
            "category": "no_match",
            "weight": 0.0,
            "source": "fallback",
            "snippet": text[:120],
            "reason": "No catalog context or known task category matched.",
        })

    score = _score_evidence(evidence)
    decision, action, needs_review, confidence = _decision_from_scores(job, score)
    category = _best_category(evidence)

    return WorkRelevanceAssessment(
        trace_id=job.trace_id,
        task_category=category,
        work_related_score=score["work"],
        personal_use_score=max(score["non_work"], score["risk"]),
        confidence=confidence,
        matched_context=matched_context,
        evidence=evidence,
        needs_review=needs_review,
        analyzer_version=ANALYZER_VERSION,
        decision=decision,
        recommended_action=action,
        score_breakdown=score,
    )
```

- [ ] **Step 5: Run the work relevance tests**

Run:

```bash
cd workers/analysis_worker && uv run pytest -q tests/test_work_relevance.py
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add workers/analysis_worker/work_relevance.py workers/analysis_worker/tests/test_work_relevance.py
git commit -m "feat(worker): add evidence-based work relevance decisions"
```

---

### Task 3: Preserve Embedding Matches in the V2 Decision Model

**Files:**
- Modify: `workers/analysis_worker/work_relevance.py`
- Modify: `workers/analysis_worker/tests/test_work_relevance.py`

- [ ] **Step 1: Extend the embedding test**

In `test_embedding_match_overrides_keyword_classification`, add these assertions:

```python
    assert result.decision == "work_related"
    assert result.recommended_action == "allow"
    assert result.needs_review is False
    assert result.score_breakdown["work"] >= 0.8
    assert result.evidence[0]["source"] == "embedding"
```

- [ ] **Step 2: Run the embedding test and verify it fails**

Run:

```bash
cd workers/analysis_worker && uv run pytest -q tests/test_work_relevance.py::test_embedding_match_overrides_keyword_classification
```

Expected: FAIL until the embedding branch fills the new fields.

- [ ] **Step 3: Update the embedding match branch**

In `classify_work_relevance_with_embeddings()`, replace the `if matches and matches[0][2] > 0.75:` return block with:

```python
    if matches and matches[0][2] > 0.75:
        context_type, name, similarity, categories, models = matches[0]
        category = categories[0] if categories else "unknown"
        work_score = min(float(similarity), 1.0)
        score = {
            "work": round(work_score, 3),
            "non_work": 0.0,
            "risk": 0.0,
            "conflict": 0.0,
            "uncertainty": round(max(0.0, 1.0 - work_score), 3),
        }
        return WorkRelevanceAssessment(
            trace_id=job.trace_id,
            task_category=category,
            work_related_score=score["work"],
            personal_use_score=0.0,
            confidence=score["work"],
            matched_context=[{
                "type": context_type,
                "name": name,
                "similarity": similarity,
                "source": "embedding",
            }],
            evidence=[{
                "kind": "work_context",
                "category": "context_catalog",
                "weight": score["work"],
                "source": "embedding",
                "snippet": name,
                "reason": f"Semantic match with catalog entry '{name}' (similarity={similarity:.3f}).",
            }],
            needs_review=False,
            analyzer_version=ANALYZER_VERSION + "+emb",
            decision=DECISION_WORK_RELATED,
            recommended_action=ACTION_ALLOW,
            score_breakdown=score,
        )
```

- [ ] **Step 4: Run work relevance tests**

Run:

```bash
cd workers/analysis_worker && uv run pytest -q tests/test_work_relevance.py
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/analysis_worker/work_relevance.py workers/analysis_worker/tests/test_work_relevance.py
git commit -m "feat(worker): preserve embedding work relevance decisions"
```

---

### Task 4: Map Work Relevance Decisions to Specific Alerts

**Files:**
- Modify: `workers/analysis_worker/rules.py`
- Modify: `workers/analysis_worker/tests/test_rules.py`

- [ ] **Step 1: Replace the old low-token test with new alert tests**

In `workers/analysis_worker/tests/test_rules.py`, replace `test_does_not_detect_low_work_relevance_high_cost_for_low_tokens` with:

```python
def test_detects_low_token_non_work_personal_use():
    assessment = WorkRelevanceAssessment(
        trace_id="trace_personal",
        task_category="personal_chat",
        work_related_score=0.1,
        personal_use_score=0.8,
        confidence=0.8,
        matched_context=[],
        evidence=[{"category": "personal_chat", "reason": "Matched personal terms."}],
        needs_review=True,
        analyzer_version="work_relevance_mvp_2026_04_28",
        decision="non_work_related",
        recommended_action="alert_non_work",
        score_breakdown={"work": 0.0, "non_work": 0.8, "risk": 0.0, "conflict": 0.0, "uncertainty": 0.2},
    )

    alerts = detect_work_relevance_anomalies(job(usage_total_tokens=100), assessment)

    assert [alert.anomaly_type for alert in alerts] == ["non_work_personal_use"]
    assert alerts[0].severity == "medium"
    assert alerts[0].observed_value == 100
    assert "personal_chat" in alerts[0].reason
```

Append these tests near the existing work relevance rule tests:

```python
def test_detects_job_search_non_work_alert():
    assessment = WorkRelevanceAssessment(
        trace_id="trace_job_search",
        task_category="job_search",
        work_related_score=0.0,
        personal_use_score=0.9,
        confidence=0.9,
        matched_context=[],
        evidence=[{"category": "job_search", "reason": "Matched resume terms."}],
        needs_review=True,
        analyzer_version="work_relevance_mvp_2026_04_28",
        decision="non_work_related",
        recommended_action="alert_non_work",
        score_breakdown={"work": 0.0, "non_work": 0.9, "risk": 0.0, "conflict": 0.0, "uncertainty": 0.1},
    )

    alerts = detect_work_relevance_anomalies(job(usage_total_tokens=300), assessment)

    assert [alert.anomaly_type for alert in alerts] == ["non_work_job_search"]
    assert alerts[0].severity == "high"


def test_detects_unknown_high_cost_review_alert():
    assessment = WorkRelevanceAssessment(
        trace_id="trace_unknown",
        task_category="unknown",
        work_related_score=0.0,
        personal_use_score=0.0,
        confidence=0.35,
        matched_context=[],
        evidence=[{"category": "no_match", "reason": "No context matched."}],
        needs_review=True,
        analyzer_version="work_relevance_mvp_2026_04_28",
        decision="unknown",
        recommended_action="review_high_cost_unknown",
        score_breakdown={"work": 0.0, "non_work": 0.0, "risk": 0.0, "conflict": 0.0, "uncertainty": 1.0},
    )

    alerts = detect_work_relevance_anomalies(job(usage_total_tokens=25000), assessment)

    assert [alert.anomaly_type for alert in alerts] == ["unknown_high_cost"]
    assert alerts[0].severity == "medium"


def test_record_only_unknown_does_not_alert():
    assessment = WorkRelevanceAssessment(
        trace_id="trace_unknown",
        task_category="unknown",
        work_related_score=0.0,
        personal_use_score=0.0,
        confidence=0.25,
        matched_context=[],
        evidence=[],
        needs_review=False,
        analyzer_version="work_relevance_mvp_2026_04_28",
        decision="unknown",
        recommended_action="record_only",
        score_breakdown={"work": 0.0, "non_work": 0.0, "risk": 0.0, "conflict": 0.0, "uncertainty": 1.0},
    )

    assert detect_work_relevance_anomalies(job(usage_total_tokens=500), assessment) == []
```

- [ ] **Step 2: Run the rule tests and verify they fail**

Run:

```bash
cd workers/analysis_worker && uv run pytest -q tests/test_rules.py -k "work_relevance or unknown_high_cost or record_only"
```

Expected: FAIL because `detect_work_relevance_anomalies()` still requires high token usage.

- [ ] **Step 3: Replace `detect_work_relevance_anomalies()`**

In `workers/analysis_worker/rules.py`, replace `detect_work_relevance_anomalies()` with:

```python
def detect_work_relevance_anomalies(
    job: TraceCapturedJob,
    assessment: WorkRelevanceAssessment,
) -> list[AnomalyAlert]:
    action = getattr(assessment, "recommended_action", "")
    decision = getattr(assessment, "decision", "")
    category = assessment.task_category

    if action == "record_only" or decision == "work_related":
        return []

    if action == "review_high_cost_unknown":
        return [_anomaly(
            job,
            "unknown_high_cost",
            "medium",
            observed_value=job.usage_total_tokens,
            threshold_value=LOW_WORK_RELEVANCE_TOKEN_THRESHOLD,
            reason=_work_relevance_reason(assessment, "trace has high token usage but insufficient work relevance evidence"),
        )]

    if action == "review_conflict":
        return [_anomaly(
            job,
            "work_nonwork_conflict",
            "medium",
            observed_value=assessment.personal_use_score,
            threshold_value=0.0,
            reason=_work_relevance_reason(assessment, "trace contains both work and non-work evidence"),
        )]

    if action != "alert_non_work" and not (
        assessment.personal_use_score >= LOW_WORK_RELEVANCE_PERSONAL_SCORE_THRESHOLD
        and job.usage_total_tokens >= LOW_WORK_RELEVANCE_TOKEN_THRESHOLD
    ):
        return []

    anomaly_type, severity = _non_work_alert_type_and_severity(category, job.usage_total_tokens)
    return [_anomaly(
        job,
        anomaly_type,
        severity,
        observed_value=job.usage_total_tokens,
        threshold_value=LOW_WORK_RELEVANCE_TOKEN_THRESHOLD,
        reason=_work_relevance_reason(
            assessment,
            f"trace classified as {category} with personal use score {assessment.personal_use_score:.2f}",
        ),
    )]
```

Add these helpers below it:

```python
def _non_work_alert_type_and_severity(category: str, total_tokens: int) -> tuple[str, str]:
    if category == "policy_violation":
        return "non_work_high_risk", "high"
    if category == "job_search":
        return "non_work_job_search", "high"
    if category == "side_business":
        return "non_work_side_business", "high"
    severity = "high" if total_tokens >= LOW_WORK_RELEVANCE_TOKEN_THRESHOLD else "medium"
    return "non_work_personal_use", severity


def _work_relevance_reason(assessment: WorkRelevanceAssessment, fallback: str) -> str:
    evidence = getattr(assessment, "evidence", []) or []
    reasons: list[str] = []
    for item in evidence:
        if isinstance(item, dict) and item.get("reason"):
            reasons.append(str(item["reason"]))
        elif isinstance(item, str):
            reasons.append(item)
    detail = "; ".join(reasons[:3])
    if detail:
        return f"{fallback}: {detail}"
    return fallback
```

- [ ] **Step 4: Run all rule tests**

Run:

```bash
cd workers/analysis_worker && uv run pytest -q tests/test_rules.py
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/analysis_worker/rules.py workers/analysis_worker/tests/test_rules.py
git commit -m "feat(worker): alert on non-work relevance decisions"
```

---

### Task 5: Update Worker Pipeline and E2E Coverage

**Files:**
- Modify: `workers/analysis_worker/tests/test_pipeline.py`
- Modify: `e2e/test_worker_work_relevance.py`

- [ ] **Step 1: Add pipeline assertions for v2 result JSON**

Append this test to `workers/analysis_worker/tests/test_pipeline.py`:

```python
def test_process_job_line_persists_low_token_non_work_alert(tmp_path: Path):
    repo = CapturingRepository()
    evidence = FilesystemEvidenceStore(tmp_path)
    request_ref = evidence.write_text("trace_1", "request_body", "application/json", json.dumps({
        "model": "gpt-4.1",
        "messages": [{"role": "user", "content": "Write a birthday toast for my friend."}],
    })).object_ref
    response_ref = evidence.write_text("trace_1", "response_body", "application/json", "{}").object_ref
    payload = json.dumps({
        "type": "trace_captured",
        "trace_id": "trace_1",
        "route_pattern": "/v1/chat/completions",
        "protocol_family": "openai_chat",
        "capture_mode": "raw_and_normalized",
        "username": "alice",
        "request_raw_ref": request_ref,
        "response_raw_ref": response_ref,
        "model_requested": "gpt-4.1",
        "usage_total_tokens": 120,
        "request_started_at": "2026-04-28T13:45:22Z",
    })

    result = process_job_line(payload, evidence, repo)

    assert result["anomaly_count"] == 1
    work_results = [r for r in repo.results if r.category == "work_relevance"]
    assert work_results[0].result["decision"] == "non_work_related"
    assert work_results[0].result["recommended_action"] == "alert_non_work"
    assert repo.anomalies[0].anomaly_type == "non_work_personal_use"
```

- [ ] **Step 2: Run the pipeline test**

Run:

```bash
cd workers/analysis_worker && uv run pytest -q tests/test_pipeline.py::test_process_job_line_persists_low_token_non_work_alert
```

Expected: PASS after Tasks 1-4.

- [ ] **Step 3: Add E2E coverage cases**

In `e2e/test_worker_work_relevance.py`, add three seeded traces using the file's existing helper pattern:

```python
LOW_TOKEN_NON_WORK_PROMPT = "Write a funny birthday toast for my friend."
HIGH_TOKEN_UNKNOWN_PROMPT = "Explain this idea in a clearer way."
WORK_RELATED_PROMPT = "Debug the new-api gateway route registry and write tests."
```

Add assertions that verify:

```python
assert_anomaly_type(conn, "trace_low_token_non_work", "non_work_personal_use")
assert_anomaly_type(conn, "trace_high_token_unknown", "unknown_high_cost")
assert_no_work_relevance_anomaly(conn, "trace_work_related")
```

If `assert_anomaly_type` and `assert_no_work_relevance_anomaly` do not exist in that file, add:

```python
def assert_anomaly_type(conn, trace_id: str, anomaly_type: str) -> None:
    with conn.cursor() as cur:
        cur.execute(
            "SELECT anomaly_type FROM usage_anomalies WHERE %s = ANY(sample_trace_ids)",
            (trace_id,),
        )
        rows = [row[0] for row in cur.fetchall()]
    assert anomaly_type in rows


def assert_no_work_relevance_anomaly(conn, trace_id: str) -> None:
    with conn.cursor() as cur:
        cur.execute(
            """
            SELECT anomaly_type
            FROM usage_anomalies
            WHERE %s = ANY(sample_trace_ids)
              AND anomaly_type IN (
                'non_work_personal_use',
                'non_work_job_search',
                'non_work_side_business',
                'non_work_high_risk',
                'unknown_high_cost',
                'work_nonwork_conflict'
              )
            """,
            (trace_id,),
        )
        rows = cur.fetchall()
    assert rows == []
```

- [ ] **Step 4: Run focused Python tests**

Run:

```bash
cd workers/analysis_worker && uv run pytest -q tests/test_models.py tests/test_work_relevance.py tests/test_rules.py tests/test_pipeline.py
```

Expected: PASS.

- [ ] **Step 5: Run or document E2E verification**

Run when postgres, redis, new-api, and audit-gateway are running:

```bash
uv run e2e/test_worker_work_relevance.py
```

Expected: PASS. If services are not running in the implementation environment, record this exact reason in the final handoff.

- [ ] **Step 6: Commit**

```bash
git add workers/analysis_worker/tests/test_pipeline.py e2e/test_worker_work_relevance.py
git commit -m "test(worker): cover work relevance alert pipeline"
```

---

### Task 6: Update Documentation and Run Final Verification

**Files:**
- Modify: `ARCHITECTURE.md`
- Modify: `README.md` if the E2E command or worker behavior text needs a short note.

- [ ] **Step 1: Update architecture wording**

In `ARCHITECTURE.md`, replace the `work_relevance.py` row with:

```markdown
| `work_relevance.py` | 工作相关性分类器：基于 `context_catalog`、embedding 匹配和关键词证据生成 decision/action/score/evidence，供 `rules.py` 转换为非工作相关告警 |
```

Add this sentence to the analysis worker pipeline section:

```markdown
工作相关性结果会完整写入 `analysis_results.result_json`；明确非工作相关、未知高成本和工作/非工作冲突会写入 `usage_anomalies` 供管理员复核。
```

- [ ] **Step 2: Run full worker tests**

Run:

```bash
cd workers/analysis_worker && uv run pytest -q
```

Expected: PASS.

- [ ] **Step 3: Run Go tests if no Python-only constraint exists**

Run:

```bash
make test
```

Expected: PASS.

- [ ] **Step 4: Check repository status**

Run:

```bash
git status --short
```

Expected: only intentional documentation or implementation files are modified. Ignore pre-existing untracked `.playwright-mcp/` unless the user asks to remove it.

- [ ] **Step 5: Commit docs and any final fixes**

```bash
git add ARCHITECTURE.md README.md
git commit -m "docs: describe work relevance anomaly decisions"
```

If `README.md` did not change, run:

```bash
git add ARCHITECTURE.md
git commit -m "docs: describe work relevance anomaly decisions"
```

---

## Self-Review Notes

- Spec coverage: Tasks 1-4 implement decision fields, evidence scoring, new alert types, and low-token non-work alerts. Task 5 covers pipeline and E2E behavior. Task 6 covers documentation and final verification.
- Scope: Feedback storage is deliberately Phase 2 and not implemented here because the approved first phase only reserves the feedback path and does not require admin UI or schema work.
- Compatibility: Existing public functions remain `classify_work_relevance()`, `classify_work_relevance_with_embeddings()`, and `detect_work_relevance_anomalies()`. Existing callers in `main.py` do not need changes.
- Type consistency: `evidence` is widened to `list[Any]` in `models.py` so existing string evidence and new structured evidence both serialize safely during migration.
