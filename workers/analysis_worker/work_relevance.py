from dataclasses import dataclass
from typing import Any

from models import ContextCatalogEntry, NormalizedMessage, TraceCapturedJob, WorkRelevanceAssessment


ANALYZER_VERSION = "work_relevance_mvp_2026_04_28"

LOW_TOKEN_LIMIT = 2_000
HIGH_TOKEN_LIMIT = 20_000
MAX_INTENT_CHARS = 4_000

DECISION_WORK_RELATED = "work_related"
DECISION_NON_WORK_RELATED = "non_work_related"
DECISION_NEEDS_REVIEW = "needs_review"
DECISION_UNKNOWN = "unknown"

ACTION_ALLOW = "allow"
ACTION_ALERT_NON_WORK = "alert_non_work"
ACTION_REVIEW_HIGH_COST_UNKNOWN = "review_high_cost_unknown"
ACTION_REVIEW_CONFLICT = "review_conflict"
ACTION_RECORD_ONLY = "record_only"

VALID_DECISIONS = {
    DECISION_WORK_RELATED,
    DECISION_NON_WORK_RELATED,
    DECISION_NEEDS_REVIEW,
    DECISION_UNKNOWN,
}
VALID_ACTIONS = {
    ACTION_ALLOW,
    ACTION_ALERT_NON_WORK,
    ACTION_REVIEW_HIGH_COST_UNKNOWN,
    ACTION_REVIEW_CONFLICT,
    ACTION_RECORD_ONLY,
}

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


@dataclass(frozen=True)
class ExtractedIntent:
    text: str
    original_length: int
    truncated: bool


@dataclass(frozen=True)
class CatalogMatch:
    context: ContextCatalogEntry
    matched_terms: list[str]
    strength: str


