from unittest.mock import MagicMock

from models import ContextCatalogEntry, NormalizedMessage, TraceCapturedJob
from work_relevance import ANALYZER_VERSION, classify_work_relevance, classify_work_relevance_with_embeddings


def job(**overrides):
    values = {
        "type": "trace_captured",
        "trace_id": "trace_1",
        "route_pattern": "/v1/chat/completions",
        "protocol_family": "openai_chat",
        "capture_mode": "raw_and_normalized",
        "username": "alice",
        "model_requested": "gpt-4.1",
        "usage_total_tokens": 1200,
    }
    values.update(overrides)
    return TraceCapturedJob(**values)


def message(text: str) -> NormalizedMessage:
    return NormalizedMessage(
        trace_id="trace_1",
        direction="request",
        sequence_index=0,
        role="user",
        modality="text",
        content_text=text,
        content_text_hash="hash",
        media_url="",
        source_path="request.messages[0]",
        protocol_item_type="openai_chat_message",
        token_count_estimate=10,
        metadata={},
    )


def context() -> ContextCatalogEntry:
    return ContextCatalogEntry(
        id=1,
        context_type="repo",
        name="new-api-gateway",
        description="Audit gateway",
        keywords=["new-api", "gateway", "audit"],
        aliases=["relay"],
        owner="platform",
        expected_task_categories=["coding", "debugging", "documentation"],
        expected_models=["gpt-4.1"],
        expected_usage_level="normal",
        active=True,
    )


def test_classifies_context_matched_coding_as_work_related():
    assessment = classify_work_relevance(
        job(),
        [message("Debug the new-api gateway relay route and write tests.")],
        [context()],
    )

    assert assessment.task_category == "debugging"
    assert assessment.work_related_score >= 0.9
    assert assessment.personal_use_score == 0.0
    assert assessment.confidence >= 0.9
    assert assessment.needs_review is False
    assert assessment.analyzer_version == ANALYZER_VERSION
    assert assessment.decision == "work_related"
    assert assessment.recommended_action == "allow"
    assert assessment.score_breakdown["work"] >= 0.9
    assert any(item["kind"] == "work_task" for item in assessment.evidence)
    assert assessment.matched_context[0]["name"] == "new-api-gateway"


def test_classifies_personal_chat_as_review_needed():
    assessment = classify_work_relevance(
        job(),
        [message("Write a funny birthday party toast for my friend.")],
        [context()],
    )

    assert assessment.task_category == "personal_chat"
    assert assessment.work_related_score == 0.0
    assert assessment.personal_use_score == 0.8
    assert assessment.decision == "non_work_related"
    assert assessment.recommended_action == "alert_non_work"
    assert assessment.needs_review is True
    assert assessment.matched_context == []


def test_empty_messages_are_unknown_and_low_confidence():
    assessment = classify_work_relevance(job(), [], [context()])

    assert assessment.task_category == "unknown"
    assert assessment.work_related_score == 0.0
    assert assessment.personal_use_score == 0.0
    assert assessment.confidence == 0.25
    assert assessment.decision == "unknown"
    assert assessment.recommended_action == "record_only"
    assert assessment.needs_review is False


def test_embedding_match_overrides_keyword_classification():
    mock_embedding_client = MagicMock()
    mock_embedding_client.embed.return_value = [0.1] * 1024

    mock_connection = MagicMock()
    mock_cursor = MagicMock()
    mock_connection.cursor.return_value = mock_cursor
    mock_cursor.fetchall.return_value = [
        ("repo", "Backend API Development", 0.85, ["coding"], ["gpt-4.1"]),
    ]

    test_job = job()
    messages = [message("Help me implement a REST API endpoint for user authentication")]

    result = classify_work_relevance_with_embeddings(
        test_job, messages, [], mock_embedding_client, mock_connection,
    )

    assert result.task_category == "coding"
    assert result.work_related_score >= 0.7
    assert result.confidence >= 0.7


def test_embedding_falls_back_to_keywords_when_no_match():
    mock_embedding_client = MagicMock()
    mock_embedding_client.embed.return_value = [0.1] * 1024

    mock_connection = MagicMock()
    mock_cursor = MagicMock()
    mock_connection.cursor.return_value = mock_cursor
    mock_cursor.fetchall.return_value = []  # No matches

    test_job = job()
    messages = [message("Help me debug this error in my code")]

    result = classify_work_relevance_with_embeddings(
        test_job, messages, [], mock_embedding_client, mock_connection,
    )

    # Falls back to keyword classification
    assert result.task_category == "debugging"


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
