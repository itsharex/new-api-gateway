from models import AnalysisContext, NormalizedMessage, TraceCapturedJob, WorkRelevanceAssessment
from rules import detect_anomalies, detect_coverage_alerts, detect_work_relevance_anomalies


def job(**overrides):
    values = {
        "type": "trace_captured",
        "trace_id": "trace_1",
        "route_pattern": "/v1/chat/completions",
        "protocol_family": "openai_chat",
        "capture_mode": "raw_and_normalized",
        "username": "alice",
        "model_requested": "gpt-4.1",
        "usage_total_tokens": 18,
        "token_fingerprint": "tkfp_raw",
        "fingerprint_display": "tkfp_display",
        "new_api_token_id": 42,
        "token_name_snapshot": "alice",
        "status_code": 200,
        "upstream_status_code": 200,
        "request_started_at": "2026-04-28T13:45:22Z",
        "request_body_size": 128,
        "response_body_size": 256,
    }
    values.update(overrides)
    return TraceCapturedJob(**values)


def test_legacy_identity_signals_no_longer_emit_anomalies():
    assert detect_anomalies(job(username="", status_code=200, upstream_status_code=200)) == []
    assert detect_anomalies(job(
        username="alice",
        token_name_snapshot="Alice API Token",
        identity_resolution_status="invalid_username",
    )) == []
    assert detect_anomalies(job(
        username="",
        identity_resolution_status="missing_username",
        status_code=200,
        upstream_status_code=200,
    )) == []


def test_detects_high_trace_tokens_from_effective_tokens():
    alerts = detect_anomalies(job(
        usage_prompt_tokens=50000,
        usage_cached_tokens=20000,
        usage_completion_tokens=12000,
        usage_total_tokens=62000,
    ))

    assert [alert.anomaly_type for alert in alerts] == ["high_trace_tokens"]
    assert alerts[0].observed_value == 42000
    assert alerts[0].threshold_value == 40000
    assert alerts[0].sample_trace_ids == ["trace_1"]


def test_token_name_snapshot_difference_does_not_imply_invalid_username():
    alerts = detect_anomalies(job(
        username="alice",
        token_name_snapshot="Alice API Token",
        identity_resolution_status="resolved",
    ))

    assert [alert.anomaly_type for alert in alerts] == []


def test_legacy_capture_and_model_signals_no_longer_emit_anomalies():
    assert detect_anomalies(job(
        capture_mode="raw_only",
        usage_total_tokens=0,
        response_body_size=2 * 1024 * 1024,
        route_pattern="/mj/*",
        protocol_family="midjourney",
    )) == []
    assert detect_anomalies(job(
        capture_mode="raw_and_minimal",
        usage_total_tokens=0,
        response_body_size=2 * 1024 * 1024,
        route_pattern="/mj/*",
        protocol_family="midjourney",
    )) == []
    assert detect_anomalies(job(model_requested="o1-pro", usage_total_tokens=500)) == []


def test_detects_normalization_gap_when_no_messages_for_normalized_route():
    alerts = detect_coverage_alerts(job(), messages=[])

    assert [alert.alert_code for alert in alerts] == ["normalization_gap"]
    assert alerts[0].severity == "high"
    assert alerts[0].route_pattern == "/v1/chat/completions"
    assert alerts[0].payload_shape_hash


def test_no_coverage_alert_when_messages_exist():
    message = NormalizedMessage(
        trace_id="trace_1",
        direction="request",
        sequence_index=0,
        role="user",
        modality="text",
        content_text="Summarize incident",
        content_text_hash="abc",
        media_url="",
        source_path="request.messages[0]",
        protocol_item_type="openai_chat_message",
        token_count_estimate=2,
        metadata={},
    )

    assert detect_coverage_alerts(job(), [message]) == []


