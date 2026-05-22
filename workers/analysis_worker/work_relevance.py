from models import ContextCatalogEntry, NormalizedMessage, TraceCapturedJob, WorkRelevanceAssessment


ANALYZER_VERSION = "work_relevance_mvp_2026_04_28"

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
    "personal_chat": ["birthday", "friend", "dating", "vacation", "party", "personal"],
    "entertainment": ["game", "joke", "lyrics", "movie", "roleplay"],
}

WORK_CATEGORIES = {
    "coding",
    "debugging",
    "code_review",
    "documentation",
    "translation",
    "data_analysis",
    "meeting_summary",
    "product_design",
    "customer_support",
    "research",
    "operations",
}

PERSONAL_CATEGORIES = {"personal_chat", "entertainment"}


def classify_work_relevance(
    job: TraceCapturedJob,
    messages: list[NormalizedMessage],
    contexts: list[ContextCatalogEntry],
) -> WorkRelevanceAssessment:
    text = _combined_text(messages)
    if not text:
        return WorkRelevanceAssessment(
            trace_id=job.trace_id,
            task_category="unknown",
            work_related_score=0.0,
            personal_use_score=0.0,
            confidence=0.1,
            matched_context=[],
            evidence=["No normalized text was available for work relevance classification."],
            needs_review=True,
            analyzer_version=ANALYZER_VERSION,
        )

    category, category_terms = _detect_category(text)
    matched_context = _match_contexts(text, contexts)

    if category in PERSONAL_CATEGORIES:
        return WorkRelevanceAssessment(
            trace_id=job.trace_id,
            task_category=category,
            work_related_score=0.1,
            personal_use_score=0.8,
            confidence=0.8,
            matched_context=matched_context,
            evidence=[f"Detected personal category terms: {', '.join(category_terms)}."],
            needs_review=True,
            analyzer_version=ANALYZER_VERSION,
        )

    if matched_context and category in WORK_CATEGORIES:
        return WorkRelevanceAssessment(
            trace_id=job.trace_id,
            task_category=category,
            work_related_score=0.9,
            personal_use_score=0.02,
            confidence=0.8,
            matched_context=matched_context,
            evidence=[f"Matched catalog context and work category {category}."],
            needs_review=False,
            analyzer_version=ANALYZER_VERSION,
        )

    if matched_context:
        return WorkRelevanceAssessment(
            trace_id=job.trace_id,
            task_category=category,
            work_related_score=0.72,
            personal_use_score=0.05,
            confidence=0.62,
            matched_context=matched_context,
            evidence=["Matched catalog context but task category was weak or unknown."],
            needs_review=False,
            analyzer_version=ANALYZER_VERSION,
        )

    if category in WORK_CATEGORIES:
        return WorkRelevanceAssessment(
            trace_id=job.trace_id,
            task_category=category,
            work_related_score=0.55,
            personal_use_score=0.08,
            confidence=0.45,
            matched_context=[],
            evidence=[f"Detected work category {category} without catalog context."],
            needs_review=True,
            analyzer_version=ANALYZER_VERSION,
        )

    return WorkRelevanceAssessment(
        trace_id=job.trace_id,
        task_category="unknown",
        work_related_score=0.25,
        personal_use_score=0.1,
        confidence=0.2,
        matched_context=[],
        evidence=["No catalog context or known task category matched."],
        needs_review=True,
        analyzer_version=ANALYZER_VERSION,
    )


def _combined_text(messages: list[NormalizedMessage]) -> str:
    return "\n".join(message.content_text for message in messages if message.content_text).lower()


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
