from models import ContextCatalogEntry, NormalizedMessage, TraceCapturedJob
from work_relevance import ANALYZER_VERSION, classify_work_relevance


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
    assert assessment.work_related_score == 0.9
    assert assessment.personal_use_score == 0.02
    assert assessment.confidence >= 0.75
    assert assessment.needs_review is False
    assert assessment.analyzer_version == ANALYZER_VERSION
    assert assessment.matched_context[0]["name"] == "new-api-gateway"


def test_classifies_personal_chat_as_review_needed():
    assessment = classify_work_relevance(
        job(),
        [message("Write a funny birthday party toast for my friend.")],
        [context()],
    )

    assert assessment.task_category == "personal_chat"
    assert assessment.work_related_score == 0.1
    assert assessment.personal_use_score == 0.8
    assert assessment.needs_review is True
    assert assessment.matched_context == []


def test_empty_messages_are_unknown_and_low_confidence():
    assessment = classify_work_relevance(job(), [], [context()])

    assert assessment.task_category == "unknown"
    assert assessment.work_related_score == 0.0
    assert assessment.personal_use_score == 0.0
    assert assessment.confidence == 0.1
    assert assessment.needs_review is True