def classify_work_relevance(
    job: TraceCapturedJob,
    messages: list[NormalizedMessage],
    contexts: list[ContextCatalogEntry],
    llm_judge=None,
) -> WorkRelevanceAssessment:
    text = _combined_text(messages)
    intent = extract_user_intent(messages)
    if not intent.text:
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

    strong_matches, weak_matches = _catalog_matches(intent.text, contexts)
    matched_context, evidence = _context_evidence(intent.text, contexts)
    evidence.extend(_keyword_evidence(intent.text, TASK_KEYWORDS, "work_task", 0.45))
    non_work_evidence = _keyword_evidence(intent.text, NON_WORK_KEYWORDS, "non_work", 0.8)
    risk_evidence = _keyword_evidence(intent.text, HIGH_RISK_KEYWORDS, "high_risk", 1.0)
    evidence.extend(non_work_evidence)
    evidence.extend(risk_evidence)

    if (
        token_tier(job.usage_total_tokens) in {"low", "medium"}
        and not non_work_evidence
        and not risk_evidence
        and strong_matches
        and any(_match_payload(match)["source"] == "catalog_alias" for match in strong_matches)
    ):
        matched = [_match_payload(match) for match in strong_matches]
        score = {
            "work": 0.95,
            "non_work": 0.0,
            "risk": 0.0,
            "conflict": 0.0,
            "uncertainty": 0.05,
        }
        return WorkRelevanceAssessment(
            trace_id=job.trace_id,
            task_category=_best_category(evidence),
            work_related_score=0.95,
            personal_use_score=0.0,
            confidence=0.95,
            matched_context=matched,
            evidence=evidence,
            needs_review=False,
            analyzer_version=ANALYZER_VERSION,
            decision=DECISION_WORK_RELATED,
            recommended_action=ACTION_ALLOW,
            score_breakdown=score,
        )

    if llm_judge is not None and _should_call_llm(job, strong_matches, weak_matches, non_work_evidence + risk_evidence):
        bundle = _build_llm_bundle(job, intent, strong_matches, weak_matches, non_work_evidence + risk_evidence)
        try:
            adapted = _adapt_llm_result(job, llm_judge.judge(bundle))
            evidence.append({
                "kind": "llm_judge",
                "category": adapted["decision"],
                "weight": adapted["confidence"],
                "source": "llm_judge",
                "snippet": intent.text[:120],
                "reason": "LLM judge adapted work relevance decision.",
            })
            return WorkRelevanceAssessment(
                trace_id=job.trace_id,
                task_category=adapted["task_category"],
                work_related_score=adapted["work_related_score"],
                personal_use_score=adapted["personal_use_score"],
                confidence=adapted["confidence"],
                matched_context=[_match_payload(match) for match in strong_matches + weak_matches],
                evidence=evidence,
                needs_review=adapted["needs_review"],
                analyzer_version=ANALYZER_VERSION,
                decision=adapted["decision"],
                recommended_action=adapted["recommended_action"],
                score_breakdown=adapted["score_breakdown"],
            )
        except Exception as exc:
            error_type = getattr(exc, "error_type", exc.__class__.__name__)
            return _conservative_llm_fallback(
                job,
                messages,
                contexts,
                strong_matches,
                non_work_evidence + risk_evidence,
                error_type,
            )

    if not evidence:
        evidence.append({
            "kind": "insufficient",
            "category": "no_match",
            "weight": 0.0,
            "source": "fallback",
            "snippet": intent.text[:120],
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


def token_tier(total_tokens: int) -> str:
    if total_tokens <= LOW_TOKEN_LIMIT:
        return "low"
    if total_tokens >= HIGH_TOKEN_LIMIT:
        return "high"
    return "medium"


def extract_user_intent(messages: list[NormalizedMessage], max_chars: int = MAX_INTENT_CHARS) -> ExtractedIntent:
    relevant = [
        message.content_text
        for message in messages
        if message.direction == "request"
        and message.role in {"user", "developer", "system"}
        and message.content_text
    ]
    text = "\n".join(relevant).lower()
    original_length = len(text)
    truncated = original_length > max_chars
    return ExtractedIntent(
        text=text[:max_chars],
        original_length=original_length,
        truncated=truncated,
    )


def _normalized_terms(values: list[str]) -> list[str]:
    seen: set[str] = set()
    normalized: list[str] = []
    for value in values:
        term = value.strip().lower()
        if term and term not in seen:
            seen.add(term)
            normalized.append(term)
    return normalized


def _catalog_matches(text: str, contexts: list[ContextCatalogEntry]) -> tuple[list[CatalogMatch], list[CatalogMatch]]:
    strong_matches: list[CatalogMatch] = []
    weak_matches: list[CatalogMatch] = []
    for context in contexts:
        if not context.active:
            continue
        strong_terms = _normalized_terms([*context.aliases, context.name])
        weak_terms = _normalized_terms(context.keywords)
        matched_strong = [term for term in strong_terms if term in text]
        matched_weak = [term for term in weak_terms if term in text]
        if matched_strong:
            strong_matches.append(CatalogMatch(context=context, matched_terms=matched_strong, strength="strong"))
        if matched_weak:
            weak_matches.append(CatalogMatch(context=context, matched_terms=matched_weak, strength="weak"))
    return strong_matches, weak_matches


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
    strong_matches, weak_matches = _catalog_matches(text, contexts)
    matched_context = [_match_payload(match) for match in strong_matches + weak_matches]
    evidence = [
        {
            "kind": "work_context",
            "category": "context_catalog",
            "weight": 0.9 if match["strength"] == "strong" else 0.35,
            "source": match["source"],
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


def _match_payload(match: CatalogMatch) -> dict[str, Any]:
    alias_terms = set(_normalized_terms(match.context.aliases))
    name_term = match.context.name.strip().lower()
    source = "catalog_keyword"
    if any(term in alias_terms for term in match.matched_terms):
        source = "catalog_alias"
    elif name_term in match.matched_terms:
        source = "catalog_name"
    return {
        "type": match.context.context_type,
        "name": match.context.name,
        "matched_terms": match.matched_terms,
        "source": source,
        "strength": match.strength,
    }


def _build_llm_bundle(
    job: TraceCapturedJob,
    intent: ExtractedIntent,
    strong_matches: list[CatalogMatch],
    weak_matches: list[CatalogMatch],
    non_work_evidence: list[dict[str, object]],
) -> dict[str, Any]:
    return {
        "trace_id": job.trace_id,
        "token_tier": token_tier(job.usage_total_tokens),
        "usage_total_tokens": job.usage_total_tokens,
        "model_requested": job.model_requested,
        "intent": {
            "text": intent.text,
            "truncated": intent.truncated,
            "original_length": intent.original_length,
        },
        "catalog_matches": {
            "strong": [_match_payload(match) for match in strong_matches],
            "weak": [_match_payload(match) for match in weak_matches],
        },
        "non_work_evidence": non_work_evidence,
    }


def _should_call_llm(
    job: TraceCapturedJob,
    strong_matches: list[CatalogMatch],
    weak_matches: list[CatalogMatch],
    non_work_evidence: list[dict[str, object]],
) -> bool:
    if non_work_evidence and (strong_matches or weak_matches):
        return True
    if token_tier(job.usage_total_tokens) == "high" and not strong_matches:
        return True
    if token_tier(job.usage_total_tokens) == "medium" and weak_matches:
        return True
    return False


def _clamp_float(value: Any, lower: float, upper: float) -> float:
    try:
        numeric = float(value)
    except (TypeError, ValueError):
        numeric = lower
    return max(lower, min(upper, numeric))


def _adapt_llm_result(job: TraceCapturedJob, raw: dict[str, Any]) -> dict[str, Any]:
    decision = raw.get("decision")
    action = raw.get("recommended_action")
    if decision not in VALID_DECISIONS or action not in VALID_ACTIONS:
        fallback_decision, fallback_action, fallback_review, fallback_confidence = _decision_from_scores(
            job,
            {"work": 0.0, "non_work": 0.0, "risk": 0.0, "conflict": 0.0, "uncertainty": 1.0},
        )
        return {
            "task_category": "unknown",
            "decision": fallback_decision,
            "recommended_action": fallback_action,
            "needs_review": fallback_review,
            "confidence": fallback_confidence,
            "work_related_score": 0.0,
            "personal_use_score": 0.0,
            "score_breakdown": {
                "work": 0.0,
                "non_work": 0.0,
                "risk": 0.0,
                "conflict": 0.0,
                "uncertainty": 1.0,
            },
        }

    confidence = _clamp_float(raw.get("confidence", 0.7), 0.0, 1.0)
    work_score = 0.0
    personal_score = 0.0
    if decision == DECISION_WORK_RELATED:
        work_score = max(0.7, confidence)
    elif decision == DECISION_NON_WORK_RELATED:
        personal_score = max(0.7, confidence)
    elif decision == DECISION_NEEDS_REVIEW:
        work_score = 0.5
        personal_score = 0.5

    return {
        "task_category": str(raw.get("task_category") or "unknown"),
        "decision": decision,
        "recommended_action": action,
        "needs_review": action in {
            ACTION_ALERT_NON_WORK,
            ACTION_REVIEW_CONFLICT,
            ACTION_REVIEW_HIGH_COST_UNKNOWN,
        },
        "confidence": confidence,
        "work_related_score": work_score,
        "personal_use_score": personal_score,
        "score_breakdown": {
            "work": round(work_score, 3),
            "non_work": round(personal_score, 3),
            "risk": 0.0,
            "conflict": round(min(work_score, personal_score), 3),
            "uncertainty": round(max(0.0, 1.0 - confidence), 3),
        },
    }


def _llm_unavailable_evidence(error_type: str) -> dict[str, object]:
    return {
        "kind": "llm_judge",
        "category": "llm_unavailable",
        "weight": 0.0,
        "source": "llm_unavailable",
        "snippet": error_type,
        "reason": f"LLM judge unavailable due to {error_type}.",
    }


def _conservative_llm_fallback(
    job: TraceCapturedJob,
    messages: list[NormalizedMessage],
    contexts: list[ContextCatalogEntry],
    strong_matches: list[CatalogMatch],
    non_work_evidence: list[dict[str, object]],
    error_type: str,
) -> WorkRelevanceAssessment:
    assessment = classify_work_relevance(job, messages, contexts, llm_judge=None)
    evidence = list(assessment.evidence)
    evidence.append(_llm_unavailable_evidence(error_type))
    if non_work_evidence and strong_matches:
        return WorkRelevanceAssessment(
            trace_id=assessment.trace_id,
            task_category=assessment.task_category,
            work_related_score=assessment.work_related_score,
            personal_use_score=assessment.personal_use_score,
            confidence=assessment.confidence,
            matched_context=assessment.matched_context,
            evidence=evidence,
            needs_review=True,
            analyzer_version=assessment.analyzer_version,
            decision=DECISION_NEEDS_REVIEW,
            recommended_action=ACTION_REVIEW_CONFLICT,
            score_breakdown=assessment.score_breakdown,
        )
    if token_tier(job.usage_total_tokens) == "high" and assessment.decision == DECISION_UNKNOWN:
        return WorkRelevanceAssessment(
            trace_id=assessment.trace_id,
            task_category=assessment.task_category,
            work_related_score=assessment.work_related_score,
            personal_use_score=assessment.personal_use_score,
            confidence=assessment.confidence,
            matched_context=assessment.matched_context,
            evidence=evidence,
            needs_review=True,
            analyzer_version=assessment.analyzer_version,
            decision=DECISION_UNKNOWN,
            recommended_action=ACTION_REVIEW_HIGH_COST_UNKNOWN,
            score_breakdown=assessment.score_breakdown,
        )
    return WorkRelevanceAssessment(
        trace_id=assessment.trace_id,
        task_category=assessment.task_category,
        work_related_score=assessment.work_related_score,
        personal_use_score=assessment.personal_use_score,
        confidence=assessment.confidence,
        matched_context=assessment.matched_context,
        evidence=evidence,
        needs_review=assessment.needs_review,
        analyzer_version=assessment.analyzer_version,
        decision=assessment.decision,
        recommended_action=assessment.recommended_action,
        score_breakdown=assessment.score_breakdown,
    )


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

    return classify_work_relevance(job, messages, contexts)
