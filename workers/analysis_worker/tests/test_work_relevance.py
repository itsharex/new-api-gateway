import work_relevance

from llm_judge import LLMJudgeUnavailable
from models import ContextCatalogEntry, NormalizedMessage, TraceCapturedJob
from work_relevance import ANALYZER_VERSION, classify_work_relevance, extract_user_intent


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


def response_message(text: str) -> NormalizedMessage:
    return NormalizedMessage(
        trace_id="trace_1",
        direction="response",
        sequence_index=1,
        role="assistant",
        modality="text",
        content_text=text,
        content_text_hash="hash-response",
        media_url="",
        source_path="response.output[0]",
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


class StubJudge:
    def __init__(self, result=None, error=None):
        self.result = result or {}
        self.error = error
        self.calls = []

    def judge(self, bundle):
        self.calls.append(bundle)
        if self.error is not None:
            raise self.error
        return self.result


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
    assert assessment.needs_review is False
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


def test_module_no_longer_exposes_removed_classifier():
    removed_name = "classify_work_relevance_with_" + "".join(["em", "bed", "dings"])
    assert not hasattr(work_relevance, removed_name)




def test_low_token_personal_use_decides_non_work():
    assessment = classify_work_relevance(
        job(usage_total_tokens=120),
        [message("Write a birthday toast for my friend and make it funny.")],
        [context()],
    )

    assert assessment.task_category == "personal_chat"
    assert assessment.decision == "non_work_related"
    assert assessment.recommended_action == "alert_non_work"
    assert assessment.needs_review is False
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


def test_unknown_high_cost_no_longer_requires_review():
    assessment = classify_work_relevance(
        job(usage_total_tokens=25000),
        messages=[message("tell me something vague")],
        contexts=[],
    )

    assert assessment.decision == "unknown"
    assert assessment.recommended_action == "record_only"
    assert assessment.needs_review is False


def test_non_work_alert_does_not_require_review():
    assessment = classify_work_relevance(
        job(usage_total_tokens=300),
        [message("Rewrite my resume and prepare answers for a senior backend interview.")],
        [context()],
    )

    assert assessment.decision == "non_work_related"
    assert assessment.recommended_action == "alert_non_work"
    assert assessment.needs_review is False


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


def test_extract_user_intent_excludes_assistant_response_text():
    intent = extract_user_intent([
        message("Debug the relay handler in new-api gateway."),
        response_message("Here is a bedtime story about dragons."),
    ])

    assert "debug the relay handler" in intent.text
    assert "bedtime story" not in intent.text


def test_extract_user_intent_records_truncation():
    intent = extract_user_intent([message("A" * 30)], max_chars=12)

    assert intent.text == "a" * 12
    assert intent.truncated is True
    assert intent.original_length == 30


def test_strong_alias_match_short_circuits_to_work_related_allow():
    assessment = classify_work_relevance(
        job(usage_total_tokens=800),
        [message("Please inspect relay auth failures.")],
        [context()],
    )

    assert assessment.decision == "work_related"
    assert assessment.recommended_action == "allow"
    assert assessment.matched_context[0]["source"] == "catalog_alias"
    assert assessment.confidence >= 0.95


def test_weak_keyword_medium_cost_calls_llm_and_adapts_work_related_result():
    judge = StubJudge({
        "decision": "work_related",
        "recommended_action": "allow",
        "task_category": "documentation",
        "confidence": 0.82,
    })

    assessment = classify_work_relevance(
        job(usage_total_tokens=6000),
        [message("Please update the gateway docs and guide wording.")],
        [context()],
        llm_judge=judge,
    )

    assert len(judge.calls) == 1
    assert assessment.decision == "work_related"
    assert assessment.recommended_action == "allow"
    assert assessment.task_category == "documentation"
    assert assessment.work_related_score >= 0.7
    assert assessment.evidence[-1]["source"] == "llm_judge"


def test_conflict_calls_llm_and_adapts_review_conflict_result():
    judge = StubJudge({
        "decision": "needs_review",
        "recommended_action": "review_conflict",
        "task_category": "job_search",
        "confidence": 0.74,
    })

    assessment = classify_work_relevance(
        job(usage_total_tokens=1200),
        [message("In relay, rewrite my resume bullet about debugging this route.")],
        [context()],
        llm_judge=judge,
    )

    assert len(judge.calls) == 1
    assert assessment.decision == "needs_review"
    assert assessment.recommended_action == "review_conflict"
    assert assessment.needs_review is True
    assert assessment.evidence[-1]["source"] == "llm_judge"


def test_llm_unavailable_on_conflict_uses_conservative_fallback():
    judge = StubJudge(error=LLMJudgeUnavailable("timeout", "judge timed out"))

    assessment = classify_work_relevance(
        job(usage_total_tokens=1200),
        [message("In relay, rewrite my resume bullet about debugging this route.")],
        [context()],
        llm_judge=judge,
    )

    assert len(judge.calls) == 1
    assert assessment.decision == "needs_review"
    assert assessment.recommended_action == "review_conflict"
    assert assessment.evidence[-1] == {
        "kind": "llm_unavailable",
        "category": "timeout",
        "weight": 0.0,
        "source": "llm_judge",
        "snippet": "timeout",
        "reason": "LLM judge unavailable due to timeout.",
    }


def test_llm_unavailable_on_high_cost_unknown_records_only():
    judge = StubJudge(error=LLMJudgeUnavailable("http_error", "503"))

    assessment = classify_work_relevance(
        job(usage_total_tokens=25000),
        [message("Explain this idea in a clearer way.")],
        [context()],
        llm_judge=judge,
    )

    assert len(judge.calls) == 1
    assert assessment.decision == "unknown"
    assert assessment.recommended_action == "record_only"
    assert assessment.needs_review is False
    assert assessment.evidence[-1]["kind"] == "llm_unavailable"
    assert assessment.evidence[-1]["category"] == "http_error"
    assert assessment.evidence[-1]["source"] == "llm_judge"


def test_malformed_llm_payload_on_conflict_uses_conservative_fallback():
    judge = StubJudge({
        "decision": "definitely_allow",
        "recommended_action": "ship_it",
        "confidence": 0.99,
    })

    assessment = classify_work_relevance(
        job(usage_total_tokens=1200),
        [message("In relay, rewrite my resume bullet about debugging this route.")],
        [context()],
        llm_judge=judge,
    )

    assert len(judge.calls) == 1
    assert assessment.decision == "needs_review"
    assert assessment.recommended_action == "review_conflict"
    assert assessment.needs_review is True
    assert assessment.evidence[-1]["kind"] == "llm_unavailable"
    assert assessment.evidence[-1]["category"] == "invalid_result"
    assert assessment.evidence[-1]["source"] == "llm_judge"


def test_contradictory_llm_decision_and_action_use_conservative_fallback():
    judge = StubJudge({
        "decision": "work_related",
        "recommended_action": "alert_non_work",
        "task_category": "debugging",
        "confidence": 0.91,
    })

    assessment = classify_work_relevance(
        job(usage_total_tokens=1200),
        [message("In relay, rewrite my resume bullet about debugging this route.")],
        [context()],
        llm_judge=judge,
    )

    assert len(judge.calls) == 1
    assert assessment.decision == "needs_review"
    assert assessment.recommended_action == "review_conflict"
    assert assessment.needs_review is True
    assert assessment.evidence[-1]["kind"] == "llm_unavailable"
    assert assessment.evidence[-1]["category"] == "invalid_result"
    assert assessment.evidence[-1]["source"] == "llm_judge"


def test_weak_match_with_non_work_and_llm_unavailable_uses_review_conflict_fallback():
    judge = StubJudge(error=LLMJudgeUnavailable("timeout", "judge timed out"))

    assessment = classify_work_relevance(
        job(usage_total_tokens=1200),
        [message("In the gateway repo, rewrite my resume bullet and interview summary.")],
        [context()],
        llm_judge=judge,
    )

    assert len(judge.calls) == 1
    assert assessment.decision == "needs_review"
    assert assessment.recommended_action == "review_conflict"
    assert assessment.needs_review is True
    assert assessment.evidence[-1]["kind"] == "llm_unavailable"
    assert assessment.evidence[-1]["category"] == "timeout"
    assert assessment.evidence[-1]["source"] == "llm_judge"


def test_allow_llm_false_skips_judge_but_marks_enrichment_request_for_conflict():
    judge = StubJudge({
        "decision": "work_related",
        "recommended_action": "allow",
        "task_category": "debugging",
        "confidence": 0.91,
    })

    assessment = classify_work_relevance(
        job(usage_total_tokens=1200),
        [message("In the gateway repo, rewrite my resume bullet and interview summary.")],
        [context()],
        llm_judge=judge,
        allow_llm=False,
    )

    assert judge.calls == []
    assert assessment.decision == "needs_review"
    assert assessment.recommended_action == "review_conflict"
    assert assessment.needs_review is True
    assert assessment.llm_judge_requested is True
    assert assessment.llm_judge_reason == "mixed_signals"
    assert not any(item.get("source") == "llm_judge" for item in assessment.evidence if isinstance(item, dict))
