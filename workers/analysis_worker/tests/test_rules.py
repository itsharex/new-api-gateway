from models import AnalysisContext, NormalizedMessage, TraceCapturedJob, WorkRelevanceAssessment
from rules import DETECTOR_VERSION, detect_anomalies, detect_coverage_alerts, detect_work_relevance_anomalies


def job(**overrides):
    values = {
        "type": "trace_captured",
        "trace_id": "trace_1",
        "route_pattern": "/v1/chat/completions",
        "protocol_family": "openai_chat",
        "capture_mode": "raw_and_normalized",
        "employee_no": "E10001",
        "model_requested": "gpt-4.1",
        "usage_total_tokens": 18,
        "token_fingerprint": "tkfp_raw",
        "fingerprint_display": "tkfp_display",
        "new_api_token_id": 42,
        "token_name_snapshot": "E10001",
        "status_code": 200,
        "upstream_status_code": 200,
        "request_started_at": "2026-04-28T13:45:22Z",
        "request_body_size": 128,
        "response_body_size": 256,
    }
    values.update(overrides)
    return TraceCapturedJob(**values)


def test_detects_identity_unresolved_success():
    alerts = detect_anomalies(job(employee_no="", status_code=200, upstream_status_code=200))

    assert [alert.anomaly_type for alert in alerts] == ["identity_unresolved_success"]
    assert alerts[0].severity == "high"
    assert alerts[0].observed_value == 1
    assert alerts[0].threshold_value == 0
    assert alerts[0].detector_version == DETECTOR_VERSION


def test_anomaly_windows_are_deterministic_without_request_timestamp():
    alerts = detect_anomalies(job(
        employee_no="",
        request_started_at="",
        status_code=200,
        upstream_status_code=200,
    ))

    assert [alert.anomaly_type for alert in alerts] == ["identity_unresolved_success"]
    assert alerts[0].window_start == "1970-01-01T00:00:00+00:00"
    assert alerts[0].window_end == "1970-01-01T00:01:00+00:00"


def test_detects_high_trace_tokens():
    alerts = detect_anomalies(job(usage_total_tokens=25000))

    assert [alert.anomaly_type for alert in alerts] == ["high_trace_tokens"]
    assert alerts[0].observed_value == 25000
    assert alerts[0].threshold_value == 20000
    assert alerts[0].sample_trace_ids == ["trace_1"]


def test_token_name_snapshot_difference_does_not_imply_invalid_employee_number():
    alerts = detect_anomalies(job(
        employee_no="E10001",
        token_name_snapshot="Alice API Token",
        identity_resolution_status="resolved",
    ))

    assert [alert.anomaly_type for alert in alerts] == []


def test_detects_invalid_employee_number_from_identity_resolution_status():
    alerts = detect_anomalies(job(
        employee_no="E10001",
        token_name_snapshot="Alice API Token",
        identity_resolution_status="invalid_employee_no",
    ))

    assert [alert.anomaly_type for alert in alerts] == ["invalid_employee_no"]
    assert alerts[0].severity == "high"


def test_detects_raw_only_large_response():
    alerts = detect_anomalies(job(
        capture_mode="raw_only",
        usage_total_tokens=0,
        response_body_size=2 * 1024 * 1024,
        route_pattern="/mj/*",
        protocol_family="midjourney",
    ))

    assert [alert.anomaly_type for alert in alerts] == ["raw_only_large_response"]
    assert "raw-only" in alerts[0].reason


def test_detects_raw_and_minimal_large_response():
    alerts = detect_anomalies(job(
        capture_mode="raw_and_minimal",
        usage_total_tokens=0,
        response_body_size=2 * 1024 * 1024,
        route_pattern="/mj/*",
        protocol_family="midjourney",
    ))

    assert [alert.anomaly_type for alert in alerts] == ["raw_only_large_response"]


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


