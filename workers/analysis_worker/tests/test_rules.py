from models import NormalizedMessage, TraceCapturedJob
from rules import DETECTOR_VERSION, detect_anomalies, detect_coverage_alerts


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


def test_detects_high_trace_tokens():
    alerts = detect_anomalies(job(usage_total_tokens=25000))

    assert [alert.anomaly_type for alert in alerts] == ["high_trace_tokens"]
    assert alerts[0].observed_value == 25000
    assert alerts[0].threshold_value == 20000
    assert alerts[0].sample_trace_ids == ["trace_1"]


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
