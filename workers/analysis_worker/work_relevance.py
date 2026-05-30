from models import ContextCatalogEntry, NormalizedMessage, TraceCapturedJob, WorkRelevanceAssessment


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


def _combined_text(messages: list[NormalizedMessage]) -> str:
    return "\n".join(message.content_text for message in messages if message.content_text).lower()


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
    if best["kind"] == "insufficient":
        return "unknown"
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


def _detect_category(text: str) -> tuple[str, list[str]]:
    best_category = "unknown"
    best_terms: list[str] = []
    for category, terms in TASK_KEYWORDS.items():
        matched = [term for term in terms if term in text]
        if len(matched) > len(best_terms):
            best_category = category
            best_terms = matched
    return best_category, best_terms


def _match_contexts(text: str, contexts: list[ContextCatalogEntry]) -> list[dict[str, object]]:
    matches: list[dict[str, object]] = []
    for context in contexts:
        matched_terms = [term for term in context.search_terms() if term in text]
        if matched_terms:
            matches.append({
                "type": context.context_type,
                "name": context.name,
                "matched_terms": matched_terms,
            })
    return matches


def classify_work_relevance_with_embeddings(
    job,
    messages,
    contexts,
    embedding_client,
    pg_connection,
) -> WorkRelevanceAssessment:
    if embedding_client is None:
        raise TypeError("embedding_client is required for classify_work_relevance_with_embeddings")
    if pg_connection is None:
        raise TypeError("pg_connection is required for classify_work_relevance_with_embeddings")

    text = _combined_text(messages)
    if not text:
        return classify_work_relevance(job, messages, contexts)

    trace_embedding = embedding_client.embed(text)
    embedding_str = "[" + ",".join(str(v) for v in trace_embedding) + "]"

    cursor = pg_connection.cursor()
    cursor.execute(
        """
        SELECT
            cc.context_type,
            cc.name,
            1 - (cc.embedding <=> %s::vector) AS similarity,
            cc.expected_task_categories,
            cc.expected_models
        FROM context_catalog cc
        WHERE cc.active = true
          AND cc.embedding IS NOT NULL
        ORDER BY cc.embedding <=> %s::vector
        LIMIT 3
        """,
        (embedding_str, embedding_str),
    )
    matches = cursor.fetchall()

    if matches and matches[0][2] > 0.75:
        context_type, name, similarity, categories, models = matches[0]
        category = categories[0] if categories else "unknown"
        return WorkRelevanceAssessment(
            trace_id=job.trace_id,
            task_category=category,
            work_related_score=min(similarity, 1.0),
            personal_use_score=max(1.0 - similarity, 0.0),
            confidence=min(similarity, 1.0),
            matched_context=[{
                "type": context_type,
                "name": name,
                "similarity": similarity,
                "source": "embedding",
            }],
            evidence=[f"Semantic match with catalog entry '{name}' (similarity={similarity:.3f})."],
            needs_review=False,
            analyzer_version=ANALYZER_VERSION + "+emb",
        )

    return classify_work_relevance(job, messages, contexts)