def test_detects_low_work_relevance_high_cost_anomaly():
    assessment = WorkRelevanceAssessment(
        trace_id="trace_personal",
        task_category="personal_chat",
        work_related_score=0.1,
        personal_use_score=0.8,
        confidence=0.8,
        matched_context=[],
        evidence=["Detected personal category terms: birthday."],
        needs_review=True,
        analyzer_version="work_relevance_mvp_2026_04_28",
    )
    alerts = detect_work_relevance_anomalies(
        job(trace_id="trace_personal", usage_total_tokens=25000, model_requested="gpt-4.1"),
        assessment,
    )

    assert len(alerts) == 1
    assert alerts[0].anomaly_type == "low_work_relevance_high_cost"
    assert alerts[0].severity == "high"
    assert alerts[0].observed_value == 25000
    assert alerts[0].threshold_value == 20000
    assert "personal use score" in alerts[0].reason


def test_does_not_detect_low_work_relevance_high_cost_for_low_tokens():
    assessment = WorkRelevanceAssessment(
        trace_id="trace_personal",
        task_category="personal_chat",
        work_related_score=0.1,
        personal_use_score=0.8,
        confidence=0.8,
        matched_context=[],
        evidence=[],
        needs_review=True,
        analyzer_version="work_relevance_mvp_2026_04_28",
    )

    assert detect_work_relevance_anomalies(job(usage_total_tokens=100), assessment) == []


def test_detects_missing_employee_number():
    alerts = detect_anomalies(job(employee_no="", status_code=401, upstream_status_code=401))

    assert [alert.anomaly_type for alert in alerts] == ["missing_employee_no"]
    assert alerts[0].severity == "high"
    assert alerts[0].observed_value == 1
    assert alerts[0].threshold_value == 0


def test_detects_expensive_model_overuse():
    alerts = detect_anomalies(job(model_requested="o1-pro", usage_total_tokens=501))

    assert [alert.anomaly_type for alert in alerts] == ["expensive_model_overuse"]
    assert alerts[0].severity == "high"
    assert alerts[0].observed_value == 501
    assert alerts[0].threshold_value == 500


def test_detects_long_output_anomaly():
    alerts = detect_anomalies(job(usage_completion_tokens=8001, usage_total_tokens=9000))

    assert [alert.anomaly_type for alert in alerts] == ["long_output_anomaly"]
    assert alerts[0].severity == "medium"
    assert alerts[0].observed_value == 8001
    assert alerts[0].threshold_value == 8000


def test_detects_repeated_prompt_within_trace():
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

    alerts = detect_anomalies(job(), messages=messages)

    assert [alert.anomaly_type for alert in alerts] == ["repeated_prompt"]
    assert alerts[0].severity == "medium"
    assert alerts[0].observed_value == 3
    assert alerts[0].threshold_value == 3


def test_detects_daily_token_limit_exceeded():
    context = AnalysisContext(daily_total_tokens=100001)

    alerts = detect_anomalies(job(), context=context)

    assert [alert.anomaly_type for alert in alerts] == ["daily_token_limit_exceeded"]
    assert alerts[0].severity == "high"
    assert alerts[0].observed_value == 100001
    assert alerts[0].threshold_value == 100000


def test_detects_short_window_token_spike():
    context = AnalysisContext(short_window_total_tokens=10001)

    alerts = detect_anomalies(job(), context=context)

    assert [alert.anomaly_type for alert in alerts] == ["short_window_token_spike"]
    assert alerts[0].severity == "medium"
    assert alerts[0].observed_value == 10001
    assert alerts[0].threshold_value == 10000


def test_detects_off_hours_high_usage():
    context = AnalysisContext(local_timezone_offset_hours=8)

    alerts = detect_anomalies(job(
        request_started_at="2026-04-28T14:45:22Z",
        usage_total_tokens=2001,
    ), context=context)

    assert [alert.anomaly_type for alert in alerts] == ["off_hours_high_usage"]
    assert alerts[0].severity == "medium"
    assert alerts[0].observed_value == 2001
    assert alerts[0].threshold_value == 2000


def test_detects_possible_token_leak_signal():
    context = AnalysisContext(distinct_client_hashes_last_hour=3)

    alerts = detect_anomalies(job(), context=context)

    assert [alert.anomaly_type for alert in alerts] == ["possible_token_leak"]
    assert alerts[0].severity == "high"
    assert alerts[0].observed_value == 3
    assert alerts[0].threshold_value == 3
