import json
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from hashlib import sha256
from typing import Any


@dataclass(frozen=True)
class TraceCapturedJob:
    type: str
    trace_id: str
    route_pattern: str
    protocol_family: str
    capture_mode: str
    employee_no: str
    request_raw_ref: str = ""
    request_headers_ref: str = ""
    response_raw_ref: str = ""
    response_headers_ref: str = ""
    request_content_type: str = ""
    response_content_type: str = ""
    model_requested: str = ""
    usage_prompt_tokens: int = 0
    usage_completion_tokens: int = 0
    usage_total_tokens: int = 0
    usage_reasoning_tokens: int = 0
    usage_cached_tokens: int = 0
    token_fingerprint: str = ""
    fingerprint_display: str = ""
    new_api_token_id: int = 0
    token_name_snapshot: str = ""
    status_code: int = 0
    upstream_status_code: int = 0
    stream: bool = False
    request_started_at: str = ""
    request_body_size: int = 0
    response_body_size: int = 0


@dataclass(frozen=True)
class NormalizedMessage:
    trace_id: str
    direction: str
    sequence_index: int
    role: str
    modality: str
    content_text: str
    content_text_hash: str
    media_url: str
    source_path: str
    protocol_item_type: str
    token_count_estimate: int
    metadata: dict[str, Any]


@dataclass(frozen=True)
class AnalysisResult:
    trace_id: str
    analyzer_name: str
    analyzer_version: str
    policy_version: str
    category: str
    label: str
    score: float
    confidence: float
    severity: str
    result: dict[str, Any]


@dataclass(frozen=True)
class UsageAggregateDelta:
    bucket_start: str
    bucket_size: str
    token_fingerprint: str
    new_api_token_id: int
    employee_no: str
    token_name_snapshot: str
    model: str
    route_pattern: str
    protocol_family: str
    request_count: int
    success_count: int
    error_count: int
    stream_count: int
    prompt_tokens: int
    completion_tokens: int
    total_tokens: int
    reasoning_tokens: int
    cached_tokens: int
    request_body_bytes: int
    response_body_bytes: int


@dataclass(frozen=True)
class AnomalyAlert:
    anomaly_id: str
    anomaly_type: str
    severity: str
    token_fingerprint: str
    fingerprint_display: str
    new_api_token_id: int
    employee_no: str
    token_name_snapshot: str
    window_start: str
    window_end: str
    observed_value: float
    threshold_value: float
    baseline_value: float | None
    model: str
    route_pattern: str
    sample_trace_ids: list[str]
    reason: str
    detector_version: str


@dataclass(frozen=True)
class CoverageAlert:
    alert_id: str
    alert_code: str
    severity: str
    method: str
    route_pattern: str
    raw_path: str
    content_type: str
    protocol_family: str
    payload_shape_hash: str
    normalizer: str
    normalizer_version: str
    sample_trace_ids: list[str]
    message: str
    affected_trace_count: int = 1
    affected_token_count: int = 0
    affected_employee_count: int = 0


def stable_suffix(*parts: str) -> str:
    joined = "\x00".join(parts)
    return sha256(joined.encode("utf-8")).hexdigest()[:16]


def anomaly_id(rule_key: str, trace_id: str, employee_no: str) -> str:
    return f"anom_{rule_key}_{stable_suffix(rule_key, trace_id, employee_no)}"


def coverage_alert_id(alert_code: str, route_pattern: str, payload_shape_hash: str) -> str:
    return f"cov_{alert_code}_{stable_suffix(alert_code, route_pattern, payload_shape_hash)}"


def window_end_from_start(value: str, seconds: int = 60) -> str:
    if not value:
        return (datetime.now(timezone.utc) + timedelta(seconds=seconds)).isoformat()
    parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    return (parsed.astimezone(timezone.utc) + timedelta(seconds=seconds)).isoformat()


def parse_job(line: str) -> TraceCapturedJob:
    data = json.loads(line)
    known = {field: data[field] for field in TraceCapturedJob.__dataclass_fields__ if field in data}
    return TraceCapturedJob(**known)


def text_hash(value: str) -> str:
    return sha256(value.encode("utf-8")).hexdigest()


def bucket_start_hour(value: str) -> str:
    if not value:
        return datetime.now(timezone.utc).replace(minute=0, second=0, microsecond=0).isoformat()
    parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    return parsed.astimezone(timezone.utc).replace(minute=0, second=0, microsecond=0).isoformat()


def bucket_start_day(value: str) -> str:
    if not value:
        now = datetime.now(timezone.utc)
        return now.replace(hour=0, minute=0, second=0, microsecond=0).isoformat()
    parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    return parsed.astimezone(timezone.utc).replace(hour=0, minute=0, second=0, microsecond=0).isoformat()
