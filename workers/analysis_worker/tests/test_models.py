import json
from datetime import datetime, timedelta, timezone

from models import (
    AnalysisStage,
    AnalysisTask,
    AnalysisContext,
    ContextCatalogEntry,
    StreamEnvelope,
    TaskStatus,
    TraceCapturedJob,
    TraceStageStatus,
    WorkRelevanceAssessment,
    anomaly_id,
    bucket_start_hour,
    coverage_alert_id,
    parse_job,
    window_end_from_start,
)
from work_relevance import ANALYZER_VERSION


def test_stream_envelope_defaults_to_core_stage_attempt_one():
    envelope = StreamEnvelope(trace_id="trace_1")

    assert envelope.trace_id == "trace_1"
    assert envelope.stage == AnalysisStage.CORE
    assert envelope.attempt == 1
    assert envelope.hints == {}


def test_analysis_task_tracks_lease_and_error_fields():
    task = AnalysisTask(
        trace_id="trace_1",
        stage=AnalysisStage.ENRICHMENT,
        status=TaskStatus.QUEUED,
        attempt_count=0,
        max_attempts=5,
    )

    assert task.lease_owner == ""
    assert task.last_error_code == ""
    assert task.last_error_message == ""
    assert task.updated_at == ""


def test_trace_stage_status_values_match_dual_stage_summary_model():
    assert TraceStageStatus.PENDING.value == "pending"
    assert TraceStageStatus.PROCESSING.value == "processing"
    assert TraceStageStatus.COMPLETED.value == "completed"
    assert TraceStageStatus.FAILED.value == "failed"
    assert TraceStageStatus.NOT_REQUIRED.value == "not_required"


def test_parse_job_keeps_gateway_contract_fields():
    job = parse_job(json.dumps({
        "type": "trace_captured",
        "trace_id": "trace_123",
        "route_pattern": "/v1/chat/completions",
        "protocol_family": "openai_chat",
        "capture_mode": "raw_and_normalized",
        "username": "alice",
        "request_raw_ref": "raw/2026/04/28/trace_123/request_body.bin",
        "response_raw_ref": "raw/2026/04/28/trace_123/response_body.bin",
        "model_requested": "gpt-4.1",
        "usage_prompt_tokens": 11,
        "usage_completion_tokens": 7,
        "usage_total_tokens": 18,
        "usage_reasoning_tokens": 2,
        "usage_cached_tokens": 3,
        "token_fingerprint": "tkfp_raw_value",
        "fingerprint_display": "tkfp_display",
        "new_api_token_id": 42,
        "token_name_snapshot": "alice",
        "identity_resolution_status": "resolved",
        "client_ip_hash": "client_hash_current",
        "user_agent_hash": "ua_hash_current",
        "request_body_size": 128,
        "response_body_size": 256
    }))

    assert job == TraceCapturedJob(
        type="trace_captured",
        trace_id="trace_123",
        route_pattern="/v1/chat/completions",
        protocol_family="openai_chat",
        capture_mode="raw_and_normalized",
        username="alice",
        request_raw_ref="raw/2026/04/28/trace_123/request_body.bin",
        response_raw_ref="raw/2026/04/28/trace_123/response_body.bin",
        request_headers_ref="",
        response_headers_ref="",
        request_content_type="",
        response_content_type="",
        model_requested="gpt-4.1",
        usage_prompt_tokens=11,
        usage_completion_tokens=7,
        usage_total_tokens=18,
        usage_reasoning_tokens=2,
        usage_cached_tokens=3,
        token_fingerprint="tkfp_raw_value",
        fingerprint_display="tkfp_display",
        new_api_token_id=42,
        token_name_snapshot="alice",
        identity_resolution_status="resolved",
        client_ip_hash="client_hash_current",
        user_agent_hash="ua_hash_current",
        status_code=0,
        upstream_status_code=0,
        stream=False,
        request_started_at="",
        request_body_size=128,
        response_body_size=256
    )


def test_bucket_start_hour_truncates_iso_timestamp():
    assert bucket_start_hour("2026-04-28T13:45:22Z") == "2026-04-28T13:00:00+00:00"


def test_anomaly_id_is_stable_for_same_rule_and_trace():
    first = anomaly_id("high_trace_tokens", "trace_123", "alice")
    second = anomaly_id("high_trace_tokens", "trace_123", "alice")

    assert first == second
    assert first.startswith("anom_high_trace_tokens_")


def test_coverage_alert_id_groups_by_alert_route_and_shape():
    first = coverage_alert_id("normalization_gap", "/v1/chat/completions", "abc123")
    second = coverage_alert_id("normalization_gap", "/v1/chat/completions", "abc123")
    other = coverage_alert_id("normalization_gap", "/v1/responses", "abc123")

    assert first == second
    assert first != other
    assert first.startswith("cov_normalization_gap_")