def test_work_nonwork_conflict_is_review_only_and_not_an_anomaly():
    assessment = WorkRelevanceAssessment(
        trace_id="trace_conflict",
        task_category="job_search",
        work_related_score=0.5,
        personal_use_score=0.5,
        confidence=0.65,
        matched_context=[],
        evidence=[],
        needs_review=True,
        analyzer_version="test",
        decision="needs_review",
        recommended_action="review_conflict",
        score_breakdown={"work": 0.5, "non_work": 0.5, "risk": 0.0, "conflict": 0.5, "uncertainty": 0.35},
    )

    assert detect_work_relevance_anomalies(job(trace_id="trace_conflict"), assessment) == []


def test_llm_judge_work_related_assessment_produces_no_work_relevance_anomaly():
    assessment = WorkRelevanceAssessment(
        trace_id="trace_llm_work",
        task_category="coding",
        work_related_score=0.92,
        personal_use_score=0.0,
        confidence=0.92,
        matched_context=[{"type": "project", "name": "XWallet App", "source": "llm_judge"}],
        evidence=[{"kind": "work_context", "source": "llm_judge", "reason": "project coding task"}],
        needs_review=False,
        analyzer_version="work_relevance_mvp_2026_04_28+llm",
        decision="work_related",
        recommended_action="allow",
        score_breakdown={"work": 0.92, "non_work": 0.0, "risk": 0.0, "conflict": 0.0, "uncertainty": 0.08},
    )

    assert detect_work_relevance_anomalies(job(usage_total_tokens=25000), assessment) == []


def test_explicit_non_work_collapses_to_non_work_use():
    assessment = WorkRelevanceAssessment(
        trace_id="trace_non_work",
        task_category="job_search",
        work_related_score=0.0,
        personal_use_score=0.9,
        confidence=0.9,
        matched_context=[],
        evidence=[{"reason": "Matched job_search terms: resume."}],
        needs_review=False,
        analyzer_version="test",
        decision="non_work_related",
        recommended_action="alert_non_work",
        score_breakdown={"work": 0.0, "non_work": 0.9, "risk": 0.0, "conflict": 0.0, "uncertainty": 0.1},
    )

    alerts = detect_work_relevance_anomalies(job(trace_id="trace_non_work"), assessment)

    assert [alert.anomaly_type for alert in alerts] == ["non_work_use"]


def test_unknown_high_cost_is_record_only_and_not_an_anomaly():
    assessment = WorkRelevanceAssessment(
        trace_id="trace_unknown",
        task_category="unknown",
        work_related_score=0.0,
        personal_use_score=0.0,
        confidence=0.25,
        matched_context=[],
        evidence=[{"category": "no_match", "reason": "No context matched."}],
        needs_review=False,
        analyzer_version="work_relevance_mvp_2026_04_28",
        decision="unknown",
        recommended_action="record_only",
        score_breakdown={"work": 0.0, "non_work": 0.0, "risk": 0.0, "conflict": 0.0, "uncertainty": 1.0},
    )

    alerts = detect_work_relevance_anomalies(job(usage_total_tokens=25000), assessment)

    assert alerts == []


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


def test_detects_long_output_anomaly():
    alerts = detect_anomalies(job(usage_completion_tokens=16000, usage_total_tokens=17000))

    assert [alert.anomaly_type for alert in alerts] == ["long_output_anomaly"]
    assert alerts[0].severity == "medium"
    assert alerts[0].observed_value == 16000
    assert alerts[0].threshold_value == 16000
    assert "meeting or exceeding" in alerts[0].reason


def test_legacy_prompt_and_aggregate_signals_no_longer_emit_anomalies():
    messages = [
        NormalizedMessage(
            trace_id="trace_1",
            direction="request",
            sequence_index=index,
            role="user",
            modality="text",
            content_text="Run the same prompt",
            content_text_hash="same_hash",
            media_url="",
            source_path=f"request.messages[{index}]",
            protocol_item_type="openai_chat_message",
            token_count_estimate=4,
            metadata={},
        )
        for index in range(3)
    ]
    aggregate_context = AnalysisContext(
        daily_tokens_before=100000,
        short_window_tokens_before=10000,
        distinct_client_hashes_1h=3,
    )

    assert detect_anomalies(job(), messages=messages) == []
    assert detect_anomalies(job(usage_total_tokens=1), context=aggregate_context) == []
    assert detect_anomalies(job(
        request_started_at="not-a-timestamp",
        usage_total_tokens=1,
    ), context=aggregate_context) == []


