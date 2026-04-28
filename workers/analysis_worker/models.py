import json
from dataclasses import dataclass
from datetime import datetime, timezone
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
