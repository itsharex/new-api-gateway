import json
from datetime import datetime, timedelta, timezone

from models import TraceCapturedJob, anomaly_id, bucket_start_hour, coverage_alert_id, parse_job, window_end_from_start


def test_parse_job_keeps_gateway_contract_fields():
    job = parse_job(json.dumps({
        "type": "trace_captured",
        "trace_id": "trace_123",
        "route_pattern": "/v1/chat/completions",
        "protocol_family": "openai_chat",
        "capture_mode": "raw_and_normalized",
        "employee_no": "E10001",
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
        "token_name_snapshot": "E10001",
        "identity_resolution_status": "resolved",
        "request_body_size": 128,
        "response_body_size": 256
    }))

    assert job == TraceCapturedJob(
        type="trace_captured",
        trace_id="trace_123",
        route_pattern="/v1/chat/completions",
        protocol_family="openai_chat",
        capture_mode="raw_and_normalized",
        employee_no="E10001",
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
        token_name_snapshot="E10001",
        identity_resolution_status="resolved",
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
    first = anomaly_id("high_trace_tokens", "trace_123", "E10001")
    second = anomaly_id("high_trace_tokens", "trace_123", "E10001")

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