def test_detects_off_hours_high_usage():
    context = AnalysisContext(local_timezone_offset_hours=8)

    alerts = detect_anomalies(job(
        request_started_at="2026-04-28T15:45:22Z",
        usage_prompt_tokens=15000,
        usage_completion_tokens=5000,
        usage_total_tokens=20000,
    ), context=context)

    assert [alert.anomaly_type for alert in alerts] == ["off_hours_high_usage"]
    assert alerts[0].severity == "medium"
    assert alerts[0].observed_value == 20000
    assert alerts[0].threshold_value == 20000
    assert "meeting or exceeding" in alerts[0].reason


def test_does_not_emit_token_scoped_aggregate_alerts_for_empty_token_fingerprint():
    context = AnalysisContext(
        daily_tokens_before=200000,
        short_window_tokens_before=20000,
        distinct_client_hashes_1h=10,
    )

    alerts = detect_anomalies(job(
        token_fingerprint="",
        usage_total_tokens=1,
        request_started_at="2026-04-28T02:45:22Z",
    ), context=context)

    assert [alert.anomaly_type for alert in alerts] == []


def test_malformed_request_timestamp_is_not_off_hours():
    alerts = detect_anomalies(job(
        request_started_at="not-a-timestamp",
        usage_prompt_tokens=15000,
        usage_completion_tokens=5000,
        usage_total_tokens=20000,
    ))

    assert [alert.anomaly_type for alert in alerts] == []


def test_high_trace_tokens_uses_p95_baseline():
    ctx = AnalysisContext(trace_effective_tokens_p95=30000.0)
    alerts = detect_anomalies(job(
        usage_prompt_tokens=34000,
        usage_completion_tokens=12000,
        usage_total_tokens=46000,
    ), context=ctx)
    high_trace = [a for a in alerts if a.anomaly_type == "high_trace_tokens"][0]
    assert high_trace.threshold_value == 45000.0
    assert high_trace.baseline_value == 30000.0


def test_high_trace_tokens_falls_back_to_default_without_baseline():
    ctx = AnalysisContext(trace_effective_tokens_p95=None)
    alerts = detect_anomalies(job(
        usage_prompt_tokens=30000,
        usage_completion_tokens=12000,
        usage_total_tokens=42000,
    ), context=ctx)
    high_trace = [a for a in alerts if a.anomaly_type == "high_trace_tokens"][0]
    assert high_trace.threshold_value == 40000


def test_long_output_uses_completion_p95_baseline():
    ctx = AnalysisContext(completion_tokens_p95=12000.0)
    alerts = detect_anomalies(job(usage_completion_tokens=18000, usage_total_tokens=20000), context=ctx)
    long_out = [a for a in alerts if a.anomaly_type == "long_output_anomaly"][0]
    assert long_out.threshold_value == 18000.0
    assert long_out.baseline_value == 12000.0


def test_off_hours_uses_effective_token_floor_even_with_baseline_context():
    ctx = AnalysisContext(
        off_hours_baseline=500.0,
        off_hours_mad=100.0,
        local_timezone_offset_hours=8,
    )
    alerts = detect_anomalies(job(
        request_started_at="2026-04-28T15:45:22Z",
        usage_prompt_tokens=15000,
        usage_completion_tokens=5000,
        usage_total_tokens=20000,
    ), context=ctx)
    off_hours = [a for a in alerts if a.anomaly_type == "off_hours_high_usage"][0]
    assert off_hours.threshold_value == 20000
    assert off_hours.baseline_value is None