def test_window_end_from_start_empty_value_honors_offset():
    before = datetime.now(timezone.utc) + timedelta(seconds=60)
    parsed = datetime.fromisoformat(window_end_from_start("", seconds=60))
    after = datetime.now(timezone.utc) + timedelta(seconds=60)

    assert before <= parsed <= after


def test_context_catalog_entry_normalizes_keyword_lists():
    entry = ContextCatalogEntry(
        id=1,
        context_type="repo",
        name="new-api-gateway",
        description="Audit gateway for new-api",
        keywords=["Gateway", " new-api ", ""],
        aliases=["audit gateway", "Gateway"],
        owner="platform",
        expected_task_categories=["coding", "debugging"],
        expected_models=["gpt-4.1"],
        expected_usage_level="normal",
        active=True,
    )

    assert entry.search_terms() == ["gateway", "new-api", "audit gateway", "new-api-gateway"]


def test_work_relevance_assessment_converts_to_analysis_result():
    assessment = WorkRelevanceAssessment(
        trace_id="trace_1",
        task_category="coding",
        work_related_score=0.82,
        personal_use_score=0.05,
        confidence=0.74,
        matched_context=[{"type": "repo", "name": "new-api-gateway", "matched_terms": ["gateway"]}],
        evidence=["Request matched repo context."],
        needs_review=False,
        analyzer_version=ANALYZER_VERSION,
    )

    result = assessment.to_analysis_result()

    assert result.category == "work_relevance"
    assert result.label == "coding"
    assert result.score == 0.82
    assert result.confidence == 0.74
    assert result.result["matched_context"][0]["name"] == "new-api-gateway"


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
        needs_review=False,
        analyzer_version=ANALYZER_VERSION,
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
    assert result.severity == ""
    assert result.result["decision"] == "non_work_related"
    assert result.result["recommended_action"] == "alert_non_work"
    assert result.result["score_breakdown"]["non_work"] == 0.9
    assert result.result["evidence"][0]["category"] == "job_search"


def test_analysis_context_accepts_effective_and_legacy_rule_fields():
    ctx = AnalysisContext(
        daily_tokens_before=1234,
        daily_token_limit=200000,
        short_window_tokens_before=80,
        short_window_token_threshold=9000,
        expensive_models={"o1-pro", " gpt-4.5-preview "},
        expensive_model_token_threshold=600,
        trace_effective_tokens_p95=15000.0,
        trace_tokens_p95=17000.0,
        completion_tokens_p95=6000.0,
        off_hours_baseline=1000.0,
        off_hours_mad=300.0,
        model_baselines={"o1-pro": 400.0, "gpt-4.5-preview": 350.0},
        baseline_computed_at="2026-05-18T12:00:00+00:00",
    )
    assert ctx.daily_tokens_before == 1234
    assert ctx.expensive_model_token_threshold == 600
    assert ctx.trace_effective_tokens_p95 == 15000.0
    assert ctx.trace_tokens_p95 == 17000.0
    assert ctx.completion_tokens_p95 == 6000.0
    assert ctx.model_baselines["o1-pro"] == 400.0
    assert ctx.expensive_model_set() == {"o1-pro", "gpt-4.5-preview"}
    assert ctx.baseline_computed_at is not None


def test_analysis_context_defaults_preserve_legacy_rule_fields():
    ctx = AnalysisContext()

    assert ctx.trace_effective_tokens_p95 is None
    assert ctx.trace_tokens_p95 is None
    assert ctx.completion_tokens_p95 is None
    assert ctx.long_output_token_threshold == 8_000
    assert ctx.off_hours_token_threshold == 2_000
    assert ctx.repeated_prompt_threshold == 3
    assert ctx.model_baselines is None
    assert ctx.baseline_computed_at is None


from models import message_key


def test_message_key_is_deterministic_for_same_inputs():
    k1 = message_key("user", "text", "hello")
    k2 = message_key("user", "text", "hello")
    assert k1 == k2
    assert len(k1) == 64  # sha256 hex


def test_message_key_differs_by_role():
    assert message_key("user", "text", "hi") != message_key("assistant", "text", "hi")


def test_message_key_differs_by_modality():
    assert message_key("user", "text", "hi") != message_key("user", "audio", "hi")


def test_message_key_differs_by_content():
    assert message_key("user", "text", "hi") != message_key("user", "text", "hello")


def test_message_key_length_prefix_prevents_collision():
    # 字段值含分隔字符 ":": ("a:b", "c") 与 ("a", ":b" 不冲突)
    # 字段值含 null byte: ("a\x00b", "c", "x") 与 ("a", "b\x00c", "x") 不冲突
    assert message_key("a:b", "c", "x") != message_key("a", ":b", "x")
    assert message_key("a\x00b", "c", "x") != message_key("a", "b\x00c", "x")
