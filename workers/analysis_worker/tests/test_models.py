import json
from datetime import datetime, timedelta, timezone

from models import (
    ContextCatalogEntry,
    TraceCapturedJob,
    WorkRelevanceAssessment,
    anomaly_id,
    bucket_start_hour,
    coverage_alert_id,
    parse_job,
    window_end_from_start,
)


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
        analyzer_version="work_relevance_mvp_2026_04_28",
    )

    result = assessment.to_analysis_result()

    assert result.category == "work_relevance"
    assert result.label == "coding"
    assert result.score == 0.82
    assert result.confidence == 0.74
    assert result.result["matched_context"][0]["name"] == "new-api-gateway"
